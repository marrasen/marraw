import { Star, Download, ZoomIn } from 'lucide-react';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Button } from '@/components/ui/button';
import { Separator } from '@/components/ui/separator';
import { Slider } from '@/components/ui/slider';
import { cn } from '@/lib/utils';
import { useUIStore, type FlagFilter } from '@/stores/uiStore';

export function FilterBar({ shownCount, totalCount }: { shownCount: number; totalCount: number }) {
  const minRating = useUIStore((s) => s.minRating);
  const flagFilter = useUIStore((s) => s.flagFilter);
  const setFilters = useUIStore((s) => s.setFilters);
  const setExportOpen = useUIStore((s) => s.setExportOpen);
  const view = useUIStore((s) => s.view);
  const cellSize = useUIStore((s) => s.cellSize);
  const setCellSize = useUIStore((s) => s.setCellSize);

  return (
    <div className="flex items-center gap-3 border-b px-3 py-1.5">
      <div className="flex items-center" role="group" aria-label="Minimum rating filter">
        {[1, 2, 3, 4, 5].map((n) => (
          <button
            key={n}
            onClick={() => setFilters({ minRating: minRating === n ? 0 : n })}
            className="p-0.5"
            aria-label={`Show ${n}+ stars`}
          >
            <Star
              className={cn(
                'size-4',
                n <= minRating ? 'fill-amber-400 text-amber-400' : 'text-muted-foreground/40',
              )}
            />
          </button>
        ))}
      </div>

      <Separator orientation="vertical" className="h-5" />

      <ToggleGroup
        size="sm"
        value={[flagFilter]}
        onValueChange={(groupValue) => {
          const v = (groupValue as string[])[0];
          if (v) setFilters({ flagFilter: v as FlagFilter });
        }}
      >
        <ToggleGroupItem value="all">All</ToggleGroupItem>
        <ToggleGroupItem value="pick">Picks</ToggleGroupItem>
        <ToggleGroupItem value="not-excluded">Unculled</ToggleGroupItem>
        <ToggleGroupItem value="exclude">Excluded</ToggleGroupItem>
      </ToggleGroup>

      {view === 'grid' && (
        <>
          <Separator orientation="vertical" className="h-5" />
          <div className="flex items-center gap-2" title="Thumbnail size">
            <ZoomIn className="size-4 text-muted-foreground" />
            <Slider
              className="w-28"
              value={cellSize}
              min={120}
              max={400}
              step={20}
              onValueChange={(v) => setCellSize(v as number)}
              aria-label="Thumbnail size"
            />
          </div>
        </>
      )}

      <span className="ml-auto text-xs text-muted-foreground">
        {shownCount === totalCount ? `${totalCount} photos` : `${shownCount} of ${totalCount} photos`}
      </span>
      <Button size="sm" variant="outline" onClick={() => setExportOpen(true)}>
        <Download data-icon="inline-start" />
        Export
      </Button>
    </div>
  );
}
