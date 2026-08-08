// The develop control catalog: neutral params, the ControlId union, and each
// control's range/values with get/set mapping between UI values and stored
// params (bright/gamma/shadow store 0 for "default", Kelvin flips the WB
// mode). A leaf module — editSession (keyboard stepping) and dials (toolbar
// mini dials) both build on it, and it must import nothing that could pull
// them back in.
import type { Mask, MaskAdjust, Params } from '@/api/edit';

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
  hslHue: [0, 0, 0, 0, 0, 0, 0, 0],
  hslSat: [0, 0, 0, 0, 0, 0, 0, 0],
  hslLum: [0, 0, 0, 0, 0, 0, 0, 0],
  vignette: 0,
  texture: 0,
  clarity: 0,
  dehaze: 0,
  sharpen: 0,
  // The server stores the default as "" — same generated-union lie as wbMode.
  demosaic: '' as Params['demosaic'],
  caRed: 0,
  caBlue: 0,
  // Lens profile correction. The stored default is "auto with the profile's
  // own strength" — an uncorrected frame is the deviation here, not the
  // neutral, because a profile describes what the lens did rather than an
  // effect someone chose. Same generated-union lie as wbMode for the mode.
  lensMode: '' as Params['lensMode'],
  lensDistortion: 0,
  lensVignetting: 0,
  lensCA: 0,
  rotate: 0,
  flipH: false,
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
  | 'lensDistortion'
  | 'lensVignetting'
  | 'lensCA'
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
  expEV: { kind: 'numeric', min: -5, max: 5, step: 0.05, bigStep: 0.25, get: (p) => p.expEV, set: (v) => ({ expEV: v }) },
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
  // Offsets from the profile's own measurement: 0 is the full correction,
  // -1 switches that component off, +1 doubles it.
  lensDistortion: { kind: 'numeric', min: -1, max: 1, step: 0.05, bigStep: 0.25, get: (p) => p.lensDistortion ?? 0, set: (v) => ({ lensDistortion: v }) },
  lensVignetting: { kind: 'numeric', min: -1, max: 1, step: 0.05, bigStep: 0.25, get: (p) => p.lensVignetting ?? 0, set: (v) => ({ lensVignetting: v }) },
  lensCA: { kind: 'numeric', min: -1, max: 1, step: 0.05, bigStep: 0.25, get: (p) => p.lensCA ?? 0, set: (v) => ({ lensCA: v }) },
  cropAngle: { kind: 'numeric', min: -15, max: 15, step: 0.1, bigStep: 1, get: (p) => p.cropAngle, set: (v) => ({ cropAngle: v }) },
};

// Panel order (EditPanel top→bottom) for walking the focused control with
// Ctrl+↑/↓, and the canonical order of the toolbar dial catalog (lib/dials).
// wbTemp and wbKelvin swap depending on the WB mode, so the walk only ever
// visits the temperature dial that is actually rendered.
export const CONTROL_ORDER: ControlId[] = [
  'cropAngle',
  'expEV', 'expPreserve', 'bright', 'gamma', 'shadow',
  'contrast', 'whites', 'blacks', 'toneShadows', 'toneHighlights',
  'clarity', 'texture', 'dehaze',
  'wbMode', 'wbTemp', 'wbKelvin', 'wbTint',
  'saturation', 'vibrance',
  'splitShadowHue', 'splitShadowAmt', 'splitHighlightHue', 'splitHighlightAmt',
  'vignette',
  'sharpen', 'highlight', 'nrThreshold', 'fbddNoiseRd', 'medPasses',
  'demosaic', 'caRed', 'caBlue',
  'lensDistortion', 'lensVignetting', 'lensCA',
];

// Human labels for every editable param, keyed to match the develop panel's
// slider names. Used to name undo-history entries ("Exposure", "Vignette")
// by diffing the params that changed between two snapshots.
const PARAM_LABELS: Partial<Record<keyof Params, string>> = {
  expEV: 'Exposure',
  expPreserve: 'Preserve highlights',
  bright: 'Brightness',
  gamma: 'Gamma',
  shadow: 'Shadow slope',
  contrast: 'Contrast',
  whites: 'Whites',
  blacks: 'Blacks',
  toneShadows: 'Shadows',
  toneHighlights: 'Highlights',
  clarity: 'Clarity',
  texture: 'Texture',
  dehaze: 'Dehaze',
  wbMode: 'White balance',
  wbMul: 'White balance',
  wbTemp: 'Temperature',
  wbKelvin: 'Temperature',
  wbTint: 'Tint',
  saturation: 'Saturation',
  vibrance: 'Vibrance',
  splitShadowHue: 'Split shadow',
  splitShadowAmt: 'Split shadow',
  splitHighlightHue: 'Split highlight',
  splitHighlightAmt: 'Split highlight',
  hslHue: 'Mixer hue',
  hslSat: 'Mixer saturation',
  hslLum: 'Mixer luminance',
  vignette: 'Vignette',
  sharpen: 'Sharpen',
  highlight: 'Highlight recovery',
  nrThreshold: 'Noise reduction',
  fbddNoiseRd: 'FBDD denoise',
  medPasses: 'Median passes',
  demosaic: 'Demosaic',
  caRed: 'CA red/cyan',
  caBlue: 'CA blue/yellow',
  lensMode: 'Lens correction',
  lensDistortion: 'Lens distortion',
  lensVignetting: 'Lens vignetting',
  lensCA: 'Lens CA',
  rotate: 'Rotate',
  flipH: 'Flip',
  cropX: 'Crop',
  cropY: 'Crop',
  cropW: 'Crop',
  cropH: 'Crop',
  cropAngle: 'Straighten',
  spots: 'Retouch',
};

export function paramLabel(key: keyof Params): string {
  return PARAM_LABELS[key] ?? String(key);
}

// --- Local adjustment masks ---
// Masks live in Params.masks; each carries its own MaskAdjust slider set,
// deliberately OUTSIDE the ControlId/CONTROL_ORDER machinery (those map flat
// Params fields for keyboard walking and dials) — the mask panel renders
// these specs directly for whichever mask is selected.

export type MaskControlId = keyof MaskAdjust;

export interface MaskControlSpec {
  label: string;
  min: number;
  max: number;
  step: number;
  bigStep: number;
  // Display convention. Default is the ±1 slider shown as ±100; 'ev' shows
  // the raw stops, 'deg' the raw degrees. MaskRow derives scale and format
  // from this rather than special-casing each control.
  unit?: 'ev' | 'deg';
}

// Panel order. Ranges mirror the server's Normalize clamps (edit.go).
export const MASK_CONTROL_ORDER: MaskControlId[] = [
  'expEV', 'contrast', 'toneHighlights', 'toneShadows', 'whites', 'blacks',
  'temp', 'tint', 'saturation',
];

// Spatial effects, rendered as their own collapsible sub-block: these gather
// neighbouring pixels rather than remapping each one, so they read as a
// different kind of control and would drown the tone sliders in one flat list.
// Grouped by what they do: the three defocus dials, then the two that add
// light, then the two that give the region a character of its own.
export const MASK_FX_ORDER: MaskControlId[] = [
  'blur', 'motionBlur', 'zoomBlur', 'glow', 'streaks', 'prism', 'mosaic', 'fxAngle',
];

// Every mask slider in panel order — what the keyboard walk steps through.
export const MASK_ALL_CONTROLS: MaskControlId[] = [...MASK_CONTROL_ORDER, ...MASK_FX_ORDER];

const pm1 = { min: -1, max: 1, step: 0.02, bigStep: 0.1 };
const fx01 = { min: 0, max: 1, step: 0.02, bigStep: 0.1 };
export const MASK_CONTROL_SPECS: Record<MaskControlId, MaskControlSpec> = {
  expEV: { label: 'Exposure', min: -4, max: 4, step: 0.05, bigStep: 0.25, unit: 'ev' },
  contrast: { label: 'Contrast', ...pm1 },
  toneHighlights: { label: 'Highlights', ...pm1 },
  toneShadows: { label: 'Shadows', ...pm1 },
  whites: { label: 'Whites', ...pm1 },
  blacks: { label: 'Blacks', ...pm1 },
  temp: { label: 'Temperature', ...pm1 },
  tint: { label: 'Tint', ...pm1 },
  saturation: { label: 'Saturation', ...pm1 },
  blur: { label: 'Blur', ...fx01 },
  motionBlur: { label: 'Motion blur', ...fx01 },
  zoomBlur: { label: 'Zoom blur', ...fx01 },
  glow: { label: 'Glow', ...fx01 },
  streaks: { label: 'Light streaks', ...fx01 },
  prism: { label: 'Prism', ...pm1 },
  mosaic: { label: 'Mosaic', ...fx01 },
  fxAngle: { label: 'Direction', min: 0, max: 180, step: 1, bigStep: 15, unit: 'deg' },
};

// Shape controls are the sliders that live on the MASK itself rather than in
// mask.adjust — they change which pixels the mask selects, not what happens to
// them. They render ABOVE the adjust sliders (AIShapeRows/RangeShapeRows), so
// the keyboard walk must enter them first or ↓ appears to skip the top of the
// panel. Accessors, because the value's home and its "unset" spelling differ
// per control. Two-thumb windows (depth, luminance) and the circular hue pair
// stay out: the scalar step model cannot express them.
export type MaskShapeControlId = 'threshold' | 'rangeSatMin' | 'feather';

export interface MaskShapeSpec {
  label: string;
  min: number;
  max: number;
  step: number;
  bigStep: number;
  get(m: Mask): number;
  set(v: number): Partial<Mask>;
}

export const MASK_SHAPE_SPECS: Record<MaskShapeControlId, MaskShapeSpec> = {
  // Raw 0 means "the server's 0.5 default", so stepping starts from what the
  // slider shows; the floor keeps it off the 0 that would mean default again.
  threshold: {
    label: 'Threshold', min: 0.02, max: 1, step: 0.01, bigStep: 0.05,
    get: (m) => m.threshold || 0.5, set: (v) => ({ threshold: v }),
  },
  rangeSatMin: {
    label: 'Min saturation', min: 0, max: 1, step: 0.01, bigStep: 0.05,
    get: (m) => m.rangeSatMin ?? 0, set: (v) => ({ rangeSatMin: v }),
  },
  feather: {
    label: 'Edge feather', min: 0, max: 1, step: 0.01, bigStep: 0.05,
    get: (m) => m.feather ?? 0, set: (v) => ({ feather: v }),
  },
};

// The shape sliders one mask actually shows, in panel order — mirrors what
// AIShapeRows/RangeShapeRows render, so the focus ring never lands on a row
// that isn't there.
export function maskShapeOrder(m: Mask): MaskShapeControlId[] {
  if (m.type === 'ai') {
    return m.aiKind === 'subject' || m.aiKind === 'background'
      ? ['threshold', 'feather']
      : ['feather'];
  }
  if (m.type === 'range') return ['rangeSatMin', 'feather'];
  return [];
}

// What the mask panel's keyboard focus can sit on: an adjust slider or a shape
// slider.
export type MaskPanelControlId = MaskControlId | MaskShapeControlId;

export function isMaskShapeControl(c: MaskPanelControlId): c is MaskShapeControlId {
  return c in MASK_SHAPE_SPECS;
}

// Must walk MASK_ALL_CONTROLS, not just the tone block: a blur-only mask is a
// real edit and would otherwise report as neutral (no changed dot, and the
// server would still render it — a mismatch the compiler can't catch).
export function maskAdjustIsNeutral(a: MaskAdjust | undefined): boolean {
  if (!a) return true;
  return MASK_ALL_CONTROLS.every((k) => (a[k] ?? 0) === 0);
}

// maskHasFX mirrors the server's MaskAdjust.HasFX: fxAngle alone is inert (the
// server zeroes it), so it never counts as an effect.
export function maskHasFX(a: MaskAdjust | undefined): boolean {
  if (!a) return false;
  return MASK_FX_ORDER.some((k) => k !== 'fxAngle' && (a[k] ?? 0) !== 0);
}

// maskCanRemove mirrors the server's Mask.MaskRemoveAllowed — KEEP IN SYNC:
// normalizeMasks clears the flag on anything this rejects, so a pill offered
// here that the server refuses would silently un-toggle itself. Removal needs
// a binary, bounded region derivable from the params alone, which leaves out
// the soft/unbounded types (linear, radial, depth, range) and any effectively
// inverted mask, whose region is everything *but* the subject.
export function maskCanRemove(m: Mask): boolean {
  if (m.type === 'brush') return !m.invert && (m.strokes?.length ?? 0) > 0;
  if (m.type !== 'ai') return false;
  if (!!m.invert !== (m.aiKind === 'background')) return false;
  // Keyed on the MAP kind (background samples the subject matte), so an
  // inverted Background mask — which selects the subject — qualifies too.
  const mapKind = m.aiKind === 'background' ? 'subject' : m.aiKind;
  return mapKind === 'subject' || mapKind === 'person' || mapKind === 'class';
}

export const MASK_TYPE_LABELS: Record<string, string> = {
  linear: 'Linear gradient',
  radial: 'Radial',
  brush: 'Brush',
  ai: 'AI',
  range: 'Range',
};

export function maskLabel(m: Mask, index: number): string {
  if (m.type === 'ai') {
    // Person masks label by instance ID (their classId), not list position,
    // so the row reads the same as the chip that added it.
    if (m.aiKind === 'person') return `Person ${m.classId ?? 0}`;
    const kind =
      m.aiKind === 'subject' ? 'Subject'
      : m.aiKind === 'background' ? 'Background'
      : m.aiKind === 'depth' ? 'Depth'
      : m.aiKind === 'class' ? (AI_CATEGORY_NAMES[m.classId ?? 0] ?? 'Class')
      : 'AI';
    return `${kind} ${index + 1}`;
  }
  return `${MASK_TYPE_LABELS[m.type] ?? 'Mask'} ${index + 1}`;
}

// AI_CATEGORY_NAMES mirrors internal/aimask CategoryNames — category IDs are
// stable API (they live in saved edit params), so index = ID.
export const AI_CATEGORY_NAMES = [
  'Other', 'Sky', 'People', 'Foliage', 'Water', 'Ground',
  'Architecture', 'Mountains & rocks', 'Vehicles', 'Animals',
] as const;

// aiMask builds a freshly generated AI mask. mapVer comes from
// Edits.GenerateAIMap — it pins the model that produced the map, so the
// server renders only against a matching map file. Depth seeds a near-range
// window (1 = nearest); subject relies on the server defaults (threshold
// 0.5, model edges); class masks get a light feather to soften the label
// map's hard boundaries.
export const DEPTH_WINDOW_DEFAULT = { depthLo: 0.6, depthHi: 1 } as const;

export function aiMask(kind: 'subject' | 'depth', mapVer: string): Mask {
  if (kind === 'depth') {
    return { type: 'ai', aiKind: kind, mapVer, ...DEPTH_WINDOW_DEFAULT, feather: 0.3, adjust: {} };
  }
  return { type: 'ai', aiKind: kind, mapVer, adjust: {} };
}

// backgroundMask is the one-click background-separation recipe: the background
// kind (the subject matte, inverted by the server — see edit.AIBackground), so
// the row reads "Background" with the Invert pill free for the photographer to
// flip back to the subject. Pre-loaded with a light bloom, anamorphic streaks
// and a chromatic fringe. No blur —
// a heavy default defocus smears the whole frame into mush and reads as a
// filter; the glow/streaks/prism trio separates the background while it
// still shows what is there. Everything after is the photographer's to tune.
export function backgroundMask(mapVer: string): Mask {
  return {
    type: 'ai', aiKind: 'background', mapVer,
    adjust: { glow: 0.1, streaks: 0.2, prism: 0.6, fxAngle: 25 },
  };
}

export function aiClassMask(classId: number, mapVer: string): Mask {
  return { type: 'ai', aiKind: 'class', mapVer, classId, feather: 0.25, adjust: {} };
}

// aiPersonMask targets one person in the instance map; classId doubles as
// the instance ID (1..N, left to right). Lighter feather than class masks —
// instance edges hug the person, and guided refinement sharpens hi-res.
export function aiPersonMask(instanceId: number, mapVer: string): Mask {
  return { type: 'ai', aiKind: 'person', mapVer, classId: instanceId, feather: 0.15, adjust: {} };
}

// Range-mask window defaults: both dimensions fully open (select everything)
// until the user narrows the luminance/hue sliders or picks a colour, the
// Lightroom luminance-range convention. neutral targets for the range sliders.
export const RANGE_LUMA_DEFAULT = { rangeLumaLo: 0, rangeLumaHi: 1 } as const;
export const RANGE_HUE_DEFAULT = { rangeHueLo: 0, rangeHueHi: 1 } as const;

// Default geometry for a freshly added mask: centered and clearly visible,
// so the user immediately sees what the handles do. Fractions of the
// oriented frame, matching the server model.
export function defaultMask(type: Mask['type']): Mask {
  switch (type) {
    case 'linear':
      // Top-down sky gradient: full above 30%, gone by 60%.
      return { type, x0: 0.5, y0: 0.3, x1: 0.5, y1: 0.6, adjust: {} };
    case 'radial':
      return { type, cx: 0.5, cy: 0.5, rx: 0.3, ry: 0.25, feather: 0.5, adjust: {} };
    case 'range':
      return { type, ...RANGE_LUMA_DEFAULT, ...RANGE_HUE_DEFAULT, rangeSatMin: 0, feather: 0.25, adjust: {} };
    default:
      return { type: 'brush', strokes: [], adjust: {} };
  }
}
