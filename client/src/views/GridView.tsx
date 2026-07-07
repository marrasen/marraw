import { useEffect, useMemo, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Check } from 'lucide-react';
import { setVisible, type Photo } from '@/api/library';
import { useApiClient } from '@/api/client';
import { cn } from '@/lib/utils';
import { imgUrl } from '@/lib/backend';
import { useUIStore } from '@/stores/uiStore';

const CELL_GAP = 12;

export function GridView({ photos, folderId }: { photos: Photo[]; folderId: number }) {
  const client = useApiClient();
  // Element state (not a ref) so measurement re-attaches whenever the
  // scroll container mounts — a plain ref + [] effect misses it when the
  // first render shows the empty state.
  const [scrollEl, setScrollEl] = useState<HTMLDivElement | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  scrollRef.current = scrollEl;
  const [width, setWidth] = useState(0);

  useEffect(() => {
    if (!scrollEl) return;
    const ro = new ResizeObserver(() => setWidth(scrollEl.clientWidth));
    ro.observe(scrollEl);
    setWidth(scrollEl.clientWidth);
    return () => ro.disconnect();
  }, [scrollEl]);

  const cellTarget = useUIStore((s) => s.cellSize);
  const cols = Math.max(1, Math.floor(width / cellTarget));
  const cellW = width > 0 ? Math.floor((width - CELL_GAP * (cols + 1)) / cols) : cellTarget;
  // Uniform 3:2 cells per the handoff grid spec (no filename strip).
  const cellH = Math.floor((cellW * 2) / 3);
  const rowCount = Math.ceil(photos.length / cols);

  const setGrid = useUIStore((s) => s.setGrid);
  useEffect(() => setGrid(cols), [cols, setGrid]);

  const virtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => cellH + CELL_GAP,
    overscan: 3,
  });

  const items = virtualizer.getVirtualItems();

  // Prefetch hint: tell the backend which photos are (nearly) on screen.
  const visibleKey = items.length ? `${items[0].index}-${items[items.length - 1].index}` : '';
  useEffect(() => {
    if (!items.length || photos.length === 0) return;
    const t = setTimeout(() => {
      const from = Math.max(0, items[0].index - 2) * cols;
      const to = Math.min(photos.length, (items[items.length - 1].index + 3) * cols);
      setVisible(client, folderId, photos.slice(from, to).map((p) => p.id)).catch(() => {});
    }, 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visibleKey, folderId, photos.length, cols, client]);

  // Keep the focused cell scrolled into view when navigating by keyboard.
  const focusId = useUIStore((s) => s.focusId);
  const focusRow = useMemo(() => {
    const idx = photos.findIndex((p) => p.id === focusId);
    return idx < 0 ? null : Math.floor(idx / cols);
  }, [photos, focusId, cols]);
  useEffect(() => {
    if (focusRow != null) virtualizer.scrollToIndex(focusRow);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusRow]);

  return (
    <div className="relative min-h-0 flex-1">
      <div ref={setScrollEl} className="size-full overflow-y-auto" tabIndex={-1}>
        {photos.length === 0 && (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            No photos match the current filter.
          </div>
        )}
        <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
          {items.map((row) => (
            <div
              key={row.key}
              className="absolute left-0 flex w-full"
              style={{ top: row.start, height: cellH, gap: CELL_GAP, paddingLeft: CELL_GAP, paddingRight: CELL_GAP }}
            >
              {photos.slice(row.index * cols, row.index * cols + cols).map((p) => (
                <GridCell key={p.id} photo={p} w={cellW} h={cellH} />
              ))}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function GridCell({ photo, w, h }: { photo: Photo; w: number; h: number }) {
  const focusId = useUIStore((s) => s.focusId);
  const selected = useUIStore((s) => s.selection.has(photo.id));
  const multiSelect = useUIStore((s) => s.selection.size > 1);
  const focus = useUIStore((s) => s.focus);
  const setView = useUIStore((s) => s.setView);
  const [loaded, setLoaded] = useState(false);
  const level = w * window.devicePixelRatio > 256 ? '512' : '256';
  const isFocus = focusId === photo.id;

  return (
    <div
      className={cn(
        'group relative cursor-pointer overflow-hidden rounded bg-inset',
        photo.flag === 'exclude' && 'opacity-40',
        // Batch spec: selected cells tint + accent border; the range anchor
        // (focus) gets the white ring.
        multiSelect && selected && 'bg-primary/15',
      )}
      style={{ width: w, height: h }}
      onClick={(e) => focus(photo.id, { extend: e.shiftKey, toggle: e.ctrlKey || e.metaKey })}
      onDoubleClick={() => {
        focus(photo.id);
        // Double-click drops the frame into the Cull confirm loupe.
        useUIStore.getState().setMode('cull');
      }}
      title={photo.fileName}
    >
      {!loaded && <div className="skeleton-shimmer absolute inset-0" />}
      <img
        src={imgUrl(photo, level)}
        alt={photo.fileName}
        draggable={false}
        loading="lazy"
        onLoad={() => setLoaded(true)}
        className={cn('size-full object-cover', !loaded && 'opacity-0')}
      />
      {photo.rating > 0 && (
        <div className="absolute bottom-[5px] left-[5px] rounded bg-black/50 px-[5px] py-0.5 text-[9px] tracking-[.5px] text-rating">
          {'★'.repeat(photo.rating)}
        </div>
      )}
      {photo.flag !== 'none' && (
        <div
          className={cn(
            'absolute top-1.5 right-1.5 size-[9px] rounded-[2px]',
            photo.flag === 'pick' ? 'bg-success' : 'bg-destructive',
          )}
          aria-label={photo.flag === 'pick' ? 'Pick' : 'Excluded'}
        />
      )}
      {multiSelect && selected && (
        <>
          <div className="pointer-events-none absolute inset-0 rounded border-2 border-primary" />
          <div className="absolute top-1.5 left-1.5 flex size-4 items-center justify-center rounded-[5px] bg-primary">
            <Check className="size-[11px] text-primary-foreground" strokeWidth={2.5} />
          </div>
        </>
      )}
      {isFocus && (
        <div
          className={cn(
            'pointer-events-none absolute inset-0 rounded border-2',
            multiSelect && selected ? 'border-white' : 'border-primary',
          )}
        />
      )}
      {!isFocus && !multiSelect && selected && (
        <div className="pointer-events-none absolute inset-0 rounded border-2 border-primary/60" />
      )}
    </div>
  );
}
