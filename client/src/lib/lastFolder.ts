// The folder that was open when the app last closed, reopened on the next
// launch. Server-persisted (ui:lastFolder) like every other preference, so
// the memory belongs to the library, not to the machine looking at it.
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { openFolder } from '@/api/library';
import { useApiClient, type ApiClient } from '@/api/client';
import { baseName } from '@/lib/library';
import { updateLastFolder, updateRailHidden } from '@/lib/uiSettings';
import { useUIStore } from '@/stores/uiStore';

// With no folder open there is no filter toolbar, and the rail's show/hide
// toggle lives in that toolbar — so a collapsed rail leaves nothing on screen
// to open a library with. Drop the collapse for real (not as a render-time
// override) so opening a folder next doesn't yank the rail away again.
function ensureRailReachable(client: ApiClient) {
  if (useUIStore.getState().railHidden) updateRailHidden(client, false);
}

/**
 * Reopens the remembered folder once settings arrive, then keeps the memory
 * current as the open folder changes.
 *
 * The order matters more than either half: while a folder is still opening
 * the store says "no folder open", and writing THAT back would erase the very
 * folder being restored. So the effect walks
 * `wait -> opening -> live` and only starts mirroring in `live`.
 */
export function useLastFolder() {
  const client = useApiClient();
  const settingsLoaded = useUIStore((s) => s.settingsLoaded);
  const folderPath = useUIStore((s) => s.folderPath);
  const phase = useRef<'wait' | 'opening' | 'live'>('wait');
  // A restore that failed keeps its memory on purpose — plug the drive back
  // in and the next launch lands there again — so the no-folder write below
  // must not clear it. Dropped as soon as some folder does open.
  const keepMemory = useRef(false);
  // Re-runs this effect when a restore settles without moving the store.
  const [settled, setSettled] = useState(0);

  useEffect(() => {
    if (!settingsLoaded) return;

    if (phase.current === 'wait') {
      phase.current = 'opening';
      const { lastFolder, folderId } = useUIStore.getState();
      // ?openFolder= (the UI smoke test, and handy as a deep link) is already
      // opening something and wins. Settings can arrive either side of that
      // open, so this falls through to the check below rather than returning:
      // the folder may be open already, or still on its way.
      const requested = new URLSearchParams(window.location.search).get('openFolder');
      if (!requested && folderId == null) {
        if (lastFolder) {
          openFolder(client, lastFolder)
            .then((info) => useUIStore.getState().setFolder(info.folderId, lastFolder))
            .catch((err: unknown) => {
              // Deleted, renamed, or its drive unplugged. This launch lands on
              // the library instead, and says why.
              keepMemory.current = true;
              setSettled((n) => n + 1);
              toast.error(`Could not reopen ${baseName(lastFolder)}`, {
                description: (err as Error).message,
              });
            });
          return;
        }
        phase.current = 'live'; // nothing to restore
      }
    }

    if (phase.current === 'opening') {
      if (folderPath == null && !keepMemory.current) return; // still opening
      phase.current = 'live';
    }

    const path = folderPath ?? '';
    if (path) keepMemory.current = false;
    // Nothing open — this is where someone picks a library, so the rail has
    // to be reachable. Covers both landing here at startup and closing the
    // open folder (removing or hiding it in the rail).
    else ensureRailReachable(client);
    if (!path && keepMemory.current) return;
    if (path !== useUIStore.getState().lastFolder) updateLastFolder(client, path);
  }, [settingsLoaded, folderPath, settled, client]);
}
