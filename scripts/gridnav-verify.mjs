// Verifies lib/gridNav.rowNeighbor: ↑/↓ across time-gap group boundaries.
//
// The grid and the contact sheet restart their rows at every group header, so
// a row is not a flat ±cols step through the photo list. Run with:
//
//   node scripts/gridnav-verify.mjs
//
// Node 24 strips TypeScript types natively, so the real module runs as-is.
import { rowNeighbor } from '../client/src/lib/gridNav.ts';

let failed = 0;
const eq = (label, got, want) => {
  const ok = got === want;
  if (!ok) failed++;
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}: got ${got}, want ${want}`);
};

// Layout under test. cols=4, groups start at 0 and 6.
//   group A (0..5):  [0 1 2 3]
//                    [4 5]
//   group B (6..13): [6 7 8 9]
//                    [10 11 12 13]
const total = 14;
const cols = 4;
const starts = [0, 6];

console.log('-- across a group boundary (the bug: flat math sent 6 -> 2)');
eq('up from 6 (first frame of group B)', rowNeighbor(6, total, cols, starts, -1), 4);
eq('up from 7 -> group A last row, col 1', rowNeighbor(7, total, cols, starts, -1), 5);
eq('up from 8 clamps to ragged row end', rowNeighbor(8, total, cols, starts, -1), 5);
eq('up from 9 clamps to ragged row end', rowNeighbor(9, total, cols, starts, -1), 5);
eq('down from 4 (group A last row)', rowNeighbor(4, total, cols, starts, 1), 6);
eq('down from 5 -> group B first row, col 1', rowNeighbor(5, total, cols, starts, 1), 7);

console.log('-- within a group it is a plain grid');
eq('down from 0', rowNeighbor(0, total, cols, starts, 1), 4);
eq('down from 6', rowNeighbor(6, total, cols, starts, 1), 10);
eq('up from 10', rowNeighbor(10, total, cols, starts, -1), 6);
eq('down from 1 into the short row', rowNeighbor(1, total, cols, starts, 1), 5);
eq('down from 2 clamps to frame 5', rowNeighbor(2, total, cols, starts, 1), 5);

console.log('-- list edges clamp, as flat ←/→ navigation does');
eq('up from 0', rowNeighbor(0, total, cols, starts, -1), 0);
eq('up from 3', rowNeighbor(3, total, cols, starts, -1), 0);
eq('down from 13 (last frame)', rowNeighbor(13, total, cols, starts, 1), 13);
eq('down from 10 (last row)', rowNeighbor(10, total, cols, starts, 1), 13);

console.log('-- grouping off (gapMinutes null) == the old flat math');
const flat = [0];
for (const i of [0, 3, 4, 7, 9, 13]) {
  eq(`flat down from ${i}`, rowNeighbor(i, total, cols, flat, 1), Math.min(i + 4, 13));
  eq(`flat up from ${i}`, rowNeighbor(i, total, cols, flat, -1), Math.max(i - 4, 0));
}

console.log('-- degenerate inputs');
eq('cols=1 down (loupe)', rowNeighbor(5, total, 1, starts, 1), 6);
eq('cols=1 up (loupe)', rowNeighbor(5, total, 1, starts, -1), 4);
eq('empty list', rowNeighbor(0, 0, cols, [], 1), 0);
eq('no groupStarts', rowNeighbor(6, total, cols, [], -1), 2);
eq('up into a one-frame group', rowNeighbor(6, total, cols, [0, 5, 6], -1), 5);
eq('down out of a one-frame group', rowNeighbor(5, total, cols, [0, 5, 6], 1), 6);

console.log(failed ? `\n${failed} FAILED` : '\nall passed');
process.exit(failed ? 1 : 0);
