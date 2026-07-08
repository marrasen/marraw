// The develop control catalog: neutral params, the ControlId union, and each
// control's range/values with get/set mapping between UI values and stored
// params (bright/gamma/shadow store 0 for "default", Kelvin flips the WB
// mode). A leaf module — editSession (keyboard stepping) and dials (toolbar
// mini dials) both build on it, and it must import nothing that could pull
// them back in.
import type { Params } from '@/api/edits';

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
  vignette: 'Vignette',
  sharpen: 'Sharpen',
  highlight: 'Highlight recovery',
  nrThreshold: 'Noise reduction',
  fbddNoiseRd: 'FBDD denoise',
  medPasses: 'Median passes',
  demosaic: 'Demosaic',
  caRed: 'CA red/cyan',
  caBlue: 'CA blue/yellow',
  cropX: 'Crop',
  cropY: 'Crop',
  cropW: 'Crop',
  cropH: 'Crop',
  cropAngle: 'Straighten',
};

export function paramLabel(key: keyof Params): string {
  return PARAM_LABELS[key] ?? String(key);
}
