import { PanelLeft, PictureInPicture2 } from 'lucide-react';
import { Segmented } from '@/components/ui/segmented';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { TaskTray } from '@/components/TaskTray';
import { WindowControls } from '@/components/WindowControls';
import { mod, modK } from '@/lib/platform';
import { useUIStore, type Mode } from '@/stores/uiStore';
import { useApiClient } from '@/api/client';
import { updateRailHidden } from '@/lib/uiSettings';
import { toggleViewer, useViewerOpen, viewerSupported } from '@/lib/viewerWindow';
import { baseName, rootName, samePath, useLibraryRoots } from '@/lib/library';
import '@/lib/electron';

const MODE_ITEMS: { value: Mode | 'export'; label: string }[] = [
  { value: 'library', label: 'Library' },
  { value: 'cull', label: 'Cull' },
  { value: 'develop', label: 'Develop' },
  { value: 'export', label: 'Export' },
];

/**
 * The constant 48px top bar: logo mark + shoot name, the centered mode
 * segmented control, background-task chips, and the ⌘K affordance. Cull /
 * Develop / Export are dimmed until a folder is open.
 */
export function TopBar() {
  const mode = useUIStore((s) => s.mode);
  const setMode = useUIStore((s) => s.setMode);
  const setExportOpen = useUIStore((s) => s.setExportOpen);
  const setPaletteOpen = useUIStore((s) => s.setPaletteOpen);
  const folderPath = useUIStore((s) => s.folderPath);
  const railHidden = useUIStore((s) => s.railHidden);
  const viewerOpen = useViewerOpen();
  const client = useApiClient();
  const { roots } = useLibraryRoots();

  const current = folderPath ? roots.find((r) => samePath(r.path, folderPath)) : undefined;
  // Folder NAME only (the tooltip carries the full path) — matches CinemaHUD.
  const shootName = current ? rootName(current) : folderPath ? baseName(folderPath) : 'marraw';
  const hasFolder = folderPath != null;

  // The whole bar is the frameless window's move handle; every interactive
  // island is carved back out with no-drag.
  return (
    <div className="flex h-12 shrink-0 items-center gap-3.5 border-b bg-sidebar py-0 pr-2 pl-4 [-webkit-app-region:drag]">
      {/* Both side clusters get equal flexible shares (flex-1 basis-0) so
          the mode control sits at the true window center at any width. */}
      <div className="flex min-w-0 flex-1 basis-0 items-center gap-2">
        {/* Collapses the rail out of the way for a wider grid. Left of the
            mark, over the rail it controls. */}
        <Button
          variant="ghost"
          size="icon-sm"
          aria-pressed={railHidden}
          className="shrink-0 text-muted-foreground hover:text-foreground [-webkit-app-region:no-drag]"
          onClick={() => updateRailHidden(client, !railHidden)}
          title={railHidden ? 'Show the library rail' : 'Hide the library rail'}
        >
          <PanelLeft />
          <span className="sr-only">{railHidden ? 'Show the library rail' : 'Hide the library rail'}</span>
        </Button>
        <img src="./icon.svg" alt="" className="size-6 shrink-0" />
        <span className="truncate text-[13px] font-semibold" title={folderPath ?? undefined}>
          {shootName}
        </span>
      </div>
      <Segmented
        aria-label="Mode"
        items={MODE_ITEMS.map((m) => ({
          ...m,
          disabled: m.value !== 'library' && !hasFolder,
        }))}
        value={mode}
        onValueChange={(v) => {
          if (v === 'export') setExportOpen(true);
          else setMode(v);
        }}
        className={cn('shrink-0 [-webkit-app-region:no-drag]', !hasFolder && 'opacity-50')}
      />
      <div className="flex min-w-0 flex-1 basis-0 items-center justify-end gap-3">
        <button
          className="flex h-[30px] shrink-0 items-center gap-2 rounded-lg border border-border bg-secondary px-3 text-xs whitespace-nowrap text-muted-foreground hover:text-foreground [-webkit-app-region:no-drag] dark:bg-white/5"
          onClick={() => setPaletteOpen(true)}
          disabled={!hasFolder}
          style={!hasFolder ? { opacity: 0.5 } : undefined}
          title="Jump to anything"
        >
          {/* The label yields first when the window narrows; the chip stays. */}
          <span className="max-[860px]:hidden">Jump to anything</span>
          <span className="rounded bg-black/10 px-1.5 py-px font-mono dark:bg-white/10">{modK}</span>
        </button>
        {/* The pop-out photo window, for people who don't reach for Ctrl+N.
            Lit while it is up, and closes it again — including a window
            opened from another marraw window, or closed by its own key. */}
        {viewerSupported() && (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-pressed={viewerOpen}
            className={cn(
              'shrink-0 [-webkit-app-region:no-drag]',
              viewerOpen ? 'bg-primary/15 text-primary hover:bg-primary/20' : 'text-muted-foreground hover:text-foreground',
            )}
            onClick={toggleViewer}
            title={viewerOpen ? `Close the photo window (${mod}+N)` : `Pop the photo out into its own window (${mod}+N)`}
          >
            <PictureInPicture2 />
            <span className="sr-only">{viewerOpen ? 'Close the photo window' : 'Pop out the photo window'}</span>
          </Button>
        )}
        <div data-testid="task-tray">
          <TaskTray />
        </div>
        {window.win && <div className="h-[22px] w-px bg-black/10 dark:bg-white/9" />}
        <WindowControls />
      </div>
    </div>
  );
}
