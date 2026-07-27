// Server-rendered mask-tint cache (Edits.MaskTintPreview PNGs as object
// URLs), shared by MaskHoverTint (panel-hovered committed masks) and
// PersonPickOverlay (loupe-hovered person candidates): hovering the same
// mask twice shouldn't refetch. Keyed by everything that changes the
// rendered plane; object URLs are revoked on eviction. Sized for a chips
// row of people plus a handful of panel masks.
import type { ApiClient } from '@/api/client';
import type { Mask, Params } from '@/api/edit';
import { maskTintPreview } from '@/api/edits';

const tintCache = new Map<string, string>();
const tintOrder: string[] = [];
const TINT_CACHE_CAP = 16;

// tintKey identifies one mask's rendered tint: the mask itself plus the
// geometry that moves the display frame under it.
export function tintKey(photoId: number, mask: Mask, draft: Params): string {
  return JSON.stringify({
    p: photoId,
    m: mask,
    c: [draft.cropX, draft.cropY, draft.cropW, draft.cropH, draft.cropAngle, draft.rotate, draft.flipH],
  });
}

export function cachedTint(key: string): string | undefined {
  return tintCache.get(key);
}

function cacheTint(key: string, url: string) {
  if (tintCache.has(key)) return;
  tintCache.set(key, url);
  tintOrder.push(key);
  if (tintOrder.length > TINT_CACHE_CAP) {
    const evict = tintOrder.shift()!;
    const old = tintCache.get(evict);
    tintCache.delete(evict);
    if (old) URL.revokeObjectURL(old);
  }
}

// fetchTint resolves a mask's tint URL through the cache. params must hold
// the mask at maskIndex — for a hover candidate that is the draft with the
// candidate appended, without ever committing it.
export async function fetchTint(
  client: ApiClient,
  photoId: number,
  params: Params,
  maskIndex: number,
  key: string,
): Promise<string> {
  const hit = tintCache.get(key);
  if (hit) return hit;
  const blob = await maskTintPreview(client, photoId, params, maskIndex, 1024);
  // A concurrent fetch may have landed the same key; reuse its URL so the
  // eviction bookkeeping stays one URL per key.
  const raced = tintCache.get(key);
  if (raced) return raced;
  const url = URL.createObjectURL(blob);
  cacheTint(key, url);
  return url;
}
