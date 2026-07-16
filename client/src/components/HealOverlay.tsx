import { useRef, useState } from 'react';
import type { Params, Spot } from '@/api/edit';
import type { ApiClient } from '@/api/client';
import { cn } from '@/lib/utils';
import { displayFromFrame, frameFromDisplay } from '@/lib/crop';
import {
  SPOT_FEATHER_DEFAULT,
  esBeginSpot,
  esCommitSpot,
  esFinishSpot,
  esSetActiveSpot,
  esUpdateSpot,
  useEditSession,
} from '@/lib/editSession';

// HealOverlay is the on-canvas editor for retouch spots: click (or click-drag
// to size) places a spot, its destination and source circles are draggable,
// and a connector line ties them together. Like MaskOverlay it sits over the
// displayed (cropped, straightened) image and round-trips every pointer
// position through frameFromDisplay/displayFromFrame — the twin of the Go
// maskFrame mapping — so the circles stay glued to the same image content the
// backend heals. Spot geometry lives inside draft.spots, so placement/drags
// flow through esUpdateSpot → the ordinary low-res draft render, committing on
// release; the source patch is chosen server-side once at release (esFinishSpot).
export function HealOverlay({
  client,
  draft,
  frameW,
  frameH,
  boxW,
  boxH,
}: {
  client: ApiClient;
  draft: Params;
  frameW: number; // oriented-frame pixel dims (rotatedDims) for aspect-true math
  frameH: number;
  boxW: number; // displayed image box in CSS px
  boxH: number;
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  const activeSpot = useEditSession((s) => s.activeSpot);
  const [cursor, setCursor] = useState<[number, number] | null>(null);
  const [dragging, setDragging] = useState(false);

  const spots = draft.spots ?? [];
  const L = Math.max(frameW, frameH);
  // Uniform frame-px → box-px scale (the crop is the same scale on both axes).
  const k = boxW / ((draft.cropW > 0 ? draft.cropW : 1) * frameW);
  const q = (v: number) => Math.round(v * 1e4) / 1e4;

  const pointFrac = (e: React.PointerEvent): [number, number] => {
    const r = rootRef.current!.getBoundingClientRect();
    return [(e.clientX - r.left) / r.width, (e.clientY - r.top) / r.height];
  };
  const toFrame = (bx: number, by: number) => frameFromDisplay(bx, by, draft, frameW, frameH);
  const toBoxPx = (fx: number, fy: number): [number, number] => {
    const [bx, by] = displayFromFrame(fx, fy, draft, frameW, frameH);
    return [bx * boxW, by * boxH];
  };
  const radiusBoxPx = (spot: Spot) => Math.max(2, spot.radius * L * k);

  // --- placement (create + size drag) ---
  const place = useRef<{ index: number; center: [number, number]; def: number } | null>(null);
  const beginCreate = (e: React.PointerEvent) => {
    if (e.button !== 0) return;
    e.preventDefault();
    try {
      rootRef.current?.setPointerCapture(e.pointerId);
    } catch {
      // synthetic test pointers can't be captured; the drag still works
    }
    const [bx, by] = pointFrac(e);
    const [cx, cy] = toFrame(bx, by);
    // Default radius ≈ 20 CSS px at the current zoom (frame fraction of L).
    const def = Math.min(0.05, Math.max(0.003, 20 / (L * k)));
    // Interim source: offset toward the frame center by 2.5 radii, so the live
    // preview shows a plausible fill until the server picks the real patch.
    const dx = 0.5 - cx;
    const dy = 0.5 - cy;
    const mag = Math.hypot(dx * frameW, dy * frameH) || 1;
    const off = (2.5 * def * L) / mag;
    const sx = Math.min(1, Math.max(0, cx + dx * off));
    const sy = Math.min(1, Math.max(0, cy + dy * off));
    const index = esBeginSpot(client, {
      cx: q(cx), cy: q(cy), radius: q(def), sx: q(sx), sy: q(sy), feather: SPOT_FEATHER_DEFAULT,
    });
    if (index < 0) return;
    place.current = { index, center: [cx, cy], def };
    setDragging(true);
  };
  const placeMove = (e: React.PointerEvent) => {
    setCursor(pointFrac(e));
    const p = place.current;
    if (!p) return;
    const [bx, by] = pointFrac(e);
    const [fx, fy] = toFrame(bx, by);
    // Drag beyond the default footprint grows the spot; a plain click keeps it.
    const dist = Math.hypot((fx - p.center[0]) * frameW, (fy - p.center[1]) * frameH) / L;
    esUpdateSpot(client, p.index, { radius: q(Math.max(p.def, dist)) });
  };
  const placeEnd = (e: React.PointerEvent) => {
    const p = place.current;
    if (!p) return;
    place.current = null;
    setDragging(false);
    try {
      rootRef.current?.releasePointerCapture(e.pointerId);
    } catch {
      // see beginCreate
    }
    void esFinishSpot(client, p.index);
  };

  // --- handle drags (dest center, source center, radius) ---
  const grip = useRef<{ kind: string; start: Spot; startFrame: [number, number] } | null>(null);
  const beginGrip = (e: React.PointerEvent, kind: string, index: number) => {
    e.stopPropagation();
    e.preventDefault();
    esSetActiveSpot(index);
    const spot = (useEditSession.getState().draft?.spots ?? [])[index];
    if (!spot) return;
    try {
      (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
    } catch {
      // see beginCreate
    }
    const [bx, by] = pointFrac(e);
    grip.current = { kind, start: spot, startFrame: toFrame(bx, by) };
    setDragging(true);
  };
  const gripMove = (e: React.PointerEvent) => {
    const g = grip.current;
    if (g == null || activeSpot == null) return;
    const [bx, by] = pointFrac(e);
    const [fx, fy] = toFrame(bx, by);
    const s = g.start;
    let patch: Partial<Spot> | null = null;
    switch (g.kind) {
      case 'dest':
        patch = { cx: q(s.cx + (fx - g.startFrame[0])), cy: q(s.cy + (fy - g.startFrame[1])) };
        break;
      case 'source':
        patch = { sx: q(s.sx + (fx - g.startFrame[0])), sy: q(s.sy + (fy - g.startFrame[1])) };
        break;
      case 'radius': {
        const dist = Math.hypot((fx - s.cx) * frameW, (fy - s.cy) * frameH) / L;
        patch = { radius: q(Math.max(0.002, dist)) };
        break;
      }
    }
    if (patch) esUpdateSpot(client, activeSpot, patch);
  };
  const gripEnd = (e: React.PointerEvent) => {
    if (!grip.current) return;
    grip.current = null;
    setDragging(false);
    (e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId);
    esCommitSpot(client);
  };

  return (
    <div
      ref={rootRef}
      className="absolute inset-0 z-10 touch-none cursor-crosshair select-none"
      data-testid="heal-overlay"
      onPointerDown={beginCreate}
      onPointerMove={placeMove}
      onPointerUp={placeEnd}
      onPointerCancel={placeEnd}
      onPointerLeave={() => setCursor(null)}
    >
      <svg className="pointer-events-none absolute inset-0 size-full overflow-visible">
        {spots.map((spot, i) => {
          const active = i === activeSpot;
          const [dcx, dcy] = toBoxPx(spot.cx, spot.cy);
          const r = radiusBoxPx(spot);
          if (!active) {
            // Inactive spot: a faint destination ring, clickable to select.
            return (
              <circle
                key={i}
                cx={dcx} cy={dcy} r={r}
                fill="transparent" stroke="white" strokeOpacity=".55" strokeWidth={1.5}
                className="pointer-events-auto cursor-pointer"
                onPointerDown={(e) => beginGrip(e, 'dest', i)}
              />
            );
          }
          const [scx, scy] = toBoxPx(spot.sx, spot.sy);
          return (
            <g key={i}>
              <line x1={dcx} y1={dcy} x2={scx} y2={scy} stroke="white" strokeOpacity=".7" strokeDasharray="4 3" />
              {/* Source ring (dashed) */}
              <circle cx={scx} cy={scy} r={r} fill="transparent" stroke="white" strokeOpacity=".85" strokeDasharray="5 3" />
              {/* Destination ring (solid) */}
              <circle cx={dcx} cy={dcy} r={r} fill="rgba(120,180,255,.12)" stroke="white" strokeOpacity=".95" strokeWidth={1.75} />
            </g>
          );
        })}
      </svg>
      {activeSpot != null && spots[activeSpot] && (
        <ActiveHandles
          spot={spots[activeSpot]}
          index={activeSpot}
          toBoxPx={toBoxPx}
          radiusBoxPx={radiusBoxPx}
          begin={beginGrip}
          move={gripMove}
          end={gripEnd}
        />
      )}
      {/* Placement cursor footprint when hovering empty canvas. */}
      {cursor && !dragging && (
        <div
          className="pointer-events-none absolute rounded-full border border-white/70"
          style={{
            width: Math.min(0.05, Math.max(0.003, 20 / (L * k))) * L * k * 2,
            height: Math.min(0.05, Math.max(0.003, 20 / (L * k))) * L * k * 2,
            left: cursor[0] * boxW,
            top: cursor[1] * boxH,
            transform: 'translate(-50%, -50%)',
          }}
        />
      )}
    </div>
  );
}

// ActiveHandles draws the grabbable dots for the selected spot: move the
// destination, move the source, and resize (a dot on the destination ring's
// east point).
function ActiveHandles({
  spot,
  index,
  toBoxPx,
  radiusBoxPx,
  begin,
  move,
  end,
}: {
  spot: Spot;
  index: number;
  toBoxPx: (fx: number, fy: number) => [number, number];
  radiusBoxPx: (spot: Spot) => number;
  begin: (e: React.PointerEvent, kind: string, index: number) => void;
  move: (e: React.PointerEvent) => void;
  end: (e: React.PointerEvent) => void;
}) {
  const [dcx, dcy] = toBoxPx(spot.cx, spot.cy);
  const [scx, scy] = toBoxPx(spot.sx, spot.sy);
  const r = radiusBoxPx(spot);
  return (
    <>
      <Dot at={[dcx, dcy]} cursor="move" kind="dest" index={index} begin={begin} move={move} end={end} title="Move fill" />
      <Dot at={[scx, scy]} cursor="move" kind="source" index={index} begin={begin} move={move} end={end} title="Move source" />
      <Dot at={[dcx + r, dcy]} cursor="ew-resize" kind="radius" index={index} begin={begin} move={move} end={end} title="Size" />
    </>
  );
}

function Dot({
  at,
  cursor,
  kind,
  index,
  begin,
  move,
  end,
  title,
}: {
  at: [number, number];
  cursor: string;
  kind: string;
  index: number;
  begin: (e: React.PointerEvent, kind: string, index: number) => void;
  move: (e: React.PointerEvent) => void;
  end: (e: React.PointerEvent) => void;
  title?: string;
}) {
  return (
    <div
      className={cn(
        'pointer-events-auto absolute z-10 size-4 touch-none rounded-full border-2 border-white bg-black/40 shadow-[0_0_0_1px_rgba(0,0,0,.4)]',
      )}
      style={{ left: at[0], top: at[1], transform: 'translate(-50%, -50%)', cursor }}
      title={title}
      onPointerDown={(e) => begin(e, kind, index)}
      onPointerMove={move}
      onPointerUp={end}
      onPointerCancel={end}
    />
  );
}
