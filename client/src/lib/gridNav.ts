/**
 * Vertical navigation for grids that restart their rows at every time-gap
 * group header (the Library grid and the Cull contact sheet).
 *
 * ↑/↓ cannot be a flat ±cols step on the photo list: once a group header
 * intervenes, the frame visually above index i is no longer i - cols. Resolve
 * the move inside the focused frame's own group, and step onto the facing edge
 * row of the neighbouring group when it runs off the top or bottom.
 */

/** Must match the `grid-cols-8` section layout in ContactSheet.tsx. */
export const CONTACT_SHEET_COLS = 8;

const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));

/**
 * rowNeighbor returns the index one row above (dir -1) or below (dir 1) of
 * `index`, honouring the group boundaries in `groupStarts` (ascending, first
 * entry 0 — as returned by gapGroupStarts).
 *
 * Rows are ragged: the target column is clamped to the last frame of the row
 * it lands on. At the very top or bottom of the list the index clamps to the
 * first/last frame, matching the flat behaviour of ←/→.
 */
export function rowNeighbor(
  index: number,
  total: number,
  cols: number,
  groupStarts: readonly number[],
  dir: -1 | 1,
): number {
  if (total <= 0) return 0;
  if (cols <= 1) return clamp(index + dir, 0, total - 1);

  // The group owning `index`: the last start at or before it.
  let g = 0;
  while (g + 1 < groupStarts.length && groupStarts[g + 1] <= index) g++;
  const from = groupStarts[g] ?? 0;
  const to = g + 1 < groupStarts.length ? groupStarts[g + 1] : total;

  const col = (index - from) % cols;
  const targetRow = Math.floor((index - from) / cols) + dir;
  const rowsInGroup = Math.ceil((to - from) / cols);

  if (targetRow >= 0 && targetRow < rowsInGroup) {
    return Math.min(from + targetRow * cols + col, to - 1);
  }

  if (dir === 1) {
    if (to >= total) return total - 1; // last group: clamp to the last frame
    // First row of the next group.
    const nextTo = g + 2 < groupStarts.length ? groupStarts[g + 2] : total;
    return Math.min(to + col, nextTo - 1);
  }

  if (from === 0) return 0; // first group: clamp to the first frame
  // Last row of the previous group.
  const prevFrom = groupStarts[g - 1];
  const prevRows = Math.ceil((from - prevFrom) / cols);
  return Math.min(prevFrom + (prevRows - 1) * cols + col, from - 1);
}
