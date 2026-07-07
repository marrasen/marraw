import { useState } from 'react';
import { Check, ChevronDown, Loader2, X } from 'lucide-react';
import { ChipProgress, ChipSpinner, TaskChip } from '@/components/ui/task-chip';
import { useApiClient } from '@/api/client';
import { useMyTasks, cancelSharedTask } from '@/api/tasks';
import type { SharedTaskState } from '@/api/tasks-handler';
import { cn } from '@/lib/utils';
import { useEditSession } from '@/lib/editSession';
import { useTaskToasts } from '@/lib/taskToasts';

// RenderSpinner: a tiny status-bar note while an edit preview render is in
// flight (not a shared task — request-scoped).
export function RenderSpinner() {
  const rendering = useEditSession((s) => s.rendering);
  if (rendering === 0) return null;
  return (
    <span className="flex items-center gap-1.5 text-muted-foreground">
      <Loader2 className="size-3 animate-spin" />
      Rendering preview…
    </span>
  );
}

// TaskTray: one chip while a single job runs; several jobs collapse into a
// summary pill. Either opens the expanded tray (handoff "TASK TRAY"). Lives
// in the top bar (library grid) and the cinema HUD's right cluster.
export function TaskTray() {
  const client = useApiClient();
  const tasks = useMyTasks();
  const [open, setOpen] = useState(false);

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

// TaskToasts: the single app-level owner of terminal-state toasts, mounted
// once in App so "done"/"failed" fire in every mode — including the cinema
// canvases where the tray chip fades out with the chrome.
export function TaskToasts() {
  useTaskToasts(useMyTasks());
  return null;
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
