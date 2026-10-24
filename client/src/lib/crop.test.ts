import { describe, expect, it } from 'vitest';

import type { Params } from '@/api/edit';
import { flipCropPatch, geometryMatrix, hasCrop, renderedDims, rotateCropPatch, rotateTurns } from '@/lib/crop';

// The geometry helpers read only the crop, rotate, flip and mask fields, but
// their parameter is the whole generated Params. One cast here beats spelling
// out sixty neutral tone values in every case.
function geom(over: Partial<Params>): Params {
  return {
    cropX: 0, cropY: 0, cropW: 0, cropH: 0, cropAngle: 0,
    rotate: 0, flipH: false,
    ...over,
  } as unknown as Params;
}

// A crop that is not the full frame, deliberately asymmetric in both axes so a
// transposed or mirrored result cannot pass by coincidence.
const CROP = { cropX: 0.1, cropY: 0.2, cropW: 0.5, cropH: 0.3 };

function cropOf(p: Params) {
  return { cropX: p.cropX, cropY: p.cropY, cropW: p.cropW, cropH: p.cropH };
}

function apply(p: Params, patch: Partial<Params>): Params {
  return { ...p, ...patch };
}

describe('rotateCropPatch', () => {
  it('returns the frame to where it started after four quarter turns', () => {
    let p = geom(CROP);
    for (let i = 0; i < 4; i++) p = apply(p, rotateCropPatch(p, 'cw'));
    expect(rotateTurns(p)).toBe(0);
    for (const [k, v] of Object.entries(cropOf(p))) {
      expect(v).toBeCloseTo(CROP[k as keyof typeof CROP], 10);
    }
  });

  it('undoes a turn with the opposite turn', () => {
    const start = geom(CROP);
    const turned = apply(start, rotateCropPatch(start, 'cw'));
    const back = apply(turned, rotateCropPatch(turned, 'ccw'));
    expect(rotateTurns(back)).toBe(0);
    for (const [k, v] of Object.entries(cropOf(back))) {
      expect(v).toBeCloseTo(CROP[k as keyof typeof CROP], 10);
    }
  });

  it('transposes the rectangle and keeps it inside the frame', () => {
    const p = geom(CROP);
    const turned = apply(p, rotateCropPatch(p, 'cw'));
    // A quarter turn swaps the sides.
    expect(turned.cropW).toBeCloseTo(CROP.cropH, 10);
    expect(turned.cropH).toBeCloseTo(CROP.cropW, 10);
    expect(turned.cropX).toBeGreaterThanOrEqual(0);
    expect(turned.cropY).toBeGreaterThanOrEqual(0);
    expect(turned.cropX + turned.cropW).toBeLessThanOrEqual(1 + 1e-12);
    expect(turned.cropY + turned.cropH).toBeLessThanOrEqual(1 + 1e-12);
  });

  // mirror ∘ R_cw = R_ccw ∘ mirror: under a mirror the on-screen turn the user
  // asked for runs the other way through the stored turn count. Getting this
  // backwards sends the button the wrong way only for flipped photos.
  it('runs the stored turn the other way while the frame is mirrored', () => {
    const plain = geom({});
    expect(rotateCropPatch(plain, 'cw').rotate).toBe(1);

    const mirrored = geom({ flipH: true });
    expect(rotateCropPatch(mirrored, 'cw').rotate).toBe(3);
    expect(rotateCropPatch(mirrored, 'ccw').rotate).toBe(1);
  });

  it('leaves an absent crop absent', () => {
    const patch = rotateCropPatch(geom({}), 'cw');
    expect(patch.cropW).toBeUndefined();
    expect(patch.cropH).toBeUndefined();
  });
});

describe('flipCropPatch', () => {
  it('is its own inverse on either axis', () => {
    for (const axis of ['h', 'v'] as const) {
      const start = geom({ ...CROP, cropAngle: 3 });
      const once = apply(start, flipCropPatch(start, axis));
      const twice = apply(once, flipCropPatch(once, axis));
      expect(twice.flipH).toBe(false);
      expect(rotateTurns(twice)).toBe(0);
      expect(twice.cropAngle).toBeCloseTo(3, 10);
      for (const [k, v] of Object.entries(cropOf(twice))) {
        expect(v).toBeCloseTo(CROP[k as keyof typeof CROP], 10);
      }
    }
  });

  it('reflects the rectangle along the mirrored axis only', () => {
    const p = geom(CROP);
    const h = apply(p, flipCropPatch(p, 'h'));
    expect(h.cropX).toBeCloseTo(1 - (CROP.cropX + CROP.cropW), 10);
    expect(h.cropY).toBeCloseTo(CROP.cropY, 10);

    const v = apply(p, flipCropPatch(p, 'v'));
    expect(v.cropY).toBeCloseTo(1 - (CROP.cropY + CROP.cropH), 10);
    expect(v.cropX).toBeCloseTo(CROP.cropX, 10);
  });

  // A vertical mirror is the horizontal one plus a half turn — both toggle
  // flipH, and only the vertical one adds two quarter turns.
  it('takes a vertical mirror as a horizontal one plus a half turn', () => {
    const p = geom({});
    expect(flipCropPatch(p, 'h').flipH).toBe(true);
    expect(flipCropPatch(p, 'h').rotate).toBeUndefined();
    expect(flipCropPatch(p, 'v').flipH).toBe(true);
    expect(flipCropPatch(p, 'v').rotate).toBe(2);
  });

  it('negates the straighten angle, since a tilt reads the other way mirrored', () => {
    const p = geom({ cropAngle: 2.5 });
    expect(flipCropPatch(p, 'h').cropAngle).toBeCloseTo(-2.5, 10);
    // An untilted frame needs no angle in the patch at all.
    expect(flipCropPatch(geom({}), 'h').cropAngle).toBeUndefined();
  });
});

describe('hasCrop / rotateTurns / renderedDims', () => {
  it('reads a full-frame or absent crop as no crop', () => {
    expect(hasCrop(null)).toBe(false);
    expect(hasCrop(undefined)).toBe(false);
    expect(hasCrop(geom({}))).toBe(false);
    expect(hasCrop(geom(CROP))).toBe(true);
  });

  it('normalizes the stored turn into 0..3', () => {
    expect(rotateTurns(null)).toBe(0);
    expect(rotateTurns(geom({ rotate: 0 }))).toBe(0);
    expect(rotateTurns(geom({ rotate: 3 }))).toBe(3);
  });

  it('swaps the rendered sides on a quarter turn', () => {
    expect(renderedDims(6000, 4000, geom({}))).toEqual([6000, 4000]);
    const [w, h] = renderedDims(6000, 4000, geom({ rotate: 1 }));
    expect([w, h]).toEqual([4000, 6000]);
    // A half turn keeps them.
    expect(renderedDims(6000, 4000, geom({ rotate: 2 }))).toEqual([6000, 4000]);
  });

  it('applies the crop fractions to the rendered size', () => {
    const [w, h] = renderedDims(6000, 4000, geom(CROP));
    expect(w).toBe(Math.round(6000 * CROP.cropW));
    expect(h).toBe(Math.round(4000 * CROP.cropH));
  });
});

// The hold-to-compare original lands where the edit does: a point of the
// output, taken back to the full image the way internal/pyramid/geometry.go
// samples it (straighten about the turned frame's middle, then the mirror and
// the quarter turns undone), must come out at the same point through the
// CSS matrix.
describe('geometryMatrix', () => {
  const W = 300;
  const H = 200;
  const apply = (m: string, x: number, y: number) => {
    const [a, b, c, d, e, f] = m.slice(7, -1).split(',').map(Number);
    return [a * x + c * y + e, b * x + d * y + f];
  };
  const cases: Partial<Params>[] = [
    {},
    { rotate: 1 },
    { rotate: 2, flipH: true },
    { rotate: 3, ...CROP },
    { ...CROP, cropAngle: 4.5 },
    { rotate: 1, flipH: true, ...CROP, cropAngle: -3.2 },
    { cropAngle: 7 },
  ];
  for (const over of cases) {
    it(JSON.stringify(over), () => {
      const p = geom(over);
      const m = geometryMatrix(W, H, p);
      const turns = rotateTurns(p);
      const [fw, fh] = turns % 2 ? [H, W] : [W, H];
      const [ow, oh] = renderedDims(W, H, p);
      for (const [ox, oy] of [[0, 0], [ow, 0], [0, oh], [ow * 0.3, oh * 0.7]]) {
        // Output to the straightened frame, then back onto the turned frame.
        const fx = (hasCrop(p) ? p.cropX! * fw : 0) + ox - fw / 2;
        const fy = (hasCrop(p) ? p.cropY! * fh : 0) + oy - fh / 2;
        const r = (-(p.cropAngle ?? 0) * Math.PI) / 180;
        let sx = fw / 2 + fx * Math.cos(r) - fy * Math.sin(r);
        const sy = fh / 2 + fx * Math.sin(r) + fy * Math.cos(r);
        if (p.flipH) sx = fw - sx;
        // The turned frame back to the image.
        const [ix, iy] = [
          [sx, sy],
          [sy, H - sx],
          [W - sx, H - sy],
          [W - sy, sx],
        ][turns];
        const [gx, gy] = apply(m, ix, iy);
        expect(gx).toBeCloseTo(ox, 3);
        expect(gy).toBeCloseTo(oy, 3);
      }
    });
  }
});
