import { useEffect, useState } from 'react';
import { Minus, Pin, PinOff, Square, X } from 'lucide-react';
import { cn } from '@/lib/utils';
import '@/lib/electron';

// RestoreGlyph: two overlapping rounded squares (window is maximized).
function RestoreGlyph() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" strokeWidth="1.25">
      <rect x="1.5" y="3.5" width="7" height="7" rx="1.4" />
      <path d="M4 1.5h5.1A1.4 1.4 0 0 1 10.5 2.9V8" />
    </svg>
  );
}

/**
 * The frameless window's own minimize / maximize-restore / close cluster
 * (diff handoff "frameless window + baked-in controls"). `variant="bar"`
 * sits in the structured 48px top bar; `variant="glass"` is the floating
 * pill of the cinema HUD. Renders nothing outside Electron.
 */
export function WindowControls({
  variant = 'bar',
  pinned,
  onTogglePin,
}: {
  variant?: 'bar' | 'glass';
  /**
   * Always-on-top state. Passing onTogglePin leads the cluster with a pin —
   * the pop-out photo window's, where floating over other apps is a choice
   * and this is the only chrome there is to put it in.
   */
  pinned?: boolean;
  onTogglePin?: (next: boolean) => void;
}) {
  const [max, setMax] = useState(false);
  useEffect(() => {
    if (!window.win) return;
    window.win.onMaxChange(setMax);
    void window.win.isMax().then(setMax);
  }, []);
  if (!window.win) return null;

  const glass = variant === 'glass';
  const btn = cn(
    'grid place-items-center transition-colors [-webkit-app-region:no-drag]',
    glass
      ? 'h-[26px] w-[28px] rounded-md text-secondary-foreground hover:bg-black/10 hover:text-foreground dark:hover:bg-white/14'
      : 'h-[30px] w-[30px] rounded-[7px] text-muted-foreground hover:bg-black/5 hover:text-foreground dark:hover:bg-white/8',
  );
  const closeBtn = cn(
    btn,
    'hover:bg-[rgba(209,84,79,.12)] hover:text-[#c53d3d] dark:hover:bg-[rgba(242,109,109,.16)] dark:hover:text-[#f28c8c]',
  );

  const cluster = (
    <div className="flex items-center gap-0.5 [-webkit-app-region:no-drag]">
      {onTogglePin && (
        <button
          aria-label={pinned ? 'Unpin from the front' : 'Keep in front'}
          aria-pressed={pinned}
          title={pinned ? 'Stop floating over other apps' : 'Keep this window over other apps'}
          className={cn(btn, pinned && 'text-primary')}
          onClick={() => onTogglePin(!pinned)}
        >
          {pinned ? <Pin className="size-[12px]" strokeWidth={1.5} /> : <PinOff className="size-[12px]" strokeWidth={1.5} />}
        </button>
      )}
      <button aria-label="Minimize" title="Minimize" className={btn} onClick={() => window.win!.minimize()}>
        <Minus className="size-[11px]" strokeWidth={1.3} />
      </button>
      <button
        aria-label={max ? 'Restore' : 'Maximize'}
        title={max ? 'Restore' : 'Maximize'}
        className={btn}
        onClick={() => window.win!.toggleMax()}
      >
        {max ? <RestoreGlyph /> : <Square className="size-[11px]" strokeWidth={1.25} />}
      </button>
      <button aria-label="Close" title="Close" className={closeBtn} onClick={() => window.win!.close()}>
        <X className="size-[11px]" strokeWidth={1.4} />
      </button>
    </div>
  );

  if (glass) {
    return <div className="glass flex h-[34px] items-center rounded-[9px] px-1">{cluster}</div>;
  }
  return cluster;
}
