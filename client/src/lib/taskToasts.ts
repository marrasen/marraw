import { useEffect, useRef } from 'react';
import { toast } from 'sonner';
import type { SharedTaskState } from '@/api/tasks-handler';

// useTaskToasts toasts terminal task transitions: the server broadcasts the
// completed/failed snapshot briefly before removing the task. Used by the
// top-bar chips (which render the running set).
export function useTaskToasts(tasks: SharedTaskState[]) {
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
}

function doneMessage(t: SharedTaskState): string {
  const meta = t.meta as { kind?: string; destDir?: string } | undefined;
  if (meta?.kind === 'export' && meta.destDir) return `${t.title} done → ${meta.destDir}`;
  return `${t.title} done`;
}
