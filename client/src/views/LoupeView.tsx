import { useEffect, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import type { Photo } from '@/api/library';
import { cn } from '@/lib/utils';
import { imgUrl, levelForSize, type Level } from '@/lib/backend';
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

function MainImage({ photo }: { photo: Photo }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [zoom, setZoom] = useState<'fit' | '1:1'>('fit');
  const previewHash = useUIStore((s) => s.previewHash);

  // While an edit preview is active we always show the 2048 rendition the
  // backend just produced; otherwise pick by container size / zoom.
  const [fitLevel, setFitLevel] = useState<Level>('1024');
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const update = () => setFitLevel(levelForSize(Math.max(el.clientWidth, el.clientHeight)));
    const ro = new ResizeObserver(update);
    ro.observe(el);
    update();
    return () => ro.disconnect();
  }, []);

  const level: Level = previewHash ? '2048' : zoom === '1:1' ? 'full' : fitLevel;
  const src = imgUrl(photo, level, previewHash ?? undefined);
  // Under the hi-res image keep a small rendition that is almost certainly
  // cached, so level/zoom switches never flash white.
  const placeholder = imgUrl(photo, '512', previewHash ? undefined : undefined);

  useEffect(() => setZoom('fit'), [photo.id]);

  return (
    <div
      ref={containerRef}
      className={cn(
        'relative min-h-0 flex-1 bg-black/40',
        zoom === 'fit' ? 'cursor-zoom-in overflow-hidden' : 'cursor-zoom-out overflow-auto',
      )}
      onDoubleClick={() => setZoom((z) => (z === 'fit' ? '1:1' : 'fit'))}
    >
      {zoom === 'fit' ? (
        <DecodedImage key={photo.id} src={src} placeholder={placeholder} className="absolute inset-0 size-full object-contain" />
      ) : (
        <DecodedImage key={photo.id + ':1:1'} src={src} placeholder={placeholder} className="max-w-none" />
      )}
    </div>
  );
}

// DecodedImage double-buffers src changes: the new image is decoded
// off-screen and swapped in only when ready, so slider drags and zoom
// changes never flash.
export function DecodedImage({
  src,
  placeholder,
  className,
}: {
  src: string;
  placeholder?: string;
  className?: string;
}) {
  const [shown, setShown] = useState(placeholder ?? src);
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
