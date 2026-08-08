import { describe, expect, it } from 'vitest';

import type { Photo } from '@/api/library';
import { gapGroupStarts, gapLabel, groupByGap, rangeLabel } from '@/lib/timeGaps';

const MIN = 60;

// Only takenAt is read; a real Photo carries forty other fields.
function photos(...takenAt: number[]): Photo[] {
  return takenAt.map((t, i) => ({ id: i + 1, takenAt: t }) as unknown as Photo);
}

// Group sizes are easier to reason about than start indices when comparing two
// orderings of the same shoot.
function sizes(starts: readonly number[], total: number): number[] {
  return starts.map((s, i) => (i + 1 < starts.length ? starts[i + 1] : total) - s);
}

describe('gapGroupStarts', () => {
  const base = 1_700_000_000;

  it('opens a group only where the gap exceeds the threshold', () => {
    // 0s, 30s, then 10 min later, then 20 s after that.
    const t = [base, base + 30, base + 10 * MIN, base + 10 * MIN + 20];
    expect(gapGroupStarts(t, 6)).toEqual([0, 2]);
    // A threshold above the largest gap leaves one group.
    expect(gapGroupStarts(t, 30)).toEqual([0]);
    // Below the smallest gap, every frame is its own group.
    expect(gapGroupStarts(t, 0.1)).toEqual([0, 1, 2, 3]);
  });

  it('treats a gap exactly on the threshold as inside the group', () => {
    const t = [base, base + 6 * MIN];
    expect(gapGroupStarts(t, 6)).toEqual([0]); // strictly greater opens a group
    expect(gapGroupStarts(t, 5.9)).toEqual([0, 1]);
  });

  it('collapses to one flat group without a threshold', () => {
    const t = [base, base + 10 * MIN];
    expect(gapGroupStarts(t, null)).toEqual([0]);
    expect(gapGroupStarts(t, 0)).toEqual([0]);
    expect(gapGroupStarts(t, -5)).toEqual([0]);
  });

  it('has nothing to group in an empty list', () => {
    expect(gapGroupStarts([], 6)).toEqual([]);
    expect(gapGroupStarts([], null)).toEqual([]);
  });

  // A freshly scanned folder has no capture times until the metadata pass
  // reaches it. Those frames must not each open a group, or the grid breaks
  // into one header per photo while the backfill runs.
  it('never opens a gap on a frame with no capture time', () => {
    const t = [base, 0, 0, base + 20];
    expect(gapGroupStarts(t, 6)).toEqual([0]);
    // Untimed frames also do not hide a real gap that straddles them.
    expect(gapGroupStarts([base, 0, base + 30 * MIN], 6)).toEqual([0, 2]);
  });

  it('groups a shoot with no times at all as one', () => {
    expect(gapGroupStarts([0, 0, 0], 6)).toEqual([0]);
  });

  // The module takes |Δt| specifically so that oldest-first and newest-first
  // orderings of the same shoot break into the same groups. If this drifted,
  // reversing the sort would silently reshape the grid — and ↑/↓, which
  // navigates by these boundaries, would land on the wrong frame.
  it('finds the same groups whichever way the shoot is sorted', () => {
    const ascending = [
      base, base + 5, base + 11,
      base + 40 * MIN, base + 40 * MIN + 9,
      base + 200 * MIN,
    ];
    const descending = [...ascending].reverse();

    const up = sizes(gapGroupStarts(ascending, 6), ascending.length);
    const down = sizes(gapGroupStarts(descending, 6), descending.length);
    expect(up).toEqual([3, 2, 1]);
    expect(down).toEqual([...up].reverse());
  });
});

describe('groupByGap', () => {
  const base = 1_700_000_000;

  it('carries each group its own span and the dead time before it', () => {
    // Second group opens 45 min after the first group's LAST frame, which is
    // where the dead time is measured from — not from where that group began.
    const lastOfFirst = base + 30;
    const g = groupByGap(photos(base, lastOfFirst, lastOfFirst + 45 * MIN, lastOfFirst + 46 * MIN), 6);
    expect(g).toHaveLength(2);
    expect(g[0].photos).toHaveLength(2);
    expect(g[0].start).toBe(base);
    expect(g[0].end).toBe(lastOfFirst);
    expect(g[0].gapBeforeMin).toBeNull(); // nothing precedes the first group

    expect(g[1].photos).toHaveLength(2);
    expect(g[1].gapBeforeMin).toBe(45);
  });

  it('accounts for every photo exactly once, in order', () => {
    const all = photos(base, base + 5, base + 90 * MIN, base + 90 * MIN + 5, base + 300 * MIN);
    const flat = groupByGap(all, 6).flatMap((g) => g.photos);
    expect(flat).toEqual(all);
  });

  it('reports a group with no capture times rather than dropping it', () => {
    const g = groupByGap(photos(0, 0), 6);
    expect(g).toHaveLength(1);
    expect(g[0].start).toBe(0);
    expect(rangeLabel(g[0])).toBe('no time');
  });

  it('has no groups for no photos', () => {
    expect(groupByGap([], 6)).toEqual([]);
  });
});

describe('gapLabel', () => {
  it('counts minutes below an hour and a half, hours above', () => {
    expect(gapLabel(7)).toBe('+7 min gap');
    expect(gapLabel(89)).toBe('+89 min gap');
    expect(gapLabel(90)).toBe('+1.5 h gap');
    expect(gapLabel(240)).toBe('+4 h gap');
  });
});
