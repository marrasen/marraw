import { cn } from '@/lib/utils';
import type { Photo } from '@/api/library';
import { EYES_CLOSED_BADGE } from '@/lib/eyes';

// EyesBadge is the blink marker (◡) shared by every thumbnail surface (grid,
// cull scrubber): it shows when closed-eye detection flags a frame, so a cull
// sweep can catch blinks before picking a burst's keeper. A soft signal —
// sunglasses, profiles, and squints misfire — so it suggests, never judges.
// Renders nothing for unflagged or unanalyzed frames.
export function EyesBadge({ photo, className }: { photo: Photo; className?: string }) {
  const p = photo.eyesClosed;
  if (p == null || p < EYES_CLOSED_BADGE) return null;
  return (
    <div
      className={cn('rounded bg-black/50 px-[4px] py-0.5 text-[9px] text-rose-400', className)}
      title={`Closed eyes detected (${Math.round(p * 100)}% confident)`}
      aria-label="Closed eyes detected"
      data-testid="eyes-badge"
    >
      ◡
    </div>
  );
}
