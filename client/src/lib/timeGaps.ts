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
 * gapGroupStarts returns the index of each group's first photo: a new group
 * starts when the gap between consecutive frames exceeds thresholdMin minutes.
 * Photos keep their list order; the gap is |Δt| so both capture-time sort
 * directions group identically (name order is not time-monotonic — callers
 * pass null there, see selectGapMinutes). Frames without a capture time never
 * open a gap. thresholdMin null/0 = one flat group, i.e. `[0]`.
 *
 * This is the boundary logic shared by everything that lays out groups — the
 * grid, the contact sheet, and keyboard row navigation. They must agree on
 * where a group begins or ↑/↓ lands on the wrong frame.
 */
export function gapGroupStarts(takenAt: readonly number[], thresholdMin: number | null): number[] {
  if (takenAt.length === 0) return [];
  if (!thresholdMin || thresholdMin <= 0) return [0];
  const starts: number[] = [];
  let lastTaken = 0;
  for (let i = 0; i < takenAt.length; i++) {
    const t = takenAt[i];
    const gapSec = t > 0 && lastTaken > 0 ? Math.abs(t - lastTaken) : 0;
    if (starts.length === 0 || gapSec > thresholdMin * 60) starts.push(i);
    if (t > 0) lastTaken = t;
  }
  return starts;
}

/**
 * groupByGap splits photos into time-gap groups at the gapGroupStarts
 * boundaries, carrying each group's time range and the dead time before it.
 */
export function groupByGap(photos: Photo[], thresholdMin: number | null): TimeGroup[] {
  const starts = gapGroupStarts(
    photos.map((p) => p.takenAt),
    thresholdMin,
  );
  const groups: TimeGroup[] = [];
  let lastTaken = 0;
  for (let g = 0; g < starts.length; g++) {
    const to = g + 1 < starts.length ? starts[g + 1] : photos.length;
    const slice = photos.slice(starts[g], to);
    const first = slice[0];
    // |Δt| like gapGroupStarts: in newest-first order the dead time sits
    // chronologically after this group, but it is still the gap at the
    // boundary displayed above it.
    const gapSec = first.takenAt > 0 && lastTaken > 0 ? Math.abs(first.takenAt - lastTaken) : 0;
    const cur: TimeGroup = {
      photos: slice,
      start: first.takenAt,
      end: first.takenAt,
      gapBeforeMin: g === 0 ? null : Math.round(gapSec / 60),
    };
    for (const p of slice) {
      if (p.takenAt > 0) {
        cur.end = p.takenAt;
        if (cur.start === 0) cur.start = p.takenAt;
        lastTaken = p.takenAt;
      }
    }
    groups.push(cur);
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
