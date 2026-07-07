/**
 * MiniSlider's sibling for the enum develop controls (WB mode, highlight
 * recovery, FBDD denoise, demosaic) in the Cull confirm bar and the Develop
 * quick dock: label on top, a compact chip below that advances to the next
 * value on click and commits immediately (same contract as EditPanel's
 * cycle rows).
 */
export function MiniCycle({
  label,
  value,
  values,
  valueLabel,
  onChange,
}: {
  label: string;
  value: string | number;
  values: (string | number)[];
  valueLabel: (v: string | number) => string;
  onChange: (v: string | number) => void;
}) {
  const next = () => {
    const i = values.indexOf(value);
    onChange(values[(i + 1) % values.length]);
  };
  return (
    <div className="flex w-[82px] flex-col gap-[5px]">
      <span className="text-[10px] leading-none text-secondary-foreground">{label}</span>
      <button
        className="flex h-[18px] items-center justify-center rounded-[5px] border border-input bg-white/5 font-mono text-[10px] leading-none text-white hover:border-ring"
        aria-label={`${label}: ${valueLabel(value)} — click for the next setting`}
        title="Click for the next setting"
        onClick={next}
      >
        {valueLabel(value)}
      </button>
    </div>
  );
}
