import { useEffect, useMemo } from 'react';
import { useListPhotos, type Photo, type PhotoPatchEvent } from '@/api/library';
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
    return next;
  });
}

export interface PhotoLists {
  all: Photo[];
  visible: Photo[];
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

  const visible = useMemo(
    () =>
      all.filter((p) => {
        if (p.rating < minRating) return false;
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
    [all, minRating, flagFilter],
  );

  // Keep keyboard navigation in sync with what is on screen.
  useEffect(() => {
    useUIStore.getState().setVisibleIds(
      visible.map((p) => p.id),
      visible.map((p) => p.takenAt),
    );
  }, [visible]);

  // Flags per photo so P/X can toggle against the current state.
  useEffect(() => {
    useUIStore.getState().setPhotoFlags(new Map(all.map((p) => [p.id, p.flag])));
  }, [all]);

  return { all, visible, isLoading };
}
