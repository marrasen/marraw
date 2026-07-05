import { toast } from 'sonner';
import { setRating, setFlag, type FlagType } from '@/api/library';
import type { ApiClient } from '@/api/client';
import { useUIStore } from '@/stores/uiStore';

// Optimistic rating/flag mutations shared by the keyboard map and the
// edit-panel controls: apply the local override immediately, then persist.
export function applyRating(client: ApiClient, ids: number[], rating: number) {
  if (ids.length === 0) return;
  useUIStore.getState().applyLocal(ids, { rating });
  setRating(client, ids, rating).catch((err) => toast.error(`Rating failed: ${err.message}`));
}

export function applyFlag(client: ApiClient, ids: number[], flag: FlagType) {
  if (ids.length === 0) return;
  useUIStore.getState().applyLocal(ids, { flag });
  setFlag(client, ids, flag).catch((err) => toast.error(`Flag failed: ${err.message}`));
}
