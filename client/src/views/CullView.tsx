import { useEffect, useMemo, useState } from 'react';
import type { Photo } from '@/api/library';
import { useApiClient } from '@/api/client';
import { CinemaImage } from '@/views/LoupeView';
import { CinemaHUD } from '@/components/cinema/CinemaHUD';
import { ConfirmBar } from '@/components/cinema/ConfirmBar';
import { ScrubberDeck } from '@/components/cinema/ScrubberDeck';
import { ContactSheet } from '@/components/cinema/ContactSheet';
import { GapControl } from '@/components/cinema/GapControl';
import { ZoomCluster } from '@/components/cinema/ZoomCluster';
import { esLoad, useEditSession } from '@/lib/editSession';
import { groupByGap, timeLabel } from '@/lib/timeGaps';
import { useIdle } from '@/lib/useIdle';
import { useUIStore } from '@/stores/uiStore';

/**
 * Cull mode: the cinema confirm loupe with the time-gap scrubber deck. The
 * confirm bar (with its embedded zoom cluster) and the deck stay up whether
 * the photo sits at fit or 1:1.
 */
export function CullView({ photos }: { photos: Photo[] }) {
  const client = useApiClient();
  const focusId = useUIStore((s) => s.focusId);
  const contactSheet = useUIStore((s) => s.contactSheet);
  const gapMinutes = useUIStore((s) => s.gapMinutes);
  const cropping = useEditSession((s) => s.cropping);
  const wbPicking = useEditSession((s) => s.wbPicking);
  const idle = useIdle();
  const [zoomInfo, setZoomInfo] = useState<{ scale: number; rendering: boolean }>({
    scale: 1,
    rendering: false,
  });

  const photo = photos.find((p) => p.id === focusId) ?? photos[0];
  const groups = useMemo(() => groupByGap(photos, gapMinutes), [photos, gapMinutes]);

  // The confirm bar's quick dials need an edit session even though the
  // Develop panel is not mounted in this mode.
  useEffect(() => {
    if (photo) void esLoad(client, photo.id, [photo.id]);
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
    return <ContactSheet photos={photos} groups={groups} />;
  }

  const idx = photos.findIndex((p) => p.id === photo.id);
  const overlayActive = cropping || wbPicking;

  return (
    <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
      <CinemaImage
        photo={photo}
        photos={photos}
        onZoomInfo={(scale, rendering) => setZoomInfo({ scale, rendering })}
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
          <ConfirmBar
            photo={photo}
            hidden={idle}
            zoom={<ZoomCluster scale={zoomInfo.scale} rendering={zoomInfo.rendering} />}
          />
          <ScrubberDeck groups={groups} focusId={photo.id} hidden={idle} />
        </>
      )}
    </div>
  );
}
