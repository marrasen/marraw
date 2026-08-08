// The pop-out photo window (Ctrl+N), from the driving window's side. The
// window itself is electron/main.cjs's; all this side can do is ask for it and
// know whether it is up.
import { useEffect, useState } from 'react';

import '@/lib/electron';

/** Whether the shell can pop a window out at all (never in a browser tab). */
export const viewerSupported = () => typeof window.win?.toggleViewer === 'function';

/** Opens the pop-out window, or closes the one that is up. */
export const toggleViewer = () => window.win?.toggleViewer?.();

/**
 * True in the pop-out window itself (main.tsx's ?view=viewer), which shares
 * this preload and so would otherwise offer to pop itself out.
 */
export const isViewerWindow = () =>
  new URLSearchParams(window.location.search).get('view') === 'viewer';

/**
 * Whether the pop-out window is open right now. Read once on mount (it may
 * have been opened before this window mounted, or by another window) and then
 * kept current by pushes from the shell — it can be closed from its own
 * Ctrl+N, which nothing here would otherwise see.
 */
export function useViewerOpen(): boolean {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    void window.win?.getViewerPhoto?.().then((s) => setOpen(s.open));
    return window.win?.onViewerOpen?.(setOpen);
  }, []);
  return open;
}
