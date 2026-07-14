import { cn } from '@/lib/utils';
import type { BurstInfo } from '@/lib/bursts';

// BurstBadge is the near-duplicate marker shared by every thumbnail surface
// (grid, contact sheet): the ⧉ count, tinted as the group's sharpest frame on
// the best member. Call sites pass only their positioning class so the label,
// titles, and test hooks can't drift between views.
export function BurstBadge({
  burst,
  photoId,
  className,
}: {
  burst: BurstInfo;
  photoId: number;
  className?: string;
}) {
  const isBest = burst.bestId === photoId;
  return (
    <div
      className={cn(
        'rounded bg-black/50 px-[5px] py-0.5 font-mono text-[9px]',
        isBest ? 'text-success-text' : 'text-zinc-300',
        className,
      )}
      title={
        isBest
          ? `Burst of ${burst.count} — sharpest frame`
          : `Burst of ${burst.count} near-duplicates`
      }
      data-testid="burst-badge"
      data-best={isBest || undefined}
    >
      ⧉ {burst.count}
    </div>
  );
}
