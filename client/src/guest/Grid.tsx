import { Check, Star, X } from 'lucide-react';

import type { Photo } from '@/api/library';
import { imgUrl, levelForSize } from '@/lib/backend';
import { cn } from '@/lib/utils';

// The album. auto-fill rather than a fixed column count, so a phone in
// portrait gets three-up, a tablet six, and a laptop as many as it has room
// for — one layout instead of a set of breakpoints.

interface Props {
  photos: Photo[];
  selecting: boolean;
  selected: Set<number>;
  onOpen: (index: number) => void;
  onToggleSelect: (id: number) => void;
}

export function Grid({ photos, selecting, selected, onOpen, onToggleSelect }: Props) {
  return (
    <div
      className="grid gap-1 p-1"
      style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(clamp(96px, 30vw, 200px), 1fr))' }}
    >
      {photos.map((p, i) => {
        const isSelected = selected.has(p.id);
        return (
          <button
            key={p.id}
            type="button"
            onClick={() => (selecting ? onToggleSelect(p.id) : onOpen(i))}
            className="relative aspect-square overflow-hidden rounded-sm bg-zinc-900 active:opacity-70"
          >
            <img
              // The thumbnail level is picked from the rendered size, so a
              // phone never pulls a 2048 to paint a 120px tile — this page is
              // usually on mobile data.
              src={imgUrl(p, levelForSize(200, '512'))}
              alt={p.fileName}
              loading="lazy"
              decoding="async"
              className="size-full object-cover"
            />
            {p.flag === 'exclude' && <div className="absolute inset-0 bg-black/50" />}
            <Badges rating={p.rating} flag={p.flag} />
            {selecting && (
              <span
                className={cn(
                  'absolute top-1 right-1 grid size-6 place-items-center rounded-full border',
                  isSelected ? 'border-white bg-white text-black' : 'border-white/70 bg-black/30',
                )}
              >
                {isSelected && <Check className="size-4" />}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

// Rating and flag are shown on the tile itself: the point of the page is
// seeing at a glance what has been picked, without opening anything.
function Badges({ rating, flag }: { rating: number; flag: Photo['flag'] }) {
  if (!rating && flag === 'none') return null;
  return (
    <div className="absolute inset-x-0 bottom-0 flex items-center gap-1 bg-gradient-to-t from-black/70 to-transparent px-1 pt-4 pb-1">
      {rating > 0 && (
        <span className="flex items-center gap-0.5 text-[11px] text-amber-300">
          <Star className="size-3 fill-amber-300" strokeWidth={0} />
          {rating}
        </span>
      )}
      {flag === 'pick' && <Check className="size-3.5 text-emerald-400" />}
      {flag === 'exclude' && <X className="size-3.5 text-rose-400" />}
    </div>
  );
}
