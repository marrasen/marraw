import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Maximize, Square, Star, Check, X } from 'lucide-react';
import type { Photo } from '@/api/library';
import { useApiClient, type ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Slider } from '@/components/ui/slider';
import { cn } from '@/lib/utils';
import { imgUrl, tileUrl, TILE_SIZE, type Level } from '@/lib/backend';
import { esCommit, esPickWB, esSetCropping, esUpdate, useEditSession } from '@/lib/editSession';
import { useUIStore } from '@/stores/uiStore';
import { displayDims as fullDisplayDims, renderedDims, ASPECT_PRESETS } from '@/lib/crop';
import { CropOverlay } from '@/components/CropOverlay';

export function LoupeView({ photos }: { photos: Photo[] }) {
  const focusId = useUIStore((s) => s.focusId);
  const photo = photos.find((p) => p.id === focusId) ?? photos[0];

  if (!photo) {
    return (
      <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
        Nothing to show.
      </div>
    );
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <MainImage photo={photo} photos={photos} />
      <Filmstrip photos={photos} currentId={photo.id} />
    </div>
  );
}

// aspectRatioFrac converts a selected aspect preset into a crop ratio in
// fraction space (crop-width-fraction / crop-height-fraction), accounting for
// the frame's own aspect: a 1:1 pixel crop of a 3:2 frame is not 1:1 in
// fractions. Returns null for a freeform crop.
function aspectRatioFrac(key: string, fdw: number, fdh: number): number | null {
  const preset = ASPECT_PRESETS.find((p) => p.key === key);
  if (!preset) return null;
  const ratio = key === 'orig' ? fdw / fdh : preset.ratio;
  if (!ratio || fdw <= 0 || fdh <= 0) return null;
  return (ratio * fdh) / fdw;
}

// CropToolbar shows aspect presets plus reset/done while the crop overlay is
// active, in the same slot the zoom toolbar normally occupies.
function CropToolbar({
  client,
  aspectKey,
  onPickAspect,
}: {
  client: ApiClient;
  aspectKey: string;
  onPickAspect: (k: string) => void;
}) {
  return (
    <div className="absolute bottom-3 left-1/2 z-20 flex -translate-x-1/2 items-center gap-1 rounded-md border bg-background/85 px-2 py-1 backdrop-blur">
      {ASPECT_PRESETS.map((p) => (
        <Button
          key={p.key}
          size="sm"
          variant={aspectKey === p.key ? 'secondary' : 'ghost'}
          onClick={() => onPickAspect(p.key)}
        >
          {p.label}
        </Button>
      ))}
      <span className="mx-1 h-4 w-px bg-border" />
      <Button
        size="sm"
        variant="ghost"
        onClick={() => esUpdate(client, { cropX: 0, cropY: 0, cropW: 1, cropH: 1 })}
        title="Reset crop to the full frame"
      >
        Reset
      </Button>
      <Button size="sm" onClick={() => esSetCropping(client, false)} title="Apply crop (Enter or R)">
        Done
      </Button>
    </div>
  );
}

// levelForPx picks the smallest rendition covering px device pixels; past
// pyramid depth the loupe switches to full-resolution tiles.
function levelForPx(px: number): Level | 'tiles' {
  for (const l of ['256', '512', '1024', '2048'] as const) {
    if (Number(l) >= px) return l;
  }
  return 'tiles';
}

// useTilePrefetch warms the photos adjacent to the focused one while the
// loupe is past pyramid depth: it pre-decodes their 2048 underlay (the
// bridge shown at the moment of a switch) and requests one tile, which makes
// the backend render the photo's whole tile set ahead of time — the visible
// tiles then come out of the local cache in tens of milliseconds. Underlay
// refs are held only for the current window so the browser can evict older
// decodes.
function useTilePrefetch(photos: Photo[], photo: Photo, active: boolean) {
  const held = useRef<Map<string, HTMLImageElement>>(new Map());
  const triggered = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!active) return;
    const i = photos.findIndex((p) => p.id === photo.id);
    if (i < 0) return;
    const ac = new AbortController();
    const next = new Map<string, HTMLImageElement>();
    for (const j of [i + 1, i - 1, i + 2]) {
      const p = photos[j];
      if (!p) continue;
      const url = imgUrl(p, '2048');
      const img = held.current.get(url) ?? new Image();
      if (!img.src) {
        img.src = url;
        img.decode().catch(() => {});
      }
      next.set(url, img);
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
  }, [photos, photo, active]);
}

function MainImage({ photo, photos }: { photo: Photo; photos: Photo[] }) {
  const client = useApiClient();
  const containerRef = useRef<HTMLDivElement>(null);
  const [container, setContainer] = useState<[number, number]>([0, 0]);
  const zoom = useUIStore((s) => s.loupeZoom);
  const setZoom = useUIStore((s) => s.setLoupeZoom);
  const preview = useEditSession((s) => s.preview);
  const wbPicking = useEditSession((s) => s.wbPicking);
  const cropping = useEditSession((s) => s.cropping);
  const draft = useEditSession((s) => s.draft);
  const esPhotoId = useEditSession((s) => s.photoId);
  // The crop that applies to the shown pixels: only when the edit session is
  // on this photo. While cropping, the loupe shows the full (uncropped) frame
  // so the overlay can reach the whole image.
  const activeCrop = esPhotoId === photo.id ? draft : null;
  const [aspectKey, setAspectKey] = useState('free');
  // Pan position as a ratio of the scrollable range — survives photo
  // switches so a burst series can be compared at the exact same crop.
  const panRatio = useRef<[number, number]>([0.5, 0.5]);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const update = () => setContainer([el.clientWidth, el.clientHeight]);
    const ro = new ResizeObserver(update);
    ro.observe(el);
    update();
    return () => ro.disconnect();
  }, []);

  const [fdw, fdh] = fullDisplayDims(photo);
  // When cropping we display the full frame; otherwise the cropped render.
  const [dw, dh] = cropping ? [fdw, fdh] : renderedDims(fdw, fdh, activeCrop);
  const haveDims = dw > 0 && dh > 0 && container[0] > 0;
  const fitScale = haveDims ? Math.min(container[0] / dw, container[1] / dh) : 1;
  const scale = zoom === 'fit' ? fitScale : zoom;
  const boxW = Math.max(1, Math.round(dw * scale));
  const boxH = Math.max(1, Math.round(dh * scale));

  // While an edit preview is active, show the JPEG the backend just pushed
  // over the WebSocket (rendered at 2048) instead of a cache URL.
  const previewUrl = preview && preview.photoId === photo.id ? preview.url : null;
  const level = levelForPx(Math.max(boxW, boxH) * window.devicePixelRatio);
  // Past pyramid depth the 2048 rendition stays on as an instantly-available
  // underlay stretched into the box, and TileLayer sharpens the visible
  // region with full-resolution tiles on top; neighbors are warmed so
  // stepping through a burst stays instant.
  const wantTiles = !previewUrl && level === 'tiles' && !cropping;
  const src = previewUrl ?? imgUrl(photo, level === 'tiles' ? '2048' : level);
  const [shownSrc, setShownSrc] = useState('');
  useTilePrefetch(photos, photo, wantTiles);

  // Restore the pan ratio whenever the geometry or photo changes.
  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    el.scrollLeft = panRatio.current[0] * Math.max(0, el.scrollWidth - el.clientWidth);
    el.scrollTop = panRatio.current[1] * Math.max(0, el.scrollHeight - el.clientHeight);
  }, [photo.id, boxW, boxH]);

  const onScroll = () => {
    const el = containerRef.current;
    if (!el) return;
    const rx = el.scrollWidth > el.clientWidth ? el.scrollLeft / (el.scrollWidth - el.clientWidth) : 0.5;
    const ry = el.scrollHeight > el.clientHeight ? el.scrollTop / (el.scrollHeight - el.clientHeight) : 0.5;
    panRatio.current = [rx, ry];
  };

  const onWheel = (e: React.WheelEvent) => {
    if (!e.ctrlKey) return;
    e.preventDefault();
    const cur = zoom === 'fit' ? fitScale : zoom;
    const next = Math.min(4, Math.max(0.05, cur * Math.exp(-e.deltaY * 0.0015)));
    const el = containerRef.current;
    if (el && haveDims) {
      // Anchor the image point under the cursor (map-style zoom): compute
      // the scroll that keeps it stationary at the new scale and hand it to
      // the pan-ratio restore that runs when the box resizes.
      const rect = el.getBoundingClientRect();
      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;
      const offX = Math.max(0, (el.clientWidth - dw * cur) / 2);
      const offY = Math.max(0, (el.clientHeight - dh * cur) / 2);
      const ix = (el.scrollLeft + mx - offX) / cur; // image px under the cursor
      const iy = (el.scrollTop + my - offY) / cur;
      const sx = ix * next - mx + Math.max(0, (el.clientWidth - dw * next) / 2);
      const sy = iy * next - my + Math.max(0, (el.clientHeight - dh * next) / 2);
      const maxX = Math.max(0, Math.round(dw * next) - el.clientWidth);
      const maxY = Math.max(0, Math.round(dh * next) - el.clientHeight);
      panRatio.current = [
        maxX > 0 ? Math.min(1, Math.max(0, sx / maxX)) : 0.5,
        maxY > 0 ? Math.min(1, Math.max(0, sy / maxY)) : 0.5,
      ];
    }
    setZoom(next);
  };

  // Click-drag pans the zoomed image; the pointer is captured so the drag
  // survives leaving the container. WB picking keeps plain clicks.
  const dragFrom = useRef<[number, number] | null>(null);
  const [dragging, setDragging] = useState(false);
  const pannable = haveDims && !cropping && (boxW > container[0] || boxH > container[1]);
  const onPointerDown = (e: React.PointerEvent) => {
    if (wbPicking || cropping || e.button !== 0 || !pannable) return;
    try {
      e.currentTarget.setPointerCapture(e.pointerId);
    } catch {
      // Capture only widens the drag beyond the container; a pointer that
      // can't be captured (synthetic test events) still pans.
    }
    dragFrom.current = [e.clientX, e.clientY];
    setDragging(true);
  };
  const onPointerMove = (e: React.PointerEvent) => {
    const el = containerRef.current;
    if (!dragFrom.current || !el) return;
    el.scrollLeft -= e.clientX - dragFrom.current[0];
    el.scrollTop -= e.clientY - dragFrom.current[1];
    dragFrom.current = [e.clientX, e.clientY];
  };
  const onPointerEnd = (e: React.PointerEvent) => {
    if (!dragFrom.current) return;
    dragFrom.current = null;
    setDragging(false);
    if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
  };

  // Selecting an aspect preset snaps the crop to the largest centered
  // rectangle of that ratio; Free leaves the current crop and only constrains
  // later handle drags.
  const applyAspect = (key: string) => {
    setAspectKey(key);
    const rf = aspectRatioFrac(key, fdw, fdh);
    if (!rf) return;
    let w = 1;
    let h = 1 / rf;
    if (h > 1) {
      h = 1;
      w = rf;
    }
    esUpdate(client, { cropX: (1 - w) / 2, cropY: (1 - h) / 2, cropW: w, cropH: h });
    esCommit(client);
  };

  const onImageClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if (!wbPicking) return;
    e.stopPropagation();
    const rect = e.currentTarget.getBoundingClientRect();
    const x = (e.clientX - rect.left) / rect.width;
    const y = (e.clientY - rect.top) / rect.height;
    void esPickWB(client, Math.min(1, Math.max(0, x)), Math.min(1, Math.max(0, y)));
  };

  return (
    <div className="relative min-h-0 flex-1">
      <div
        ref={containerRef}
        className={cn(
          'no-scrollbar flex size-full touch-none overflow-auto bg-black/40 select-none',
          wbPicking ? 'cursor-crosshair' : dragging ? 'cursor-grabbing' : pannable && 'cursor-grab',
        )}
        onScroll={onScroll}
        onWheel={onWheel}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerEnd}
        onPointerCancel={onPointerEnd}
        onDoubleClick={() => !wbPicking && setZoom(zoom === 'fit' ? 1 : 'fit')}
      >
        {haveDims ? (
          <div
            className="relative m-auto shrink-0"
            style={{ width: boxW, height: boxH }}
            onClick={onImageClick}
          >
            <DecodedImage src={src} onShown={setShownSrc} className="absolute inset-0 size-full" />
            {wantTiles && shownSrc.includes(`/img/${photo.id}/`) && (
              <TileLayer
                key={`${photo.id}|${photo.cacheKey}|${photo.editHash}`}
                photo={photo}
                dw={dw}
                dh={dh}
                boxW={boxW}
                boxH={boxH}
                container={containerRef}
              />
            )}
            {cropping && draft && (
              <CropOverlay
                draft={draft}
                ratioFrac={aspectRatioFrac(aspectKey, fdw, fdh)}
                onChange={(patch) => esUpdate(client, patch)}
                onCommit={() => esCommit(client)}
              />
            )}
          </div>
        ) : (
          // Metadata not scanned yet: plain fit rendering.
          <div className="m-auto" onClick={onImageClick}>
            <DecodedImage src={src} className="max-h-full max-w-full object-contain" />
          </div>
        )}
      </div>
      {wbPicking && (
        <div className="absolute top-3 left-1/2 -translate-x-1/2 rounded-md border bg-background/85 px-3 py-1 text-xs backdrop-blur">
          Click a neutral gray area to set white balance — Esc to cancel
        </div>
      )}
      {cropping ? (
        <CropToolbar client={client} aspectKey={aspectKey} onPickAspect={applyAspect} />
      ) : (
        <ZoomToolbar scale={scale} isFit={zoom === 'fit'} setZoom={setZoom} />
      )}
    </div>
  );
}

function ZoomToolbar({
  scale,
  isFit,
  setZoom,
}: {
  scale: number;
  isFit: boolean;
  setZoom: (z: 'fit' | number) => void;
}) {
  return (
    <div className="absolute bottom-3 left-1/2 flex -translate-x-1/2 items-center gap-2 rounded-md border bg-background/85 px-2 py-1 backdrop-blur">
      <Button size="sm" variant={isFit ? 'secondary' : 'ghost'} onClick={() => setZoom('fit')} title="Fit (Z or Space)">
        <Maximize data-icon="inline-start" />
        Fit
      </Button>
      <Button size="sm" variant={!isFit && Math.abs(scale - 1) < 0.01 ? 'secondary' : 'ghost'} onClick={() => setZoom(1)} title="100% (Z or Space)">
        <Square data-icon="inline-start" />
        1:1
      </Button>
      <Slider
        className="w-36"
        value={Math.round(scale * 100)}
        min={5}
        max={400}
        step={5}
        onValueChange={(v) => setZoom((v as number) / 100)}
        aria-label="Zoom"
      />
      <span className="w-10 text-right text-xs tabular-nums text-muted-foreground">
        {Math.round(scale * 100)}%
      </span>
    </div>
  );
}

// TileLayer sharpens the loupe past pyramid depth: the part of the image in
// the scrolled viewport (plus a margin) is covered with full-resolution
// TILE_SIZE tiles scaled into the display box, on top of the always-present
// 2048 underlay. Tiles accumulate for the component's lifetime, so panning
// back never re-fades — the caller keys this component by photo + edit
// state, so a switch starts from scratch. A tile a hair off the rendered
// image's edge 404s and simply stays hidden, leaving the underlay visible.
function TileLayer({
  photo,
  dw,
  dh,
  boxW,
  boxH,
  container,
}: {
  photo: Photo;
  dw: number; // rendered (crop-aware) display width
  dh: number;
  boxW: number;
  boxH: number;
  container: React.RefObject<HTMLDivElement | null>;
}) {
  const cols = Math.ceil(dw / TILE_SIZE);
  const rows = Math.ceil(dh / TILE_SIZE);
  const scale = boxW / dw;
  // Tile keys mounted so far.
  const [tiles, setTiles] = useState<string[]>([]);

  useEffect(() => {
    const el = container.current;
    if (!el) return;
    let raf = 0;
    const update = () => {
      raf = 0;
      // Viewport rect in image pixels. When the box is smaller than the
      // viewport it sits centered with no scroll range; the offset folds
      // that case into the same formula.
      const offX = Math.max(0, (el.clientWidth - boxW) / 2);
      const offY = Math.max(0, (el.clientHeight - boxH) / 2);
      const margin = TILE_SIZE / 2;
      const x0 = Math.max(0, Math.floor(((el.scrollLeft - offX) / scale - margin) / TILE_SIZE));
      const y0 = Math.max(0, Math.floor(((el.scrollTop - offY) / scale - margin) / TILE_SIZE));
      const x1 = Math.min(cols - 1, Math.floor(((el.scrollLeft - offX + el.clientWidth) / scale + margin) / TILE_SIZE));
      const y1 = Math.min(rows - 1, Math.floor(((el.scrollTop - offY + el.clientHeight) / scale + margin) / TILE_SIZE));
      setTiles((prev) => {
        const have = new Set(prev);
        const added: string[] = [];
        for (let ty = y0; ty <= y1; ty++) {
          for (let tx = x0; tx <= x1; tx++) {
            const k = `${tx},${ty}`;
            if (!have.has(k)) added.push(k);
          }
        }
        return added.length > 0 ? [...prev, ...added] : prev;
      });
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
  }, [container, scale, cols, rows, boxW, boxH]);

  return (
    <div className="pointer-events-none absolute inset-0 overflow-hidden">
      <div
        className="absolute top-0 left-0 origin-top-left"
        style={{ width: dw, height: dh, transform: `scale(${scale})` }}
      >
        {tiles.map((k) => {
          const [tx, ty] = k.split(',').map(Number);
          return <Tile key={k} src={tileUrl(photo, tx, ty)} left={tx * TILE_SIZE} top={ty * TILE_SIZE} />;
        })}
      </div>
    </div>
  );
}

// Tile renders at its natural size (the server decides edge-tile dimensions)
// and fades in once loaded; a 404 off the rendered edge stays invisible.
function Tile({ src, left, top }: { src: string; left: number; top: number }) {
  const [loaded, setLoaded] = useState(false);
  return (
    <img
      src={src}
      alt=""
      draggable={false}
      onLoad={() => setLoaded(true)}
      className="absolute max-w-none transition-opacity duration-150"
      style={{ left, top, opacity: loaded ? 1 : 0 }}
    />
  );
}

// DecodedImage double-buffers src changes: the new image decodes off-screen
// and swaps in only when ready, so photo switches, zoom-level upgrades, and
// slider drags never flash or show a misplaced small rendition. Until the
// decode lands, the previous src keeps filling the same box — at loupe depth
// that previous 2048 doubles as the bridge while tiles arrive. onShown
// reports every swap so the caller can keep overlays (the tile layer) in
// lockstep with what is actually displayed.
export function DecodedImage({
  src,
  className,
  onShown,
}: {
  src: string;
  className?: string;
  onShown?: (src: string) => void;
}) {
  const [shown, setShown] = useState(src);
  // Depends on onShown too: the caller may attach it after mount (the loupe
  // renders a bare fallback until its container is measured), and shown may
  // never change again after that.
  useEffect(() => {
    onShown?.(shown);
  }, [shown, onShown]);
  useEffect(() => {
    let alive = true;
    const img = new Image();
    img.src = src;
    img
      .decode()
      .then(() => alive && setShown(src))
      .catch(() => alive && setShown(src)); // decode() can reject spuriously; let <img> retry
    return () => {
      alive = false;
      // A superseded rendition still downloading is dead weight — abort it
      // so the server can cancel the render (holding the arrow key would
      // otherwise stack up a full develop per photo skimmed past).
      if (!img.complete) img.src = '';
    };
  }, [src]);
  return <img src={shown} draggable={false} alt="" className={className} />;
}

function Filmstrip({ photos, currentId }: { photos: Photo[]; currentId: number }) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const focus = useUIStore((s) => s.focus);
  const selection = useUIStore((s) => s.selection);
  const virtualizer = useVirtualizer({
    horizontal: true,
    count: photos.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 96,
    overscan: 10,
  });

  const currentIndex = photos.findIndex((p) => p.id === currentId);
  useEffect(() => {
    if (currentIndex >= 0) virtualizer.scrollToIndex(currentIndex);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentIndex]);

  return (
    <div ref={scrollRef} className="h-24 shrink-0 overflow-x-auto border-t">
      <div className="relative h-full" style={{ width: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((item) => {
          const p = photos[item.index];
          const selected = selection.has(p.id);
          return (
            <button
              key={p.id}
              className={cn(
                'absolute top-0 h-full p-1',
                p.id === currentId && 'bg-accent',
                selected && p.id !== currentId && 'bg-primary/20',
                p.flag === 'exclude' && 'opacity-40',
              )}
              style={{ left: item.start, width: 96 }}
              onClick={(e) => focus(p.id, { extend: e.shiftKey, toggle: e.ctrlKey || e.metaKey })}
            >
              <span className={cn('relative block size-full rounded-sm', selected && 'ring-2 ring-primary')}>
                <img
                  src={imgUrl(p, '256')}
                  alt={p.fileName}
                  draggable={false}
                  loading="lazy"
                  className="size-full rounded-sm object-contain"
                />
                <FilmstripBadges photo={p} />
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

// FilmstripBadges overlays the cull state on a thumbnail: rating stars at
// the bottom left, pick/exclude at the top right.
function FilmstripBadges({ photo }: { photo: Photo }) {
  if (photo.rating === 0 && photo.flag === 'none') return null;
  return (
    <>
      {photo.rating > 0 && (
        <span data-testid="strip-rating" className="absolute bottom-0.5 left-0.5 flex items-center gap-px rounded bg-black/60 px-1 py-px text-[10px] text-amber-400">
          {photo.rating}
          <Star className="size-2.5 fill-amber-400 text-amber-400" />
        </span>
      )}
      {photo.flag === 'pick' && (
        <span className="absolute top-0.5 right-0.5 rounded-full bg-black/60 p-0.5">
          <Check className="size-3 text-emerald-400" aria-label="Pick" />
        </span>
      )}
      {photo.flag === 'exclude' && (
        <span className="absolute top-0.5 right-0.5 rounded-full bg-black/60 p-0.5">
          <X className="size-3 text-red-400" aria-label="Excluded" />
        </span>
      )}
    </>
  );
}
