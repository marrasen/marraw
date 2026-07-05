import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Maximize, Square } from 'lucide-react';
import type { Photo } from '@/api/library';
import { Button } from '@/components/ui/button';
import { Slider } from '@/components/ui/slider';
import { cn } from '@/lib/utils';
import { imgUrl, type Level } from '@/lib/backend';
import { useUIStore } from '@/stores/uiStore';

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
      <MainImage photo={photo} />
      <Filmstrip photos={photos} currentId={photo.id} />
    </div>
  );
}

// displayDims returns the on-screen orientation-corrected pixel size.
function displayDims(photo: Photo): [number, number] {
  if (photo.orientation === 5 || photo.orientation === 6) return [photo.height, photo.width];
  return [photo.width, photo.height];
}

// levelForPx picks the smallest rendition covering px device pixels.
function levelForPx(px: number): Level {
  for (const l of ['256', '512', '1024', '2048'] as const) {
    if (Number(l) >= px) return l;
  }
  return 'full';
}

function MainImage({ photo }: { photo: Photo }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [container, setContainer] = useState<[number, number]>([0, 0]);
  const zoom = useUIStore((s) => s.loupeZoom);
  const setZoom = useUIStore((s) => s.setLoupeZoom);
  const previewHash = useUIStore((s) => s.previewHash);
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

  const [dw, dh] = displayDims(photo);
  const haveDims = dw > 0 && dh > 0 && container[0] > 0;
  const fitScale = haveDims ? Math.min(container[0] / dw, container[1] / dh) : 1;
  const scale = zoom === 'fit' ? fitScale : zoom;
  const boxW = Math.max(1, Math.round(dw * scale));
  const boxH = Math.max(1, Math.round(dh * scale));

  // While an edit preview is active, always show the 2048 rendition the
  // backend just produced (previews render at 2048 only).
  const level: Level = previewHash ? '2048' : levelForPx(Math.max(boxW, boxH) * window.devicePixelRatio);
  const src = imgUrl(photo, level, previewHash ?? undefined);

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
    setZoom(cur * Math.exp(-e.deltaY * 0.0015));
  };

  return (
    <div className="relative min-h-0 flex-1">
      <div
        ref={containerRef}
        className="flex size-full overflow-auto bg-black/40"
        onScroll={onScroll}
        onWheel={onWheel}
        onDoubleClick={() => setZoom(zoom === 'fit' ? 1 : 'fit')}
      >
        {haveDims ? (
          <div className="relative m-auto shrink-0" style={{ width: boxW, height: boxH }}>
            <DecodedImage src={src} className="absolute inset-0 size-full" />
          </div>
        ) : (
          // Metadata not scanned yet: plain fit rendering.
          <DecodedImage src={src} className="m-auto max-h-full max-w-full object-contain" />
        )}
      </div>
      <ZoomToolbar scale={scale} isFit={zoom === 'fit'} setZoom={setZoom} />
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
      <Button size="sm" variant={isFit ? 'secondary' : 'ghost'} onClick={() => setZoom('fit')} title="Fit (Z)">
        <Maximize data-icon="inline-start" />
        Fit
      </Button>
      <Button size="sm" variant={!isFit && Math.abs(scale - 1) < 0.01 ? 'secondary' : 'ghost'} onClick={() => setZoom(1)} title="100% (Z)">
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

// DecodedImage double-buffers src changes: the new image decodes off-screen
// and swaps in only when ready, so photo switches, zoom-level upgrades, and
// slider drags never flash or show a misplaced small rendition. Until the
// first decode lands, the previous src keeps filling the same box.
export function DecodedImage({ src, className }: { src: string; className?: string }) {
  const [shown, setShown] = useState(src);
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
    };
  }, [src]);
  return <img src={shown} draggable={false} alt="" className={className} />;
}

function Filmstrip({ photos, currentId }: { photos: Photo[]; currentId: number }) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const focus = useUIStore((s) => s.focus);
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
          return (
            <button
              key={p.id}
              className={cn(
                'absolute top-0 h-full p-1',
                p.id === currentId && 'bg-accent',
                p.flag === 'exclude' && 'opacity-40',
              )}
              style={{ left: item.start, width: 96 }}
              onClick={() => focus(p.id)}
            >
              <img
                src={imgUrl(p, '256')}
                alt={p.fileName}
                draggable={false}
                loading="lazy"
                className="size-full rounded-sm object-contain"
              />
            </button>
          );
        })}
      </div>
    </div>
  );
}
