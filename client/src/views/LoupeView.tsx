import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Crop as CropIcon, FlipHorizontal2, FlipVertical2, Pipette, RotateCcwSquare, RotateCwSquare } from 'lucide-react';
import { onRenderProgressEvent, type Photo } from '@/api/library';
import { useApiClient, type ApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Slider } from '@/components/ui/slider';
import { Segmented } from '@/components/ui/segmented';
import { ChipSpinner } from '@/components/ui/task-chip';
import { PyramidImage } from '@/components/PyramidImage';
import { cn } from '@/lib/utils';
import { imgUrl, tileUrl, TILE_SIZE, type Level } from '@/lib/backend';
import {
  esClearPreview,
  esCommit,
  esPickWB,
  esPreviewSettled,
  esSetCropping,
  esUpdate,
  esWBPickAsShot,
  esWBPickAuto,
  esWBPickCancel,
  esWBPickDone,
  esWBPickReset,
  useEditSession,
} from '@/lib/editSession';
import { setLoupeNav } from '@/lib/loupeNav';
import { useUIStore } from '@/stores/uiStore';
import { displayDims as fullDisplayDims, renderedDims, rotatedDims, rotateCropPatch, flipCropPatch, fitCropToRotation, ASPECT_PRESETS } from '@/lib/crop';
import { CropOverlay } from '@/components/CropOverlay';
import { MaskOverlay } from '@/components/MaskOverlay';
import { MaskHoverTint } from '@/components/MaskHoverTint';
import type { Params } from '@/api/edit';

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
        <div className="w-[150px]">
          <Slider
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
        </div>
        <span className="w-[44px] text-right font-mono text-[11.5px] tabular-nums">
          {shown >= 0 ? '+' : ''}
          {shown.toFixed(1)}°
        </span>
      </div>
      <div className="h-[26px] w-px bg-white/15" />
      {/* Coarse rotation: remaps the rect so the same pixels stay selected;
          the flat frame re-renders through the ordinary preview path. */}
      <Button
        size="icon-sm"
        variant="ghost"
        className="text-muted-foreground"
        title="Rotate 90° counter-clockwise"
        aria-label="Rotate 90° counter-clockwise"
        onClick={() => {
          const d = useEditSession.getState().draft;
          if (!d) return;
          esUpdate(client, rotateCropPatch(d, 'ccw'));
          esCommit(client);
        }}
      >
        <RotateCcwSquare />
      </Button>
      <Button
        size="icon-sm"
        variant="ghost"
        className="text-muted-foreground"
        title="Rotate 90° clockwise"
        aria-label="Rotate 90° clockwise"
        onClick={() => {
          const d = useEditSession.getState().draft;
          if (!d) return;
          esUpdate(client, rotateCropPatch(d, 'cw'));
          esCommit(client);
        }}
      >
        <RotateCwSquare />
      </Button>
      <Button
        size="icon-sm"
        variant="ghost"
        className="text-muted-foreground"
        title="Flip horizontal"
        aria-label="Flip horizontal"
        onClick={() => {
          const d = useEditSession.getState().draft;
          if (!d) return;
          esUpdate(client, flipCropPatch(d, 'h'));
          esCommit(client);
        }}
      >
        <FlipHorizontal2 />
      </Button>
      <Button
        size="icon-sm"
        variant="ghost"
        className="text-muted-foreground"
        title="Flip vertical"
        aria-label="Flip vertical"
        onClick={() => {
          const d = useEditSession.getState().draft;
          if (!d) return;
          esUpdate(client, flipCropPatch(d, 'v'));
          esCommit(client);
        }}
      >
        <FlipVertical2 />
      </Button>
      <div className="h-[26px] w-px bg-white/15" />
      <Button
        size="sm"
        variant="ghost"
        className="text-muted-foreground"
        onClick={() => esUpdate(client, { rotate: 0, flipH: false, cropX: 0, cropY: 0, cropW: 1, cropH: 1, cropAngle: 0 })}
        title="Reset to the full, unrotated frame"
      >
        Reset
      </Button>
      <Button size="sm" onClick={() => esSetCropping(client, false)} title="Apply crop (Enter or R)">
        Done
      </Button>
    </div>
  );
}

// WBBar is the white-balance eyedropper toolbar, laid out like CropBar: a hint,
// the value-source shortcuts (As shot / Auto / Reset), then Cancel / Done.
// Clicking a neutral gray in the image previews a balance; Done keeps it as one
// history entry, Cancel/Reset restore the draft from when the picker opened.
function WBBar({ client }: { client: ApiClient }) {
  return (
    <div className="glass absolute bottom-4 left-1/2 z-40 flex -translate-x-1/2 items-center gap-3.5 rounded-[13px] px-4 py-2.5">
      <div className="flex items-center gap-2">
        <Pipette className="size-[15px] text-accent-text" strokeWidth={1.5} />
        <span className="text-[11.5px] text-muted-foreground">Click a neutral gray</span>
      </div>
      <div className="h-[26px] w-px bg-white/15" />
      <Button size="sm" variant="ghost" className="text-muted-foreground" onClick={() => esWBPickAsShot(client)} title="Camera as-shot white balance">
        As shot
      </Button>
      <Button size="sm" variant="ghost" className="text-muted-foreground" onClick={() => esWBPickAuto(client)} title="Auto white balance">
        Auto
      </Button>
      <Button size="sm" variant="ghost" className="text-muted-foreground" onClick={() => esWBPickReset(client)} title="Revert to the value before picking">
        Reset
      </Button>
      <div className="h-[26px] w-px bg-white/15" />
      <Button size="sm" variant="ghost" onClick={() => esWBPickCancel(client)} title="Discard and exit (Esc)">
        Cancel
      </Button>
      <Button size="sm" onClick={() => esWBPickDone(client)} title="Keep white balance (Enter)">
        Done
      </Button>
    </div>
  );
}

// cropMemo remembers each photo's crop so returning to it sizes the loupe box
// correctly on the very first frame — before esLoad has refetched the draft —
// instead of briefly stretching the cropped rendition into a full-frame box.
const cropMemo = new Map<number, Params>();

// Pan position as a ratio of the scrollable range — module scope so it
// survives photo switches (a burst series can be compared at the exact same
// crop) AND mode switches (Cull ⇄ Develop each remount CinemaImage; the zoom
// itself already survives in the uiStore).
const panRatio: { current: [number, number] } = { current: [0.5, 0.5] };

// slackFor pads the scroll range around the image box on one axis: full
// flush-to-flush travel when the box is smaller than the viewport, plus a
// 40%-of-viewport overscroll margin per side so the photo can always be
// pushed partly past an edge, away from overlaid chrome.
function slackFor(box: number, viewport: number): number {
  return Math.round(viewport * 0.4) + Math.max(0, viewport - box);
}

// levelForPx picks the smallest rendition covering px device pixels; past
// pyramid depth the loupe switches to full-resolution tiles.
function levelForPx(px: number): Level | 'tiles' {
  for (const l of ['256', '512', '1024', '2048'] as const) {
    if (Number(l) >= px) return l;
  }
  return 'tiles';
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
function useTilePrefetch(photos: Photo[], photo: Photo, active: boolean, tiles: boolean) {
  const held = useRef<Map<string, HTMLImageElement>>(new Map());
  const triggered = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!active) return;
    const i = photos.findIndex((p) => p.id === photo.id);
    if (i < 0) return;
    const ac = new AbortController();
    const next = new Map<string, HTMLImageElement>();
    const warm = (p: Photo, lvl: Level) => {
      const url = imgUrl(p, lvl);
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
      // The 512 underlay is cheap (warmed at scan, a disk hit) and keeps the
      // low-res bridge instant on arrow-through, so warm it for every neighbour.
      // The heavy 2048 is warmed ONLY at tile depth (1:1), where it is the
      // wanted bridge and browsing is deliberate: prefetching it in fit fires a
      // full RAW render per neighbour at visible priority, saturating the decode
      // pool and the browser's connection budget with long uncancellable renders
      // — that starves the warm 512s and freezes the browse. In fit the sharp
      // 2048 loads on demand for the focused photo only (plus the background
      // pre-render pass warming ahead).
      warm(p, '512');
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
  }, [photos, photo, active, tiles]);
}

// renderStage names what the backend is doing at a given progress fraction,
// mirroring pyramid.generate's budget: LibRaw decode 0–0.70, look/masks/
// detail 0.70–0.90, JPEG write-out from 0.90.
function renderStage(frac: number): string {
  if (frac < 0.72) return 'decoding RAW';
  if (frac < 0.92) return 'developing';
  return 'writing';
}

// useRenderProgress reports the backend's live render progress for the
// focused photo as a 0..1 fraction, or null before the first event — the
// decode's unpack phase reports nothing, so the indicator stays indeterminate
// until LibRaw's pipeline starts checkpointing. Subscribed only while the
// rendering indicator is up; events for other photos (background tile
// prefetch of neighbours) are ignored.
function useRenderProgress(client: ApiClient, photoId: number, active: boolean): number | null {
  const [frac, setFrac] = useState<number | null>(null);
  useEffect(() => {
    setFrac(null);
    if (!active) return;
    return onRenderProgressEvent(client, (ev) => {
      if (ev.photoId === photoId) setFrac(ev.fraction);
    });
  }, [client, photoId, active]);
  return frac;
}

// CinemaImage is the shared photo engine of every cinema surface: the
// pannable/zoomable image (over an accent-tinted radial backdrop) with tile
// sharpening, navigator inset, rendering indicator, and the crop / WB
// overlays. Zoom controls live in each mode's own control bar, fed through
// onZoomInfo.
export function CinemaImage({
  photo,
  photos,
  onZoomInfo,
  renderingBadgeBottom = 180,
  navigatorBottom = 18,
  showNavigator = true,
}: {
  photo: Photo;
  photos: Photo[];
  /** Reports the effective scale for embedded zoom UIs. */
  onZoomInfo?: (scale: number) => void;
  /** Bottom offset of the "Rendering full resolution" badge (above the mode's control bar). */
  renderingBadgeBottom?: number;
  navigatorBottom?: number;
  /**
   * Show the floating navigator inset over the canvas. Develop turns it off —
   * the always-visible drawer would cover it, so the Info tab hosts a
   * live navigator fed from the shared loupeNav store instead.
   */
  showNavigator?: boolean;
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
  const activeMask = useEditSession((s) => s.activeMask);
  const uiMode = useUIStore((s) => s.mode);
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
  // Crop-mode GEOMETRY (full-frame box, CSS rotation, overlay) is only valid
  // over the flat crop-stripped render — anything else (the committed
  // rendition, a stale non-flat preview) has the crop baked into its pixels,
  // and stretching it into the full-frame box + rotating it again is exactly
  // the "cropped before rotating" artifact. Until the flat frame arrives the
  // loupe keeps showing the ordinary cropped view; controls (chip, CropBar)
  // key off plain `cropping` so they appear instantly.
  const cropUI = cropping && !!preview && preview.photoId === photo.id && preview.flat;
  // The mask overlay edits the selected local adjustment on the ordinary
  // (cropped) Develop view — never during crop or WB picking, and only while
  // the edit session is on this photo.
  const maskUI =
    uiMode === 'develop' &&
    activeMask != null &&
    !cropping &&
    !wbPicking &&
    esPhotoId === photo.id &&
    !!draft?.masks?.[activeMask];
  // The hover tint needs no selected mask — hovering any Masks-panel row
  // shows that mask's weight over the ordinary Develop view.
  const tintUI = uiMode === 'develop' && !cropping && !wbPicking && esPhotoId === photo.id;

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const update = () => setContainer([el.clientWidth, el.clientHeight]);
    const ro = new ResizeObserver(update);
    ro.observe(el);
    update();
    return () => ro.disconnect();
  }, []);

  // React delegates wheel listeners as passive, so preventDefault in the
  // React onWheel below is silently ignored — a native non-passive listener
  // is the only way to actually suppress Chromium's default for ctrl+wheel
  // and trackpad pinch (which arrives as ctrl+wheel) over this surface.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const block = (ev: WheelEvent) => {
      if (ev.ctrlKey) ev.preventDefault();
    };
    el.addEventListener('wheel', block, { passive: false });
    return () => el.removeEventListener('wheel', block);
  }, []);

  const [fdw, fdh] = fullDisplayDims(photo);
  // The flat frame the crop overlay works against keeps the coarse rotation
  // (only the rect and straighten angle are stripped), so its box uses the
  // rotated dims.
  const [rfw, rfh] = rotatedDims(fdw, fdh, activeCrop);
  // Once the flat frame is up we display the full (rotated) frame; otherwise
  // the cropped render.
  const [dw, dh] = cropUI ? [rfw, rfh] : renderedDims(fdw, fdh, activeCrop);
  const haveDims = dw > 0 && dh > 0 && container[0] > 0;
  const fitScale = haveDims ? Math.min(container[0] / dw, container[1] / dh) : 1;
  const scale = zoom === 'fit' ? fitScale : zoom;

  // Zoom changes tween quickly instead of snapping: shownScale chases the
  // target over ~160ms (ease-out) and drives the displayed box, while the
  // rendition level, zoom readouts, and wheel math key off the target so no
  // intermediate pyramid levels get fetched. Photo switches and crop-mode
  // toggles snap — animating a size change between different frames reads
  // as the photo warping.
  const [shownScale, setShownScale] = useState(scale);
  const shownRef = useRef(scale);
  shownRef.current = shownScale;
  // cropUI (not cropping) so the box-size flip when the flat frame arrives
  // snaps instead of tweening between unrelated geometries.
  const snapKey = `${photo.id}|${cropUI}|${haveDims}`;
  const prevSnapKey = useRef('');
  // Only a deliberate zoom change (wheel, buttons, Z, double-click — every
  // path routes through setLoupeZoom, so `zoom` moves) should tween. A passive
  // fitScale recompute — the background metadata scan pushing a fresh photo
  // list with corrected dimensions/orientation, or a window resize — leaves
  // `zoom` at 'fit' and MUST snap: animating it reads as the photo spuriously
  // zooming in and springing back.
  const prevZoom = useRef(zoom);
  // Set on every wheel/pinch zoom (onWheel below) so the tween effect snaps
  // instead of easing for continuous input.
  const snapZoomRef = useRef(false);
  useEffect(() => {
    const snap = prevSnapKey.current !== snapKey;
    prevSnapKey.current = snapKey;
    const zoomChanged = prevZoom.current !== zoom;
    prevZoom.current = zoom;
    // Wheel/pinch zoom snaps: it's continuous input, and easing a moving target
    // both lags visibly and (because onWheel anchors off the mid-tween shownScale)
    // makes the cursor-anchor math wobble the faster you scroll. Only deliberate
    // keyboard/button/double-click steps tween.
    const wheelSnap = snapZoomRef.current;
    snapZoomRef.current = false;
    if (wheelSnap || snap || !zoomChanged || !haveDims || Math.abs(scale - shownRef.current) < 1e-4) {
      setShownScale(scale);
      return;
    }
    const from = shownRef.current;
    const start = performance.now();
    const DURATION = 160;
    let raf = requestAnimationFrame(function tick(t) {
      const p = Math.min(1, (t - start) / DURATION);
      const eased = 1 - Math.pow(1 - p, 3);
      setShownScale(from + (scale - from) * eased);
      if (p < 1) raf = requestAnimationFrame(tick);
    });
    return () => cancelAnimationFrame(raf);
  }, [scale, snapKey, haveDims, zoom]);

  const boxW = Math.max(1, Math.round(dw * shownScale));
  const boxH = Math.max(1, Math.round(dh * shownScale));
  // Slack pads the scrollable content so the photo can be dragged clear of
  // overlaid chrome (develop drawer, control bars) at ANY zoom: per axis it
  // allows flush-to-flush travel when the box fits the viewport, plus an
  // overscroll margin past the flush edges — that margin is what lets a
  // fit-height photo move up away from the decks. The default panRatio of
  // 0.5 keeps the photo centered. Crop mode pins the frame centered.
  const slackX = cropping ? 0 : slackFor(boxW, container[0]);
  const slackY = cropping ? 0 : slackFor(boxH, container[1]);

  // Entering crop at Fit leaves the frame flush against the container, so the
  // edge/corner handles sit on the window border where the OS claims the
  // pointer for window resizing (or exits fullscreen). Step the zoom out once
  // (the keyboard's 0.8 factor) so every handle has grabbable margin — sized
  // against the FLAT frame's fit scale, since that's the geometry crop mode
  // shows and its fit can be smaller than the cropped frame's. If the user
  // came from Fit, leaving crop returns there.
  const cropWasFit = useRef(false);
  const prevCropping = useRef(cropping);
  useEffect(() => {
    if (cropping === prevCropping.current) return;
    prevCropping.current = cropping;
    if (cropping) {
      cropWasFit.current = zoom === 'fit';
      if (cropWasFit.current && rfw > 0 && rfh > 0 && container[0] > 0 && container[1] > 0) {
        setZoom(Math.min(container[0] / rfw, container[1] / rfh) * 0.8);
      }
    } else if (cropWasFit.current) {
      cropWasFit.current = false;
      setZoom('fit');
    }
  }, [cropping, zoom, rfw, rfh, container, setZoom]);

  // While an edit preview is active, show the JPEG the backend just pushed
  // over the WebSocket instead of a cache URL — but only when its geometry
  // matches the mode: flat frames belong to crop mode, cropped renders to
  // normal viewing. A flat blob kept after Done (or one landing late from an
  // in-flight render) is gated out and src falls back to the committed
  // rendition, whose crop is already baked in.
  const previewUrl =
    preview && preview.photoId === photo.id && preview.flat === cropping ? preview.url : null;
  // Rendition level from the TARGET scale, not the animating one, so a zoom
  // tween never requests the pyramid levels it passes through.
  const level = levelForPx(Math.max(dw, dh) * scale * window.devicePixelRatio);
  // Past pyramid depth the 2048 rendition stays on as an instantly-available
  // underlay stretched into the box, and TileLayer sharpens the visible
  // region with full-resolution tiles on top; neighbors are warmed so
  // stepping through a burst stays instant.
  const wantTiles = !previewUrl && level === 'tiles' && !cropping;
  const src = previewUrl ?? imgUrl(photo, level === 'tiles' ? '2048' : level);
  // The pyramid level fit displays (never 'tiles' in the fit branch below).
  const fitLevel: Level = level === 'tiles' ? '2048' : level;
  const [shownSrc, setShownSrc] = useState('');
  // Warm neighbours whenever the loupe is showing committed renditions (fit
  // included — that path has no tile layer to bridge a cold 2048); fire the
  // full-res tile trigger only past pyramid depth.
  useTilePrefetch(photos, photo, !previewUrl && !cropping && haveDims, wantTiles);

  // Once a commit lands (the photo's editHash changes to the newly rendered
  // state) AND we're past pyramid depth, drop the live 2048 preview so the
  // loupe shows the committed full-resolution tiles instead of the upscaled,
  // blurry preview blob that otherwise lingers until the next photo switch.
  // Below tile depth the 2048 preview and the committed rendition are the same
  // resolution, so keep it — clearing there would just cause a needless swap.
  // Crop mode keeps the preview no matter what: mid-crop commits (straighten
  // release) bump the hash too, and evicting the flat frame would swap in the
  // committed crop-baked rendition under the overlay. The exit commit changes
  // the hash again with cropping already false, so the normal clear still runs.
  const lastHash = useRef(photo.editHash);
  const levelRef = useRef(level);
  levelRef.current = level;
  useEffect(() => {
    if (photo.editHash !== lastHash.current) {
      lastHash.current = photo.editHash; // always consume the hash advance
      if (levelRef.current !== 'tiles') return;
      if (useEditSession.getState().cropping) return;
      // A one-shot auto/preset apply advances the hash immediately (its persist)
      // while the sharp render is still queued or in flight — evicting here
      // would drop the instant low-res blob and fall back to a committed
      // rendition that isn't rendered yet. Keep the preview until the session
      // settles; the settled clear below then takes over once the 2048 lands.
      if (!esPreviewSettled()) return;
      const p = useEditSession.getState().preview;
      if (p && p.photoId === photo.id) esClearPreview();
    }
  }, [photo.editHash, photo.id]);

  // The hash-change clear above only fires while ALREADY at tile depth —
  // committing at a lower zoom and zooming to 1:1 afterwards left the 2048
  // preview blob up forever: it gates the tile layer off, so full resolution
  // never rendered and no indicator showed. Once the session is settled
  // (draft == committed, nothing rendering) the blob adds nothing over the
  // committed renditions — drop it so the tiles take over. Mid-drag draft
  // previews stay: tiles would regress to committed pixels under the pointer.
  const esRendering = useEditSession((s) => s.rendering);
  useEffect(() => {
    if (level !== 'tiles' || cropping) return;
    const p = useEditSession.getState().preview;
    if (p && p.photoId === photo.id && esPreviewSettled()) esClearPreview();
  }, [level, cropping, photo.id, preview, esRendering]);

  // Restore the pan ratio whenever the geometry or photo changes. `container`
  // must be a dep: at a numeric zoom boxW is container-independent, so on a
  // fresh mount (mode switch) it is already final while the container still
  // measures 0×0 — without it the restore never re-runs after measurement and
  // the view sticks at the top-left corner. slackX/slackY must be deps too:
  // leaving crop mode restores the pan slack around an UNCHANGED box when the
  // edit was straighten-only (the angle never changes the output size), and
  // without a re-run the scroll stays at crop mode's zero — the slack padding
  // then shows as the photo shoved far right and down.
  // Keyboard-pan tween state (see the loupePan effect below). Any effect that
  // writes the scroll position directly must cancel an in-flight tween first,
  // or the next frames would drag the view back toward a stale target.
  const panTweenRaf = useRef(0);
  const panTweenTarget = useRef<[number, number] | null>(null);
  const cancelPanTween = () => {
    cancelAnimationFrame(panTweenRaf.current);
    panTweenTarget.current = null;
  };
  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    cancelPanTween();
    el.scrollLeft = panRatio.current[0] * Math.max(0, el.scrollWidth - el.clientWidth);
    el.scrollTop = panRatio.current[1] * Math.max(0, el.scrollHeight - el.clientHeight);
  }, [photo.id, boxW, boxH, slackX, slackY, container]);

  // Any return to fit recenters — as a glide, not a snap: the pan ratio eases
  // to the middle with the same duration/curve as the zoom tween, so a photo
  // panned away at 1:1 travels home WHILE it shrinks instead of jumping to
  // center first. Each frame writes the scroll from the live ranges, which is
  // the same formula the pan-ratio restore applies when the zoom tween resizes
  // the box — the two writers agree, so they compose instead of fighting.
  const centerTweenRaf = useRef(0);
  const cancelCenterTween = () => cancelAnimationFrame(centerTweenRaf.current);
  const centerTick = useUIStore((s) => s.loupeCenterTick);
  const lastCenterTick = useRef(centerTick);
  useLayoutEffect(() => {
    if (centerTick === lastCenterTick.current) return;
    lastCenterTick.current = centerTick;
    const el = containerRef.current;
    if (!el) {
      panRatio.current = [0.5, 0.5];
      return;
    }
    cancelPanTween();
    cancelCenterTween();
    const apply = () => {
      el.scrollLeft = panRatio.current[0] * Math.max(0, el.scrollWidth - el.clientWidth);
      el.scrollTop = panRatio.current[1] * Math.max(0, el.scrollHeight - el.clientHeight);
    };
    const [fx, fy] = panRatio.current;
    if (Math.abs(fx - 0.5) < 1e-3 && Math.abs(fy - 0.5) < 1e-3) {
      panRatio.current = [0.5, 0.5];
      apply();
      return;
    }
    const start = performance.now();
    const DURATION = 160;
    centerTweenRaf.current = requestAnimationFrame(function tick(t) {
      const p = Math.min(1, (t - start) / DURATION);
      const eased = 1 - Math.pow(1 - p, 3);
      panRatio.current = [fx + (0.5 - fx) * eased, fy + (0.5 - fy) * eased];
      apply();
      if (p < 1) centerTweenRaf.current = requestAnimationFrame(tick);
    });
    return cancelCenterTween;
  }, [centerTick]);

  const onScroll = () => {
    const el = containerRef.current;
    if (!el) return;
    const rx = el.scrollWidth > el.clientWidth ? el.scrollLeft / (el.scrollWidth - el.clientWidth) : 0.5;
    const ry = el.scrollHeight > el.clientHeight ? el.scrollTop / (el.scrollHeight - el.clientHeight) : 0.5;
    panRatio.current = [rx, ry];
  };

  // Shift+arrow pan: keyboard.ts accumulates viewport-fraction nudges in the
  // store (this component owns the scroll container, the keymap doesn't);
  // applying the not-yet-consumed difference means batched keydowns can't
  // drop a press. Each nudge eases toward its destination with the same
  // 160 ms ease-out cubic as the zoom tween; the target accumulates across
  // presses (base = in-flight target, not current scroll) so held-key repeats
  // keep pushing it ahead while the tween chases — a glide, never a lost
  // press. onScroll runs every frame so panRatio and the navigator stay
  // truthful. In crop mode slack is 0, so the nudge naturally no-ops.
  const loupePan = useUIStore((s) => s.loupePan);
  const consumedPan = useRef(loupePan);
  useLayoutEffect(() => {
    const [dx, dy] = [loupePan[0] - consumedPan.current[0], loupePan[1] - consumedPan.current[1]];
    consumedPan.current = loupePan;
    if (dx === 0 && dy === 0) return;
    const el = containerRef.current;
    if (!el) return;
    cancelCenterTween(); // a fresh keyboard pan takes over from a recenter glide
    const base = panTweenTarget.current ?? [el.scrollLeft, el.scrollTop];
    const target: [number, number] = [base[0] + dx * el.clientWidth, base[1] + dy * el.clientHeight];
    panTweenTarget.current = target;
    const from: [number, number] = [el.scrollLeft, el.scrollTop];
    const start = performance.now();
    const DURATION = 160;
    cancelAnimationFrame(panTweenRaf.current);
    panTweenRaf.current = requestAnimationFrame(function tick(t) {
      const p = Math.min(1, (t - start) / DURATION);
      const eased = 1 - Math.pow(1 - p, 3);
      el.scrollLeft = from[0] + (target[0] - from[0]) * eased;
      el.scrollTop = from[1] + (target[1] - from[1]) * eased;
      onScroll();
      if (p < 1) panTweenRaf.current = requestAnimationFrame(tick);
      else panTweenTarget.current = null;
    });
  }, [loupePan]);

  // Navigator viewport: the visible region as fractions of the image box.
  // With slack the box edge sits slackX from the scroll origin and the photo
  // can be partly off-screen in any direction, so the visible span is the
  // viewport ∩ box intersection.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    let raf = 0;
    const update = () => {
      raf = 0;
      const posX = slackX - el.scrollLeft; // box left edge in viewport coords
      const posY = slackY - el.scrollTop;
      const x0 = Math.max(0, -posX);
      const y0 = Math.max(0, -posY);
      const x1 = Math.min(boxW, el.clientWidth - posX);
      const y1 = Math.min(boxH, el.clientHeight - posY);
      setViewport([x0 / boxW, y0 / boxH, Math.max(0, x1 - x0) / boxW, Math.max(0, y1 - y0) / boxH]);
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
  }, [boxW, boxH, slackX, slackY]);

  // Wheel-zoom base: a trackpad pinch delivers wheel events faster than React
  // re-renders, so deriving each step from the render-closure `scale` reads
  // the same stale base several times per frame — most of the gesture is
  // dropped and the zoom crawls, then jumps. The freshest target lives on a
  // ref; the render-time sync keeps external zoom changes (buttons, fit, Z)
  // authoritative, and is safe because setZoom lands in the store
  // synchronously — any later render already reads the accumulated value.
  const wheelZoomRef = useRef(scale);
  wheelZoomRef.current = scale;
  const onWheel = (e: React.WheelEvent) => {
    if (!e.ctrlKey) return;
    cancelCenterTween(); // wheel zoom owns panRatio (cursor anchor) from here
    const next = Math.min(4, Math.max(0.05, wheelZoomRef.current * Math.exp(-e.deltaY * 0.003)));
    wheelZoomRef.current = next;
    const el = containerRef.current;
    if (el && haveDims) {
      // Anchor the image point under the cursor (map-style zoom): compute
      // the scroll that keeps it stationary at the new scale and hand it to
      // the pan-ratio restore that runs when the box resizes. The cursor is
      // mapped through the currently RENDERED layout (shownScale + slack, a
      // tween may be mid-flight); the destination through the target.
      const rect = el.getBoundingClientRect();
      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;
      const ix = (el.scrollLeft + mx - slackX) / shownScale; // image px under the cursor
      const iy = (el.scrollTop + my - slackY) / shownScale;
      const bw = Math.round(dw * next);
      const bh = Math.round(dh * next);
      const nSlackX = slackFor(bw, el.clientWidth);
      const nSlackY = slackFor(bh, el.clientHeight);
      const sx = ix * next - mx + nSlackX;
      const sy = iy * next - my + nSlackY;
      const maxX = bw + 2 * nSlackX - el.clientWidth;
      const maxY = bh + 2 * nSlackY - el.clientHeight;
      panRatio.current = [
        maxX > 0 ? Math.min(1, Math.max(0, sx / maxX)) : 0.5,
        maxY > 0 ? Math.min(1, Math.max(0, sy / maxY)) : 0.5,
      ];
    }
    snapZoomRef.current = true;
    setZoom(next);
  };

  // Click-drag pans the zoomed image; the pointer is captured so the drag
  // survives leaving the container. WB picking keeps plain clicks.
  const dragFrom = useRef<[number, number] | null>(null);
  const [dragging, setDragging] = useState(false);
  // Slack means there is nearly always somewhere to drag the photo, zoomed
  // in or not — only crop mode pins it.
  const pannable = haveDims && !cropping;
  const onPointerDown = (e: React.PointerEvent) => {
    if (wbPicking || cropping || e.button !== 0 || !pannable) return;
    try {
      e.currentTarget.setPointerCapture(e.pointerId);
    } catch {
      // Capture only widens the drag beyond the container; a pointer that
      // can't be captured (synthetic test events) still pans.
    }
    cancelPanTween(); // grabbing the photo mid-glide wins over the keyboard tween
    cancelCenterTween();
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
    const rf = aspectRatioFrac(key, rfw, rfh);
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
      rfw / rfh,
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

  // Rendering indicator: tiles mounted but not decoded yet.
  const rendering = wantTiles && pendingTiles[0] > 0;

  // Photo-switch indicator: the shown pixels still belong to another photo.
  // Rapid culling outruns the decode pipeline and the double buffer keeps the
  // previous frame up instead of flashing — correct, but without a signal it
  // reads as the app freezing. Any preview blob counts as current while the
  // edit session's preview is on this photo (drag frames land every few tens
  // of milliseconds; flagging the swap gaps would blink the badge non-stop).
  const showsCurrent =
    shownSrc !== '' &&
    (shownSrc.includes(`/img/${photo.id}/`) ||
      (shownSrc.startsWith('blob:') && preview != null && preview.photoId === photo.id));
  const loadingPhoto = haveDims && shownSrc !== '' && !showsCurrent;
  const busy = rendering || loadingPhoto;
  // Live progress for whichever render the chip is waiting on: 1:1 tiles OR
  // an interactive level render during a photo switch — the server reports
  // both (fixed levels only at visible/interactive priority).
  const renderFrac = useRenderProgress(client, photo.id, busy);

  // Parents embed the zoom cluster in their own control bars.
  const onZoomInfoRef = useRef(onZoomInfo);
  onZoomInfoRef.current = onZoomInfo;
  useEffect(() => {
    onZoomInfoRef.current?.(scale);
  }, [scale]);

  // Mirror the fit scale into the store: +/- zoom steps in keyboard.ts start
  // from it while the zoom is 'fit'. Guarded on haveDims so the placeholder
  // 1 never overwrites a real value.
  useEffect(() => {
    if (haveDims) useUIStore.getState().setLoupeFitScale(fitScale);
  }, [fitScale, haveDims]);

  // Navigator drag / click pans the viewport to the pointed-at fraction.
  const panTo = (fx: number, fy: number) => {
    const el = containerRef.current;
    if (!el) return;
    el.scrollLeft = slackX + fx * boxW - el.clientWidth / 2;
    el.scrollTop = slackY + fy * boxH - el.clientHeight / 2;
  };

  // Publish live pan/zoom to the shared store so an off-canvas navigator (the
  // Develop Info tab) can mirror the viewport and drive panning. panTo closes
  // over the current geometry, so register a stable wrapper backed by a ref to
  // the latest closure and clear it on unmount.
  const panToRef = useRef(panTo);
  panToRef.current = panTo;
  useEffect(() => {
    setLoupeNav({ panTo: (fx, fy) => panToRef.current(fx, fy) });
    return () => setLoupeNav({ panTo: null });
  }, []);
  useEffect(() => {
    setLoupeNav({ viewport, scale, isFit: zoom === 'fit' });
  }, [viewport, scale, zoom]);

  const onMagnifierMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!wbPicking) return;
    const rect = e.currentTarget.getBoundingClientRect();
    setCursor([(e.clientX - rect.left) / rect.width, (e.clientY - rect.top) / rect.height]);
  };

  // Live RGB readout under the pipette.
  const sample = usePixelSampler(shownSrc, wbPicking);
  const sampledRGB = (() => {
    if (!wbPicking || !cursor || !sample) return null;
    const x = Math.min(sample.width - 1, Math.max(0, Math.round(cursor[0] * sample.width)));
    const y = Math.min(sample.height - 1, Math.max(0, Math.round(cursor[1] * sample.height)));
    const i = (y * sample.width + x) * 4;
    return [sample.data[i], sample.data[i + 1], sample.data[i + 2]] as const;
  })();

  return (
    <div
      className="relative min-h-0 flex-1 overflow-hidden"
      style={{
        background:
          'radial-gradient(120% 90% at 50% 38%, color-mix(in oklch, var(--primary) 14%, var(--background)), var(--background) 68%)',
      }}
    >
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
          // The slack wrapper is the actual scroll content: the box centered
          // in it, with room to drag the photo to any viewport edge.
          <div
            className="m-auto flex shrink-0 items-center justify-center"
            style={{ width: boxW + 2 * slackX, height: boxH + 2 * slackY }}
          >
          <div
            className={cn('relative shrink-0', cropUI && 'overflow-hidden bg-black')}
            style={{ width: boxW, height: boxH }}
            onClick={onImageClick}
            onPointerMove={onMagnifierMove}
            onPointerLeave={() => setCursor(null)}
            onContextMenu={(e) => {
              if (wbPicking) {
                e.preventDefault();
                esWBPickReset(client); // revert picks, stay in the picker
              }
            }}
          >
            {/* Low-res bridge: the always-warm 512 fills the same box and, as a
                double-buffered DecodedImage, holds the previous frame until the
                new 512 decodes — never transparent, so a fast browse never
                blinks through to nothing. It sits BEHIND the sharp layer; the
                sharp only covers it once it actually has the current photo's
                pixels. Off during crop (rotated flat frame) and live edit (the
                blob is the truth). */}
            {!cropUI && !previewUrl && (
              // stale: a photo whose current-hash renders were never written
              // (superseded settle, janitor eviction) must still show the
              // RIGHT photo instantly — at a previous edit state if need be —
              // instead of holding the previous photo while a decode runs.
              <DecodedImage src={imgUrl(photo, '512', { stale: true })} className="absolute inset-0 size-full" />
            )}
            {!cropUI && !previewUrl && !wantTiles ? (
              // Fit: show the pre-rendered rendition the instant it exists and
              // NEVER trigger an on-demand render just to browse — a not-yet-
              // pre-rendered photo paints the 512 above with no stall. Keyed on
              // the photo so a switch starts fresh (no lingering previous frame,
              // nothing mid-decode to cancel).
              <FitImage
                key={`${photo.id}|${photo.editHash}`}
                photo={photo}
                level={fitLevel}
                onShown={setShownSrc}
                className="absolute inset-0 size-full"
              />
            ) : (
              <DecodedImage
                src={src}
                onShown={setShownSrc}
                className="absolute inset-0 size-full"
                // While cropping, the straighten angle is a live client-side
                // rotation of the flat frame — instant, full-resolution feedback
                // — matched exactly by the backend crop on commit. Gated on the
                // flat frame actually being up: rotating a crop-baked render
                // would rotate the crop twice.
                style={cropUI && draft ? { transform: `rotate(${draft.cropAngle}deg)` } : undefined}
              />
            )}
            {wantTiles && shownSrc.includes(`/img/${photo.id}/`) && (
              <TileLayer
                key={`${photo.id}|${photo.cacheKey}|${photo.editHash}`}
                photo={photo}
                dw={dw}
                dh={dh}
                boxW={boxW}
                slackX={slackX}
                slackY={slackY}
                container={containerRef}
                onProgress={(pending, loaded) => setPendingTiles([pending, loaded])}
              />
            )}
            {cropUI && draft && (
              <CropOverlay
                draft={draft}
                ratioFrac={aspectRatioFrac(aspectKey, rfw, rfh)}
                frameAspect={rfw / rfh}
                pxDims={[rfw, rfh]}
                onChange={(patch) => esUpdate(client, patch)}
                onCommit={() => esCommit(client)}
              />
            )}
            {tintUI && draft && (
              <MaskHoverTint
                draft={draft}
                frameW={rfw}
                frameH={rfh}
                boxW={boxW}
                boxH={boxH}
              />
            )}
            {maskUI && draft && (
              <MaskOverlay
                client={client}
                draft={draft}
                frameW={rfw}
                frameH={rfh}
                boxW={boxW}
                boxH={boxH}
              />
            )}
            {wbPicking && cursor && (
              <Magnifier src={shownSrc} boxW={boxW} boxH={boxH} cursor={cursor} rgb={sampledRGB} />
            )}
          </div>
          </div>
        ) : (
          // Metadata not scanned yet: plain fit rendering.
          <div className="m-auto" onClick={onImageClick}>
            <DecodedImage src={src} className="max-h-full max-w-full object-contain" />
          </div>
        )}
      </div>

      {/* Rendering / loading: top progress line + centered badge. Both stay
          mounted and fade instead of popping in and out; the fade-in delay
          swallows tile loads and photo swaps that finish near-instantly, so
          stepping through a warm burst never blinks the chrome. */}
      <div
        className={cn(
          'pointer-events-none absolute inset-x-0 top-0 z-40 h-0.5 bg-white/10 transition-opacity duration-200',
          busy ? 'opacity-100 delay-150' : 'opacity-0 delay-0',
        )}
      >
        {renderFrac != null ? (
          // Determinate: the backend streams render progress (1:1 tiles and
          // interactive level renders alike).
          <div
            className="absolute inset-y-0 left-0 bg-gradient-to-r from-primary/50 to-primary transition-[width] duration-150 ease-linear"
            style={{ width: `${Math.round(renderFrac * 100)}%` }}
          />
        ) : (
          <div className="animate-chip-indeterminate absolute inset-y-0 w-1/3 bg-gradient-to-r from-transparent via-primary to-[#aab0ff]/0" />
        )}
      </div>
      <div
        className={cn(
          'glass pointer-events-none absolute left-1/2 z-40 flex -translate-x-1/2 items-center gap-3 rounded-xl px-[18px] py-[13px] transition-opacity duration-200',
          busy ? 'opacity-100 delay-150' : 'opacity-0 delay-0',
        )}
        style={{ bottom: renderingBadgeBottom }}
      >
        <ChipSpinner className="size-[19px]" />
        <div className="flex flex-col gap-0.5">
          <span className="text-[13.5px] font-semibold text-foreground">
            {loadingPhoto ? 'Loading photo' : 'Rendering full resolution'}
          </span>
          <span className="font-mono text-[11px] text-muted-foreground">
            {loadingPhoto
              ? renderFrac != null
                ? `${renderStage(renderFrac)} · ${Math.round(renderFrac * 100)}%`
                : 'decoding RAW preview'
              : renderFrac != null
                ? `1:1 tile · ${renderStage(renderFrac)} · ${Math.round(renderFrac * 100)}%`
                : '1:1 tile · decoding RAW'}
          </span>
        </div>
      </div>

      {/* Crop mode chip (top left, replaces the HUD status cluster). */}
      {cropping && (
        <div className="glass absolute top-4 left-[18px] z-40 flex items-center gap-2.5 rounded-[9px] px-3 py-[7px]">
          <CropIcon className="size-[13px] text-accent-text" strokeWidth={1.5} />
          <span className="text-[12.5px] font-semibold">Crop</span>
          <span className="font-mono text-[11px] text-muted-foreground">R to exit</span>
        </div>
      )}

      {/* WB eyedropper toolbar (mirrors CropBar). */}
      {wbPicking && <WBBar client={client} />}

      {cropping ? (
        <CropBar
          client={client}
          aspectKey={aspectKey}
          angle={draft?.cropAngle ?? 0}
          onPickAspect={applyAspect}
        />
      ) : (
        showNavigator && !wbPicking && (
          <NavigatorInset
            photo={photo}
            scale={scale}
            viewport={viewport}
            isFit={zoom === 'fit'}
            bottom={navigatorBottom}
            onPan={panTo}
          />
        )
      )}
    </div>
  );
}

// usePixelSampler decodes the shown rendition into readable pixels while
// the WB pipette is up, so the readout tag can show the RGB under the
// cursor (the /img endpoint is CORS-open; preview blobs are same-origin).
function usePixelSampler(src: string, active: boolean): ImageData | null {
  const [data, setData] = useState<ImageData | null>(null);
  useEffect(() => {
    if (!active || !src) return;
    let alive = true;
    const ac = new AbortController();
    fetch(src, { signal: ac.signal })
      .then((r) => (r.ok ? r.blob() : Promise.reject(new Error(String(r.status)))))
      .then((b) => createImageBitmap(b))
      .then((bmp) => {
        if (!alive) {
          bmp.close();
          return;
        }
        const c = document.createElement('canvas');
        c.width = bmp.width;
        c.height = bmp.height;
        const ctx = c.getContext('2d', { willReadFrequently: true })!;
        ctx.drawImage(bmp, 0, 0);
        setData(ctx.getImageData(0, 0, bmp.width, bmp.height));
        bmp.close();
      })
      .catch(() => {});
    return () => {
      alive = false;
      ac.abort();
      // Release the decoded pixels once the pipette goes away (or the
      // rendition changes) — an ImageData of a 2048 frame is not small.
      setData(null);
    };
  }, [src, active]);
  return active ? data : null;
}

// Magnifier: the WB pipette's loupe — a 138px circle showing the pixels
// under the (hidden) cursor at 3×, with a pixel grid, an accent target,
// and the sampled-RGB readout tag.
function Magnifier({
  src,
  boxW,
  boxH,
  cursor,
  rgb,
}: {
  src: string;
  boxW: number;
  boxH: number;
  cursor: [number, number];
  rgb: readonly [number, number, number] | null;
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
      {rgb && (
        <div
          className="absolute flex items-center gap-2 rounded-[9px] border border-white/15 bg-[rgba(12,14,18,.78)] px-[11px] py-[7px] whitespace-nowrap shadow-[0_14px_34px_-12px_rgba(0,0,0,.6)] backdrop-blur-md"
          style={{ left: SIZE + 12, top: 44 }}
        >
          <div
            className="size-5 rounded-[5px] border border-white/30"
            style={{ background: `rgb(${rgb[0]},${rgb[1]},${rgb[2]})` }}
          />
          <span className="font-mono text-[11px] text-[#c4c7cc] tabular-nums">
            R{rgb[0]} G{rgb[1]} B{rgb[2]}
          </span>
        </div>
      )}
    </div>
  );
}

// NavigatorMap: the interactive minimap itself — the 256px rendition with the
// visible-region rectangle over it, click/drag to recenter the pan. Shared by
// the floating NavigatorInset (over the canvas) and the Develop Info tab
// (in-panel, fed from the loupeNav store).
export function NavigatorMap({
  photo,
  viewport,
  onPan,
  className,
}: {
  photo: Photo;
  viewport: [number, number, number, number];
  onPan?: (fx: number, fy: number) => void;
  className?: string;
}) {
  const [vx, vy, vw, vh] = viewport;
  const [dragging, setDragging] = useState(false);
  const zoomed = vw < 0.999 || vh < 0.999;

  const panFromEvent = (e: React.PointerEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    onPan?.(
      Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width)),
      Math.min(1, Math.max(0, (e.clientY - rect.top) / rect.height)),
    );
  };

  return (
    <div
      className={cn('relative touch-none overflow-hidden rounded-md', zoomed && (dragging ? 'cursor-grabbing' : 'cursor-grab'), className)}
      onPointerDown={(e) => {
        if (!zoomed) return;
        e.currentTarget.setPointerCapture?.(e.pointerId);
        setDragging(true);
        panFromEvent(e);
      }}
      onPointerMove={(e) => dragging && panFromEvent(e)}
      onPointerUp={(e) => {
        setDragging(false);
        e.currentTarget.releasePointerCapture?.(e.pointerId);
      }}
      onPointerCancel={() => setDragging(false)}
    >
      <PyramidImage src={imgUrl(photo, '256')} className="block w-full" />
      {zoomed && (
        <div
          className="pointer-events-none absolute rounded-[2px] border-[1.5px] border-white"
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
  );
}

// NavigatorInset: the 200px glass minimap (bottom right) with the visible
// region outlined; click to recenter the pan there.
function NavigatorInset({
  photo,
  scale,
  viewport,
  isFit,
  bottom = 18,
  onPan,
}: {
  photo: Photo;
  scale: number;
  viewport: [number, number, number, number];
  isFit: boolean;
  bottom?: number;
  /** Center the main viewport on this image fraction (drag or click). */
  onPan?: (fx: number, fy: number) => void;
}) {
  const [, , vw, vh] = viewport;
  const zoomed = vw < 0.999 || vh < 0.999;
  if (isFit && !zoomed) return null;

  return (
    <div className="glass absolute right-[18px] z-30 w-[200px] rounded-[11px] p-[9px]" style={{ bottom }}>
      <div className="mb-[7px] flex items-center justify-between">
        <span className="text-[10px] tracking-[.06em] text-muted-foreground uppercase">Navigator</span>
        <span className="font-mono text-[10.5px] text-accent-text tabular-nums">
          {Math.round(scale * 100)}%
        </span>
      </div>
      <NavigatorMap photo={photo} viewport={viewport} onPan={onPan} />
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
  slackX,
  slackY,
  container,
  onProgress,
}: {
  photo: Photo;
  dw: number; // rendered (crop-aware) display width
  dh: number;
  boxW: number;
  /** Per-side pan slack around the box (see slackFor) — the box's offset in scroll coordinates. */
  slackX: number;
  slackY: number;
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

  useEffect(() => {
    const el = container.current;
    if (!el) return;
    let raf = 0;
    const update = () => {
      raf = 0;
      // Viewport rect in image pixels. The box sits centered in its slack
      // wrapper (pan freedom), so its edge in scroll coordinates is offset
      // by the per-side slack.
      const offX = slackX;
      const offY = slackY;
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
  }, [container, scale, cols, rows, slackX, slackY]);

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

// FitImage paints the fit-loupe sharp layer from the PRE-RENDERED pyramid only.
// It requests the target level cacheOnly, so the server serves the warm file or
// a 404 — it never kicks a blocking RAW decode just because the user browsed
// onto the photo. A hit shows the sharp rendition instantly; a miss renders
// nothing (returns null) and the always-warm 512 underlay behind shows through,
// so a fast scan across not-yet-pre-rendered frames never stalls. On a miss it
// immediately kicks ONE render for THIS photo so a frame paused on sharpens,
// and re-requests the warm file when that lands — cheap when the background
// pre-render pass has already covered it. Skimming past is safe without a
// settle delay: the parent keys this by photo, so a switch remounts fresh (no
// lingering frame) and unmount aborts the in-flight kick, which cancels the
// RAW decode itself mid-flight — a skimmed frame costs at most the
// uncancellable unpack, not a full render.
function FitImage({
  photo,
  level,
  onShown,
  className,
}: {
  photo: Photo;
  level: Level;
  onShown?: (src: string) => void;
  className?: string;
}) {
  const [missed, setMissed] = useState(false);
  // Bumped once a settle-kicked render lands so the cacheOnly <img> re-requests
  // the now-warm file; part of the <img> key so it remounts to refetch.
  const [warmed, setWarmed] = useState(0);
  const cacheUrl = imgUrl(photo, level, { cacheOnly: true });

  useEffect(() => {
    if (!missed) return;
    const img = new Image();
    let alive = true;
    img.src = imgUrl(photo, level); // render-allowed: one render for this photo
    img
      .decode()
      .then(() => {
        if (!alive) return;
        setMissed(false);
        setWarmed((n) => n + 1);
      })
      .catch(() => {});
    return () => {
      alive = false;
      if (!img.complete) img.src = '';
    };
  }, [missed, photo, level]);

  if (missed) return null;
  return (
    <img
      key={warmed}
      src={cacheUrl}
      draggable={false}
      alt=""
      className={className}
      onLoad={() => onShown?.(cacheUrl)}
      onError={() => setMissed(true)}
    />
  );
}
