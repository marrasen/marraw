import { useEffect, useMemo, useState } from 'react';

import { WindowControls } from '@/components/WindowControls';
import { useHoverKeep, useIdle } from '@/lib/useIdle';
import { cn } from '@/lib/utils';
import { useListPhotos, type Photo } from '@/api/library';
import { photoPatchReducer } from '@/lib/usePhotos';
import { useUIStore } from '@/stores/uiStore';
import { CinemaImage } from '@/views/LoupeView';
import type { ViewerPhoto } from '@/lib/electron';

// ViewerSurface shows one photo from a folder it subscribes to itself.
//
// The RAW list, not usePhotos: this window never loads the server's UI
// settings, so its filters and sort would silently disagree with the window
// driving it — and the viewer only ever needs to find one photo by id.
// Subscription patches still land, so saving an edit bumps editHash and the
// pixels here refresh with it.
function ViewerSurface({ folderId, photoId }: ViewerPhoto) {
  const { data } = useListPhotos(folderId, { applyPatch: photoPatchReducer });
  const photos = useMemo(() => data ?? [], [data]);
  const found = photos.find((p) => p.id === photoId) ?? null;
  // Hold the last photo that resolved: a delete, or the list of a folder that
  // was just opened still loading, should leave the previous frame up rather
  // than blank the window. Adjusted during render (React's documented pattern)
  // instead of in an effect — the fallback has to be current in THIS paint, or
  // the window flashes empty for a frame on the way to it.
  const [lastGood, setLastGood] = useState<Photo | null>(null);
  if (found && found !== lastGood) setLastGood(found);
  const photo = found ?? lastGood;
  if (!photo) return null;
  // No navigator inset and a low badge: nothing may sit over the photo, and
  // there is no control bar here for the rendering badge to clear.
  return <CinemaImage photo={photo} photos={photos} showNavigator={false} renderingBadgeBottom={24} />;
}

/**
 * The pop-out photo window (Ctrl+N): a second, chromeless, always-on-top
 * window showing whatever photo the main window has focused — with its own
 * zoom and pan, kept across photo switches.
 *
 * Both come free from being a separate Electron window: the zustand store and
 * LoupeView's module-scoped pan ratio are per-realm, and that pan ratio
 * already survives photo switches by design. Mounting CinemaImage bare is
 * likewise enough to have no UI on top — every piece of chrome (HUD, decks,
 * drawers) is rendered by the cinema *hosts*, not by the engine. Nothing here
 * opens an edit session either, so the develop overlays stay off: they all
 * gate on mode === 'develop', and this window's store never leaves 'library'.
 */
export default function ViewerApp() {
  const [target, setTarget] = useState<ViewerPhoto | null>(null);
  // Always-on-top belongs to the shell, which opened this window floating or
  // not from its own prefs — so read the answer from there rather than assume
  // it, and write changes straight back for the next window.
  const [alwaysOnTop, setAlwaysOnTopState] = useState(true);
  useEffect(() => {
    void window.win?.getViewerAlwaysOnTop?.().then(setAlwaysOnTopState);
  }, []);
  const setAlwaysOnTop = (on: boolean) => {
    setAlwaysOnTopState(on);
    window.win?.setViewerAlwaysOnTop?.(on);
  };

  useEffect(() => {
    let pushed = false;
    const unsub = window.win?.onViewerPhoto?.((s) => {
      pushed = true;
      setTarget(s);
    });
    // Pull as well as subscribe: the focus that opened this window happened
    // while its page was still loading, and only the main process kept it. A
    // push that lands while this is in flight is newer, so it wins.
    void window.win?.getViewerPhoto?.().then((s) => {
      if (!pushed && s.folderId != null && s.photoId != null)
        setTarget({ folderId: s.folderId, photoId: s.photoId });
    });
    return unsub;
  }, []);

  // Trackpad pinch arrives as ctrl+wheel; CinemaImage consumes it for image
  // zoom, but anywhere else it would trigger Chromium's page zoom. React
  // registers wheel listeners as passive, so only a native non-passive one can
  // preventDefault. (Mirrors the same guard in App.tsx.)
  useEffect(() => {
    const block = (e: WheelEvent) => {
      if (e.ctrlKey) e.preventDefault();
    };
    window.addEventListener('wheel', block, { passive: false, capture: true });
    return () =>
      window.removeEventListener('wheel', block, { capture: true } as EventListenerOptions);
  }, []);

  // Its own small keymap rather than useKeyboard: that one drives modes,
  // ratings and an edit session this window has none of. Ctrl+N in particular
  // has to be handled here — a renderer keydown only reaches the FOCUSED
  // window, so without this the viewer could not be closed while you are
  // looking at it.
  useEffect(() => {
    let fullscreen = false;
    const offFullScreen = window.win?.onFullScreenChange((fs) => {
      fullscreen = fs;
    });
    // Stepping from 'fit' starts at the actual fit scale (mirrored out by the
    // loupe), so + walks out of fit instead of jumping to 1:1 — as in the main
    // window's keymap.
    const zoomStep = (factor: number) => {
      const s = useUIStore.getState();
      s.setLoupeZoom((s.loupeZoom === 'fit' ? s.loupeFitScale : s.loupeZoom) * factor);
    };
    const toggleFit = () => {
      const s = useUIStore.getState();
      s.setLoupeZoom(s.loupeZoom === 'fit' ? 1 : 'fit');
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey) {
        if (e.key.toLowerCase() === 'n') {
          e.preventDefault();
          window.win?.close();
        }
        return;
      }
      switch (e.key) {
        case 'F11':
          e.preventDefault();
          window.win?.toggleFullScreen();
          break;
        case 'Escape':
          // Esc only leaves fullscreen. Closing the window is Ctrl+N's job —
          // the same key that opened it.
          if (fullscreen) window.win?.toggleFullScreen();
          break;
        case '+':
        case '=':
          zoomStep(1.25);
          break;
        case '-':
        case '_':
          zoomStep(0.8);
          break;
        case 'z':
        case 'Z':
        case ' ':
          e.preventDefault(); // space must not scroll the pannable surface
          toggleFit();
          break;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
      offFullScreen?.();
    };
  }, []);

  return (
    <div className="relative flex h-screen flex-col bg-background text-foreground">
      {target ? <ViewerSurface folderId={target.folderId} photoId={target.photoId} /> : null}
      <ViewerHUD pinned={alwaysOnTop} onTogglePin={setAlwaysOnTop} />
    </div>
  );
}

/**
 * The cinema HUD's top band, reduced to its right island: the window controls
 * as a glass pill, led by the pin that decides whether this window floats over
 * other apps.
 *
 * It is the whole reason the window reads as a window rather than a picture
 * stuck to the screen — and being chrome, it stays out of the way: shown on
 * any pointer move, faded after a few idle seconds, and never faded out from
 * under a cursor that is resting on it. The band behind it is the move handle,
 * exactly as in Cull and Develop.
 */
function ViewerHUD({ pinned, onTogglePin }: { pinned: boolean; onTogglePin: (next: boolean) => void }) {
  const idle = useIdle();
  const { hovered, bind } = useHoverKeep();
  const conceal = idle && !hovered;
  return (
    <div className="pointer-events-none absolute inset-x-0 top-0 z-30">
      {/* Without a drag handle a frameless window cannot be moved to the
          monitor it exists for. Double-click maximizes, which Chromium already
          does for app-region drags on Windows and macOS but not on Linux. */}
      <div
        className="pointer-events-auto absolute inset-x-0 top-0 h-12 [-webkit-app-region:drag]"
        onDoubleClick={maximizeOnDoubleClick}
      />
      <div
        {...bind}
        className={cn(
          'absolute top-4 right-[18px] transition-opacity duration-300 [-webkit-app-region:no-drag]',
          conceal ? 'opacity-0' : 'pointer-events-auto',
        )}
      >
        <WindowControls variant="glass" pinned={pinned} onTogglePin={onTogglePin} />
      </div>
    </div>
  );
}

// Chromium maximizes on a double-click inside an app-region drag on Windows
// and macOS by itself; doing it here as well would toggle twice and land back
// where it started. Linux gets no such handling, so it needs ours.
const maximizeOnDoubleClick =
  typeof navigator !== 'undefined' && navigator.userAgent.includes('Linux')
    ? () => window.win?.toggleMax()
    : undefined;
