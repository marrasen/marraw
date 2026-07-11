import { useEffect, useMemo, useRef, useState } from 'react';
import { Clock } from 'lucide-react';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { imgUrl } from '@/lib/backend';
import { rowLayout } from '@/lib/justify';
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
  const { roots } = useLibraryRoots();
  const current = folderPath ? roots.find((r) => samePath(r.path, folderPath)) : undefined;
  const picked = photos.filter((p) => p.flag === 'pick').length;
  const scrollRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(el.clientWidth));
    ro.observe(el);
    setWidth(el.clientWidth);
    return () => ro.disconnect();
  }, []);

  const contentW = Math.max(1, width - SHEET_PAD * 2);
  // Natural row height tracks the crop cell so the frames don't jump size
  // between modes; clamped so a very wide/narrow window stays sensible.
  const naturalTarget = Math.min(160, Math.max(96, Math.floor((contentW / SHEET_COLS) * 2 / 3)));

  // Row model for keyboard nav, plus the justified rows for natural rendering.
  const { groupRows, rowStarts, centersX } = useMemo(() => {
    const rowStarts: number[] = [];
    const centersX: number[] = [];
    const groupRows: { start: number; count: number; height: number; widths: number[] }[][] = [];
    const natural = thumbFit === 'natural';
    let base = 0;
    for (const g of groups) {
      const rowsForGroup: { start: number; count: number; height: number; widths: number[] }[] = [];
      if (natural) {
        const gl = rowLayout(g.photos, { width: contentW, gap: SHEET_GAP, targetHeight: naturalTarget });
        for (const r of gl.rows) {
          rowStarts.push(base + r.start);
          rowsForGroup.push({
            start: base + r.start,
            count: r.count,
            height: r.height,
            widths: gl.widths.slice(r.start, r.start + r.count),
          });
        }
        for (let k = 0; k < g.photos.length; k++) centersX[base + k] = gl.centersX[k];
      } else {
        for (let i = 0; i < g.photos.length; i += SHEET_COLS) rowStarts.push(base + i);
      }
      groupRows.push(rowsForGroup);
      base += g.photos.length;
    }
    return { groupRows, rowStarts, centersX };
  }, [groups, thumbFit, contentW, naturalTarget]);

  const setNavRowModel = useUIStore((s) => s.setNavRowModel);
  useEffect(() => {
    setNavRowModel(rowStarts, thumbFit === 'natural' ? centersX : null);
  }, [rowStarts, centersX, thumbFit, setNavRowModel]);
  useEffect(() => () => setNavRowModel([], null), [setNavRowModel]);

  useEffect(() => {
    if (focusId == null) return;
    const el = scrollRef.current?.querySelector<HTMLElement>(`[data-sheet-id="${focusId}"]`);
    el?.scrollIntoView({ block: 'nearest' });
  }, [focusId]);

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
                  {gapLabel(g.gapBeforeMin)} before
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
}: {
  photo: Photo;
  focused: boolean;
  onFocus: (id: number, opts?: { extend?: boolean; toggle?: boolean }) => void;
  onOpen: () => void;
  // Grid modes size the box with an aspect class; natural sizes it explicitly.
  boxClassName?: string;
  boxStyle?: React.CSSProperties;
  fitClass: string;
}) {
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
    </div>
  );
}
