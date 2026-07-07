import type { Photo } from '@/api/library';
import { useApiClient } from '@/api/client';
import { cn } from '@/lib/utils';
import { applyFlag, applyRating } from '@/lib/actions';
import { esCommit, esUpdate, useEditSession } from '@/lib/editSession';
import { MiniSlider } from '@/components/cinema/MiniSlider';

const pct = (v: number) => (v === 0 ? '0' : `${v > 0 ? '+' : ''}${Math.round(v * 100)}`);
const ev = (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(2)}`;

/**
 * The Cull confirm bar (bottom-center glass): filename + star row, Pick /
 * Reject, and the three quick-triage dials (Exposure / Contrast / Temp).
 */
export function ConfirmBar({ photo, hidden }: { photo: Photo; hidden?: boolean }) {
  const client = useApiClient();
  const draft = useEditSession((s) => s.draft);
  const onDraft = useEditSession((s) => s.photoId) === photo.id && draft != null;
  const displayName = photo.fileName.split(/[\\/]/).pop() ?? photo.fileName;

  return (
    <div
      className={cn(
        'glass absolute bottom-[126px] left-1/2 z-30 flex -translate-x-1/2 items-center gap-4 rounded-[14px] px-[18px] py-3 transition-opacity duration-300',
        hidden && 'pointer-events-none opacity-0',
      )}
    >
      <div className="flex flex-col gap-[5px]">
        <span className="font-mono text-[11.5px]">{displayName}</span>
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
      <div className="h-9 w-px bg-white/15" />
      {onDraft && draft ? (
        <>
          <MiniSlider
            label="Exposure"
            value={draft.expEV}
            display={ev(draft.expEV)}
            min={-2}
            max={3}
            step={0.05}
            neutral={0}
            onChange={(v) => esUpdate(client, { expEV: v })}
            onCommit={() => esCommit(client)}
          />
          <MiniSlider
            label="Contrast"
            value={draft.contrast}
            display={pct(draft.contrast)}
            min={-1}
            max={1}
            step={0.02}
            neutral={0}
            onChange={(v) => esUpdate(client, { contrast: v })}
            onCommit={() => esCommit(client)}
          />
          <MiniSlider
            label="Temp"
            value={draft.wbTemp}
            display={pct(draft.wbTemp)}
            min={-1}
            max={1}
            step={0.02}
            neutral={0}
            onChange={(v) => esUpdate(client, { wbTemp: v })}
            onCommit={() => esCommit(client)}
          />
        </>
      ) : (
        <span className="w-[270px] text-[10.5px] text-muted-foreground">Loading edits…</span>
      )}
    </div>
  );
}
