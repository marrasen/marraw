import { useEffect, useRef } from 'react';
import { toast } from 'sonner';
import { Loader2, X } from 'lucide-react';
import { useApiClient } from '@/api/client';
import { useMyTasks, cancelSharedTask } from '@/api/tasks';
import type { SharedTaskState } from '@/api/tasks-handler';
import { Progress } from '@/components/ui/progress';
import { useEditSession } from '@/lib/editSession';

// TaskTray shows every running backend job (scan, pre-render, export, …) as
// a compact progress bar with a cancel button, plus a spinner while an edit
// preview render is in flight. Lives at the bottom left in the status bar.
export function TaskTray() {
  const client = useApiClient();
  const tasks = useMyTasks();
  const rendering = useEditSession((s) => s.rendering);

  // Toast terminal transitions: the server broadcasts the completed/failed
  // snapshot briefly before removing the task.
  const seen = useRef(new Map<string, SharedTaskState>());
  useEffect(() => {
    const prev = seen.current;
    const next = new Map(tasks.map((t) => [t.id, t]));
    for (const t of tasks) {
      const before = prev.get(t.id);
      if (before?.status === t.status) continue;
      if (t.status === 'completed') {
        toast.success(doneMessage(t));
      } else if (t.status === 'failed') {
        if (t.error === 'canceled') toast.info(`${t.title} canceled`);
        else toast.error(`${t.title} failed: ${t.error ?? 'unknown error'}`);
      }
    }
    seen.current = next;
  }, [tasks]);

  const running = tasks.filter((t) => t.status === 'running' || t.status === 'created');

  return (
    <div className="flex min-w-0 items-center gap-4" data-testid="task-tray">
      {running.map((t) => (
        <TaskRow key={t.id} task={t} onCancel={() => cancelSharedTask(client, t.id).catch(() => {})} />
      ))}
      {rendering > 0 && (
        <span className="flex items-center gap-1.5 text-muted-foreground">
          <Loader2 className="size-3 animate-spin" />
          Rendering preview…
        </span>
      )}
    </div>
  );
}

function doneMessage(t: SharedTaskState): string {
  const meta = t.meta as { kind?: string; destDir?: string } | undefined;
  if (meta?.kind === 'export' && meta.destDir) return `${t.title} done → ${meta.destDir}`;
  return `${t.title} done`;
}

function TaskRow({ task, onCancel }: { task: SharedTaskState; onCancel: () => void }) {
  const pct = task.total ? Math.round(((task.current ?? 0) / task.total) * 100) : 0;
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <span className="max-w-44 truncate" title={task.title}>
        {task.title}
      </span>
      <Progress value={pct} className="w-24" />
      <span className="tabular-nums text-muted-foreground">
        {task.total ? `${task.current ?? 0}/${task.total}` : '…'}
      </span>
      <button
        className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
        onClick={onCancel}
        title="Cancel"
        aria-label={`Cancel ${task.title}`}
      >
        <X className="size-3" />
      </button>
    </div>
  );
}
