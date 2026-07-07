import { Segmented } from '@/components/ui/segmented';
import { TaskTray } from '@/components/TaskTray';
import { WindowControls } from '@/components/WindowControls';
import { useConnection } from '@/api/client';
import { modK } from '@/lib/platform';
import { cn } from '@/lib/utils';
import { rootName, samePath, useLibraryRoots } from '@/lib/library';
import { useUIStore, type Mode } from '@/stores/uiStore';

const MODE_ITEMS: { value: Mode | 'export'; label: string }[] = [
  { value: 'library', label: 'Library' },
  { value: 'cull', label: 'Cull' },
  { value: 'develop', label: 'Develop' },
  { value: 'export', label: 'Export' },
];

/**
 * The three floating glass clusters of a cinema canvas: status (left), the
 * mode segmented control (center), and a context slot (right). Fades out
 * with the rest of the chrome when `hidden`.
 */
export function CinemaHUD({
  status,
  right,
  hidden,
}: {
  /** Extra content after the logo + shoot name in the left cluster. */
  status?: React.ReactNode;
  /** The right cluster (⌘K affordance, group-by-gap control, …). */
  right?: React.ReactNode;
  hidden?: boolean;
}) {
  const mode = useUIStore((s) => s.mode);
  const setMode = useUIStore((s) => s.setMode);
  const setExportOpen = useUIStore((s) => s.setExportOpen);
  const folderPath = useUIStore((s) => s.folderPath);
  const { roots } = useLibraryRoots();
  const { state } = useConnection();
  const current = folderPath ? roots.find((r) => samePath(r.path, folderPath)) : undefined;

  return (
    <div
      className={cn(
        'pointer-events-none absolute inset-x-0 top-0 z-30 transition-opacity duration-300',
        hidden && 'opacity-0',
      )}
    >
      {/* The top band doubles as the frameless window's move handle; the
          glass clusters below carve themselves out with no-drag. */}
      <div className="pointer-events-auto absolute inset-x-0 top-0 h-12 [-webkit-app-region:drag]" />
      <div className={cn('absolute top-4 left-[18px] [-webkit-app-region:no-drag]', !hidden && 'pointer-events-auto')}>
        <div className="glass flex items-center gap-2.5 rounded-[9px] px-3 py-[7px]">
          <div className="flex size-[18px] items-center justify-center rounded-[5px] bg-primary text-[11px] font-bold text-primary-foreground">
            m
          </div>
          <span className="text-[12.5px] font-semibold">
            {current ? rootName(current) : (folderPath ?? 'marraw')}
          </span>
          {status}
          <span
            className={cn(
              'size-1.5 rounded-full',
              state === 'connected' ? 'bg-success' : state === 'disconnected' ? 'bg-destructive' : 'bg-rating',
            )}
            title={state}
          />
        </div>
      </div>
      <div className={cn('absolute top-4 left-1/2 -translate-x-1/2 [-webkit-app-region:no-drag]', !hidden && 'pointer-events-auto')}>
        <Segmented
          aria-label="Mode"
          variant="glass"
          items={MODE_ITEMS}
          value={mode}
          onValueChange={(v) => {
            if (v === 'export') setExportOpen(true);
            else setMode(v);
          }}
        />
      </div>
      <div
        className={cn(
          'absolute top-4 right-[18px] flex items-center gap-3 [-webkit-app-region:no-drag]',
          !hidden && 'pointer-events-auto',
        )}
      >
        <TaskTray />
        {right}
        <WindowControls variant="glass" />
      </div>
    </div>
  );
}

/** The ⌘K affordance as a glass chip (right HUD cluster). */
export function PaletteChip({ label = 'Jump to any control' }: { label?: string }) {
  const setPaletteOpen = useUIStore((s) => s.setPaletteOpen);
  return (
    <button
      className="glass flex h-[34px] items-center gap-2 rounded-[9px] px-3 text-xs text-muted-foreground hover:text-foreground"
      onClick={() => setPaletteOpen(true)}
    >
      <span>{label}</span>
      <span className="rounded bg-white/10 px-1.5 py-px font-mono">{modK}</span>
    </button>
  );
}
