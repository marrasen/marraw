// Crop geometry shared by the loupe box sizing, the tile layer, and the crop
// overlay. Mirrors internal/edit/edit.go (OutputDims) and internal/pyramid/
// geometry.go so the client sizes the rendered image identically to what the
// backend produced — no round trip needed.
import type { Params } from '@/api/edits';
import type { Photo } from '@/api/library';

// displayDims is the on-screen, orientation-corrected size of the full frame:
// the coordinate space the crop rectangle and straighten angle live in.
export function displayDims(photo: Photo): [number, number] {
  if (photo.orientation === 5 || photo.orientation === 6) return [photo.height, photo.width];
  return [photo.width, photo.height];
}

// hasCrop reports whether a real crop rectangle is set (a straighten angle
// alone rotates the full frame without cropping).
export function hasCrop(p: Params | null | undefined): boolean {
  return !!p && p.cropW > 0 && p.cropH > 0;
}

// renderedDims maps the full display dimensions to the rendered size after the
// crop. The straighten angle does not change the output size. Matches
// edit.Params.OutputDims on the Go side.
export function renderedDims(fullW: number, fullH: number, p: Params | null | undefined): [number, number] {
  if (!hasCrop(p)) return [fullW, fullH];
  return [Math.max(1, Math.round(p!.cropW * fullW)), Math.max(1, Math.round(p!.cropH * fullH))];
}

// A neutral (full-frame, unrotated) crop rectangle.
export const FULL_CROP = { cropX: 0, cropY: 0, cropW: 1, cropH: 1, cropAngle: 0 } as const;

export interface CropRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

// Coverage tolerance in frame-fraction space. fitCropToRotation's binary
// search parks rect corners within ~2⁻²⁴ of exactly ON the rotated-frame
// boundary, and the rect round-trips through the store between events —
// without slack, a corner that is mathematically on the edge flunks strict
// coverage and every subsequent interaction gets rejected. The cost is at
// most a fraction of a pixel of black on real frames — invisible.
const COVER_EPS = 1e-4;

// coveredByRotation reports whether a frame-fraction point (x, y in [0,1]) is
// still covered by the source image after the frame is rotated by angleDeg —
// i.e. NOT in the black wedge a straighten exposes. aspect = frameW / frameH;
// the check runs in aspect-corrected space because rotation is not aspect
// invariant. Mirrors internal/pyramid/geometry.go's inverse map.
export function coveredByRotation(x: number, y: number, angleDeg: number, aspect: number): boolean {
  if (angleDeg === 0) return true;
  const rad = (angleDeg * Math.PI) / 180;
  const cosN = Math.cos(-rad);
  const sinN = Math.sin(-rad);
  const cx = aspect / 2;
  const cy = 0.5;
  const dx = x * aspect - cx;
  const dy = y - cy;
  const sx = cx + dx * cosN - dy * sinN;
  const sy = cy + dx * sinN + dy * cosN;
  return sx >= -COVER_EPS && sx <= aspect + COVER_EPS && sy >= -COVER_EPS && sy <= 1 + COVER_EPS;
}

// rectCornersCovered reports whether all four corners of a crop rect lie within
// the rotated frame. Because that region is convex, all-corners-inside implies
// the whole rectangle is black-free.
export function rectCornersCovered(r: CropRect, angleDeg: number, aspect: number): boolean {
  return (
    coveredByRotation(r.x, r.y, angleDeg, aspect) &&
    coveredByRotation(r.x + r.w, r.y, angleDeg, aspect) &&
    coveredByRotation(r.x, r.y + r.h, angleDeg, aspect) &&
    coveredByRotation(r.x + r.w, r.y + r.h, angleDeg, aspect)
  );
}

// fitCropToRotation shrinks a crop rect (about its own centre, preserving
// aspect) to the largest black-free rectangle for the given straighten angle.
// If the centre itself lies in the black wedge it recentres on the frame
// centre first (always covered for angles below 45°). Returns the rect
// unchanged when it is already valid or the angle is zero.
export function fitCropToRotation(r: CropRect, angleDeg: number, aspect: number): CropRect {
  if (angleDeg === 0 || rectCornersCovered(r, angleDeg, aspect)) return r;
  let cx = r.x + r.w / 2;
  let cy = r.y + r.h / 2;
  if (!coveredByRotation(cx, cy, angleDeg, aspect)) {
    cx = 0.5;
    cy = 0.5;
  }
  // Binary-search the largest scale in [0,1] that keeps every corner covered.
  let lo = 0;
  let hi = 1;
  for (let i = 0; i < 24; i++) {
    const f = (lo + hi) / 2;
    const w = r.w * f;
    const h = r.h * f;
    if (rectCornersCovered({ x: cx - w / 2, y: cy - h / 2, w, h }, angleDeg, aspect)) lo = f;
    else hi = f;
  }
  const w = r.w * lo;
  const h = r.h * lo;
  return { x: cx - w / 2, y: cy - h / 2, w, h };
}

// maxCoveredT finds the largest t ∈ [0,1] for which make(t) stays black-free
// under the given straighten angle, assuming make(0) is covered. The caller's
// make() runs the full candidate pipeline (edge clamps, minimum size, aspect
// lock, frame clamps), so every probed rect honors every invariant at once.
export function maxCoveredT(make: (t: number) => CropRect, angleDeg: number, aspect: number): number {
  if (rectCornersCovered(make(1), angleDeg, aspect)) return 1;
  let lo = 0;
  let hi = 1;
  for (let i = 0; i < 20; i++) {
    const t = (lo + hi) / 2;
    if (rectCornersCovered(make(t), angleDeg, aspect)) lo = t;
    else hi = t;
  }
  return lo;
}

// slideMoveRect clamps a whole-rect move (same w/h) so it slides along the
// tilted frame edge instead of freezing: keep the largest usable x-component
// first, then the largest y from there. Per-axis sequential is deliberate —
// a single-t line clamp just parks at the boundary and the drag dies; taking
// the axes in turn preserves the along-edge component of the motion. Falls
// back to refitting `from` if it is itself uncovered (mid-resync race).
export function slideMoveRect(from: CropRect, to: CropRect, angleDeg: number, aspect: number): CropRect {
  if (!rectCornersCovered(from, angleDeg, aspect)) return fitCropToRotation(from, angleDeg, aspect);
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const tx = maxCoveredT((t) => ({ ...from, x: from.x + dx * t }), angleDeg, aspect);
  const afterX = { ...from, x: from.x + dx * tx };
  const ty = maxCoveredT((t) => ({ ...afterX, y: afterX.y + dy * t }), angleDeg, aspect);
  return { ...afterX, y: afterX.y + dy * ty };
}

// Common aspect presets for the crop overlay. `null` ratio means freeform.
export interface AspectPreset {
  key: string;
  label: string;
  ratio: number | null; // width / height, in the displayed orientation
}
export const ASPECT_PRESETS: AspectPreset[] = [
  { key: 'free', label: 'Free', ratio: null },
  { key: 'orig', label: 'Original', ratio: null }, // resolved from the photo
  { key: '1:1', label: '1:1', ratio: 1 },
  { key: '3:2', label: '3:2', ratio: 3 / 2 },
  { key: '4:3', label: '4:3', ratio: 4 / 3 },
  { key: '16:9', label: '16:9', ratio: 16 / 9 },
];
