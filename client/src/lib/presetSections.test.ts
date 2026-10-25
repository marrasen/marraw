import { describe, expect, it } from 'vitest';

import type { Params } from '@/api/edit';
import { NEUTRAL } from '@/lib/controlSpecs';
import { adaptiveLookDiff } from '@/lib/presetSections';

describe('adaptiveLookDiff', () => {
  // The look as drawn on the source photo, and that photo's own auto tone
  // and color: the auto writes only its fields, the rest come back as drawn.
  const hsl = [0, 10, 0, 0, 0, 0, 0, 0] as Params['hslSat'];
  const draft: Params = { ...NEUTRAL, expEV: 1.2, contrast: 0.3, saturation: 0.2, clarity: 0.4, vignette: -0.3, hslSat: hsl };
  const auto: Params = { ...draft, expEV: 0.8, contrast: 0.1, saturation: 0.05 };

  it('stores the auto fields as the difference from the auto', () => {
    const out = adaptiveLookDiff(draft, auto, ['tone', 'color']);
    expect(out.expEV).toBeCloseTo(0.4);
    expect(out.contrast).toBeCloseTo(0.2);
    expect(out.saturation).toBeCloseTo(0.15);
  });

  it('keeps fields no auto writes as drawn', () => {
    const out = adaptiveLookDiff(draft, auto, ['tone', 'color']);
    expect(out.clarity).toBe(0.4);
    expect(out.vignette).toBe(-0.3);
    expect(out.hslSat).toEqual(hsl);
  });

  it('keeps an auto field as drawn when its section is not auto', () => {
    const out = adaptiveLookDiff(draft, auto, ['color']);
    expect(out.expEV).toBe(1.2);
    expect(out.contrast).toBe(0.3);
    expect(out.saturation).toBeCloseTo(0.15);
  });
});
