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
export function burstMap(photos: Photo[]): Map<number, BurstInfo> {
  const map = new Map<number, BurstInfo>();
  const bestScore = new Map<number, number>();
  for (const p of photos) {
    if (p.groupId == null) continue;
    const info = map.get(p.groupId) ?? { count: 0, bestId: null };
    info.count++;
    const score = focusScore(p);
    if (score != null && score > (bestScore.get(p.groupId) ?? -Infinity)) {
      bestScore.set(p.groupId, score);
      info.bestId = p.id;
    }
    map.set(p.groupId, info);
  }
  return map;
}

// burstFor is the per-cell lookup: this photo's group info, or undefined
// when it is not part of a burst.
export function burstFor(p: Photo, bursts: Map<number, BurstInfo>): BurstInfo | undefined {
  return p.groupId == null ? undefined : bursts.get(p.groupId);
}
