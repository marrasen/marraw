import { describe, expect, it } from 'vitest';

import { rowNeighbor, uniformRowStarts } from '@/lib/gridNav';

describe('uniformRowStarts', () => {
  it('lays out one ungrouped run', () => {
    expect(uniformRowStarts(7, 3, [0])).toEqual([0, 3, 6]);
  });

  it('restarts rows at every group, leaving each group a ragged last row', () => {
    // Groups at 0 and 4, four columns: the first group is one ragged row of 4,
    // and the second starts a fresh row rather than filling the first.
    expect(uniformRowStarts(9, 4, [0, 4])).toEqual([0, 4, 8]);
    // A group of 2 followed by a group of 5.
    expect(uniformRowStarts(7, 3, [0, 2])).toEqual([0, 2, 5]);
  });

  it('tolerates the shapes callers can hand it', () => {
    expect(uniformRowStarts(0, 3, [0])).toEqual([]);
    expect(uniformRowStarts(-1, 3, [0])).toEqual([]);
    expect(uniformRowStarts(5, 0, [0])).toEqual([0, 1, 2, 3, 4]); // cols floors at 1
    expect(uniformRowStarts(5, 3, [])).toEqual([0, 3]); // no groups = one run
  });
});

describe('rowNeighbor', () => {
  // A uniform 3-wide grid over 8 photos: rows [0,1,2] [3,4,5] [6,7].
  const rows = [0, 3, 6];

  it('keeps its column moving between full rows', () => {
    expect(rowNeighbor(1, 8, rows, 1)).toBe(4);
    expect(rowNeighbor(4, 8, rows, -1)).toBe(1);
  });

  it('clamps into a ragged last row instead of overshooting the list', () => {
    // Column 2 of the middle row has no counterpart in a row holding two.
    expect(rowNeighbor(5, 8, rows, 1)).toBe(7);
  });

  it('comes back to the same column leaving a ragged row', () => {
    expect(rowNeighbor(7, 8, rows, -1)).toBe(4);
  });

  it('clamps to the ends of the list rather than refusing to move', () => {
    expect(rowNeighbor(1, 8, rows, -1)).toBe(0);
    expect(rowNeighbor(7, 8, rows, 1)).toBe(7);
    expect(rowNeighbor(6, 8, rows, 1)).toBe(7);
  });

  it('steps flat when there is no row model — the loupe and filmstrip', () => {
    expect(rowNeighbor(3, 8, [], 1)).toBe(4);
    expect(rowNeighbor(3, 8, [], -1)).toBe(2);
    expect(rowNeighbor(0, 8, [], -1)).toBe(0);
    expect(rowNeighbor(7, 8, [], 1)).toBe(7);
  });

  it('answers 0 for an empty list', () => {
    expect(rowNeighbor(0, 0, rows, 1)).toBe(0);
    expect(rowNeighbor(3, 0, [], -1)).toBe(0);
  });

  // The row model is rebuilt by a different render than the one holding
  // `total`, so a mode switch or a filter change can leave it describing more
  // photos than exist. Landing outside the list would index undefined.
  it('never leaves the list when the row model is stale', () => {
    const stale = [0, 3, 6, 9, 12];
    for (const dir of [-1, 1] as const) {
      for (let i = 0; i < 5; i++) {
        const got = rowNeighbor(i, 5, stale, dir);
        expect(got).toBeGreaterThanOrEqual(0);
        expect(got).toBeLessThanOrEqual(4);
      }
    }
  });

  describe('justified rows, matched by pixel centre', () => {
    // Two rows of unequal counts: [0,1] then [2,3,4]. An ordinal column would
    // put frame 1 above frame 3; visually it sits above frame 4.
    const jrows = [0, 2];
    const centers = [0.25, 0.75, 0.17, 0.5, 0.83];

    it('lands on the frame nearest in x, not the nearest in ordinal', () => {
      expect(rowNeighbor(1, 5, jrows, 1, centers)).toBe(4);
      expect(rowNeighbor(0, 5, jrows, 1, centers)).toBe(2);
      expect(rowNeighbor(3, 5, jrows, -1, centers)).toBe(0);
      expect(rowNeighbor(4, 5, jrows, -1, centers)).toBe(1);
    });

    it('falls back to ordinal when the centres do not cover the list', () => {
      // A short centres array is a stale layout; ordinal is wrong but safe.
      expect(rowNeighbor(1, 5, jrows, 1, [0.25, 0.75])).toBe(3);
    });
  });
});
