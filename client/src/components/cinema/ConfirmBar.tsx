import { useState } from 'react';
import type { Photo } from '@/api/library';
import type { Params } from '@/api/edit';
import { useApiClient } from '@/api/client';
import { cn } from '@/lib/utils';
import { applyFlag, applyRating } from '@/lib/actions';
import { DIALS } from '@/lib/dials';
import { esCommit, esUpdate, useEditSession } from '@/lib/editSession';
import { MiniCycle } from '@/components/cinema/MiniCycle';
import { MiniSlider } from '@/components/cinema/MiniSlider';
import { useUIStore } from '@/stores/uiStore';

/**
 * The Cull confirm bar (bottom-center glass): filename + star row, Pick /
 * Reject, the user-picked quick dials (Settings → Toolbars; none by default,
 * which keeps the bar compact), and the always-present zoom cluster.
 */
export function ConfirmBar({
  photo,
  hidden,
  zoom,
}: {
  photo: Photo;
  hidden?: boolean;
  zoom?: React.ReactNode;
}) {
  const client = useApiClient();
  const dials = useUIStore((s) => s.cullDials);
  const draft = useEditSession((s) => s.draft);
  const onDraft = useEditSession((s) => s.photoId) === photo.id && draft != null;
  const displayName = photo.fileName.split(/[\\/]/).pop() ?? photo.fileName;

  // While the next photo's edit session loads, keep showing the last draft
  // (input disabled) instead of swapping the dials for a placeholder — the
  // load takes a frame or two and the swap read as flicker while arrowing
  // through a take.
  // Adjust-during-render (not an effect): React re-renders synchronously
  // before paint, so the held draft updates with no flicker frame.
  const [held, setHeld] = useState<Params | null>(null);
  if (onDraft && draft && draft !== held) setHeld(draft);
  const shown = onDraft && draft ? draft : held;

  return (
    <div
      className={cn(
        'glass absolute bottom-[126px] left-1/2 z-30 flex -translate-x-1/2 items-center gap-4 rounded-[14px] px-[18px] py-3 transition-opacity duration-300',
        hidden && 'pointer-events-none opacity-0',
      )}
    >
      <div className="flex flex-col gap-[5px]">
        {/* Fixed width: filename length varies shot to shot, and a centered
            bar re-centers on every change of its natural width. */}
        <span className="w-[112px] truncate font-mono text-[11.5px]" title={displayName}>
          {displayName}
        </span>
        <div className="flex text-[15px] leading-none" role="group" aria-label="Rating">
          {[1, 2, 3, 4, 5].map((n) => (
            <button
              key={n}
              className="pr-0.5"
              aria-label={`${n} stars`}
              onClick={() => applyRating(client, [photo.id], photo.rating === n ? 0 : n)}
            >
              <span className={n <= photo.rating ? 'text-rating' : 'text-white/30'}>★</span>
            </button>
          ))}
        </div>
      </div>
      <div className="h-9 w-px bg-white/15" />
      <div className="flex gap-[7px]">
        <button
          className={cn(
            'flex h-[34px] items-center gap-1.5 rounded-lg border px-4 text-[12.5px]',
            photo.flag === 'pick'
              ? 'border-success bg-success/25 text-success-text'
              : 'border-success/45 bg-success/15 text-success-text hover:bg-success/25',
          )}
          onClick={() => applyFlag(client, [photo.id], photo.flag === 'pick' ? 'none' : 'pick')}
        >
          Pick <span className="font-mono text-[10px] opacity-80">P</span>
        </button>
        <button
          className={cn(
            'flex h-[34px] items-center gap-1.5 rounded-lg border px-4 text-[12.5px]',
            photo.flag === 'exclude'
              ? 'border-destructive bg-destructive/25 text-danger-text'
              : 'border-destructive/40 bg-destructive/10 text-danger-text hover:bg-destructive/20',
          )}
          onClick={() => applyFlag(client, [photo.id], photo.flag === 'exclude' ? 'none' : 'exclude')}
        >
          Reject <span className="font-mono text-[10px] opacity-80">X</span>
        </button>
      </div>
      {dials.length > 0 && (
        <>
          <div className="h-9 w-px bg-white/15" />
          <div className={cn('flex items-center gap-4', !onDraft && 'pointer-events-none')}>
            {DIALS.filter((d) => dials.includes(d.key)).map((d) =>
              d.kind === 'numeric' ? (
                <MiniSlider
                  key={d.key}
                  label={d.label}
                  value={d.value(shown)}
                  display={d.display(d.value(shown))}
                  min={d.min}
                  max={d.max}
                  step={d.step}
                  neutral={d.neutral}
                  onChange={(v) => esUpdate(client, d.patch(v))}
                  onCommit={() => esCommit(client)}
                />
              ) : (
                <MiniCycle
                  key={d.key}
                  label={d.label}
                  value={d.value(shown)}
                  values={d.values}
                  valueLabel={d.valueLabel}
                  onChange={(v) => {
                    const patch = d.patch(v);
                    esUpdate(client, patch);
                    esCommit(client, patch);
                  }}
                />
              ),
            )}
          </div>
        </>
      )}
      {zoom && (
        <>
          <div className="h-9 w-px bg-white/15" />
          {zoom}
        </>
      )}
    </div>
  );
}
