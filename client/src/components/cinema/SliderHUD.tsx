import { useEffect } from 'react';
import { Slider } from '@/components/ui/slider';
import { DIALS } from '@/lib/dials';
import { esSetKeyAdjust, useEditSession } from '@/lib/editSession';

/**
 * The heads-up keyboard-adjust readout: while +/- nudges the focused develop
 * control, Develop hides its drawer + chrome and floats this small glass panel
 * centered at the bottom, showing just that one slider. It is a pure readout
 * (pointer-events-none) driven by the +/- keys — moving the mouse dismisses it
 * (restoring the full drawer) via the pointer listeners below. Rendered only
 * while the adjust is live, so the listeners exist only then.
 */
export function SliderHUD() {
  const activeControl = useEditSession((s) => s.activeControl);
  // Fall back to the held previous draft through esLoad's null gap, matching
  // the drawer — the value snaps when the new photo's params land.
  const draft = useEditSession((s) => s.draft ?? s.lastDraft);

  // Grabbing the mouse (or any pointer activity) ends the heads-up adjust and
  // brings the full chrome back. keydown is deliberately NOT watched, so
  // repeated +/- keep the panel up.
  useEffect(() => {
    const end = () => esSetKeyAdjust(false);
    window.addEventListener('pointermove', end, { passive: true });
    window.addEventListener('pointerdown', end, { passive: true });
    return () => {
      window.removeEventListener('pointermove', end);
      window.removeEventListener('pointerdown', end);
    };
  }, []);

  const dial = DIALS.find((d) => d.key === activeControl);
  if (!activeControl || !dial) return null;

  return (
    <div className="glass pointer-events-none absolute bottom-4 left-1/2 z-40 flex -translate-x-1/2 items-center gap-3.5 rounded-[13px] px-4 py-2.5">
      <span className="text-[11.5px] text-muted-foreground">{dial.label}</span>
      {dial.kind === 'numeric' ? (
        <>
          <div className="w-[160px]">
            <Slider
              value={dial.value(draft)}
              min={dial.min}
              max={dial.max}
              step={dial.step}
              fillFrom={dial.neutral}
              aria-label={dial.label}
            />
          </div>
          <span className="w-[48px] text-right font-mono text-[11.5px] text-foreground tabular-nums">
            {dial.display(dial.value(draft))}
          </span>
        </>
      ) : (
        <span className="font-mono text-[11.5px] text-foreground tabular-nums">
          {dial.valueLabel(dial.value(draft))}
        </span>
      )}
    </div>
  );
}
