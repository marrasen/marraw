import { useEffect, useRef, useState } from 'react';

import type { Photo } from '@/api/library';
import { TILE_SIZE, tileUrl } from '@/lib/backend';

// TileLayer sharpens a zoomed image past pyramid depth: the part of the image
// currently in view (plus a margin) is covered with full-resolution TILE_SIZE
// tiles scaled into the display box, on top of the always-present 2048
// underlay. A tile a hair off the rendered image's edge 404s and simply stays
// hidden, leaving the underlay visible.
//
// The layer knows nothing about how its host pans. The desktop loupe pans by
// scrolling a slack-padded container; the share page pans by transforming the
// image under a finger. Both boil down to "which rectangle of the image can
// the viewer see", which is what viewportFor answers — see TileViewport.

/** The visible region of the image, in image pixels. */
export interface TileViewport {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** The container measurements viewportFor derives the visible region from. */
export interface ContainerMetrics {
  scrollLeft: number;
  scrollTop: number;
  clientWidth: number;
  clientHeight: number;
}

export function TileLayer({
  photo,
  dw,
  dh,
  scale,
  container,
  viewportFor,
  maxTiles,
  onProgress,
}: {
  photo: Photo;
  dw: number; // rendered (crop-aware) display width
  dh: number;
  /** Image pixels to layer pixels — the layer's own scaling, excluding any transform its host applies on top. */
  scale: number;
  container: React.RefObject<HTMLElement | null>;
  /**
   * Maps container metrics to the visible region. Recomputed whenever this
   * changes identity, so a host that pans without scrolling (a CSS transform)
   * passes a fresh closure per render and a host that scrolls memoizes it.
   */
  viewportFor: (m: ContainerMetrics) => TileViewport;
  /**
   * Cap on mounted tiles, least-recently-visible evicted first (never one
   * that is on screen). Omitted, tiles accumulate for the component's
   * lifetime, so panning back never re-fades — right when the host unmounts
   * the layer per photo and the image is bounded by a desktop viewport.
   */
  maxTiles?: number;
  /** Reports (pending, loaded) tile counts for a rendering indicator. */
  onProgress?: (pending: number, loaded: number) => void;
}) {
  const cols = Math.ceil(dw / TILE_SIZE);
  const rows = Math.ceil(dh / TILE_SIZE);
  // Tile keys mounted so far, and a mirror the scroll handler can read
  // without going through React state.
  const [tiles, setTiles] = useState<string[]>([]);
  const tilesRef = useRef<string[]>([]);
  // Loaded keys rather than a count: eviction has to take a tile back out.
  const loadedRef = useRef(new Set<string>());
  const [loaded, setLoaded] = useState(0);
  // Monotonic visibility clock, so eviction can pick the tile whose region
  // the viewer left longest ago.
  const lastSeen = useRef(new Map<string, number>());
  const tick = useRef(0);

  // Keep the latest callback without retriggering the count effect below;
  // updated in an effect (not during render) per react-hooks/refs.
  const onProgressRef = useRef(onProgress);
  useEffect(() => {
    onProgressRef.current = onProgress;
  });
  useEffect(() => {
    onProgressRef.current?.(Math.max(0, tiles.length - loaded), loaded);
  }, [tiles.length, loaded]);
  // Component unmount (leaving 1:1 or switching photo) clears the indicator.
  useEffect(() => () => onProgressRef.current?.(0, 0), []);

  const settle = (key: string) => {
    if (loadedRef.current.has(key)) return;
    loadedRef.current.add(key);
    setLoaded(loadedRef.current.size);
  };

  useEffect(() => {
    const el = container.current;
    if (!el) return;
    let raf = 0;
    const update = () => {
      raf = 0;
      const node = container.current;
      if (!node) return;
      const vp = viewportFor({
        scrollLeft: node.scrollLeft,
        scrollTop: node.scrollTop,
        clientWidth: node.clientWidth,
        clientHeight: node.clientHeight,
      });
      const margin = TILE_SIZE / 2;
      const x0 = Math.max(0, Math.floor((vp.x - margin) / TILE_SIZE));
      const y0 = Math.max(0, Math.floor((vp.y - margin) / TILE_SIZE));
      const x1 = Math.min(cols - 1, Math.floor((vp.x + vp.w + margin) / TILE_SIZE));
      const y1 = Math.min(rows - 1, Math.floor((vp.y + vp.h + margin) / TILE_SIZE));

      tick.current += 1;
      const onScreen: string[] = [];
      for (let ty = y0; ty <= y1; ty++) {
        for (let tx = x0; tx <= x1; tx++) {
          const k = `${tx},${ty}`;
          onScreen.push(k);
          lastSeen.current.set(k, tick.current);
        }
      }

      const have = new Set(tilesRef.current);
      const added = onScreen.filter((k) => !have.has(k));
      const over = maxTiles !== undefined && tilesRef.current.length + added.length > maxTiles;
      if (added.length === 0 && !over) return;

      let next = added.length > 0 ? [...tilesRef.current, ...added] : tilesRef.current;
      let dropped = 0;
      if (maxTiles !== undefined && next.length > maxTiles) {
        const visible = new Set(onScreen);
        const evictable = next
          .filter((k) => !visible.has(k))
          .sort((a, b) => (lastSeen.current.get(a) ?? 0) - (lastSeen.current.get(b) ?? 0));
        const drop = new Set(evictable.slice(0, next.length - maxTiles));
        if (drop.size > 0) {
          for (const k of drop) {
            lastSeen.current.delete(k);
            loadedRef.current.delete(k);
          }
          next = next.filter((k) => !drop.has(k));
          dropped = drop.size;
        }
      }
      // Being over the cap is not itself a change: when every mounted tile is
      // on screen there is nothing evictable, and setting fresh state anyway
      // re-runs this effect (viewportFor is a new closure per render) and
      // spins forever.
      if (added.length === 0 && dropped === 0) return;
      tilesRef.current = next;
      setTiles(next);
      if (dropped > 0) setLoaded(loadedRef.current.size);
    };
    const schedule = () => {
      if (!raf) raf = requestAnimationFrame(update);
    };
    el.addEventListener('scroll', schedule);
    update();
    return () => {
      el.removeEventListener('scroll', schedule);
      cancelAnimationFrame(raf);
    };
  }, [container, viewportFor, cols, rows, maxTiles]);

  return (
    <div className="pointer-events-none absolute inset-0 overflow-hidden">
      <div
        className="absolute top-0 left-0 origin-top-left"
        style={{ width: dw, height: dh, transform: `scale(${scale})` }}
      >
        {tiles.map((k) => {
          const [tx, ty] = k.split(',').map(Number);
          return (
            <Tile
              key={k}
              src={tileUrl(photo, tx, ty)}
              left={tx * TILE_SIZE}
              top={ty * TILE_SIZE}
              onSettled={() => settle(k)}
            />
          );
        })}
      </div>
    </div>
  );
}

// Tile renders at its natural size (the server decides edge-tile dimensions)
// and fades in once loaded; a 404 off the rendered edge stays invisible.
// onSettled fires on load AND error so the rendering indicator never hangs
// on an edge tile that does not exist.
function Tile({
  src,
  left,
  top,
  onSettled,
}: {
  src: string;
  left: number;
  top: number;
  onSettled?: () => void;
}) {
  const [loaded, setLoaded] = useState(false);
  const settled = useRef(false);
  const settle = () => {
    if (!settled.current) {
      settled.current = true;
      onSettled?.();
    }
  };
  return (
    <img
      src={src}
      alt=""
      draggable={false}
      onLoad={() => {
        setLoaded(true);
        settle();
      }}
      onError={settle}
      className="absolute max-w-none transition-opacity duration-150"
      style={{ left, top, opacity: loaded ? 1 : 0 }}
    />
  );
}
