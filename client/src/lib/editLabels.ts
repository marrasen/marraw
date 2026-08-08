// Naming an undo entry from what changed between two edit states.
//
// Pure: a function of the two Params and the control catalog, with no view of
// the session it was extracted from. That is what the undo list reads out to
// the user — "Add vignette", "Brush stroke", "Reorder masks" — so it is worth
// being able to test on its own.
import type { Mask, Params, Spot } from '@/api/edit';
import { MASK_TYPE_LABELS, NEUTRAL, paramLabel } from '@/lib/controlSpecs';

// Effect-style controls read naturally as "Add vignette" / "Remove clarity"
// when they move on/off their default; everything else just names the control.
const ADD_REMOVE_LABELS = new Set([
  'Vignette', 'Texture', 'Clarity', 'Dehaze',
  'Split shadow', 'Split highlight', 'Sharpen', 'Noise reduction',
]);

function paramIsDefault(p: Params, key: keyof Params): boolean {
  const v = p[key];
  const d = NEUTRAL[key];
  if (Array.isArray(v) && Array.isArray(d)) return JSON.stringify(v) === JSON.stringify(d);
  return v === d;
}

// maskDiffLabel names a masks-only change: add/remove by count, otherwise by
// what part of the changed mask moved (a brush stroke, the shape, the sliders).
function maskDiffLabel(prev: Mask[] | undefined, next: Mask[] | undefined): string {
  const a = prev ?? [];
  const b = next ?? [];
  if (b.length > a.length) {
    const type = b[b.length - 1]?.type;
    return MASK_TYPE_LABELS[type] ? `Add ${type} mask` : 'Add mask';
  }
  if (b.length < a.length) return 'Remove mask';
  // The same masks in a different order (esMoveMask). Named before the
  // per-slot walk below, which would otherwise read the shifted rows as an
  // edit to whichever mask landed in slot i.
  const sorted = (ms: Mask[]) => JSON.stringify(ms.map((m) => JSON.stringify(m)).sort());
  if (JSON.stringify(a) !== JSON.stringify(b) && sorted(a) === sorted(b)) return 'Reorder masks';
  for (let i = 0; i < b.length; i++) {
    if (JSON.stringify(a[i]) === JSON.stringify(b[i])) continue;
    if (!a[i]?.disabled !== !b[i]?.disabled) return b[i]?.disabled ? 'Hide mask' : 'Show mask';
    if (JSON.stringify(a[i]?.strokes) !== JSON.stringify(b[i]?.strokes)) return 'Brush stroke';
    if (JSON.stringify(a[i]?.adjust) !== JSON.stringify(b[i]?.adjust)) return 'Adjust mask';
    return 'Move mask';
  }
  return 'Adjust mask';
}

// spotDiffLabel names a spots-only change: add/remove by count, otherwise by
// what moved (a source/dest circle or radius, versus the mode/feather sliders).
function spotDiffLabel(prev: Spot[] | undefined, next: Spot[] | undefined): string {
  const a = prev ?? [];
  const b = next ?? [];
  if (b.length > a.length) return 'Add spot';
  if (b.length < a.length) return 'Remove spot';
  for (let i = 0; i < b.length; i++) {
    if (JSON.stringify(a[i]) === JSON.stringify(b[i])) continue;
    const p = a[i];
    const n = b[i];
    if (!p.disabled !== !n.disabled) return n.disabled ? 'Hide spot' : 'Show spot';
    if (p.cx !== n.cx || p.cy !== n.cy || p.sx !== n.sx || p.sy !== n.sy || p.radius !== n.radius) {
      return 'Move spot';
    }
    return 'Adjust spot';
  }
  return 'Adjust spot';
}

// labelForDiff names a commit from the params that changed between the
// previous history head and the new snapshot: a single control by its label
// (with Add/Remove for effect toggles), a mixed change as "Adjust".
export function labelForDiff(prev: Params, next: Params): string {
  const keys = (Object.keys(next) as (keyof Params)[]).filter((k) => {
    const a = prev[k];
    const b = next[k];
    return Array.isArray(a) && Array.isArray(b) ? JSON.stringify(a) !== JSON.stringify(b) : a !== b;
  });
  if (keys.length === 0) return 'Edit';
  if (keys.length === 1 && keys[0] === 'masks') return maskDiffLabel(prev.masks, next.masks);
  if (keys.length === 1 && keys[0] === 'spots') return spotDiffLabel(prev.spots, next.spots);
  const labels = new Set(keys.map((k) => paramLabel(k)));
  if (labels.size !== 1) return 'Adjust';
  const label = [...labels][0];
  if (ADD_REMOVE_LABELS.has(label)) {
    const wasDefault = keys.every((k) => paramIsDefault(prev, k));
    const nowDefault = keys.every((k) => paramIsDefault(next, k));
    if (wasDefault && !nowDefault) return `Add ${label.toLowerCase()}`;
    if (!wasDefault && nowDefault) return `Remove ${label.toLowerCase()}`;
  }
  return label;
}
