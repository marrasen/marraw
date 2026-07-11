// Verifies lib/gridNav: ↑/↓ navigation over the explicit row model, including
// across time-gap group boundaries and in variable-width (natural) rows.
//
//   node scripts/gridnav-verify.mjs
//
// Node 24 strips TypeScript types natively, so the real module runs as-is.
import { rowNeighbor, uniformRowStarts } from '../client/src/lib/gridNav.ts';

let failed = 0;
const eq = (label, got, want) => {
  const ok = got === want;
  if (!ok) failed++;
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}: got ${got}, want ${want}`);
};

// -- Regression: the uniform grid, rebuilt through uniformRowStarts, must land
// exactly where the old cols+groupStarts contract did. cols=4, groups at 0, 6.
//   group A (0..5):  [0 1 2 3]      group B (6..13): [6 7 8 9]
//                    [4 5]                           [10 11 12 13]
const total = 14;
const starts = uniformRowStarts(total, 4, [0, 6]);
eq('uniformRowStarts shape', JSON.stringify(starts), JSON.stringify([0, 4, 6, 10]));

console.log('-- across a group boundary (the bug: flat math sent 6 -> 2)');
eq('up from 6 (first frame of group B)', rowNeighbor(6, total, starts, -1), 4);
eq('up from 7 -> group A last row, col 1', rowNeighbor(7, total, starts, -1), 5);
eq('up from 8 clamps to ragged row end', rowNeighbor(8, total, starts, -1), 5);
eq('up from 9 clamps to ragged row end', rowNeighbor(9, total, starts, -1), 5);
eq('down from 4 (group A last row)', rowNeighbor(4, total, starts, 1), 6);
eq('down from 5 -> group B first row, col 1', rowNeighbor(5, total, starts, 1), 7);

console.log('-- within a group it is a plain grid');
eq('down from 0', rowNeighbor(0, total, starts, 1), 4);
eq('down from 6', rowNeighbor(6, total, starts, 1), 10);
eq('up from 10', rowNeighbor(10, total, starts, -1), 6);
eq('down from 1 into the short row', rowNeighbor(1, total, starts, 1), 5);
eq('down from 2 clamps to frame 5', rowNeighbor(2, total, starts, 1), 5);

console.log('-- list edges clamp, as flat ←/→ navigation does');
eq('up from 0', rowNeighbor(0, total, starts, -1), 0);
eq('up from 3', rowNeighbor(3, total, starts, -1), 0);
eq('down from 13 (last frame)', rowNeighbor(13, total, starts, 1), 13);
eq('down from 10 (last row)', rowNeighbor(10, total, starts, 1), 13);

console.log('-- grouping off (gapMinutes null) == a plain uniform grid');
const flat = uniformRowStarts(total, 4, [0]);
eq('flat shape', JSON.stringify(flat), JSON.stringify([0, 4, 8, 12]));
for (const i of [0, 3, 4, 7, 9, 13]) {
  eq(`flat down from ${i}`, rowNeighbor(i, total, flat, 1), Math.min(i + 4, 13));
  eq(`flat up from ${i}`, rowNeighbor(i, total, flat, -1), Math.max(i - 4, 0));
}

// -- Ragged rows fed as a direct row model (the justified natural layout).
//   [0 1 2] [3 4 5 6] [7 8 9] [10 11 12]
console.log('-- ragged rows (variable frames per row), ordinal columns');
const ragged = [0, 3, 7, 10];
const rt = 13;
eq('down from 0', rowNeighbor(0, rt, ragged, 1), 3);
eq('down from 2', rowNeighbor(2, rt, ragged, 1), 5);
eq('down from 6 clamps to the shorter row', rowNeighbor(6, rt, ragged, 1), 9);
eq('down from 3', rowNeighbor(3, rt, ragged, 1), 7);
eq('up from 9', rowNeighbor(9, rt, ragged, -1), 5);
eq('up from 7', rowNeighbor(7, rt, ragged, -1), 3);
eq('up from 1 clamps to first frame', rowNeighbor(1, rt, ragged, -1), 0);
eq('down from 11 clamps to last frame', rowNeighbor(11, rt, ragged, 1), 12);

// -- Pixel columns: variable-width rows match on nearest x-center, not ordinal.
//   row0 centers .1 .5 .9   row1 centers .2 .55 .85
console.log('-- pixel columns (natural: nearest x-center)');
const pxStarts = [0, 3];
const pxTotal = 6;
const cx = [0.1, 0.5, 0.9, 0.2, 0.55, 0.85];
eq('down from 0 (.10) -> nearest .20', rowNeighbor(0, pxTotal, pxStarts, 1, cx), 3);
eq('down from 2 (.90) -> nearest .85', rowNeighbor(2, pxTotal, pxStarts, 1, cx), 5);
eq('up from 4 (.55) -> nearest .50', rowNeighbor(4, pxTotal, pxStarts, -1, cx), 1);

console.log('-- 1D surfaces (empty model) fall back to a flat ±1 step');
eq('down from 5', rowNeighbor(5, total, [], 1), 6);
eq('up from 5', rowNeighbor(5, total, [], -1), 4);
eq('down clamps at the end', rowNeighbor(13, total, [], 1), 13);

console.log('-- degenerate / defensive');
eq('empty list', rowNeighbor(0, 0, [0], 1), 0);
eq('single row down clamps', rowNeighbor(2, 6, [0], 1), 5);
eq('single row up clamps', rowNeighbor(2, 6, [0], -1), 0);
// A stale row model can reference indices past `total`; never return OOB.
eq('stale rowStarts overrun', rowNeighbor(0, 4, [0, 5], 1), 3);

console.log(failed ? `\n${failed} FAILED` : '\nall passed');
process.exit(failed ? 1 : 0);
