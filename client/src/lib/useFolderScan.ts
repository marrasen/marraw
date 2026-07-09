import { useMyTasks } from '@/api/tasks';
import { samePath } from '@/lib/library';

export interface ScanProgress {
  current: number;
  total: number;
}

/**
 * useFolderScan reports the running header-indexing scan of a folder (the
 * "scan" background pass), driving the freshly-added scanning states:
 * "N ready / M" counts and the amber status-bar note.
 */
export function useFolderScan(folderPath: string | null): ScanProgress | null {
  const tasks = useMyTasks();
  if (!folderPath) return null;
  const t = tasks.find((t) => {
    if (t.status !== 'running' && t.status !== 'created') return false;
    const meta = t.meta as { kind?: string; folderPath?: string } | undefined;
    return meta?.kind === 'scan' && meta.folderPath != null && samePath(meta.folderPath, folderPath);
  });
  return t ? { current: t.current ?? 0, total: t.total ?? 0 } : null;
}
