import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import { ChipSpinner } from '@/components/ui/task-chip';
import { useUIStore } from '@/stores/uiStore';

/**
 * The zoom controls, embedded in a mode's control bar and always present:
 * Fit/1:1 segmented, zoom slider, and the % readout that flips to the
 * rendering state while 1:1 tiles decode.
 */
export function ZoomCluster({ scale, rendering }: { scale: number; rendering: boolean }) {
  const zoom = useUIStore((s) => s.loupeZoom);
  const setZoom = useUIStore((s) => s.setLoupeZoom);
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
        onValueChange={(v) => setZoom(v === 'fit' ? 'fit' : 1)}
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
      {rendering ? (
        <span className="flex w-[104px] items-center gap-1.5 font-mono text-[10.5px] text-[#aab0ff]">
          <ChipSpinner className="size-3" />
          Rendering {Math.round(scale * 100)}%
        </span>
      ) : (
        <span className="w-[38px] text-right font-mono text-[11px] tabular-nums">
          {Math.round(scale * 100)}%
        </span>
      )}
    </div>
  );
}
