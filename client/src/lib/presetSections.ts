// The user-preset section catalog and apply math. A preset stores a FULL
// Params snapshot (the server re-marshals through edit.Params, so sparse
// fields can't round-trip); `sections` filters which look groups land at
// apply time, and `relative` switches the landing from absolute values to
// deltas-from-neutral on top of the photo's current edits. Geometry
// (rotate/flip/crop/straighten) and local adjustments (masks/spots) are
// never part of a preset — a preset is a look, not a crop.
//
// A leaf module like controlSpecs: editSession and the presets UI build on
// it, so it must not import them back.
import type { Params } from '@/api/edit';
import type { UserPreset } from '@/api/settings';
import { CONTROL_SPECS, NEUTRAL } from '@/lib/controlSpecs';

// The look subset of the develop panel's GroupId — crop and retouch never
// travel in a preset. Mirrored (ids only) in internal/api/uisettings.go.
export type PresetGroup = 'tone' | 'presence' | 'wb' | 'color' | 'effects' | 'detail';

export const PRESET_GROUPS: { id: PresetGroup; label: string }[] = [
  { id: 'tone', label: 'Tone' },
  { id: 'presence', label: 'Presence' },
  { id: 'wb', label: 'White balance' },
  { id: 'color', label: 'Color' },
  { id: 'effects', label: 'Effects' },
  { id: 'detail', label: 'Detail' },
];

const GROUP_IDS = new Set<string>(PRESET_GROUPS.map((g) => g.id));

// Look params, i.e. everything a preset can carry.
export type LookParamKey = Exclude<
  keyof Params,
  'rotate' | 'flipH' | 'cropX' | 'cropY' | 'cropW' | 'cropH' | 'cropAngle' | 'masks' | 'spots'
>;

// How a field lands when the preset is relative:
// - 'add': numeric with a fixed neutral — the preset's offset from neutral
//   is added onto the photo's current value, clamped to the control range.
// - 'absolute': position-valued numerics (hues, Kelvin, WB multipliers)
//   where "current + delta" is meaningless — written as stored, but only
//   when non-neutral in the preset (neutral can't be told from untouched).
// - 'enum': cycle values — same non-neutral-only rule as 'absolute'.
// Absolute presets write every included field as stored regardless of mode.
type ApplyMode = 'add' | 'absolute' | 'enum';

interface FieldInfo {
  group: PresetGroup;
  mode: ApplyMode;
}

// Exhaustive over every look field of Params — the `satisfies` check fails
// to compile when a new Params field is neither mapped here nor added to
// the geometry/local exclusions in LookParamKey. Group membership matches
// CONTROL_GROUP in editSession.ts; fields without a ControlId (wbMul, the
// HSL mixer arrays) are placed with their panel section.
export const PRESET_FIELDS = {
  expEV: { group: 'tone', mode: 'add' },
  expPreserve: { group: 'tone', mode: 'add' },
  bright: { group: 'tone', mode: 'add' },
  gamma: { group: 'tone', mode: 'add' },
  shadow: { group: 'tone', mode: 'add' },
  contrast: { group: 'tone', mode: 'add' },
  whites: { group: 'tone', mode: 'add' },
  blacks: { group: 'tone', mode: 'add' },
  toneShadows: { group: 'tone', mode: 'add' },
  toneHighlights: { group: 'tone', mode: 'add' },
  clarity: { group: 'presence', mode: 'add' },
  texture: { group: 'presence', mode: 'add' },
  dehaze: { group: 'presence', mode: 'add' },
  wbMode: { group: 'wb', mode: 'enum' },
  wbMul: { group: 'wb', mode: 'absolute' },
  wbTemp: { group: 'wb', mode: 'add' },
  wbTint: { group: 'wb', mode: 'add' },
  wbKelvin: { group: 'wb', mode: 'absolute' },
  saturation: { group: 'color', mode: 'add' },
  vibrance: { group: 'color', mode: 'add' },
  splitShadowHue: { group: 'color', mode: 'absolute' },
  splitShadowAmt: { group: 'color', mode: 'add' },
  splitHighlightHue: { group: 'color', mode: 'absolute' },
  splitHighlightAmt: { group: 'color', mode: 'add' },
  hslHue: { group: 'color', mode: 'add' },
  hslSat: { group: 'color', mode: 'add' },
  hslLum: { group: 'color', mode: 'add' },
  vignette: { group: 'effects', mode: 'add' },
  sharpen: { group: 'detail', mode: 'add' },
  highlight: { group: 'detail', mode: 'enum' },
  nrThreshold: { group: 'detail', mode: 'add' },
  fbddNoiseRd: { group: 'detail', mode: 'enum' },
  medPasses: { group: 'detail', mode: 'add' },
  demosaic: { group: 'detail', mode: 'enum' },
  caRed: { group: 'detail', mode: 'add' },
  caBlue: { group: 'detail', mode: 'add' },
} as const satisfies Record<LookParamKey, FieldInfo>;

const LOOK_KEYS = Object.keys(PRESET_FIELDS) as LookParamKey[];

// presetSections narrows a stored section list to known group ids; empty or
// missing means "all sections" (presets saved before sections existed).
export function presetSections(preset: UserPreset): PresetGroup[] {
  const known = (preset.sections ?? []).filter((s): s is PresetGroup => GROUP_IDS.has(s));
  return known.length > 0 ? known : PRESET_GROUPS.map((g) => g.id);
}

// The server clamps HSL bands to ±1 (edit.go Normalize); they have no
// ControlId, so the range lives here.
const HSL_RANGE = { min: -1, max: 1 };

function clampRound(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, Math.round(v * 1000) / 1000));
}

// Numeric fields whose stored 0 is a sentinel for a nonzero effective
// default (bright→1, gamma→2.222, shadow→4.5, wbKelvin→5500). Deltas must
// be computed over EFFECTIVE values via the control spec's get(), and a
// result equal to the effective default is stored back as the sentinel 0 so
// an untouched slider round-trips to "default" instead of pinning it.
function effective(key: LookParamKey, p: Params): number {
  const spec = CONTROL_SPECS[key as keyof typeof CONTROL_SPECS];
  if (spec && spec.kind === 'numeric') return spec.get(p);
  return (p as unknown as Record<string, number>)[key];
}

// applyUserPreset lays a preset over the photo's current draft and returns
// the merged params. Only fields of included sections move; geometry and
// masks/spots always come from the draft. `targetBaseExpEV` is the target
// photo's measured camera-mimic baseline (photo.baseExpEV, 0 = unmeasured):
// exposure is re-anchored so the preset carries its CREATIVE exposure — the
// offset from the source photo's calibrated baseline — rather than the
// source photo's absolute dial position. A legacy preset (baseExpEV 0)
// skips re-anchoring and lands as stored, the old behavior.
export function applyUserPreset(draft: Params, preset: UserPreset, targetBaseExpEV: number): Params {
  const sections = new Set(presetSections(preset));
  const out: Params = { ...draft };
  const src = preset.params;
  const relative = preset.relative === true;

  for (const key of LOOK_KEYS) {
    const { group, mode } = PRESET_FIELDS[key];
    if (!sections.has(group)) continue;

    if (key === 'expEV') {
      out.expEV = clampRound(presetExpEV(draft, preset, targetBaseExpEV), -5, 5);
      continue;
    }

    if (key === 'hslHue' || key === 'hslSat' || key === 'hslLum') {
      out[key] = src[key].map((v, i) =>
        clampRound(relative ? draft[key][i] + v : v, HSL_RANGE.min, HSL_RANGE.max),
      ) as Params['hslHue'];
      continue;
    }

    if (key === 'wbMul') {
      // Custom multipliers: absolute-only; in relative mode a neutral
      // [0,0,0,0] (unset) is indistinguishable from untouched, so skip it.
      if (!relative || src.wbMul.some((v) => v !== 0)) out.wbMul = [...src.wbMul];
      continue;
    }

    if (mode === 'enum') {
      // Cycle values: absolute presets write as stored; relative presets
      // only when the preset's value is non-neutral.
      if (!relative || src[key] !== NEUTRAL[key]) {
        (out as unknown as Record<string, unknown>)[key] = src[key];
      }
      continue;
    }

    const spec = CONTROL_SPECS[key as keyof typeof CONTROL_SPECS];
    const min = spec && spec.kind === 'numeric' ? spec.min : -1;
    const max = spec && spec.kind === 'numeric' ? spec.max : 1;
    const fields = out as unknown as Record<string, number>;
    const raw = (src as unknown as Record<string, number>)[key];
    const neutral = (NEUTRAL as unknown as Record<string, number>)[key];
    // A stored neutral passes through unclamped: sentinel-default fields
    // (bright/gamma/shadow/wbKelvin keep 0 for their nonzero default) sit
    // below their control range, and clamping 0 up to the minimum would
    // turn "untouched" into a real adjustment.
    const asStored = raw === neutral ? raw : clampRound(raw, min, max);

    if (mode === 'absolute') {
      if (!relative || raw !== neutral) fields[key] = asStored;
      continue;
    }

    // mode === 'add'
    if (relative) {
      const delta = effective(key, src) - effective(key, NEUTRAL);
      if (delta === 0) continue; // untouched in the preset — leave the draft's value
      const v = clampRound(effective(key, draft) + delta, min, max);
      // A result landing exactly on the effective default stores the
      // sentinel back, so the field still reads as "untouched".
      fields[key] = v === effective(key, NEUTRAL) ? neutral : v;
    } else {
      fields[key] = asStored;
    }
  }

  return out;
}

// presetExpEV resolves the exposure a preset lands at. The preset's stored
// expEV includes the SOURCE photo's calibrated baseline (the seeded
// camera-mimic compensation) — the look's creative intent is the offset
// from that baseline. Re-anchoring adds the creative offset to the TARGET
// photo's baseline (absolute mode) or to the draft's current exposure
// (relative mode). preset.baseExpEV of 0 means unknown (legacy preset or
// unmeasured source photo): absolute mode then applies the stored value
// unchanged rather than guessing — re-anchoring with a fabricated baseline
// would double-compensate.
function presetExpEV(draft: Params, preset: UserPreset, targetBaseExpEV: number): number {
  const srcBase = preset.baseExpEV ?? 0;
  const creative = preset.params.expEV - srcBase;
  if (preset.relative === true) return draft.expEV + creative;
  if (srcBase === 0) return preset.params.expEV;
  return targetBaseExpEV + creative;
}

// stripToLook returns a copy of `draft` with geometry zeroed and local
// adjustments removed — the shape a preset's params snapshot must have.
export function stripToLook(draft: Params): Params {
  return {
    ...draft,
    rotate: 0,
    flipH: false,
    cropX: 0,
    cropY: 0,
    cropW: 0,
    cropH: 0,
    cropAngle: 0,
    masks: undefined,
    spots: undefined,
  };
}
