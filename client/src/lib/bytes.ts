/**
 * Human-readable byte size.
 *
 * `zero` is what a count of zero renders as, which is the one thing callers
 * genuinely disagree about: a photo whose size we have not read yet reads
 * '—', while a cache holding nothing, or a download sitting at 0 B/s, really
 * is zero and should say so.
 *
 * This lived as two implementations — one here-ish in exif.ts, one private to
 * SettingsDialog — that had drifted apart in both the zero case and where they
 * stopped printing a decimal, so a file size and a cache size were formatted
 * by different rules.
 */
export function formatBytes(bytes: number, zero = '—'): string {
  if (bytes <= 0) return zero;
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  // Whole bytes never get a decimal, and past 100 of any unit the decimal is
  // noise rather than precision.
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`;
}
