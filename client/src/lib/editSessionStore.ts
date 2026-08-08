// The edit session's state: what the panel and the loupe read while a photo
// is open. Its own module so the render scheduler can reach it without
// importing editSession.ts, which imports the scheduler — a cycle that would
// otherwise only work by accident of when each module is first touched.
//
// editSession.ts re-exports useEditSession, so the twenty files that read it
// carry on importing it from there.

import type { AICategory, AIInstance } from '@/api/edits';
import type { AIKindType, Params } from '@/api/edit';
import type { ControlId, MaskPanelControlId } from '@/lib/controlSpecs';
import { create } from 'zustand';
// SpotMode is the retouch fill mode a new spot is created with (and the toggle
// the panel offers per spot). Mirrors the server's edit.SpotMode ("" = heal).
// 'fill' inpaints the region with the ML model — no source patch.
export type SpotMode = 'heal' | 'clone' | 'fill';

// The two AI maps whose regions can be picked (label maps with per-region
// IDs): person instances and scene categories.
export type AIPickKind = 'person' | 'class';

export interface Preview {
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
  // Global undo ordering vs the cull history (see undoSeq.ts). 0 on the
  // 'Original' baseline, which is never an undo candidate.
  seq: number;
}

export interface HistoryEntry {
  stack: HistorySnapshot[];
  index: number;
}

export interface EditSessionState {
  photoId: number | null;
  applyIds: number[]; // commit targets; >1 when multiple photos selected
  // The focused photo's measured camera-mimic exposure baseline
  // (photo.baseExpEV; 0 = unmeasured). Presets re-anchor exposure to it.
  baseExpEV: number;
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
  // Reset/Cancel, and the state the server samples every pick against. Null
  // when the picker is closed.
  wbPickBase: Params | null;
  // Object URL of the frame the server samples for wbPickBase — the pipette's
  // magnifier shows THIS rather than the live preview, so the RGB readout is
  // the pixels the next pick is actually computed from. Null until it loads.
  wbPickFrameUrl: string | null;
  // Colour eyedropper for a range mask: while on, loupe clicks sample the
  // developed colour and seed the active range mask's hue window (each pick is
  // one committed history entry; the picker stays open for repeated sampling).
  rangePicking: boolean;
  // Transient render override while a preset card is hovered: the loupe
  // paints these params instead of the draft. Never touches the draft,
  // history, or persistence — clearing it reverts by construction.
  hoverParams: Params | null;
  // The last preset apply, kept for the post-apply amount scrubber: lerping
  // between base (the pre-apply draft) and result re-derives any strength
  // 0..200%. Invalidated by a photo switch, any other edit, undo, or reset.
  lastPresetApply: { photoId: number; base: Params; result: Params; name: string; amount: number } | null;
  cropping: boolean; // crop overlay active: loupe shows the uncropped frame
  // Heal/retouch tool active: pointer-down on the loupe places or grabs a spot
  // (draft.spots) instead of panning.
  healing: boolean;
  // The selected retouch spot (index into draft.spots): its dest+source circles
  // and connector show on the loupe and its row expands in the Retouch group.
  activeSpot: number | null;
  // Fill mode a newly placed spot is created with (the panel's clone/heal
  // toggle for new spots).
  spotMode: SpotMode;
  // Retouch region tool: 'spot' places circles (click / click-drag to size),
  // 'brush' paints an arbitrary stroke region (Kind "stroke" spots).
  spotTool: 'spot' | 'brush';
  // Heal brush settings for the next stroke (the mask brush's twins; radius is
  // a fraction of the frame long edge, the server's stroke model).
  spotBrushRadius: number;
  spotBrushFeather: number;
  // Visualize spots: high-pass dust view over the loupe while healing (A key).
  spotVisualize: boolean;
  spotVisualizeThreshold: number; // 0..1, higher = more sensitive
  // Spot indices with an ML fill generation in flight (row spinner). Indices
  // are best-effort — a concurrent removal shifts them, and the busy marks
  // are cleared wholesale when each generation settles.
  fillBusy: number[];
  // Spot index whose fill needs the model downloaded: set when GenerateFill
  // refuses without consent, drives the download dialog in the Retouch group.
  fillConsent: number | null;
  // The same two, for mask removals (Remove pill). Kept separate from the spot
  // pair rather than folded into a tagged union: the dialogs live in different
  // panel groups and decline reverts a different field, so one shared slot
  // would have to be discriminated at every use.
  maskFillBusy: number[];
  maskFillConsent: number | null;
  // The selected local-adjustment mask (index into draft.masks): its overlay
  // handles show on the loupe and its sliders expand in the Masks tab.
  activeMask: number | null;
  // The keyboard-focused slider of the active mask (Masks-tab counterpart of
  // activeControl): ↑/↓ walk it across every mask's sliders, +/- adjusts.
  // Covers the shape sliders (threshold, feather, …) as well as the adjust
  // ones — they render above the adjust block, so the walk must reach them.
  activeMaskControl: MaskPanelControlId | null;
  // Brush paint mode: pointer strokes on the loupe paint into the active
  // (brush) mask instead of panning.
  maskPaint: boolean;
  // Mask row currently hovered in the Masks panel: the loupe shows that
  // mask's red weight tint while set (see MaskHoverTint).
  tintMask: number | null;
  // Spot row currently hovered in the Retouch panel: the loupe tints that
  // spot's area red while set (see SpotHoverTint).
  tintSpot: number | null;
  // AI region-mask detections, kept per kind until the photo changes. Both
  // chip rows (people + scene) can show at once; hovering a chip or — while
  // armed — the loupe tints that region. mapVer pins the generating model.
  aiDetect: {
    person: { mapVer: string; instances: AIInstance[] } | null;
    class: { mapVer: string; categories: AICategory[] } | null;
  };
  // The armed image-pick tool: while set, hovering the loupe tints the region
  // under the cursor and clicking adds its mask. null = not armed (chips may
  // still be visible). Panning by click-drag keeps working while armed.
  aiPickArmed: AIPickKind | null;
  // The region currently hovered (loupe pointer or a panel chip); the
  // AIPickOverlay renders its tint.
  aiHover: { kind: AIPickKind; id: number } | null;
  // An AI kind the focused photo's masks reference but whose model isn't
  // downloaded (esEnsureAIMaps hit the consent sentinel). The Local panel
  // turns it into the download dialog; cleared when consumed or on a photo
  // switch, so a mask that can't render never nags more than once.
  aiMapRestore: AIKindType | null;
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
  baseExpEV: 0,
  draft: null,
  lastDraft: null,
  loading: false,
  history: {},
  activeControl: null,
  rendering: 0,
  preview: null,
  wbPicking: false,
  wbPickBase: null,
  wbPickFrameUrl: null,
  rangePicking: false,
  hoverParams: null,
  lastPresetApply: null,
  cropping: false,
  healing: false,
  activeSpot: null,
  spotMode: 'heal',
  spotTool: 'spot',
  spotBrushRadius: 0.02,
  spotBrushFeather: 0.5,
  spotVisualize: false,
  spotVisualizeThreshold: 0.4,
  fillBusy: [],
  fillConsent: null,
  maskFillBusy: [],
  maskFillConsent: null,
  activeMask: null,
  activeMaskControl: null,
  maskPaint: false,
  tintMask: null,
  tintSpot: null,
  aiDetect: { person: null, class: null },
  aiPickArmed: null,
  aiHover: null,
  aiMapRestore: null,
  brushRadius: 0.05,
  brushFeather: 0.5,
  brushFlow: 1,
  brushErase: false,
  keyAdjust: false,
}));
