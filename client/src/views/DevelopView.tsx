import { useState } from 'react';
import { Pin, PinOff } from 'lucide-react';
import type { Photo } from '@/api/library';
import { useApiClient } from '@/api/client';
import { CinemaImage, Filmstrip } from '@/views/LoupeView';
import { CinemaHUD, PaletteChip } from '@/components/cinema/CinemaHUD';
import { MiniSlider } from '@/components/cinema/MiniSlider';
import { EditPanel } from '@/components/EditPanel';
import { esCommit, esUpdate, useEditSession } from '@/lib/editSession';
import { useIdle } from '@/lib/useIdle';
import { cn } from '@/lib/utils';
import { useUIStore } from '@/stores/uiStore';

const pct = (v: number) => (v === 0 ? '0' : `${v > 0 ? '+' : ''}${Math.round(v * 100)}`);
const ev = (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(2)}`;

/**
 * Develop mode: the maximal darkroom canvas. The photo fills the window;
 * the full develop stack lives in a pinnable right drawer, the six
 * most-touched dials float as a quick dock, and a glass filmstrip keeps the
 * take in reach.
 */
export function DevelopView({ photos, all }: { photos: Photo[]; all: Photo[] }) {
  const focusId = useUIStore((s) => s.focusId);
  const cropping = useEditSession((s) => s.cropping);
  const wbPicking = useEditSession((s) => s.wbPicking);
  const [pinned, setPinned] = useState(() => localStorage.getItem('marraw:developPinned') !== '0');
  const idle = useIdle();

  const photo = photos.find((p) => p.id === focusId) ?? photos[0];
  if (!photo) {
    return (
      <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
        Nothing to develop — the current filter shows no photos.
      </div>
    );
  }

  const overlayActive = cropping || wbPicking;
  const togglePin = () => {
    setPinned((v) => {
      localStorage.setItem('marraw:developPinned', v ? '0' : '1');
      return !v;
    });
  };

  return (
    <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
      <CinemaImage photo={photo} photos={photos} hideChrome={overlayActive} />
      {!overlayActive && (
        <CinemaHUD
          hidden={idle && !pinned}
          status={
            <span className="font-mono text-[11px] text-[#aab0ff]">
              {photo.fileName.split(/[\\/]/).pop()}
            </span>
          }
          right={<PaletteChip />}
        />
      )}
      {/* Pinnable full-stack drawer. Stays MOUNTED through crop/eyedropper
          overlays (it owns the edit-session lifecycle) — only hidden. */}
      <div
        className={cn(
          'glass absolute top-16 right-4 bottom-4 z-30 flex w-[352px] flex-col overflow-hidden rounded-[13px] transition-transform duration-200',
          (!pinned || overlayActive) && 'translate-x-[calc(100%+32px)]',
        )}
      >
            <div className="flex items-center justify-between px-4 pt-[13px] pb-1">
              <span className="text-[10px] tracking-[.07em] text-muted-foreground uppercase">
                Develop · full stack
              </span>
              <button
                className="flex items-center gap-1 text-[11px] text-[#aab0ff] hover:text-foreground"
                onClick={togglePin}
                title={pinned ? 'Unpin drawer' : 'Pin drawer'}
              >
                <Pin className="size-3" strokeWidth={1.5} />
                Pinned
              </button>
            </div>
        <div className="min-h-0 flex-1">
          <EditPanel photos={all} />
        </div>
      </div>
      {!overlayActive && (
        <>
          {!pinned && (
            <button
              className="glass absolute top-16 right-4 z-30 flex items-center gap-1.5 rounded-[9px] px-3 py-2 text-xs text-muted-foreground hover:text-foreground"
              onClick={togglePin}
              title="Pin the develop drawer"
            >
              <PinOff className="size-3.5" strokeWidth={1.5} />
              Develop
            </button>
          )}
          <QuickDock hidden={idle} shifted={pinned} />
          <div
            className={cn(
              'absolute bottom-4 z-30 flex justify-center transition-opacity duration-300',
              pinned ? 'left-[calc(50%-192px)] -translate-x-1/2' : 'inset-x-0',
              idle && 'pointer-events-none opacity-0',
            )}
          >
            <div className="glass max-w-[720px] overflow-hidden rounded-[11px] p-1">
              <Filmstrip photos={photos} currentId={photo.id} />
            </div>
          </div>
        </>
      )}
    </div>
  );
}

// QuickDock: the six most-touched dials floating over the canvas.
function QuickDock({ hidden, shifted }: { hidden?: boolean; shifted?: boolean }) {
  const client = useApiClient();
  const draft = useEditSession((s) => s.draft);
  if (!draft) return null;

  const dial = (
    label: string,
    field: 'expEV' | 'contrast' | 'toneHighlights' | 'toneShadows' | 'wbTemp' | 'vibrance',
    opts: { min: number; max: number; step: number; display: (v: number) => string },
  ) => (
    <MiniSlider
      key={field}
      label={label}
      value={draft[field]}
      display={opts.display(draft[field])}
      min={opts.min}
      max={opts.max}
      step={opts.step}
      neutral={0}
      onChange={(v) => esUpdate(client, { [field]: v })}
      onCommit={() => esCommit(client)}
    />
  );

  return (
    <div
      className={cn(
        'glass absolute bottom-[100px] z-30 flex items-center gap-3.5 rounded-[14px] px-[18px] py-3 transition-opacity duration-300',
        shifted ? 'left-[calc(50%-192px)] -translate-x-1/2' : 'left-1/2 -translate-x-1/2',
        hidden && 'pointer-events-none opacity-0',
      )}
    >
      <span
        className="text-[10px] tracking-[.06em] text-muted-foreground uppercase"
        style={{ writingMode: 'vertical-rl', transform: 'rotate(180deg)' }}
      >
        Quick
      </span>
      {dial('Exposure', 'expEV', { min: -2, max: 3, step: 0.05, display: ev })}
      {dial('Contrast', 'contrast', { min: -1, max: 1, step: 0.02, display: pct })}
      {dial('Highlights', 'toneHighlights', { min: -1, max: 1, step: 0.02, display: pct })}
      {dial('Shadows', 'toneShadows', { min: -1, max: 1, step: 0.02, display: pct })}
      {dial('Temp', 'wbTemp', { min: -1, max: 1, step: 0.02, display: pct })}
      {dial('Vibrance', 'vibrance', { min: -1, max: 1, step: 0.02, display: pct })}
    </div>
  );
}
