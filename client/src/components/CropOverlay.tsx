import { useEffect, useRef, useState } from 'react';
import type { Params } from '@/api/edit';
import { cn } from '@/lib/utils';
import { fitCropToRotation, maxCoveredT, rectCornersCovered, slideMoveRect } from '@/lib/crop';

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
  frameAspect,
  pxDims,
  onChange,
  onCommit,
}: {
  draft: Params;
  ratioFrac: number | null;
  frameAspect: number; // full-frame displayed width / height (for rotation math)
  /** Full-frame pixel dimensions, for the center ratio · size pill. */
  pxDims?: [number, number];
  onChange: (patch: Partial<Params>) => void;
  onCommit: () => void;
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  const drag = useRef<{ grip: Grip; startRect: Rect; startX: number; startY: number } | null>(null);
  const [active, setActive] = useState(false);
  // The rectangle is driven from local state during a drag so a pointer-move
  // re-renders only this overlay — writing to the edit draft on every move
  // would re-render the whole panel and stutter. The draft is updated once on
  // release. When the draft changes from elsewhere (aspect reset, undo) and no
  // drag is in flight, resync.
  const [rect, setRectState] = useState<Rect>(() => currentRect(draft));
  const rectRef = useRef(rect);
  const setRect = (rc: Rect) => {
    rectRef.current = rc;
    setRectState(rc);
  };
  // Resync from the draft when it changes externally, and keep the crop clear
  // of the black wedge the straighten angle exposes: as the angle changes the
  // rect is shrunk to the largest inscribed rectangle and pushed back to the
  // draft. fitCropToRotation is idempotent, so once fitted this settles.
  useEffect(() => {
    if (drag.current) return;
    const base = currentRect(draft);
    const fitted = fitCropToRotation(base, draft.cropAngle, frameAspect);
    setRect(fitted);
    if (fitted !== base) {
      onChange({ cropX: fitted.x, cropY: fitted.y, cropW: fitted.w, cropH: fitted.h });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft.cropX, draft.cropY, draft.cropW, draft.cropH, draft.cropAngle, frameAspect]);

  const pointFrac = (e: React.PointerEvent): [number, number] => {
    const el = rootRef.current!;
    const r = el.getBoundingClientRect();
    return [clamp01((e.clientX - r.left) / r.width), clamp01((e.clientY - r.top) / r.height)];
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

  const beginDrag = (e: React.PointerEvent, grip: Grip) => {
    e.stopPropagation();
    e.preventDefault();
    try {
      (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
    } catch {
      // Capture only widens the drag beyond the handle; a pointer that can't
      // be captured (synthetic test events) still drags.
    }
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

    // The whole candidate pipeline as a function of the applied delta, so
    // the coverage clamp below can probe scaled-back deltas with every
    // invariant (edge clamps, MIN floor, aspect lock, frame clamps) intact.
    const compute = (fx: number, fy: number): Rect => {
      let rc: Rect = { ...s };
      if (grip === 'move') {
        rc.x = clamp01(Math.min(s.x + fx, 1 - s.w));
        rc.y = clamp01(Math.min(s.y + fy, 1 - s.h));
        if (s.x + fx < 0) rc.x = 0;
        if (s.y + fy < 0) rc.y = 0;
      } else {
        let left = s.x;
        let top = s.y;
        let right = s.x + s.w;
        let bottom = s.y + s.h;
        if (grip.includes('w')) left = clamp01(Math.min(s.x + fx, right - MIN));
        if (grip.includes('e')) right = clamp01(Math.max(s.x + s.w + fx, left + MIN));
        if (grip.includes('n')) top = clamp01(Math.min(s.y + fy, bottom - MIN));
        if (grip.includes('s')) bottom = clamp01(Math.max(s.y + s.h + fy, top + MIN));
        rc = { x: left, y: top, w: right - left, h: bottom - top };
        rc = applyRatio(rc, grip);
      }
      // Final clamp into the frame.
      rc.w = Math.min(rc.w, 1 - Math.max(0, rc.x));
      rc.h = Math.min(rc.h, 1 - Math.max(0, rc.y));
      rc.x = clamp01(rc.x);
      rc.y = clamp01(rc.y);
      return rc;
    };

    let rc = compute(dx, dy);
    // With a straighten angle, a drag that would cross into the black wedge
    // slides along the rotated frame's edge instead of freezing: moves keep
    // the largest usable per-axis components; resizes retreat along the
    // pointer ray. Recomputed from the start rect + fresh total delta every
    // event, so there is no drift and never a dropped move.
    if (draft.cropAngle !== 0 && !rectCornersCovered(rc, draft.cropAngle, frameAspect)) {
      if (grip === 'move') {
        rc = slideMoveRect(compute(0, 0), rc, draft.cropAngle, frameAspect);
      } else {
        const t = maxCoveredT((k) => compute(k * dx, k * dy), draft.cropAngle, frameAspect);
        rc = compute(t * dx, t * dy);
      }
    }
    setRect(rc); // local only — no store write while dragging
  };

  const onPointerUp = (e: React.PointerEvent) => {
    if (!drag.current) return;
    drag.current = null;
    setActive(false);
    (e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId);
    // Push the final rectangle to the draft and persist once.
    const rc = rectRef.current;
    onChange({ cropX: rc.x, cropY: rc.y, cropW: rc.w, cropH: rc.h });
    onCommit();
  };

  // Percent style for the rectangle and the four mask panels around it.
  const pct = (v: number) => `${v * 100}%`;
  const box = { left: pct(rect.x), top: pct(rect.y), width: pct(rect.w), height: pct(rect.h) };

  const handle = (grip: Grip, cls: string) => (
    <div
      onPointerDown={(e) => beginDrag(e, grip)}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      className={cn('absolute z-10 touch-none', cls)}
    />
  );

  // Center pill: aspect (reduced to a friendly ratio when close) + pixel
  // dimensions of the crop.
  const pill = (() => {
    if (!pxDims) return null;
    const w = Math.round(rect.w * pxDims[0]);
    const h = Math.round(rect.h * pxDims[1]);
    if (w <= 0 || h <= 0) return null;
    const r = w / h;
    const known: [string, number][] = [
      ['1:1', 1],
      ['3:2', 3 / 2],
      ['2:3', 2 / 3],
      ['4:5', 4 / 5],
      ['5:4', 5 / 4],
      ['4:3', 4 / 3],
      ['3:4', 3 / 4],
      ['16:9', 16 / 9],
      ['9:16', 9 / 16],
    ];
    const hit = known.find(([, kr]) => Math.abs(kr - r) / kr < 0.01);
    return { ratio: hit ? hit[0] : r.toFixed(2), dims: `${w.toLocaleString()} × ${h.toLocaleString()}` };
  })();

  return (
    <div ref={rootRef} className="absolute inset-0 z-10 touch-none select-none" data-testid="crop-overlay">
      {/* Dim scrim outside the crop, as four panels. */}
      <div className="pointer-events-none absolute inset-x-0 top-0 bg-[rgba(4,6,9,.62)]" style={{ height: box.top }} />
      <div className="pointer-events-none absolute inset-x-0 bottom-0 bg-[rgba(4,6,9,.62)]" style={{ height: pct(1 - rect.y - rect.h) }} />
      <div className="pointer-events-none absolute left-0 bg-[rgba(4,6,9,.62)]" style={{ top: box.top, height: box.height, width: box.left }} />
      <div className="pointer-events-none absolute right-0 bg-[rgba(4,6,9,.62)]" style={{ top: box.top, height: box.height, width: pct(1 - rect.x - rect.w) }} />

      {/* The crop rectangle. */}
      <div
        className={cn('absolute border border-white/85', active && 'border-white')}
        style={box}
        onPointerDown={(e) => beginDrag(e, 'move')}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
      >
        {/* Rule-of-thirds guides. */}
        <div className="pointer-events-none absolute inset-0">
          <div className="absolute inset-y-0 left-1/3 w-px bg-white/28" />
          <div className="absolute inset-y-0 left-2/3 w-px bg-white/28" />
          <div className="absolute inset-x-0 top-1/3 h-px bg-white/28" />
          <div className="absolute inset-x-0 top-2/3 h-px bg-white/28" />
        </div>
        {/* Corner L marks (drawn) + their grab areas. */}
        <div className="pointer-events-none absolute -top-0.5 -left-0.5 size-[22px] border-t-[3px] border-l-[3px] border-white" />
        <div className="pointer-events-none absolute -top-0.5 -right-0.5 size-[22px] border-t-[3px] border-r-[3px] border-white" />
        <div className="pointer-events-none absolute -bottom-0.5 -left-0.5 size-[22px] border-b-[3px] border-l-[3px] border-white" />
        <div className="pointer-events-none absolute -bottom-0.5 -right-0.5 size-[22px] border-b-[3px] border-r-[3px] border-white" />
        {/* Edge bar handles. */}
        <div className="pointer-events-none absolute -top-0.5 left-1/2 h-1 w-[30px] -translate-x-1/2 rounded-[2px] bg-white" />
        <div className="pointer-events-none absolute -bottom-0.5 left-1/2 h-1 w-[30px] -translate-x-1/2 rounded-[2px] bg-white" />
        <div className="pointer-events-none absolute top-1/2 -left-0.5 h-[30px] w-1 -translate-y-1/2 rounded-[2px] bg-white" />
        <div className="pointer-events-none absolute top-1/2 -right-0.5 h-[30px] w-1 -translate-y-1/2 rounded-[2px] bg-white" />
        {/* Ratio · size pill. */}
        {pill && (
          <div className="pointer-events-none absolute top-1/2 left-1/2 flex -translate-x-1/2 -translate-y-1/2 items-center gap-1.5 rounded-[7px] border border-white/15 bg-[rgba(12,14,18,.6)] px-[9px] py-1 font-mono text-[11px] whitespace-nowrap backdrop-blur-md">
            <span className="text-accent-text">{pill.ratio}</span>
            <span className="text-muted-foreground">{pill.dims}</span>
          </div>
        )}
        {/* Invisible grab areas. */}
        {handle('n', 'left-6 right-6 -top-1.5 h-3 cursor-ns-resize')}
        {handle('s', 'left-6 right-6 -bottom-1.5 h-3 cursor-ns-resize')}
        {handle('w', 'top-6 bottom-6 -left-1.5 w-3 cursor-ew-resize')}
        {handle('e', 'top-6 bottom-6 -right-1.5 w-3 cursor-ew-resize')}
        {handle('nw', '-top-2 -left-2 size-6 cursor-nwse-resize')}
        {handle('ne', '-top-2 -right-2 size-6 cursor-nesw-resize')}
        {handle('sw', '-bottom-2 -left-2 size-6 cursor-nesw-resize')}
        {handle('se', '-bottom-2 -right-2 size-6 cursor-nwse-resize')}
      </div>
    </div>
  );
}
