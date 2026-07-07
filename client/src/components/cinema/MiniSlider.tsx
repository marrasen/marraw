import { useState } from 'react';
import { Slider } from '@/components/ui/slider';

/**
 * A compact 82px labeled dial for the Cull confirm bar and the Develop quick
 * dock: label + mono value on top, a 3px bipolar-fill track below. Drags
 * preview live and persist on release (same contract as EditSlider).
 */
export function MiniSlider({
  label,
  value,
  display,
  min,
  max,
  step,
  neutral,
  onChange,
  onCommit,
}: {
  label: string;
  value: number;
  display: string;
  min: number;
  max: number;
  step: number;
  neutral?: number;
  onChange: (v: number) => void;
  onCommit: (v: number) => void;
}) {
  const [dragging, setDragging] = useState<number | null>(null);
  const shown = dragging ?? value;
  return (
    <div className="flex w-[82px] flex-col gap-[5px]">
      <div className="flex justify-between text-[10px] leading-none">
        <span className="text-secondary-foreground">{label}</span>
        <span className="font-mono text-white tabular-nums">{display}</span>
      </div>
      <Slider
        value={shown}
        min={min}
        max={max}
        step={step}
        fillFrom={neutral}
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
  );
}
