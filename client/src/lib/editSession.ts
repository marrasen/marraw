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

export const NEUTRAL: Params = {
  expEV: 0,
  expPreserve: 0,
  wbMode: 'camera',
  wbMul: [0, 0, 0, 0],
  wbTemp: 0,
  wbTint: 0,
  wbKelvin: 0,
  bright: 0,
  gamma: 0,
  shadow: 0,
  highlight: 0,
  nrThreshold: 0,
  fbddNoiseRd: 0,
  medPasses: 0,
  contrast: 0,
  whites: 0,
  blacks: 0,
  toneShadows: 0,
  toneHighlights: 0,
  saturation: 0,
  vibrance: 0,
  splitShadowHue: 0,
  splitShadowAmt: 0,
  splitHighlightHue: 0,
  splitHighlightAmt: 0,
  vignette: 0,
  texture: 0,
  clarity: 0,
  dehaze: 0,
  sharpen: 0,
  // The server stores the default as "" — same generated-union lie as wbMode.
  demosaic: '' as Params['demosaic'],
  caRed: 0,
  caBlue: 0,
  cropX: 0,
  cropY: 0,
  cropW: 0,
  cropH: 0,
  cropAngle: 0,
};

// Controls addressable from the keyboard. Numeric controls step with +/-;
// enum controls cycle.
export type ControlId =
  | 'expEV'
  | 'expPreserve'
  | 'bright'
  | 'gamma'
  | 'shadow'
  | 'contrast'
  | 'whites'
  | 'blacks'
  | 'toneShadows'
  | 'toneHighlights'
  | 'wbMode'
  | 'wbTemp'
  | 'wbTint'
  | 'wbKelvin'
  | 'highlight'
  | 'saturation'
  | 'vibrance'
  | 'splitShadowHue'
  | 'splitShadowAmt'
  | 'splitHighlightHue'
  | 'splitHighlightAmt'
  | 'vignette'
  | 'texture'
  | 'clarity'
  | 'dehaze'
  | 'sharpen'
  | 'nrThreshold'
  | 'fbddNoiseRd'
  | 'medPasses'
  | 'demosaic'
  | 'caRed'
  | 'caBlue'
  | 'cropAngle';

interface NumericSpec {
  kind: 'numeric';
  min: number;
  max: number;
  step: number;
  bigStep: number;
  get: (p: Params) => number;
  set: (v: number) => Partial<Params>;
}
interface CycleSpec {
  kind: 'cycle';
  values: (string | number)[];
  get: (p: Params) => string | number;
  set: (v: string | number) => Partial<Params>;
}
type ControlSpec = NumericSpec | CycleSpec;

export const CONTROL_SPECS: Record<ControlId, ControlSpec> = {
  expEV: { kind: 'numeric', min: -2, max: 3, step: 0.05, bigStep: 0.25, get: (p) => p.expEV, set: (v) => ({ expEV: v }) },
  expPreserve: { kind: 'numeric', min: 0, max: 1, step: 0.05, bigStep: 0.2, get: (p) => p.expPreserve, set: (v) => ({ expPreserve: v }) },
  bright: {
    kind: 'numeric', min: 0.25, max: 4, step: 0.05, bigStep: 0.25,
    get: (p) => (p.bright === 0 ? 1 : p.bright),
    set: (v) => ({ bright: v }),
  },
  gamma: {
    kind: 'numeric', min: 1, max: 3.5, step: 0.05, bigStep: 0.25,
    get: (p) => (p.gamma === 0 ? 2.222 : p.gamma),
    set: (v) => ({ gamma: v }),
  },
  shadow: {
    kind: 'numeric', min: 1, max: 12, step: 0.5, bigStep: 1.5,
    get: (p) => (p.shadow === 0 ? 4.5 : p.shadow),
    set: (v) => ({ shadow: v }),
  },
  contrast: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.contrast, set: (v) => ({ contrast: v }) },
  whites: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.whites, set: (v) => ({ whites: v }) },
  blacks: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.blacks, set: (v) => ({ blacks: v }) },
  toneShadows: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.toneShadows, set: (v) => ({ toneShadows: v }) },
  toneHighlights: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.toneHighlights, set: (v) => ({ toneHighlights: v }) },
  wbMode: {
    kind: 'cycle', values: ['camera', 'auto', 'kelvin'],
    // The server normalizes "camera" (the default) to "" in stored params.
    get: (p) => (p.wbMode as string) || 'camera',
    set: (v) =>
      v === 'kelvin'
        ? { wbMode: 'kelvin', wbKelvin: 5500, wbMul: [0, 0, 0, 0] }
        : { wbMode: v as Params['wbMode'], wbKelvin: 0, wbMul: [0, 0, 0, 0] },
  },
  wbTemp: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.wbTemp, set: (v) => ({ wbTemp: v }) },
  wbTint: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.wbTint, set: (v) => ({ wbTint: v }) },
  wbKelvin: {
    kind: 'numeric', min: 2000, max: 12000, step: 50, bigStep: 250,
    get: (p) => (p.wbKelvin === 0 ? 5500 : p.wbKelvin),
    // Stepping the Kelvin control switches into kelvin mode.
    set: (v) => ({ wbMode: 'kelvin', wbKelvin: v }),
  },
  saturation: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.saturation, set: (v) => ({ saturation: v }) },
  vibrance: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.vibrance, set: (v) => ({ vibrance: v }) },
  splitShadowHue: { kind: 'numeric', min: 0, max: 359, step: 5, bigStep: 30, get: (p) => p.splitShadowHue, set: (v) => ({ splitShadowHue: v }) },
  splitShadowAmt: { kind: 'numeric', min: 0, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.splitShadowAmt, set: (v) => ({ splitShadowAmt: v }) },
  splitHighlightHue: { kind: 'numeric', min: 0, max: 359, step: 5, bigStep: 30, get: (p) => p.splitHighlightHue, set: (v) => ({ splitHighlightHue: v }) },
  splitHighlightAmt: { kind: 'numeric', min: 0, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.splitHighlightAmt, set: (v) => ({ splitHighlightAmt: v }) },
  vignette: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.vignette, set: (v) => ({ vignette: v }) },
  texture: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.texture, set: (v) => ({ texture: v }) },
  clarity: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.clarity, set: (v) => ({ clarity: v }) },
  dehaze: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.dehaze, set: (v) => ({ dehaze: v }) },
  sharpen: { kind: 'numeric', min: 0, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.sharpen, set: (v) => ({ sharpen: v }) },
  highlight: {
    kind: 'cycle', values: [0, 1, 2, 5],
    get: (p) => p.highlight,
    set: (v) => ({ highlight: v as number }),
  },
  nrThreshold: { kind: 'numeric', min: 0, max: 1000, step: 25, bigStep: 100, get: (p) => p.nrThreshold, set: (v) => ({ nrThreshold: v }) },
  fbddNoiseRd: {
    kind: 'cycle', values: [0, 1, 2],
    get: (p) => p.fbddNoiseRd,
    set: (v) => ({ fbddNoiseRd: v as number }),
  },
  medPasses: { kind: 'numeric', min: 0, max: 5, step: 1, bigStep: 1, get: (p) => p.medPasses, set: (v) => ({ medPasses: v }) },
  demosaic: {
    kind: 'cycle', values: ['', 'vng', 'ppg', 'ahd', 'dht'],
    get: (p) => p.demosaic as string,
    set: (v) => ({ demosaic: v as Params['demosaic'] }),
  },
  caRed: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.caRed, set: (v) => ({ caRed: v }) },
  caBlue: { kind: 'numeric', min: -1, max: 1, step: 0.02, bigStep: 0.1, get: (p) => p.caBlue, set: (v) => ({ caBlue: v }) },
  cropAngle: { kind: 'numeric', min: -15, max: 15, step: 0.1, bigStep: 1, get: (p) => p.cropAngle, set: (v) => ({ cropAngle: v }) },
};

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

// esLoad opens an edit session for the newly focused photo.
export async function esLoad(client: ApiClient, photoId: number, applyIds: number[]) {
  window.clearTimeout(commitTimer);
  previewPending = null;
  previewAbort?.abort();
  esClearPreview();
  setState({ photoId, applyIds, draft: null, loading: true, wbPicking: false, cropping: false });
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

export function esSetActive(control: ControlId | null) {
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
