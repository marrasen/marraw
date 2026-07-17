import { Segmented } from '@/components/ui/segmented';
import { cn } from '@/lib/utils';
import { TaskTray } from '@/components/TaskTray';
import { WindowControls } from '@/components/WindowControls';
import { modK } from '@/lib/platform';
import { useUIStore, type Mode } from '@/stores/uiStore';
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
        <div className="flex size-6 shrink-0 items-center justify-center rounded-[7px] bg-primary text-sm font-bold text-primary-foreground">
          m
        </div>
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
        <div data-testid="task-tray">
          <TaskTray />
        </div>
        {window.win && <div className="h-[22px] w-px bg-black/10 dark:bg-white/9" />}
        <WindowControls />
      </div>
    </div>
  );
}
