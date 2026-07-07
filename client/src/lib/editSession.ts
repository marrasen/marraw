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
  type Params,
} from '@/api/edits';
import type { AutoPreset } from '@/lib/autoPresets';
import { CONTROL_ORDER, CONTROL_SPECS, NEUTRAL, type ControlId } from '@/lib/controlSpecs';
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
export type GroupId = 'crop' | 'tone' | 'presence' | 'wb' | 'color' | 'effects' | 'detail';

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
  if (i >= 0 && i < order.length) setState({ activeControl: order[i] });
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

interface HistoryEntry {
  stack: Params[];
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
  cropping: boolean; // crop overlay active: loupe shows the uncropped frame
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
  cropping: false,
}));

// Preview renders are coalesced, not debounced: a render fires immediately
// when none is in flight, and while one IS in flight only "a newer state is
// wanted" is remembered — the moment the current render returns, the newest
// draft goes out. The server paces the stream naturally and no fixed latency
// is added to any adjustment. Drag frames render at DRAFT_PX (quarter the
// pixels of 2048, in-memory on the server — fast and cheap, transiently
// upscaled by the loupe); commits and one-shot applies render FULL_PX, which
// the server also persists to the pyramid cache for /img.
const DRAFT_PX = 1024;
const FULL_PX = 2048;
let previewInFlight = false;
let previewPending: { full: boolean } | null = null;
let previewAbort: AbortController | null = null;
let commitTimer = 0;

function schedulePreview(client: ApiClient, full: boolean) {
  if (previewInFlight) {
    previewPending = { full: (previewPending?.full ?? false) || full };
    return;
  }
  void renderPreview(client, full);
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
  setState({ preview: null });
}

// esPreviewSettled reports whether the current preview blob shows nothing
// beyond the committed state: no render in flight or queued, and the draft
// equal to the history head (a drag or a pending esStep commit means the blob
// carries uncommitted pixels the committed renditions don't have yet).
export function esPreviewSettled(): boolean {
  const s = useEditSession.getState();
  if (s.rendering > 0 || previewPending || pendingPatch) return false;
  if (s.photoId == null || !s.draft) return false;
  const h = s.history[s.photoId];
  return !h || sameParams(s.draft, h.stack[h.index]);
}

// esLoad opens an edit session for the newly focused photo.
export async function esLoad(client: ApiClient, photoId: number, applyIds: number[]) {
  window.clearTimeout(commitTimer);
  previewPending = null;
  previewAbort?.abort();
  esClearPreview();
  setState((s) => ({
    photoId,
    applyIds,
    draft: null,
    lastDraft: s.draft ?? s.lastDraft,
    loading: true,
    wbPicking: false,
    cropping: false,
  }));
  const params = await getEditParams(client, photoId).catch(() => null);
  if (useEditSession.getState().photoId !== photoId) return; // superseded
  const draft = params ?? { ...NEUTRAL };
  setState((s) => {
    const history = s.history[photoId] ? s.history : { ...s.history, [photoId]: { stack: [draft], index: 0 } };
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
  setState({ activeControl: control });
}

export function esSetWBPicking(on: boolean) {
  setState({ wbPicking: on });
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
    schedulePreview(client, true);
  }
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
  schedulePreview(client, false);
}

async function renderPreview(client: ApiClient, full: boolean) {
  esFlushDraft(); // render the freshest slider state, not last frame's
  const { photoId, draft, cropping } = useEditSession.getState();
  if (photoId == null || !draft) return;
  previewInFlight = true;
  const ac = new AbortController();
  previewAbort = ac;
  setState((s) => ({ rendering: s.rendering + 1 }));
  // In crop mode the loupe draws the rectangle and applies the straighten
  // angle as a CSS rotation, both client-side — so render the flat full frame
  // (crop + angle stripped) once and let the overlay/transform do the rest.
  // The draft keeps the real crop and angle for commit.
  const renderParams = cropping
    ? { ...draft, cropX: 0, cropY: 0, cropW: 0, cropH: 0, cropAngle: 0 }
    : draft;
  try {
    const blob = await previewEdit(client, photoId, renderParams, full ? FULL_PX : DRAFT_PX, {
      signal: ac.signal,
    });
    if (useEditSession.getState().photoId !== photoId || ac.signal.aborted) return;
    const url = URL.createObjectURL(blob);
    const old = useEditSession.getState().preview;
    if (old) URL.revokeObjectURL(old.url);
    setState({ preview: { photoId, url, blob, flat: cropping } });
  } catch {
    // aborted or superseded
  } finally {
    previewInFlight = false;
    setState((s) => ({ rendering: Math.max(0, s.rendering - 1) }));
    const pending = previewPending;
    previewPending = null;
    // A newer state arrived while rendering — fire it now (unless the whole
    // session moved on and aborted us).
    if (pending && !ac.signal.aborted) void renderPreview(client, pending.full);
  }
}

function pushHistory(photoId: number, params: Params) {
  setState((s) => {
    const entry = s.history[photoId] ?? { stack: [{ ...NEUTRAL }], index: 0 };
    if (sameParams(entry.stack[entry.index], params)) return {};
    const stack = [...entry.stack.slice(0, entry.index + 1), params].slice(-50);
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
  pushHistory(s.photoId, params);
  const ids = s.applyIds.length > 1 ? s.applyIds : [s.photoId];
  persist(client, params, ids);
  // Settle render: drag frames were low-res, so bring the loupe back to the
  // full 2048 (which the server also writes to the pyramid cache).
  schedulePreview(client, true);
}

// esApplyParams replaces the whole draft (paste, picker result, undo) with
// immediate preview + persist.
export function esApplyParams(client: ApiClient, params: Params, opts?: { skipHistory?: boolean }) {
  const s = useEditSession.getState();
  if (s.photoId == null) return;
  setState({ draft: params });
  if (!opts?.skipHistory) pushHistory(s.photoId, params);
  schedulePreview(client, true);
  const ids = s.applyIds.length > 1 ? s.applyIds : [s.photoId];
  persist(client, params, ids);
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
  const index = h.index + dir;
  if (index < 0 || index >= h.stack.length) return;
  const params = h.stack[index];
  setState({
    draft: params,
    history: { ...s.history, [s.photoId]: { ...h, index } },
  });
  schedulePreview(client, true);
  setEditParams(client, s.photoId, params).catch((err) =>
    toast.error(`Save failed: ${(err as Error).message}`),
  );
}

// esStep adjusts the active (or given) control from the keyboard: +/- steps
// numeric controls and cycles enum controls. Commits after a short idle.
export function esStep(client: ApiClient, control: ControlId, dir: 1 | -1, big = false) {
  const s = useEditSession.getState();
  if (!s.draft) return;
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
  setState({ draft: { ...NEUTRAL } });
  esClearPreview();
  const ids = s.applyIds.length > 1 ? s.applyIds : [photoId];
  resetEdits(client, ids)
    .then(async () => {
      const params = await getEditParams(client, photoId).catch(() => null);
      if (useEditSession.getState().photoId !== photoId) return;
      const draft = params ?? { ...NEUTRAL };
      setState({ draft });
      pushHistory(photoId, draft);
    })
    .catch((err) => toast.error((err as Error).message));
}

// Sections the backend's AutoAdjust can compute; 'all' expands server-side.
export type AutoSection = 'tone' | 'wb' | 'color';

// esAuto asks the backend to compute auto values for the given sections of
// the focused photo and applies the merged result (preview + persist + undo
// via esApplyParams). On a multi-selection the focused photo's auto result
// applies to all targets — the same semantics as paste and the WB picker.
export async function esAuto(client: ApiClient, sections: (AutoSection | 'all')[]) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft) return;
  esFlushDraft();
  try {
    const params = await autoAdjust(client, s.photoId, useEditSession.getState().draft!, sections);
    esApplyParams(client, params);
  } catch (err) {
    toast.error(`Auto adjust failed: ${(err as Error).message}`);
  }
}

// esApplyAutoPreset runs a creative auto: the preset's auto sections first
// (skipped when empty — an offsets-only preset), then its style offsets on
// top, clamped to the control ranges. One history entry, one persist.
export async function esApplyAutoPreset(client: ApiClient, preset: AutoPreset) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft) return;
  esFlushDraft();
  let base = useEditSession.getState().draft!;
  if (preset.sections.length > 0) {
    try {
      base = await autoAdjust(client, s.photoId, base, preset.sections);
    } catch (err) {
      toast.error(`Auto adjust failed: ${(err as Error).message}`);
      return;
    }
  }
  const out = { ...base };
  // Offset keys are restricted to direct zero-neutral numeric params
  // (autoPresets.ts), so plain field addition is safe here.
  const fields = out as unknown as Record<string, number>;
  for (const [key, delta] of Object.entries(preset.offsets)) {
    const spec = CONTROL_SPECS[key as ControlId];
    if (!spec || spec.kind !== 'numeric' || typeof delta !== 'number') continue;
    const v = fields[key] + delta;
    fields[key] = Math.min(spec.max, Math.max(spec.min, Math.round(v * 100) / 100));
  }
  esApplyParams(client, out);
}

// esPickWB asks the backend to compute custom multipliers that neutralize
// the clicked spot (relative coordinates in the displayed image).
export async function esPickWB(client: ApiClient, x: number, y: number) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft) return;
  setState({ wbPicking: false });
  try {
    const params = await pickWhiteBalance(client, s.photoId, s.draft, x, y);
    esApplyParams(client, params);
  } catch (err) {
    toast.error(`White balance pick failed: ${(err as Error).message}`);
  }
}
