import { describe, expect, it } from 'vitest';

import type { CurvePoint } from '@/api/edit';
import { CURVE_ENDPOINTS, curvePolyline, evalCurve, hasToneCurve } from '@/lib/toneCurve';

// Sample a curve densely enough to catch a bulge between control points.
function sample(pts: CurvePoint[], steps = 500): CurvePoint[] {
  return Array.from({ length: steps + 1 }, (_, i) => {
    const x = i / steps;
    return { x, y: evalCurve(pts, x) };
  });
}

describe('hasToneCurve', () => {
  // This decides whether the panel shows a "changed" dot, and it mirrors Go's
  // edit.Params.HasToneCurve. If the two disagree the dot lies in one
  // direction, or the server renders a curve the client believes is neutral.
  it('treats every identity form as unbent', () => {
    expect(hasToneCurve(undefined)).toBe(false);
    expect(hasToneCurve([])).toBe(false);
    expect(hasToneCurve([{ x: 0.5, y: 0.9 }])).toBe(false); // one point cannot bend
    expect(hasToneCurve(CURVE_ENDPOINTS)).toBe(false);
    expect(hasToneCurve([{ x: 0, y: 0 }, { x: 0.5, y: 0.5 }, { x: 1, y: 1 }])).toBe(false);
  });

  it('notices a single point off the diagonal', () => {
    expect(hasToneCurve([{ x: 0, y: 0 }, { x: 0.5, y: 0.6 }, { x: 1, y: 1 }])).toBe(true);
    // Including one that only moves an endpoint — a lifted black point.
    expect(hasToneCurve([{ x: 0, y: 0.1 }, { x: 1, y: 1 }])).toBe(true);
  });
});

describe('evalCurve', () => {
  it('passes x through for the identity curve', () => {
    for (const x of [0, 0.13, 0.5, 0.87, 1]) {
      expect(evalCurve(CURVE_ENDPOINTS, x)).toBeCloseTo(x, 10);
    }
  });

  it('is flat past the outermost points', () => {
    const pts = [{ x: 0.25, y: 0.4 }, { x: 0.75, y: 0.8 }];
    expect(evalCurve(pts, 0)).toBeCloseTo(0.4, 10);
    expect(evalCurve(pts, 0.1)).toBeCloseTo(0.4, 10);
    expect(evalCurve(pts, 0.9)).toBeCloseTo(0.8, 10);
    expect(evalCurve(pts, 1)).toBeCloseTo(0.8, 10);
  });

  it('lands exactly on its control points', () => {
    const pts = [{ x: 0, y: 0 }, { x: 0.3, y: 0.62 }, { x: 0.7, y: 0.71 }, { x: 1, y: 1 }];
    for (const p of pts) expect(evalCurve(pts, p.x)).toBeCloseTo(p.y, 10);
  });

  // The reason the module carries Fritsch–Carlson tangents at all: a plain
  // cubic spline through these points bulges past them, and a tone curve that
  // overshoots inverts tones — shadows coming back lighter than the midtones
  // feeding them. The backend's buildCurveLUT makes the same guarantee, and
  // the drawn line has to match the rendered pixels.
  it('never overshoots a segment, however steep', () => {
    const curves: CurvePoint[][] = [
      [{ x: 0, y: 0 }, { x: 0.5, y: 0.5 }, { x: 1, y: 1 }],
      [{ x: 0, y: 0 }, { x: 0.25, y: 0.05 }, { x: 0.75, y: 0.95 }, { x: 1, y: 1 }], // hard S
      [{ x: 0, y: 0 }, { x: 0.02, y: 0.9 }, { x: 1, y: 1 }], // near-vertical rise
      [{ x: 0, y: 0.5 }, { x: 0.5, y: 0.5 }, { x: 1, y: 0.5 }], // flat
      [{ x: 0, y: 0 }, { x: 0.4, y: 0.6 }, { x: 0.6, y: 0.4 }, { x: 1, y: 1 }], // non-monotone data
      [{ x: 0, y: 1 }, { x: 1, y: 0 }], // fully inverted
    ];
    for (const pts of curves) {
      for (const { x, y } of sample(pts)) {
        // Locate the segment this sample falls in and stay inside its box.
        let i = 0;
        while (i < pts.length - 2 && x > pts[i + 1].x) i++;
        const lo = Math.min(pts[i].y, pts[i + 1].y);
        const hi = Math.max(pts[i].y, pts[i + 1].y);
        if (x < pts[0].x || x > pts[pts.length - 1].x) continue; // the flat tails
        expect(y).toBeGreaterThanOrEqual(lo - 1e-9);
        expect(y).toBeLessThanOrEqual(hi + 1e-9);
      }
    }
  });

  it('rises without ever dipping when its points do', () => {
    const pts = [{ x: 0, y: 0 }, { x: 0.2, y: 0.1 }, { x: 0.5, y: 0.8 }, { x: 0.8, y: 0.85 }, { x: 1, y: 1 }];
    let prev = -Infinity;
    for (const { y } of sample(pts)) {
      expect(y).toBeGreaterThanOrEqual(prev - 1e-9);
      prev = y;
    }
  });

  it('stays inside 0..1 even when asked outside it', () => {
    const pts = [{ x: 0, y: 0 }, { x: 0.5, y: 0.9 }, { x: 1, y: 1 }];
    for (const x of [-1, -0.001, 0, 0.5, 1, 1.001, 2]) {
      const y = evalCurve(pts, x);
      expect(y).toBeGreaterThanOrEqual(0);
      expect(y).toBeLessThanOrEqual(1);
    }
  });

  // Two points at the same x is what a drag onto a neighbour produces; the
  // slope there is a division by zero waiting to happen.
  it('survives duplicate and unordered x without producing NaN', () => {
    for (const pts of [
      [{ x: 0, y: 0 }, { x: 0.5, y: 0.3 }, { x: 0.5, y: 0.7 }, { x: 1, y: 1 }],
      [{ x: 0, y: 0 }, { x: 0, y: 0.5 }, { x: 1, y: 1 }],
    ]) {
      for (const { y } of sample(pts, 50)) expect(Number.isFinite(y)).toBe(true);
    }
  });

  it('handles the empty curve as identity', () => {
    expect(evalCurve([], 0.42)).toBeCloseTo(0.42, 10);
  });
});

describe('curvePolyline', () => {
  it('returns steps+1 points spanning the full unit range', () => {
    const line = curvePolyline(CURVE_ENDPOINTS, 8);
    expect(line).toHaveLength(9);
    expect(line[0].x).toBe(0);
    expect(line[8].x).toBe(1);
    for (const p of line) expect(p.y).toBeCloseTo(p.x, 10);
  });
});
