import { useSyncExternalStore } from 'react';

// Per-photo image cache-buster.
//
// /img renditions are content-addressed by cacheKey (v), editHash (e) and
// render version (r), then served immutable — so the browser caches them for a
// year. That is correct only while those three fully determine the pixels, but
// an AI-mask map is a render input that lives OUTSIDE the edit hash. Restoring
// a sidecar-imported map (Edits.GenerateAIMap returning generated=true)
// regenerates the pixels of an edit whose hash is unchanged: the server drops
// its now-wrong cached renditions (Cache.InvalidateEdit), yet the browser keeps
// serving the stale, map-less thumbnail under the very same URL. The loupe
// recovers through its live preview blob; grid thumbnails, which point straight
// at /img, do not.
//
// bumpImgBust adds a per-photo nonce to the URL (imgUrl's `b` query param, which
// the server ignores for path resolution) so those thumbnails refetch the
// corrected pixels. It is persisted because the stale entry is cached immutable
// and would otherwise resurface after a reload; the next real edit changes the
// edit hash and supersedes the nonce anyway.

const KEY = 'marraw.imgBust';
const CAP = 512; // oldest entries evicted; bumps are rare (map restores only)

function load(): Map<number, number> {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return new Map();
    return new Map(JSON.parse(raw) as [number, number][]);
  } catch {
    return new Map();
  }
}

const busts = load();
const listeners = new Set<() => void>();

function persist() {
  try {
    // Map insertion order is recency (bump re-inserts): keep the newest CAP.
    const entries = [...busts.entries()];
    localStorage.setItem(KEY, JSON.stringify(entries.slice(Math.max(0, entries.length - CAP))));
  } catch {
    // storage full or disabled: the in-memory map still busts this session.
  }
}

export function getImgBust(photoId: number): number {
  return busts.get(photoId) ?? 0;
}

// bumpImgBust records that photoId's rendered pixels changed under an unchanged
// URL, so every cached /img rendition of it must be refetched.
export function bumpImgBust(photoId: number): void {
  const next = (busts.get(photoId) ?? 0) + 1;
  busts.delete(photoId); // re-insert at the end so CAP eviction is LRU
  busts.set(photoId, next);
  persist();
  for (const l of listeners) l();
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

// useImgBust re-renders the caller when photoId's cache-buster advances, so a
// mounted thumbnail picks up the new imgUrl the moment a map restore lands.
export function useImgBust(photoId: number): number {
  return useSyncExternalStore(
    subscribe,
    () => getImgBust(photoId),
  );
}
