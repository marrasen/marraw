// editSession is the client-side state machine for non-destructive editing:
// the draft params of the focused photo, its per-photo undo history, the
// live preview (a JPEG Blob pushed over the WebSocket by PreviewEdit), the
// keyboard-focused control, and the commit targets (multi-selection).
//
// It lives outside React so the global keyboard map, the edit panel, and the
// loupe all drive the same state.
import { toast } from 'sonner';
import type { ApiClient } from '@/api/client';
import {
  autoAdjust,
  generateAIMap,
  generateAIMaps,
  generateFill,
  generateMaskFill,
  getEditParams,
  pasteEditParams,
  pickRangeColor,
  pickWhiteBalance,
  resetEdits,
  setEditParams,
  suggestHealSource,
  wBPickFrame,
} from '@/api/edits';
import { bumpImgBust } from '@/lib/imgCacheBust';
import type { Suggestion } from '@/api/edits';
import type { AIKindType, Mask, Params, Spot } from '@/api/edit';
import type { UserPreset } from '@/api/settings';
import { isModelNotDownloaded } from '@/lib/aiConsent';
import { offsetIsAdditive, type AutoPreset, type OffsetKey } from '@/lib/autoPresets';
import { applyUserPreset, lerpPresetAmount } from '@/lib/presetSections';
import { nextSeq } from '@/lib/undoSeq';
import {
  CONTROL_ORDER,
  CONTROL_SPECS,
  MASK_ALL_CONTROLS,
  MASK_CONTROL_SPECS,
  MASK_SHAPE_SPECS,
  NEUTRAL,
  defaultMask,
  isMaskShapeControl,
  maskShapeOrder,
  type ControlId,
  type MaskPanelControlId,
} from '@/lib/controlSpecs';
import { labelForDiff } from '@/lib/editLabels';
import { updateEditGroupOpen } from '@/lib/uiSettings';
import { useUIStore } from '@/stores/uiStore';

import {
  abandonRenders,
  esFlushDraft,
  forgetShown,
  queueDraftPatch,
  schedulePreview,
  schedulerIdle,
} from '@/lib/previewScheduler';
export { esFlushDraft } from '@/lib/previewScheduler';
import { useEditSession, type AIPickKind, type EditSessionState, type HistorySnapshot, type SpotMode } from '@/lib/editSessionStore';
export { useEditSession } from '@/lib/editSessionStore';
export type { AIPickKind, EditSessionState, HistorySnapshot, SpotMode } from '@/lib/editSessionStore';
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
export type GroupId = 'crop' | 'retouch' | 'tone' | 'presence' | 'wb' | 'color' | 'effects' | 'detail';


// Default edge softness for a freshly placed spot (fraction of its radius).
export const SPOT_FEATHER_DEFAULT = 0.5;

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
  lensDistortion: 'detail', lensVignetting: 'detail', lensCA: 'detail',
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


function setState(patch: Partial<EditSessionState> | ((s: EditSessionState) => Partial<EditSessionState>)) {
  useEditSession.setState(patch);
}

// Timers and generation tokens for the tool-mode helpers below: the commit
// debounce, the hover-preview debounce, the post-apply amount scrubber, and
// the idle delay before a freshly opened photo's AI maps are generated.
let commitTimer = 0;
// Monotonic token guarding the async autoAdjust in esAuto/esApplyAutoPreset:
// a newer apply (or a photo switch) supersedes an in-flight one so a stale
// result can't clobber the draft.
let applyGen = 0;
// Hover-preview debounce: quick sweeps across preset cards must not fire a
// render (or an autoAdjust) per card. The gen token supersedes the async
// resolution of an earlier hover.
let hoverTimer = 0;
let hoverGen = 0;
let amountTimer = 0;
// Idle delay before a freshly opened photo's missing AI maps are generated
// (esLoad → esEnsureAIMaps): flicking through a folder must not fire an
// inference — and the RAW decode behind it — for every frame it passes.
let ensureTimer = 0;
const ENSURE_AI_IDLE_MS = 400;

function sameParams(a: Params, b: Params): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export function esClearPreview() {
  const p = useEditSession.getState().preview;
  if (p) URL.revokeObjectURL(p.url);
  // The sharp frame (if any) is no longer what's displayed, so a future
  // settle for the same params must render, not dedupe-skip.
  forgetShown();
  setState({ preview: null });
}

// esPreviewSettled reports whether the current preview blob shows nothing
// beyond the committed state: no render in flight or queued, and the draft
// equal to the history head (a drag or a pending esStep commit means the blob
// carries uncommitted pixels the committed renditions don't have yet).
export function esPreviewSettled(): boolean {
  const s = useEditSession.getState();
  if (!schedulerIdle() || s.rendering > 0) return false;
  // A hover overlay is on screen: the blob shows the hovered preset, not the
  // committed state — even though the draft itself sits at the history head.
  if (s.hoverParams != null) return false;
  if (s.photoId == null || !s.draft) return false;
  const h = s.history[s.photoId];
  return !h || sameParams(s.draft, h.stack[h.index].params);
}

// esLoad opens an edit session for the newly focused photo. baseExpEV is
// the photo's measured camera-mimic baseline (photo.baseExpEV; 0 when
// unmeasured or the caller doesn't have the payload at hand).
export async function esLoad(client: ApiClient, photoId: number, applyIds: number[], baseExpEV = 0) {
  window.clearTimeout(commitTimer);
  window.clearTimeout(hoverTimer);
  window.clearTimeout(ensureTimer);
  applyGen++; // supersede any autoAdjust still in flight for the old photo
  hoverGen++;
  abandonRenders(); // clears the queue before aborting, so nothing refires for the old photo
  esClearPreview();
  revokePickFrame();
  setState((s) => ({
    photoId,
    applyIds,
    baseExpEV,
    hoverParams: null,
    lastPresetApply: null,
    draft: null,
    lastDraft: s.draft ?? s.lastDraft,
    loading: true,
    wbPicking: false,
    wbPickBase: null,
    wbPickFrameUrl: null,
    rangePicking: false,
    cropping: false,
    healing: false,
    activeSpot: null,
    spotVisualize: false,
    activeMask: null,
    activeMaskControl: null,
    maskPaint: false,
    tintMask: null,
    tintSpot: null,
    aiDetect: { person: null, class: null },
    aiPickArmed: null,
    aiHover: null,
    aiMapRestore: null,
    keyAdjust: false,
  }));
  const params = await getEditParams(client, photoId).catch(() => null);
  if (useEditSession.getState().photoId !== photoId) return; // superseded
  const draft = params ?? { ...NEUTRAL };
  setState((s) => {
    const history = s.history[photoId]
      ? s.history
      : { ...s.history, [photoId]: { stack: [{ params: draft, label: 'Original', seq: 0 }], index: 0 } };
    return { draft, loading: false, history };
  });
  // The edit may carry AI masks whose maps this machine never generated — a
  // sidecar from another computer, a cleared data dir, or a paste that landed
  // on the whole selection. Without the map they render as nothing. Only for a
  // photo the user actually stops on (see ensureTimer): browsing past one must
  // not queue an inference behind the frame the loupe is waiting for.
  ensureTimer = window.setTimeout(() => {
    if (useEditSession.getState().photoId === photoId) esEnsureAIMaps(client, photoId, draft);
  }, ENSURE_AI_IDLE_MS);
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
    if (
      es.activeControl != null ||
      es.activeMask != null ||
      es.activeMaskControl != null ||
      es.maskPaint ||
      es.healing ||
      es.rangePicking ||
      es.aiPickArmed != null
    ) {
      setState({
        activeControl: null,
        keyAdjust: false,
        activeMask: null,
        activeMaskControl: null,
        maskPaint: false,
        healing: false,
        rangePicking: false,
        activeSpot: null,
        spotVisualize: false,
        aiPickArmed: null,
        aiHover: null,
      });
    }
  }
});

// The Local tab's tools own the loupe pointer: the AI region pick, the heal
// tool and brush paint all read a click on the image as an edit. Unlike crop
// and WB pick they leave the drawer up, so the tab strip stays clickable while
// one is armed — and leaving the tab takes the tool's own controls (and the
// button showing it is armed) off screen while the image still answers to it.
// Entering these tools switches TO the Local tab, so leaving it exits them.
// uiStore cannot call in here, so the invariant is enforced from this side.
useUIStore.subscribe((s, prev) => {
  if (s.developTab === 'masks' || prev.developTab !== 'masks') return;
  const es = useEditSession.getState();
  if (!es.maskPaint && !es.healing && es.aiPickArmed == null) return;
  setState({
    maskPaint: false,
    healing: false,
    activeSpot: null,
    spotVisualize: false,
    aiPickArmed: null,
    aiHover: null,
  });
});

// esSetWBPicking opens/closes the WB eyedropper. Opening snapshots the current
// draft as the revert target (wbPickBase) for Reset/Cancel; closing here is a
// plain dismiss — use esWBPickDone / esWBPickCancel to keep or discard the
// previewed value.
//
// The snapshot is also what every pick in this session is sampled against, so
// it is fetched once here as an image: picks stay comparable with each other,
// and the magnifier can show the very pixels being sampled.
export function esSetWBPicking(client: ApiClient, on: boolean) {
  const s = useEditSession.getState();
  if (on && !s.draft) return;
  revokePickFrame();
  // Opening slides the develop drawer away — drop any keyboard-focused
  // control so +/- can't keep adjusting an invisible slider.
  setState(
    on
      ? { wbPicking: true, wbPickBase: s.draft, wbPickFrameUrl: null, rangePicking: false, activeControl: null, keyAdjust: false, aiPickArmed: null, aiHover: null }
      : { wbPicking: false, wbPickBase: null, wbPickFrameUrl: null },
  );
  if (on && s.photoId != null && s.draft) void loadWBPickFrame(client, s.photoId, s.draft);
}

// loadWBPickFrame fetches the pinned frame the server samples and hands it to
// the magnifier. A failure is silent: the readout falls back to the live
// preview, and picking itself doesn't depend on this.
async function loadWBPickFrame(client: ApiClient, photoId: number, base: Params) {
  try {
    const blob = await wBPickFrame(client, photoId, base);
    const cur = useEditSession.getState();
    if (cur.photoId !== photoId || !cur.wbPicking || cur.wbPickBase !== base) return;
    revokePickFrame();
    setState({ wbPickFrameUrl: URL.createObjectURL(blob) });
  } catch {
    // superseded or unavailable — the magnifier keeps the live frame
  }
}

// revokePickFrame releases the pinned frame's object URL, if one is held.
function revokePickFrame() {
  const url = useEditSession.getState().wbPickFrameUrl;
  if (url) URL.revokeObjectURL(url);
}

// esSetRangePicking opens/closes the range mask's colour eyedropper. It only
// opens when a range mask is selected; opening drops any other picker/tool so
// they don't fight over the loupe pointer.
export function esSetRangePicking(on: boolean) {
  const s = useEditSession.getState();
  if (on) {
    if (s.activeMask == null) return;
    const m = s.draft?.masks?.[s.activeMask];
    if (!m || m.type !== 'range') return;
  }
  setState(
    on
      ? { rangePicking: true, wbPicking: false, wbPickBase: null, activeControl: null, keyAdjust: false, aiPickArmed: null, aiHover: null }
      : { rangePicking: false },
  );
}

// esPickRangeColor samples the developed colour at the clicked loupe point and
// seeds the active range mask's hue window + saturation floor, committing one
// history entry. The eyedropper stays open so several picks can be compared;
// esApplyParams leaves rangePicking untouched.
export async function esPickRangeColor(client: ApiClient, x: number, y: number) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft || s.activeMask == null) return;
  const idx = s.activeMask;
  const m = s.draft.masks?.[idx];
  if (!m || m.type !== 'range') return;
  try {
    const params = await pickRangeColor(client, s.photoId, s.draft, x, y, idx);
    const cur = useEditSession.getState();
    if (cur.photoId !== s.photoId || !cur.rangePicking) return; // superseded / closed
    esApplyParams(client, params, { label: 'Pick range colour' });
  } catch (err) {
    toast.error(`Colour pick failed: ${(err as Error).message}`);
  }
}

// esSetCropping toggles the crop overlay. Entering re-renders the preview
// without the crop (the full straightened frame the overlay draws on);
// leaving re-renders the committed crop and persists the draft.
export function esSetCropping(client: ApiClient, on: boolean) {
  const s = useEditSession.getState();
  if (s.cropping === on) return;
  // Entering slides the develop drawer away — drop any keyboard-focused
  // control so +/- can't keep adjusting an invisible slider.
  setState(on ? { cropping: true, rangePicking: false, activeControl: null, keyAdjust: false, aiPickArmed: null, aiHover: null } : { cropping: false });
  if (!on) {
    esCommit(client); // persist the crop; the commit re-renders the cropped frame
  } else {
    schedulePreview(client, 'settle');
  }
}

// --- Retouch spots (heal / clone) ---
// Spots live inside the draft (Params.spots) so history, copy/paste and
// persistence handle them for free, exactly like masks. Placing/dragging goes
// through esUpdate (coalesced draft frames) and commits on release, so one
// gesture is one history entry; the source patch is chosen server-side once at
// release (esFinishSpot) and stored in the spot, keeping it stable.

// esSetHealing toggles the heal tool. Unlike crop it needs no flat re-render —
// the ordinary (cropped) view is the heal canvas — so it only flips the flag
// and drops any spot selection when leaving.
export function esSetHealing(on: boolean) {
  const s = useEditSession.getState();
  if (s.healing === on) return;
  if (on) useUIStore.getState().setDevelopTab('masks'); // the Retouch section lives on the Local tab
  setState(
    on
      ? { healing: true, rangePicking: false, aiPickArmed: null, aiHover: null }
      : { healing: false, activeSpot: null, spotVisualize: false },
  );
}

// --- AI region pick tool (people + scene) ---

// esSetAIDetect stores a kind's detection result (the chip row). Kept until
// the photo changes; both kinds can hold a result at once. No side effects —
// arming the image-pick tool is separate (esArmAIPick).
export function esSetAIDetect(kind: AIPickKind, result: EditSessionState['aiDetect'][AIPickKind]) {
  setState((s) => ({ aiDetect: { ...s.aiDetect, [kind]: result } }));
}

// esArmAIPick enters/exits the image-pick tool for one kind (null = disarm).
// Arming keeps the develop drawer up (the chips stay reachable) and drops the
// heal/paint tools it shares the Local tab with; it needs a detection for that
// kind. Crop and WB can't race it: their overlays hide the panel button and
// they disarm on entry regardless. Leaving the Local tab disarms it (see the
// uiStore subscription above). Disarming leaves the chips (aiDetect) in place
// — only a photo switch clears those.
export function esArmAIPick(kind: AIPickKind | null) {
  const s = useEditSession.getState();
  if (kind && (!s.draft || !s.aiDetect[kind])) return;
  setState(
    kind
      ? {
          aiPickArmed: kind,
          aiHover: null,
          activeControl: null,
          keyAdjust: false,
          healing: false,
          rangePicking: false,
          activeSpot: null,
          spotVisualize: false,
          maskPaint: false,
        }
      : { aiPickArmed: null, aiHover: null },
  );
}

// esSetAIHover highlights one region (from the loupe pointer or a panel
// chip); the AIPickOverlay renders its tint. Bails when the region is
// unchanged so a stream of pointer moves over one region doesn't churn the
// hover reference (and the tint fetch that keys off it).
export function esSetAIHover(hover: { kind: AIPickKind; id: number } | null) {
  const cur = useEditSession.getState().aiHover;
  if (cur?.kind === hover?.kind && cur?.id === hover?.id) return;
  setState({ aiHover: hover });
}

// esSetActiveSpot selects a spot (its overlay circles + expanded row).
export function esSetActiveSpot(index: number | null) {
  setState({ activeSpot: index });
}

// esSetSpotMode sets the fill mode for newly placed spots.
export function esSetSpotMode(mode: SpotMode) {
  setState({ spotMode: mode });
}

// esSetSpotTool switches the retouch region tool (circle spots / heal brush).
export function esSetSpotTool(tool: 'spot' | 'brush') {
  setState({ spotTool: tool });
}

// esSetSpotBrush updates the heal brush settings for the next stroke.
export function esSetSpotBrush(
  patch: Partial<Pick<EditSessionState, 'spotBrushRadius' | 'spotBrushFeather'>>,
) {
  setState(patch);
}

// esSetSpotVisualize toggles the dust-visualization loupe view (A key while
// healing); esSetSpotVisualizeThreshold tunes its sensitivity.
export function esSetSpotVisualize(on: boolean) {
  setState({ spotVisualize: on });
}
export function esSetSpotVisualizeThreshold(t: number) {
  setState({ spotVisualizeThreshold: t });
}

// esBeginSpot appends a spot to the draft (no commit yet) and selects it,
// returning its index. The overlay drives the placement drag through
// esUpdateSpot and finalizes on release with esFinishSpot — so the whole
// gesture lands as one "Add spot" history entry. mode omits the canonical
// "heal" so a heal spot marshals clean.
export function esBeginSpot(client: ApiClient, spot: Omit<Spot, 'mode'>): number {
  esFlushDraft();
  const s = useEditSession.getState();
  if (!s.draft || s.photoId == null) return -1;
  const full: Spot = s.spotMode === 'heal' ? { ...spot } : { ...spot, mode: s.spotMode };
  const spots = [...(s.draft.spots ?? []), full];
  const index = spots.length - 1;
  setState({ activeSpot: index });
  esUpdate(client, { spots });
  return index;
}

// esUpdateSpot merges a patch into one spot during a placement or handle drag
// (coalesced low-res preview; commit on release). Flushes first so back-to-back
// updates in one frame don't clobber each other.
export function esUpdateSpot(client: ApiClient, index: number, patch: Partial<Spot>) {
  esFlushDraft();
  const s = useEditSession.getState();
  const spots = s.draft?.spots;
  if (!spots || !spots[index]) return;
  const next = spots.slice();
  next[index] = { ...next[index], ...patch };
  esUpdate(client, { spots: next });
}

// esFinishSpot asks the backend for the best source patch for a just-placed
// spot, applies it, and commits (one history entry). Falls back to committing
// the interim source if the suggestion fails or is superseded. Guarded by
// applyGen so neither a photo switch nor a spot removal (which shifts the
// indices this call patches by) can land the suggestion on the wrong spot.
export async function esFinishSpot(client: ApiClient, index: number) {
  esFlushDraft();
  const s = useEditSession.getState();
  const spots = s.draft?.spots;
  if (s.photoId == null || !s.draft || !spots || !spots[index]) return;
  // A fill spot has no source to suggest: commit as placed — esCommit's
  // ensure pass generates (or asks consent for) the inpaint patch.
  if (spots[index].mode === 'fill') {
    esCommit(client);
    return;
  }
  const gen = ++applyGen;
  const pid = s.photoId;
  try {
    const suggested = await suggestHealSource(client, pid, s.draft, spots[index]);
    if (applyGen === gen && useEditSession.getState().photoId === pid) {
      // A stroke spot's dest reference comes back too (the painted region's
      // enclosing-circle center) — the source vector is relative to it, so
      // both must be stored together.
      const patch: Partial<Spot> =
        spots[index].kind === 'stroke'
          ? { cx: suggested.cx, cy: suggested.cy, sx: suggested.sx, sy: suggested.sy }
          : { sx: suggested.sx, sy: suggested.sy };
      esUpdateSpot(client, index, patch);
    }
  } catch {
    // keep the interim source
  } finally {
    if (applyGen === gen && useEditSession.getState().photoId === pid) {
      esCommit(client);
    }
  }
}

// esRemoveSpot deletes a spot and commits. Removal shifts the indices after
// it, so it supersedes any esFinishSpot still awaiting its source suggestion
// (applyGen) — a stale index must not patch whatever spot slid into its slot.
export function esRemoveSpot(client: ApiClient, index: number) {
  esFlushDraft();
  const s = useEditSession.getState();
  const spots = s.draft?.spots;
  if (!spots || !spots[index]) return;
  applyGen++;
  const next = spots.filter((_, i) => i !== index);
  setState({ activeSpot: null });
  esCommit(client, { spots: next });
}

// --- Local adjustment masks ---
// Masks live inside the draft (Params.masks) so history, copy/paste and
// persistence handle them for free; these helpers only edit the array and
// drive the same esUpdate/esCommit flow as any slider. Unlike the crop there
// is no client-only preview path — mask changes alter pixels, so shape drags
// render backend draft frames like any adjustment.

// esSetActiveMask selects a mask (its overlay handles + expanded sliders).
// Row selection carries no slider focus; deselecting leaves paint mode.
export function esSetActiveMask(index: number | null) {
  setState((s) => ({
    activeMask: index,
    activeMaskControl: null,
    maskPaint: index == null ? false : s.maskPaint,
    // The colour eyedropper belongs to one range mask; changing selection
    // closes it (re-open from the newly selected mask's controls).
    rangePicking: false,
    keyAdjust: false,
    // Selecting a mask hands the loupe to its overlay handles — disarm the AI
    // pick tool so the two don't fight over pointer events. Deselecting leaves
    // the armed state untouched.
    aiPickArmed: index == null ? s.aiPickArmed : null,
    aiHover: index == null ? s.aiHover : null,
  }));
}

// esSetActiveMaskControl focuses one slider of one mask (pointer-down on the
// row, mirroring esSetActive for the develop controls).
export function esSetActiveMaskControl(index: number, control: MaskPanelControlId | null) {
  setState({ activeMask: index, activeMaskControl: control, keyAdjust: false });
}

// esMoveMaskActive walks the keyboard focus through every mask's sliders as
// ONE flat list (mask 1's sliders, then mask 2's, …) — stepping past a mask's
// last slider lands on the next mask's first, selecting that mask as it goes,
// so ↑/↓ tour all masks. With nothing focused it enters at the selected
// mask's near edge (or the list's, like esMoveActive); at the very ends it
// stays put.
export function esMoveMaskActive(dir: 1 | -1) {
  const s = useEditSession.getState();
  const masks = s.draft?.masks;
  if (!masks || masks.length === 0) return;
  // Built per mask rather than by modular arithmetic over a fixed stride,
  // because the effect direction is only rendered when a smear is live — the
  // walk must skip it, or ↑/↓ would park the focus ring on a control that
  // isn't on screen.
  const steps: { mask: number; control: MaskPanelControlId }[] = [];
  const edges: number[] = []; // index of each mask's first step
  masks.forEach((m, mi) => {
    edges.push(steps.length);
    // Shape sliders first: they render above the adjust block, so entering a
    // mask from above must land on Threshold, not Exposure.
    for (const control of maskShapeOrder(m)) steps.push({ mask: mi, control });
    for (const control of MASK_ALL_CONTROLS) {
      if (control === 'fxAngle' && !(m.adjust?.motionBlur || m.adjust?.streaks)) continue;
      steps.push({ mask: mi, control });
    }
  });
  if (steps.length === 0) return;
  let i: number;
  if (s.activeMask != null && s.activeMaskControl != null) {
    i = steps.findIndex((st) => st.mask === s.activeMask && st.control === s.activeMaskControl);
    if (i < 0) i = dir > 0 ? -1 : steps.length; // the focused control just went away
  } else if (s.activeMask != null) {
    // A selected mask without a focused slider: enter at its near edge.
    const first = edges[s.activeMask] ?? 0;
    const last = (edges[s.activeMask + 1] ?? steps.length) - 1;
    i = dir > 0 ? first - 1 : last + 1;
  } else {
    i = dir > 0 ? -1 : steps.length;
  }
  i += dir;
  if (i < 0 || i >= steps.length) return;
  setState({
    activeMask: steps[i].mask,
    activeMaskControl: steps[i].control,
    maskPaint: false,
    keyAdjust: false,
  });
}

// esStepMask nudges the focused mask slider from the keyboard (+/-, Shift =
// big steps): live low-res preview per step, one undoable commit after a
// short idle — esStep's contract for the develop controls. Deliberately no
// heads-up keyAdjust mode: hiding the drawer would hide the slider ring the
// walk just placed.
export function esStepMask(client: ApiClient, dir: 1 | -1, big = false) {
  const s = useEditSession.getState();
  const masks = s.draft?.masks;
  if (!masks || s.activeMask == null || s.activeMaskControl == null) return;
  const m = masks[s.activeMask];
  if (!m) return;
  // Shape sliders live on the mask itself, adjust sliders in mask.adjust — the
  // step/clamp/commit tail is the same either way.
  if (isMaskShapeControl(s.activeMaskControl)) {
    const spec = MASK_SHAPE_SPECS[s.activeMaskControl];
    const raw = spec.get(m) + dir * (big ? spec.bigStep : spec.step);
    const v = Math.min(spec.max, Math.max(spec.min, Math.round(raw * 1000) / 1000));
    esUpdateMask(client, s.activeMask, spec.set(v));
  } else {
    const spec = MASK_CONTROL_SPECS[s.activeMaskControl];
    const step = big ? spec.bigStep : spec.step;
    const raw = (m.adjust?.[s.activeMaskControl] ?? 0) + dir * step;
    const v = Math.min(spec.max, Math.max(spec.min, Math.round(raw * 1000) / 1000));
    esUpdateMask(client, s.activeMask, { adjust: { ...m.adjust, [s.activeMaskControl]: v } });
  }
  esFlushDraft(); // a discrete key step should land in the draft immediately
  schedulePreview(client, 'settle'); // sharp frame right behind the instant one
  window.clearTimeout(commitTimer);
  commitTimer = window.setTimeout(() => esCommit(client), 600);
}

// esSetMaskPaint toggles brush paint mode for the active brush mask. Painting
// owns the loupe pointer, so entering it disarms the AI pick tool.
export function esSetMaskPaint(on: boolean) {
  setState(on ? { maskPaint: true, aiPickArmed: null, aiHover: null } : { maskPaint: false });
}

// esSetBrushTool updates the shared brush tool settings (radius/feather/flow/
// erase) used for the next stroke.
export function esSetBrushTool(
  patch: Partial<Pick<EditSessionState, 'brushRadius' | 'brushFeather' | 'brushFlow' | 'brushErase'>>,
) {
  setState(patch);
}

// esSetTintMask shows (or clears) the hover weight tint for one mask.
export function esSetTintMask(index: number | null) {
  setState({ tintMask: index });
}

// esSetTintSpot shows (or clears) the hover tint for one retouch spot.
export function esSetTintSpot(index: number | null) {
  setState({ tintSpot: index });
}

// esAddMask appends a mask with a sensible default shape, selects it, and
// commits ("Add radial mask" in history). A brush starts empty and drops the
// session straight into paint mode. Also switches the panel to the Masks tab
// so the new mask's sliders are visible.
export function esAddMask(client: ApiClient, type: Mask['type']) {
  esAddMaskObject(client, defaultMask(type));
}

// esAddMaskObject appends a fully-formed mask — the AI path builds its mask
// from a GenerateAIMap result (kind + mapVer) rather than default geometry.
export function esAddMaskObject(client: ApiClient, mask: Mask) {
  esFlushDraft();
  const s = useEditSession.getState();
  if (!s.draft || s.photoId == null) return;
  const masks = [...(s.draft.masks ?? []), mask];
  useUIStore.getState().setDevelopTab('masks');
  // AI adds (chips, image click, Subject/Depth) keep the pick tool armed so
  // several regions can be added in a row; a parametric add (Linear/Radial/
  // Brush) needs the loupe for handles, so it disarms.
  setState({
    activeMask: masks.length - 1,
    activeMaskControl: null,
    maskPaint: mask.type === 'brush',
    ...(mask.type === 'ai' ? {} : { aiPickArmed: null, aiHover: null }),
  });
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

// esMoveMask moves one mask to another slot in the stack and commits. Order is
// composite order — a later mask sees the earlier ones' output (pyramid.
// ApplyMasks) — so this is a pixel change like any slider move, not a display
// nicety, and it goes through the same commit/settle path.
//
// Session state that names masks by position has to follow the mask it named:
// the selection (its overlay handles are on the loupe) and the pending removal
// consent (declining it clears that mask's Remove flag). maskFillBusy is left
// alone on purpose — a generation in flight clears the index it set, so
// remapping it would strand a spinner that never gets cleared; parking one on a
// neighbour until it settles is the documented best-effort behaviour.
export function esMoveMask(client: ApiClient, from: number, to: number) {
  esFlushDraft();
  const s = useEditSession.getState();
  const masks = s.draft?.masks;
  if (!masks || !masks[from] || to < 0 || to >= masks.length || from === to) return;
  const next = masks.slice();
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  setState({
    activeMask: s.activeMask == null ? null : movedIndex(s.activeMask, from, to),
    maskFillConsent: s.maskFillConsent == null ? null : movedIndex(s.maskFillConsent, from, to),
  });
  esCommit(client, { masks: next });
}

// movedIndex maps an index through the same move: where whatever sat at i ends
// up once the entry at `from` is pulled out and re-inserted at `to`.
function movedIndex(i: number, from: number, to: number): number {
  if (i === from) return to;
  const j = i > from ? i - 1 : i; // after the removal
  return j >= to ? j + 1 : j; //     after the insertion
}

// esRemoveMask deletes a mask and commits.
export function esRemoveMask(client: ApiClient, index: number) {
  esFlushDraft();
  const s = useEditSession.getState();
  const masks = s.draft?.masks;
  if (!masks || !masks[index]) return;
  const next = masks.filter((_, i) => i !== index);
  setState({ activeMask: null, activeMaskControl: null, maskPaint: false });
  esCommit(client, { masks: next });
}

const CROP_RECT_KEYS = ['cropX', 'cropY', 'cropW', 'cropH'] as const;
// Params the crop overlay previews entirely client-side while cropping: the
// rectangle (drawn by the overlay) and the straighten angle (a CSS rotation
// of the flat frame). Changing any of these needs no backend render.
const CROP_LIVE_KEYS = [...CROP_RECT_KEYS, 'cropAngle'] as const;

export function esUpdate(client: ApiClient, patch: Partial<Params>) {
  const s = useEditSession.getState();
  if (!s.draft || s.photoId == null) return;
  // A manual slider move ends the post-apply amount scrubber (its base and
  // result no longer describe the draft) and any hover overlay (the moved
  // slider must be visible, not a stale hover frame).
  if (s.lastPresetApply || s.hoverParams) setState({ lastPresetApply: null, hoverParams: null });
  queueDraftPatch(patch);
  // While cropping, the crop rectangle and straighten angle are previewed
  // client-side (overlay + CSS rotation), so they need no backend render.
  if (s.cropping && Object.keys(patch).every((k) => (CROP_LIVE_KEYS as readonly string[]).includes(k))) {
    return;
  }
  schedulePreview(client, 'draft');
}

function pushHistory(photoId: number, params: Params, label: string) {
  setState((s) => {
    const entry = s.history[photoId] ?? { stack: [{ params: { ...NEUTRAL }, label: 'Original', seq: 0 }], index: 0 };
    if (sameParams(entry.stack[entry.index].params, params)) return {};
    const stack = [...entry.stack.slice(0, entry.index + 1), { params, label, seq: nextSeq() }].slice(-50);
    return { history: { ...s.history, [photoId]: { stack, index: stack.length - 1 } } };
  });
}

// persist saves the params to every target. The returned promise settles when
// the save is done (or has failed and been reported) — never rejects — so
// callers can sequence work that must not race the write.
function persist(client: ApiClient, params: Params, ids: number[]): Promise<void> {
  const p =
    ids.length > 1
      ? pasteEditParams(client, ids, params)
      : setEditParams(client, ids[0], params);
  return p.catch((err) => {
    toast.error(`Save failed: ${(err as Error).message}`);
  });
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
  void persist(client, params, ids);
  // Settle render: drag frames were low-res, so bring the loupe back to the
  // full 2048 (which the server also writes to the pyramid cache).
  schedulePreview(client, 'settle');
  // Fill spots need their ML patch to exist for the state just committed;
  // the server fast-paths when nothing changed, so this is free noise-wise.
  void esEnsureFills(client);
  void esEnsureMaskFills(client);
}

// esEnsureFills asks the server for the inpaint patch of every enabled fill
// spot in the draft. Runs after every commit: GenerateFill is idempotent and
// cheap when the patch is cached, and re-keys itself when the spot geometry
// or decode settings changed — so this one hook covers new fill spots, mode
// switches, and develop changes that invalidate existing patches. Without the
// model on disk the server refuses (consent contract) and the first refused
// index opens the download dialog via fillConsent.
async function esEnsureFills(client: ApiClient, allowDownload = false, only?: number) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft?.spots) return;
  const pid = s.photoId;
  const draft = s.draft;
  for (let i = 0; i < (draft.spots?.length ?? 0); i++) {
    const sp = draft.spots![i];
    if (sp.mode !== 'fill' || sp.disabled) continue;
    if (only != null && i !== only) continue;
    if (useEditSession.getState().fillBusy.includes(i)) continue;
    setState((st) => ({ fillBusy: [...st.fillBusy, i] }));
    try {
      const res = await generateFill(client, pid, draft, i, allowDownload);
      if (useEditSession.getState().photoId !== pid) return;
      if (res.generated) {
        // New pixels for the same edit hash: refresh thumbs and the loupe.
        bumpImgBust(pid);
        schedulePreview(client, 'settle');
      }
    } catch (err) {
      if (useEditSession.getState().photoId !== pid) return;
      if (isModelNotDownloaded(err)) {
        setState((st) => (st.fillConsent == null ? { fillConsent: i } : {}));
      } else {
        toast.error(`Content-aware fill failed: ${(err as Error).message}`);
      }
    } finally {
      setState((st) => ({ fillBusy: st.fillBusy.filter((b) => b !== i) }));
    }
  }
}

// esConfirmFillDownload re-runs the consent-blocked fill with the download
// allowed — the dialog's confirm action.
export function esConfirmFillDownload(client: ApiClient) {
  const index = useEditSession.getState().fillConsent;
  setState({ fillConsent: null });
  if (index == null) return;
  void esEnsureFills(client, true, index);
}

// esDeclineFillDownload reverts the consent-blocked spot to heal mode (a fill
// spot without a model would silently render nothing) and re-runs the source
// suggestion so it heals from a sensible patch.
export function esDeclineFillDownload(client: ApiClient) {
  const s = useEditSession.getState();
  const index = s.fillConsent;
  setState({ fillConsent: null });
  if (index == null || !s.draft?.spots?.[index]) return;
  esUpdateSpot(client, index, { mode: undefined });
  void esFinishSpot(client, index);
}

// esEnsureMaskFills is esEnsureFills for Remove masks: it asks the server for
// the inpaint patch of every enabled removal in the draft. Same contract —
// GenerateMaskFill is idempotent and cheap when the patch is cached, and
// re-keys itself when the region or the decode settings changed, so one hook
// after each commit covers new removals, repainted brushes, a different
// person picked, and develop changes that invalidate a patch.
//
// A removal whose AI map hasn't been generated yet is skipped quietly (the
// server says so): esEnsureAIMaps runs the detection, its commit lands here
// again, and the patch generates then.
async function esEnsureMaskFills(client: ApiClient, allowDownload = false, only?: number) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft?.masks) return;
  const pid = s.photoId;
  const draft = s.draft;
  for (let i = 0; i < (draft.masks?.length ?? 0); i++) {
    const m = draft.masks![i];
    if (!m.remove || m.disabled) continue;
    if (only != null && i !== only) continue;
    if (useEditSession.getState().maskFillBusy.includes(i)) continue;
    setState((st) => ({ maskFillBusy: [...st.maskFillBusy, i] }));
    try {
      const res = await generateMaskFill(client, pid, draft, i, allowDownload);
      if (useEditSession.getState().photoId !== pid) return;
      if (res.generated) {
        // New pixels for the same edit hash: refresh thumbs and the loupe.
        bumpImgBust(pid);
        schedulePreview(client, 'settle');
      }
    } catch (err) {
      if (useEditSession.getState().photoId !== pid) return;
      const msg = (err as Error).message;
      if (isModelNotDownloaded(err)) {
        setState((st) => (st.maskFillConsent == null ? { maskFillConsent: i } : {}));
      } else if (msg.includes(MASK_FILL_TOO_LARGE)) {
        toast.error('That region covers too much of the frame to fill convincingly.');
        esUpdateMask(client, i, { remove: undefined });
        esCommit(client);
      } else if (!msg.includes(MASK_FILL_NO_REGION)) {
        // No region yet just means the AI map is still coming; anything else
        // is worth surfacing.
        toast.error(`Remove failed: ${msg}`);
      }
    } finally {
      setState((st) => ({ maskFillBusy: st.maskFillBusy.filter((b) => b !== i) }));
    }
  }
}

// Server sentinels for a refused removal (internal/api/fill.go). Matched as
// substrings, the isModelNotDownloaded precedent.
const MASK_FILL_TOO_LARGE = 'mask region is too large to remove';
const MASK_FILL_NO_REGION = 'mask has no region to remove';

// esToggleMaskRemove flips a mask into or out of inpaint mode and kicks off
// the generation for the state just committed.
export function esToggleMaskRemove(client: ApiClient, index: number, on: boolean) {
  esUpdateMask(client, index, { remove: on ? true : undefined });
  esCommit(client);
  if (on) void esEnsureMaskFills(client, false, index);
}

// esConfirmMaskFillDownload re-runs the consent-blocked removal with the
// download allowed — the dialog's confirm action.
export function esConfirmMaskFillDownload(client: ApiClient) {
  const index = useEditSession.getState().maskFillConsent;
  setState({ maskFillConsent: null });
  if (index == null) return;
  void esEnsureMaskFills(client, true, index);
}

// esDeclineMaskFillDownload clears the Remove flag: without the model the mask
// would keep its removal pill lit while rendering nothing.
export function esDeclineMaskFillDownload(client: ApiClient) {
  const s = useEditSession.getState();
  const index = s.maskFillConsent;
  setState({ maskFillConsent: null });
  if (index == null || !s.draft?.masks?.[index]) return;
  esUpdateMask(client, index, { remove: undefined });
  esCommit(client);
}

// An AI mask is a RECIPE, not pixels: it names a model map by (aiKind,
// mapVer), and that map is a per-photo file the params do NOT carry. A photo
// without it on disk renders the mask as exactly nothing (pyramid.newAIEval
// returns no evaluator — a silent no-op by design). So every path that lands
// AI masks on a photo that never ran the model has to kick the inference off
// itself: pasting settings, applying a preset, opening a photo whose sidecar
// came from another machine.
//
// This used to live in the Local panel's mask section, which meant the maps
// only ever ran while that tab was mounted — a pasted background blur stayed
// invisible until the user happened to open Local.
//
// Remembered per (photo, kind) for the session: GenerateAIMap is idempotent
// and cheap when the map is on disk, but there's no reason to re-ask on every
// paste or history step.
const aiMapsFired = new Set<string>();

// esEnsureAIMaps materializes the maps `params`' AI masks reference on the
// focused photo, then repaints — a map is a render input OUTSIDE the edit
// hash, so nothing else invalidates the frames already on screen. `rest` (the
// other photos of a multi-photo apply) get theirs from one shared background
// task, each landing map cache-busting its own thumb via AIMapsGeneratedEvent.
//
// Never downloads a model: a missing one surfaces as aiMapRestore for the
// consent dialog, exactly once per session per kind.
export function esEnsureAIMaps(client: ApiClient, photoId: number, params: Params, rest: number[] = []) {
  const kinds = [
    ...new Set((params.masks ?? []).filter((m) => m.type === 'ai' && m.aiKind).map((m) => m.aiKind!)),
  ];
  if (kinds.length === 0) return;
  void (async () => {
    const ran: AIKindType[] = [];
    for (const kind of kinds) {
      const key = `${photoId}|${kind}`;
      if (aiMapsFired.has(key)) {
        ran.push(kind);
        continue;
      }
      aiMapsFired.add(key);
      try {
        const res = await generateAIMap(client, photoId, kind, false);
        ran.push(kind);
        // Repaint ONLY when a map actually regenerated: an unconditional
        // nudge forces a transient (non-abortable) decode on every first
        // visit to a masked photo — those piled up into browse stalls.
        if (!res.generated) continue;
        bumpImgBust(photoId); // the grid thumb is immutably cached — refetch it
        // The loupe's sharp frame was rendered without the map and is deduped
        // by lastShown; the low-res frame clears that, so the settle behind it
        // really re-renders.
        if (useEditSession.getState().photoId === photoId) previewThenSettle(client);
      } catch (err) {
        if (isModelNotDownloaded(err)) {
          // Stays in aiMapsFired: ask once, not on every paste or re-render.
          setState((st) => (st.aiMapRestore == null ? { aiMapRestore: kind } : {}));
          continue;
        }
        aiMapsFired.delete(key); // transient failure: allow a retry
      }
    }
    // Consent already settled by the focused photo's runs above, so the batch
    // needs no allowDownload of its own.
    if (rest.length === 0 || ran.length === 0) return;
    void generateAIMaps(client, rest, ran, false).catch((err) => {
      toast.error(`AI masks failed for the selection: ${(err as Error).message}`);
    });
  })();
}

// esClearAIMapRestore drops the pending consent ask once a dialog owns it.
export function esClearAIMapRestore() {
  setState({ aiMapRestore: null });
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
  const photoId = s.photoId;
  // hoverParams cleared: a replaced draft must not stay hidden behind a
  // stale hover overlay (the pointer may still be parked on a card).
  setState({ draft: params, lastPresetApply: null, hoverParams: null });
  if (!opts?.skipHistory) pushHistory(photoId, params, opts?.label ?? 'Edit');
  schedulePreview(client, 'settle');
  const ids = s.applyIds.length > 1 ? s.applyIds : [photoId];
  // Pasted AI masks need their maps generated here — AFTER the save lands:
  // GenerateAIMap drops the photo's cached renditions for the edit hash it
  // reads from the DB, so a map materialized before the paste is stored would
  // invalidate the OLD hash and leave the just-rendered maskless frame cached
  // under the new one.
  void persist(client, params, ids).then(() =>
    esEnsureAIMaps(client, photoId, params, ids.filter((id) => id !== photoId)),
  );
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
  setState({ draft: params, lastPresetApply: null, hoverParams: null });
  pushHistory(s.photoId, params, label);
  const ids = s.applyIds.length > 1 ? s.applyIds : [s.photoId];
  void persist(client, params, ids);
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

// The seq of the snapshot the next undo would leave / the next redo would
// restore on the focused photo, or null when that side is spent — compared
// against the cull history's by the keyboard dispatch (see undoSeq.ts).
export function esUndoSeq(): number | null {
  const s = useEditSession.getState();
  if (s.photoId == null) return null;
  const h = s.history[s.photoId];
  return h && h.index > 0 ? h.stack[h.index].seq : null;
}

export function esRedoSeq(): number | null {
  const s = useEditSession.getState();
  if (s.photoId == null) return null;
  const h = s.history[s.photoId];
  return h && h.index < h.stack.length - 1 ? h.stack[h.index + 1].seq : null;
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
  const photoId = s.photoId;
  const params = h.stack[index].params;
  setState({
    draft: params,
    lastPresetApply: null,
    history: { ...s.history, [photoId]: { ...h, index } },
  });
  schedulePreview(client, 'settle');
  // Redoing back to a state whose masks were pasted in — same save-then-
  // generate ordering as esApplyParams.
  void persist(client, params, [photoId]).then(() => esEnsureAIMaps(client, photoId, params));
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
  // Same order as esLoad, which abandonRenders owns: the queue is cleared
  // before the abort so the finally does not refire, and an in-flight
  // pre-reset render cannot land a stale blob after the clear below (the
  // photoId guard alone won't catch it — the photo hasn't changed).
  abandonRenders();
  setState({ draft: { ...NEUTRAL }, lastPresetApply: null });
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
    // AFTER the apply — it clears lastPresetApply like any draft replacement.
    setState({ lastPresetApply: { photoId: pid, base, result: out, name: preset.name, amount: 1 } });
  } catch (err) {
    toast.error(`Auto adjust failed: ${(err as Error).message}`);
  }
}

// The fields a suggested look can carry — the union of what the server's
// auto base and suggestion recipes write (lockstep with
// internal/pyramid/suggest.go). Applying merges exactly these over the live
// draft, so a suggestion computed from a stale base snapshot can never
// revert edits it doesn't speak for (WB, detail, texture, geometry, masks).
const SUGGESTION_KEYS = [
  'expEV', 'contrast', 'whites', 'blacks', 'toneShadows', 'toneHighlights',
  'saturation', 'vibrance',
  'splitShadowHue', 'splitShadowAmt', 'splitHighlightHue', 'splitHighlightAmt',
  'clarity', 'dehaze', 'vignette',
] as const;

function mergeSuggestion(draft: Params, s: Suggestion): Params {
  const out: Params = { ...draft };
  const fields = out as unknown as Record<string, unknown>;
  const src = s.params as unknown as Record<string, unknown>;
  for (const key of SUGGESTION_KEYS) fields[key] = src[key];
  return out;
}

// esApplySuggestion applies a server-suggested look (SuggestEdits candidate)
// over the live draft: one labeled undo entry, multi-select persistence, and
// the instant-low-res + queued-settle paint (esApplyParamsPreview). Records
// lastPresetApply so the post-apply Amount scrubber works on suggestions.
export function esApplySuggestion(client: ApiClient, s: Suggestion) {
  const st = useEditSession.getState();
  if (st.photoId == null || !st.draft) return;
  const base = st.draft;
  const result = mergeSuggestion(base, s);
  esApplyParamsPreview(client, result, s.label);
  // AFTER the apply — it clears lastPresetApply like any draft replacement.
  setState({ lastPresetApply: { photoId: st.photoId, base, result, name: s.label, amount: 1 } });
}

// esHoverSuggestion previews a suggestion on the loupe while its card is
// hovered. The candidate's params are already resolved, so unlike preset
// hovers there is no RPC inside the debounce — just the merge.
export function esHoverSuggestion(client: ApiClient, s: Suggestion) {
  hoverStart(client, (cur) => mergeSuggestion(cur.draft!, s));
}

// esApplyUserPreset lays a saved "My presets" look over the photo's current
// draft: only the preset's included sections move (applyUserPreset —
// sections filter, relative deltas, exposure re-anchored to the photo's
// calibrated baseline), so geometry, masks, retouch spots, and every
// section the preset doesn't carry keep the photo's own values. Shared by
// the Presets tab and the Ctrl+Shift+1..9 shortcuts.
export function esApplyUserPreset(
  client: ApiClient,
  preset: UserPreset,
  opts?: { onMasksNeedDownload?: (kind: AIKindType) => void },
) {
  const s = useEditSession.getState();
  if (!s.draft || s.photoId == null) return;
  const autoSecs = presetAutoSections(preset);
  if (autoSecs.length === 0) {
    const base = s.draft;
    const result = applyUserPreset(base, preset, s.baseExpEV);
    esApplyParams(client, result, { label: preset.name });
    // AFTER esApplyParams — it clears lastPresetApply (any whole-draft
    // replacement invalidates a stale scrubber).
    setState({ lastPresetApply: { photoId: s.photoId, base, result, name: preset.name, amount: 1 } });
    runPresetMasks(client, preset, opts);
    return;
  }
  // Adaptive preset: the backend computes the photo's own auto for the
  // preset's sections first, then the stored creative diff lands on top
  // (relative overlay). Same supersede guard + instant-preview pattern as
  // esApplyAutoPreset.
  esFlushDraft();
  const gen = ++applyGen;
  const pid = s.photoId;
  const base = useEditSession.getState().draft!;
  void (async () => {
    try {
      const resolved = await autoAdjust(client, pid, base, autoSecs);
      const cur = useEditSession.getState();
      if (applyGen !== gen || cur.photoId !== pid) return; // superseded
      const result = applyUserPreset(resolved, preset, cur.baseExpEV);
      esApplyParamsPreview(client, result, preset.name);
      setState({ lastPresetApply: { photoId: pid, base, result, name: preset.name, amount: 1 } });
      // Masks only AFTER the look landed — running them concurrently would
      // let the late look replace the draft and drop the appended masks.
      runPresetMasks(client, preset, opts);
    } catch (err) {
      toast.error(`Auto adjust failed: ${(err as Error).message}`);
    }
  })();
}

// runPresetMasks fires the preset's AI-mask phase (fire-and-forget behind
// the look apply). Without a consent hook a missing model just reports how
// to get it — a keyboard apply must not pop a dialog the view isn't
// prepared for.
function runPresetMasks(
  client: ApiClient,
  preset: UserPreset,
  opts?: { onMasksNeedDownload?: (kind: AIKindType) => void },
) {
  void esApplyPresetMasks(client, preset).then((r) => {
    if (r.status !== 'needs-download') return;
    if (opts?.onMasksNeedDownload) opts.onMasksNeedDownload(r.kind!);
    else toast(`Preset applied — AI masks skipped (the ${r.kind} model isn't downloaded yet)`);
  });
}

// esApplyPresetMasks is a preset apply's second phase: re-run detection for
// the preset's AI-mask RECIPES on the focused photo (GenerateAIMap is
// idempotent — instant when the map is on disk) and append them to the
// draft as one further history entry "Name · masks". The look stays applied
// whatever happens here; a missing model resolves to 'needs-download' so
// the caller can ask consent and retry with allowDownload.
//
// The recipes persist to the WHOLE selection (applyIds), but a recipe renders
// as a no-op until the photo's own map exists — so after the persist, the rest
// of the selection's maps are materialized server-side as one background task
// (GenerateAIMaps). Each landed map repaints its grid thumb via the
// AIMapsGeneratedEvent broadcast → bumpImgBust (wired in main.tsx).
export async function esApplyPresetMasks(
  client: ApiClient,
  preset: UserPreset,
  opts?: { allowDownload?: boolean },
): Promise<{ status: 'none' | 'done' | 'needs-download'; kind?: AIKindType }> {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft) return { status: 'none' };
  // Person masks ride along like any AI recipe: "person 2" on another photo
  // means THAT photo's second person from the left — deterministic but
  // semantically arbitrary. Kept by design (stripping would surprise more);
  // the mask is visible and deletable where it misses. Range (luma/colour)
  // masks travel too but need no model — they append verbatim.
  const recipes = (preset.params.masks ?? []).filter((m) => m.type === 'ai' && m.aiKind);
  const rangeRecipes = (preset.params.masks ?? []).filter((m) => m.type === 'range');
  if (recipes.length === 0 && rangeRecipes.length === 0) return { status: 'none' };
  const pid = s.photoId;
  const kinds = [...new Set(recipes.map((m) => m.aiKind!))];
  const vers: Partial<Record<AIKindType, string>> = {};
  for (const kind of kinds) {
    try {
      const res = await generateAIMap(client, pid, kind, opts?.allowDownload ?? false);
      vers[kind] = res.mapVer;
      aiMapsFired.add(`${pid}|${kind}`); // this map is settled — the commit below need not re-ask
    } catch (err) {
      if (isModelNotDownloaded(err)) return { status: 'needs-download', kind };
      toast.error(`AI masks failed: ${(err as Error).message}`);
      return { status: 'none' };
    }
    if (useEditSession.getState().photoId !== pid) return { status: 'none' }; // superseded
  }
  const cur = useEditSession.getState();
  if (cur.photoId !== pid || !cur.draft) return { status: 'none' };
  const masks = [
    // APPEND to the photo's own masks — replacing would destroy local work;
    // a duplicate from re-applying is visible and deletable.
    ...(cur.draft.masks ?? []),
    ...recipes.map((m) => ({ ...m, mapVer: vers[m.aiKind!]! })),
    ...rangeRecipes,
  ];
  // esApplyParams clears the amount scrubber (any whole-draft replacement
  // does); the look phase's record stays valid — scrubs preserve the
  // draft's masks — so restore it across the mask commit.
  const keep = cur.lastPresetApply;
  // The focused photo's maps just ran above; the rest of the selection gets the
  // same recipes persisted but no inference — esApplyParams' esEnsureAIMaps
  // pass kicks that off once the save has landed (consent is already settled
  // here, so it needs no allowDownload of its own).
  esApplyParams(client, { ...cur.draft, masks }, { label: `${preset.name} · masks` });
  if (keep && keep.photoId === pid) setState({ lastPresetApply: keep });
  return { status: 'done' };
}

// resolveUserPreset computes the params applying `preset` to the current
// draft would produce — including the autoAdjust round trip for adaptive
// presets. Shared by the hover preview and the preset thumbnails.
export async function resolveUserPreset(
  client: ApiClient,
  photoId: number,
  draft: Params,
  preset: UserPreset,
  baseExpEV: number,
): Promise<Params> {
  const autoSecs = presetAutoSections(preset);
  const base = autoSecs.length > 0 ? await autoAdjust(client, photoId, draft, autoSecs) : draft;
  return applyUserPreset(base, preset, baseExpEV);
}

function presetAutoSections(preset: UserPreset): AutoSection[] {
  const known: AutoSection[] = ['tone', 'wb', 'color'];
  return known.filter((s) => (preset.autoSections ?? []).includes(s));
}

// esHoverPreset previews a preset on the loupe while its card is hovered:
// after a short debounce (sweeping across cards must not render per card)
// the merged params land in hoverParams — a pure render override; draft,
// history, and persistence stay untouched. Suppressed while a modal-ish
// tool owns the loupe (WB picker, crop, heal, mask paint, keyboard adjust).
// Adaptive presets resolve their autoAdjust inside the debounce.
export function esHoverPreset(client: ApiClient, preset: UserPreset) {
  hoverStart(client, (cur) => resolveUserPreset(client, cur.photoId!, cur.draft!, preset, cur.baseExpEV));
}

// esHoverAutoPreset is esHoverPreset for creative-auto presets: the debounce
// also absorbs the autoAdjust round trip, and the gen token drops a stale
// resolution (card left, photo switched) on the floor.
export function esHoverAutoPreset(client: ApiClient, preset: AutoPreset) {
  hoverStart(client, (cur) => computePresetParams(client, cur.photoId!, cur.draft!, preset));
}

function hoverSuppressed(s: EditSessionState): boolean {
  return s.wbPicking || s.rangePicking || s.cropping || s.healing || s.maskPaint || s.keyAdjust;
}

function hoverStart(client: ApiClient, resolve: (s: EditSessionState) => Params | Promise<Params>) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft || hoverSuppressed(s)) return;
  window.clearTimeout(hoverTimer);
  const gen = ++hoverGen;
  const pid = s.photoId;
  hoverTimer = window.setTimeout(() => {
    void (async () => {
      const cur = useEditSession.getState();
      if (hoverGen !== gen || cur.photoId !== pid || !cur.draft || hoverSuppressed(cur)) return;
      try {
        const params = await resolve(cur);
        const now = useEditSession.getState();
        if (hoverGen !== gen || now.photoId !== pid || hoverSuppressed(now)) return;
        setState({ hoverParams: params });
        schedulePreview(client, 'draft'); // low-res only — hovers never settle
      } catch {
        // autoAdjust failed — the hover just doesn't preview
      }
    })();
  }, 150);
}

// esHoverEnd cancels a pending hover and, if one was showing, reverts the
// loupe to the draft: instant low-res frame with the sharp settle queued
// behind it.
export function esHoverEnd(client: ApiClient) {
  window.clearTimeout(hoverTimer);
  hoverGen++;
  const s = useEditSession.getState();
  if (s.hoverParams == null) return;
  setState({ hoverParams: null });
  schedulePreview(client, 'draft');
  schedulePreview(client, 'settle');
}

// esSetPresetAmount scrubs the strength of the last preset apply: the draft
// becomes the base→result lerp at t (0 = pre-preset, 1 = as applied, up to
// 2 = doubled, clamped per-field). Renders the instant low-res frame per
// move; the persist + history amend rides a short idle (esCommitPresetAmount)
// so a scrub lands as ONE amended entry, not an undo-stack spam.
export function esSetPresetAmount(client: ApiClient, t: number) {
  const s = useEditSession.getState();
  const a = s.lastPresetApply;
  if (!a || s.photoId !== a.photoId || !s.draft) return;
  // The draft owns the photo's local adjustments — a preset's mask phase
  // may have appended masks AFTER base/result were captured, and the scrub
  // must not lerp them away.
  const params = { ...lerpPresetAmount(a.base, a.result, t), masks: s.draft.masks, spots: s.draft.spots };
  setState({ draft: params, lastPresetApply: { ...a, amount: t } });
  schedulePreview(client, 'draft');
  window.clearTimeout(amountTimer);
  amountTimer = window.setTimeout(() => esCommitPresetAmount(client), 400);
}

// esCommitPresetAmount persists the scrubbed strength and AMENDS the preset's
// history entry in place (label "Name · 85%") — the apply stays one undoable
// step whatever the final amount.
export function esCommitPresetAmount(client: ApiClient) {
  window.clearTimeout(amountTimer);
  const s = useEditSession.getState();
  const a = s.lastPresetApply;
  if (!a || s.photoId == null || s.photoId !== a.photoId || !s.draft) return;
  const params = s.draft;
  const label = a.amount === 1 ? a.name : `${a.name} · ${Math.round(a.amount * 100)}%`;
  setState((st) => {
    const h = st.history[a.photoId];
    if (!h) return {};
    const stack = [...h.stack];
    // Keep the original seq: the amended apply stays one undoable step at
    // its original position in the cross-stack undo order.
    stack[h.index] = { ...stack[h.index], params, label };
    return { history: { ...st.history, [a.photoId]: { ...h, stack } } };
  });
  const ids = s.applyIds.length > 1 ? s.applyIds : [s.photoId];
  void persist(client, params, ids);
  schedulePreview(client, 'settle');
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
// draft updates but nothing is committed until Done. Every pick in a session
// is sampled against wbPickBase, the draft as it was when the picker opened,
// so two spots can be compared by clicking one then the other — sampling the
// live frame would fold each pick into the basis of the next and force a
// pick/undo/pick dance. The eyedropper stays open for repeated sampling; Done
// keeps the value, Reset / Cancel restore the pre-picker draft.
export async function esPickWB(client: ApiClient, x: number, y: number) {
  const s = useEditSession.getState();
  if (s.photoId == null || !s.draft) return;
  try {
    const params = await pickWhiteBalance(client, s.photoId, s.draft, s.wbPickBase ?? s.draft, x, y);
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
  revokePickFrame();
  setState({ wbPicking: false, wbPickBase: null, wbPickFrameUrl: null });
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
  revokePickFrame();
  setState({ wbPicking: false, wbPickBase: null, wbPickFrameUrl: null });
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
