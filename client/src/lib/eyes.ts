import type { Photo } from '@/api/library';

// The closed-eye probability above which a frame gets flagged. 0.5 is the
// classifier's own decision boundary; the score itself is the worst eye of
// the frame's most confident faces (see internal/eyes). Lives here (not in
// EyesBadge) so the blinks-only filter in usePhotos and the badge share one
// threshold without the hook importing from components/.
export const EYES_CLOSED_BADGE = 0.5;

// hasClosedEyes: whether closed-eye detection flags this frame. eyesClosed is
// only set once the photo was analyzed AND a judgeable face was found (the
// server hides the "no face" sentinel), so a null check suffices.
export function hasClosedEyes(p: Photo): boolean {
  return p.eyesClosed != null && p.eyesClosed >= EYES_CLOSED_BADGE;
}
