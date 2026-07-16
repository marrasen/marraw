import { useRef, useState } from 'react';
import type { Params, Spot, Stroke } from '@/api/edit';
import type { ApiClient } from '@/api/client';
import { displayFromFrame, frameFromDisplay, quant4 } from '@/lib/crop';
import { Dot } from '@/components/MaskOverlay';
import {
  SPOT_FEATHER_DEFAULT,
  esBeginSpot,
  esCommit,
  esFinishSpot,
  esSetActiveSpot,
  esUpdateSpot,
  useEditSession,
} from '@/lib/editSession';

// The server clamps Spot.Radius to this in Normalize (a fraction of the frame
// long edge); the drags clamp to the same bound so a draft never previews a
// larger disc than the commit will render.
const SPOT_RADIUS_MAX = 0.5;

// enclosingCircle reduces a stroke's points (frame fractions) plus its brush
// radius to the enclosing circle — the client twin of the server's
// StrokeSpotCircle, used for the interim dest/source references while
// painting (the server recomputes both authoritatively in esFinishSpot).
function enclosingCircle(
  pts: number[],
  radius: number,
  frameW: number,
  frameH: number,
): { cx: number; cy: number; rad: number } {
  const L = Math.max(frameW, frameH);
  const r = radius * L;
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (let i = 0; i + 1 < pts.length; i += 2) {
    const px = pts[i] * frameW;
    const py = pts[i + 1] * frameH;
    minX = Math.min(minX, px - r);
    maxX = Math.max(maxX, px + r);
    minY = Math.min(minY, py - r);
    maxY = Math.max(maxY, py + r);
  }
  if (minX > maxX) return { cx: 0.5, cy: 0.5, rad: 0 };
  return {
    cx: (minX + maxX) / 2 / frameW,
    cy: (minY + maxY) / 2 / frameH,
    rad: Math.hypot(maxX - minX, maxY - minY) / 2 / L,
  };
}

// interimSource offsets a dest reference toward the frame center so the live
// preview shows a plausible fill until the server picks the real patch.
function interimSource(
  cx: number,
  cy: number,
  rad: number,
  frameW: number,
  frameH: number,
): [number, number] {
  const L = Math.max(frameW, frameH);
  const dx = 0.5 - cx;
  const dy = 0.5 - cy;
  const mag = Math.hypot(dx * frameW, dy * frameH) || 1;
  const off = (2.5 * rad * L) / mag;
  return [Math.min(1, Math.max(0, cx + dx * off)), Math.min(1, Math.max(0, cy + dy * off))];
}

// HealOverlay is the on-canvas editor for retouch spots. In the circle tool a
// click (or click-drag to size) places a spot; in the brush tool a drag paints
// a stroke region (a Kind "stroke" spot, one gesture = one spot). Either way
// the destination and source are draggable afterwards, tied by a connector
// line. Like MaskOverlay it sits over the displayed (cropped, straightened)
// image and round-trips every pointer position through frameFromDisplay/
// displayFromFrame — the twin of the Go maskFrame mapping — so the shapes stay
// glued to the same image content the backend heals. Spot geometry lives
// inside draft.spots, so placement/drags flow through esUpdateSpot → the
// ordinary low-res draft render, committing on release; the source patch is
// chosen server-side once at release (esFinishSpot).
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
  const spotTool = useEditSession((s) => s.spotTool);
  const brushRadius = useEditSession((s) => s.spotBrushRadius);
  const brushFeather = useEditSession((s) => s.spotBrushFeather);
  const [cursor, setCursor] = useState<[number, number] | null>(null);
  const [dragging, setDragging] = useState(false);

  const spots = draft.spots ?? [];
  const L = Math.max(frameW, frameH);
  // Uniform frame-px → box-px scale (the crop is the same scale on both axes).
  const k = boxW / ((draft.cropW > 0 ? draft.cropW : 1) * frameW);
  // Default radius for a new spot ≈ 20 CSS px at the current zoom (frame
  // fraction of the long edge) — also the placement cursor's footprint.
  const defRadius = Math.min(0.05, Math.max(0.003, 20 / (L * k)));
  const cursorRadius = spotTool === 'brush' ? brushRadius : defRadius;

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
  const clampRadius = (r: number) => Math.min(SPOT_RADIUS_MAX, r);
  // Box-px SVG path through a stroke's points (rotation-aware per point).
  const strokePath = (pts: number[], dx = 0, dy = 0) => {
    let d = '';
    for (let i = 0; i + 1 < pts.length; i += 2) {
      const [bx, by] = toBoxPx(pts[i] + dx, pts[i + 1] + dy);
      d += `${i === 0 ? 'M' : 'L'}${bx.toFixed(1)} ${by.toFixed(1)}`;
    }
    // A single-point stroke still needs visible geometry: a zero-length line
    // with round caps renders as a dot.
    if (pts.length === 2) {
      const [bx, by] = toBoxPx(pts[0] + dx, pts[1] + dy);
      d += `L${(bx + 0.01).toFixed(2)} ${by.toFixed(1)}`;
    }
    return d;
  };

  // --- placement (circle: create + size drag) ---
  const place = useRef<{ index: number; center: [number, number] } | null>(null);
  // --- painting (brush: create + extend stroke) ---
  const paint = useRef<{ index: number; stroke: Stroke } | null>(null);

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
    if (spotTool === 'brush') {
      const stroke: Stroke = {
        radius: quant4(brushRadius),
        feather: quant4(brushFeather),
        pts: [quant4(cx), quant4(cy)],
      };
      const c = enclosingCircle(stroke.pts, stroke.radius, frameW, frameH);
      const [sx, sy] = interimSource(c.cx, c.cy, c.rad, frameW, frameH);
      const index = esBeginSpot(client, {
        kind: 'stroke', strokes: [stroke],
        cx: quant4(c.cx), cy: quant4(c.cy), radius: 0,
        sx: quant4(sx), sy: quant4(sy),
      });
      if (index < 0) return;
      paint.current = { index, stroke };
      setDragging(true);
      return;
    }
    // Interim source: offset toward the frame center by 2.5 radii, so the live
    // preview shows a plausible fill until the server picks the real patch.
    const [sx, sy] = interimSource(cx, cy, defRadius, frameW, frameH);
    const index = esBeginSpot(client, {
      cx: quant4(cx), cy: quant4(cy), radius: quant4(defRadius),
      sx: quant4(sx), sy: quant4(sy), feather: SPOT_FEATHER_DEFAULT,
    });
    if (index < 0) return;
    place.current = { index, center: [cx, cy] };
    setDragging(true);
  };
  const placeMove = (e: React.PointerEvent) => {
    setCursor(pointFrac(e));
    const pt = paint.current;
    if (pt) {
      const [bx, by] = pointFrac(e);
      const [fx, fy] = toFrame(bx, by);
      const st = pt.stroke;
      // Decimate: skip points closer than a quarter radius (long-edge units,
      // matching the server's stamp spacing) so strokes stay compact.
      const lx = st.pts[st.pts.length - 2];
      const ly = st.pts[st.pts.length - 1];
      if (Math.hypot((fx - lx) * frameW, (fy - ly) * frameH) < (st.radius * L) / 4) return;
      st.pts.push(quant4(fx), quant4(fy));
      const c = enclosingCircle(st.pts, st.radius, frameW, frameH);
      const [sx, sy] = interimSource(c.cx, c.cy, c.rad, frameW, frameH);
      esUpdateSpot(client, pt.index, {
        strokes: [{ ...st, pts: [...st.pts] }],
        cx: quant4(c.cx), cy: quant4(c.cy), sx: quant4(sx), sy: quant4(sy),
      });
      return;
    }
    const p = place.current;
    if (!p) return;
    const [bx, by] = pointFrac(e);
    const [fx, fy] = toFrame(bx, by);
    // Drag beyond the default footprint grows the spot; a plain click keeps it.
    const dist = Math.hypot((fx - p.center[0]) * frameW, (fy - p.center[1]) * frameH) / L;
    esUpdateSpot(client, p.index, { radius: quant4(clampRadius(Math.max(defRadius, dist))) });
  };
  const placeEnd = (e: React.PointerEvent) => {
    const index = paint.current?.index ?? place.current?.index;
    if (index == null) return;
    place.current = null;
    paint.current = null;
    setDragging(false);
    try {
      rootRef.current?.releasePointerCapture(e.pointerId);
    } catch {
      // see beginCreate
    }
    void esFinishSpot(client, index);
  };

  // --- handle drags (dest center, source center, radius) ---
  // The dragged spot's index lives in the ref, NOT in the render-scope
  // activeSpot: grabbing an inactive spot selects it and drags in the same
  // gesture, and the first pointermoves land before React re-renders with the
  // new selection — routing them by activeSpot would move the old spot.
  const grip = useRef<{ kind: string; index: number; start: Spot; startFrame: [number, number] } | null>(null);
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
    grip.current = { kind, index, start: spot, startFrame: toFrame(bx, by) };
    setDragging(true);
  };
  const gripMove = (e: React.PointerEvent) => {
    const g = grip.current;
    if (g == null) return;
    const [bx, by] = pointFrac(e);
    const [fx, fy] = toFrame(bx, by);
    const s = g.start;
    let patch: Partial<Spot> | null = null;
    switch (g.kind) {
      case 'dest': {
        const dx = fx - g.startFrame[0];
        const dy = fy - g.startFrame[1];
        patch = { cx: quant4(s.cx + dx), cy: quant4(s.cy + dy) };
        if (s.kind === 'stroke') {
          // Moving a painted region translates its strokes with the reference.
          patch.strokes = (s.strokes ?? []).map((st) => ({
            ...st,
            pts: st.pts.map((v, i) => quant4(v + (i % 2 === 0 ? dx : dy))),
          }));
        }
        break;
      }
      case 'source':
        patch = { sx: quant4(s.sx + (fx - g.startFrame[0])), sy: quant4(s.sy + (fy - g.startFrame[1])) };
        break;
      case 'radius': {
        const dist = Math.hypot((fx - s.cx) * frameW, (fy - s.cy) * frameH) / L;
        patch = { radius: quant4(clampRadius(Math.max(0.002, dist))) };
        break;
      }
    }
    if (patch) esUpdateSpot(client, g.index, patch);
  };
  const gripEnd = (e: React.PointerEvent) => {
    if (!grip.current) return;
    grip.current = null;
    setDragging(false);
    (e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId);
    esCommit(client);
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
          if (spot.kind === 'stroke') {
            const strokes = spot.strokes ?? [];
            const sw = (st: Stroke) => Math.max(3, 2 * st.radius * L * k);
            if (!active) {
              // Inactive painted region: a faint pass over the stroke path,
              // clickable to select.
              return (
                <g key={i}>
                  {strokes.map((st, j) => (
                    <path
                      key={j}
                      d={strokePath(st.pts)}
                      fill="none" stroke="white" strokeOpacity=".4"
                      strokeWidth={sw(st)} strokeLinecap="round" strokeLinejoin="round"
                      className="pointer-events-auto cursor-pointer"
                      pointerEvents="stroke"
                      onPointerDown={(e) => beginGrip(e, 'dest', i)}
                      onPointerMove={gripMove}
                      onPointerUp={gripEnd}
                      onPointerCancel={gripEnd}
                    />
                  ))}
                </g>
              );
            }
            const [dcx, dcy] = toBoxPx(spot.cx, spot.cy);
            const [scx, scy] = toBoxPx(spot.sx, spot.sy);
            const dx = spot.sx - spot.cx;
            const dy = spot.sy - spot.cy;
            return (
              <g key={i}>
                <line x1={dcx} y1={dcy} x2={scx} y2={scy} stroke="white" strokeOpacity=".7" strokeDasharray="4 3" />
                {/* Source region (translated copy, dashed outline effect via low opacity) */}
                {strokes.map((st, j) => (
                  <path
                    key={`s${j}`}
                    d={strokePath(st.pts, dx, dy)}
                    fill="none" stroke="white" strokeOpacity=".35"
                    strokeWidth={sw(st)} strokeLinecap="round" strokeLinejoin="round"
                    strokeDasharray="5 3"
                  />
                ))}
                {/* Destination region */}
                {strokes.map((st, j) => (
                  <path
                    key={`d${j}`}
                    d={strokePath(st.pts)}
                    fill="none" stroke="rgba(120,180,255,.35)"
                    strokeWidth={sw(st)} strokeLinecap="round" strokeLinejoin="round"
                  />
                ))}
                {strokes.map((st, j) => (
                  <path
                    key={`o${j}`}
                    d={strokePath(st.pts)}
                    fill="none" stroke="white" strokeOpacity=".95" strokeWidth={1.5}
                    strokeLinecap="round" strokeLinejoin="round"
                  />
                ))}
              </g>
            );
          }
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
                onPointerMove={gripMove}
                onPointerUp={gripEnd}
                onPointerCancel={gripEnd}
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
            width: cursorRadius * L * k * 2,
            height: cursorRadius * L * k * 2,
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
// destination, move the source, and (circles only — a painted region has no
// single radius) resize via a dot on the destination ring's east point.
// Reuses MaskOverlay's Dot so the grip styling can't drift.
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
  const beginAt = (e: React.PointerEvent, kind: string) => begin(e, kind, index);
  return (
    <>
      <Dot at={[dcx, dcy]} cursor="move" grip="dest" begin={beginAt} move={move} end={end} title="Move fill" />
      <Dot at={[scx, scy]} cursor="move" grip="source" begin={beginAt} move={move} end={end} title="Move source" />
      {spot.kind !== 'stroke' && (
        <Dot at={[dcx + r, dcy]} cursor="ew-resize" grip="radius" begin={beginAt} move={move} end={end} title="Size" />
      )}
    </>
  );
}
