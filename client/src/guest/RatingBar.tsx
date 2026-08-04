import { Check, Star, X } from 'lucide-react';

import type { FlagType } from '@/api/library';
import { cn } from '@/lib/utils';

// The culling controls. Deliberately large and always visible rather than the
// app's keyboard shortcuts: this is someone on a sofa with a phone, and the
// whole job is "rate, pick, next".

interface Props {
  rating: number;
  flag: FlagType;
  onRate: (rating: number) => void;
  onFlag: (flag: FlagType) => void;
  className?: string;
}

export function RatingBar({ rating, flag, onRate, onFlag, className }: Props) {
  return (
    <div className={cn('flex items-center justify-center gap-1', className)}>
      {[1, 2, 3, 4, 5].map((n) => (
        <button
          key={n}
          type="button"
          aria-label={`${n} star${n === 1 ? '' : 's'}`}
          aria-pressed={rating >= n}
          // Tapping the current rating clears it, so a mis-tap is undone the
          // same way it was made.
          onClick={() => onRate(rating === n ? 0 : n)}
          className="grid size-9 place-items-center rounded-full active:bg-white/10"
        >
          <Star
            className={cn('size-5', rating >= n ? 'fill-amber-400 text-amber-400' : 'text-white/40')}
            strokeWidth={1.5}
          />
        </button>
      ))}
      <span className="mx-0.5 h-5 w-px bg-white/15" />
      <button
        type="button"
        aria-label="Pick"
        aria-pressed={flag === 'pick'}
        onClick={() => onFlag(flag === 'pick' ? 'none' : 'pick')}
        className={cn(
          'grid size-9 place-items-center rounded-full active:bg-white/10',
          flag === 'pick' && 'bg-emerald-500/20',
        )}
      >
        <Check className={cn('size-5', flag === 'pick' ? 'text-emerald-400' : 'text-white/40')} />
      </button>
      <button
        type="button"
        aria-label="Reject"
        aria-pressed={flag === 'exclude'}
        onClick={() => onFlag(flag === 'exclude' ? 'none' : 'exclude')}
        className={cn(
          'grid size-9 place-items-center rounded-full active:bg-white/10',
          flag === 'exclude' && 'bg-rose-500/20',
        )}
      >
        <X className={cn('size-5', flag === 'exclude' ? 'text-rose-400' : 'text-white/40')} />
      </button>
    </div>
  );
}
