import { useEffect, useRef, useState } from 'react';

import type { Photo } from '@/api/library';
import { imgUrl, tileUrl, type Level } from '@/lib/backend';

// When a full-resolution tile set is worth having, and how to get one without
// stalling the browse. The tile layer itself is in components/TileLayer.

// levelForPx picks the smallest rendition covering px device pixels; past
// pyramid depth the viewer switches to full-resolution tiles.
export function levelForPx(px: number): Level | 'tiles' {
  for (const l of ['256', '512', '1024', '2048'] as const) {
    if (Number(l) >= px) return l;
  }
  return 'tiles';
}

// useTilesWarm reports whether the photo's full-resolution tile set is
// already rendered (tile (0,0) is the grid-complete sentinel), optionally
// kicking ONE background render for the focused photo when it isn't. This is
// what keeps high-DPI fit view browsable: on a 4K display the fit box
// exceeds 2048 device pixels, so the loupe wants tiles for EVERY photo — but
// the pre-render pass deliberately stops at 2048, and rendering the full
// tile set on demand costs 1.5-2.5s per photo. So at fit, tiles engage only
// once they exist; a cold photo shows the warm 2048 (upscaled — exactly what
// was on screen before this hook existed) while the kicked render sharpens
// it if the user pauses. Skimming past aborts the kick mid-decode.
//
// `kick` is also the guest-side bound: an unrendered photo behind a share
// link must not let a visitor start work on the owner's decode pool for
// anything short of a deliberate deep zoom.
// KICK_DWELL_MS is how long the user stays on a photo before its missing
// tile set is rendered. `immediate` shortens it to IMMEDIATE_DWELL_MS, for a
// caller that knows the user is on the photo: the editor, right after its
// own save of it. Not to zero: a burst of saves (holding undo, stepping the
// history) still coalesces into one render instead of starting and
// aborting one per step.
const KICK_DWELL_MS = 350;
const IMMEDIATE_DWELL_MS = 120;

export function useTilesWarm(
  photo: Photo | null | undefined,
  want: boolean,
  kick: boolean,
  immediate = false,
): boolean {
  // The tile URL the warmth was found for: warm answers for that state
  // only, so a caller never reads the last state's warmth in the render the
  // next state's hash arrives in, before the effect below has run.
  const [warmUrl, setWarmUrl] = useState('');
  // Key the probe on the tile URL, not the photo OBJECT: it encodes id +
  // cacheKey + editHash, the only things that change which tiles exist. A
  // background metadata/rating push hands down a fresh photo object with the
  // same rendered state — resetting `warm` on that identity change would drop
  // wantTiles false→true for a frame, flashing the tile layer off and on.
  const url0 = photo ? tileUrl(photo, 0, 0) : '';
  // Read when the kick is set, whenever `want` turns true: not a reason to
  // probe again on its own. A dwell already under way when `immediate`
  // turns true is shortened (shorten, below): the save that makes it true
  // can land after the tiles came to be wanted.
  const immediateRef = useRef(immediate);
  const shorten = useRef<(() => void) | null>(null);
  useEffect(() => {
    immediateRef.current = immediate;
    if (immediate) shorten.current?.();
  }, [immediate]);
  useEffect(() => {
    if (!want || !url0) return;
    const ac = new AbortController();
    let dwell = 0;
    const render = async () => {
      shorten.current = null;
      window.clearTimeout(dwell);
      try {
        const res = await fetch(url0, { signal: ac.signal });
        if (res.ok) setWarmUrl(url0);
      } catch {
        // aborted: the user moved on mid-render
      }
    };
    (async () => {
      try {
        const probe = await fetch(url0 + '&cacheOnly=1', { signal: ac.signal });
        if (probe.ok) {
          setWarmUrl(url0);
          return;
        }
        // Found warm once, gone since (the janitor evicted the tiles): cold.
        setWarmUrl((w) => (w === url0 ? '' : w));
        if (!kick) return;
        // Kick only after a real dwell: while the user is skimming, no full
        // render ever STARTS — cheaper and more robust than starting one per
        // step and racing to cancel it (a render that slips past its
        // cancellation checkpoint blocks the pipeline for seconds).
        if (immediateRef.current) {
          dwell = window.setTimeout(() => void render(), IMMEDIATE_DWELL_MS);
          return;
        }
        const start = Date.now();
        shorten.current = () => {
          shorten.current = null;
          window.clearTimeout(dwell);
          const left = Math.max(0, IMMEDIATE_DWELL_MS - (Date.now() - start));
          dwell = window.setTimeout(() => void render(), left);
        };
        dwell = window.setTimeout(() => void render(), KICK_DWELL_MS);
      } catch {
        // aborted: the user moved on
      }
    })();
    return () => {
      shorten.current = null;
      window.clearTimeout(dwell);
      ac.abort();
      // Leaving the state (or the zoom) forgets its warmth: coming back
      // probes again rather than trust tiles the janitor may have evicted.
      setWarmUrl((w) => (w === url0 ? '' : w));
    };
  }, [url0, want, kick]);
  return want && url0 !== '' && warmUrl === url0;
}

// useTilePrefetch warms the photos adjacent to the focused one so stepping
// through a burst stays instant. It pre-decodes their 2048 rendition — the
// fit underlay AND the 1:1 bridge, and the single decode the backend runs for
// it also yields every smaller level, so whatever level the neighbour's fit
// needs is warm too. `active` runs this whenever the loupe is up (fit included,
// which is where a cold 2048 otherwise stalls the arrow keys). `tiles`
// additionally requests one full-res tile per neighbour, making the backend
// render the whole tile set ahead of a 1:1 landing — only worth its cost past
// pyramid depth. Underlay refs are held only for the current window so the
// browser can evict older decodes.
export function useTilePrefetch(
  photos: Photo[],
  photo: Photo,
  active: boolean,
  tiles: boolean,
  fitLevel: Level,
) {
  const held = useRef<Map<string, HTMLImageElement>>(new Map());
  const triggered = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!active) return;
    const i = photos.findIndex((p) => p.id === photo.id);
    if (i < 0) return;
    const ac = new AbortController();
    const next = new Map<string, HTMLImageElement>();
    const warm = (p: Photo, lvl: Level, cacheOnly = false) => {
      const url = imgUrl(p, lvl, cacheOnly ? { cacheOnly: true } : undefined);
      const img = held.current.get(url) ?? new Image();
      if (!img.src) {
        img.src = url;
        img.decode().catch(() => {});
      }
      next.set(url, img);
    };
    for (const j of [i + 1, i - 1, i + 2]) {
      const p = photos[j];
      if (!p) continue;
      // Warm the neighbour's fit-level rendition, cacheOnly: pull it into the
      // browser cache IF it is already on disk (the exact URL SharpImage requests
      // while browsing — imgUrl(p, fitLevel, {cacheOnly:true}) — so arrowing onto
      // it is a cache hit at any viewport), but NEVER let a neighbour trigger a RAW
      // decode. A plain (render-allowed) warm fires a PriorityVisible render
      // per cold neighbour, saturating the pool with long uncancellable unpacks
      // and freezing the browse the instant the user skims onto an unrendered
      // run. Cold neighbours are filled by the focus-first background
      // pre-render pass instead. The heavy 2048 is warmed ONLY at tile depth
      // (1:1), where browsing is deliberate and the on-demand render is the point.
      warm(p, fitLevel, true);
      if (!tiles) continue;
      warm(p, '2048');
      const tile = tileUrl(p, 0, 0);
      if (!triggered.current.has(tile)) {
        triggered.current.add(tile);
        // On abort, un-remember the trigger so a later revisit retries.
        fetch(tile, { signal: ac.signal }).catch(() => triggered.current.delete(tile));
      }
    }
    // Warm underlays that fell out of the neighbor window: stop their
    // downloads too, not just release them for eviction.
    for (const [url, img] of held.current) {
      if (!next.has(url) && !img.complete) img.src = '';
    }
    held.current = next;
    // Aborting on every focus change is fine: if the user lands on the
    // prefetched neighbor, the main image/tile layer re-requests it at
    // visible priority and the pool dedups against any run still going.
    return () => ac.abort();
  }, [photos, photo, active, tiles, fitLevel]);
}
