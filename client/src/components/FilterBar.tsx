import { useState } from 'react';
import { LayoutGrid, Star, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { deletePhotos } from '@/api/library';
import { resetEdits } from '@/api/edits';
import { useApiClient } from '@/api/client';
import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { applyFlag, applyRating } from '@/lib/actions';
import { esApplyParams } from '@/lib/editSession';
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
  const multiSelect = useUIStore((s) => s.selection.size > 1);

  // A multi-photo selection takes over the filter row (handoff "BATCH").
  if (multiSelect) return <SelectionBar />;

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

// SelectionBar: batch rate / flag / paste / restore for the whole selection,
// in the filter row's slot. Esc clears the selection.
function SelectionBar() {
  const client = useApiClient();
  const selection = useUIStore((s) => s.selection);
  const clipboard = useUIStore((s) => s.clipboard);
  const clearSelection = useUIStore((s) => s.clearSelection);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const ids = [...selection];

  const doDelete = async () => {
    setDeleting(true);
    try {
      const res = await deletePhotos(client, ids);
      toast.success(`Moved ${res.deleted} photo${res.deleted === 1 ? '' : 's'} to the Recycle Bin`);
      clearSelection();
      setConfirmDelete(false);
    } catch (err) {
      toast.error(`Delete failed: ${(err as Error).message}`);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="flex h-[47px] shrink-0 items-center gap-3.5 border-b border-primary/28 bg-primary/10 px-[18px]">
      <span className="flex items-center gap-2 text-[13px] text-foreground">
        <span className="rounded-[5px] bg-primary px-[7px] py-0.5 font-mono font-semibold text-primary-foreground">
          {selection.size}
        </span>
        <span>selected</span>
      </span>
      <div className="h-5 w-px bg-black/15 dark:bg-white/15" />
      <div className="flex items-center gap-2" role="group" aria-label="Batch rating">
        <span className="text-xs text-muted-foreground">Rate</span>
        <div className="flex gap-px text-[13px] leading-none">
          {[1, 2, 3, 4, 5].map((n) => (
            <button
              key={n}
              className="pr-0.5 text-rating opacity-80 hover:opacity-100"
              aria-label={`Rate ${n} stars`}
              onClick={() => applyRating(client, ids, n)}
            >
              ★
            </button>
          ))}
        </div>
      </div>
      <div className="flex gap-[7px]">
        <button
          className="flex h-[30px] items-center gap-1.5 rounded-lg border border-success/45 bg-success/15 px-[13px] text-[12.5px] text-success-text hover:bg-success/25"
          onClick={() => applyFlag(client, ids, 'pick')}
        >
          Pick <span className="font-mono text-[10px] opacity-80">P</span>
        </button>
        <button
          className="flex h-[30px] items-center gap-1.5 rounded-lg border border-destructive/40 bg-destructive/10 px-[13px] text-[12.5px] text-danger-text hover:bg-destructive/20"
          onClick={() => applyFlag(client, ids, 'exclude')}
        >
          Reject <span className="font-mono text-[10px] opacity-80">X</span>
        </button>
      </div>
      <div className="flex gap-[7px]">
        <Button
          variant="outline"
          size="sm"
          disabled={!clipboard}
          title={clipboard ? `Paste copied settings onto ${selection.size} photos` : 'Copy settings first (Ctrl+C)'}
          onClick={() => {
            if (!clipboard) return;
            esApplyParams(client, clipboard);
            toast.success(`Settings pasted to ${selection.size} photos`);
          }}
        >
          Paste settings
        </Button>
        <Button
          variant="outline"
          size="sm"
          title="Reset all edits on the selection"
          onClick={() => {
            resetEdits(client, ids)
              .then(() => toast.success(`Restored ${selection.size} photos to original`))
              .catch((err) => toast.error((err as Error).message));
          }}
        >
          Restore original
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          className="text-danger-text"
          title="Move selection to the Recycle Bin"
          aria-label="Delete selection"
          onClick={() => setConfirmDelete(true)}
        >
          <Trash2 />
        </Button>
      </div>
      <div className="flex-1" />
      <span className="font-mono text-[11px] text-muted-foreground">Esc to clear</span>
      <Dialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete {selection.size} photos?</DialogTitle>
            <DialogDescription>
              The RAW files are moved to the Recycle Bin — you can restore them from there.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmDelete(false)} disabled={deleting}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void doDelete()} disabled={deleting}>
              {deleting ? 'Deleting…' : 'Move to Recycle Bin'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
