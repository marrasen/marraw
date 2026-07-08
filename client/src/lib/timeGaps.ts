import type { Photo } from '@/api/library';

// TimeGroup is one burst/happening in the Cull scrubber: consecutive frames
// whose inter-frame gap stays under the threshold.
export interface TimeGroup {
  photos: Photo[];
  start: number; // unix seconds of first frame (0 = unknown)
  end: number;
  /** Minutes of dead time before this group; null for the first group. */
  gapBeforeMin: number | null;
}

/**
 * groupByGap splits photos into time-gap groups: a new group starts when the
 * gap between consecutive frames exceeds thresholdMin minutes. Photos keep
 * their list order (file-name order tracks capture order); frames without a
 * capture time never open a gap. thresholdMin null/0 = one flat group.
 */
export function groupByGap(photos: Photo[], thresholdMin: number | null): TimeGroup[] {
  if (photos.length === 0) return [];
  if (!thresholdMin || thresholdMin <= 0) {
    return [
      {
        photos,
        start: photos[0]?.takenAt ?? 0,
        end: photos[photos.length - 1]?.takenAt ?? 0,
        gapBeforeMin: null,
      },
    ];
  }
  const groups: TimeGroup[] = [];
  let cur: TimeGroup | null = null;
  let lastTaken = 0;
  for (const p of photos) {
    const gapSec = p.takenAt > 0 && lastTaken > 0 ? p.takenAt - lastTaken : 0;
    if (cur == null || gapSec > thresholdMin * 60) {
      cur = {
        photos: [],
        start: p.takenAt,
        end: p.takenAt,
        gapBeforeMin: cur == null ? null : Math.round(gapSec / 60),
      };
      groups.push(cur);
    }
    cur.photos.push(p);
    if (p.takenAt > 0) {
      cur.end = p.takenAt;
      if (cur.start === 0) cur.start = p.takenAt;
      lastTaken = p.takenAt;
    }
  }
  return groups;
}

const fmt = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', hour12: false });
const dayFmt = new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'short' });

export function timeLabel(unixSec: number): string {
  return unixSec > 0 ? fmt.format(new Date(unixSec * 1000)) : '—';
}

/** "Tue 12 Mar" — day prefix for grids whose groups span multiple days. */
export function dayLabel(unixSec: number): string {
  return unixSec > 0 ? dayFmt.format(new Date(unixSec * 1000)) : '';
}

/** "09:12 – 09:18" (single time when the group spans under a minute). */
export function rangeLabel(g: TimeGroup): string {
  if (g.start === 0) return 'no time';
  const a = timeLabel(g.start);
  const b = timeLabel(g.end);
  return a === b ? a : `${a} – ${b}`;
}

export function gapLabel(min: number): string {
  if (min >= 90) {
    const h = Math.round(min / 6) / 10;
    return `+${h} h gap`;
  }
  return `+${min} min gap`;
}
