import { useState } from 'react';
import { ArrowUpDown, Contrast, Eye, Focus, Layers, LayoutGrid, PanelRight, Star, Trash2, Wand2 } from 'lucide-react';
import { toast } from 'sonner';
import { deletePhotos } from '@/api/library';
import { resetEdits } from '@/api/edits';
import { useApiClient } from '@/api/client';
import { GapControl } from '@/components/cinema/GapControl';
import { Segmented } from '@/components/ui/segmented';
import { Slider } from '@/components/ui/slider';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
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
import { applyFlag, applyRating, judgeAllBursts } from '@/lib/actions';
import type { BurstInfo } from '@/lib/bursts';
import { esApplyParams } from '@/lib/editSession';
import { updateFolderFilters, updateLibrarySort } from '@/lib/uiSettings';
import { useUIStore, type FlagFilter, type LibrarySort } from '@/stores/uiStore';

const FLAG_ITEMS: { value: FlagFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'pick', label: 'Picks' },
  { value: 'not-excluded', label: 'Not excluded' },
  { value: 'exclude', label: 'Excluded' },
];

const SORT_ITEMS: { value: LibrarySort; label: string }[] = [
  { value: 'captureAsc', label: 'Capture time · oldest first' },
  { value: 'captureDesc', label: 'Capture time · newest first' },
  { value: 'nameAsc', label: 'File name · A to Z' },
  { value: 'nameDesc', label: 'File name · Z to A' },
];

export function FilterBar({
  softBelow,
  subjectAnalyzed,
  eyesAnalyzed,
  photoCount,
  bursts,
}: {
  softBelow: number;
  // How many photos in the folder have been analyzed for subjects (whether or
  // not one was found), out of photoCount total — drives the subject-scan
  // indicator's state and count.
  subjectAnalyzed: number;
  // How many photos have been checked for closed eyes (whether or not a face
  // was found) — drives the eye-scan indicator, like subjectAnalyzed.
  eyesAnalyzed: number;
  photoCount: number;
  // The folder's near-duplicate groups (whole-folder, from usePhotos) —
  // drives the collapse-bursts toggle and the auto-judge sweep.
  bursts: Map<number, BurstInfo>;
}) {
  const client = useApiClient();
  const minRating = useUIStore((s) => s.minRating);
  const librarySort = useUIStore((s) => s.librarySort);
  const flagFilter = useUIStore((s) => s.flagFilter);
  const softOnly = useUIStore((s) => s.softOnly);
  const toggleSoftOnly = useUIStore((s) => s.toggleSoftOnly);
  const collapseBursts = useUIStore((s) => s.collapseBursts);
  const toggleCollapseBursts = useUIStore((s) => s.toggleCollapseBursts);
  const setSubjectScanOpen = useUIStore((s) => s.setSubjectScanOpen);
  const setEyeScanOpen = useUIStore((s) => s.setEyeScanOpen);
  const view = useUIStore((s) => s.view);
  const cellSize = useUIStore((s) => s.cellSize);
  const setCellSize = useUIStore((s) => s.setCellSize);
  const showEditPanel = useUIStore((s) => s.showEditPanel);
  const toggleEditPanel = useUIStore((s) => s.toggleEditPanel);
  const multiSelect = useUIStore((s) => s.selection.size > 1);

  // A multi-photo selection takes over the filter row (handoff "BATCH").
  if (multiSelect) return <SelectionBar />;

  return (
    // @container so the controls can collapse against the toolbar's own width
    // (rail is user-resizable, so a viewport breakpoint would misjudge space).
    // overflow-hidden is a hard backstop against any control spilling into the
    // develop panel. The live shown/total count lives in the StatusBar below.
    <div className="@container flex h-[47px] shrink-0 items-center gap-4 overflow-hidden border-b px-[18px] @max-[700px]:gap-2">
      <div className="flex items-center gap-2" role="group" aria-label="Minimum rating filter">
        <span className="text-[11.5px] text-muted-foreground @max-[960px]:hidden">Rating</span>
        <div className="flex gap-px">
          {[1, 2, 3, 4, 5].map((n) => (
            <button
              key={n}
              onClick={() => updateFolderFilters(client, { minRating: minRating === n ? 0 : n })}
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
        <span className="text-[11px] text-muted-foreground @max-[960px]:hidden">&amp; up</span>
      </div>

      <div className="h-5 w-px bg-border" />

      <Segmented
        aria-label="Flag filter"
        size="sm"
        items={FLAG_ITEMS}
        value={flagFilter}
        onValueChange={(v) => updateFolderFilters(client, { flagFilter: v })}
        className="border-0 bg-secondary dark:bg-white/5"
      />

      {/* Soft-focus filter: isolate the frames the grid badges as soft so they
          can be picked through and rejected. Disabled until the folder has
          enough sharpness scores to define a threshold (softBelow > 0). */}
      <button
        onClick={toggleSoftOnly}
        disabled={softBelow <= 0}
        className={cn(
          'flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-[11.5px] disabled:cursor-not-allowed disabled:opacity-40',
          softOnly
            ? 'bg-amber-400/15 text-amber-500 dark:text-amber-400'
            : 'text-muted-foreground hover:bg-secondary hover:text-foreground',
        )}
        title={
          softBelow <= 0
            ? 'No focus scores yet — soft frames can’t be detected'
            : softOnly
              ? 'Showing only soft-focus frames'
              : 'Show only soft-focus frames'
        }
        aria-label="Show only soft-focus frames"
        aria-pressed={softOnly}
      >
        <Contrast className="size-[13px]" strokeWidth={1.75} />
        <span className="@max-[960px]:hidden">Soft</span>
      </button>

      {/* Collapse bursts: show one frame per near-duplicate group — the
          sharpest member (or the lead frame until scores exist). Singles are
          unaffected. Disabled when the folder has no bursts. */}
      <button
        onClick={toggleCollapseBursts}
        disabled={bursts.size === 0}
        className={cn(
          'flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-[11.5px] disabled:cursor-not-allowed disabled:opacity-40',
          collapseBursts
            ? 'bg-amber-400/15 text-amber-500 dark:text-amber-400'
            : 'text-muted-foreground hover:bg-secondary hover:text-foreground',
        )}
        title={
          bursts.size === 0
            ? 'No bursts in this folder'
            : collapseBursts
              ? 'Showing one frame per burst — the sharpest'
              : 'Collapse each burst to its sharpest frame'
        }
        aria-label="Collapse bursts to their sharpest frame"
        aria-pressed={collapseBursts}
      >
        <Layers className="size-[13px]" strokeWidth={1.75} />
        <span className="@max-[960px]:hidden">Bursts</span>
      </button>

      {/* Auto-judge bursts: the folder-wide Shift+P — pick every burst's
          sharpest frame, reject the rest, one undo entry. Skips unscored
          bursts and bursts where a non-sharpest member is already picked. */}
      <button
        onClick={() => judgeAllBursts(client, bursts)}
        disabled={![...bursts.values()].some((b) => b.bestId != null)}
        className="flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-[11.5px] text-muted-foreground hover:bg-secondary hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40"
        title={
          bursts.size === 0
            ? 'No bursts in this folder'
            : 'Pick the sharpest frame of every burst and reject the rest (one undo). Bursts where you already picked a keeper are left alone.'
        }
        aria-label="Auto-judge bursts: pick the sharpest frame of every burst, reject the rest"
        data-testid="auto-judge-bursts"
      >
        <Wand2 className="size-[13px]" strokeWidth={1.75} />
        <span className="@max-[960px]:hidden">Auto-judge</span>
      </button>

      {/* Subject-aware focus: amber with a count once any frame is scored over
          its AI subject matte, muted otherwise. Click opens the folder-wide
          "analyze subjects & re-score focus" dialog. */}
      <button
        onClick={() => setSubjectScanOpen(true)}
        disabled={photoCount === 0}
        className={cn(
          'flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-[11.5px] disabled:cursor-not-allowed disabled:opacity-40',
          subjectAnalyzed > 0
            ? 'bg-amber-400/15 text-amber-500 dark:text-amber-400'
            : 'text-muted-foreground hover:bg-secondary hover:text-foreground',
        )}
        title={
          subjectAnalyzed === 0
            ? 'Focus uses whole-frame sharpness — click to analyze subjects & re-score focus'
            : subjectAnalyzed < photoCount
              ? `${subjectAnalyzed} of ${photoCount} photos analyzed for subjects — click to analyze the rest`
              : `All ${photoCount} photos analyzed for subjects & re-scored`
        }
        aria-label="Analyze subjects and re-score focus"
        data-testid="subject-scan-button"
      >
        <Focus className="size-[13px]" strokeWidth={1.75} />
        <span className="@max-[960px]:hidden">
          {subjectAnalyzed > 0 ? `${subjectAnalyzed}/${photoCount}` : 'Subjects'}
        </span>
      </button>

      {/* Closed-eye detection: amber with a count once any frame has been
          checked, muted otherwise. Click opens the folder-wide "detect closed
          eyes" dialog. */}
      <button
        onClick={() => setEyeScanOpen(true)}
        disabled={photoCount === 0}
        className={cn(
          'flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-[11.5px] disabled:cursor-not-allowed disabled:opacity-40',
          eyesAnalyzed > 0
            ? 'bg-amber-400/15 text-amber-500 dark:text-amber-400'
            : 'text-muted-foreground hover:bg-secondary hover:text-foreground',
        )}
        title={
          eyesAnalyzed === 0
            ? 'Click to detect closed eyes and badge the blinks'
            : eyesAnalyzed < photoCount
              ? `${eyesAnalyzed} of ${photoCount} photos checked for closed eyes — click to check the rest`
              : `All ${photoCount} photos checked for closed eyes`
        }
        aria-label="Detect closed eyes"
        data-testid="eye-scan-button"
      >
        <Eye className="size-[13px]" strokeWidth={1.75} />
        <span className="@max-[960px]:hidden">
          {eyesAnalyzed > 0 ? `${eyesAnalyzed}/${photoCount}` : 'Eyes'}
        </span>
      </button>

      <div className="flex-1" />

      {view === 'grid' && (
        <>
          {/* Group-by-gap — a secondary control; drops out first when tight so
              the flag filter and panel toggle keep their room. */}
          <div className="flex items-center gap-4 @max-[700px]:gap-2 @max-[720px]:hidden">
            <GapControl labelClassName="@max-[880px]:hidden" />
            <div className="h-5 w-px bg-border" />
          </div>

          {/* Thumbnail size: inline slider on a roomy bar, folded behind the
              grid icon as it tightens, then gone entirely on the narrowest. */}
          <div className="flex items-center gap-4 @max-[700px]:gap-2 @max-[640px]:hidden">
            <div className="flex items-center gap-2 @max-[780px]:hidden" title="Thumbnail size">
              <LayoutGrid
                className="size-[13px] shrink-0 text-muted-foreground"
                strokeWidth={1.5}
              />
              {/* Fixed-width wrapper, not className on the root: the root's own
                  data-horizontal:w-full outranks a width utility passed in and
                  collapses the track to its intrinsic (thumb-only) width. */}
              <div className="w-[104px]">
                <Slider
                  value={cellSize}
                  min={120}
                  max={400}
                  step={20}
                  onValueChange={(v) => setCellSize(v as number)}
                  aria-label="Thumbnail size"
                />
              </div>
              <span className="w-[42px] font-mono text-[11px] text-muted-foreground tabular-nums">
                {cellSize}px
              </span>
            </div>
            <DropdownMenu>
              <DropdownMenuTrigger
                className="hidden size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground @max-[780px]:flex"
                title="Thumbnail size"
                aria-label="Thumbnail size"
              >
                <LayoutGrid className="size-[15px]" strokeWidth={1.5} />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-[188px] p-3">
                <div className="flex items-center gap-2" onKeyDown={(e) => e.stopPropagation()}>
                  <LayoutGrid
                    className="size-[13px] shrink-0 text-muted-foreground"
                    strokeWidth={1.5}
                  />
                  <div className="flex-1">
                    <Slider
                      value={cellSize}
                      min={120}
                      max={400}
                      step={20}
                      onValueChange={(v) => setCellSize(v as number)}
                      aria-label="Thumbnail size"
                    />
                  </div>
                  <span className="w-[42px] font-mono text-[11px] text-muted-foreground tabular-nums">
                    {cellSize}px
                  </span>
                </div>
              </DropdownMenuContent>
            </DropdownMenu>

            <div className="h-5 w-px bg-border" />
          </div>
        </>
      )}

      {/* Sort order applies to every view (the loupe filmstrip and Cull deck
          follow the same list), so it lives outside the grid-only cluster. */}
      <DropdownMenu>
        <DropdownMenuTrigger
          className={cn(
            'flex size-7 shrink-0 items-center justify-center rounded-md hover:bg-secondary hover:text-foreground',
            librarySort === 'captureAsc' ? 'text-muted-foreground' : 'text-foreground',
          )}
          title="Sort order"
          aria-label="Sort order"
        >
          <ArrowUpDown className="size-[15px]" strokeWidth={1.5} />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-[218px]">
          <DropdownMenuRadioGroup
            value={librarySort}
            onValueChange={(v) => updateLibrarySort(client, v as LibrarySort)}
          >
            {SORT_ITEMS.map((it) => (
              <DropdownMenuRadioItem key={it.value} value={it.value}>
                {it.label}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>

      <div className="h-5 w-px bg-border" />

      <button
        onClick={toggleEditPanel}
        className={cn(
          'flex size-7 shrink-0 items-center justify-center rounded-md hover:bg-secondary hover:text-foreground',
          showEditPanel ? 'text-foreground' : 'text-muted-foreground',
        )}
        title={showEditPanel ? 'Hide develop panel' : 'Show develop panel'}
        aria-label={showEditPanel ? 'Hide develop panel' : 'Show develop panel'}
        aria-pressed={showEditPanel}
      >
        <PanelRight className="size-[15px]" strokeWidth={1.5} />
      </button>
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
            esApplyParams(client, clipboard, { label: 'Paste' });
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
