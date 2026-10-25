import { describe, expect, it } from 'vitest';

import type { Params } from '@/api/edit';
import { NEUTRAL } from '@/lib/controlSpecs';
import { adaptiveLookDiff, applyUserPreset } from '@/lib/presetSections';
import type { UserPreset } from '@/api/settings';

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

describe('applyUserPreset of an adaptive preset', () => {
  const draft: Params = { ...NEUTRAL, expEV: 1.2, contrast: 0.3, clarity: 0.4 };
  const auto: Params = { ...draft, expEV: 0.8, contrast: 0.1 };
  const params = adaptiveLookDiff(draft, auto, ['tone']);
  // Another photo, its own auto run, with clarity of its own.
  const target: Params = { ...NEUTRAL, expEV: 0.5, contrast: 0.2, clarity: 0.1 };

  it('lands the auto fields on the photo\'s own auto, the rest as drawn', () => {
    const preset = { id: 'a', name: 'a', params, autoSections: ['tone'] } as UserPreset;
    const out = applyUserPreset(target, preset, 0);
    expect(out.expEV).toBeCloseTo(0.9);
    expect(out.contrast).toBeCloseTo(0.4);
    expect(out.clarity).toBe(0.4);
  });

  it('adds the rest when the preset is relative', () => {
    const preset = { id: 'a', name: 'a', params, autoSections: ['tone'], relative: true } as UserPreset;
    expect(applyUserPreset(target, preset, 0).clarity).toBeCloseTo(0.5);
  });

  it('leaves the photo\'s auto white balance when the preset was saved on camera', () => {
    // Through the server, camera white balance is spelled ''.
    const wb = { ...params, wbMode: '' } as unknown as Params;
    const preset = { id: 'a', name: 'a', params: wb, autoSections: ['tone', 'wb'], relative: true } as UserPreset;
    const onAuto = { ...target, wbMode: 'auto' } as Params;
    expect(applyUserPreset(onAuto, preset, 0).wbMode).toBe('auto');
  });
});
