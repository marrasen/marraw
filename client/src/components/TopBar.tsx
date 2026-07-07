import { useState } from 'react';
import { Check, ChevronDown, X } from 'lucide-react';
import { Segmented } from '@/components/ui/segmented';
import { ChipProgress, ChipSpinner, TaskChip } from '@/components/ui/task-chip';
import { useApiClient } from '@/api/client';
import { useMyTasks, cancelSharedTask } from '@/api/tasks';
import type { SharedTaskState } from '@/api/tasks-handler';
import { cn } from '@/lib/utils';
import { WindowControls } from '@/components/WindowControls';
import { useUIStore, type Mode } from '@/stores/uiStore';
import { rootName, samePath, useLibraryRoots } from '@/lib/library';
import { useTaskToasts } from '@/lib/taskToasts';
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
  const shootName = current ? rootName(current) : folderPath ? folderPath : 'marraw';
  const hasFolder = folderPath != null;

  // The whole bar is the frameless window's move handle; every interactive
  // island is carved back out with no-drag.
  return (
    <div className="flex h-12 shrink-0 items-center gap-3.5 border-b bg-sidebar py-0 pr-2 pl-4 [-webkit-app-region:drag]">
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
        className={cn('[-webkit-app-region:no-drag]', !hasFolder && 'opacity-50')}
      />
      <div className="flex flex-1 items-center justify-end gap-3" data-testid="task-tray">
        <button
          className="flex h-[30px] items-center gap-2 rounded-lg border border-border bg-secondary px-3 text-xs text-muted-foreground hover:text-foreground [-webkit-app-region:no-drag] dark:bg-white/5"
          onClick={() => setPaletteOpen(true)}
          disabled={!hasFolder}
          style={!hasFolder ? { opacity: 0.5 } : undefined}
        >
          <span>Jump to anything</span>
          <span className="rounded bg-black/10 px-1.5 py-px font-mono dark:bg-white/10">⌘K</span>
        </button>
        <TopBarTasks />
        {window.win && <div className="h-[22px] w-px bg-black/10 dark:bg-white/9" />}
        <WindowControls />
      </div>
    </div>
  );
}

// TopBarTasks: one chip while a single job runs; several jobs collapse into
// a summary pill. Either opens the expanded tray (handoff "TASK TRAY").
function TopBarTasks() {
  const client = useApiClient();
  const tasks = useMyTasks();
  const [open, setOpen] = useState(false);
  useTaskToasts(tasks);

  const running = tasks.filter((t) => t.status === 'running' || t.status === 'created');
  if (tasks.length === 0) return null;

  const pctOf = (t: SharedTaskState) =>
    t.total ? Math.round(((t.current ?? 0) / t.total) * 100) : undefined;

  return (
    <div className="relative [-webkit-app-region:no-drag]">
      {running.length <= 1 && running[0] ? (
        <TaskChip
          label={running[0].title}
          count={running[0].total ? `${running[0].current ?? 0}/${running[0].total.toLocaleString()}` : undefined}
          pct={pctOf(running[0])}
          onCancel={() => cancelSharedTask(client, running[0].id).catch(() => {})}
          onClick={() => setOpen((v) => !v)}
          className="max-w-72 py-1.5"
        />
      ) : running.length > 1 ? (
        <button
          className="flex items-center gap-2.5 rounded-[11px] border border-glass-border bg-white/85 px-3 py-2 backdrop-blur-md dark:bg-[rgba(12,14,18,.78)]"
          onClick={() => setOpen((v) => !v)}
        >
          <ChipSpinner />
          <span className="text-xs text-secondary-foreground">{running.length} tasks</span>
          <ChipProgress pct={averagePct(running)} className="w-[84px]" />
          <span className="font-mono text-[10.5px] text-muted-foreground tabular-nums">
            {averagePct(running) != null ? `${averagePct(running)}%` : '…'}
          </span>
          <ChevronDown className={cn('size-3.5 text-muted-foreground transition-transform', open && 'rotate-180')} />
        </button>
      ) : null}
      {open && <TaskTrayCard tasks={tasks} onClose={() => setOpen(false)} />}
    </div>
  );
}

function averagePct(tasks: SharedTaskState[]): number | undefined {
  const measurable = tasks.filter((t) => t.total);
  if (measurable.length === 0) return undefined;
  const sum = measurable.reduce((n, t) => n + ((t.current ?? 0) / t.total!) * 100, 0);
  return Math.round(sum / measurable.length);
}

// TaskTrayCard: the expanded 384px tray — every background job with its own
// progress and cancel, finished jobs settling to a green check.
function TaskTrayCard({ tasks, onClose }: { tasks: SharedTaskState[]; onClose: () => void }) {
  const client = useApiClient();
  const running = tasks.filter((t) => t.status === 'running' || t.status === 'created');

  return (
    <>
      <div className="fixed inset-0 z-40" onClick={onClose} />
      <div className="absolute top-[calc(100%+8px)] right-0 z-50 flex w-96 flex-col rounded-xl border border-glass-border bg-popover/98 shadow-[0_30px_70px_-20px_rgba(0,0,0,.85)]">
        <div className="px-3.5 pt-3 pb-1 text-[10px] tracking-[.06em] text-muted-foreground uppercase">
          Background tasks · {running.length} running
        </div>
        <div className="flex max-h-80 flex-col gap-1 overflow-y-auto p-2">
          {tasks.map((t) =>
            t.status === 'completed' ? (
              <div key={t.id} className="flex items-center gap-2.5 rounded-lg px-2.5 py-2">
                <Check className="size-3.5 shrink-0 text-success" />
                <span className="truncate text-xs text-secondary-foreground">{t.title} · Done</span>
              </div>
            ) : (
              <div key={t.id} className="flex items-start gap-2.5 rounded-lg px-2.5 py-2 hover:bg-accent">
                <ChipSpinner className="mt-0.5" />
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <div className="flex items-baseline justify-between gap-3">
                    <span className="truncate text-xs text-foreground">{t.title}</span>
                    {t.total != null && t.total > 0 && (
                      <span className="shrink-0 font-mono text-[10.5px] text-muted-foreground tabular-nums">
                        {(t.current ?? 0).toLocaleString()}/{t.total.toLocaleString()}
                      </span>
                    )}
                  </div>
                  <ChipProgress pct={t.total ? Math.round(((t.current ?? 0) / t.total) * 100) : undefined} />
                  {taskSublabel(t) && (
                    <span className="truncate text-[10.5px] text-faint">{taskSublabel(t)}</span>
                  )}
                </div>
                <button
                  className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-[7px] border border-glass-border text-muted-foreground hover:text-foreground"
                  onClick={() => cancelSharedTask(client, t.id).catch(() => {})}
                  aria-label={`Cancel ${t.title}`}
                  title="Cancel"
                >
                  <X className="size-3" />
                </button>
              </div>
            ),
          )}
          {tasks.length === 0 && (
            <div className="px-2.5 py-3 text-xs text-muted-foreground">No background tasks.</div>
          )}
        </div>
        <div className="flex items-center border-t px-3.5 py-2.5">
          <span className="font-mono text-[11px] text-muted-foreground">Runs in the background</span>
          {running.length > 0 && (
            <button
              className="ml-auto text-xs text-danger-text hover:underline"
              onClick={() => {
                for (const t of running) void cancelSharedTask(client, t.id).catch(() => {});
              }}
            >
              Cancel all
            </button>
          )}
        </div>
      </div>
    </>
  );
}

function taskSublabel(t: SharedTaskState): string | null {
  const meta = t.meta as { kind?: string; folder?: string; destDir?: string } | undefined;
  if (meta?.kind === 'export' && meta.destDir) return `→ ${meta.destDir}`;
  if (meta?.folder) return meta.folder;
  return null;
}
