import { useEffect, useMemo } from 'react';
import { useListPhotos, onPhotoPatchEvent, type Photo } from '@/api/library';
import { useApiClient } from '@/api/client';
import { useUIStore } from '@/stores/uiStore';

// usePatchEvents folds server patch broadcasts into the override map.
// Mount once at App level.
export function usePatchEvents() {
  const client = useApiClient();
  useEffect(
    () =>
      onPhotoPatchEvent(client, (ev) => {
        useUIStore.getState().applyPatches(ev.patches);
      }),
    [client],
  );
}

export interface PhotoLists {
  all: Photo[];
  visible: Photo[];
  isLoading: boolean;
}

// usePhotos merges the subscribed folder list with local overrides and
// applies the cull filters, entirely client-side.
export function usePhotos(folderId: number): PhotoLists {
  const { data, isLoading } = useListPhotos(folderId);
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
