import { useState } from 'react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useApiClient } from '@/api/client';
import { cn } from '@/lib/utils';
import { updateGapMinutes } from '@/lib/uiSettings';
import { useUIStore } from '@/stores/uiStore';

const PRESETS: { min: number; hint?: string }[] = [
  { min: 1, hint: 'bursts' },
  { min: 2 },
  { min: 5, hint: 'recommended' },
  { min: 10 },
  { min: 30, hint: 'scenes' },
];

/**
 * The group-by-gap threshold control (Cull HUD / contact sheet): presets
 * mapped to how photographers shoot, a custom minute value, or Off.
 */
export function GapControl({
  glass,
  labelClassName,
}: {
  glass?: boolean;
  /** Extra classes for the "Group by gap" label — lets the filter bar hide it
      on a narrow toolbar via a container-query variant. */
  labelClassName?: string;
}) {
  const client = useApiClient();
  const gapMinutes = useUIStore((s) => s.gapMinutes);
  const setGapMinutes = (min: number | null) =>
    updateGapMinutes(client, min == null ? null : Math.max(1, Math.round(min)));
  const [custom, setCustom] = useState(String(gapMinutes ?? 6));
  const isPreset = gapMinutes != null && PRESETS.some((p) => p.min === gapMinutes);
  // Parsed custom value (matching setGapMinutes' clamp), and whether it differs
  // from the committed setting — drives the Apply affordance.
  const customValue = (() => {
    const n = Number(custom);
    return Number.isFinite(n) && n > 0 ? Math.max(1, Math.round(n)) : null;
  })();
  const customDirty = customValue != null && customValue !== gapMinutes;

  return (
    <DropdownMenu
      onOpenChange={(open) => {
        // Re-sync the custom field from the live setting each time the menu
        // opens, so it never shows (or commits) a stale value from a prior
        // session.
        if (open) setCustom(String(gapMinutes ?? 6));
      }}
    >
      <DropdownMenuTrigger
        className={cn(
          'flex h-[34px] items-center gap-2 rounded-[9px] px-3 text-xs',
          glass ? 'glass' : 'border border-border bg-secondary dark:bg-white/5',
        )}
      >
        <span className={cn('whitespace-nowrap text-muted-foreground', labelClassName)}>Group by gap</span>
        <span className="flex items-center gap-1.5 rounded-[5px] bg-primary/20 px-2 py-0.5 font-mono whitespace-nowrap text-accent-text">
          {gapMinutes == null ? 'Off' : `${gapMinutes} min`}
          <span className="opacity-60">▾</span>
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-[240px] rounded-[11px] border-glass-border bg-popover/98 p-[7px]">
        <div className="px-2.5 pt-1.5 pb-2 text-[10px] tracking-[.06em] text-muted-foreground uppercase">
          New group when gap exceeds
        </div>
        {PRESETS.map((p) => (
          <DropdownMenuItem
            key={p.min}
            className="flex h-8 justify-between rounded-[7px] px-2.5 text-[13px]"
            onClick={() => setGapMinutes(p.min)}
          >
            <span className={cn(gapMinutes === p.min && 'font-semibold text-foreground')}>
              {p.min} min
            </span>
            {p.hint && (
              <span
                className={cn(
                  'font-mono text-[10.5px]',
                  p.hint === 'recommended' ? 'text-primary' : 'text-faint',
                )}
              >
                {p.hint}
              </span>
            )}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <div
          className={cn(
            'flex h-10 items-center gap-2 rounded-[7px] py-0 pr-2 pl-2.5 text-[13px]',
            !isPreset && gapMinutes != null && 'border border-primary/30 bg-sidebar-accent',
          )}
        >
          <span>Custom</span>
          <div className="ml-auto flex items-center gap-1">
            <input
              className="h-[26px] w-9 rounded-md border border-input bg-black/20 text-center font-mono text-xs text-foreground outline-none focus:border-primary"
              value={custom}
              onChange={(e) => setCustom(e.target.value)}
              onKeyDown={(e) => {
                e.stopPropagation();
                if (e.key === 'Enter' && customValue != null) setGapMinutes(customValue);
              }}
              aria-label="Custom gap minutes"
            />
            <span className="font-mono text-[11px] text-muted-foreground">min</span>
            <button
              type="button"
              disabled={!customDirty}
              onClick={() => customValue != null && setGapMinutes(customValue)}
              className={cn(
                'flex h-[26px] items-center gap-1 rounded-md px-1.5 font-mono text-[11px] transition-opacity',
                customDirty
                  ? 'bg-primary/20 text-accent-text'
                  : 'pointer-events-none opacity-0',
              )}
              aria-label="Apply custom gap"
            >
              Apply <span className="opacity-70">↵</span>
            </button>
          </div>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          className="flex h-8 rounded-[7px] px-2.5 text-[13px] text-muted-foreground"
          onClick={() => setGapMinutes(null)}
        >
          Off — one flat grid
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
