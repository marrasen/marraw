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
  getEditParams,
  pasteEditParams,
  pickWhiteBalance,
  previewEdit,
  resetEdits,
  setEditParams,
  type Params,
} from '@/api/edits';

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
  // The server stores the default as "" — same generated-union lie as wbMode.
  demosaic: '' as Params['demosaic'],
  caRed: 0,
  caBlue: 0,
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
  | 'nrThreshold'
  | 'fbddNoiseRd'
  | 'medPasses'
  | 'demosaic'
  | 'caRed'
  | 'caBlue';

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
};

interface Preview {
  photoId: number;
  url: string; // object URL of the preview JPEG
  blob: Blob;
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
}));

let previewTimer = 0;
let previewAbort: AbortController | null = null;
let commitTimer = 0;

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
  window.clearTimeout(previewTimer);
  window.clearTimeout(commitTimer);
  previewAbort?.abort();
  esClearPreview();
  setState({ photoId, applyIds, draft: null, loading: true, wbPicking: false });
  let params: Params | null = null;
  try {
    params = await getEditParams(client, photoId);
  } catch {
    params = null;
  }
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

// esUpdate changes the draft and schedules a debounced live preview.
export function esUpdate(client: ApiClient, patch: Partial<Params>) {
  const s = useEditSession.getState();
  if (!s.draft || s.photoId == null) return;
  setState({ draft: { ...s.draft, ...patch } });
  window.clearTimeout(previewTimer);
  previewTimer = window.setTimeout(() => void renderPreview(client), 150);
}

async function renderPreview(client: ApiClient) {
  const { photoId, draft } = useEditSession.getState();
  if (photoId == null || !draft) return;
  previewAbort?.abort();
  const ac = new AbortController();
  previewAbort = ac;
  setState((s) => ({ rendering: s.rendering + 1 }));
  try {
    const blob = await previewEdit(client, photoId, draft, { signal: ac.signal });
    if (useEditSession.getState().photoId !== photoId || ac.signal.aborted) return;
    const url = URL.createObjectURL(blob);
    const old = useEditSession.getState().preview;
    if (old) URL.revokeObjectURL(old.url);
    setState({ preview: { photoId, url, blob } });
  } catch {
    // aborted or superseded
  } finally {
    setState((s) => ({ rendering: Math.max(0, s.rendering - 1) }));
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
  const s = useEditSession.getState();
  if (!s.draft || s.photoId == null) return;
  const params = patch ? { ...s.draft, ...patch } : s.draft;
  if (patch) setState({ draft: params });
  pushHistory(s.photoId, params);
  const ids = s.applyIds.length > 1 ? s.applyIds : [s.photoId];
  persist(client, params, ids);
}

// esApplyParams replaces the whole draft (paste, picker result, undo) with
// immediate preview + persist.
export function esApplyParams(client: ApiClient, params: Params, opts?: { skipHistory?: boolean }) {
  const s = useEditSession.getState();
  if (s.photoId == null) return;
  setState({ draft: params });
  if (!opts?.skipHistory) pushHistory(s.photoId, params);
  window.clearTimeout(previewTimer);
  void renderPreview(client);
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
  window.clearTimeout(previewTimer);
  void renderPreview(client);
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
  window.clearTimeout(commitTimer);
  commitTimer = window.setTimeout(() => esCommit(client), 600);
}

// esReset clears the edit state of every photo in the selection.
export function esReset(client: ApiClient) {
  const s = useEditSession.getState();
  if (s.photoId == null) return;
  setState({ draft: { ...NEUTRAL } });
  pushHistory(s.photoId, { ...NEUTRAL });
  esClearPreview();
  const ids = s.applyIds.length > 1 ? s.applyIds : [s.photoId];
  resetEdits(client, ids).catch((err) => toast.error((err as Error).message));
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
