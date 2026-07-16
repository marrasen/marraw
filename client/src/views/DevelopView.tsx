import { useMemo, useState } from 'react';
import type { Photo } from '@/api/library';
import { useApiClient } from '@/api/client';
import { burstFor, type BurstInfo } from '@/lib/bursts';
import { CinemaImage } from '@/views/LoupeView';
import { BurstBadge } from '@/components/BurstBadge';
import { CinemaHUD, PaletteChip } from '@/components/cinema/CinemaHUD';
import { GapControl } from '@/components/cinema/GapControl';
import { MiniCycle } from '@/components/cinema/MiniCycle';
import { MiniSlider } from '@/components/cinema/MiniSlider';
import { ScrubberDeck } from '@/components/cinema/ScrubberDeck';
import { SliderHUD } from '@/components/cinema/SliderHUD';
import { ZoomCluster } from '@/components/cinema/ZoomCluster';
import { EditPanel } from '@/components/EditPanel';
import { DIALS } from '@/lib/dials';
import { esCommit, esUpdate, useEditSession } from '@/lib/editSession';
import { groupByGap } from '@/lib/timeGaps';
import { useHoverKeep, useIdle } from '@/lib/useIdle';
import { cn } from '@/lib/utils';
import { selectGapMinutes, useUIStore } from '@/stores/uiStore';

/**
 * Develop mode: the maximal darkroom canvas. The photo fills the window;
 * the full develop stack lives in a pinnable right drawer, the user-picked
 * quick dials float as a dock with the always-present zoom cluster (none by
 * default — Settings → Toolbars), and the same time-gap camera roll as Cull
 * keeps the take in reach.
 */
export function DevelopView({
  photos,
  all,
  bursts,
}: {
  photos: Photo[];
  all: Photo[];
  bursts: Map<number, BurstInfo>;
}) {
  const focusId = useUIStore((s) => s.focusId);
  const gapMinutes = useUIStore(selectGapMinutes);
  const cropping = useEditSession((s) => s.cropping);
  const wbPicking = useEditSession((s) => s.wbPicking);
  const keyAdjust = useEditSession((s) => s.keyAdjust);
  const activeControl = useEditSession((s) => s.activeControl);
  const idle = useIdle();
  const [scale, setScale] = useState(1);
  const [panelHovered, setPanelHovered] = useState(false);

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
  const burst = burstFor(photo, bursts);
  // Nudging a focused control with +/- floats a compact bottom readout and
  // hides the drawer + chrome (which the idle timer also does). Guarded on an
  // active control so a stale keyAdjust can never hide the UI with nothing to
  // show.
  const adjusting = keyAdjust && activeControl != null;
  // A mouse resting still on the drawer emits no pointermove, so the idle timer
  // would fade the panel out from under the cursor. Suppress the idle fade
  // while the pointer is over the drawer (a +/- adjust still hides it).
  const chromeHidden = (idle && !panelHovered) || adjusting;

  return (
    <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
      <CinemaImage
        photo={photo}
        photos={photos}
        onZoomInfo={setScale}
        renderingBadgeBottom={216}
        showNavigator={false}
      />
      {!overlayActive && (
        <CinemaHUD
          hidden={chromeHidden}
          status={
            <span className="font-mono text-[11px] text-[#aab0ff]">
              {photo.fileName.split(/[\\/]/).pop()}
              {burst && (
                <BurstBadge
                  burst={burst}
                  photoId={photo.id}
                  className="ml-2 inline-flex align-middle"
                />
              )}
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
      {/* Full-stack drawer (Develop / Presets / Info tabs live inside
          EditPanel). Stays MOUNTED through crop/eyedropper overlays (it owns
          the edit-session lifecycle) and through the idle fade — only slid out
          of the way or faded, never unmounted. */}
      <div
        onPointerEnter={() => setPanelHovered(true)}
        onPointerLeave={() => setPanelHovered(false)}
        className={cn(
          'glass absolute top-16 right-4 bottom-4 z-30 flex w-[352px] flex-col overflow-hidden rounded-[13px] transition-[transform,opacity] duration-200',
          overlayActive && 'translate-x-[calc(100%+32px)]',
          // Fade out with the rest of the chrome when idle, and while a +/-
          // adjust floats the compact bottom readout instead.
          chromeHidden && 'pointer-events-none opacity-0',
        )}
      >
        <div className="min-h-0 flex-1">
          <EditPanel photos={all} />
        </div>
      </div>
      {!overlayActive && (
        <>
          <QuickDock hidden={chromeHidden} shifted zoom={<ZoomCluster scale={scale} />} />
          <ScrubberDeck groups={groups} focusId={photo.id} hidden={chromeHidden} shifted bursts={bursts} />
          {adjusting && <SliderHUD />}
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
  // The dock never fades while the pointer rests on it (useHoverKeep); the
  // handlers live on the glass pill — the outer wrapper spans the canvas and
  // is pointer-events-none.
  const { hovered, bind } = useHoverKeep();
  const conceal = hidden && !hovered;

  return (
    <div
      className={cn(
        'pointer-events-none absolute bottom-[126px] left-4 z-30 flex justify-center transition-opacity duration-300',
        shifted ? 'right-[384px]' : 'right-4',
        conceal && 'opacity-0',
      )}
    >
    <div
      {...bind}
      className={cn(
        'glass flex max-w-full items-center gap-3.5 overflow-x-auto rounded-[14px] px-[18px] py-3',
        !conceal && 'pointer-events-auto',
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
          {zoom && <div className="h-9 w-px bg-white/15" />}
        </>
      )}
      {zoom}
    </div>
    </div>
  );
}
