import { useEffect, useMemo, useRef, useState } from 'react';
import type { Mask, Params, Stroke } from '@/api/edit';
import type { ApiClient } from '@/api/client';
import { cn } from '@/lib/utils';
import { displayFromFrame, frameFromDisplay } from '@/lib/crop';
import { esCommit, esUpdateMask, useEditSession } from '@/lib/editSession';

// MaskOverlay is the on-canvas editor for the selected local-adjustment mask:
// draggable shape handles for linear/radial masks, stroke painting for brush
// masks, and a red tint visualizing the mask's weight while the shape is
// being manipulated. It sits over the displayed (cropped, straightened)
// image; mask geometry lives in oriented-frame fractions, so every pointer
// position round-trips through frameFromDisplay/displayFromFrame — the twin
// of the Go maskFrame mapping, which keeps the handles glued to the same
// image content the backend adjusts.
//
// Unlike the crop overlay there is no client-only preview: mask changes alter
// pixels, so shape drags flow through esUpdateMask → the ordinary low-res
// draft renders, committing on release.
export function MaskOverlay({
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
  const activeMask = useEditSession((s) => s.activeMask);
  const maskPaint = useEditSession((s) => s.maskPaint);
  const brushRadius = useEditSession((s) => s.brushRadius);
  const brushFeather = useEditSession((s) => s.brushFeather);
  const brushFlow = useEditSession((s) => s.brushFlow);
  const brushErase = useEditSession((s) => s.brushErase);
  const [dragging, setDragging] = useState(false);
  const [cursor, setCursor] = useState<[number, number] | null>(null);

  const masks = draft.masks ?? [];
  const mask = activeMask != null ? masks[activeMask] : undefined;
  const painting = maskPaint && mask?.type === 'brush';

  // Pointer → fraction of the displayed box (unclamped: mask geometry may
  // legitimately sit off-frame, e.g. a radial center dragged past the edge).
  const pointFrac = (e: React.PointerEvent): [number, number] => {
    const r = rootRef.current!.getBoundingClientRect();
    return [(e.clientX - r.left) / r.width, (e.clientY - r.top) / r.height];
  };
  const toFrame = (bx: number, by: number) => frameFromDisplay(bx, by, draft, frameW, frameH);
  const toBoxPx = (fx: number, fy: number): [number, number] => {
    const [bx, by] = displayFromFrame(fx, fy, draft, frameW, frameH);
    return [bx * boxW, by * boxH];
  };
  // Uniform frame-px → box-px scale (the crop is the same scale on both axes).
  const k = boxW / ((draft.cropW > 0 ? draft.cropW : 1) * frameW);

  // --- shape handle drags (linear A/B/line, radial move/rx/ry/rotate) ---
  const drag = useRef<{
    grip: string;
    start: Mask;
    startFrame: [number, number];
  } | null>(null);

  const beginDrag = (e: React.PointerEvent, grip: string) => {
    if (activeMask == null || !mask) return;
    e.stopPropagation();
    e.preventDefault();
    try {
      (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
    } catch {
      // synthetic test pointers can't be captured; the drag still works
    }
    const [bx, by] = pointFrac(e);
    drag.current = { grip, start: mask, startFrame: toFrame(bx, by) };
    setDragging(true);
  };

  const onDragMove = (e: React.PointerEvent) => {
    const d = drag.current;
    if (!d || activeMask == null) return;
    const [bx, by] = pointFrac(e);
    const [fx, fy] = toFrame(bx, by);
    const dfx = fx - d.startFrame[0];
    const dfy = fy - d.startFrame[1];
    const s = d.start;
    const q = (v: number) => Math.round(v * 1e4) / 1e4;
    let patch: Partial<Mask> | null = null;
    switch (d.grip) {
      case 'a':
        patch = { x0: q(fx), y0: q(fy) };
        break;
      case 'b':
        patch = { x1: q(fx), y1: q(fy) };
        break;
      case 'line':
        patch = {
          x0: q((s.x0 ?? 0) + dfx), y0: q((s.y0 ?? 0) + dfy),
          x1: q((s.x1 ?? 0) + dfx), y1: q((s.y1 ?? 0) + dfy),
        };
        break;
      case 'center':
        patch = { cx: q((s.cx ?? 0) + dfx), cy: q((s.cy ?? 0) + dfy) };
        break;
      case 'rx':
      case 'ry': {
        // Radius follows the pointer's distance from the center along the
        // handle's (rotated) axis, measured in frame pixels.
        const px = (fx - (s.cx ?? 0)) * frameW;
        const py = (fy - (s.cy ?? 0)) * frameH;
        const rad = ((s.angle ?? 0) * Math.PI) / 180;
        const axisX = d.grip === 'rx' ? Math.cos(rad) : -Math.sin(rad);
        const axisY = d.grip === 'rx' ? Math.sin(rad) : Math.cos(rad);
        const dist = Math.abs(px * axisX + py * axisY);
        patch =
          d.grip === 'rx'
            ? { rx: q(Math.max(0.01, dist / frameW)) }
            : { ry: q(Math.max(0.01, dist / frameH)) };
        break;
      }
      case 'rotate': {
        const px = (fx - (s.cx ?? 0)) * frameW;
        const py = (fy - (s.cy ?? 0)) * frameH;
        // The rotate handle sits on the -y (top) axis of the ellipse.
        const deg = (Math.atan2(py, px) * 180) / Math.PI + 90;
        patch = { angle: q(((deg % 360) + 360) % 360) };
        break;
      }
    }
    if (patch) esUpdateMask(client, activeMask, patch);
  };

  const endDrag = (e: React.PointerEvent) => {
    if (!drag.current) return;
    drag.current = null;
    setDragging(false);
    (e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId);
    esCommit(client);
  };

  // --- brush painting ---
  const stroke = useRef<Stroke | null>(null);
  const beginPaint = (e: React.PointerEvent) => {
    if (!painting || activeMask == null || !mask || e.button !== 0) return;
    e.stopPropagation();
    e.preventDefault();
    try {
      (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
    } catch {
      // see beginDrag
    }
    const [bx, by] = pointFrac(e);
    const [fx, fy] = toFrame(bx, by);
    const q = (v: number) => Math.round(v * 1e4) / 1e4;
    // Snapshot the committed strokes NOW, before the first live update lands
    // in the draft — pushStroke re-sends [snapshot + live stroke] each move,
    // so reading the draft later would double-append the in-progress stroke.
    baseStrokes.current = (useEditSession.getState().draft?.masks?.[activeMask]?.strokes ?? []).slice();
    stroke.current = {
      erase: brushErase || undefined,
      radius: q(brushRadius),
      feather: q(brushFeather),
      flow: brushFlow >= 1 ? undefined : q(brushFlow),
      pts: [q(fx), q(fy)],
    };
    setDragging(true);
    pushStroke();
  };
  const paintMove = (e: React.PointerEvent) => {
    setCursor(pointFrac(e));
    const st = stroke.current;
    if (!st || activeMask == null) return;
    const [bx, by] = pointFrac(e);
    const [fx, fy] = toFrame(bx, by);
    // Decimate: skip points closer than a quarter radius (long-edge units,
    // matching the server's stamp spacing) so strokes stay compact.
    const L = Math.max(frameW, frameH);
    const lx = st.pts[st.pts.length - 2];
    const ly = st.pts[st.pts.length - 1];
    const dist = Math.hypot((fx - lx) * frameW, (fy - ly) * frameH);
    if (dist < (st.radius * L) / 4) return;
    const q = (v: number) => Math.round(v * 1e4) / 1e4;
    st.pts.push(q(fx), q(fy));
    pushStroke();
  };
  const endPaint = (e: React.PointerEvent) => {
    if (!stroke.current) return;
    stroke.current = null;
    baseStrokes.current = null;
    setDragging(false);
    (e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId);
    esCommit(client);
  };
  // pushStroke sends [pointerdown snapshot + the in-progress stroke] as the
  // draft's stroke list (previewed by the ordinary low-res render path).
  const pushStroke = () => {
    const st = stroke.current;
    const base = baseStrokes.current;
    if (!st || !base || activeMask == null) return;
    esUpdateMask(client, activeMask, { strokes: [...base, { ...st, pts: [...st.pts] }] });
  };
  // Stroke list as of pointerdown, captured synchronously in beginPaint.
  const baseStrokes = useRef<Stroke[] | null>(null);

  if (!mask || activeMask == null) return null;

  // Show the weight tint while the shape is being manipulated (or always in
  // paint mode — you need to see where you've painted).
  const showTint = dragging || painting;

  return (
    <div
      ref={rootRef}
      className={cn('absolute inset-0 z-10 select-none', painting ? 'touch-none cursor-crosshair' : 'pointer-events-none')}
      data-testid="mask-overlay"
      onPointerDown={beginPaint}
      onPointerMove={paintMove}
      onPointerUp={endPaint}
      onPointerCancel={endPaint}
      onPointerLeave={() => setCursor(null)}
    >
      {showTint && (
        <MaskTint mask={mask} draft={draft} frameW={frameW} frameH={frameH} boxW={boxW} boxH={boxH} k={k} />
      )}
      {mask.type === 'linear' && (
        <LinearHandles
          mask={mask}
          toBoxPx={toBoxPx}
          begin={beginDrag}
          move={onDragMove}
          end={endDrag}
        />
      )}
      {mask.type === 'radial' && (
        <RadialHandles
          mask={mask}
          draft={draft}
          frameW={frameW}
          frameH={frameH}
          k={k}
          toBoxPx={toBoxPx}
          begin={beginDrag}
          move={onDragMove}
          end={endDrag}
        />
      )}
      {painting && cursor && (
        // Brush cursor: the stamp footprint at the pointer.
        <div
          className="pointer-events-none absolute rounded-full border border-white/80 shadow-[0_0_0_1px_rgba(0,0,0,.4)]"
          style={{
            width: brushRadius * Math.max(frameW, frameH) * k * 2,
            height: brushRadius * Math.max(frameW, frameH) * k * 2,
            left: cursor[0] * boxW,
            top: cursor[1] * boxH,
            transform: 'translate(-50%, -50%)',
          }}
        />
      )}
    </div>
  );
}

// Handle dot shared by both parametric shapes.
function Dot({
  at,
  cursor,
  grip,
  begin,
  move,
  end,
  title,
}: {
  at: [number, number];
  cursor: string;
  grip: string;
  begin: (e: React.PointerEvent, grip: string) => void;
  move: (e: React.PointerEvent) => void;
  end: (e: React.PointerEvent) => void;
  title?: string;
}) {
  return (
    <div
      className="pointer-events-auto absolute z-10 size-4 touch-none rounded-full border-2 border-white bg-black/40 shadow-[0_0_0_1px_rgba(0,0,0,.4)]"
      style={{ left: at[0], top: at[1], transform: 'translate(-50%, -50%)', cursor }}
      title={title}
      onPointerDown={(e) => begin(e, grip)}
      onPointerMove={move}
      onPointerUp={end}
      onPointerCancel={end}
    />
  );
}

// LinearHandles: A and B dots plus a grabbable connecting line, with dashed
// guides through A and B perpendicular to the gradient axis.
function LinearHandles({
  mask,
  toBoxPx,
  begin,
  move,
  end,
}: {
  mask: Mask;
  toBoxPx: (fx: number, fy: number) => [number, number];
  begin: (e: React.PointerEvent, grip: string) => void;
  move: (e: React.PointerEvent) => void;
  end: (e: React.PointerEvent) => void;
}) {
  const a = toBoxPx(mask.x0 ?? 0, mask.y0 ?? 0);
  const b = toBoxPx(mask.x1 ?? 0, mask.y1 ?? 0);
  const dx = b[0] - a[0];
  const dy = b[1] - a[1];
  const len = Math.max(1, Math.hypot(dx, dy));
  // Perpendicular guides, long enough to cross any box.
  const G = 4000;
  const px = (-dy / len) * G;
  const py = (dx / len) * G;
  return (
    <>
      <svg className="absolute inset-0 size-full overflow-visible">
        <line x1={a[0] - px} y1={a[1] - py} x2={a[0] + px} y2={a[1] + py} stroke="white" strokeOpacity=".8" strokeDasharray="6 4" />
        <line x1={b[0] - px} y1={b[1] - py} x2={b[0] + px} y2={b[1] + py} stroke="white" strokeOpacity=".5" strokeDasharray="6 4" />
        <line x1={a[0]} y1={a[1]} x2={b[0]} y2={b[1]} stroke="white" strokeOpacity=".9" />
        <line
          x1={a[0]} y1={a[1]} x2={b[0]} y2={b[1]}
          stroke="transparent"
          className="touch-none"
          style={{ cursor: 'move', strokeWidth: 14, pointerEvents: 'stroke' }}
          onPointerDown={(e) => begin(e, 'line')}
          onPointerMove={move}
          onPointerUp={end}
          onPointerCancel={end}
        />
      </svg>
      <Dot at={a} cursor="move" grip="a" begin={begin} move={move} end={end} title="Full-strength edge" />
      <Dot at={b} cursor="move" grip="b" begin={begin} move={move} end={end} title="Zero edge" />
    </>
  );
}

// RadialHandles: the ellipse outline with center (move), edge (rx/ry) and
// rotation dots. Drawn in frame-pixel space under one affine group transform
// (translate to the frame center's box position, straighten rotation, uniform
// scale) so the ellipse and its rotation render exactly where the backend
// evaluates them.
function RadialHandles({
  mask,
  draft,
  frameW,
  frameH,
  k,
  toBoxPx,
  begin,
  move,
  end,
}: {
  mask: Mask;
  draft: Params;
  frameW: number;
  frameH: number;
  k: number;
  toBoxPx: (fx: number, fy: number) => [number, number];
  begin: (e: React.PointerEvent, grip: string) => void;
  move: (e: React.PointerEvent) => void;
  end: (e: React.PointerEvent) => void;
}) {
  const rad = ((mask.angle ?? 0) * Math.PI) / 180;
  const cxF = (mask.cx ?? 0) * frameW;
  const cyF = (mask.cy ?? 0) * frameH;
  const rxPx = (mask.rx ?? 0) * frameW;
  const ryPx = (mask.ry ?? 0) * frameH;
  const centerBox = toBoxPx(mask.cx ?? 0, mask.cy ?? 0);
  // Handle positions: points on the (rotated) ellipse axes, mapped to box px.
  const onAxis = (dist: number, axis: 'x' | 'y'): [number, number] => {
    const ax = axis === 'x' ? Math.cos(rad) : -Math.sin(rad);
    const ay = axis === 'x' ? Math.sin(rad) : Math.cos(rad);
    return toBoxPx((cxF + dist * ax) / frameW, (cyF + dist * ay) / frameH);
  };
  const east = onAxis(rxPx, 'x');
  const south = onAxis(ryPx, 'y');
  const rotDot = onAxis(-ryPx - 24 / k, 'y'); // just beyond the top edge
  const cropAngle = draft.cropAngle ?? 0;
  return (
    <>
      <svg className="absolute inset-0 size-full overflow-visible">
        <g transform={`translate(${centerBox[0]} ${centerBox[1]}) rotate(${cropAngle + (mask.angle ?? 0)}) scale(${k})`}>
          <ellipse
            cx={0} cy={0} rx={rxPx} ry={ryPx}
            fill="none" stroke="white" strokeOpacity=".9" vectorEffect="non-scaling-stroke"
          />
          {(mask.feather ?? 0) > 0 && (
            <ellipse
              cx={0} cy={0}
              rx={rxPx * (1 - (mask.feather ?? 0))} ry={ryPx * (1 - (mask.feather ?? 0))}
              fill="none" stroke="white" strokeOpacity=".4" strokeDasharray="6 4" vectorEffect="non-scaling-stroke"
            />
          )}
          <line x1={0} y1={-ryPx} x2={0} y2={-ryPx - 24 / k} stroke="white" strokeOpacity=".6" vectorEffect="non-scaling-stroke" />
        </g>
      </svg>
      <Dot at={centerBox} cursor="move" grip="center" begin={begin} move={move} end={end} title="Move" />
      <Dot at={east} cursor="ew-resize" grip="rx" begin={begin} move={move} end={end} title="Width" />
      <Dot at={south} cursor="ns-resize" grip="ry" begin={begin} move={move} end={end} title="Height" />
      <Dot at={rotDot} cursor="grab" grip="rotate" begin={begin} move={move} end={end} title="Rotate" />
    </>
  );
}

// MaskTint paints the mask's weight as a red overlay: SVG gradients for the
// parametric shapes, canvas stroke rendering for the brush (an approximation
// of the backend's feathered stamps — round-capped strokes with a blur for
// the feather; the pixels themselves are always the backend's). Exported for
// MaskHoverTint, which shows the same tint while a mask row is hovered.
export function MaskTint({
  mask,
  draft,
  frameW,
  frameH,
  boxW,
  boxH,
  k,
}: {
  mask: Mask;
  draft: Params;
  frameW: number;
  frameH: number;
  boxW: number;
  boxH: number;
  k: number;
}) {
  const id = useMemo(() => `mask-tint-${Math.random().toString(36).slice(2)}`, []);
  const toBoxPx = (fx: number, fy: number): [number, number] => {
    const [bx, by] = displayFromFrame(fx, fy, draft, frameW, frameH);
    return [bx * boxW, by * boxH];
  };
  const RED = 'rgba(240,64,64,';

  if (mask.type === 'linear') {
    const a = toBoxPx(mask.x0 ?? 0, mask.y0 ?? 0);
    const b = toBoxPx(mask.x1 ?? 0, mask.y1 ?? 0);
    const [from, to] = mask.invert ? [b, a] : [a, b];
    return (
      <svg className="pointer-events-none absolute inset-0 size-full">
        <defs>
          <linearGradient id={id} gradientUnits="userSpaceOnUse" x1={from[0]} y1={from[1]} x2={to[0]} y2={to[1]}>
            <stop offset="0" stopColor={`${RED}.4)`} />
            <stop offset="1" stopColor={`${RED}0)`} />
          </linearGradient>
        </defs>
        <rect x={0} y={0} width={boxW} height={boxH} fill={`url(#${id})`} />
      </svg>
    );
  }

  if (mask.type === 'radial') {
    const centerBox = toBoxPx(mask.cx ?? 0, mask.cy ?? 0);
    const feather = Math.max(mask.feather ?? 0, 0.02);
    const solid = Math.max(0, 1 - feather);
    const transform = `translate(${centerBox[0]} ${centerBox[1]}) rotate(${(draft.cropAngle ?? 0) + (mask.angle ?? 0)}) scale(${k})`;
    const rxPx = (mask.rx ?? 0) * frameW;
    const ryPx = (mask.ry ?? 0) * frameH;
    if (!mask.invert) {
      return (
        <svg className="pointer-events-none absolute inset-0 size-full">
          <defs>
            <radialGradient id={id}>
              <stop offset={solid} stopColor={`${RED}.4)`} />
              <stop offset="1" stopColor={`${RED}0)`} />
            </radialGradient>
          </defs>
          <g transform={transform}>
            <ellipse cx={0} cy={0} rx={rxPx} ry={ryPx} fill={`url(#${id})`} />
          </g>
        </svg>
      );
    }
    // Inverted: red everywhere except the ellipse, fading through the feather
    // band (a luminance mask: white background, black-center gradient hole).
    return (
      <svg className="pointer-events-none absolute inset-0 size-full">
        <defs>
          <radialGradient id={`${id}-g`}>
            <stop offset={solid} stopColor="black" />
            <stop offset="1" stopColor="white" />
          </radialGradient>
          <mask id={`${id}-m`}>
            <rect x={0} y={0} width={boxW} height={boxH} fill="white" />
            <g transform={transform}>
              <ellipse cx={0} cy={0} rx={rxPx} ry={ryPx} fill={`url(#${id}-g)`} />
            </g>
          </mask>
        </defs>
        <rect x={0} y={0} width={boxW} height={boxH} fill={`${RED}.4)`} mask={`url(#${id}-m)`} />
      </svg>
    );
  }

  return <BrushTint mask={mask} draft={draft} frameW={frameW} frameH={frameH} boxW={boxW} boxH={boxH} k={k} />;
}

function BrushTint({
  mask,
  draft,
  frameW,
  frameH,
  boxW,
  boxH,
  k,
}: {
  mask: Mask;
  draft: Params;
  frameW: number;
  frameH: number;
  boxW: number;
  boxH: number;
  k: number;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const strokes = mask.strokes ?? [];
  const strokesKey = JSON.stringify(strokes);
  const invert = !!mask.invert;
  useEffect(() => {
    const canvas = ref.current;
    if (!canvas) return;
    const w = Math.max(1, Math.round(boxW));
    const h = Math.max(1, Math.round(boxH));
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.clearRect(0, 0, w, h);
    const L = Math.max(frameW, frameH);
    const drawStroke = (s: Stroke, op: GlobalCompositeOperation) => {
      if (s.pts.length < 2) return;
      const radiusPx = Math.max(1, s.radius * L * k);
      const blur = radiusPx * Math.min(1, Math.max(0, s.feather ?? 0));
      ctx.save();
      ctx.globalCompositeOperation = op;
      ctx.globalAlpha = s.flow && s.flow > 0 ? s.flow : 1;
      // The feathered stamp envelope, approximated as a round-capped stroke
      // with a blur standing in for the feather falloff.
      ctx.filter = blur > 0.5 ? `blur(${blur / 2}px)` : 'none';
      ctx.strokeStyle = 'rgba(240,64,64,1)';
      ctx.fillStyle = 'rgba(240,64,64,1)';
      ctx.lineCap = 'round';
      ctx.lineJoin = 'round';
      ctx.lineWidth = Math.max(1, 2 * (radiusPx - blur / 2));
      if (s.pts.length === 2) {
        const [bx, by] = displayFromFrame(s.pts[0], s.pts[1], draft, frameW, frameH);
        ctx.beginPath();
        ctx.arc(bx * boxW, by * boxH, Math.max(0.5, radiusPx - blur / 2), 0, Math.PI * 2);
        ctx.fill();
      } else {
        ctx.beginPath();
        for (let i = 0; i + 1 < s.pts.length; i += 2) {
          const [bx, by] = displayFromFrame(s.pts[i], s.pts[i + 1], draft, frameW, frameH);
          if (i === 0) ctx.moveTo(bx * boxW, by * boxH);
          else ctx.lineTo(bx * boxW, by * boxH);
        }
        ctx.stroke();
      }
      ctx.restore();
    };
    if (!invert) {
      // In stroke order: paint adds, erase cuts — the backend's compose order.
      for (const s of strokes) drawStroke(s, s.erase ? 'destination-out' : 'source-over');
    } else {
      // Inverted: red everywhere with the painted strokes cut out (erase
      // strokes restore red, so they cut from the cut — approximated by
      // simply skipping them; the pixels themselves are always the backend's).
      ctx.fillStyle = 'rgba(240,64,64,1)';
      ctx.fillRect(0, 0, w, h);
      for (const s of strokes) {
        if (!s.erase) drawStroke(s, 'destination-out');
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [strokesKey, invert, boxW, boxH, frameW, frameH, k, draft.cropX, draft.cropY, draft.cropW, draft.cropH, draft.cropAngle]);
  return (
    <canvas
      ref={ref}
      className="pointer-events-none absolute inset-0 size-full opacity-40"
      aria-hidden
    />
  );
}
