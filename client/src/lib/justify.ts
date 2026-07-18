// Justified-row layout for the `natural` thumbFit: every frame keeps its own
// aspect ratio, a row shares one height, and each full row is stretched to fill
// the container width exactly (Flickr/Lightroom style). Pure and testable — the
// grids feed it their measured width and turn the result into cells.
import type { Photo } from '@/api/library';
import { displayDims, renderedDims } from '@/lib/crop';

export interface JustifiedRow {
  start: number; // flat index of the row's first frame
  count: number;
  height: number; // px
}

export interface RowLayout {
  rows: JustifiedRow[]; // each row's flat start index, count, and height
  widths: number[]; // per-photo pixel width, parallel to `photos`
  centersX: number[]; // per-photo normalized x-center (0..1 of container width)
}

// aspectOf is the on-screen width/height ratio of a frame — the RENDERED
// aspect, with the edit's rotate/crop applied, so a cropped photo's cell
// matches its thumbnail. Falls back to 3:2 (today's crop cell) while
// width/height are still 0 — metadata not yet scanned — so a cold folder
// renders near-uniform and settles as it streams in. The fallback checks the
// FULL dims before applying geometry: renderedDims clamps 0×0 to 1×1, which
// would read as square.
export function aspectOf(photo: Photo): number {
  const [fw, fh] = displayDims(photo);
  if (!(fw > 0 && fh > 0)) return 3 / 2;
  const [w, h] = renderedDims(fw, fh, photo);
  return w / h;
}

export interface LayoutOpts {
  width: number;
  gap: number;
  targetHeight: number;
}

export function rowLayout(photos: Photo[], opts: LayoutOpts): RowLayout {
  const { gap, targetHeight } = opts;
  const W = Math.max(1, opts.width);
  const n = photos.length;
  const widths = new Array<number>(n);
  const centersX = new Array<number>(n);
  const rows: JustifiedRow[] = [];

  let i = 0;
  while (i < n) {
    // Accumulate frames (each at targetHeight) until the row's natural width
    // reaches the container. The frame that crosses the width is INCLUDED, so
    // a full row's natural width is always >= W and stretching only ever
    // shrinks it — a lone frame never blows up to fill the width.
    let sumAr = 0;
    let count = 0;
    let j = i;
    while (j < n) {
      sumAr += aspectOf(photos[j]);
      count++;
      j++;
      if (targetHeight * sumAr + gap * (count - 1) >= W) break; // row is full
    }

    // A full row (natural width >= W) stretches to fill W exactly; a short row
    // that ran into the end of the list stays at targetHeight, left-aligned.
    const gaps = gap * (count - 1);
    const filled = targetHeight * sumAr + gaps >= W;
    const rowH = filled ? (W - gaps) / sumAr : targetHeight;

    let x = 0;
    for (let k = i; k < j; k++) {
      const w = Math.max(1, Math.round(rowH * aspectOf(photos[k])));
      widths[k] = w;
      centersX[k] = (x + w / 2) / W;
      x += w + gap;
    }
    rows.push({ start: i, count, height: Math.round(rowH) });
    i = j;
  }

  return { rows, widths, centersX };
}
