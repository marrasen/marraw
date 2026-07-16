import { memo, useEffect, useMemo, useRef } from 'react';
import type { Photo } from '@/api/library';
import { burstFor, type BurstInfo } from '@/lib/bursts';
import { cn } from '@/lib/utils';
import { imgUrl } from '@/lib/backend';
import { useImgBust } from '@/lib/imgCacheBust';
import { gapLabel, rangeLabel, type TimeGroup } from '@/lib/timeGaps';
import { useHoverKeep } from '@/lib/useIdle';
import { aspectOf } from '@/lib/justify';
import { BurstBadge } from '@/components/BurstBadge';
import { EyesBadge } from '@/components/EyesBadge';
import { PyramidImage } from '@/components/PyramidImage';
import { SoftBadge } from '@/components/SoftBadge';
import { useUIStore, type ThumbFit } from '@/stores/uiStore';

// Filmstrip thumbnails are 40px tall. crop keeps the fixed 60px slot; fit and
// natural size the width to the frame's aspect (fallback 60 = 40×1.5, so an
// unscanned frame doesn't reflow when its dimensions arrive).
const STRIP_H = 40;
function stripWidth(photo: Photo, fit: ThumbFit): number {
  if (fit === 'crop') return 60;
  return Math.max(1, Math.round(STRIP_H * aspectOf(photo)));
}

/**
 * The Cull scrubber deck: the shoot as a horizontal glass strip of 60×40
 * thumbnails, split into time-gap groups with a vertical "+N min gap"
 * divider between them. G blows it up into the contact sheet.
 */
export function ScrubberDeck({
  groups,
  focusId,
  hidden,
  shifted,
  softBelow = 0,
  bursts,
}: {
  groups: TimeGroup[];
  focusId: number | null;
  hidden?: boolean;
  /** Center on the free canvas left of Develop's pinned drawer. */
  shifted?: boolean;
  /** Soft-focus cutoff for the per-thumb soft badge; 0 hides it (Develop). */
  softBelow?: number;
  /** Near-duplicate groups for the per-thumb burst badge; omitted hides it. */
  bursts?: Map<number, BurstInfo>;
}) {
  const focus = useUIStore((s) => s.focus);
  const thumbFit = useUIStore((s) => s.thumbFit);
  // The deck never fades while the pointer rests on it (useHoverKeep); the
  // handlers live on the filmstrip itself — the outer wrapper spans the
  // window width and is pointer-events-none.
  const { hovered, bind } = useHoverKeep();
  const scrollRef = useRef<HTMLDivElement>(null);
  const centeredOnce = useRef(false);
  const anim = useRef(0);

  // Per-photo strip widths, memoized so a keystroke doesn't recompute the
  // whole roll. widthsKey fingerprints the values: the centering effect keys
  // on it so real reflow (metadata streaming in, a thumbFit switch) re-centers
  // the focus, while a rating patch (new identities, same geometry) doesn't.
  const stripWidths = useMemo(
    () => groups.map((g) => g.photos.map((p) => stripWidth(p, thumbFit))),
    [groups, thumbFit],
  );
  const widthsKey = useMemo(() => stripWidths.flat().join(','), [stripWidths]);

  // Manual scrolling takes over from an in-flight centering animation.
  useEffect(() => {
    const box = scrollRef.current;
    if (!box) return;
    const cancel = () => {
      cancelAnimationFrame(anim.current);
      anim.current = 0;
    };
    box.addEventListener('wheel', cancel, { passive: true });
    box.addEventListener('pointerdown', cancel);
    return () => {
      cancel();
      box.removeEventListener('wheel', cancel);
      box.removeEventListener('pointerdown', cancel);
    };
  }, []);

  // Keep the focused frame centered as the user arrows through the take.
  // The first centering after mount (a mode switch remounts the deck) jumps
  // instantly — smooth would visibly scroll in from the start of the roll.
  // Native smooth scrollIntoView restarts its easing from zero every time a
  // new call interrupts it, so rapid arrowing stalls near the old frame; the
  // rAF loop instead retargets mid-flight and keeps the motion continuous.
  useEffect(() => {
    const box = scrollRef.current;
    if (focusId == null || !box) return;
    const el = box.querySelector<HTMLElement>(`[data-strip-id="${focusId}"]`);
    if (!el) return;
    const elRect = el.getBoundingClientRect();
    const boxRect = box.getBoundingClientRect();
    const target = Math.max(
      0,
      Math.min(
        box.scrollWidth - box.clientWidth,
        box.scrollLeft + (elRect.left - boxRect.left) - (box.clientWidth - elRect.width) / 2,
      ),
    );
    cancelAnimationFrame(anim.current);
    anim.current = 0;
    const instant = !centeredOnce.current || matchMedia('(prefers-reduced-motion: reduce)').matches;
    centeredOnce.current = true;
    if (instant) {
      box.scrollLeft = target;
      return;
    }
    // Track position as a float — scrollLeft assignments round to device
    // pixels, and the exponential approach would stall inside that rounding.
    let pos = box.scrollLeft;
    let last = performance.now();
    const step = (now: number) => {
      const dt = Math.min(64, now - last);
      last = now;
      pos += (target - pos) * (1 - Math.exp(-dt / 90));
      if (Math.abs(target - pos) < 0.75) {
        box.scrollLeft = target;
        anim.current = 0;
        return;
      }
      box.scrollLeft = pos;
      anim.current = requestAnimationFrame(step);
    };
    anim.current = requestAnimationFrame(step);
  }, [focusId, widthsKey]);

  const conceal = hidden && !hovered;
  return (
    <div
      className={cn(
        'pointer-events-none absolute bottom-4 left-4 z-30 flex justify-center transition-opacity duration-300',
        shifted ? 'right-[384px]' : 'right-4',
        conceal && 'opacity-0',
      )}
    >
    <div
      {...bind}
      data-testid="filmstrip"
      className={cn(
        'flex max-w-full items-stretch rounded-[11px] border border-glass-border px-3.5 py-2.5',
        'bg-white/75 backdrop-blur-2xl dark:bg-[rgba(10,12,16,.6)]',
        !conceal && 'pointer-events-auto',
      )}
    >
      <div className="mr-2 flex shrink-0 flex-col justify-center gap-[3px] border-r border-white/12 pr-3">
        <span className="text-[9px] tracking-[.06em] text-muted-foreground uppercase">Groups</span>
        <span className="font-mono text-[13px] text-accent-text">{groups.length}</span>
      </div>
      {/* py/-my and px/-mx slack: the focused thumb scales past its slot and
          the scroll container would otherwise clip the overhang — vertically
          for every frame, horizontally for the first/last of the roll where
          the enlarged frame and its border sit flush against the edge. */}
      <div ref={scrollRef} className="no-scrollbar -mx-2 -my-1.5 flex items-stretch overflow-x-auto px-2 py-1.5">
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
                {g.photos.map((p, j) => (
                  <StripThumb
                    key={p.id}
                    photo={p}
                    focused={p.id === focusId}
                    onFocus={focus}
                    width={stripWidths[i][j]}
                    softBelow={softBelow}
                    burst={bursts && burstFor(p, bursts)}
                  />
                ))}
              </div>
            </div>
            {i < groups.length - 1 && <div className="w-2 shrink-0" />}
          </div>
        ))}
      </div>
    </div>
    </div>
  );
}

// Memoized: a keystroke re-renders the deck, and only the two thumbs whose
// focus state changed need to reconcile out of a potentially huge roll.
const StripThumb = memo(function StripThumb({
  photo,
  focused,
  onFocus,
  width,
  softBelow,
  burst,
}: {
  photo: Photo;
  focused: boolean;
  onFocus: (id: number, opts?: { extend?: boolean; toggle?: boolean }) => void;
  width: number;
  softBelow: number;
  burst?: BurstInfo;
}) {
  useImgBust(photo.id); // refetch when a restored AI map repaints this thumb
  return (
    <button
      data-strip-id={photo.id}
      // The focused frame pops like a dock icon: a transform (layout-free, so
      // neighbors and the centering scroll math stay put) with a lift shadow.
      className={cn(
        'relative h-10 shrink-0 overflow-hidden rounded-[3px] bg-inset transition-transform duration-150',
        focused && 'z-10 scale-[1.22] shadow-[0_5px_16px_rgba(0,0,0,.5)]',
      )}
      style={{ width }}
      onClick={(e) => onFocus(photo.id, { extend: e.shiftKey, toggle: e.ctrlKey || e.metaKey })}
      title={photo.fileName}
    >
      <PyramidImage
        src={imgUrl(photo, '256')}
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
      {photo.rating > 0 && (
        <span
          data-testid="strip-rating"
          className="absolute bottom-0.5 left-0.5 flex items-center gap-px rounded-[3px] bg-black/55 px-1 text-[8px]"
        >
          <span className="text-rating">★</span>
          <span className="text-white">{photo.rating}</span>
        </span>
      )}
      <div className="absolute right-0.5 bottom-0.5 flex items-center gap-0.5">
        <EyesBadge photo={photo} className="px-1 py-0 text-[8px] leading-none" />
        <SoftBadge photo={photo} softBelow={softBelow} className="px-1 py-0 text-[8px] leading-none" />
      </div>
      {burst && (
        <BurstBadge
          burst={burst}
          photoId={photo.id}
          className="absolute top-0.5 left-0.5 px-1 py-0 text-[8px] leading-none"
        />
      )}
      {focused && <span className="absolute inset-0 rounded-[3px] border-2 border-primary" />}
    </button>
  );
});
