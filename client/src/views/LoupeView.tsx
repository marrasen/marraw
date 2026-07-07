import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Crop as CropIcon, Pipette, Star, Check, X } from 'lucide-react';
import type { Photo } from '@/api/library';
import { useApiClient, type ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Slider } from '@/components/ui/slider';
import { Segmented } from '@/components/ui/segmented';
import { ChipSpinner } from '@/components/ui/task-chip';
import { cn } from '@/lib/utils';
import { imgUrl, tileUrl, TILE_SIZE, type Level } from '@/lib/backend';
import { esClearPreview, esCommit, esPickWB, esSetCropping, esUpdate, useEditSession } from '@/lib/editSession';
import { useUIStore } from '@/stores/uiStore';
import { displayDims as fullDisplayDims, renderedDims, fitCropToRotation, ASPECT_PRESETS } from '@/lib/crop';
import { CropOverlay } from '@/components/CropOverlay';
import type { Params } from '@/api/edits';

// LoupeView is kept as the plain full-viewer wrapper (legacy view state);
// the cinema modes compose CinemaImage + their own floating chrome instead.
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
      <CinemaImage photo={photo} photos={photos} />
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

// CropBar: the glass control bar of the crop overlay — aspect presets, the
// bipolar Straighten dial, and Reset / Done (handoff plate "CROP").
function CropBar({
  client,
  aspectKey,
  angle,
  onPickAspect,
}: {
  client: ApiClient;
  aspectKey: string;
  angle: number;
  onPickAspect: (k: string) => void;
}) {
  const [dragging, setDragging] = useState<number | null>(null);
  const shown = dragging ?? angle;
  return (
    <div className="glass absolute bottom-4 left-1/2 z-40 flex -translate-x-1/2 items-center gap-3.5 rounded-[13px] px-4 py-2.5">
      <Segmented
        aria-label="Aspect ratio"
        size="sm"
        items={ASPECT_PRESETS.map((p) => ({ value: p.key, label: p.label }))}
        value={aspectKey}
        onValueChange={onPickAspect}
        className="border-0 bg-white/5"
      />
      <div className="h-[26px] w-px bg-white/15" />
      <div className="flex items-center gap-2.5">
        <span className="text-[11.5px] text-muted-foreground">Straighten</span>
        <Slider
          className="w-[150px]"
          value={shown}
          min={-15}
          max={15}
          step={0.1}
          fillFrom={0}
          aria-label="Straighten"
          onValueChange={(v) => {
            setDragging(v as number);
            esUpdate(client, { cropAngle: v as number });
          }}
          onValueCommitted={(v) => {
            setDragging(null);
            esCommit(client, { cropAngle: v as number });
          }}
        />
        <span className="w-[44px] text-right font-mono text-[11.5px] tabular-nums">
          {shown >= 0 ? '+' : ''}
          {shown.toFixed(1)}°
        </span>
      </div>
      <div className="h-[26px] w-px bg-white/15" />
      <Button
        size="sm"
        variant="ghost"
        className="text-muted-foreground"
        onClick={() => esUpdate(client, { cropX: 0, cropY: 0, cropW: 1, cropH: 1, cropAngle: 0 })}
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

// cropMemo remembers each photo's crop so returning to it sizes the loupe box
// correctly on the very first frame — before esLoad has refetched the draft —
// instead of briefly stretching the cropped rendition into a full-frame box.
const cropMemo = new Map<number, Params>();

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

// CinemaImage is the shared photo engine of every cinema surface: the
// pannable/zoomable image with tile sharpening, plus its own floating
// furniture (zoom bar, navigator, rendering indicator, crop overlay, WB
// eyedropper). `hideChrome` lets Cull swap the zoom furniture for its
// confirm bar while the photo sits at fit.
export function CinemaImage({
  photo,
  photos,
  hideChrome,
}: {
  photo: Photo;
  photos: Photo[];
  hideChrome?: boolean;
}) {
  const client = useApiClient();
  const containerRef = useRef<HTMLDivElement>(null);
  const [container, setContainer] = useState<[number, number]>([0, 0]);
  // Tiles mounted but not yet decoded — drives the rendering indicator.
  const [pendingTiles, setPendingTiles] = useState<[number, number]>([0, 0]); // [pending, loaded]
  // Visible-region fractions for the navigator inset.
  const [viewport, setViewport] = useState<[number, number, number, number]>([0, 0, 1, 1]);
  // Cursor position (fractions of the image box) while the WB pipette is up.
  const [cursor, setCursor] = useState<[number, number] | null>(null);
  const zoom = useUIStore((s) => s.loupeZoom);
  const setZoom = useUIStore((s) => s.setLoupeZoom);
  const preview = useEditSession((s) => s.preview);
  const wbPicking = useEditSession((s) => s.wbPicking);
  const cropping = useEditSession((s) => s.cropping);
  const draft = useEditSession((s) => s.draft);
  const esPhotoId = useEditSession((s) => s.photoId);
  // The crop that applies to the shown pixels: the draft when the edit session
  // is on this photo, else the last-known crop from the memo (so a return
  // doesn't flash uncropped while the draft refetches). While cropping, the
  // loupe shows the full (uncropped) frame so the overlay can reach the image.
  const liveCrop = esPhotoId === photo.id ? draft : null;
  if (liveCrop) cropMemo.set(photo.id, liveCrop);
  const activeCrop = liveCrop ?? cropMemo.get(photo.id) ?? null;
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

  // Once a commit lands (the photo's editHash changes to the newly rendered
  // state) AND we're past pyramid depth, drop the live 2048 preview so the
  // loupe shows the committed full-resolution tiles instead of the upscaled,
  // blurry preview blob that otherwise lingers until the next photo switch.
  // Below tile depth the 2048 preview and the committed rendition are the same
  // resolution, so keep it — clearing there would just cause a needless swap.
  const lastHash = useRef(photo.editHash);
  const levelRef = useRef(level);
  levelRef.current = level;
  useEffect(() => {
    if (photo.editHash !== lastHash.current) {
      lastHash.current = photo.editHash;
      if (levelRef.current !== 'tiles') return;
      const p = useEditSession.getState().preview;
      if (p && p.photoId === photo.id) esClearPreview();
    }
  }, [photo.editHash, photo.id]);

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

  // Navigator viewport: the visible region as fractions of the image box.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    let raf = 0;
    const update = () => {
      raf = 0;
      const vw = Math.min(1, el.clientWidth / boxW);
      const vh = Math.min(1, el.clientHeight / boxH);
      const vx = boxW > el.clientWidth ? el.scrollLeft / boxW : 0;
      const vy = boxH > el.clientHeight ? el.scrollTop / boxH : 0;
      setViewport([vx, vy, vw, vh]);
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
  }, [boxW, boxH]);

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
    // Shrink to the black-free region if a straighten angle is set.
    const fitted = fitCropToRotation(
      { x: (1 - w) / 2, y: (1 - h) / 2, w, h },
      draft?.cropAngle ?? 0,
      fdw / fdh,
    );
    esUpdate(client, { cropX: fitted.x, cropY: fitted.y, cropW: fitted.w, cropH: fitted.h });
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

  // Rendering indicator: tiles mounted but not decoded yet. The first jump
  // to 1:1 (no tile landed) also blurs the soft 2048 underlay so "still
  // resolving" can never read as "this shot is blurry".
  const rendering = wantTiles && pendingTiles[0] > 0;
  const firstDecode = rendering && pendingTiles[1] === 0;

  const onMagnifierMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!wbPicking) return;
    const rect = e.currentTarget.getBoundingClientRect();
    setCursor([(e.clientX - rect.left) / rect.width, (e.clientY - rect.top) / rect.height]);
  };

  return (
    <div className="relative min-h-0 flex-1 overflow-hidden bg-inset">
      <div
        ref={containerRef}
        className={cn(
          'no-scrollbar flex size-full touch-none overflow-auto select-none',
          wbPicking ? 'cursor-none' : dragging ? 'cursor-grabbing' : pannable && 'cursor-grab',
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
            className={cn('relative m-auto shrink-0', cropping && 'overflow-hidden bg-black')}
            style={{ width: boxW, height: boxH }}
            onClick={onImageClick}
            onPointerMove={onMagnifierMove}
            onPointerLeave={() => setCursor(null)}
            onContextMenu={(e) => {
              if (wbPicking) {
                e.preventDefault();
                useEditSession.setState({ wbPicking: false });
              }
            }}
          >
            <DecodedImage
              src={src}
              onShown={setShownSrc}
              className={cn(
                'absolute inset-0 size-full transition-[filter] duration-200',
                firstDecode && 'blur-[9px]',
              )}
              // While cropping, the straighten angle is a live client-side
              // rotation of the flat frame — instant, full-resolution feedback
              // — matched exactly by the backend crop on commit.
              style={cropping && draft ? { transform: `rotate(${draft.cropAngle}deg)` } : undefined}
            />
            {wantTiles && shownSrc.includes(`/img/${photo.id}/`) && (
              <TileLayer
                key={`${photo.id}|${photo.cacheKey}|${photo.editHash}`}
                photo={photo}
                dw={dw}
                dh={dh}
                boxW={boxW}
                boxH={boxH}
                container={containerRef}
                onProgress={(pending, loaded) => setPendingTiles([pending, loaded])}
              />
            )}
            {cropping && draft && (
              <CropOverlay
                draft={draft}
                ratioFrac={aspectRatioFrac(aspectKey, fdw, fdh)}
                frameAspect={fdw / fdh}
                pxDims={[fdw, fdh]}
                onChange={(patch) => esUpdate(client, patch)}
                onCommit={() => esCommit(client)}
              />
            )}
            {wbPicking && cursor && (
              <Magnifier src={shownSrc} boxW={boxW} boxH={boxH} cursor={cursor} />
            )}
          </div>
        ) : (
          // Metadata not scanned yet: plain fit rendering.
          <div className="m-auto" onClick={onImageClick}>
            <DecodedImage src={src} className="max-h-full max-w-full object-contain" />
          </div>
        )}
      </div>

      {/* Rendering full resolution: top progress line + centered badge. */}
      {rendering && (
        <div className="pointer-events-none absolute inset-x-0 top-0 z-40 h-0.5 bg-white/10">
          <div className="animate-chip-indeterminate absolute inset-y-0 w-1/3 bg-gradient-to-r from-transparent via-primary to-[#aab0ff]/0" />
        </div>
      )}
      {firstDecode && (
        <div className="glass pointer-events-none absolute top-1/2 left-1/2 z-40 flex -translate-x-1/2 -translate-y-1/2 items-center gap-3 rounded-xl px-[18px] py-[13px]">
          <ChipSpinner className="size-[19px]" />
          <div className="flex flex-col gap-0.5">
            <span className="text-[13.5px] font-semibold text-foreground">Rendering full resolution</span>
            <span className="font-mono text-[11px] text-muted-foreground">1:1 tile · decoding RAW</span>
          </div>
        </div>
      )}

      {/* Crop mode chip (top left, replaces the HUD status cluster). */}
      {cropping && (
        <div className="glass absolute top-4 left-[18px] z-40 flex items-center gap-2.5 rounded-[9px] px-3 py-[7px]">
          <CropIcon className="size-[13px] text-accent-text" strokeWidth={1.5} />
          <span className="text-[12.5px] font-semibold">Crop</span>
          <span className="font-mono text-[11px] text-muted-foreground">R to exit</span>
        </div>
      )}

      {/* WB eyedropper hint bar. */}
      {wbPicking && (
        <div className="glass absolute bottom-4 left-1/2 z-40 flex -translate-x-1/2 items-center gap-3 rounded-xl px-4 py-2.5 text-[12.5px]">
          <Pipette className="size-[15px] text-accent-text" strokeWidth={1.5} />
          <span className="text-secondary-foreground">Click a neutral gray to set white balance</span>
          <span className="h-4 w-px bg-white/15" />
          <span className="text-muted-foreground">
            Right-click resets · <span className="font-mono text-secondary-foreground">Esc</span> cancels
          </span>
        </div>
      )}

      {cropping ? (
        <CropBar
          client={client}
          aspectKey={aspectKey}
          angle={draft?.cropAngle ?? 0}
          onPickAspect={applyAspect}
        />
      ) : (
        !hideChrome &&
        !wbPicking && (
          <>
            <ZoomBar
              photos={photos}
              photo={photo}
              scale={scale}
              isFit={zoom === 'fit'}
              setZoom={setZoom}
              rendering={rendering}
            />
            <NavigatorInset photo={photo} scale={scale} viewport={viewport} isFit={zoom === 'fit'} />
          </>
        )
      )}
    </div>
  );
}

// Magnifier: the WB pipette's loupe — a 138px circle showing the pixels
// under the (hidden) cursor at 3×, with a pixel grid and an accent target.
function Magnifier({
  src,
  boxW,
  boxH,
  cursor,
}: {
  src: string;
  boxW: number;
  boxH: number;
  cursor: [number, number];
}) {
  const ZOOM = 3;
  const SIZE = 138;
  const [fx, fy] = cursor;
  return (
    <div
      className="pointer-events-none absolute z-40"
      style={{ left: fx * boxW - SIZE / 2, top: fy * boxH - SIZE / 2, width: SIZE, height: SIZE }}
    >
      <div className="absolute inset-0 overflow-hidden rounded-full border-2 border-white shadow-[0_12px_34px_-8px_rgba(0,0,0,.7)]">
        <img
          src={src}
          alt=""
          draggable={false}
          className="absolute max-w-none"
          style={{
            width: boxW * ZOOM,
            height: boxH * ZOOM,
            left: SIZE / 2 - fx * boxW * ZOOM,
            top: SIZE / 2 - fy * boxH * ZOOM,
          }}
        />
        <div
          className="absolute inset-0"
          style={{
            backgroundImage:
              'repeating-linear-gradient(0deg,rgba(0,0,0,.14) 0 1px,transparent 1px 14px),repeating-linear-gradient(90deg,rgba(0,0,0,.14) 0 1px,transparent 1px 14px)',
          }}
        />
        <div
          className="absolute top-1/2 left-1/2 size-3.5 -translate-x-1/2 -translate-y-1/2 border-[1.5px] border-primary"
          style={{ boxShadow: '0 0 0 1px rgba(0,0,0,.7)' }}
        />
      </div>
      <Pipette
        className="absolute size-[22px] text-white drop-shadow-[0_2px_4px_rgba(0,0,0,.6)]"
        style={{ left: SIZE - 20, top: SIZE - 22 }}
        strokeWidth={1.6}
      />
    </div>
  );
}

// NavigatorInset: the 200px glass minimap (bottom right) with the visible
// region outlined; click to recenter the pan there.
function NavigatorInset({
  photo,
  scale,
  viewport,
  isFit,
}: {
  photo: Photo;
  scale: number;
  viewport: [number, number, number, number];
  isFit: boolean;
}) {
  const [vx, vy, vw, vh] = viewport;
  const zoomed = vw < 0.999 || vh < 0.999;
  if (isFit && !zoomed) return null;
  return (
    <div className="glass absolute right-[18px] bottom-[18px] z-30 w-[200px] rounded-[11px] p-[9px]">
      <div className="mb-[7px] flex items-center justify-between">
        <span className="text-[10px] tracking-[.06em] text-muted-foreground uppercase">Navigator</span>
        <span className="font-mono text-[10.5px] text-accent-text tabular-nums">
          {Math.round(scale * 100)}%
        </span>
      </div>
      <div className="relative overflow-hidden rounded-md">
        <img src={imgUrl(photo, '256')} alt="" draggable={false} className="block w-full" />
        {zoomed && (
          <div
            className="absolute rounded-[2px] border-[1.5px] border-white"
            style={{
              left: `${vx * 100}%`,
              top: `${vy * 100}%`,
              width: `${vw * 100}%`,
              height: `${vh * 100}%`,
              boxShadow: '0 0 0 999px rgba(0,0,0,.34)',
            }}
          />
        )}
      </div>
    </div>
  );
}

// ZoomBar: the glass zoom control (handoff plate "LOUPE") — frame stepper,
// Fit/1:1 segmented, zoom slider + readout, and the rendering state while
// 1:1 tiles decode.
function ZoomBar({
  photos,
  photo,
  scale,
  isFit,
  setZoom,
  rendering,
}: {
  photos: Photo[];
  photo: Photo;
  scale: number;
  isFit: boolean;
  setZoom: (z: 'fit' | number) => void;
  rendering: boolean;
}) {
  const idx = photos.findIndex((p) => p.id === photo.id);
  const move = (delta: number) => {
    const next = photos[idx + delta];
    if (next) useUIStore.getState().focus(next.id);
  };
  return (
    <div className="glass absolute bottom-[18px] left-1/2 z-30 flex -translate-x-1/2 items-center gap-3.5 rounded-[13px] px-4 py-2.5">
      <div className="flex items-center gap-[7px] font-mono text-xs text-secondary-foreground">
        <button
          className="flex size-[26px] items-center justify-center rounded-[7px] border border-white/15 text-muted-foreground hover:text-foreground disabled:opacity-40"
          disabled={idx <= 0}
          onClick={() => move(-1)}
          aria-label="Previous photo"
        >
          ‹
        </button>
        <span className="tabular-nums">
          {(idx + 1).toLocaleString()} / {photos.length.toLocaleString()}
        </span>
        <button
          className="flex size-[26px] items-center justify-center rounded-[7px] border border-white/15 text-muted-foreground hover:text-foreground disabled:opacity-40"
          disabled={idx >= photos.length - 1}
          onClick={() => move(1)}
          aria-label="Next photo"
        >
          ›
        </button>
      </div>
      <div className="h-[26px] w-px bg-white/15" />
      <Segmented
        aria-label="Zoom mode"
        size="sm"
        items={[
          { value: 'fit', label: 'Fit' },
          { value: '1:1', label: '1:1' },
        ]}
        value={isFit ? 'fit' : '1:1'}
        onValueChange={(v) => setZoom(v === 'fit' ? 'fit' : 1)}
        className="border-0 bg-white/5"
      />
      <div className="flex items-center gap-2.5">
        <button className="text-[15px] text-muted-foreground" onClick={() => setZoom(Math.max(0.05, scale * 0.8))} aria-label="Zoom out">
          −
        </button>
        <Slider
          className="w-[150px]"
          value={Math.round(scale * 100)}
          min={5}
          max={400}
          step={5}
          onValueChange={(v) => setZoom((v as number) / 100)}
          aria-label="Zoom"
        />
        <button className="text-[15px] text-muted-foreground" onClick={() => setZoom(Math.min(4, scale * 1.25))} aria-label="Zoom in">
          +
        </button>
        {rendering ? (
          <span className="flex w-[110px] items-center gap-2 font-mono text-[11.5px] text-[#aab0ff]">
            <ChipSpinner className="size-[13px]" />
            Rendering {Math.round(scale * 100)}%
          </span>
        ) : (
          <span className="w-[42px] text-right font-mono text-[11.5px] tabular-nums">
            {Math.round(scale * 100)}%
          </span>
        )}
      </div>
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
  onProgress,
}: {
  photo: Photo;
  dw: number; // rendered (crop-aware) display width
  dh: number;
  boxW: number;
  boxH: number;
  container: React.RefObject<HTMLDivElement | null>;
  /** Reports (pending, loaded) tile counts for the rendering indicator. */
  onProgress?: (pending: number, loaded: number) => void;
}) {
  const cols = Math.ceil(dw / TILE_SIZE);
  const rows = Math.ceil(dh / TILE_SIZE);
  const scale = boxW / dw;
  // Tile keys mounted so far.
  const [tiles, setTiles] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(0);
  const onProgressRef = useRef(onProgress);
  onProgressRef.current = onProgress;
  useEffect(() => {
    onProgressRef.current?.(Math.max(0, tiles.length - loaded), loaded);
  }, [tiles.length, loaded]);
  // Component unmount (leaving 1:1 or switching photo) clears the indicator.
  useEffect(() => () => onProgressRef.current?.(0, 0), []);

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
          return (
            <Tile
              key={k}
              src={tileUrl(photo, tx, ty)}
              left={tx * TILE_SIZE}
              top={ty * TILE_SIZE}
              onSettled={() => setLoaded((n) => n + 1)}
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
  style,
  onShown,
}: {
  src: string;
  className?: string;
  style?: React.CSSProperties;
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
  return <img src={shown} draggable={false} alt="" className={className} style={style} />;
}

export function Filmstrip({ photos, currentId }: { photos: Photo[]; currentId: number }) {
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
    <div
      ref={scrollRef}
      data-testid="filmstrip"
      className="no-scrollbar h-16 w-[720px] max-w-[80vw] shrink-0 overflow-x-auto"
    >
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
