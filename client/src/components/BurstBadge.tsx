import { cn } from '@/lib/utils';
import type { BurstInfo } from '@/lib/bursts';

// BurstBadge is the near-duplicate marker shared by every thumbnail surface
// (grid, contact sheet): "⧉ 2/4" — this frame's position in its burst —
// tinted as the group's sharpest frame on the best member. Call sites pass
// only their positioning class so the label, titles, and test hooks can't
// drift between views.
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
  const pos = burst.members.indexOf(photoId) + 1;
  return (
    <div
      className={cn(
        'rounded bg-black/50 px-[5px] py-0.5 font-mono text-[9px]',
        isBest ? 'text-success-text' : 'text-zinc-300',
        className,
      )}
      title={
        isBest
          ? `Frame ${pos} of a ${burst.count}-frame burst — sharpest frame`
          : `Frame ${pos} of a ${burst.count}-frame burst of near-duplicates`
      }
      data-testid="burst-badge"
      data-best={isBest || undefined}
    >
      ⧉ {pos > 0 ? `${pos}/${burst.count}` : burst.count}
    </div>
  );
}
