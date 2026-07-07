import type { Params } from '@/api/edits';
import { CONTROL_ORDER, CONTROL_SPECS, NEUTRAL, type ControlId } from '@/lib/controlSpecs';

// The develop dials a user can pin to the Cull confirm bar and the Develop
// quick dock (Settings → Toolbars): the full develop control catalog, with
// ranges, values, and read/write behaviour derived from CONTROL_SPECS so the
// dials can never drift from the panel (bright/gamma/shadow store 0 for
// "default", stepping Kelvin flips the WB mode — the specs own all of that).
// Order follows CONTROL_ORDER (the panel top→bottom), which is also the
// order pinned dials render in.
export type DialKey = ControlId;

const pct = (v: number) => (v === 0 ? '0' : `${v > 0 ? '+' : ''}${Math.round(v * 100)}`);
const ev = (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(2)}`;
const pct0 = (v: number) => String(Math.round(v * 100));
const mult = (v: number) => `${v.toFixed(2)}×`;
const num2 = (v: number) => v.toFixed(2);
const num1 = (v: number) => v.toFixed(1);
const kelvin = (v: number) => `${Math.round(v)}K`;
const hue = (v: number) => `${Math.round(v)}°`;
const angle = (v: number) => (v === 0 ? '0°' : `${v > 0 ? '+' : ''}${v.toFixed(1)}°`);
const int = (v: number) => String(Math.round(v));

interface DialBase {
  key: DialKey;
  label: string;
  // Settings-picker section, following the develop panel's groups.
  group: string;
}
export interface NumericDial extends DialBase {
  kind: 'numeric';
  min: number;
  max: number;
  step: number;
  // Slider fill anchor, and the value shown with no draft loaded.
  neutral: number;
  display: (v: number) => string;
  value: (p: Params | null) => number;
  patch: (v: number) => Partial<Params>;
}
export interface CycleDial extends DialBase {
  kind: 'cycle';
  values: (string | number)[];
  valueLabel: (v: string | number) => string;
  value: (p: Params | null) => string | number;
  patch: (v: string | number) => Partial<Params>;
}
export type DialDef = NumericDial | CycleDial;

// Presentation metadata per control (labels sized for the 82px mini dials);
// everything behavioural comes from the control spec.
const META: Record<
  DialKey,
  { label: string; group: string; display?: (v: number) => string; valueLabels?: Record<string, string> }
> = {
  cropAngle: { label: 'Straighten', group: 'Crop', display: angle },
  expEV: { label: 'Exposure', group: 'Tone', display: ev },
  expPreserve: { label: 'Preserve', group: 'Tone', display: pct0 },
  bright: { label: 'Brightness', group: 'Tone', display: mult },
  gamma: { label: 'Gamma', group: 'Tone', display: num2 },
  shadow: { label: 'Slope', group: 'Tone', display: num1 },
  contrast: { label: 'Contrast', group: 'Tone' },
  whites: { label: 'Whites', group: 'Tone' },
  blacks: { label: 'Blacks', group: 'Tone' },
  toneShadows: { label: 'Shadows', group: 'Tone' },
  toneHighlights: { label: 'Highlights', group: 'Tone' },
  clarity: { label: 'Clarity', group: 'Presence' },
  texture: { label: 'Texture', group: 'Presence' },
  dehaze: { label: 'Dehaze', group: 'Presence' },
  wbMode: {
    label: 'WB mode',
    group: 'White balance',
    valueLabels: { camera: 'As shot', auto: 'Auto', kelvin: 'Kelvin' },
  },
  wbTemp: { label: 'Temp', group: 'White balance' },
  wbKelvin: { label: 'Kelvin', group: 'White balance', display: kelvin },
  wbTint: { label: 'Tint', group: 'White balance' },
  saturation: { label: 'Saturation', group: 'Color' },
  vibrance: { label: 'Vibrance', group: 'Color' },
  splitShadowHue: { label: 'Shadow hue', group: 'Color', display: hue },
  splitShadowAmt: { label: 'Shadow amt', group: 'Color', display: pct0 },
  splitHighlightHue: { label: 'Highl hue', group: 'Color', display: hue },
  splitHighlightAmt: { label: 'Highl amt', group: 'Color', display: pct0 },
  vignette: { label: 'Vignette', group: 'Effects' },
  sharpen: { label: 'Sharpen', group: 'Detail', display: pct0 },
  highlight: {
    label: 'Recovery',
    group: 'Detail',
    valueLabels: { 0: 'Clip', 1: 'Unclip', 2: 'Blend', 5: 'Rebuild' },
  },
  nrThreshold: { label: 'Noise', group: 'Detail', display: int },
  fbddNoiseRd: { label: 'FBDD', group: 'Detail', valueLabels: { 0: 'Off', 1: 'Light', 2: 'Full' } },
  medPasses: { label: 'Median', group: 'Detail', display: int },
  demosaic: {
    label: 'Demosaic',
    group: 'Detail',
    valueLabels: { '': 'Auto', vng: 'VNG', ppg: 'PPG', ahd: 'AHD', dht: 'DHT' },
  },
  caRed: { label: 'CA red', group: 'Detail' },
  caBlue: { label: 'CA blue', group: 'Detail' },
};

export const DIALS: DialDef[] = CONTROL_ORDER.map((key): DialDef => {
  const spec = CONTROL_SPECS[key];
  const m = META[key];
  if (spec.kind === 'cycle') {
    return {
      kind: 'cycle',
      key,
      label: m.label,
      group: m.group,
      values: spec.values,
      valueLabel: (v) => m.valueLabels?.[String(v)] ?? String(v),
      value: (p) => spec.get(p ?? NEUTRAL),
      patch: (v) => spec.set(v),
    };
  }
  return {
    kind: 'numeric',
    key,
    label: m.label,
    group: m.group,
    min: spec.min,
    max: spec.max,
    step: spec.step,
    neutral: spec.get(NEUTRAL),
    display: m.display ?? pct,
    value: (p) => spec.get(p ?? NEUTRAL),
    patch: (v) => spec.set(v),
  };
});

// sanitizeDialKeys narrows a stored toolbar-dial selection to known dials,
// in canonical render order. Default is none — the compact toolbar.
export function sanitizeDialKeys(raw: string[]): DialKey[] {
  return DIALS.map((d) => d.key).filter((k) => raw.includes(k));
}
