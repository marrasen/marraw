import type { Photo } from '@/api/library';

// focusScore is the score culling judgments use: the subject-weighted score
// when the photo has an AI subject matte (so a sharp background can't hide a
// soft subject), otherwise the whole-frame score.
export function focusScore(p: Photo): number | undefined {
  return p.subjectSharpness ?? p.sharpness;
}

// softThreshold derives the soft-focus cutoff from the shoot itself: sharpness
// scores are scene-dependent (low-texture scenes score low at perfect focus),
// so a frame is "soft" when it sits far below its own folder's median — the
// within-shoot comparison culling actually needs. The 50 floor keeps
// uniformly-low-texture folders badge-free. 0 means "can't call anything soft".
//
// The median must come from ONE population: whole-frame sharpness only. Mixing
// in subject-only scores (systematically lower — subject-region variance) skews
// the cutoff and false-badges masked frames. isSoft still compares each frame's
// own focusScore, so "background sharp, subject soft" frames still trip it.
//
// Feed this the WHOLE folder (not a filtered view), so the cutoff and the
// per-cell badges agree no matter which filters are active.
export function softThreshold(photos: Photo[]): number {
  const vals = photos.map((p) => p.sharpness).filter((v): v is number => v != null).sort((a, b) => a - b);
  if (vals.length < 4) return 0; // too few measurements to call anything soft
  return Math.max(50, vals[Math.floor(vals.length / 2)] / 15);
}

// isSoft: does this frame trip the soft-focus badge/filter — its own focusScore
// below the shoot-relative cutoff. softBelow <= 0 disables it (too few
// measurements, or a uniformly low-texture folder).
export function isSoft(p: Photo, softBelow: number): boolean {
  if (softBelow <= 0) return false;
  const score = focusScore(p);
  return score != null && score < softBelow;
}

export interface BurstInfo {
  count: number;
  // bestId is the group's sharpest member by focusScore; null until at least
  // one member has a measured score.
  bestId: number | null;
  // Member photo ids in the order of the list fed to burstMap (display
  // order; capture order under the default sort), so a badge can say "2/4" —
  // which frame of the burst this is, not just how big the burst is.
  members: number[];
}

// burstMap indexes the near-duplicate groups the backend derives (frames
// shot moments apart whose perceptual hashes match, photo.groupId): per
// group, its members and its sharpest one — the frame the badges suggest
// keeping.
//
// Rank each group by ONE metric so members stay comparable: the subject score
// only when EVERY member has one (else a subject-region variance, which runs
// lower for a smooth in-focus subject, would lose to a sibling's
// background-inflated whole-frame score — crowning the softer frame). Feed it
// the whole-folder photo list, not a filtered view, so the count and best
// frame describe the real group.
export function burstMap(photos: Photo[]): Map<number, BurstInfo> {
  const members = new Map<number, Photo[]>();
  for (const p of photos) {
    if (p.groupId == null) continue;
    const list = members.get(p.groupId);
    if (list) list.push(p);
    else members.set(p.groupId, [p]);
  }
  const map = new Map<number, BurstInfo>();
  for (const [groupId, list] of members) {
    const allSubject = list.every((p) => p.subjectSharpness != null);
    const metric = (p: Photo) => (allSubject ? p.subjectSharpness : p.sharpness);
    let bestId: number | null = null;
    let best = -Infinity;
    for (const p of list) {
      const score = metric(p);
      if (score != null && score > best) {
        best = score;
        bestId = p.id;
      }
    }
    map.set(groupId, { count: list.length, bestId, members: list.map((p) => p.id) });
  }
  return map;
}

// burstFor is the per-cell lookup: this photo's group info, or undefined
// when it is not part of a burst.
export function burstFor(p: Photo, bursts: Map<number, BurstInfo>): BurstInfo | undefined {
  return p.groupId == null ? undefined : bursts.get(p.groupId);
}
