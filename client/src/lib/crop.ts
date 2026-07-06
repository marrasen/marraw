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
