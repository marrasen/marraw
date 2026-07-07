import { useConnection } from '@/api/client';
import { cn } from '@/lib/utils';
import { RenderSpinner } from '@/components/TaskTray';
import { useUIStore } from '@/stores/uiStore';

/**
 * The 26px mono status strip under the grid: path · counts · picked · render
 * note · daemon connection.
 */
export function StatusBar({
  shown,
  total,
  picked,
}: {
  shown: number;
  total: number;
  picked: number;
}) {
  const { state } = useConnection();
  const folderPath = useUIStore((s) => s.folderPath);
  const selection = useUIStore((s) => s.selection);

  return (
    <div className="flex h-[26px] shrink-0 items-center gap-4 border-t bg-sidebar px-4 font-mono text-[11px] text-muted-foreground">
      {folderPath && <span className="truncate">{folderPath}</span>}
      <span className="shrink-0 tabular-nums">
        {shown.toLocaleString()} / {total.toLocaleString()}
      </span>
      {picked > 0 && <span className="shrink-0 text-success-text">{picked.toLocaleString()} picked</span>}
      {selection.size > 1 && <span className="shrink-0">{selection.size} selected</span>}
      <span className="flex-1" />
      <RenderSpinner />
      <span className="flex shrink-0 items-center gap-1.5" title={state}>
        <span
          className={cn(
            'size-1.5 rounded-full',
            state === 'connected' ? 'bg-success' : state === 'disconnected' ? 'bg-destructive' : 'bg-rating',
          )}
        />
        {state === 'connected' ? 'daemon connected' : state}
      </span>
    </div>
  );
}
