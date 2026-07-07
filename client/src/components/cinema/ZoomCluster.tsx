import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import { useUIStore } from '@/stores/uiStore';

/**
 * The zoom controls, embedded in a mode's control bar and always present:
 * Fit/1:1 segmented, zoom slider, and the fixed-width % readout. Rendering
 * progress lives in the canvas badge (CinemaImage), not here — swapping the
 * readout for a spinner used to resize the whole bar on every photo step.
 */
export function ZoomCluster({ scale }: { scale: number }) {
  const zoom = useUIStore((s) => s.loupeZoom);
  const setZoom = useUIStore((s) => s.setLoupeZoom);
  const centerLoupe = useUIStore((s) => s.centerLoupe);
  const isFit = zoom === 'fit';
  return (
    <div className="flex items-center gap-2.5">
      <Segmented
        aria-label="Zoom mode"
        size="sm"
        items={[
          { value: 'fit', label: 'Fit' },
          { value: '1:1', label: '1:1' },
        ]}
        value={isFit ? 'fit' : '1:1'}
        onValueChange={(v) => {
          // Re-clicking the active Fit recenters a panned-away photo.
          if (v === 'fit' && isFit) centerLoupe();
          else setZoom(v === 'fit' ? 'fit' : 1);
        }}
        className="border-0 bg-white/5"
      />
      <div className="w-[110px]">
        <Slider
          value={Math.round(scale * 100)}
          min={5}
          max={400}
          step={5}
          onValueChange={(v) => setZoom((v as number) / 100)}
          aria-label="Zoom"
        />
      </div>
      <span className="w-[38px] text-right font-mono text-[11px] tabular-nums">
        {Math.round(scale * 100)}%
      </span>
    </div>
  );
}
