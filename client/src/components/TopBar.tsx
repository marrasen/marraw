import { Segmented } from '@/components/ui/segmented';
import { TaskChip } from '@/components/ui/task-chip';
import { useApiClient } from '@/api/client';
import { useMyTasks, cancelSharedTask } from '@/api/tasks';
import { useUIStore, type Mode } from '@/stores/uiStore';
import { rootName, samePath, useLibraryRoots } from '@/lib/library';
import { useTaskToasts } from '@/lib/taskToasts';

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
  const shootName = current ? rootName(current) : folderPath ? folderPath : 'marraw';
  const hasFolder = folderPath != null;

  return (
    <div className="flex h-12 shrink-0 items-center gap-3.5 border-b bg-sidebar px-4">
      <div className="flex min-w-0 items-center gap-2">
        <div className="flex size-6 shrink-0 items-center justify-center rounded-[7px] bg-primary text-sm font-bold text-primary-foreground">
          m
        </div>
        <span className="truncate text-[13px] font-semibold" title={folderPath ?? undefined}>
          {shootName}
        </span>
      </div>
      <div className="flex-1" />
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
        className={!hasFolder ? 'opacity-50' : undefined}
      />
      <div className="flex flex-1 items-center justify-end gap-3" data-testid="task-tray">
        <TopBarTasks />
        <button
          className="flex h-[30px] items-center gap-2 rounded-lg border border-border bg-secondary px-3 text-xs text-muted-foreground hover:text-foreground dark:bg-white/5"
          onClick={() => setPaletteOpen(true)}
          disabled={!hasFolder}
          style={!hasFolder ? { opacity: 0.5 } : undefined}
        >
          <span>Jump to anything</span>
          <span className="rounded bg-black/10 px-1.5 py-px font-mono dark:bg-white/10">⌘K</span>
        </button>
      </div>
    </div>
  );
}

// TopBarTasks shows every running backend job (scan, pre-render, export, …)
// as a shared-spec task chip with a cancel button.
function TopBarTasks() {
  const client = useApiClient();
  const tasks = useMyTasks();
  useTaskToasts(tasks);

  const running = tasks.filter((t) => t.status === 'running' || t.status === 'created');
  return (
    <>
      {running.map((t) => (
        <TaskChip
          key={t.id}
          label={t.title}
          count={t.total ? `${t.current ?? 0}/${t.total.toLocaleString()}` : undefined}
          pct={t.total ? Math.round(((t.current ?? 0) / t.total) * 100) : undefined}
          onCancel={() => cancelSharedTask(client, t.id).catch(() => {})}
          className="max-w-72 py-1.5"
        />
      ))}
    </>
  );
}
