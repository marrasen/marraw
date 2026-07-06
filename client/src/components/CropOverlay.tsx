import { useRef, useState } from 'react';
import type { Params } from '@/api/edits';
import { cn } from '@/lib/utils';

// Which edges a drag moves. Corner/edge handles set one or both; an interior
// drag moves the whole rectangle.
type Grip = 'move' | 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw';

interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

const MIN = 0.05; // smallest crop as a fraction of a side
const clamp01 = (v: number) => Math.min(1, Math.max(0, v));

// currentRect reads the draft's crop, defaulting to the full frame when
// no crop is set (cropW/H === 0).
function currentRect(draft: Params): Rect {
  if (draft.cropW > 0 && draft.cropH > 0) {
    return { x: draft.cropX, y: draft.cropY, w: draft.cropW, h: draft.cropH };
  }
  return { x: 0, y: 0, w: 1, h: 1 };
}

// CropOverlay draws the crop rectangle, rule-of-thirds guides, and dark mask
// over the (uncropped, straightened) loupe frame. It works in fractions of
// the displayed frame and writes cropX/Y/W/H back through onChange during the
// drag; onCommit fires on release. ratioFrac, if set, is the locked aspect
// ratio expressed in fraction space (width-fraction / height-fraction).
export function CropOverlay({
  draft,
  ratioFrac,
  onChange,
  onCommit,
}: {
  draft: Params;
  ratioFrac: number | null;
  onChange: (patch: Partial<Params>) => void;
  onCommit: () => void;
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  const drag = useRef<{ grip: Grip; startRect: Rect; startX: number; startY: number } | null>(null);
  const [active, setActive] = useState(false);
  const rect = currentRect(draft);

  const pointFrac = (e: React.PointerEvent): [number, number] => {
    const el = rootRef.current!;
    const r = el.getBoundingClientRect();
    return [clamp01((e.clientX - r.left) / r.width), clamp01((e.clientY - r.top) / r.height)];
  };

  const commitRect = (rc: Rect) => {
    onChange({ cropX: rc.x, cropY: rc.y, cropW: rc.w, cropH: rc.h });
  };

  const applyRatio = (rc: Rect, grip: Grip): Rect => {
    if (!ratioFrac) return rc;
    // Preserve width for horizontal grips, height for vertical; corners key
    // off width. Anchor the opposite edge so the moving edge does the work.
    const anchorRight = grip === 'w' || grip === 'nw' || grip === 'sw';
    const anchorBottom = grip === 'n' || grip === 'nw' || grip === 'ne';
    let { x, y, w, h } = rc;
    if (grip === 'n' || grip === 's') {
      const nw = h * ratioFrac;
      const cx = x + w / 2;
      x = clamp01(cx - nw / 2);
      w = nw;
    } else {
      const nh = w / ratioFrac;
      if (anchorBottom) y = y + h - nh;
      h = nh;
    }
    if (anchorRight) x = rc.x + rc.w - w;
    return { x, y, w, h };
  };

  const onPointerDown = (grip: Grip) => (e: React.PointerEvent) => {
    e.stopPropagation();
    e.preventDefault();
    (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
    const [px, py] = pointFrac(e);
    drag.current = { grip, startRect: rect, startX: px, startY: py };
    setActive(true);
  };

  const onPointerMove = (e: React.PointerEvent) => {
    if (!drag.current) return;
    const [px, py] = pointFrac(e);
    const { grip, startRect: s, startX, startY } = drag.current;
    const dx = px - startX;
    const dy = py - startY;
    let rc: Rect = { ...s };

    if (grip === 'move') {
      rc.x = clamp01(Math.min(s.x + dx, 1 - s.w));
      rc.y = clamp01(Math.min(s.y + dy, 1 - s.h));
      if (s.x + dx < 0) rc.x = 0;
      if (s.y + dy < 0) rc.y = 0;
    } else {
      let left = s.x;
      let top = s.y;
      let right = s.x + s.w;
      let bottom = s.y + s.h;
      if (grip.includes('w')) left = clamp01(Math.min(s.x + dx, right - MIN));
      if (grip.includes('e')) right = clamp01(Math.max(s.x + s.w + dx, left + MIN));
      if (grip.includes('n')) top = clamp01(Math.min(s.y + dy, bottom - MIN));
      if (grip.includes('s')) bottom = clamp01(Math.max(s.y + s.h + dy, top + MIN));
      rc = { x: left, y: top, w: right - left, h: bottom - top };
      rc = applyRatio(rc, grip);
    }
    // Final clamp into the frame.
    rc.w = Math.min(rc.w, 1 - Math.max(0, rc.x));
    rc.h = Math.min(rc.h, 1 - Math.max(0, rc.y));
    rc.x = clamp01(rc.x);
    rc.y = clamp01(rc.y);
    commitRect(rc);
  };

  const onPointerUp = (e: React.PointerEvent) => {
    if (!drag.current) return;
    drag.current = null;
    setActive(false);
    (e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId);
    onCommit();
  };

  // Percent style for the rectangle and the four mask panels around it.
  const pct = (v: number) => `${v * 100}%`;
  const box = { left: pct(rect.x), top: pct(rect.y), width: pct(rect.w), height: pct(rect.h) };

  const handle = (grip: Grip, cls: string) => (
    <div
      onPointerDown={onPointerDown(grip)}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      className={cn('absolute z-10 touch-none', cls)}
    />
  );

  return (
    <div ref={rootRef} className="absolute inset-0 z-10 touch-none select-none" data-testid="crop-overlay">
      {/* Dark mask outside the crop, as four panels. */}
      <div className="pointer-events-none absolute inset-x-0 top-0 bg-black/55" style={{ height: box.top }} />
      <div className="pointer-events-none absolute inset-x-0 bottom-0 bg-black/55" style={{ height: pct(1 - rect.y - rect.h) }} />
      <div className="pointer-events-none absolute left-0 bg-black/55" style={{ top: box.top, height: box.height, width: box.left }} />
      <div className="pointer-events-none absolute right-0 bg-black/55" style={{ top: box.top, height: box.height, width: pct(1 - rect.x - rect.w) }} />

      {/* The crop rectangle. */}
      <div
        className={cn('absolute border border-white/80', active && 'border-white')}
        style={box}
        onPointerDown={onPointerDown('move')}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
      >
        {/* Rule-of-thirds guides. */}
        <div className="pointer-events-none absolute inset-0">
          <div className="absolute inset-y-0 left-1/3 w-px bg-white/25" />
          <div className="absolute inset-y-0 left-2/3 w-px bg-white/25" />
          <div className="absolute inset-x-0 top-1/3 h-px bg-white/25" />
          <div className="absolute inset-x-0 top-2/3 h-px bg-white/25" />
        </div>
        {/* Edge handles. */}
        {handle('n', 'left-1/4 right-1/4 -top-1 h-2 cursor-ns-resize')}
        {handle('s', 'left-1/4 right-1/4 -bottom-1 h-2 cursor-ns-resize')}
        {handle('w', 'top-1/4 bottom-1/4 -left-1 w-2 cursor-ew-resize')}
        {handle('e', 'top-1/4 bottom-1/4 -right-1 w-2 cursor-ew-resize')}
        {/* Corner handles. */}
        {handle('nw', '-top-1.5 -left-1.5 size-3 border-t-2 border-l-2 border-white cursor-nwse-resize')}
        {handle('ne', '-top-1.5 -right-1.5 size-3 border-t-2 border-r-2 border-white cursor-nesw-resize')}
        {handle('sw', '-bottom-1.5 -left-1.5 size-3 border-b-2 border-l-2 border-white cursor-nesw-resize')}
        {handle('se', '-bottom-1.5 -right-1.5 size-3 border-b-2 border-r-2 border-white cursor-nwse-resize')}
      </div>
    </div>
  );
}
