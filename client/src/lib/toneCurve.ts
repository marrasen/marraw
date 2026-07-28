// Tone-curve helpers shared by the ToneCurve widget (drawing + editing) and
// the develop panel's dirty indicator. The render math lives in Go
// (pyramid.buildCurveLUT); this is the client mirror used only to draw the
// preview line and decide whether the curve bends anything — it must agree
// with the backend's monotone-cubic (Fritsch–Carlson) interpolation so the
// on-screen curve matches the rendered pixels.
//
// A leaf module: import only the generated type, nothing that pulls the panel
// back in.
import type { CurvePoint, Params } from '@/api/edit';

// The identity curve: the two pinned endpoints. Stored as `undefined` on
// Params (neutral, omitted on the wire); materialized to these when the user
// first grabs the widget.
export const CURVE_ENDPOINTS: CurvePoint[] = [
  { x: 0, y: 0 },
  { x: 1, y: 1 },
];

// The four curve channels: the master (RGB) curve shapes overall tone, and the
// per-channel curves grade color on top of it — the same order the render
// composes them in (pyramid.buildLookLUTs). `stroke` is the widget's line
// color for that channel.
export type CurveKey = 'toneCurve' | 'toneCurveR' | 'toneCurveG' | 'toneCurveB';

export const CURVE_CHANNELS: { key: CurveKey; label: string; stroke: string }[] = [
  { key: 'toneCurve', label: 'RGB', stroke: 'currentColor' },
  { key: 'toneCurveR', label: 'R', stroke: '#ef4444' },
  { key: 'toneCurveG', label: 'G', stroke: '#22c55e' },
  { key: 'toneCurveB', label: 'B', stroke: '#3b82f6' },
];

export const CURVE_KEYS: CurveKey[] = CURVE_CHANNELS.map((c) => c.key);

// curveOf reads one channel's stored curve off a draft.
export function curveOf(p: Params, key: CurveKey): CurvePoint[] | undefined {
  return p[key];
}

// hasToneCurve reports whether a curve bends anything — the client mirror of
// edit.Params.HasToneCurve (Go): true only with ≥2 points and one off the
// diagonal. An undefined, single-point, or all-diagonal curve is identity.
export function hasToneCurve(pts?: CurvePoint[]): boolean {
  if (!pts || pts.length < 2) return false;
  return pts.some((p) => p.y !== p.x);
}

// Fritsch–Carlson monotone tangents for the given control points (assumed
// sorted by x, ≥2 points). Returns one slope per point; monotone so the drawn
// curve never overshoots or inverts between points, matching buildCurveLUT.
function tangents(pts: CurvePoint[]): number[] {
  const n = pts.length;
  const d: number[] = [];
  for (let i = 0; i < n - 1; i++) {
    const dx = pts[i + 1].x - pts[i].x;
    d.push(dx <= 0 ? 0 : (pts[i + 1].y - pts[i].y) / dx);
  }
  const m = new Array<number>(n);
  m[0] = d[0];
  m[n - 1] = d[n - 2];
  for (let i = 1; i < n - 1; i++) {
    m[i] = d[i - 1] * d[i] <= 0 ? 0 : (d[i - 1] + d[i]) / 2;
  }
  for (let i = 0; i < n - 1; i++) {
    if (d[i] === 0) {
      m[i] = 0;
      m[i + 1] = 0;
      continue;
    }
    const a = m[i] / d[i];
    const b = m[i + 1] / d[i];
    const h = a * a + b * b;
    if (h > 9) {
      const t = 3 / Math.sqrt(h);
      m[i] = t * a * d[i];
      m[i + 1] = t * b * d[i];
    }
  }
  return m;
}

function clampUnit(v: number): number {
  return v < 0 ? 0 : v > 1 ? 1 : v;
}

// evalCurve samples the monotone-cubic curve at input x in 0..1, returning the
// output in 0..1. Flat past the endpoints (clamped endpoints), matching the
// backend LUT.
export function evalCurve(pts: CurvePoint[], x: number): number {
  const n = pts.length;
  if (n === 0) return clampUnit(x);
  if (x <= pts[0].x) return clampUnit(pts[0].y);
  if (x >= pts[n - 1].x) return clampUnit(pts[n - 1].y);
  const m = tangents(pts);
  let i = 0;
  while (i < n - 1 && x > pts[i + 1].x) i++;
  const h = pts[i + 1].x - pts[i].x;
  const t = (x - pts[i].x) / h;
  const t2 = t * t;
  const t3 = t2 * t;
  const h00 = 2 * t3 - 3 * t2 + 1;
  const h10 = t3 - 2 * t2 + t;
  const h01 = -2 * t3 + 3 * t2;
  const h11 = t3 - t2;
  return clampUnit(h00 * pts[i].y + h10 * h * m[i] + h01 * pts[i + 1].y + h11 * h * m[i + 1]);
}

// curvePolyline samples the curve into `steps`+1 points in unit space, for
// drawing the preview line as an SVG polyline.
export function curvePolyline(pts: CurvePoint[], steps = 48): CurvePoint[] {
  const out: CurvePoint[] = [];
  for (let i = 0; i <= steps; i++) {
    const x = i / steps;
    out.push({ x, y: evalCurve(pts, x) });
  }
  return out;
}
