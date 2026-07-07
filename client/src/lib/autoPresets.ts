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
  // Style deltas added onto the auto result, clamped to the control ranges.
  offsets: Partial<Record<OffsetKey, number>>;
}

// Params a preset may offset — restricted to direct zero-neutral deltas so
// "+0.1" always means the same thing (bright/gamma-style "0 = default"
// fields are deliberately excluded).
export type OffsetKey =
  | 'expEV'
  | 'contrast'
  | 'toneShadows'
  | 'toneHighlights'
  | 'vibrance'
  | 'saturation'
  | 'vignette'
  | 'clarity';

export const OFFSET_KEYS: { key: OffsetKey; label: string }[] = [
  { key: 'expEV', label: 'Exposure' },
  { key: 'contrast', label: 'Contrast' },
  { key: 'toneShadows', label: 'Shadows' },
  { key: 'toneHighlights', label: 'Highlights' },
  { key: 'vibrance', label: 'Vibrance' },
  { key: 'saturation', label: 'Saturation' },
  { key: 'vignette', label: 'Vignette' },
  { key: 'clarity', label: 'Clarity' },
];

const SECTIONS: AutoSection[] = ['tone', 'wb', 'color'];
const OFFSETS = new Set<string>(OFFSET_KEYS.map((o) => o.key));

// sanitizeAutoPresets narrows stored presets to the client shape, dropping
// anything malformed — unknown sections and offset keys are filtered rather
// than rejected so the list survives older/newer versions.
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
        if (OFFSETS.has(k) && typeof v === 'number' && v !== 0) offs[k as OffsetKey] = v;
      }
    }
    out.push({ id, name, sections: secs, offsets: offs });
  }
  return out;
}

export function newAutoPreset(): AutoPreset {
  return { id: crypto.randomUUID(), name: 'New preset', sections: ['tone'], offsets: {} };
}
