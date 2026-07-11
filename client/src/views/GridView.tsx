import { useEffect, useMemo, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Check, Clock } from 'lucide-react';
import { setVisible, type Photo } from '@/api/library';
import { useApiClient } from '@/api/client';
import { cn } from '@/lib/utils';
import { imgUrl } from '@/lib/backend';
import { dayLabel, gapLabel, groupByGap, rangeLabel, type TimeGroup } from '@/lib/timeGaps';
import { PyramidImage } from '@/components/PyramidImage';
import { rowLayout } from '@/lib/justify';
import { useUIStore } from '@/stores/uiStore';

const CELL_GAP = 12;
const HEADER_H = 40;

// The grid interleaves two row kinds when group-by-gap is on: a header row
// per time-gap group, then that group's photo rows. Photo rows carry absolute
// indices into the flat `photos` list (groups preserve flat order, so each
// group's slice is contiguous) and their own pixel height (uniform for
// crop/fit, per-row for the justified natural layout).
type PhotosRow = { kind: 'photos'; start: number; count: number; height: number };
type GridRow = { kind: 'header'; group: TimeGroup; multiDay: boolean } | PhotosRow;

export function GridView({ photos, folderId }: { photos: Photo[]; folderId: number }) {
  const client = useApiClient();
  // Element state (not a ref) so measurement re-attaches whenever the
  // scroll container mounts — a plain ref + [] effect misses it when the
  // first render shows the empty state.
  const [scrollEl, setScrollEl] = useState<HTMLDivElement | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  scrollRef.current = scrollEl;
  const [width, setWidth] = useState(0);

  useEffect(() => {
    if (!scrollEl) return;
    const ro = new ResizeObserver(() => setWidth(scrollEl.clientWidth));
    ro.observe(scrollEl);
    setWidth(scrollEl.clientWidth);
    return () => ro.disconnect();
  }, [scrollEl]);

  const thumbFit = useUIStore((s) => s.thumbFit);
  const cellTarget = useUIStore((s) => s.cellSize);
  const cols = Math.max(1, Math.floor(width / cellTarget));
  const cellW = width > 0 ? Math.floor((width - CELL_GAP * (cols + 1)) / cols) : cellTarget;
  // crop keeps the 3:2 handoff cell; fit makes it square so the whole frame
  // fits with symmetric letterbox. natural sizes each frame to its own aspect.
  const cellH = thumbFit === 'fit' ? cellW : Math.floor((cellW * 2) / 3);
  // Row height the justified layout aims for; the cellSize slider keeps
  // driving density, and *2/3 matches the crop cell's vertical rhythm.
  const naturalTarget = Math.max(48, Math.floor((cellTarget * 2) / 3));
  const fitClass = thumbFit === 'fit' ? 'object-contain' : 'object-cover';

  const gapMinutes = useUIStore((s) => s.gapMinutes);
  const groups = useMemo(() => groupByGap(photos, gapMinutes), [photos, gapMinutes]);
  const grouped = gapMinutes != null && photos.length > 0;

  // photoRow maps flat photo index -> row index (scroll-to-focus). rowStarts is
  // the nav row model (flat index each photos-row begins at); widths/centersX
  // are per-photo, natural only.
  const { rows, photoRow, rowStarts, widths, centersX } = useMemo(() => {
    const rows: GridRow[] = [];
    const photoRow = new Array<number>(photos.length);
    const rowStarts: number[] = [];
    const natural = thumbFit === 'natural';
    const widths = natural ? new Array<number>(photos.length) : null;
    const centersX = natural ? new Array<number>(photos.length) : null;
    // Day prefixes only when the timed groups span more than one calendar day.
    const days = new Set<string>();
    for (const g of groups) {
      if (g.start > 0) days.add(new Date(g.start * 1000).toDateString());
      if (g.end > 0) days.add(new Date(g.end * 1000).toDateString());
    }
    const multiDay = days.size > 1;
    const contentW = Math.max(1, width - CELL_GAP * 2);
    let base = 0;
    for (const g of groups) {
      if (grouped) rows.push({ kind: 'header', group: g, multiDay });
      if (natural) {
        const gl = rowLayout(g.photos, { width: contentW, gap: CELL_GAP, targetHeight: naturalTarget });
        for (const r of gl.rows) {
          for (let j = 0; j < r.count; j++) photoRow[base + r.start + j] = rows.length;
          rowStarts.push(base + r.start);
          rows.push({ kind: 'photos', start: base + r.start, count: r.count, height: r.height });
        }
        for (let k = 0; k < g.photos.length; k++) {
          widths![base + k] = gl.widths[k];
          centersX![base + k] = gl.centersX[k];
        }
      } else {
        for (let i = 0; i < g.photos.length; i += cols) {
          const count = Math.min(cols, g.photos.length - i);
          for (let j = 0; j < count; j++) photoRow[base + i + j] = rows.length;
          rowStarts.push(base + i);
          rows.push({ kind: 'photos', start: base + i, count, height: cellH });
        }
      }
      base += g.photos.length;
    }
    return { rows, photoRow, rowStarts, widths, centersX };
  }, [groups, cols, grouped, photos.length, thumbFit, width, cellH, naturalTarget]);

  // Publish the row model for keyboard nav; clear it on unmount so a stale
  // grid geometry never leaks into the loupe/filmstrip (which nav-fall back
  // to a flat ±1 step on an empty model).
  const setNavRowModel = useUIStore((s) => s.setNavRowModel);
  useEffect(() => {
    setNavRowModel(rowStarts, centersX);
  }, [rowStarts, centersX, setNavRowModel]);
  useEffect(() => () => setNavRowModel([], null), [setNavRowModel]);

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: (i) => {
      const r = rows[i];
      return r.kind === 'header' ? HEADER_H : r.height + CELL_GAP;
    },
    overscan: 3,
  });

  // Mixed row heights: the virtualizer caches sizes per index, and a given
  // index can flip between header and photos (or a natural row can change
  // height) when the model shifts — flush the cache whenever rows change.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => virtualizer.measure(), [rows]);

  const items = virtualizer.getVirtualItems();

  // Prefetch hint: tell the backend which photos are (nearly) on screen.
  const visibleKey = items.length ? `${items[0].index}-${items[items.length - 1].index}` : '';
  useEffect(() => {
    if (!items.length || photos.length === 0) return;
    const photoItems = items.filter((it) => rows[it.index]?.kind === 'photos');
    if (!photoItems.length) return;
    const t = setTimeout(() => {
      const first = rows[photoItems[0].index] as PhotosRow;
      const last = rows[photoItems[photoItems.length - 1].index] as PhotosRow;
      const from = Math.max(0, first.start - 2 * cols);
      const to = Math.min(photos.length, last.start + last.count + 3 * cols);
      setVisible(client, folderId, photos.slice(from, to).map((p) => p.id)).catch(() => {});
    }, 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visibleKey, folderId, photos.length, cols, client]);

  // Keep the focused cell scrolled into view when navigating by keyboard.
  const focusId = useUIStore((s) => s.focusId);
  const focusRow = useMemo(() => {
    const idx = photos.findIndex((p) => p.id === focusId);
    return idx < 0 ? null : (photoRow[idx] ?? null);
  }, [photos, focusId, photoRow]);
  useEffect(() => {
    // align 'auto' only scrolls when the focused row is off-screen, so a
    // background metadata snapshot reflowing the natural layout re-anchors on
    // the focus without yanking the view while it stays visible.
    if (focusRow != null) virtualizer.scrollToIndex(focusRow, { align: 'auto' });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusRow, rows]);

  return (
    <div className="relative min-h-0 flex-1">
      <div ref={setScrollEl} className="size-full overflow-y-auto" tabIndex={-1}>
        {photos.length === 0 && (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            No photos match the current filter.
          </div>
        )}
        <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
          {items.map((row) => {
            const r = rows[row.index];
            if (!r) return null;
            if (r.kind === 'header') {
              return <GroupHeaderRow key={row.key} group={r.group} multiDay={r.multiDay} top={row.start} />;
            }
            return (
              <div
                key={row.key}
                className="absolute left-0 flex w-full"
                style={{ top: row.start, height: r.height, gap: CELL_GAP, paddingLeft: CELL_GAP, paddingRight: CELL_GAP }}
              >
                {photos.slice(r.start, r.start + r.count).map((p, j) => (
                  <GridCell
                    key={p.id}
                    photo={p}
                    w={widths ? widths[r.start + j] : cellW}
                    h={r.height}
                    fitClass={fitClass}
                  />
                ))}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

// GroupHeaderRow: one time-gap group header — the ContactSheet section header
// scaled down to the library toolbar type ramp.
function GroupHeaderRow({ group, multiDay, top }: { group: TimeGroup; multiDay: boolean; top: number }) {
  const n = group.photos.length;
  return (
    <div
      data-testid="grid-group-header"
      className="absolute left-0 flex w-full items-end"
      style={{ top, height: HEADER_H, paddingLeft: CELL_GAP, paddingRight: CELL_GAP }}
    >
      <div className="mb-[6px] flex w-full items-center gap-3 border-b pb-[7px]">
        <span className="flex items-center gap-2 font-mono text-[12.5px] text-foreground">
          <Clock className="size-3 text-muted-foreground" strokeWidth={1.5} />
          {multiDay && group.start > 0 && (
            <span className="text-muted-foreground">{dayLabel(group.start)} ·</span>
          )}
          {rangeLabel(group)}
        </span>
        <span className="text-[11.5px] text-muted-foreground">
          {n} frame{n === 1 ? '' : 's'}
        </span>
        <div className="flex-1" />
        {group.gapBeforeMin != null && group.gapBeforeMin > 0 && (
          <span className="rounded-md border border-primary/30 bg-primary/15 px-2 py-[2px] font-mono text-[10.5px] text-[#aab0ff]">
            {gapLabel(group.gapBeforeMin)} before
          </span>
        )}
      </div>
    </div>
  );
}

function GridCell({ photo, w, h, fitClass }: { photo: Photo; w: number; h: number; fitClass: string }) {
  const focusId = useUIStore((s) => s.focusId);
  const selected = useUIStore((s) => s.selection.has(photo.id));
  const multiSelect = useUIStore((s) => s.selection.size > 1);
  const focus = useUIStore((s) => s.focus);
  const [loaded, setLoaded] = useState(false);
  const level = w * window.devicePixelRatio > 256 ? '512' : '256';
  const isFocus = focusId === photo.id;

  return (
    <div
      className={cn(
        'group relative cursor-pointer overflow-hidden rounded bg-inset',
        photo.flag === 'exclude' && 'opacity-40',
        // Batch spec: selected cells tint + accent border; the range anchor
        // (focus) gets the white ring.
        multiSelect && selected && 'bg-primary/15',
      )}
      style={{ width: w, height: h }}
      onClick={(e) => focus(photo.id, { extend: e.shiftKey, toggle: e.ctrlKey || e.metaKey })}
      onDoubleClick={() => {
        focus(photo.id);
        // Double-click drops the frame into the Cull confirm loupe.
        useUIStore.getState().setMode('cull');
      }}
      title={photo.fileName}
    >
      {!loaded && <div className="skeleton-shimmer absolute inset-0" />}
      <PyramidImage
        src={imgUrl(photo, level)}
        alt={photo.fileName}
        loading="lazy"
        onLoad={() => setLoaded(true)}
        className={cn('size-full', fitClass, !loaded && 'opacity-0')}
      />
      {photo.rating > 0 && (
        <div className="absolute bottom-[5px] left-[5px] rounded bg-black/50 px-[5px] py-0.5 text-[9px] tracking-[.5px] text-rating">
          {'★'.repeat(photo.rating)}
        </div>
      )}
      {photo.flag !== 'none' && (
        <div
          className={cn(
            'absolute top-1.5 right-1.5 size-[9px] rounded-[2px]',
            photo.flag === 'pick' ? 'bg-success' : 'bg-destructive',
          )}
          aria-label={photo.flag === 'pick' ? 'Pick' : 'Excluded'}
        />
      )}
      {multiSelect && selected && (
        <>
          <div className="pointer-events-none absolute inset-0 rounded border-2 border-primary" />
          <div className="absolute top-1.5 left-1.5 flex size-4 items-center justify-center rounded-[5px] bg-primary">
            <Check className="size-[11px] text-primary-foreground" strokeWidth={2.5} />
          </div>
        </>
      )}
      {isFocus && (
        <div
          className={cn(
            'pointer-events-none absolute inset-0 rounded border-2',
            multiSelect && selected ? 'border-white' : 'border-primary',
          )}
        />
      )}
      {!isFocus && !multiSelect && selected && (
        <div className="pointer-events-none absolute inset-0 rounded border-2 border-primary/60" />
      )}
    </div>
  );
}
