import { useEffect, useRef } from 'react';
import { Clock } from 'lucide-react';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { imgUrl } from '@/lib/backend';
import { gapLabel, rangeLabel, timeLabel, type TimeGroup } from '@/lib/timeGaps';
import { GapControl } from '@/components/cinema/GapControl';
import { WindowControls } from '@/components/WindowControls';
import { useUIStore } from '@/stores/uiStore';
import '@/lib/electron';
import { rootName, samePath, useLibraryRoots } from '@/lib/library';

/**
 * The Cull contact sheet (G): the scrubber blown up into a full multi-row
 * grid, one section per time-gap group. Esc collapses back to the loupe.
 */
export function ContactSheet({ photos, groups }: { photos: Photo[]; groups: TimeGroup[] }) {
  const focusId = useUIStore((s) => s.focusId);
  const focus = useUIStore((s) => s.focus);
  const setContactSheet = useUIStore((s) => s.setContactSheet);
  const folderPath = useUIStore((s) => s.folderPath);
  const { roots } = useLibraryRoots();
  const current = folderPath ? roots.find((r) => samePath(r.path, folderPath)) : undefined;
  const picked = photos.filter((p) => p.flag === 'pick').length;
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (focusId == null) return;
    const el = scrollRef.current?.querySelector<HTMLElement>(`[data-sheet-id="${focusId}"]`);
    el?.scrollIntoView({ block: 'nearest' });
  }, [focusId]);

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
            <div className="grid grid-cols-8 gap-2">
              {g.photos.map((p) => (
                <SheetCell
                  key={p.id}
                  photo={p}
                  focused={p.id === focusId}
                  onFocus={focus}
                  onOpen={() => setContactSheet(false)}
                />
              ))}
            </div>
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
}: {
  photo: Photo;
  focused: boolean;
  onFocus: (id: number, opts?: { extend?: boolean; toggle?: boolean }) => void;
  onOpen: () => void;
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
          'aspect-[3/2] overflow-hidden rounded bg-inset',
          photo.flag === 'exclude' && 'opacity-40',
        )}
      >
        <img
          src={imgUrl(photo, '256')}
          alt=""
          draggable={false}
          loading="lazy"
          className="size-full object-cover"
        />
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
