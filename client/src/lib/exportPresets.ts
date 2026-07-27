// Export-options defaults/sanitization and the named export presets
// (Export dialog → Preset). A leaf module (imports only the API types) so
// both the store and the dialog can share it.
import type { ExportOptions, ExportPreset } from '@/api/settings';

// Mirror of the Go-side name clamp in internal/api/uisettings.go.
export const EXPORT_PRESET_NAME_MAX = 80;

export const DEFAULT_EXPORT_OPTIONS: ExportOptions = {
  format: 'jpeg',
  jpegQuality: 90,
  resizeMode: 'full',
  edgePx: 2160,
  colorSpace: 'srgb',
  sharpenTarget: 'off',
  sharpenAmount: 'standard',
  fileNameTemplate: '',
  exifMode: 'all',
  removeLocation: false,
  artist: '',
  copyright: '',
  watermarkId: '',
};

// Mirrors the server's normalizeExportOptions: missing or invalid fields
// from older blobs fall back to the dialog defaults.
export function sanitizeExportOptions(o: Partial<ExportOptions> | undefined): ExportOptions {
  return {
    // An older blob may still say 'tiff16'; that format is gone, so it falls
    // back to the jpeg default like any other unknown value.
    format:
      o?.format === 'tiff8' || o?.format === 'png' || o?.format === 'rawXmp' ? o.format : 'jpeg',
    jpegQuality:
      typeof o?.jpegQuality === 'number' && o.jpegQuality >= 1 && o.jpegQuality <= 100
        ? Math.round(o.jpegQuality)
        : DEFAULT_EXPORT_OPTIONS.jpegQuality,
    resizeMode: o?.resizeMode === 'edge' ? 'edge' : 'full',
    edgePx:
      typeof o?.edgePx === 'number' && o.edgePx >= 16 && o.edgePx <= 65536
        ? Math.round(o.edgePx)
        : DEFAULT_EXPORT_OPTIONS.edgePx,
    colorSpace:
      o?.colorSpace === 'adobergb' || o?.colorSpace === 'prophoto' ? o.colorSpace : 'srgb',
    sharpenTarget:
      o?.sharpenTarget === 'screen' || o?.sharpenTarget === 'matte' || o?.sharpenTarget === 'glossy'
        ? o.sharpenTarget
        : 'off',
    sharpenAmount:
      o?.sharpenAmount === 'low' || o?.sharpenAmount === 'high' ? o.sharpenAmount : 'standard',
    fileNameTemplate: typeof o?.fileNameTemplate === 'string' ? o.fileNameTemplate.trim() : '',
    exifMode: o?.exifMode === 'copyright' || o?.exifMode === 'none' ? o.exifMode : 'all',
    removeLocation: o?.removeLocation === true,
    artist: typeof o?.artist === 'string' ? o.artist.trim() : '',
    copyright: typeof o?.copyright === 'string' ? o.copyright.trim() : '',
    watermarkId: typeof o?.watermarkId === 'string' ? o.watermarkId : '',
  };
}

// sanitizeExportPresets narrows a stored preset list to the client shape,
// dropping malformed entries; option fields degrade individually through
// sanitizeExportOptions (same contract as sanitizeUserPresets).
export function sanitizeExportPresets(raw: unknown): ExportPreset[] {
  if (!Array.isArray(raw)) return [];
  const out: ExportPreset[] = [];
  for (const p of raw) {
    if (typeof p !== 'object' || p === null) continue;
    const { id, name, options } = p as Record<string, unknown>;
    if (typeof id !== 'string' || !id || typeof name !== 'string' || !name) continue;
    out.push({ id, name, options: sanitizeExportOptions(options as Partial<ExportOptions>) });
  }
  return out;
}

// Field-wise equality over sanitized options — drives the picker's
// "(modified)" indicator.
export function exportOptionsEqual(a: ExportOptions, b: ExportOptions): boolean {
  return (
    a.format === b.format &&
    a.jpegQuality === b.jpegQuality &&
    a.resizeMode === b.resizeMode &&
    a.edgePx === b.edgePx &&
    a.colorSpace === b.colorSpace &&
    a.sharpenTarget === b.sharpenTarget &&
    a.sharpenAmount === b.sharpenAmount &&
    a.fileNameTemplate === b.fileNameTemplate &&
    a.exifMode === b.exifMode &&
    a.removeLocation === b.removeLocation &&
    a.artist === b.artist &&
    a.copyright === b.copyright &&
    a.watermarkId === b.watermarkId
  );
}
