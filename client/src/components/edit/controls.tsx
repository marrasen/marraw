// The panel's leaf controls: the sliders every develop and mask row is built
// from, plus the row of segmented buttons and the scroll-into-view hook they
// share. Extracted from EditPanel, which had grown to hold five features and
// the widget library they are all drawn with — this is the widget library.
//
// Nothing here knows about edit state: each takes a value and reports changes.
// The split between onChange and onCommit is the contract that matters — a
// drag previews continuously and persists once on release.
import { useState } from 'react';
import { RotateCcw } from 'lucide-react';

import type { Params } from '@/api/edit';
import { Slider } from '@/components/ui/slider';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { pct, useActiveScroll } from '@/components/edit/controlUtils';
import { cn } from '@/lib/utils';



export type PctField =
  | 'contrast'
  | 'whites'
  | 'blacks'
  | 'toneShadows'
  | 'toneHighlights'
  | 'saturation'
  | 'vibrance'
  | 'vignette'
  | 'texture'
  | 'clarity'
  | 'dehaze'
  | 'caRed'
  | 'caBlue';

export function PctSlider({
  label,
  hotkey,
  field,
  draft,
  update,
  commit,
  active,
  onFocusControl,
}: {
  label: string;
  hotkey?: string;
  field: PctField;
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  active?: boolean;
  onFocusControl?: () => void;
}) {
  return (
    <EditSlider
      label={label}
      hotkey={hotkey}
      value={draft[field] * 100}
      display={pct(draft[field])}
      min={-100}
      max={100}
      step={2}
      neutral={0}
      onChange={(v) => update({ [field]: v / 100 })}
      onCommit={(v) => commit({ [field]: v / 100 })}
      onClear={() => {
        update({ [field]: 0 });
        commit({ [field]: 0 });
      }}
      active={active}
      onFocusControl={onFocusControl}
    />
  );
}

export function HueSlider({
  label,
  field,
  draft,
  update,
  commit,
  active,
  onFocusControl,
}: {
  label: string;
  field: 'splitShadowHue' | 'splitHighlightHue';
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  active?: boolean;
  onFocusControl?: () => void;
}) {
  return (
    <EditSlider
      label={label}
      value={draft[field]}
      display={`${Math.round(draft[field])}°`}
      min={0}
      max={359}
      step={5}
      neutral={0}
      onChange={(v) => update({ [field]: v })}
      onCommit={(v) => commit({ [field]: v })}
      onClear={() => {
        update({ [field]: 0 });
        commit({ [field]: 0 });
      }}
      active={active}
      onFocusControl={onFocusControl}
    />
  );
}

export function AmtSlider({
  label,
  field,
  draft,
  update,
  commit,
  active,
  onFocusControl,
}: {
  label: string;
  field: 'splitShadowAmt' | 'splitHighlightAmt';
  draft: Params;
  update: (patch: Partial<Params>) => void;
  commit: (patch?: Partial<Params>) => void;
  active?: boolean;
  onFocusControl?: () => void;
}) {
  return (
    <EditSlider
      label={label}
      value={draft[field] * 100}
      display={draft[field] === 0 ? 'Off' : String(Math.round(draft[field] * 100))}
      min={0}
      max={100}
      step={2}
      neutral={0}
      onChange={(v) => update({ [field]: v / 100 })}
      onCommit={(v) => commit({ [field]: v / 100 })}
      onClear={() => {
        update({ [field]: 0 });
        commit({ [field]: 0 });
      }}
      active={active}
      onFocusControl={onFocusControl}
    />
  );
}

export function ButtonRow<V extends string | number>({
  label,
  hotkey,
  active,
  options,
  value,
  onChange,
}: {
  label: string;
  hotkey?: string;
  active?: boolean;
  options: { value: V; label: string }[];
  value: V;
  onChange: (v: V) => void;
}) {
  const selected = options.some((o) => o.value === value) ? value : options[0].value;
  const ref = useActiveScroll(active);
  return (
    <div ref={ref} className={cn('flex flex-col gap-1.5 rounded-md', active && 'ring-2 ring-ring ring-offset-2 ring-offset-background')}>
      <span className="text-xs text-muted-foreground">
        {label} {hotkey && <kbd className="text-[10px] opacity-60">{hotkey}</kbd>}
      </span>
      <ToggleGroup
        size="sm"
        className="w-full"
        value={[String(selected)]}
        onValueChange={(groupValue) => {
          const v = (groupValue as string[])[0];
          const opt = options.find((o) => String(o.value) === v);
          if (opt) onChange(opt.value);
        }}
      >
        {options.map((o) => (
          <ToggleGroupItem key={o.value} value={String(o.value)} className="flex-1">
            {o.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
    </div>
  );
}

export function EditSlider({
  label,
  hotkey,
  value,
  display,
  min,
  max,
  step,
  neutral,
  disabled,
  active,
  onFocusControl,
  onChange,
  onCommit,
  onClear,
  gradient,
}: {
  label: string;
  hotkey?: string;
  value: number;
  display: string;
  min: number;
  max: number;
  step: number;
  /** Display-space default: the fill runs from here to the thumb, and the
   * clear button shows only while the value differs from it. */
  neutral?: number;
  disabled?: boolean;
  active?: boolean;
  onFocusControl?: () => void;
  onChange: (v: number) => void;
  onCommit: (v: number) => void;
  /** Resets the control to its default (shown only when neutral is set). */
  onClear?: () => void;
  /** Gradient track (WB dials); replaces the value fill. */
  gradient?: string;
}) {
  // During a drag the thumb tracks a local value, so it stays smooth even
  // while the store update (which re-renders the whole panel) is coalesced to
  // one frame. `dragging === null` means idle → follow the prop.
  const [dragging, setDragging] = useState<number | null>(null);
  const shown = dragging ?? value;
  const changed = neutral != null && Math.abs(value - neutral) > 1e-9;
  const ref = useActiveScroll(active);
  // One row per the develop-drawer plate: label · track · mono value, the
  // reset affordance surfacing only when the value left its default.
  return (
    <div
      ref={ref}
      className={cn(
        'flex items-center gap-2.5 rounded-md',
        active && 'ring-2 ring-ring ring-offset-2 ring-offset-background',
        disabled && 'opacity-50',
      )}
      onPointerDown={onFocusControl}
      title={hotkey ? `${label} (${hotkey})` : undefined}
    >
      <span className="w-[96px] shrink-0 truncate text-[11.5px] text-secondary-foreground">
        {label}
      </span>
      <div className="min-w-0 flex-1">
        <Slider
          value={shown}
          min={min}
          max={max}
          step={step}
          fillFrom={neutral}
          gradient={gradient}
          disabled={disabled}
          aria-label={label}
          onValueChange={(v) => {
            setDragging(v as number);
            onChange(v as number);
          }}
          onValueCommitted={(v) => {
            setDragging(null);
            onCommit(v as number);
          }}
        />
      </div>
      <span className="w-[56px] shrink-0 text-right font-mono text-[11px] text-foreground tabular-nums">
        {display}
      </span>
      {onClear && neutral != null ? (
        <button
          type="button"
          className={cn(
            'shrink-0 text-muted-foreground transition-colors hover:text-foreground',
            !changed && 'invisible',
          )}
          title={`Reset ${label.toLowerCase()}`}
          aria-label={`Reset ${label.toLowerCase()}`}
          onClick={(e) => {
            e.stopPropagation();
            setDragging(null);
            onClear();
          }}
        >
          <RotateCcw className="size-3" />
        </button>
      ) : (
        <span className="w-3 shrink-0" />
      )}
    </div>
  );
}

// EditSlider's two-thumb sibling for window controls (the depth range): same
// row plate, but the value is a [lo, hi] pair and the fill spans the kept
// window between the thumbs.
export function EditRangeSlider({
  label,
  value,
  display,
  min,
  max,
  step,
  neutral,
  onChange,
  onCommit,
  onClear,
}: {
  label: string;
  value: [number, number];
  display: string;
  min: number;
  max: number;
  step: number;
  /** Display-space default window; the clear button shows while the value differs from it. */
  neutral?: [number, number];
  onChange: (v: [number, number]) => void;
  onCommit: (v: [number, number]) => void;
  onClear?: () => void;
}) {
  const [dragging, setDragging] = useState<[number, number] | null>(null);
  const shown = dragging ?? value;
  const changed =
    neutral != null &&
    (Math.abs(value[0] - neutral[0]) > 1e-9 || Math.abs(value[1] - neutral[1]) > 1e-9);
  return (
    <div className="flex items-center gap-2.5 rounded-md">
      <span className="w-[96px] shrink-0 truncate text-[11.5px] text-secondary-foreground">
        {label}
      </span>
      <div className="min-w-0 flex-1">
        <Slider
          value={shown}
          min={min}
          max={max}
          step={step}
          aria-label={label}
          onValueChange={(v) => {
            const pair = [...(v as number[])] as [number, number];
            setDragging(pair);
            onChange(pair);
          }}
          onValueCommitted={(v) => {
            setDragging(null);
            onCommit([...(v as number[])] as [number, number]);
          }}
        />
      </div>
      <span className="w-[56px] shrink-0 text-right font-mono text-[11px] text-foreground tabular-nums">
        {display}
      </span>
      {onClear && neutral != null ? (
        <button
          type="button"
          className={cn(
            'shrink-0 text-muted-foreground transition-colors hover:text-foreground',
            !changed && 'invisible',
          )}
          title={`Reset ${label.toLowerCase()}`}
          aria-label={`Reset ${label.toLowerCase()}`}
          onClick={(e) => {
            e.stopPropagation();
            setDragging(null);
            onClear();
          }}
        >
          <RotateCcw className="size-3" />
        </button>
      ) : (
        <span className="w-3 shrink-0" />
      )}
    </div>
  );
}
