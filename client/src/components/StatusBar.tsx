import { useConnection } from '@/api/client';
import { cn } from '@/lib/utils';
import { useUIStore } from '@/stores/uiStore';

export function StatusBar({ shown, total }: { shown: number; total: number }) {
  const { state } = useConnection();
  const selection = useUIStore((s) => s.selection);
  const view = useUIStore((s) => s.view);

  return (
    <div className="flex h-6 shrink-0 items-center gap-3 border-t px-3 text-[11px] text-muted-foreground">
      <span className="flex items-center gap-1.5">
        <span
          className={cn(
            'size-2 rounded-full',
            state === 'connected' ? 'bg-emerald-500' : state === 'disconnected' ? 'bg-red-500' : 'bg-amber-500',
          )}
        />
        {state}
      </span>
      <span>
        {shown}/{total} photos
        {selection.size > 0 && ` · ${selection.size} selected`}
      </span>
      <span className="ml-auto hidden sm:block">
        {view === 'grid'
          ? '↑↓←→ navigate · 1–5 rate · P pick · X exclude · Enter loupe · Ctrl+E export'
          : '←→ navigate · double-click zoom · 1–5 rate · X exclude · Esc grid'}
      </span>
    </div>
  );
}
