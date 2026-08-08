import { describe, expect, it } from 'vitest';

import type { Mask, Params, Spot } from '@/api/edit';
import { NEUTRAL } from '@/lib/controlSpecs';
import { labelForDiff } from '@/lib/editLabels';

const base = (): Params => ({ ...NEUTRAL });

function withParams(over: Partial<Params>): Params {
  return { ...NEUTRAL, ...over };
}

function mask(over: Partial<Mask> = {}): Mask {
  return { type: 'linear', adjust: {}, ...over } as unknown as Mask;
}

function spot(over: Partial<Spot> = {}): Spot {
  return { cx: 0.5, cy: 0.5, sx: 0.4, sy: 0.4, radius: 0.05, ...over } as unknown as Spot;
}

describe('labelForDiff', () => {
  it('names a single moved control', () => {
    expect(labelForDiff(base(), withParams({ expEV: 0.5 }))).toBe('Exposure');
  });

  it('calls a mixed change an adjustment', () => {
    expect(labelForDiff(base(), withParams({ expEV: 0.5, saturation: 0.2 }))).toBe('Adjust');
  });

  it('falls back when nothing actually moved', () => {
    expect(labelForDiff(base(), base())).toBe('Edit');
  });

  // Several keys that share one control label still read as that control —
  // the split-tone pair, the HSL bands.
  it('reads several keys under one label as that control', () => {
    const next = withParams({ splitShadowHue: 30, splitShadowAmt: 0.4 });
    expect(labelForDiff(base(), next)).toBe('Add split shadow');
  });

  describe('effect toggles read as add and remove', () => {
    it('names leaving the default Add', () => {
      expect(labelForDiff(base(), withParams({ vignette: 0.4 }))).toBe('Add vignette');
      expect(labelForDiff(base(), withParams({ clarity: 0.3 }))).toBe('Add clarity');
    });

    it('names returning to the default Remove', () => {
      const on = withParams({ vignette: 0.4 });
      expect(labelForDiff(on, base())).toBe('Remove vignette');
    });

    // Moving between two non-default values is neither: it is just the control.
    it('names a change between two live values after the control', () => {
      const a = withParams({ vignette: 0.4 });
      const b = withParams({ vignette: 0.6 });
      expect(labelForDiff(a, b)).toBe('Vignette');
    });

    it('leaves controls outside the effect set alone', () => {
      expect(labelForDiff(base(), withParams({ expEV: 0.5 }))).toBe('Exposure');
    });
  });

  describe('masks', () => {
    it('counts an addition and a removal', () => {
      const none = withParams({ masks: [] });
      const one = withParams({ masks: [mask({ type: 'linear' })] });
      expect(labelForDiff(none, one)).toBe('Add linear mask');
      // Removing the last mask commits an empty array, not an absent key.
      expect(labelForDiff(one, none)).toBe('Remove mask');
    });

    // Reordering has to be named before the per-slot walk, which would
    // otherwise read the shifted rows as an edit to whichever mask landed in
    // slot 0 — a drag would say "Adjust mask" and undo would look wrong.
    it('names a reorder rather than an edit to the mask that moved into place', () => {
      const a = withParams({ masks: [mask({ type: 'linear' }), mask({ type: 'radial' })] });
      const b = withParams({ masks: [mask({ type: 'radial' }), mask({ type: 'linear' })] });
      expect(labelForDiff(a, b)).toBe('Reorder masks');
    });

    it('distinguishes what changed within one mask', () => {
      const a = withParams({ masks: [mask()] });
      expect(labelForDiff(a, withParams({ masks: [mask({ disabled: true })] }))).toBe('Hide mask');
      expect(labelForDiff(withParams({ masks: [mask({ disabled: true })] }), a)).toBe('Show mask');
      expect(
        labelForDiff(a, withParams({ masks: [mask({ strokes: [{ x: 0.1, y: 0.1, r: 0.05 }] })] })),
      ).toBe('Brush stroke');
      expect(labelForDiff(a, withParams({ masks: [mask({ adjust: { expEV: 0.5 } })] }))).toBe(
        'Adjust mask',
      );
    });
  });

  describe('spots', () => {
    it('counts an addition and a removal', () => {
      const none = withParams({ spots: [] });
      const one = withParams({ spots: [spot()] });
      expect(labelForDiff(none, one)).toBe('Add spot');
      expect(labelForDiff(one, none)).toBe('Remove spot');
    });

    it('separates moving a spot from tuning it', () => {
      const a = withParams({ spots: [spot()] });
      expect(labelForDiff(a, withParams({ spots: [spot({ cx: 0.6 })] }))).toBe('Move spot');
      expect(labelForDiff(a, withParams({ spots: [spot({ radius: 0.09 })] }))).toBe('Move spot');
      expect(labelForDiff(a, withParams({ spots: [spot({ feather: 0.8 })] }))).toBe('Adjust spot');
      expect(labelForDiff(a, withParams({ spots: [spot({ disabled: true })] }))).toBe('Hide spot');
    });
  });

  // A commit touching both is not a masks-only or spots-only change, so it
  // takes the generic label rather than one of the specific ones.
  it('does not claim a masks-only label when something else moved too', () => {
    const next = withParams({ masks: [mask()], expEV: 0.5 });
    expect(labelForDiff(base(), next)).toBe('Adjust');
  });
});

// A key present in the previous state but absent from the next one is
// invisible to the diff, which walks the next state's keys. That is not a bug
// today: every caller reaches this through esCommit, which merges a patch onto
// the current draft, so a removal arrives as an empty array rather than a
// missing key — and the two states always spell the same fields. It is pinned
// because it is the assumption that makes the diff correct, and the one a
// future caller building params some other way would break.
describe('the shape labelForDiff assumes of its inputs', () => {
  it('cannot see a field the next state omits entirely', () => {
    const withMask = withParams({ masks: [mask()] });
    const omitted = base(); // NEUTRAL carries no masks key at all
    expect(labelForDiff(withMask, omitted)).toBe('Edit');
  });
});
