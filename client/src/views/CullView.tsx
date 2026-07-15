import { useEffect, useMemo, useState } from 'react';
import type { Photo } from '@/api/library';
import type { BurstInfo } from '@/lib/bursts';
import { useApiClient } from '@/api/client';
import { CinemaImage } from '@/views/LoupeView';
import { CinemaHUD } from '@/components/cinema/CinemaHUD';
import { ConfirmBar } from '@/components/cinema/ConfirmBar';
import { ScrubberDeck } from '@/components/cinema/ScrubberDeck';
import { ContactSheet } from '@/components/cinema/ContactSheet';
import { GapControl } from '@/components/cinema/GapControl';
import { ZoomCluster } from '@/components/cinema/ZoomCluster';
import { esLoad, esSetApplyIds, useEditSession } from '@/lib/editSession';
import { groupByGap, timeLabel } from '@/lib/timeGaps';
import { useIdle } from '@/lib/useIdle';
import { selectGapMinutes, useUIStore } from '@/stores/uiStore';

/**
 * Cull mode: the cinema confirm loupe with the time-gap scrubber deck. The
 * confirm bar (with its embedded zoom cluster) and the deck stay up whether
 * the photo sits at fit or 1:1.
 */
export function CullView({
  photos,
  bursts,
  softBelow,
}: {
  photos: Photo[];
  bursts: Map<number, BurstInfo>;
  // Soft-focus cutoff (whole-folder), so the scrubber's soft badge agrees with
  // the grid's.
  softBelow: number;
}) {
  const client = useApiClient();
  const focusId = useUIStore((s) => s.focusId);
  const contactSheet = useUIStore((s) => s.contactSheet);
  const gapMinutes = useUIStore(selectGapMinutes);
  const cropping = useEditSession((s) => s.cropping);
  const wbPicking = useEditSession((s) => s.wbPicking);
  const idle = useIdle();
  const [scale, setScale] = useState(1);

  const photo = photos.find((p) => p.id === focusId) ?? photos[0];
  const groups = useMemo(() => groupByGap(photos, gapMinutes), [photos, gapMinutes]);

  // The confirm bar's quick dials need an edit session even though the
  // Develop panel is not mounted in this mode. A session already open on
  // this photo (mode switch) is kept — reloading would reset overlay state.
  useEffect(() => {
    if (!photo) return;
    if (useEditSession.getState().photoId !== photo.id) void esLoad(client, photo.id, [photo.id]);
    else esSetApplyIds([photo.id]);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, photo?.id]);
  useEffect(() => {
    if (photo && focusId == null) useUIStore.getState().focus(photo.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [photo?.id, focusId]);

  if (!photo) {
    return (
      <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
        Nothing to cull — the current filter shows no photos.
      </div>
    );
  }

  if (contactSheet) {
    return <ContactSheet photos={photos} groups={groups} bursts={bursts} />;
  }

  const idx = photos.findIndex((p) => p.id === photo.id);
  const overlayActive = cropping || wbPicking;

  return (
    <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
      <CinemaImage
        photo={photo}
        photos={photos}
        onZoomInfo={setScale}
        renderingBadgeBottom={216}
        navigatorBottom={124}
      />
      {!overlayActive && (
        <>
          <CinemaHUD
            hidden={idle}
            status={
              <span className="font-mono text-[11px] text-[#aab0ff]">
                {photo.takenAt > 0 && `${timeLabel(photo.takenAt)} · `}frame {idx + 1}
              </span>
            }
            right={<GapControl glass />}
          />
          <ConfirmBar photo={photo} hidden={idle} zoom={<ZoomCluster scale={scale} />} />
          <ScrubberDeck groups={groups} focusId={photo.id} hidden={idle} softBelow={softBelow} />
        </>
      )}
    </div>
  );
}
