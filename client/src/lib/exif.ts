// Formatters for the photo's EXIF/technical metadata, shared by the develop
// panel header and the Info tab. Values come off the generated Photo record
// (api/library.ts) as raw numbers; these turn them into how photographers
// read them.

export function formatShutter(s: number): string {
  if (s <= 0) return '—';
  if (s >= 1) return `${s.toFixed(1)}s`;
  return `1/${Math.round(1 / s)}s`;
}

// EXIF apertures arrive as raw floats (5.599999999…); one decimal is how
// f-numbers are spoken, and trailing .0 is dropped (ƒ/8, not ƒ/8.0).
export function formatAperture(a: number): string {
  if (a <= 0) return '—';
  return String(Math.round(a * 10) / 10);
}


// "6000 × 4000 · 24.0 MP" — dimensions plus megapixels, or a dash if unknown.
export function formatResolution(width: number, height: number): string {
  if (width <= 0 || height <= 0) return '—';
  const mp = (width * height) / 1_000_000;
  return `${width} × ${height} · ${mp.toFixed(1)} MP`;
}

// takenAt is unix seconds (0 = unknown). Locale date + time, minute precision.
export function formatCaptured(takenAt: number): string {
  if (!takenAt) return '—';
  const d = new Date(takenAt * 1000);
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}
