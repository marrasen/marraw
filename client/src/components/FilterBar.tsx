import { LayoutGrid, Star } from 'lucide-react';
import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import { cn } from '@/lib/utils';
import { useUIStore, type FlagFilter } from '@/stores/uiStore';

const FLAG_ITEMS: { value: FlagFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'pick', label: 'Picks' },
  { value: 'not-excluded', label: 'Not excluded' },
  { value: 'exclude', label: 'Excluded' },
];

export function FilterBar({ shownCount, totalCount }: { shownCount: number; totalCount: number }) {
  const minRating = useUIStore((s) => s.minRating);
  const flagFilter = useUIStore((s) => s.flagFilter);
  const setFilters = useUIStore((s) => s.setFilters);
  const view = useUIStore((s) => s.view);
  const cellSize = useUIStore((s) => s.cellSize);
  const setCellSize = useUIStore((s) => s.setCellSize);

  return (
    <div className="flex h-[47px] shrink-0 items-center gap-4 border-b px-[18px]">
      <div className="flex items-center gap-2" role="group" aria-label="Minimum rating filter">
        <span className="text-[11.5px] text-muted-foreground">Rating</span>
        <div className="flex gap-px">
          {[1, 2, 3, 4, 5].map((n) => (
            <button
              key={n}
              onClick={() => setFilters({ minRating: minRating === n ? 0 : n })}
              className="p-0.5"
              aria-label={`Show ${n}+ stars`}
            >
              <Star
                className={cn(
                  'size-[13px]',
                  n <= minRating
                    ? 'fill-rating text-rating'
                    : 'fill-black/20 text-transparent dark:fill-white/20',
                )}
              />
            </button>
          ))}
        </div>
        <span className="text-[11px] text-muted-foreground">&amp; up</span>
      </div>

      <div className="h-5 w-px bg-border" />

      <Segmented
        aria-label="Flag filter"
        size="sm"
        items={FLAG_ITEMS}
        value={flagFilter}
        onValueChange={(v) => setFilters({ flagFilter: v })}
        className="border-0 bg-secondary dark:bg-white/5"
      />

      <div className="flex-1" />

      {view === 'grid' && (
        <>
          <div className="flex items-center gap-2" title="Thumbnail size">
            <LayoutGrid className="size-[13px] shrink-0 text-muted-foreground" strokeWidth={1.5} />
            <Slider
              value={cellSize}
              min={120}
              max={400}
              step={20}
              onValueChange={(v) => setCellSize(v as number)}
              aria-label="Thumbnail size"
              className="w-[104px]"
            />
            <span className="w-[42px] font-mono text-[11px] text-muted-foreground tabular-nums">
              {cellSize}px
            </span>
          </div>
          <div className="h-5 w-px bg-border" />
        </>
      )}

      <span className="font-mono text-xs text-secondary-foreground">
        <span className="text-foreground">{shownCount.toLocaleString()}</span> shown{' '}
        <span className="text-faint">/ {totalCount.toLocaleString()}</span>
      </span>
    </div>
  );
}
