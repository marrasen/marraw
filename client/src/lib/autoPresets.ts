// Creative auto presets (Settings → Auto presets): a named auto that runs
// the chosen auto sections and then layers user style offsets on top.
// Persisted server-side (uiSettings), applied via esApplyAutoPreset.
import type { AutoSection } from '@/lib/editSession';

export interface AutoPreset {
  id: string;
  name: string;
  // Auto sections computed by the backend before the offsets land. Empty
  // means an offsets-only preset (a fixed style, no analysis).
  sections: AutoSection[];
  // Style values applied on top of the auto result. A key whose section is
  // active is added onto the computed value (a delta); a key with no auto
  // section, or whose section is off, is written as an absolute value —
  // including 0, which forces the field to 0.
  offsets: Partial<Record<OffsetKey, number>>;
}

// Params a preset may drive. Each has a fixed neutral at 0 (or, for the split
// hues, a plain 0–359 value) so it reads the same whether it lands as a delta
// or as an absolute assignment.
export type OffsetKey =
  | 'expEV'
  | 'contrast'
  | 'whites'
  | 'blacks'
  | 'toneShadows'
  | 'toneHighlights'
  | 'vibrance'
  | 'saturation'
  | 'splitShadowHue'
  | 'splitShadowAmt'
  | 'splitHighlightHue'
  | 'splitHighlightAmt'
  | 'texture'
  | 'clarity'
  | 'dehaze'
  | 'vignette';

// How the settings slider renders a key: EV in stops, split hues in degrees,
// everything else in the panel's ±100 units.
export type OffsetUnit = 'ev' | 'deg' | 'pct';

// OFFSET_KEYS drives the settings UI and the apply path. `section` is the auto
// section that computes the param (autoTone → tone, autoColor → color), or
// null when no auto touches it — those are always absolute "creative" values.
// The tone/color sections must match internal/pyramid/auto.go.
export const OFFSET_KEYS: {
  key: OffsetKey;
  label: string;
  section: AutoSection | null;
  unit: OffsetUnit;
}[] = [
  { key: 'expEV', label: 'Exposure', section: 'tone', unit: 'ev' },
  { key: 'contrast', label: 'Contrast', section: 'tone', unit: 'pct' },
  { key: 'whites', label: 'Whites', section: 'tone', unit: 'pct' },
  { key: 'blacks', label: 'Blacks', section: 'tone', unit: 'pct' },
  { key: 'toneShadows', label: 'Shadows', section: 'tone', unit: 'pct' },
  { key: 'toneHighlights', label: 'Highlights', section: 'tone', unit: 'pct' },
  { key: 'vibrance', label: 'Vibrance', section: 'color', unit: 'pct' },
  { key: 'saturation', label: 'Saturation', section: 'color', unit: 'pct' },
  { key: 'splitShadowHue', label: 'Shadow tint', section: null, unit: 'deg' },
  { key: 'splitShadowAmt', label: 'Shadow amount', section: null, unit: 'pct' },
  { key: 'splitHighlightHue', label: 'Highlight tint', section: null, unit: 'deg' },
  { key: 'splitHighlightAmt', label: 'Highlight amount', section: null, unit: 'pct' },
  { key: 'texture', label: 'Texture', section: null, unit: 'pct' },
  { key: 'clarity', label: 'Clarity', section: null, unit: 'pct' },
  { key: 'dehaze', label: 'Dehaze', section: null, unit: 'pct' },
  { key: 'vignette', label: 'Vignette', section: null, unit: 'pct' },
];

const SECTIONS: AutoSection[] = ['tone', 'wb', 'color'];
const OFFSETS = new Set<string>(OFFSET_KEYS.map((o) => o.key));
const OFFSET_SECTION: Record<OffsetKey, AutoSection | null> = Object.fromEntries(
  OFFSET_KEYS.map((o) => [o.key, o.section]),
) as Record<OffsetKey, AutoSection | null>;

// offsetIsAdditive reports whether an offset lands on top of the auto result
// (a delta) rather than as an absolute value. True only when the param has an
// auto section and that section is active on the preset; otherwise the slider
// value is written directly.
export function offsetIsAdditive(key: OffsetKey, sections: AutoSection[]): boolean {
  const sec = OFFSET_SECTION[key];
  return sec != null && sections.includes(sec);
}

// sanitizeAutoPresets narrows stored presets to the client shape, dropping
// anything malformed — unknown sections and offset keys are filtered rather
// than rejected so the list survives older/newer versions. Zero values are
// kept: an absolute offset of 0 is meaningful (it forces the field to 0).
export function sanitizeAutoPresets(raw: unknown): AutoPreset[] {
  if (!Array.isArray(raw)) return [];
  const out: AutoPreset[] = [];
  for (const p of raw) {
    if (typeof p !== 'object' || p === null) continue;
    const { id, name, sections, offsets } = p as Record<string, unknown>;
    if (typeof id !== 'string' || typeof name !== 'string') continue;
    const secs = Array.isArray(sections)
      ? SECTIONS.filter((s) => sections.includes(s))
      : [];
    const offs: AutoPreset['offsets'] = {};
    if (typeof offsets === 'object' && offsets !== null) {
      for (const [k, v] of Object.entries(offsets)) {
        if (OFFSETS.has(k) && typeof v === 'number' && Number.isFinite(v)) {
          offs[k as OffsetKey] = v;
        }
      }
    }
    out.push({ id, name, sections: secs, offsets: offs });
  }
  return out;
}

export function newAutoPreset(): AutoPreset {
  return { id: crypto.randomUUID(), name: 'New preset', sections: ['tone'], offsets: {} };
}

// Default presets ship with marraw to inspire creative-auto use, and are what
// "Restore defaults" writes back. The six are boldly different from one another
// — cinematic teal/orange, matte faded film, dramatic monochrome, punchy
// daylight, dark moody, and warm golden hour — so cycling Ctrl+1…6 is an obvious
// before/after. Fixed IDs (not random) keep those shortcuts and the restore/seed
// paths stable across runs.
//
// Offset values are in the same native units the sliders and computePresetParams
// use: pct params (contrast, vibrance, …) as fractions in −1…1, split amounts in
// 0…1, split hues in degrees (0…359), exposure in EV stops. A key whose auto
// section is active lands as a delta on the computed value; the rest are absolute
// (see OFFSET_KEYS / offsetIsAdditive). Keep values well inside each range so they
// read as an offset, not a clamp.
export const DEFAULT_PRESETS: AutoPreset[] = [
  // Blockbuster teal shadows / orange highlights, crushed blacks, filmic lift.
  {
    id: 'default-cinematic',
    name: 'Cinematic',
    sections: ['tone', 'wb', 'color'],
    offsets: {
      contrast: 0.15,
      blacks: -0.1,
      toneShadows: 0.1,
      toneHighlights: -0.08,
      vibrance: 0.1,
      saturation: -0.05,
      splitShadowHue: 195,
      splitShadowAmt: 0.25,
      splitHighlightHue: 40,
      splitHighlightAmt: 0.22,
      clarity: 0.08,
      vignette: 0.12,
    },
  },
  // Matte film: low contrast, lifted (raised) blacks, muted colour, soft detail.
  {
    id: 'default-faded',
    name: 'Faded film',
    sections: ['tone', 'color'],
    offsets: {
      contrast: -0.2,
      whites: -0.08,
      blacks: 0.15,
      toneHighlights: -0.1,
      vibrance: -0.2,
      saturation: -0.25,
      splitShadowHue: 210,
      splitShadowAmt: 0.1,
      splitHighlightHue: 50,
      splitHighlightAmt: 0.12,
      texture: -0.12,
      clarity: -0.1,
      dehaze: -0.08,
    },
  },
  // Dramatic monochrome: colour driven fully to zero (absolute, colour auto off),
  // deep blacks, strong local contrast and vignette.
  {
    id: 'default-noir',
    name: 'Noir B&W',
    sections: ['tone'],
    offsets: {
      contrast: 0.3,
      whites: 0.12,
      blacks: -0.2,
      toneShadows: -0.08,
      saturation: -1,
      vibrance: -1,
      clarity: 0.25,
      texture: 0.15,
      dehaze: 0.1,
      vignette: 0.28,
    },
  },
  // Bright, saturated daylight — the crowd-pleaser. High contrast, vivid, crisp.
  {
    id: 'default-punchy',
    name: 'Punchy',
    sections: ['tone', 'color'],
    offsets: {
      contrast: 0.28,
      whites: 0.15,
      blacks: -0.15,
      vibrance: 0.35,
      saturation: 0.2,
      clarity: 0.22,
      texture: 0.1,
      dehaze: 0.15,
      vignette: 0.08,
    },
  },
  // Dark and desaturated with cool shadows and a heavy vignette — dialled down.
  {
    id: 'default-moody',
    name: 'Moody',
    sections: ['tone', 'color'],
    offsets: {
      expEV: -0.35,
      contrast: 0.18,
      whites: -0.1,
      blacks: -0.22,
      toneShadows: 0.1,
      vibrance: -0.15,
      saturation: -0.2,
      splitShadowHue: 220,
      splitShadowAmt: 0.18,
      dehaze: 0.15,
      clarity: 0.1,
      vignette: 0.3,
    },
  },
  // Warm golden hour: no white-balance auto (keeps the warmth), amber shadows and
  // golden highlights, gently lifted, softly glowing.
  {
    id: 'default-golden',
    name: 'Golden hour',
    sections: ['tone'],
    offsets: {
      expEV: 0.1,
      contrast: -0.05,
      toneShadows: 0.12,
      toneHighlights: -0.05,
      vibrance: 0.2,
      saturation: 0.08,
      splitShadowHue: 35,
      splitShadowAmt: 0.12,
      splitHighlightHue: 45,
      splitHighlightAmt: 0.28,
      clarity: -0.05,
      texture: 0.05,
      vignette: 0.1,
    },
  },
];
