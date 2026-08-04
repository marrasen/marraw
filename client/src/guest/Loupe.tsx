import { useEffect, useState } from 'react';
import { ChevronLeft, ChevronRight, Download, Loader2, X } from 'lucide-react';

import type { FlagType, Photo } from '@/api/library';
import { imgUrl, levelForSize } from '@/lib/backend';
import { cn } from '@/lib/utils';

import { useGestures } from './gestures';
import { RatingBar } from './RatingBar';
import { savePhoto } from './save';

interface Props {
  photos: Photo[];
  index: number;
  canDownload: boolean;
  onIndex: (index: number) => void;
  onClose: () => void;
  onRate: (id: number, rating: number) => void;
  onFlag: (id: number, flag: FlagType) => void;
}

export function Loupe({ photos, index, canDownload, onIndex, onClose, onRate, onFlag }: Props) {
  const photo = photos[index];
  const [chrome, setChrome] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const next = () => onIndex(Math.min(photos.length - 1, index + 1));
  const prev = () => onIndex(Math.max(0, index - 1));
  const { transform, drag, zoomed, reset, handlers } = useGestures({ onNext: next, onPrev: prev, onClose });

  // A new photo starts at fit — carrying the previous one's zoom over would
  // drop the viewer into the middle of an image they have not seen yet.
  useEffect(() => reset(), [index, reset]);

  // Keyboard for whoever opens the link on a laptop. Same keys as the app, so
  // the muscle memory of anyone who has used marraw still works.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowRight') next();
      else if (e.key === 'ArrowLeft') prev();
      else if (e.key === 'Escape') onClose();
      else if (e.key >= '0' && e.key <= '5') onRate(photo.id, Number(e.key));
      else if (e.key === 'p') onFlag(photo.id, photo.flag === 'pick' ? 'none' : 'pick');
      else if (e.key === 'x') onFlag(photo.id, photo.flag === 'exclude' ? 'none' : 'exclude');
      else return;
      e.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    try {
      await savePhoto(photo.id, photo.fileName);
    } catch (err) {
      setSaveError((err as Error).message || 'Could not save that photo.');
    } finally {
      setSaving(false);
    }
  };

  if (!photo) return null;
  const offset = drag ?? { x: 0, y: 0 };

  return (
    <div className="fixed inset-0 z-10 flex flex-col bg-black">
      <div
        // touch-none: every gesture here is ours, and letting the browser also
        // scroll or page-zoom makes both feel unreliable.
        className="relative flex-1 touch-none overflow-hidden"
        {...handlers}
        onPointerUp={(e) => {
          const kind = handlers.onPointerUp(e);
          if (kind === 'tap') setChrome((c) => !c);
        }}
      >
        <img
          key={photo.id}
          // 2048 is the largest pyramid level; past it the app switches to
          // full-resolution tiles, which is a lot of machinery for "does this
          // frame work". Deep zoom can come later if anyone misses it.
          src={imgUrl(photo, levelForSize(Math.max(window.innerWidth, window.innerHeight)))}
          alt={photo.fileName}
          draggable={false}
          className="size-full object-contain"
          style={{
            transform: `translate3d(${transform.x + offset.x}px, ${transform.y + offset.y}px, 0) scale(${transform.scale})`,
            // No transition while a finger is down: the image must track the
            // gesture exactly, and ease back only once it is released.
            transition: drag ? 'none' : 'transform 180ms ease-out',
            opacity: drag && !zoomed ? Math.max(0.35, 1 - Math.abs(offset.y) / 400) : 1,
          }}
        />

        {chrome && (
          <>
            <div className="pointer-events-none absolute inset-x-0 top-0 flex items-start justify-between bg-gradient-to-b from-black/70 to-transparent p-2 pt-[max(0.5rem,env(safe-area-inset-top))]">
              <button
                type="button"
                onClick={onClose}
                aria-label="Close"
                className="pointer-events-auto grid size-11 place-items-center rounded-full bg-black/40 active:bg-white/10"
              >
                <X className="size-6" />
              </button>
              <span className="pointer-events-none pt-3 pr-2 text-sm text-white/70 tabular-nums">
                {index + 1} / {photos.length}
              </span>
            </div>
            {/* Arrows for mouse users; a finger uses the swipe. */}
            <NavButton side="left" disabled={index === 0} onClick={prev} />
            <NavButton side="right" disabled={index === photos.length - 1} onClick={next} />
          </>
        )}
      </div>

      {chrome && (
        <div className="bg-zinc-950/95 pb-[max(0.5rem,env(safe-area-inset-bottom))]">
          {saveError && <p className="px-3 pt-2 text-center text-xs text-rose-400">{saveError}</p>}
          <div className="flex items-center gap-2 px-2 py-2">
            <RatingBar
              className="flex-1"
              rating={photo.rating}
              flag={photo.flag}
              onRate={(r) => onRate(photo.id, r)}
              onFlag={(f) => onFlag(photo.id, f)}
            />
            {canDownload && (
              <button
                type="button"
                onClick={() => void save()}
                disabled={saving}
                aria-label="Save photo"
                className="grid size-11 shrink-0 place-items-center rounded-full bg-white/10 active:bg-white/20 disabled:opacity-50"
              >
                {saving ? <Loader2 className="size-5 animate-spin" /> : <Download className="size-5" />}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function NavButton({
  side,
  disabled,
  onClick,
}: {
  side: 'left' | 'right';
  disabled: boolean;
  onClick: () => void;
}) {
  const Icon = side === 'left' ? ChevronLeft : ChevronRight;
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={side === 'left' ? 'Previous photo' : 'Next photo'}
      className={cn(
        'absolute top-1/2 hidden -translate-y-1/2 place-items-center rounded-full bg-black/40 p-2 sm:grid',
        side === 'left' ? 'left-2' : 'right-2',
        disabled && 'opacity-30',
      )}
    >
      <Icon className="size-7" />
    </button>
  );
}
