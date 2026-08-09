// The two colour/tone editors the Curve tab is built from: the point tone
// curve (RGB plus per-channel) and the HSL colour mixer. Both are direct
// manipulation surfaces with their own pointer handling, which is why they
// were the largest thing in EditPanel after the masks.

import { CURVE_CHANNELS, CURVE_ENDPOINTS, curveOf, curvePolyline, hasToneCurve } from '@/lib/toneCurve';
import type { CurveKey } from '@/lib/toneCurve';
import type { CurvePoint, Params } from '@/api/edit';
import { EditSlider } from '@/components/edit/controls';
import { cn } from '@/lib/utils';
import { pct } from '@/components/edit/controlUtils';
import { useRef, useState } from 'react';
const MIXER_BANDS = [
  { name: 'Red', color: '#e5484d' },
  { name: 'Orange', color: '#f76b15' },
  { name: 'Yellow', color: '#d9c400' },
  { name: 'Green', color: '#46a758' },
  { name: 'Aqua', color: '#12a594' },
  { name: 'Blue', color: '#3d7dff' },
  { name: 'Purple', color: '#8e4ec6' },
  { name: 'Magenta', color: '#d6409f' },
];
type MixerKey = 'hslHue' | 'hslSat' | 'hslLum';

// ToneCurve is the point-curve editor: a square canvas of draggable control
// points over the developed luminance, mirrored on the render's monotone-cubic
// LUT (pyramid.buildCurveLUT via lib/toneCurve). Endpoints are pinned in x
// (0 and 1) and free in y; interior points slide between their neighbors.
// Click empty space to add a point, double-click a point to remove it.
//
// The RGB/R/G/B tabs switch which channel is being edited: RGB is the master
// (Params.toneCurve, overall tone), R/G/B the per-channel color grade applied
// on top (Params.toneCurveR/G/B) — the render's composition order. The
// unselected channels stay drawn as faint guides. An identity/empty curve
// folds to undefined (neutral). Follows the ColorMixer widget contract.
const CURVE_MAX_POINTS = 16;
const CURVE_MIN_GAP = 0.02; // keep interior points from stacking on the wire

export function ToneCurve({
  draft,
  update,
  commit,
  clear,
}: {
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  clear: (patch: Partial<Params>) => void;
}) {
  const svgRef = useRef<SVGSVGElement>(null);
  const [dragIdx, setDragIdx] = useState<number | null>(null);
  const [chan, setChan] = useState<CurveKey>('toneCurve');
  const channel = CURVE_CHANNELS.find((c) => c.key === chan)!;
  const stored = curveOf(draft, chan);
  // The displayed points: the stored curve, or the identity endpoints for an
  // untouched curve (materialized into the draft on first edit).
  const pts: CurvePoint[] = stored && stored.length >= 2 ? stored : CURVE_ENDPOINTS;
  const active = hasToneCurve(stored);

  const round4 = (v: number) => Math.round(v * 1e4) / 1e4;
  const clampUnit = (v: number) => (v < 0 ? 0 : v > 1 ? 1 : v);

  // ptrUnit maps a pointer event to curve space (x right, y up), clamped.
  const ptrUnit = (e: React.PointerEvent | React.MouseEvent): CurvePoint => {
    const r = svgRef.current!.getBoundingClientRect();
    return {
      x: clampUnit((e.clientX - r.left) / r.width),
      y: clampUnit(1 - (e.clientY - r.top) / r.height),
    };
  };

  // withMoved returns a fresh point list with point `idx` moved to (x,y):
  // endpoints keep their x, interior points are penned between neighbors.
  const withMoved = (list: CurvePoint[], idx: number, x: number, y: number): CurvePoint[] => {
    const next = list.map((p) => ({ ...p }));
    const nx =
      idx === 0
        ? 0
        : idx === next.length - 1
          ? 1
          : Math.min(next[idx + 1].x - CURVE_MIN_GAP, Math.max(next[idx - 1].x + CURVE_MIN_GAP, x));
    next[idx] = { x: round4(nx), y: round4(clampUnit(y)) };
    return next;
  };

  // emit previews (drag) or persists (release) for the SELECTED channel.
  // Mid-drag it always keeps the explicit points so the dragged index stays
  // valid; on release an identity curve folds to undefined (neutral).
  const preview = (next: CurvePoint[]) => update({ [chan]: next });
  const persist = (next: CurvePoint[]) =>
    commit({ [chan]: hasToneCurve(next) ? next : undefined });

  // beginDrag arms a drag on point `idx`. preventDefault is the load-bearing
  // part: the widget sits between the "Curve" heading and the help paragraph,
  // so without it the browser reads the same gesture as a text selection of
  // the panel — the cursor turns into a drag ghost and the rest of the drag
  // goes to the selection instead of to us. Capture keeps a pointer that
  // wanders outside the square still moving the point.
  const beginDrag = (e: React.PointerEvent, idx: number) => {
    e.preventDefault();
    try {
      svgRef.current!.setPointerCapture(e.pointerId);
    } catch {
      // synthetic test pointers can't be captured; the drag still works
    }
    setDragIdx(idx);
  };

  const onBackgroundDown = (e: React.PointerEvent) => {
    if (e.button !== 0) return; // let the other buttons through untouched
    const u = ptrUnit(e);
    // A press level with an existing point is a grab of that point, not an
    // ambiguous new one stacked on its x.
    const near = pts.findIndex((p) => Math.abs(p.x - u.x) < CURVE_MIN_GAP);
    if (near !== -1) {
      beginDrag(e, near);
      return;
    }
    if (pts.length >= CURVE_MAX_POINTS) return;
    const next = [...pts.map((p) => ({ ...p })), { x: round4(u.x), y: round4(u.y) }].sort(
      (a, b) => a.x - b.x,
    );
    beginDrag(e, next.findIndex((p) => p.x === round4(u.x)));
    preview(next);
  };

  const onPointDown = (e: React.PointerEvent, idx: number) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    beginDrag(e, idx);
  };

  // endDrag persists wherever the point landed. It runs on release, on cancel,
  // and on a capture we lost to something else — a half-finished drag must
  // never leave dragIdx set, or the point would follow the bare cursor around
  // afterwards. The dragIdx guard makes the repeat calls (releasing capture
  // here itself fires lostpointercapture) no-ops.
  const endDrag = (e: React.PointerEvent) => {
    if (dragIdx == null) return;
    if (svgRef.current?.hasPointerCapture(e.pointerId)) {
      svgRef.current.releasePointerCapture(e.pointerId);
    }
    setDragIdx(null);
    persist(pts);
  };

  const onMove = (e: React.PointerEvent) => {
    if (dragIdx == null) return;
    if (e.buttons === 0) return endDrag(e); // button released where we couldn't see it
    const u = ptrUnit(e);
    preview(withMoved(pts, dragIdx, u.x, u.y));
  };

  const removePoint = (idx: number) => {
    if (idx === 0 || idx === pts.length - 1) return; // endpoints stay
    persist(pts.filter((_, i) => i !== idx));
  };

  const polyOf = (list: CurvePoint[]) =>
    curvePolyline(list).map((p) => `${p.x * 100},${(1 - p.y) * 100}`).join(' ');
  const line = polyOf(pts);
  // The other channels stay drawn faintly, so a color grade is readable while
  // editing any one of them.
  const guides = CURVE_CHANNELS.filter((c) => c.key !== chan)
    .map((c) => ({ c, curve: curveOf(draft, c.key) }))
    .filter((g) => hasToneCurve(g.curve));

  return (
    <div className="flex flex-col gap-1 pt-2">
      <div className="flex items-center justify-between">
        <span className="text-xs text-muted-foreground">Curve</span>
        <div className="flex items-center gap-1.5">
          <div className="flex items-center gap-0.5" role="group" aria-label="Curve channel">
            {CURVE_CHANNELS.map((c) => (
              <button
                key={c.key}
                type="button"
                onClick={() => setChan(c.key)}
                aria-pressed={chan === c.key}
                title={c.key === 'toneCurve' ? 'Master (RGB) curve' : `${c.label} channel curve`}
                className={cn(
                  'rounded px-1.5 py-0.5 font-mono text-[10px] leading-none transition-colors',
                  chan === c.key
                    ? 'bg-primary/15 text-foreground'
                    : 'text-muted-foreground hover:text-foreground',
                )}
              >
                {c.label}
                {hasToneCurve(curveOf(draft, c.key)) && (
                  <span className="ml-0.5 align-super text-[8px] text-primary">•</span>
                )}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => clear({ [chan]: undefined })}
            className={cn(
              'text-[11px] text-muted-foreground hover:text-foreground',
              !active && 'invisible',
            )}
          >
            Reset
          </button>
        </div>
      </div>
      <svg
        ref={svgRef}
        viewBox="0 0 100 100"
        className="aspect-square w-full touch-none rounded-md border border-border bg-muted/30 select-none"
        role="group"
        aria-label="Tone curve"
        data-channel={chan}
        onPointerDown={onBackgroundDown}
        onPointerMove={onMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        onLostPointerCapture={endDrag}
      >
        {[25, 50, 75].map((g) => (
          <g key={g}>
            <line x1={g} y1={0} x2={g} y2={100} className="stroke-border/50" strokeWidth={0.5} />
            <line x1={0} y1={g} x2={100} y2={g} className="stroke-border/50" strokeWidth={0.5} />
          </g>
        ))}
        <line x1={0} y1={100} x2={100} y2={0} className="stroke-border" strokeWidth={0.75} strokeDasharray="2 2" />
        {guides.map(({ c, curve }) => (
          <polyline
            key={c.key}
            points={polyOf(curve!)}
            fill="none"
            stroke={c.stroke === 'currentColor' ? undefined : c.stroke}
            className={cn('opacity-35', c.stroke === 'currentColor' && 'stroke-primary')}
            strokeWidth={1}
            strokeLinejoin="round"
          />
        ))}
        <polyline
          points={line}
          fill="none"
          stroke={channel.stroke === 'currentColor' ? undefined : channel.stroke}
          className={cn(channel.stroke === 'currentColor' && 'stroke-primary')}
          strokeWidth={1.5}
          strokeLinejoin="round"
        />
        {pts.map((p, i) => (
          <circle
            key={i}
            cx={p.x * 100}
            cy={(1 - p.y) * 100}
            r={dragIdx === i ? 3 : 2.4}
            stroke={channel.stroke === 'currentColor' ? undefined : channel.stroke}
            className={cn('fill-background', channel.stroke === 'currentColor' && 'stroke-primary')}
            strokeWidth={1.25}
            style={{ cursor: dragIdx === i ? 'grabbing' : 'grab' }}
            onPointerDown={(e) => onPointDown(e, i)}
            onDoubleClick={(e) => {
              e.stopPropagation();
              removePoint(i);
            }}
          />
        ))}
      </svg>
    </div>
  );
}

export function ColorMixer({
  draft,
  update,
  commit,
  clear,
}: {
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  clear: (patch: Partial<Params>) => void;
}) {
  const [band, setBand] = useState(0);
  const bandPatch = (key: MixerKey, v: number): Partial<Params> => {
    const next = [...draft[key]] as Params[MixerKey];
    next[band] = v;
    return { [key]: next };
  };
  const val = (key: MixerKey) => draft[key][band] ?? 0;
  const bandChanged = (i: number) =>
    draft.hslHue[i] !== 0 || draft.hslSat[i] !== 0 || draft.hslLum[i] !== 0;

  return (
    <div className="flex flex-col">
      <div className="flex items-center gap-2 pt-2 pb-1" role="group" aria-label="Color mixer band">
        <span className="text-[11px] text-muted-foreground">Mixer</span>
        <div className="flex flex-1 items-center justify-end gap-[7px]">
          {MIXER_BANDS.map((b, i) => (
            <button
              key={b.name}
              onClick={() => setBand(i)}
              title={`${b.name} band`}
              aria-label={`${b.name} band`}
              aria-pressed={band === i}
              className={cn(
                'relative size-[16px] rounded-full transition-opacity',
                band === i
                  ? 'ring-2 ring-ring ring-offset-1 ring-offset-background'
                  : 'opacity-70 hover:opacity-100',
              )}
              style={{ backgroundColor: b.color }}
            >
              {bandChanged(i) && (
                <span className="absolute -top-[3px] -right-[3px] size-[6px] rounded-full border border-background bg-primary" />
              )}
            </button>
          ))}
        </div>
      </div>
      <EditSlider
        label={`${MIXER_BANDS[band].name} hue`}
        value={val('hslHue') * 100}
        display={
          val('hslHue') === 0 ? '0°' : `${val('hslHue') > 0 ? '+' : ''}${Math.round(val('hslHue') * 30)}°`
        }
        min={-100}
        max={100}
        step={2}
        neutral={0}
        onChange={(v) => update(bandPatch('hslHue', v / 100))}
        onCommit={(v) => commit(bandPatch('hslHue', v / 100))}
        onClear={() => clear(bandPatch('hslHue', 0))}
      />
      <EditSlider
        label={`${MIXER_BANDS[band].name} saturation`}
        value={val('hslSat') * 100}
        display={pct(val('hslSat'))}
        min={-100}
        max={100}
        step={2}
        neutral={0}
        onChange={(v) => update(bandPatch('hslSat', v / 100))}
        onCommit={(v) => commit(bandPatch('hslSat', v / 100))}
        onClear={() => clear(bandPatch('hslSat', 0))}
      />
      <EditSlider
        label={`${MIXER_BANDS[band].name} luminance`}
        value={val('hslLum') * 100}
        display={pct(val('hslLum'))}
        min={-100}
        max={100}
        step={2}
        neutral={0}
        onChange={(v) => update(bandPatch('hslLum', v / 100))}
        onCommit={(v) => commit(bandPatch('hslLum', v / 100))}
        onClear={() => clear(bandPatch('hslLum', 0))}
      />
    </div>
  );
}


// A Delta with every field untouched; spread and override to build one.
