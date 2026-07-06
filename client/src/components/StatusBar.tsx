import { Settings } from 'lucide-react';
import { useConnection } from '@/api/client';
import { cn } from '@/lib/utils';
import { TaskTray } from '@/components/TaskTray';
import { useUIStore } from '@/stores/uiStore';

export function StatusBar({ shown, total }: { shown: number; total: number }) {
  const { state } = useConnection();
  const selection = useUIStore((s) => s.selection);
  const view = useUIStore((s) => s.view);
  const setSettingsOpen = useUIStore((s) => s.setSettingsOpen);

  return (
    <div className="flex h-6 shrink-0 items-center gap-3 border-t px-3 text-[11px] text-muted-foreground">
      <span className="flex items-center gap-1.5" title={state}>
        <span
          className={cn(
            'size-2 rounded-full',
            state === 'connected' ? 'bg-emerald-500' : state === 'disconnected' ? 'bg-red-500' : 'bg-amber-500',
          )}
        />
      </span>
      <button
        className="flex items-center gap-1 hover:text-foreground"
        onClick={() => setSettingsOpen(true)}
        title="Settings"
      >
        <Settings className="size-3.5" />
      </button>
      <TaskTray />
      <span className="ml-auto whitespace-nowrap">
        {shown}/{total} photos
        {selection.size > 0 && ` · ${selection.size} selected`}
      </span>
      <span className="hidden xl:block">
        {view === 'grid'
          ? '↑↓←→ navigate · 1–5 rate · P/X/U flag · Enter loupe · E/B/W… focus control · Ctrl+E export'
          : '←→ navigate · +/- zoom · Space 1:1 · drag to pan · E/B/W… control then +/- adjust · Esc back · Ctrl+Z undo'}
      </span>
    </div>
  );
}
