import { useMemo, useState } from 'react';
import { Pin, PinOff } from 'lucide-react';
import type { Photo } from '@/api/library';
import { useApiClient } from '@/api/client';
import { CinemaImage } from '@/views/LoupeView';
import { CinemaHUD, PaletteChip } from '@/components/cinema/CinemaHUD';
import { GapControl } from '@/components/cinema/GapControl';
import { MiniSlider } from '@/components/cinema/MiniSlider';
import { ScrubberDeck } from '@/components/cinema/ScrubberDeck';
import { ZoomCluster } from '@/components/cinema/ZoomCluster';
import { EditPanel } from '@/components/EditPanel';
import { DIALS, dialValue } from '@/lib/dials';
import { esCommit, esUpdate, useEditSession } from '@/lib/editSession';
import { groupByGap } from '@/lib/timeGaps';
import { useIdle } from '@/lib/useIdle';
import { cn } from '@/lib/utils';
import { useUIStore } from '@/stores/uiStore';

/**
 * Develop mode: the maximal darkroom canvas. The photo fills the window;
 * the full develop stack lives in a pinnable right drawer, the user-picked
 * quick dials float as a dock with the always-present zoom cluster (none by
 * default — Settings → Toolbars), and the same time-gap camera roll as Cull
 * keeps the take in reach.
 */
export function DevelopView({ photos, all }: { photos: Photo[]; all: Photo[] }) {
  const focusId = useUIStore((s) => s.focusId);
  const gapMinutes = useUIStore((s) => s.gapMinutes);
  const cropping = useEditSession((s) => s.cropping);
  const wbPicking = useEditSession((s) => s.wbPicking);
  const [pinned, setPinned] = useState(() => localStorage.getItem('marraw:developPinned') !== '0');
  const idle = useIdle();
  const [scale, setScale] = useState(1);

  const groups = useMemo(() => groupByGap(photos, gapMinutes), [photos, gapMinutes]);
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
      <CinemaImage
        photo={photo}
        photos={photos}
        onZoomInfo={setScale}
        renderingBadgeBottom={216}
        navigatorBottom={pinned ? 18 : 124}
      />
      {!overlayActive && (
        <CinemaHUD
          hidden={idle && !pinned}
          status={
            <span className="font-mono text-[11px] text-[#aab0ff]">
              {photo.fileName.split(/[\\/]/).pop()}
            </span>
          }
          right={
            <div className="flex items-center gap-3">
              <GapControl glass />
              <PaletteChip />
            </div>
          }
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
          <QuickDock hidden={idle} shifted={pinned} zoom={<ZoomCluster scale={scale} />} />
          <ScrubberDeck groups={groups} focusId={photo.id} hidden={idle} shifted={pinned} />
        </>
      )}
    </div>
  );
}

// QuickDock: the user-picked dials floating over the canvas, plus the
// embedded zoom cluster. With no dials picked (the default) it collapses to
// just the zoom cluster. Stays rendered while the next photo's edit session
// loads — the last draft keeps the dials in place (input disabled) so
// arrowing through a take doesn't blink the dock.
function QuickDock({
  hidden,
  shifted,
  zoom,
}: {
  hidden?: boolean;
  shifted?: boolean;
  zoom?: React.ReactNode;
}) {
  const client = useApiClient();
  const dials = useUIStore((s) => s.quickDials);
  const draft = useEditSession((s) => s.draft);
  const shown = useEditSession((s) => s.draft ?? s.lastDraft);

  return (
    <div
      className={cn(
        'pointer-events-none absolute bottom-[100px] left-4 z-30 flex justify-center transition-opacity duration-300',
        shifted ? 'right-[384px]' : 'right-4',
        hidden && 'opacity-0',
      )}
    >
    <div
      className={cn(
        'glass flex max-w-full items-center gap-3.5 overflow-x-auto rounded-[14px] px-[18px] py-3',
        !hidden && 'pointer-events-auto',
      )}
    >
      {dials.length > 0 && (
        <>
          <span
            className="text-[10px] tracking-[.06em] text-muted-foreground uppercase"
            style={{ writingMode: 'vertical-rl', transform: 'rotate(180deg)' }}
          >
            Quick
          </span>
          <div className={cn('flex items-center gap-3.5', !draft && 'pointer-events-none')}>
            {DIALS.filter((d) => dials.includes(d.key)).map((d) => (
              <MiniSlider
                key={d.key}
                label={d.label}
                value={dialValue(shown, d.key)}
                display={d.display(dialValue(shown, d.key))}
                min={d.min}
                max={d.max}
                step={d.step}
                neutral={0}
                onChange={(v) => esUpdate(client, { [d.key]: v })}
                onCommit={() => esCommit(client)}
              />
            ))}
          </div>
          {zoom && <div className="h-9 w-px bg-white/15" />}
        </>
      )}
      {zoom}
    </div>
    </div>
  );
}
