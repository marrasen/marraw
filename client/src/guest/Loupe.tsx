import { useEffect, useRef, useState } from 'react';
import { ChevronLeft, ChevronRight, Download, Loader2, Maximize, Minimize, X } from 'lucide-react';

import type { FlagType, Photo } from '@/api/library';
import { TileLayer, type ContainerMetrics } from '@/components/TileLayer';
import { imgUrl, levelForSize } from '@/lib/backend';
import { displayDims, renderedDims } from '@/lib/crop';
import { useTilesWarm } from '@/lib/tiles';
import { cn } from '@/lib/utils';

import { fullscreenElement, useFullscreen } from './fullscreen';
import { useGestures } from './gestures';
import { RatingBar } from './RatingBar';
import { savePhoto } from './save';

interface Props {
  photos: Photo[];
  index: number;
  canDownload: boolean;
  onIndex: (index: number) => void;
  onClose: () => void;
  onRate: (id: number, rating: number) => void;
  onFlag: (id: number, flag: FlagType) => void;
}

export function Loupe({ photos, index, canDownload, onIndex, onClose, onRate, onFlag }: Props) {
  const photo = photos[index];
  const [chrome, setChrome] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const fs = useFullscreen();

  // Entering fullscreen (or rotating) can grow the viewport past what the
  // current pyramid level covers; tracking the size lets the img re-pick it.
  const [viewportMax, setViewportMax] = useState(() =>
    Math.max(window.innerWidth, window.innerHeight),
  );
  useEffect(() => {
    const onResize = () => setViewportMax(Math.max(window.innerWidth, window.innerHeight));
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  const next = () => onIndex(Math.min(photos.length - 1, index + 1));
  const prev = () => onIndex(Math.max(0, index - 1));
  // The whole loupe, including the chrome bars outside the gesture surface:
  // the wheel must be caught over those too, or it scrolls the album behind.
  const rootRef = useRef<HTMLDivElement | null>(null);
  const { transform, drag, zoomed, reset, handlers } = useGestures({
    onNext: next,
    onPrev: prev,
    onClose,
    root: rootRef,
  });

  // The surface the gestures run on, and the box the photo occupies inside it.
  // The share page has no edit state, but the server mirrors rotate/crop onto
  // every photo, so the rendered size is known without loading any.
  const surface = useRef<HTMLDivElement | null>(null);
  const [surfaceSize, setSurfaceSize] = useState<[number, number]>([0, 0]);
  useEffect(() => {
    const el = surface.current;
    if (!el) return;
    const update = () => setSurfaceSize([el.clientWidth, el.clientHeight]);
    const ro = new ResizeObserver(update);
    ro.observe(el);
    update();
    return () => ro.disconnect();
  }, []);

  // A new photo starts at fit — carrying the previous one's zoom over would
  // drop the viewer into the middle of an image they have not seen yet.
  useEffect(() => reset(), [index, reset]);

  // Keyboard for whoever opens the link on a laptop. Same keys as the app, so
  // the muscle memory of anyone who has used marraw still works.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowRight') next();
      else if (e.key === 'ArrowLeft') prev();
      else if (e.key === 'Escape') {
        // In fullscreen the browser maps Escape to exiting it; also closing
        // the loupe would take both away in one press.
        if (fullscreenElement()) return;
        onClose();
      }
      else if (e.key >= '0' && e.key <= '5') onRate(photo.id, Number(e.key));
      else if (e.key === 'p') onFlag(photo.id, photo.flag === 'pick' ? 'none' : 'pick');
      else if (e.key === 'x') onFlag(photo.id, photo.flag === 'exclude' ? 'none' : 'exclude');
      else return;
      e.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    try {
      await savePhoto(photo.id, photo.fileName);
    } catch (err) {
      setSaveError((err as Error).message || 'Could not save that photo.');
    } finally {
      setSaving(false);
    }
  };

  // The photo's box inside the surface: object-contain letterboxes it, and
  // the tile layer has to land on exactly the same rectangle.
  const [fdw, fdh] = photo ? displayDims(photo) : [0, 0];
  const [dw, dh] = renderedDims(fdw, fdh, photo);
  const [cw, ch] = surfaceSize;
  const fitScale = dw > 0 && dh > 0 && cw > 0 && ch > 0 ? Math.min(cw / dw, ch / dh) : 0;
  const boxW = dw * fitScale;
  const boxH = dh * fitScale;
  const boxLeft = (cw - boxW) / 2;
  const boxTop = (ch - boxH) / 2;

  // Past fit the 2048 is being upscaled, so full-resolution tiles are the only
  // thing that adds detail. Warm-only under 2x: a casual pinch must never put
  // a visitor's fingers on the owner's decode pool. Past 2x the zoom is
  // deliberate enough to pay for one render — and useTilesWarm still requires
  // a dwell, and aborts it the moment the gesture moves on.
  const wantTiles = fitScale > 0 && transform.scale > 1;
  const tilesWarm = useTilesWarm(photo, wantTiles, transform.scale >= 2);

  if (!photo) return null;
  const offset = drag ?? { x: 0, y: 0 };

  // One transform for the photo and the tiles over it: computed once so the
  // two layers cannot drift apart mid-gesture.
  const panX = transform.x + offset.x;
  const panY = transform.y + offset.y;
  const frame = `translate3d(${panX}px, ${panY}px, 0) scale(${transform.scale})`;
  // No transition while a finger is down: the image must track the gesture
  // exactly, and ease back only once it is released.
  const ease = drag ? 'none' : 'transform 180ms ease-out';

  // Screen back to image pixels: the photo is transformed about the surface's
  // centre, so undo the pan and the zoom around that point, then step into the
  // letterboxed box.
  const viewportFor = (m: ContainerMetrics) => {
    const cx = m.clientWidth / 2;
    const cy = m.clientHeight / 2;
    return {
      x: (cx + (-cx - panX) / transform.scale - boxLeft) / fitScale,
      y: (cy + (-cy - panY) / transform.scale - boxTop) / fitScale,
      w: m.clientWidth / transform.scale / fitScale,
      h: m.clientHeight / transform.scale / fitScale,
    };
  };

  return (
    <div ref={rootRef} className="fixed inset-0 z-10 bg-black">
      <div
        ref={surface}
        // absolute inset-0, not a flex child: the chrome floats over the photo
        // rather than sitting beside it, so showing and hiding it cannot
        // resize the image area and make the photo jump.
        //
        // touch-none: every gesture here is ours, and letting the browser also
        // scroll or page-zoom makes both feel unreliable.
        className="no-select absolute inset-0 touch-none overflow-hidden"
        {...handlers}
        onPointerUp={(e) => {
          const kind = handlers.onPointerUp(e);
          if (kind === 'tap') setChrome((c) => !c);
        }}
      >
        <img
          key={photo.id}
          // 2048 is the largest pyramid level, and it is the underlay at any
          // zoom: the tile layer below sharpens whatever part of it is
          // actually on screen, and a photo whose tiles are cold still shows
          // this one upscaled rather than nothing.
          src={imgUrl(photo, levelForSize(viewportMax))}
          alt={photo.fileName}
          draggable={false}
          className="size-full object-contain"
          style={{
            transform: frame,
            transition: ease,
            opacity: drag && !zoomed ? Math.max(0.35, 1 - Math.abs(offset.y) / 400) : 1,
          }}
        />

        {/* Full-resolution detail once the viewer has zoomed in. The wrapper
            carries the same transform as the photo, so the tiles ride the
            gesture with it; inside it they sit on the letterboxed box. */}
        {wantTiles && tilesWarm && (
          <div
            className="pointer-events-none absolute inset-0"
            style={{ transform: frame, transition: ease }}
          >
            <div
              className="absolute"
              style={{ left: boxLeft, top: boxTop, width: boxW, height: boxH }}
            >
              <TileLayer
                key={`${photo.id}|${photo.cacheKey}|${photo.editHash}`}
                photo={photo}
                dw={dw}
                dh={dh}
                scale={fitScale}
                container={surface}
                viewportFor={viewportFor}
                // A phone panning around a 45MP frame would otherwise hold
                // every tile it has ever crossed. Roughly two screenfuls of
                // history: panning back is still instant, memory is not open
                // ended.
                maxTiles={24}
              />
            </div>
          </div>
        )}

        {chrome && (
          <>
            <div className="pointer-events-none absolute inset-x-0 top-0 flex items-center gap-2 bg-gradient-to-b from-black/70 to-transparent p-2 pt-[max(0.5rem,env(safe-area-inset-top))]">
              <button
                type="button"
                onClick={onClose}
                aria-label="Close"
                className="pointer-events-auto grid size-11 place-items-center rounded-full bg-black/40 active:bg-white/10"
              >
                <X className="size-6" />
              </button>
              {/* Which frame this is. Only ever visible with the chrome, so it
                  never sits over the photo while someone is looking at it. */}
              <span className="min-w-0 flex-1 truncate text-center text-[13px] text-white/80">
                {photo.fileName}
              </span>
              <span className="shrink-0 text-sm text-white/70 tabular-nums">
                {index + 1} / {photos.length}
              </span>
              {/* iPhone Safari has no fullscreen API; there the bar simply
                  ends at the counter, as it always has. */}
              {fs.supported && (
                <button
                  type="button"
                  onClick={fs.toggle}
                  aria-label={fs.active ? 'Exit full screen' : 'Enter full screen'}
                  className="pointer-events-auto grid size-11 place-items-center rounded-full bg-black/40 active:bg-white/10"
                >
                  {fs.active ? <Minimize className="size-6" /> : <Maximize className="size-6" />}
                </button>
              )}
            </div>
            {/* Arrows for mouse users; a finger uses the swipe. */}
            <NavButton side="left" disabled={index === 0} onClick={prev} />
            <NavButton side="right" disabled={index === photos.length - 1} onClick={next} />
          </>
        )}
      </div>

      {chrome && (
        <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/80 via-black/60 to-transparent pt-6 pb-[max(0.5rem,env(safe-area-inset-bottom))]">
          {saveError && <p className="px-3 pt-2 text-center text-xs text-rose-400">{saveError}</p>}
          <div className="flex items-center gap-1.5 px-1.5 py-2">
            <RatingBar
              className="min-w-0 flex-1"
              rating={photo.rating}
              flag={photo.flag}
              onRate={(r) => onRate(photo.id, r)}
              onFlag={(f) => onFlag(photo.id, f)}
            />
            {canDownload && (
              <button
                type="button"
                onClick={() => void save()}
                disabled={saving}
                aria-label="Save photo"
                className="grid size-9 shrink-0 place-items-center rounded-full bg-white/10 active:bg-white/20 disabled:opacity-50"
              >
                {saving ? <Loader2 className="size-5 animate-spin" /> : <Download className="size-5" />}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function NavButton({
  side,
  disabled,
  onClick,
}: {
  side: 'left' | 'right';
  disabled: boolean;
  onClick: () => void;
}) {
  const Icon = side === 'left' ? ChevronLeft : ChevronRight;
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={side === 'left' ? 'Previous photo' : 'Next photo'}
      className={cn(
        // mouse-only, not a width breakpoint: a phone in landscape is wider
        // than sm: and still has no cursor to reach for these.
        'mouse-only absolute top-1/2 -translate-y-1/2 place-items-center rounded-full bg-black/40 p-2',
        side === 'left' ? 'left-2' : 'right-2',
        disabled && 'opacity-30',
      )}
    >
      <Icon className="size-7" />
    </button>
  );
}
