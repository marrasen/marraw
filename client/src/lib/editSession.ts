// editSession is the client-side state machine for non-destructive editing:
// the draft params of the focused photo, its per-photo undo history, the
// live preview (a JPEG Blob pushed over the WebSocket by PreviewEdit), the
// keyboard-focused control, and the commit targets (multi-selection).
//
// It lives outside React so the global keyboard map, the edit panel, and the
// loupe all drive the same state.
import { create } from 'zustand';
import { toast } from 'sonner';
import type { ApiClient } from '@/api/client';
import {
  autoAdjust,
  getEditParams,
  pasteEditParams,
  pickWhiteBalance,
  previewEdit,
  resetEdits,
  setEditParams,
} from '@/api/edits';
import type { Mask, Params } from '@/api/edit';
import { offsetIsAdditive, type AutoPreset, type OffsetKey } from '@/lib/autoPresets';
import {
  CONTROL_ORDER,
  CONTROL_SPECS,
  MASK_TYPE_LABELS,
  NEUTRAL,
  defaultMask,
  paramLabel,
  type ControlId,
} from '@/lib/controlSpecs';
import { updateEditGroupOpen } from '@/lib/uiSettings';
import { useUIStore } from '@/stores/uiStore';

// The control catalog (NEUTRAL params, the ControlId union, CONTROL_SPECS,
// CONTROL_ORDER) lives in lib/controlSpecs — a leaf module shared with the
// toolbar dial catalog (lib/dials) — and is re-exported here so edit-state
// consumers keep a single import.
export { CONTROL_ORDER, CONTROL_SPECS, NEUTRAL } from '@/lib/controlSpecs';
export type { ControlId } from '@/lib/controlSpecs';

// The develop-panel sections (EditPanel's Group components) and which one
// holds each control. Selecting a control opens its group; Ctrl+↑/↓ skips
// controls whose group is closed. Open state lives in uiStore.editGroups
// (absent = open), server-persisted via updateEditGroupOpen.
export type GroupId = 'crop' | 'masks' | 'tone' | 'presence' | 'wb' | 'color' | 'effects' | 'detail';

const CONTROL_GROUP: Record<ControlId, GroupId> = {
  cropAngle: 'crop',
  expEV: 'tone', expPreserve: 'tone', bright: 'tone', gamma: 'tone', shadow: 'tone',
  contrast: 'tone', whites: 'tone', blacks: 'tone', toneShadows: 'tone', toneHighlights: 'tone',
  clarity: 'presence', texture: 'presence', dehaze: 'presence',
  wbMode: 'wb', wbTemp: 'wb', wbKelvin: 'wb', wbTint: 'wb',
  saturation: 'color', vibrance: 'color',
  splitShadowHue: 'color', splitShadowAmt: 'color', splitHighlightHue: 'color', splitHighlightAmt: 'color',
  vignette: 'effects',
  sharpen: 'detail', highlight: 'detail', nrThreshold: 'detail', fbddNoiseRd: 'detail',
  medPasses: 'detail', demosaic: 'detail', caRed: 'detail', caBlue: 'detail',
};

// esMoveActive walks the keyboard focus to the previous/next develop control
// in panel order (Ctrl+↑/↓), skipping controls in closed groups. With nothing
// focused it enters at the end the walk came from; with no open control in
// that direction it stays put.
export function esMoveActive(dir: 1 | -1) {
  const s = useEditSession.getState();
  if (!s.draft) return;
  const groups = useUIStore.getState().editGroups;
  const kelvin = ((s.draft.wbMode as string) || 'camera') === 'kelvin';
  const order = CONTROL_ORDER.filter((c) => (kelvin ? c !== 'wbTemp' : c !== 'wbKelvin'));
  let i = s.activeControl ? order.indexOf(s.activeControl) : -1;
  if (i < 0) i = dir > 0 ? -1 : order.length;
  do {
    i += dir;
  } while (i >= 0 && i < order.length && groups[CONTROL_GROUP[order[i]]] === false);
  // Walking to a control reveals the full drawer (so the moving ring reads),
  // exiting the heads-up +/- adjust.
  if (i >= 0 && i < order.length) setState({ activeControl: order[i], keyAdjust: false });
}

interface Preview {
  photoId: number;
  url: string; // object URL of the preview JPEG
  blob: Blob;
  // Rendered with crop + straighten stripped (the flat full frame crop mode
  // draws its overlay + CSS rotation over). Set from the same `cropping` that
  // built the render params, so the tag can never disagree with the pixels.
  flat: boolean;
}

// One point in a photo's edit timeline: the full params plus a human label
// derived from what changed (so the Presets tab can list "Exposure", "Add
// vignette", "Paste", …) and let the user click back to it.
export interface HistorySnapshot {
  params: Params;
  label: string;
}

interface HistoryEntry {
  stack: HistorySnapshot[];
  index: number;
}

interface EditSessionState {
  photoId: number | null;
  applyIds: number[]; // commit targets; >1 when multiple photos selected
  draft: Params | null;
  // The previous photo's draft, kept through esLoad's null gap so panels can
  // stay rendered (values snap when the new params land) instead of flashing
  // a loading placeholder on every photo switch.
  lastDraft: Params | null;
  loading: boolean;
  history: Record<number, HistoryEntry>;
  activeControl: ControlId | null;
  rendering: number; // in-flight preview renders (task tray indicator)
  preview: Preview | null;
  wbPicking: boolean;
  // Draft snapshot from when the WB eyedropper opened: the revert target for
  // Reset/Cancel. Null when the picker is closed.
  wbPickBase: Params | null;
  cropping: boolean; // crop overlay active: loupe shows the uncropped frame
  // The selected local-adjustment mask (index into draft.masks): its overlay
  // handles show on the loupe and its sliders expand in the Masks group.
  activeMask: number | null;
  // Brush paint mode: pointer strokes on the loupe paint into the active
  // (brush) mask instead of panning.
  maskPaint: boolean;
  // Brush tool settings shared between the Masks panel and the paint overlay.
  // Radius is a fraction of the frame long edge (the server's stroke model).
  brushRadius: number;
  brushFeather: number;
  brushFlow: number;
  brushErase: boolean;
  // Heads-up keyboard adjust: set while +/- is nudging the active control so
  // Develop hides its chrome + drawer and floats a compact bottom readout of
  // just that slider. Cleared by focusing/walking a control, a photo switch,
  // or pointer activity — never by the +/- keydowns themselves, so repeated
  // presses keep the UI hidden ("don't activate if hidden").
  keyAdjust: boolean;
}

export const useEditSession = create<EditSessionState>(() => ({
  photoId: null,
  applyIds: [],
  draft: null,
  lastDraft: null,
  loading: false,
  history: {},
  activeControl: null,
  rendering: 0,
  preview: null,
  wbPicking: false,
  wbPickBase: null,
  cropping: false,
  activeMask: null,
  maskPaint: false,
  brushRadius: 0.05,
  brushFeather: 0.5,
  brushFlow: 1,
  brushErase: false,
  keyAdjust: false,
}));

// Preview renders are coalesced, not debounced: a render fires immediately
// when none is in flight, and while one IS in flight only "a newer state is
// wanted" is remembered — the moment the current render returns, the newest
// draft goes out. The server paces the stream naturally and no fixed latency
// is added to any adjustment. Drag frames render at DRAFT_PX (quarter the
// pixels of 2048, in-memory on the server — fast and cheap, transiently
// upscaled by the loupe); commits and one-shot applies render FULL_PX, which
// the server also persists to the pyramid cache for /img. There is no settle
// timer anywhere: the sharp 2048 is queued immediately behind the instant
// low-res frame, and a further edit ABORTS a stale in-flight 2048 (the abort
// rides the WebSocket as a cancel frame and cancels the server handler's ctx)
// instead of waiting behind it.
const DRAFT_PX = 1024;
const FULL_PX = 2048;
// A render is keyed by the exact params it draws — crop-flattened while
// cropping — plus the flat tag itself: entering crop mode re-renders the same
// params as a different frame (the flat one), so the tag must participate.
type RenderKey = { params: string; flat: boolean };
// The single in-flight render. A full (2048) render is aborted when a newer
// edit supersedes it; in-flight 1024s are never aborted — fold frames are
// fast, land fresher feedback than a restart would, and the server can't
// cancel mid-decode anyway.
let inFlight: { full: boolean; abort: AbortController; key: RenderKey } | null = null;
// What to render next, remembered while a render is in flight. Two slots so
// a step/preset toggle that asks for a drag frame AND a settle in the same
// tick paints the instant 1024 first with the 2048 queued right behind it —
// a single sticky slot would render every held-key step at 2048.
let pending: { low: boolean; full: boolean } | null = null;
// The last FULL render that landed on screen: a settle request for the
// identical frame is skipped (the drag-release commit right after an
// identical settle, most commonly).
let lastShown: { photoId: number; key: RenderKey } | null = null;
let commitTimer = 0;
// Monotonic token guarding the async autoAdjust in esAuto/esApplyAutoPreset:
// a newer apply (or a photo switch) supersedes an in-flight one so a stale
// result can't clobber the draft.
let applyGen = 0;

// In crop mode the loupe draws the rectangle and applies the straighten angle
// as a CSS rotation, both client-side — so the backend renders the flat full
// frame (crop + angle stripped) and the overlay/transform do the rest. The
// draft keeps the real crop and angle for commit.
function flattenedParams(draft: Params, cropping: boolean): Params {
  return cropping ? { ...draft, cropX: 0, cropY: 0, cropW: 0, cropH: 0, cropAngle: 0 } : draft;
}

function keyFor(draft: Params, cropping: boolean): RenderKey {
  return { params: JSON.stringify(flattenedParams(draft, cropping)), flat: cropping };
}

function sameKey(a: RenderKey, b: RenderKey): boolean {
  return a.flat === b.flat && a.params === b.params;
}

// schedulePreview requests a render of the current draft. 'draft' is a drag
// frame: it REPLACES the pending slots (a manual edit supersedes any queued
// settle — the commit at drag end queues a fresh one) and aborts an in-flight
// full render, whose pixels are now stale. 'settle' queues the sharp 2048
// stickily behind whatever is running, aborting an in-flight full only when
// it renders different params.
function schedulePreview(client: ApiClient, kind: 'draft' | 'settle') {
  if (!inFlight) {
    void renderPreview(client, kind === 'settle');
    return;
  }
  if (kind === 'draft') {
    pending = { low: true, full: false };
    if (inFlight.full) inFlight.abort.abort();
  } else {
    pending = { low: pending?.low ?? false, full: true };
    if (inFlight.full) {
      // The supersede comparison must see the freshest slider state — but
      // only this branch pays the flush, so drag frames keep their per-frame
      // draft coalescing.
      esFlushDraft();
      const s = useEditSession.getState();
      if (s.draft && !sameKey(inFlight.key, keyFor(s.draft, s.cropping))) inFlight.abort.abort();
    }
  }
}

function setState(patch: Partial<EditSessionState> | ((s: EditSessionState) => Partial<EditSessionState>)) {
  useEditSession.setState(patch);
}

function sameParams(a: Params, b: Params): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export function esClearPreview() {
  const p = useEditSession.getState().preview;
  if (p) URL.revokeObjectURL(p.url);
  // The sharp frame (if any) is no longer what's displayed, so a future
  // settle for the same params must render, not dedupe-skip.
  lastShown = null;
  setState({ preview: null });
}

// esPreviewSettled reports whether the current preview blob shows nothing
// beyond the committed state: no render in flight or queued, and the draft
// equal to the history head (a drag or a pending esStep commit means the blob
// carries uncommitted pixels the committed renditions don't have yet).
export function esPreviewSettled(): boolean {
  const s = useEditSession.getState();
  if (inFlight || s.rendering > 0 || pending || pendingPatch) return false;
  if (s.photoId == null || !s.draft) return false;
  const h = s.history[s.photoId];
  return !h || sameParams(s.draft, h.stack[h.index].params);
}

// Effect-style controls read naturally as "Add vignette" / "Remove clarity"
// when they move on/off their default; everything else just names the control.
const ADD_REMOVE_LABELS = new Set([
  'Vignette', 'Texture', 'Clarity', 'Dehaze',
  'Split shadow', 'Split highlight', 'Sharpen', 'Noise reduction',
]);

function paramIsDefault(p: Params, key: keyof Params): boolean {
  const v = p[key];
  const d = NEUTRAL[key];
  if (Array.isArray(v) && Array.isArray(d)) return JSON.stringify(v) === JSON.stringify(d);
  return v === d;
}

// maskDiffLabel names a masks-only change: add/remove by count, otherwise by
// what part of the changed mask moved (a brush stroke, the shape, the sliders).
function maskDiffLabel(prev: Mask[] | undefined, next: Mask[] | undefined): string {
  const a = prev ?? [];
  const b = next ?? [];
  if (b.length > a.length) {
    const type = b[b.length - 1]?.type;
    return MASK_TYPE_LABELS[type] ? `Add ${type} mask` : 'Add mask';
  }
  if (b.length < a.length) return 'Remove mask';
  for (let i = 0; i < b.length; i++) {
    if (JSON.stringify(a[i]) === JSON.stringify(b[i])) continue;
    if (JSON.stringify(a[i]?.strokes) !== JSON.stringify(b[i]?.strokes)) return 'Brush stroke';
    if (JSON.stringify(a[i]?.adjust) !== JSON.stringify(b[i]?.adjust)) return 'Adjust mask';
    return 'Move mask';
  }
  return 'Adjust mask';
}

// labelForDiff names a commit from the params that changed between the
// previous history head and the new snapshot: a single control by its label
// (with Add/Remove for effect toggles), a mixed change as "Adjust".
function labelForDiff(prev: Params, next: Params): string {
  const keys = (Object.keys(next) as (keyof Params)[]).filter((k) => {
    const a = prev[k];
    const b = next[k];
    return Array.isArray(a) && Array.isArray(b) ? JSON.stringify(a) !== JSON.stringify(b) : a !== b;
  });
  if (keys.length === 0) return 'Edit';
  if (keys.length === 1 && keys[0] === 'masks') return maskDiffLabel(prev.masks, next.masks);
  const labels = new Set(keys.map((k) => paramLabel(k)));
  if (labels.size !== 1) return 'Adjust';
  const label = [...labels][0];
  if (ADD_REMOVE_LABELS.has(label)) {
    const wasDefault = keys.every((k) => paramIsDefault(prev, k));
    const nowDefault = keys.every((k) => paramIsDefault(next, k));
    if (wasDefault && !nowDefault) return `Add ${label.toLowerCase()}`;
    if (!wasDefault && nowDefault) return `Remove ${label.toLowerCase()}`;
  }
  return label;
}

// esLoad opens an edit session for the newly focused photo.
export async function esLoad(client: ApiClient, photoId: number, applyIds: number[]) {
  window.clearTimeout(commitTimer);
  applyGen++; // supersede any autoAdjust still in flight for the old photo
  pending = null; // BEFORE the abort: its finally must not refire for the old photo
  inFlight?.abort.abort();
  lastShown = null;
  esClearPreview();
  setState((s) => ({
    photoId,
    applyIds,
    draft: null,
    lastDraft: s.draft ?? s.lastDraft,
    loading: true,
    wbPicking: false,
    wbPickBase: null,
    cropping: false,
    activeMask: null,
    maskPaint: false,
    keyAdjust: false,
  }));
  const params = await getEditParams(client, photoId).catch(() => null);
  if (useEditSession.getState().photoId !== photoId) return; // superseded
  const draft = params ?? { ...NEUTRAL };
  setState((s) => {
    const history = s.history[photoId]
      ? s.history
      : { ...s.history, [photoId]: { stack: [{ params: draft, label: 'Original' }], index: 0 } };
    return { draft, loading: false, history };
  });
}

// esSetApplyIds updates commit targets when the selection changes without
// the focused photo changing.
export function esSetApplyIds(ids: number[]) {
  setState({ applyIds: ids });
}

// esSetActive focuses a develop control. A control jumped to by hotkey or
// the command palette may sit in a closed group — open the group first
// (optimistic uiStore write + server persist) so the row mounts, and
// useActiveScroll scrolls to it, with the ring on.
export function esSetActive(client: ApiClient, control: ControlId | null) {
  const group = control ? CONTROL_GROUP[control] : null;
  if (group && useUIStore.getState().editGroups[group] === false) {
    updateEditGroupOpen(client, group, true);
  }
  // Focusing a control (hotkey letter, click, palette) shows the full drawer,
  // ending any heads-up +/- adjust.
  setState({ activeControl: control, keyAdjust: false });
}

// esSetKeyAdjust toggles the heads-up keyboard-adjust mode. The bottom slider
// readout clears it on pointer activity so grabbing the mouse restores the
// full chrome + drawer.
export function esSetKeyAdjust(on: boolean) {
  setState({ keyAdjust: on });
}

// Cull draws no develop control, so a control focused in Develop must not
// survive the switch into it: +/- would keep nudging an invisible slider
// instead of zooming the loupe, and Esc would spend a press clearing a focus
// nobody can see. The hotkeys that focus a control already refuse to fire in
// Cull; this closes the other door. uiStore cannot call in here (it is a
// dependency of this module), so the invariant is enforced from this side.
useUIStore.subscribe((s, prev) => {
  if (s.mode === 'cull' && prev.mode !== 'cull') {
    const es = useEditSession.getState();
    if (es.activeControl != null || es.activeMask != null || es.maskPaint) {
      setState({ activeControl: null, keyAdjust: false, activeMask: null, maskPaint: false });
    }
  }
});

// esSetWBPicking opens/closes the WB eyedropper. Opening snapshots the current
// draft as the revert target (wbPickBase) for Reset/Cancel; closing here is a
// plain dismiss — use esWBPickDone / esWBPickCancel to keep or discard the
// previewed value.
export function esSetWBPicking(on: boolean) {
  const s = useEditSession.getState();
  if (on && !s.draft) return;
  setState(on ? { wbPicking: true, wbPickBase: s.draft } : { wbPicking: false, wbPickBase: null });
}

// esSetCropping toggles the crop overlay. Entering re-renders the preview
// without the crop (the full straightened frame the overlay draws on);
// leaving re-renders the committed crop and persists the draft.
export function esSetCropping(client: ApiClient, on: boolean) {
  const s = useEditSession.getState();
  if (s.cropping === on) return;
  setState({ cropping: on });
  if (!on) {
    esCommit(client); // persist the crop; the commit re-renders the cropped frame
  } else {
    schedulePreview(client, 'settle');
  }
}

// --- Local adjustment masks ---
// Masks live inside the draft (Params.masks) so history, copy/paste and
// persistence handle them for free; these helpers only edit the array and
// drive the same esUpdate/esCommit flow as any slider. Unlike the crop there
// is no client-only preview path — mask changes alter pixels, so shape drags
// render backend draft frames like any adjustment.

// esSetActiveMask selects a mask (its overlay handles + expanded sliders).
// Deselecting leaves paint mode.
export function esSetActiveMask(index: number | null) {
  setState((s) => ({
    activeMask: index,
    maskPaint: index == null ? false : s.maskPaint,
    keyAdjust: false,
  }));
}

// esSetMaskPaint toggles brush paint mode for the active brush mask.
export function esSetMaskPaint(on: boolean) {
  setState({ maskPaint: on });
}

// esSetBrushTool updates the shared brush tool settings (radius/feather/flow/
// erase) used for the next stroke.
export function esSetBrushTool(
  patch: Partial<Pick<EditSessionState, 'brushRadius' | 'brushFeather' | 'brushFlow' | 'brushErase'>>,
) {
  setState(patch);
}

// esAddMask appends a mask with a sensible default shape, selects it, and
// commits ("Add radial mask" in history). A brush starts empty and drops the
// session straight into paint mode. Also opens the Masks panel group so the
// new mask's sliders are visible.
export function esAddMask(client: ApiClient, type: Mask['type']) {
  esFlushDraft();
  const s = useEditSession.getState();
  if (!s.draft || s.photoId == null) return;
  const masks = [...(s.draft.masks ?? []), defaultMask(type)];
  if (useUIStore.getState().editGroups.masks === false) {
    updateEditGroupOpen(client, 'masks', true);
  }
  setState({ activeMask: masks.length - 1, maskPaint: type === 'brush' });
  esCommit(client, { masks });
}

// esUpdateMask merges a patch into one mask during an overlay drag or slider
// move (coalesced low-res preview; commit on release). Flushes first so
// back-to-back updates within one frame don't clobber each other — the patch
// value is the whole masks array.
export function esUpdateMask(client: ApiClient, index: number, patch: Partial<Mask>) {
  esFlushDraft();
  const s = useEditSession.getState();
  const masks = s.draft?.masks;
  if (!masks || !masks[index]) return;
  const next = masks.slice();
  next[index] = { ...next[index], ...patch };
  esUpdate(client, { masks: next });
}

// esRemoveMask deletes a mask and commits.
export function esRemoveMask(client: ApiClient, index: number) {
  esFlushDraft();
  const s = useEditSession.getState();
  const masks = s.draft?.masks;
  if (!masks || !masks[index]) return;
  const next = masks.filter((_, i) => i !== index);
  setState({ activeMask: null, maskPaint: false });
  esCommit(client, { masks: next });
}

const CROP_RECT_KEYS = ['cropX', 'cropY', 'cropW', 'cropH'] as const;
// Params the crop overlay previews entirely client-side while cropping: the
// rectangle (drawn by the overlay) and the straighten angle (a CSS rotation
// of the flat frame). Changing any of these needs no backend render.
const CROP_LIVE_KEYS = [...CROP_RECT_KEYS, 'cropAngle'] as const;

// Draft writes during a slider drag are coalesced to one per animation frame:
// the develop panel has ~30 controls, so applying every pointer-move
// synchronously re-rendered all of them (plus the loupe and histogram) and
// made dragging stutter. flushDraft merges the pending patch once per frame.
let pendingPatch: Partial<Params> | null = null;
let draftRaf = 0;
function flushDraft() {
  draftRaf = 0;
  const patch = pendingPatch;
  pendingPatch = null;
  if (!patch) return;
  const s = useEditSession.getState();
  if (s.draft) setState({ draft: { ...s.draft, ...patch } });
}
// esFlushDraft applies any frame-pending patch immediately — call before
// reading the draft for a commit so nothing in flight is lost.
export function esFlushDraft() {
  if (draftRaf) {
    cancelAnimationFrame(draftRaf);
    flushDraft();
  }
}

// esUpdate changes the draft (coalesced to a frame) and schedules a low-res
// live preview render (coalesced against the in-flight one).
export function esUpdate(client: ApiClient, patch: Partial<Params>) {
  const s = useEditSession.getState();
  if (!s.draft || s.photoId == null) return;
  pendingPatch = { ...(pendingPatch ?? {}), ...patch };
  if (!draftRaf) draftRaf = requestAnimationFrame(flushDraft);
  // While cropping, the crop rectangle and straighten angle are previewed
  // client-side (overlay + CSS rotation), so they need no backend render.
  if (s.cropping && Object.keys(patch).every((k) => (CROP_LIVE_KEYS as readonly string[]).includes(k))) {
    return;
  }
  schedulePreview(client, 'draft');
}

async function renderPreview(client: ApiClient, full: boolean) {
  esFlushDraft(); // render the freshest slider state, not last frame's
  const { photoId, draft, cropping } = useEditSession.getState();
  if (photoId == null || !draft) return;
  const renderParams = flattenedParams(draft, cropping);
  const key = keyFor(draft, cropping);
  // The identical sharp frame already landed (and ensurePreview wrote it to
  // the pyramid cache) — nothing to render. Most commonly the drag-release
  // commit right after an identical settle.
  if (full && lastShown && lastShown.photoId === photoId && sameKey(lastShown.key, key)) return;
  const ac = new AbortController();
  inFlight = { full, abort: ac, key };
  setState((s) => ({ rendering: s.rendering + 1 }));
  try {
    const blob = await previewEdit(client, photoId, renderParams, full ? FULL_PX : DRAFT_PX, {
      signal: ac.signal,
    });
    if (useEditSession.getState().photoId !== photoId || ac.signal.aborted) return;
    const url = URL.createObjectURL(blob);
    const old = useEditSession.getState().preview;
    if (old) URL.revokeObjectURL(old.url);
    setState({ preview: { photoId, url, blob, flat: cropping } });
    // lastShown means "the displayed blob IS this sharp frame" — a low-res
    // frame replacing it on screen must clear it, or returning to the exact
    // same params would dedupe-skip the settle and leave the soft 1024 up.
    lastShown = full ? { photoId, key } : null;
  } catch {
    // aborted or superseded
  } finally {
    inFlight = null;
    // Fire the queued state now — even when this render was aborted: the
    // abort-on-supersede path wants its replacement immediately, and a photo
    // switch cleared `pending` before aborting so it never refires here. The
    // low frame goes first for instant feedback, keeping the settle queued
    // behind it. Refire BEFORE decrementing so `rendering` never touches 0
    // mid-handoff and esPreviewSettled stays false throughout.
    const p = pending;
    if (p) {
      pending = p.low && p.full ? { low: false, full: true } : null;
      void renderPreview(client, !p.low);
    }
    setState((s) => ({ rendering: Math.max(0, s.rendering - 1) }));
  }
}

function pushHistory(photoId: number, params: Params, label: string) {
  setState((s) => {
    const entry = s.history[photoId] ?? { stack: [{ params: { ...NEUTRAL }, label: 'Original' }], index: 0 };
    if (sameParams(entry.stack[entry.index].params, params)) return {};
    const stack = [...entry.stack.slice(0, entry.index + 1), { params, label }].slice(-50);
    return { history: { ...s.history, [photoId]: { stack, index: stack.length - 1 } } };
  });
}

function persist(client: ApiClient, params: Params, ids: number[]) {
  const p =
    ids.length > 1
      ? pasteEditParams(client, ids, params)
      : setEditParams(client, ids[0], params);
  p.catch((err) => toast.error(`Save failed: ${(err as Error).message}`));
}

// esCommit persists the draft (merged with an optional final patch) to every
// photo in the selection and records it in the undo history.
export function esCommit(client: ApiClient, patch?: Partial<Params>) {
  esFlushDraft(); // apply any frame-pending slider move before snapshotting
  const s = useEditSession.getState();
  if (!s.draft || s.photoId == null) return;
  const params = patch ? { ...s.draft, ...patch } : s.draft;
  if (patch) setState({ draft: params });
  const h = s.history[s.photoId];
  const prev = h ? h.stack[h.index].params : { ...NEUTRAL };
  pushHistory(s.photoId, params, labelForDiff(prev, params));
  const ids = s.applyIds.length > 1 ? s.applyIds : [s.photoId];
  persist(client, params, ids);
  // Settle render: drag frames were low-res, so bring the loupe back to the
  // full 2048 (which the server also writes to the pyramid cache).
  schedulePreview(client, 'settle');
}

// esApplyParams replaces the whole draft (paste, picker result, undo) with
// immediate preview + persist.
export function esApplyParams(
  client: ApiClient,
  params: Params,
  opts?: { skipHistory?: boolean; label?: string },
) {
  const s = useEditSession.getState();
  if (s.photoId == null) return;
  setState({ draft: params });
  if (!opts?.skipHistory) pushHistory(s.photoId, params, opts?.label ?? 'Edit');
  schedulePreview(client, 'settle');
  const ids = s.applyIds.length > 1 ? s.applyIds : [s.photoId];
  persist(client, params, ids);
}

// esApplyParamsPreview is esApplyParams for one-shot auto/preset applies: it
// records history and persists immediately (a discrete, undoable action) but
// paints a low-res preview now with the full-res settle queued right behind.
// Rapid toggling stays cheap without any timer: each re-trigger's low-res
// request replaces the queued settle and aborts a stale in-flight 2048, so
// the sharp frame lands right after the last toggle.
function esApplyParamsPreview(client: ApiClient, params: Params, label: string) {
  const s = useEditSession.getState();
  if (s.photoId == null) return;
  setState({ draft: params });
  pushHistory(s.photoId, params, label);
  const ids = s.applyIds.length > 1 ? s.applyIds : [s.photoId];
  persist(client, params, ids);
  previewThenSettle(client);
}

// previewThenSettle paints an instant low-res frame with the sharp 2048
// queued immediately behind it (no timer — a re-trigger aborts/replaces the
// stale settle instead). The pending low/full slots keep the loupe reporting
// unsettled until the 2048 lands. Does NOT touch the draft, history, or
// persistence — callers own that.
function previewThenSettle(client: ApiClient) {
  schedulePreview(client, 'draft'); // instant low-res, supersedes a stale settle
  schedulePreview(client, 'settle');
}

export function esCanUndo(s: EditSessionState): boolean {
  if (s.photoId == null) return false;
  const h = s.history[s.photoId];
  return !!h && h.index > 0;
}

export function esCanRedo(s: EditSessionState): boolean {
  if (s.photoId == null) return false;
  const h = s.history[s.photoId];
  return !!h && h.index < h.stack.length - 1;
}

// esUndo/esRedo walk the focused photo's history. They persist to the
// focused photo only — history is per image.
export function esUndo(client: ApiClient) {
  moveHistory(client, -1);
}

export function esRedo(client: ApiClient) {
  moveHistory(client, +1);
}

function moveHistory(client: ApiClient, dir: number) {
  const s = useEditSession.getState();
  if (s.photoId == null) return;
  const h = s.history[s.photoId];
  if (!h) return;
  esJumpTo(client, h.index + dir);
}

// esJumpTo moves the focused photo's history to an absolute index (Undo/Redo
// go through it via ±1, the Presets history list clicks straight to any
// point). Persists to the focused photo only — history is per image.
export function esJumpTo(client: ApiClient, index: number) {
  const s = useEditSession.getState();
  if (s.photoId == null) return;
  const h = s.history[s.photoId];
  if (!h) return;
  if (index < 0 || index >= h.stack.length || index === h.index) return;
  const params = h.stack[index].params;
  setState({
    draft: params,
    history: { ...s.history, [s.photoId]: { ...h, index } },
  });
  schedulePreview(client, 'settle');
  setEditParams(client, s.photoId, params).catch((err) =>
    toast.error(`Save failed: ${(err as Error).message}`),
  );
}

// esHistory reads the focused photo's timeline for the Presets history list:
// the labeled snapshots and the current index, or null when nothing is loaded.
export function esHistory(s: EditSessionState): { entries: HistorySnapshot[]; index: number } | null {
  if (s.photoId == null) return null;
  const h = s.history[s.photoId];
  return h ? { entries: h.stack, index: h.index } : null;
}

// esStep adjusts the active (or given) control from the keyboard: +/- steps
// numeric controls and cycles enum controls. Every step renders the instant
// low-res frame with the sharp settle queued right behind it (the next step
// aborts a stale in-flight settle); only the persist + history entry waits
// for a short idle, so a run of nudges lands as one undoable commit.
export function esStep(client: ApiClient, control: ControlId, dir: 1 | -1, big = false) {
  const s = useEditSession.getState();
  if (!s.draft) return;
  // A +/- nudge hides Develop's chrome/drawer and floats the compact readout.
  if (!s.keyAdjust) setState({ keyAdjust: true });
  const spec = CONTROL_SPECS[control];
  let patch: Partial<Params>;
  if (spec.kind === 'numeric') {
    const step = big ? spec.bigStep : spec.step;
    const raw = spec.get(s.draft) + dir * step;
    const v = Math.min(spec.max, Math.max(spec.min, Math.round(raw * 1000) / 1000));
    patch = spec.set(v);
  } else {
    const cur = spec.values.indexOf(spec.get(s.draft));
    const next = spec.values[(cur + dir + spec.values.length) % spec.values.length];
    patch = spec.set(next);
  }
  esUpdate(client, patch);
  esFlushDraft(); // a discrete key step should land in the draft immediately
  schedulePreview(client, 'settle'); // sharp frame right behind the instant one
  window.clearTimeout(commitTimer);
  commitTimer = window.setTimeout(() => esCommit(client), 600);
}

// esReset clears the edit state of every photo in the selection, then
// reloads the clean state — which the server may seed with the photo's
// camera-mimic compensation (exposure dial back at e.g. +1.3 EV, not 0).
export function esReset(client: ApiClient) {
  const s = useEditSession.getState();
  if (s.photoId == null) return;
  const photoId = s.photoId;
  // Same order as esLoad: clear pending BEFORE aborting so the finally does
  // not refire, then abort so an in-flight pre-reset render can't land a
  // stale blob after the clear below (the photoId guard alone won't catch
  // it — the photo hasn't changed).
  pending = null;
  inFlight?.abort.abort();
  setState({ draft: { ...NEUTRAL } });
  esClearPreview();
  const ids = s.applyIds.length > 1 ? s.applyIds : [photoId];
  resetEdits(client, ids)
    .then(async () => {
      const params = await getEditParams(client, photoId).catch(() => null);
      if (useEditSession.getState().photoId !== photoId) return;
      const draft = params ?? { ...NEUTRAL };
      setState({ draft });
      pushHistory(photoId, draft, 'Reset');
    })
    .catch((err) => toast.error((err as Error).message));
}

// Sections the backend's AutoAdjust can compute; 'all' expands server-side.
export type AutoSection = 'tone' | 'wb' | 'color';

// esAuto asks the backend to compute auto values for the given sections of the
// focused photo and applies the merged result with an instant low-res preview
// and the full-res settle queued behind it (esApplyParamsPreview) so it stays
// snappy to re-trigger. On a multi-selection the focused photo's auto result
// applies to all targets — the same semantics as paste and the WB picker.
export async function esAuto(client: ApiClient, sections: (AutoSection | 'all')[]) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft) return;
  esFlushDraft();
  const gen = ++applyGen;
  const pid = s.photoId;
  try {
    const params = await autoAdjust(client, pid, useEditSession.getState().draft!, sections);
    if (applyGen !== gen || useEditSession.getState().photoId !== pid) return; // superseded
    esApplyParamsPreview(client, params, 'Auto');
  } catch (err) {
    toast.error(`Auto adjust failed: ${(err as Error).message}`);
  }
}

// esApplyAutoPreset runs a creative auto: the preset's auto sections first
// (skipped when empty — an offsets-only preset), then its style offsets on
// top, clamped to the control ranges. One history entry, one persist, with an
// instant low-res preview and the full-res settle queued right behind so
// toggling between presets stays responsive (esApplyParamsPreview).
export async function esApplyAutoPreset(client: ApiClient, preset: AutoPreset) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft) return;
  esFlushDraft();
  const gen = ++applyGen;
  const pid = s.photoId;
  const base = useEditSession.getState().draft!;
  try {
    const out = await computePresetParams(client, pid, base, preset);
    if (applyGen !== gen || useEditSession.getState().photoId !== pid) return; // superseded
    esApplyParamsPreview(client, out, preset.name);
  } catch (err) {
    toast.error(`Auto adjust failed: ${(err as Error).message}`);
  }
}

// computePresetParams resolves a creative-auto preset to concrete params for a
// photo without touching edit-session state: the preset's auto sections first
// (skipped when empty — an offsets-only preset), then its style offsets on top,
// clamped to the control ranges. Shared by esApplyAutoPreset (apply) and the
// Presets-tab thumbnail renders (preview a preset before committing).
export async function computePresetParams(
  client: ApiClient,
  photoId: number,
  base: Params,
  preset: AutoPreset,
): Promise<Params> {
  let resolved = base;
  if (preset.sections.length > 0) {
    resolved = await autoAdjust(client, photoId, base, preset.sections);
  }
  const out = { ...resolved };
  // Offset keys are numeric params (autoPresets.ts). A key covered by an
  // active auto section lands as a delta on top of the computed value;
  // anything else (creative sliders, or a section that's off) is written as
  // an absolute value — 0 included, so it can force the field to 0.
  const fields = out as unknown as Record<string, number>;
  for (const [key, val] of Object.entries(preset.offsets)) {
    const spec = CONTROL_SPECS[key as ControlId];
    if (!spec || spec.kind !== 'numeric' || typeof val !== 'number') continue;
    const v = offsetIsAdditive(key as OffsetKey, preset.sections) ? fields[key] + val : val;
    fields[key] = Math.min(spec.max, Math.max(spec.min, Math.round(v * 100) / 100));
  }
  return out;
}

// esPickWB samples the clicked spot and PREVIEWS the resulting custom WB — the
// draft updates but nothing is committed until Done. The backend reads the
// camera's scene-linear colour, so the same pixel always yields the same
// balance; the preview folds off the cached decode (no re-demosaic). The
// eyedropper stays open for repeated sampling; Done keeps the value, Reset /
// Cancel restore the pre-picker draft.
export async function esPickWB(client: ApiClient, x: number, y: number) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft) return;
  try {
    const params = await pickWhiteBalance(client, s.photoId, s.draft, x, y);
    const cur = useEditSession.getState();
    if (cur.photoId !== s.photoId || !cur.wbPicking) return; // superseded / closed
    setState({ draft: params });
    // Low-res only, no 2048 settle: the fast fold and the exact 2048 render
    // WB slightly differently, so settling on every click flashed the balance
    // twice and made picks impossible to compare. The 'draft' flavor replaces
    // any queued settle (from a preceding As-shot/Auto/Reset), so each click
    // shows one consistent fold frame; Done renders the exact 2048 once.
    schedulePreview(client, 'draft');
  } catch (err) {
    toast.error(`White balance pick failed: ${(err as Error).message}`);
  }
}

// esWBPickDone commits the previewed WB as a single history entry and closes
// the picker. A pick that changed nothing from the base just closes.
export function esWBPickDone(client: ApiClient) {
  const s = useEditSession.getState();
  const base = s.wbPickBase;
  setState({ wbPicking: false, wbPickBase: null });
  if (s.photoId == null || !s.draft) return;
  if (base && sameParams(base, s.draft)) {
    schedulePreview(client, 'settle'); // land a sharp frame, no history churn
    return;
  }
  esApplyParams(client, s.draft, { label: 'White balance' });
}

// esWBPickCancel restores the pre-picker draft and closes the picker.
export function esWBPickCancel(client: ApiClient) {
  const s = useEditSession.getState();
  const base = s.wbPickBase;
  setState({ wbPicking: false, wbPickBase: null });
  if (base && s.draft && !sameParams(base, s.draft)) {
    setState({ draft: base });
    schedulePreview(client, 'settle');
  }
}

// esWBPickReset restores the pre-picker draft but keeps the eyedropper open.
export function esWBPickReset(client: ApiClient) {
  const s = useEditSession.getState();
  const base = s.wbPickBase;
  if (!base || !s.draft || sameParams(base, s.draft)) return;
  setState({ draft: base });
  previewThenSettle(client);
}

// esWBPickAsShot / esWBPickAuto set the draft's WB to the camera as-shot or
// auto value, previewed with the eyedropper still open (not committed).
export function esWBPickAsShot(client: ApiClient) {
  wbPickSetMode(client, 'camera');
}
export function esWBPickAuto(client: ApiClient) {
  wbPickSetMode(client, 'auto');
}
function wbPickSetMode(client: ApiClient, wbMode: Params['wbMode']) {
  const s = useEditSession.getState();
  if (!s.draft) return;
  setState({
    draft: { ...s.draft, wbMode, wbMul: NEUTRAL.wbMul, wbTemp: 0, wbTint: 0, wbKelvin: 0 },
  });
  previewThenSettle(client);
}
