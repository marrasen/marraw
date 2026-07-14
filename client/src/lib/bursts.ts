import type { Photo } from '@/api/library';

// focusScore is the score culling judgments use: the subject-weighted score
// when the photo has an AI subject matte (so a sharp background can't hide a
// soft subject), otherwise the whole-frame score.
export function focusScore(p: Photo): number | undefined {
  return p.subjectSharpness ?? p.sharpness;
}

export interface BurstInfo {
  count: number;
  // bestId is the group's sharpest member by focusScore; null until at least
  // one member has a measured score.
  bestId: number | null;
}

// burstMap indexes the near-duplicate groups the backend derives (frames
// shot moments apart whose perceptual hashes match, photo.groupId): per
// group, its size and its sharpest member — the frame the badges suggest
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
    map.set(groupId, { count: list.length, bestId });
  }
  return map;
}

// burstFor is the per-cell lookup: this photo's group info, or undefined
// when it is not part of a burst.
export function burstFor(p: Photo, bursts: Map<number, BurstInfo>): BurstInfo | undefined {
  return p.groupId == null ? undefined : bursts.get(p.groupId);
}
