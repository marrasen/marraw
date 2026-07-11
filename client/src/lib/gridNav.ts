/**
 * Vertical navigation for grids, driven by an explicit row model rather than a
 * fixed column count. `rowStarts` is the ascending list of flat photo indices
 * where each visual row begins (first entry 0). This subsumes the two shapes
 * the grids actually draw:
 *
 *  - uniform grids that restart their rows at every time-gap group header (the
 *    Library grid and the Cull contact sheet in crop/fit) — build rowStarts
 *    with `uniformRowStarts`;
 *  - justified rows where each row holds a variable number of frames (natural
 *    thumbFit) — the layout already emits rowStarts directly.
 *
 * ↑/↓ cannot be a flat ±cols step on the photo list: once a group header (or a
 * short justified row) intervenes, the frame visually above index i is no
 * longer i - cols. Resolve the move against the row the frame actually sits in.
 */

const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));

/**
 * uniformRowStarts builds the row model for a uniform `cols`-wide grid whose
 * rows restart at each entry of `groupStarts` (ascending, first entry 0 — as
 * returned by gapGroupStarts). Every row is `cols` frames wide except each
 * group's ragged last row.
 */
export function uniformRowStarts(
  total: number,
  cols: number,
  groupStarts: readonly number[],
): number[] {
  if (total <= 0) return [];
  const c = Math.max(1, cols);
  const bounds = groupStarts.length > 0 && groupStarts[0] === 0 ? groupStarts : [0];
  const starts: number[] = [];
  for (let g = 0; g < bounds.length; g++) {
    const from = bounds[g];
    const to = g + 1 < bounds.length ? bounds[g + 1] : total;
    for (let i = from; i < to; i += c) starts.push(i);
  }
  return starts;
}

/**
 * rowNeighbor returns the index one row above (dir -1) or below (dir 1) of
 * `index`, honouring the visual rows in `rowStarts`.
 *
 * Column matching:
 *  - ordinal (default): keep the same position within the row, clamped to the
 *    target row's last frame (ragged rows).
 *  - pixel (`centersX` given): land on the frame whose normalized x-center is
 *    nearest the current frame's — justified rows have variable widths, so an
 *    ordinal column would jump to a visually unrelated frame.
 *
 * At the very top or bottom of the list the index clamps to the first/last
 * frame, matching the flat behaviour of ←/→. An empty `rowStarts` means a 1D
 * surface (loupe/filmstrip): fall back to a flat ±1 step.
 */
export function rowNeighbor(
  index: number,
  total: number,
  rowStarts: readonly number[],
  dir: -1 | 1,
  centersX?: readonly number[],
): number {
  if (total <= 0) return 0;
  if (rowStarts.length === 0) return clamp(index + dir, 0, total - 1);

  // The row owning `index`: the last start at or before it.
  let r = 0;
  while (r + 1 < rowStarts.length && rowStarts[r + 1] <= index) r++;
  const target = r + dir;

  // Off the top or bottom of the whole list: clamp to the first/last frame.
  if (target < 0) return 0;
  if (target >= rowStarts.length) return total - 1;

  const tStart = rowStarts[target];
  const tEnd = target + 1 < rowStarts.length ? rowStarts[target + 1] : total;

  let landed: number;
  if (centersX && centersX.length >= total) {
    const want = centersX[index];
    landed = tStart;
    let best = Infinity;
    for (let i = tStart; i < tEnd; i++) {
      const d = Math.abs(centersX[i] - want);
      if (d < best) {
        best = d;
        landed = i;
      }
    }
  } else {
    const col = index - rowStarts[r];
    landed = Math.min(tStart + col, tEnd - 1);
  }

  // Insurance against a transiently stale row model (a mode switch or filter
  // change can leave rowStarts one render ahead of `total`).
  return clamp(landed, 0, total - 1);
}
