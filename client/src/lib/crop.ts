// Crop geometry shared by the loupe box sizing, the tile layer, and the crop
// overlay. Mirrors internal/edit/edit.go (OutputDims) and internal/pyramid/
// geometry.go so the client sizes the rendered image identically to what the
// backend produced — no round trip needed.
import type { Mask, Params } from '@/api/edit';
import type { Photo } from '@/api/library';

// displayDims is the on-screen, orientation-corrected size of the full frame:
// the coordinate space the crop rectangle and straighten angle live in.
export function displayDims(photo: Photo): [number, number] {
  if (photo.orientation === 5 || photo.orientation === 6) return [photo.height, photo.width];
  return [photo.width, photo.height];
}

// AspectGeometry is the subset of edit geometry that changes the rendered
// output size. Full Params satisfies it, and so does the Photo DTO — the
// server mirrors rotate/cropW/cropH onto every photo (and edit patch) so the
// grid can size natural-layout cells without loading edit state.
export interface AspectGeometry {
  rotate?: number;
  cropW?: number;
  cropH?: number;
}

// hasCrop reports whether a real crop rectangle is set (a straighten angle
// alone rotates the full frame without cropping).
export function hasCrop(p: AspectGeometry | null | undefined): boolean {
  return !!p && (p.cropW ?? 0) > 0 && (p.cropH ?? 0) > 0;
}

// rotateTurns is the coarse rotation as canonical quarter turns clockwise in
// 0..3. Matches edit.Params.RotateTurns on the Go side.
export function rotateTurns(p: AspectGeometry | null | undefined): number {
  return p ? (((p.rotate ?? 0) % 4) + 4) % 4 : 0;
}

// rotatedDims applies the coarse 90° rotation to the full display dims. The
// crop rectangle and straighten angle live in this rotated space, so it is
// the flat-frame size the crop overlay works against.
export function rotatedDims(fullW: number, fullH: number, p: AspectGeometry | null | undefined): [number, number] {
  return rotateTurns(p) % 2 !== 0 ? [fullH, fullW] : [fullW, fullH];
}

// renderedDims maps the full display dimensions to the rendered size after the
// coarse rotation and crop. The straighten angle does not change the output
// size. Matches edit.Params.OutputDims on the Go side.
export function renderedDims(fullW: number, fullH: number, p: AspectGeometry | null | undefined): [number, number] {
  [fullW, fullH] = rotatedDims(fullW, fullH, p);
  if (!hasCrop(p)) return [fullW, fullH];
  return [Math.max(1, Math.round(p!.cropW! * fullW)), Math.max(1, Math.round(p!.cropH! * fullH))];
}

// rotateCropPatch returns the params patch for one more quarter turn of the
// DISPLAYED image in the given direction. The stored transform is
// flip∘rotate, so under a mirror the stored turn runs the opposite way
// (mirror∘R_cw = R_ccw∘mirror). An existing crop rectangle is remapped in
// display space so the same pixels stay selected (a 90° CW display turn maps
// a point (x,y) to (1-y, x), so the rect follows its corners), and mask
// geometry follows through the same point map (see remapMasks). The
// straighten angle turns with the frame and needs no change.
export function rotateCropPatch(p: Params, dir: 'cw' | 'ccw'): Partial<Params> {
  const cwTurn = p.flipH ? 3 : 1;
  const patch: Partial<Params> = {
    rotate: (rotateTurns(p) + (dir === 'cw' ? cwTurn : 4 - cwTurn)) % 4,
  };
  if (hasCrop(p)) {
    if (dir === 'cw') {
      patch.cropX = 1 - (p.cropY + p.cropH);
      patch.cropY = p.cropX;
    } else {
      patch.cropX = p.cropY;
      patch.cropY = 1 - (p.cropX + p.cropW);
    }
    patch.cropW = p.cropH;
    patch.cropH = p.cropW;
  }
  const map: PointMap = dir === 'cw' ? (x, y) => [1 - y, x] : (x, y) => [y, 1 - x];
  const masks = remapMasks(p, map, true);
  if (masks) patch.masks = masks;
  return patch;
}

// flipCropPatch mirrors the displayed image about the given axis. Both axes
// toggle FlipH — a vertical mirror is the horizontal one plus a half turn —
// and an existing crop rectangle reflects along the mirrored axis while the
// straighten angle negates (a tilt reads the other way in a mirror). Mask
// geometry reflects through the same point map.
export function flipCropPatch(p: Params, axis: 'h' | 'v'): Partial<Params> {
  const patch: Partial<Params> = { flipH: !p.flipH };
  if (axis === 'v') patch.rotate = (rotateTurns(p) + 2) % 4;
  if (hasCrop(p)) {
    if (axis === 'h') patch.cropX = 1 - (p.cropX + p.cropW);
    else patch.cropY = 1 - (p.cropY + p.cropH);
  }
  if (p.cropAngle !== 0) patch.cropAngle = -p.cropAngle;
  const map: PointMap = axis === 'h' ? (x, y) => [1 - x, y] : (x, y) => [x, 1 - y];
  const masks = remapMasks(p, map, false);
  if (masks) patch.masks = masks;
  return patch;
}

type PointMap = (x: number, y: number) => [number, number];

// remapMasks pushes every parametric mask's geometry through a display-space
// point map so masks stay glued to image content when the displayed frame is
// quarter-turned or mirrored — the exact treatment the crop rectangle gets.
// AI masks pass through untouched: they carry no geometry, and the server
// re-orients their bitmaps from the edit's Rotate/FlipH at load time.
//
// Radial subtleties, both aspect-free by construction:
//   - Quarter turn: rx/ry swap with NO aspect factor. The radii are fractions
//     of frame width/height, and an odd turn swaps those same dims, so the
//     aspect terms cancel; the tilt angle is invariant (the ellipse axes and
//     the frame axes rotate together — identical mod 180).
//   - Mirror: radii keep, the tilt reads the other way (angle negates,
//     normalized to [0,180) like the server does).
function remapMasks(p: Params, map: PointMap, quarterTurn: boolean): Mask[] | undefined {
  if (!p.masks?.length) return undefined;
  return p.masks.map((m): Mask => {
    switch (m.type) {
      case 'linear': {
        const [x0, y0] = map(m.x0 ?? 0, m.y0 ?? 0);
        const [x1, y1] = map(m.x1 ?? 0, m.y1 ?? 0);
        return { ...m, x0, y0, x1, y1 };
      }
      case 'radial': {
        const [cx, cy] = map(m.cx ?? 0, m.cy ?? 0);
        if (quarterTurn) return { ...m, cx, cy, rx: m.ry, ry: m.rx };
        return { ...m, cx, cy, angle: ((-(m.angle ?? 0) % 180) + 180) % 180 };
      }
      case 'brush': {
        if (!m.strokes?.length) return m;
        return {
          ...m,
          strokes: m.strokes.map((s) => {
            const pts = s.pts.slice();
            for (let i = 0; i + 1 < pts.length; i += 2) {
              [pts[i], pts[i + 1]] = map(pts[i], pts[i + 1]);
            }
            return { ...s, pts };
          }),
        };
      }
      default:
        return m;
    }
  });
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

// --- Mask coordinate mapping (twin of internal/pyramid/mask.go maskFrame) ---
// Mask geometry is stored in fractions of the oriented frame (the rotated,
// pre-straighten, pre-crop space the crop rectangle lives in). The displayed
// image is the straightened crop of that frame, so the overlay maps pointer
// positions (fractions of the displayed image box) into frame fractions and
// back. frameW/frameH are the frame's pixel dims (rotatedDims) — the rotation
// is not aspect invariant, so the math runs in pixel space.

// frameFromDisplay maps a displayed-image fraction to a frame fraction:
// offset by the crop origin, then un-rotate the straighten angle about the
// frame center (the same inverse map ApplyGeometry samples with).
export function frameFromDisplay(
  bx: number,
  by: number,
  p: Params | null | undefined,
  frameW: number,
  frameH: number,
): [number, number] {
  const crop = hasCrop(p);
  const px = ((crop ? p!.cropX + bx * p!.cropW : bx)) * frameW;
  const py = ((crop ? p!.cropY + by * p!.cropH : by)) * frameH;
  const rad = (-(p?.cropAngle ?? 0) * Math.PI) / 180;
  const c = Math.cos(rad);
  const s = Math.sin(rad);
  const fcx = frameW / 2;
  const fcy = frameH / 2;
  const dx = px - fcx;
  const dy = py - fcy;
  return [(fcx + dx * c - dy * s) / frameW, (fcy + dx * s + dy * c) / frameH];
}

// displayFromFrame is the inverse: rotate the frame point by +cropAngle about
// the center, then express it in crop-rectangle fractions.
export function displayFromFrame(
  fx: number,
  fy: number,
  p: Params | null | undefined,
  frameW: number,
  frameH: number,
): [number, number] {
  const rad = ((p?.cropAngle ?? 0) * Math.PI) / 180;
  const c = Math.cos(rad);
  const s = Math.sin(rad);
  const fcx = frameW / 2;
  const fcy = frameH / 2;
  const dx = fx * frameW - fcx;
  const dy = fy * frameH - fcy;
  const px = (fcx + dx * c - dy * s) / frameW;
  const py = (fcy + dx * s + dy * c) / frameH;
  if (!hasCrop(p)) return [px, py];
  return [(px - p!.cropX) / p!.cropW, (py - p!.cropY) / p!.cropH];
}

// quant4 rounds a fractional frame coordinate to 1e-4 — the client twin of the
// server's quant4, so pointer-event float noise never churns edit hashes.
// Shared by every overlay that writes frame-fraction geometry (masks, spots).
export function quant4(v: number): number {
  return Math.round(v * 1e4) / 1e4;
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
