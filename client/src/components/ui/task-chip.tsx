import { X } from 'lucide-react';
import { cn } from '@/lib/utils';

/** 15px spinner ring, stroke #aab0ff per the shared task-chip spec. */
export function ChipSpinner({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 15 15"
      fill="none"
      strokeWidth="1.5"
      className={cn('size-[15px] shrink-0 animate-spin stroke-[#aab0ff] dark:stroke-[#aab0ff]', className)}
    >
      <circle cx="7.5" cy="7.5" r="6" opacity=".3" />
      <path d="M7.5 1.5a6 6 0 0 1 6 6" />
    </svg>
  );
}

/** 4px progress bar; undefined = indeterminate moving gradient segment. */
export function ChipProgress({ pct, className }: { pct?: number; className?: string }) {
  return (
    <div className={cn('relative h-1 w-full overflow-hidden rounded-sm bg-black/10 dark:bg-white/12', className)}>
      {pct != null ? (
        <div
          className="h-full rounded-sm bg-primary transition-[width] duration-300"
          style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
        />
      ) : (
        <div className="absolute inset-y-0 w-1/3 animate-chip-indeterminate bg-gradient-to-r from-transparent via-primary to-transparent" />
      )}
    </div>
  );
}

/**
 * Shared background-task chip (handoff "Design Tokens → Task chip"). One
 * style for preview generation, import scans and exports — floats in the
 * top bar and stacks in the expanded tray.
 */
export function TaskChip({
  label,
  count,
  pct,
  onCancel,
  onClick,
  className,
}: {
  label: string;
  count?: string;
  pct?: number;
  onCancel?: () => void;
  onClick?: () => void;
  className?: string;
}) {
  return (
    <div
      className={cn(
        'flex items-center gap-[11px] rounded-[11px] border border-glass-border py-2.5 pr-2.5 pl-3.5',
        'bg-white/85 shadow-[0_16px_40px_-14px_rgba(0,0,0,.35)] backdrop-blur-md dark:bg-[rgba(12,14,18,.78)] dark:shadow-[0_16px_40px_-14px_rgba(0,0,0,.7)]',
        onClick && 'cursor-pointer',
        className,
      )}
      onClick={onClick}
    >
      <ChipSpinner className="stroke-primary dark:stroke-[#aab0ff]" />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex items-baseline justify-between gap-3 font-mono text-[10.5px] leading-none">
          <span className="truncate text-secondary-foreground">{label}</span>
          {count && <span className="shrink-0 text-muted-foreground tabular-nums">{count}</span>}
        </div>
        <ChipProgress pct={pct} />
      </div>
      {onCancel && (
        <button
          className="flex size-6 shrink-0 items-center justify-center rounded-[7px] border border-glass-border text-muted-foreground hover:text-foreground"
          onClick={(e) => {
            e.stopPropagation();
            onCancel();
          }}
          aria-label={`Cancel ${label}`}
          title="Cancel"
        >
          <X className="size-3" />
        </button>
      )}
    </div>
  );
}
