import type { Params } from '@/api/edits';

// The develop dials a user can pin to the Cull confirm bar and the Develop
// quick dock (Settings → Toolbars). Order here is the order they render in.
export type DialKey = 'expEV' | 'contrast' | 'toneHighlights' | 'toneShadows' | 'wbTemp' | 'vibrance';

const pct = (v: number) => (v === 0 ? '0' : `${v > 0 ? '+' : ''}${Math.round(v * 100)}`);
const ev = (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(2)}`;

export interface DialDef {
  key: DialKey;
  label: string;
  min: number;
  max: number;
  step: number;
  display: (v: number) => string;
}

export const DIALS: DialDef[] = [
  { key: 'expEV', label: 'Exposure', min: -2, max: 3, step: 0.05, display: ev },
  { key: 'contrast', label: 'Contrast', min: -1, max: 1, step: 0.02, display: pct },
  { key: 'toneHighlights', label: 'Highlights', min: -1, max: 1, step: 0.02, display: pct },
  { key: 'toneShadows', label: 'Shadows', min: -1, max: 1, step: 0.02, display: pct },
  { key: 'wbTemp', label: 'Temp', min: -1, max: 1, step: 0.02, display: pct },
  { key: 'vibrance', label: 'Vibrance', min: -1, max: 1, step: 0.02, display: pct },
];

export function dialValue(params: Params | null, key: DialKey): number {
  return params?.[key] ?? 0;
}

// sanitizeDialKeys narrows a stored toolbar-dial selection to known dials,
// in canonical render order. Default is none — the compact toolbar.
export function sanitizeDialKeys(raw: string[]): DialKey[] {
  return DIALS.map((d) => d.key).filter((k) => raw.includes(k));
}
