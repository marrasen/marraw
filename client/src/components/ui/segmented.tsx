import { cn } from '@/lib/utils';

export interface SegmentedItem<T extends string> {
  value: T;
  label: React.ReactNode;
  disabled?: boolean;
  title?: string;
}

/**
 * Segmented control per the handoff spec: a bordered pill track with an
 * accent-tinted active segment. `variant="glass"` floats over the photo in
 * cinema modes; `size="sm"` is the compact in-panel form (filter bar, WB
 * mode, export format).
 */
export function Segmented<T extends string>({
  items,
  value,
  onValueChange,
  variant = 'panel',
  size = 'md',
  className,
  disabled,
  'aria-label': ariaLabel,
}: {
  items: SegmentedItem<T>[];
  value: T;
  onValueChange: (v: T) => void;
  variant?: 'panel' | 'glass';
  size?: 'sm' | 'md';
  className?: string;
  disabled?: boolean;
  'aria-label'?: string;
}) {
  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      className={cn(
        'flex items-center gap-0.5 rounded-[9px] border p-1',
        variant === 'panel' && 'border-border bg-secondary dark:bg-white/5',
        variant === 'glass' && 'glass',
        disabled && 'pointer-events-none opacity-50',
        className,
      )}
    >
      {items.map((item) => {
        const active = item.value === value;
        return (
          <button
            key={item.value}
            role="radio"
            aria-checked={active}
            disabled={disabled || item.disabled}
            title={item.title}
            className={cn(
              'rounded-md whitespace-nowrap transition-colors disabled:opacity-40',
              size === 'md' ? 'px-[15px] py-[5px] text-[12.5px]' : 'px-2.5 py-1 text-[11.5px]',
              active
                ? 'bg-primary/15 text-accent-text dark:bg-primary/25'
                : 'text-muted-foreground hover:text-foreground',
            )}
            onClick={() => onValueChange(item.value)}
          >
            {item.label}
          </button>
        );
      })}
    </div>
  );
}
