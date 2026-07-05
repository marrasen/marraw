import { useEffect, useMemo } from 'react';
import { useListPhotos, type Photo, type PhotoPatchEvent } from '@/api/library';
import { useUIStore } from '@/stores/uiStore';

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

// usePhotos merges the subscribed folder list (kept fresh through
// subscription patches) with optimistic local overrides and applies the
// cull filters, entirely client-side.
export function usePhotos(folderId: number): PhotoLists {
  const { data, isLoading } = useListPhotos(folderId, { applyPatch: photoPatchReducer });
  const overrides = useUIStore((s) => s.overrides);
  const minRating = useUIStore((s) => s.minRating);
  const flagFilter = useUIStore((s) => s.flagFilter);

  const all = useMemo(() => {
    if (!data) return [];
    if (overrides.size === 0) return data;
    return data.map((p) => {
      const o = overrides.get(p.id);
      return o ? { ...p, ...o } : p;
    });
  }, [data, overrides]);

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
    useUIStore.getState().setVisibleIds(visible.map((p) => p.id));
  }, [visible]);

  return { all, visible, isLoading };
}
