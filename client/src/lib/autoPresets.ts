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
