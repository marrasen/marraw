import { useEffect, useRef } from 'react';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { imgUrl } from '@/lib/backend';
import { gapLabel, rangeLabel, type TimeGroup } from '@/lib/timeGaps';
import { useUIStore } from '@/stores/uiStore';

/**
 * The Cull scrubber deck: the shoot as a horizontal glass strip of 60×40
 * thumbnails, split into time-gap groups with a vertical "+N min gap"
 * divider between them. G blows it up into the contact sheet.
 */
export function ScrubberDeck({
  groups,
  focusId,
  hidden,
}: {
  groups: TimeGroup[];
  focusId: number | null;
  hidden?: boolean;
}) {
  const focus = useUIStore((s) => s.focus);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Keep the focused frame centered as the user arrows through the take.
  useEffect(() => {
    if (focusId == null) return;
    const el = scrollRef.current?.querySelector<HTMLElement>(`[data-strip-id="${focusId}"]`);
    el?.scrollIntoView({ inline: 'center', block: 'nearest', behavior: 'smooth' });
  }, [focusId]);

  return (
    <div
      className={cn(
        'absolute bottom-4 left-1/2 z-30 flex max-w-[90%] -translate-x-1/2 items-stretch rounded-[11px] border border-glass-border px-3.5 py-2.5 transition-opacity duration-300',
        'bg-white/75 backdrop-blur-2xl dark:bg-[rgba(10,12,16,.6)]',
        hidden && 'pointer-events-none opacity-0',
      )}
    >
      <div className="mr-2 flex shrink-0 flex-col justify-center gap-[3px] border-r border-white/12 pr-3">
        <span className="text-[9px] tracking-[.06em] text-muted-foreground uppercase">Groups</span>
        <span className="font-mono text-[13px] text-accent-text">{groups.length}</span>
      </div>
      <div ref={scrollRef} className="no-scrollbar flex items-stretch overflow-x-auto">
        {groups.map((g, i) => (
          <div key={i} className="flex shrink-0 items-stretch">
            {g.gapBeforeMin != null && g.gapBeforeMin > 0 && (
              <div className="mx-0.5 flex items-center justify-center px-2">
                <span
                  className="rounded border border-primary/30 bg-primary/15 px-[3px] py-[5px] font-mono text-[9px] whitespace-nowrap text-[#aab0ff]"
                  style={{ writingMode: 'vertical-rl', transform: 'rotate(180deg)' }}
                >
                  {gapLabel(g.gapBeforeMin)}
                </span>
              </div>
            )}
            <div className="flex flex-col gap-[5px]">
              <div className="flex items-baseline gap-1.5 pl-px">
                <span className="font-mono text-[10px] text-foreground">{rangeLabel(g)}</span>
                <span className="text-[9px] text-muted-foreground">{g.photos.length}</span>
              </div>
              <div className="flex gap-1">
                {g.photos.map((p) => (
                  <StripThumb key={p.id} photo={p} focused={p.id === focusId} onFocus={focus} />
                ))}
              </div>
            </div>
            {i < groups.length - 1 && <div className="w-2 shrink-0" />}
          </div>
        ))}
      </div>
    </div>
  );
}

function StripThumb({
  photo,
  focused,
  onFocus,
}: {
  photo: Photo;
  focused: boolean;
  onFocus: (id: number, opts?: { extend?: boolean; toggle?: boolean }) => void;
}) {
  return (
    <button
      data-strip-id={photo.id}
      className="relative h-10 w-[60px] shrink-0 overflow-hidden rounded-[3px] bg-inset"
      onClick={(e) => onFocus(photo.id, { extend: e.shiftKey, toggle: e.ctrlKey || e.metaKey })}
      title={photo.fileName}
    >
      <img
        src={imgUrl(photo, '256')}
        alt=""
        draggable={false}
        loading="lazy"
        className={cn('size-full object-cover', photo.flag === 'exclude' && 'opacity-40')}
      />
      {photo.flag !== 'none' && (
        <span
          className={cn(
            'absolute top-0.5 right-0.5 size-1.5 rounded-[2px]',
            photo.flag === 'pick' ? 'bg-success' : 'bg-destructive',
          )}
        />
      )}
      {focused && <span className="absolute inset-0 rounded-[3px] border-2 border-primary" />}
    </button>
  );
}
