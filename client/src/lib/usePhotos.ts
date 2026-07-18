import { useEffect, useMemo } from 'react';
import { useListPhotos, type Photo, type PhotoPatchEvent } from '@/api/library';
import { burstMap, isSoft, softThreshold, type BurstInfo } from '@/lib/bursts';
import { hasClosedEyes } from '@/lib/eyes';
import { useUIStore, type LibrarySort } from '@/stores/uiStore';

// photoPatchReducer folds server-pushed subscription patches (aprot
// PatchSubscription — O(patch) on the wire) into the shared query snapshot.
// Nil patch fields mean "unchanged", so mergeByKey's blind shallow merge
// does not fit; merge non-null fields by hand.
function photoPatchReducer(data: Photo[], patch: unknown): Photo[] {
  const ev = patch as PhotoPatchEvent;
  if (!ev || !Array.isArray(ev.patches)) return data;
  const byId = new Map(ev.patches.map((p) => [p.id, p]));
  return data.map((photo) => {
    const p = byId.get(photo.id);
    if (!p) return photo;
    const next = { ...photo };
    if (p.rating != null) next.rating = p.rating;
    if (p.flag != null) next.flag = p.flag;
    if (p.editHash != null) next.editHash = p.editHash;
    // Geometry arrives as explicit values on every edit save (0 = reset), so
    // != null both applies and clears — the grid's cell aspect follows live.
    if (p.rotate != null) next.rotate = p.rotate;
    if (p.cropW != null) next.cropW = p.cropW;
    if (p.cropH != null) next.cropH = p.cropH;
    if (p.subjectSharpness != null) next.subjectSharpness = p.subjectSharpness;
    if (p.subjectAnalyzed != null) next.subjectAnalyzed = p.subjectAnalyzed;
    if (p.eyesClosed != null) next.eyesClosed = p.eyesClosed;
    if (p.eyesAnalyzed != null) next.eyesAnalyzed = p.eyesAnalyzed;
    return next;
  });
}

export interface PhotoLists {
  all: Photo[];
  visible: Photo[];
  // Soft-focus cutoff derived from the whole folder (0 = not enough scores to
  // call anything soft). Shared with the grid so its badges and the soft-only
  // filter agree.
  softBelow: number;
  // Near-duplicate groups indexed from the whole folder — the badges and the
  // collapse-bursts filter share one map so they can't disagree on the
  // sharpest member.
  bursts: Map<number, BurstInfo>;
  isLoading: boolean;
}

// Natural-numeric name comparison so DSC_9 sorts ahead of DSC_10.
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

// sortPhotos reorders the server list for display. captureAsc is the order
// ListPhotos already serves (capture time, untimed frames last, file name
// breaking ties), so it passes the list through untouched — resorting it here
// could disagree with the server on ties.
function sortPhotos(photos: Photo[], sort: LibrarySort): Photo[] {
  if (sort === 'captureAsc') return photos;
  const byName = (a: Photo, b: Photo) => collator.compare(a.fileName, b.fileName);
  const out = [...photos];
  switch (sort) {
    case 'captureDesc':
      // Untimed frames stay last (as in ascending); name keeps the order total.
      out.sort((a, b) => {
        if (a.takenAt > 0 !== b.takenAt > 0) return a.takenAt > 0 ? -1 : 1;
        return b.takenAt - a.takenAt || byName(a, b);
      });
      break;
    case 'nameAsc':
      out.sort(byName);
      break;
    case 'nameDesc':
      out.sort((a, b) => byName(b, a));
      break;
  }
  return out;
}

// usePhotos merges the subscribed folder list (kept fresh through
// subscription patches) with optimistic local overrides and applies the
// cull filters, entirely client-side.
export function usePhotos(folderId: number): PhotoLists {
  const { data, isLoading } = useListPhotos(folderId, { applyPatch: photoPatchReducer });
  const overrides = useUIStore((s) => s.overrides);
  const minRating = useUIStore((s) => s.minRating);
  const flagFilter = useUIStore((s) => s.flagFilter);
  const softOnly = useUIStore((s) => s.softOnly);
  const eyesClosedOnly = useUIStore((s) => s.eyesClosedOnly);
  const collapseBursts = useUIStore((s) => s.collapseBursts);
  const librarySort = useUIStore((s) => s.librarySort);

  // Sort before merging overrides: overrides never move a photo, so rating
  // and flag edits don't pay for a resort.
  const ordered = useMemo(() => (data ? sortPhotos(data, librarySort) : []), [data, librarySort]);

  const all = useMemo(() => {
    if (overrides.size === 0) return ordered;
    return ordered.map((p) => {
      const o = overrides.get(p.id);
      return o ? { ...p, ...o } : p;
    });
  }, [ordered, overrides]);

  // Threshold from the FULL folder, so it (and the grid badges that reuse it)
  // stays put when the soft-only filter narrows the visible set.
  const softBelow = useMemo(() => softThreshold(all), [all]);

  // Groups from the FULL folder (like softBelow), so a burst's count and best
  // frame describe the real group even while filters hide members.
  const bursts = useMemo(() => burstMap(all), [all]);

  const visible = useMemo(
    () =>
      all.filter((p) => {
        if (p.rating < minRating) return false;
        if (softOnly && !isSoft(p, softBelow)) return false;
        if (eyesClosedOnly && !hasClosedEyes(p)) return false;
        if (collapseBursts && p.groupId != null) {
          const b = bursts.get(p.groupId);
          // Keep the sharpest member; until any member has a score, keep the
          // lead frame (groupId is the lead's photo id) so a burst can't
          // vanish outright.
          if (b && (b.bestId ?? p.groupId) !== p.id) return false;
        }
        switch (flagFilter) {
          case 'pick':
            return p.flag === 'pick';
          case 'exclude':
            return p.flag === 'exclude';
          case 'not-excluded':
            return p.flag !== 'exclude';
          default:
            return true;
        }
      }),
    [all, minRating, flagFilter, softOnly, softBelow, eyesClosedOnly, collapseBursts, bursts],
  );

  // Keep keyboard navigation in sync with what is on screen.
  useEffect(() => {
    useUIStore.getState().setVisibleIds(
      visible.map((p) => p.id),
      visible.map((p) => p.takenAt),
    );
  }, [visible]);

  // Flag + rating per photo, so P/X can toggle against the current state and
  // the flag/rating undo history can capture prior values.
  useEffect(() => {
    useUIStore
      .getState()
      .setPhotoMeta(new Map(all.map((p) => [p.id, { flag: p.flag, rating: p.rating }])));
  }, [all]);

  // Burst membership (photo → its group's member ids) so Shift+P/X can judge
  // the focused photo's whole burst. From the FULL folder, like burstMap: the
  // rest of a burst must be rejected even when a filter hides some of its
  // members.
  useEffect(() => {
    const groups = new Map<number, number[]>();
    for (const p of all) {
      if (p.groupId == null) continue;
      const list = groups.get(p.groupId);
      if (list) list.push(p.id);
      else groups.set(p.groupId, [p.id]);
    }
    const byPhoto = new Map<number, number[]>();
    for (const ids of groups.values()) for (const id of ids) byPhoto.set(id, ids);
    useUIStore.getState().setBurstMembers(byPhoto);
  }, [all]);

  return { all, visible, softBelow, bursts, isLoading };
}
