import { cn } from '@/lib/utils';
import type { Photo } from '@/api/library';
import { focusScore } from '@/lib/bursts';

// SoftBadge is the soft-focus marker (◐) shared by every thumbnail surface
// (grid, cull scrubber): it shows when a frame's focus score sits below the
// shoot's soft-focus cutoff, so a reject sweep can spot the misses at a glance.
// The title reflects whether the score is subject-aware. Call sites pass only
// their positioning/scale class so the glyph, titles, and test hooks can't
// drift between views. Renders nothing when the frame isn't soft.
export function SoftBadge({
  photo,
  softBelow,
  className,
}: {
  photo: Photo;
  softBelow: number;
  className?: string;
}) {
  const score = focusScore(photo);
  if (score == null || softBelow <= 0 || score >= softBelow) return null;
  return (
    <div
      className={cn('rounded bg-black/50 px-[4px] py-0.5 text-[9px] text-amber-400', className)}
      title={`${photo.subjectSharpness != null ? 'Soft subject' : 'Soft focus'} (score ${Math.round(score)})`}
      aria-label="Soft focus"
      data-testid="soft-badge"
    >
      ◐
    </div>
  );
}
