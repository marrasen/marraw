import { useEffect, useMemo, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Star, X } from 'lucide-react';
import { setVisible, type Photo } from '@/api/library';
import { useApiClient } from '@/api/client';
import { cn } from '@/lib/utils';
import { imgUrl } from '@/lib/backend';
import { useUIStore } from '@/stores/uiStore';

const CELL_TARGET = 220; // px, desired cell width
const CELL_GAP = 8;

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

  const cols = Math.max(2, Math.floor(width / CELL_TARGET));
  const cellW = width > 0 ? Math.floor((width - CELL_GAP * (cols + 1)) / cols) : CELL_TARGET;
  const cellH = Math.floor(cellW * 0.72) + 28;
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
    <div ref={setScrollEl} className="min-h-0 flex-1 overflow-y-auto" tabIndex={-1}>
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
  );
}

function GridCell({ photo, w, h }: { photo: Photo; w: number; h: number }) {
  const focusId = useUIStore((s) => s.focusId);
  const selected = useUIStore((s) => s.selection.has(photo.id));
  const focus = useUIStore((s) => s.focus);
  const setView = useUIStore((s) => s.setView);
  const level = w * window.devicePixelRatio > 256 ? '512' : '256';
  const isFocus = focusId === photo.id;

  return (
    <div
      className={cn(
        'group relative flex cursor-pointer flex-col overflow-hidden rounded-md bg-muted/40',
        selected && 'ring-2 ring-primary',
        isFocus && 'ring-2 ring-ring',
        photo.flag === 'exclude' && 'opacity-40',
      )}
      style={{ width: w, height: h }}
      onClick={(e) => focus(photo.id, { extend: e.shiftKey, toggle: e.ctrlKey || e.metaKey })}
      onDoubleClick={() => {
        focus(photo.id);
        setView('loupe');
      }}
    >
      <img
        src={imgUrl(photo, level)}
        alt={photo.fileName}
        draggable={false}
        loading="lazy"
        className="min-h-0 w-full flex-1 object-contain"
      />
      <div className="flex h-7 items-center gap-1 px-1.5 text-[11px] text-muted-foreground">
        <span className="truncate">{photo.fileName}</span>
        <span className="ml-auto flex items-center gap-0.5">
          {photo.flag === 'pick' && <span className="size-2 rounded-full bg-emerald-500" aria-label="Pick" />}
          {photo.flag === 'exclude' && <X className="size-3 text-red-500" aria-label="Excluded" />}
          {photo.rating > 0 && (
            <span className="flex items-center gap-px text-amber-400">
              {photo.rating}
              <Star className="size-3 fill-amber-400 text-amber-400" />
            </span>
          )}
        </span>
      </div>
    </div>
  );
}
