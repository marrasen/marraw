import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Clock } from 'lucide-react';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { imgUrl } from '@/lib/backend';
import { useImgBust } from '@/lib/imgCacheBust';
import { rowLayout } from '@/lib/justify';
import { burstFor, burstMap, type BurstInfo } from '@/lib/bursts';
import { uniformRowStarts } from '@/lib/gridNav';
import { gapLabel, rangeLabel, timeLabel, type TimeGroup } from '@/lib/timeGaps';
import { GapControl } from '@/components/cinema/GapControl';
import { PyramidImage } from '@/components/PyramidImage';
import { WindowControls } from '@/components/WindowControls';
import { useUIStore } from '@/stores/uiStore';
import '@/lib/electron';
import { rootName, samePath, useLibraryRoots } from '@/lib/library';

// The sheet's outer padding (p-5) and inter-cell gap (gap-2), needed to size
// the justified natural layout to the exact content width.
const SHEET_PAD = 20;
const SHEET_GAP = 8;
// crop/fit keep a fixed 8-column grid; natural falls out of the justified rows.
const SHEET_COLS = 8;

/**
 * The Cull contact sheet (G): the scrubber blown up into a full multi-row
 * grid, one section per time-gap group. Esc collapses back to the loupe.
 */
export function ContactSheet({ photos, groups }: { photos: Photo[]; groups: TimeGroup[] }) {
  const focusId = useUIStore((s) => s.focusId);
  const focus = useUIStore((s) => s.focus);
  const setContactSheet = useUIStore((s) => s.setContactSheet);
  const folderPath = useUIStore((s) => s.folderPath);
  const thumbFit = useUIStore((s) => s.thumbFit);
  // Newest-first: the dead time at a section boundary sits chronologically
  // after that section's frames (see GridView's GroupHeaderRow).
  const gapSide = useUIStore((s) => (s.librarySort === 'captureDesc' ? 'after' : 'before'));
  const { roots } = useLibraryRoots();
  const current = folderPath ? roots.find((r) => samePath(r.path, folderPath)) : undefined;
  const picked = photos.filter((p) => p.flag === 'pick').length;
  const bursts = useMemo(() => burstMap(photos), [photos]);
  const scrollRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);

  // Only the justified natural layout needs the measured width (crop/fit is a
  // pure CSS grid), so don't pay a full re-render of the unvirtualized sheet
  // per resize frame in the other modes. Layout effect: the synchronous first
  // measurement re-renders before paint, so natural never paints (or publishes
  // a nav model for) the degenerate one-frame-per-row shape it has at width 0.
  useLayoutEffect(() => {
    if (thumbFit !== 'natural') return;
    const el = scrollRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(el.clientWidth));
    ro.observe(el);
    setWidth(el.clientWidth);
    return () => ro.disconnect();
  }, [thumbFit]);

  const contentW = Math.max(1, width - SHEET_PAD * 2);
  // Natural row height tracks the crop cell so the frames don't jump size
  // between modes; clamped so a very wide/narrow window stays sensible.
  const naturalTarget = Math.min(160, Math.max(96, Math.floor((contentW / SHEET_COLS) * 2 / 3)));

  // Row model for keyboard nav, plus the justified rows for natural rendering.
  const { groupRows, rowStarts, centersX } = useMemo(() => {
    const groupStarts: number[] = [];
    let base = 0;
    for (const g of groups) {
      groupStarts.push(base);
      base += g.photos.length;
    }
    if (thumbFit !== 'natural') {
      // crop/fit: a uniform SHEET_COLS-wide grid, rows restarting per group.
      return {
        groupRows: [] as { start: number; count: number; height: number; widths: number[] }[][],
        rowStarts: uniformRowStarts(base, SHEET_COLS, groupStarts),
        centersX: [] as number[],
      };
    }
    const rowStarts: number[] = [];
    const centersX: number[] = [];
    const groupRows: { start: number; count: number; height: number; widths: number[] }[][] = [];
    for (let i = 0; i < groups.length; i++) {
      const g = groups[i];
      const b = groupStarts[i];
      const rowsForGroup: { start: number; count: number; height: number; widths: number[] }[] = [];
      const gl = rowLayout(g.photos, { width: contentW, gap: SHEET_GAP, targetHeight: naturalTarget });
      for (const r of gl.rows) {
        rowStarts.push(b + r.start);
        rowsForGroup.push({
          start: b + r.start,
          count: r.count,
          height: r.height,
          widths: gl.widths.slice(r.start, r.start + r.count),
        });
      }
      for (let k = 0; k < g.photos.length; k++) centersX[b + k] = gl.centersX[k];
      groupRows.push(rowsForGroup);
    }
    return { groupRows, rowStarts, centersX };
  }, [groups, thumbFit, contentW, naturalTarget]);

  const setNavRowModel = useUIStore((s) => s.setNavRowModel);
  useEffect(() => {
    setNavRowModel(rowStarts, thumbFit === 'natural' ? centersX : null);
  }, [rowStarts, centersX, thumbFit, setNavRowModel]);
  useEffect(() => () => setNavRowModel([], null), [setNavRowModel]);

  // Whether the focused cell is on-screen, tracked on scroll and after every
  // programmatic anchor, so a layout reflow can decide from the pre-reflow
  // state whether re-anchoring is a courtesy or a yank.
  const focusVisible = useRef(false);
  const updateFocusVisible = useCallback(() => {
    const box = scrollRef.current;
    const el = box?.querySelector<HTMLElement>(`[data-sheet-id="${focusId}"]`);
    if (!box || !el) {
      focusVisible.current = false;
      return;
    }
    const b = box.getBoundingClientRect();
    const r = el.getBoundingClientRect();
    focusVisible.current = r.bottom > b.top && r.top < b.bottom;
  }, [focusId]);

  // A row-model change (metadata streaming in re-justifies the natural rows)
  // re-anchors on the focus only when it was on-screen before the reflow —
  // never yanking back a view the user has scrolled away from. Declared before
  // the tracker effects below so it reads the pre-reflow value.
  useEffect(() => {
    if (focusId != null && focusVisible.current) {
      scrollRef.current
        ?.querySelector<HTMLElement>(`[data-sheet-id="${focusId}"]`)
        ?.scrollIntoView({ block: 'nearest' });
    }
    updateFocusVisible();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rowStarts, groupRows]);

  useEffect(() => {
    if (focusId == null) return;
    const el = scrollRef.current?.querySelector<HTMLElement>(`[data-sheet-id="${focusId}"]`);
    el?.scrollIntoView({ block: 'nearest' });
    updateFocusVisible();
  }, [focusId, updateFocusVisible]);

  useEffect(() => {
    const box = scrollRef.current;
    if (!box) return;
    updateFocusVisible();
    box.addEventListener('scroll', updateFocusVisible, { passive: true });
    return () => box.removeEventListener('scroll', updateFocusVisible);
  }, [updateFocusVisible]);

  const cellFit = thumbFit === 'fit' ? 'object-contain' : 'object-cover';
  const aspectClass = thumbFit === 'fit' ? 'aspect-square' : 'aspect-[3/2]';

  const spanned = groups.filter((g) => g.start > 0);
  const rangeSummary =
    spanned.length > 0
      ? `${timeLabel(spanned[0].start)} – ${timeLabel(spanned[spanned.length - 1].end)}`
      : '';

  return (
    <div className="flex min-h-0 flex-1 flex-col bg-background">
      <div className="flex h-12 shrink-0 items-center gap-3.5 border-b bg-sidebar py-0 pr-2 pl-4 [-webkit-app-region:drag]">
        <div className="flex size-6 items-center justify-center rounded-[7px] bg-primary text-sm font-bold text-primary-foreground">
          m
        </div>
        <span className="text-[13px] font-semibold">
          {current ? rootName(current) : (folderPath ?? '')}
        </span>
        <span className="text-[12.5px] text-muted-foreground">Contact sheet</span>
        <div className="flex-1" />
        <div className="[-webkit-app-region:no-drag]">
          <GapControl />
        </div>
        <span className="font-mono text-[11px] text-muted-foreground">Esc to loupe</span>
        {window.win && <div className="h-[22px] w-px bg-black/10 dark:bg-white/9" />}
        <WindowControls />
      </div>
      <div ref={scrollRef} className="flex flex-1 flex-col gap-[22px] overflow-y-auto p-5">
        {groups.map((g, i) => (
          <div key={i} className="flex flex-col gap-[11px]">
            <div className="flex items-center gap-3 border-b pb-[9px]">
              <span className="flex items-center gap-2 font-mono text-sm text-foreground">
                <Clock className="size-3.5 text-muted-foreground" strokeWidth={1.5} />
                {rangeLabel(g)}
              </span>
              <span className="text-[12.5px] text-muted-foreground">
                {g.photos.length} frame{g.photos.length === 1 ? '' : 's'}
              </span>
              <div className="flex-1" />
              {g.gapBeforeMin != null && g.gapBeforeMin > 0 && (
                <span className="rounded-md border border-primary/30 bg-primary/15 px-2 py-[3px] font-mono text-[11px] text-[#aab0ff]">
                  {gapLabel(g.gapBeforeMin)} {gapSide}
                </span>
              )}
            </div>
            {thumbFit === 'natural' ? (
              <div className="flex flex-col gap-2">
                {groupRows[i].map((r, ri) => (
                  <div key={ri} className="flex gap-2" style={{ height: r.height }}>
                    {photos.slice(r.start, r.start + r.count).map((p, j) => (
                      <SheetCell
                        key={p.id}
                        photo={p}
                        focused={p.id === focusId}
                        onFocus={focus}
                        onOpen={() => setContactSheet(false)}
                        boxStyle={{ width: r.widths[j], height: r.height }}
                        fitClass="object-cover"
                        burst={burstFor(p, bursts)}
                      />
                    ))}
                  </div>
                ))}
              </div>
            ) : (
              // crop/fit: a fixed SHEET_COLS-wide grid; the nav row model above
              // is built to match, so ↑/↓ land where the frames are drawn.
              <div className="grid grid-cols-8 gap-2">
                {g.photos.map((p) => (
                  <SheetCell
                    key={p.id}
                    photo={p}
                    focused={p.id === focusId}
                    onFocus={focus}
                    onOpen={() => setContactSheet(false)}
                    boxClassName={aspectClass}
                    fitClass={cellFit}
                    burst={burstFor(p, bursts)}
                  />
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
      <div className="flex h-[26px] shrink-0 items-center gap-4 border-t bg-sidebar px-4 font-mono text-[11px] text-muted-foreground">
        <span>
          {groups.length} group{groups.length === 1 ? '' : 's'}
          {rangeSummary && ` · ${rangeSummary}`}
        </span>
        <span>{photos.length.toLocaleString()} photos</span>
        {picked > 0 && <span className="text-success-text">{picked} picked</span>}
        <span className="flex-1" />
        <span>G contact sheet · 1–5 rate · P/X flag</span>
      </div>
    </div>
  );
}

function SheetCell({
  photo,
  focused,
  onFocus,
  onOpen,
  boxClassName,
  boxStyle,
  fitClass,
  burst,
}: {
  photo: Photo;
  focused: boolean;
  onFocus: (id: number, opts?: { extend?: boolean; toggle?: boolean }) => void;
  onOpen: () => void;
  // Grid modes size the box with an aspect class; natural sizes it explicitly.
  boxClassName?: string;
  boxStyle?: React.CSSProperties;
  fitClass: string;
  burst?: BurstInfo;
}) {
  useImgBust(photo.id); // refetch when a restored AI map repaints this thumb
  return (
    <div
      data-sheet-id={photo.id}
      className="relative cursor-pointer"
      onClick={(e) => onFocus(photo.id, { extend: e.shiftKey, toggle: e.ctrlKey || e.metaKey })}
      onDoubleClick={() => {
        onFocus(photo.id);
        onOpen();
      }}
      title={photo.fileName}
    >
      <div
        className={cn(
          'overflow-hidden rounded bg-inset',
          boxClassName,
          photo.flag === 'exclude' && 'opacity-40',
        )}
        style={boxStyle}
      >
        <PyramidImage src={imgUrl(photo, '256')} loading="lazy" className={cn('size-full', fitClass)} />
      </div>
      {focused && (
        <div className="pointer-events-none absolute -inset-0.5 rounded border-2 border-primary" />
      )}
      {photo.rating > 0 && (
        <div className="absolute bottom-[3px] left-[3px] flex items-center gap-0.5 rounded-[3px] bg-black/55 px-1 py-px text-[9px]">
          <span className="text-rating">★</span>
          <span className="text-white">{photo.rating}</span>
        </div>
      )}
      {photo.flag !== 'none' && (
        <div
          className={cn(
            'absolute top-[3px] right-[3px] size-2 rounded-[2px]',
            photo.flag === 'pick' ? 'bg-success' : 'bg-destructive',
          )}
        />
      )}
      {burst && (
        <div
          className={cn(
            'absolute top-[3px] left-[3px] rounded-[3px] bg-black/55 px-1 py-px font-mono text-[9px]',
            burst.bestId === photo.id ? 'text-success-text' : 'text-zinc-300',
          )}
          title={
            burst.bestId === photo.id
              ? `Burst of ${burst.count} — sharpest frame`
              : `Burst of ${burst.count} near-duplicates`
          }
          data-testid="burst-badge"
          data-best={burst.bestId === photo.id || undefined}
        >
          ⧉ {burst.count}
        </div>
      )}
    </div>
  );
}
