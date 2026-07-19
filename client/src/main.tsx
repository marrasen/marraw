import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';
import App from './App.tsx';
import { ThemeProvider } from '@/components/theme-provider.tsx';
import { ApiClient, ApiClientProvider } from '@/api/client';
import { onAIMapsGeneratedEvent } from '@/api/edits';
import { backend } from '@/lib/backend';
import { bumpImgBust } from '@/lib/imgCacheBust';
import { migrateLocalSettings } from '@/lib/migrateLocalSettings';

const client = new ApiClient(backend.ws);
client.connect();

// An AI map landed for a saved edit that already references it (batch preset
// apply, GenerateAIMaps): the photo's /img pixels changed under an unchanged
// URL, so bust its cached renditions. Wired here, not in a component — the
// event must land whatever folder or view is mounted, and bumpImgBust is
// global (localStorage) state.
onAIMapsGeneratedEvent(client, (ev) => bumpImgBust(ev.photoId));

// One-time import of pre-server-settings localStorage into the daemon DB.
void migrateLocalSettings(client);

// Dev-only test hooks for the scripted UI verification (scripts/ui-verify.mjs).
if (import.meta.env.DEV) {
  void Promise.all([
    import('@/stores/uiStore'),
    import('@/lib/editSession'),
    import('@/lib/uiSettings'),
    import('@/api/export'),
    import('@/lib/changelog'),
    import('@/lib/library'),
    import('@/lib/cullHistory'),
  ]).then(([ui, es, us, ex, cl, lib, ch]) => {
    (window as unknown as Record<string, unknown>).__marraw = {
      useUIStore: ui.useUIStore,
      useEditSession: es.useEditSession,
      // Flag/rating undo history (the `cullundo` shot surface probes it).
      useCullHistory: ch.useCullHistory,
      // Action fns the scripted UI test drives directly (client-bound calls
      // like a crop drag are awkward to synthesize as raw pointer events).
      esUpdate: (patch: unknown) => es.esUpdate(client, patch as never),
      esCommit: () => es.esCommit(client),
      esSetCropping: (on: boolean) => es.esSetCropping(client, on),
      esAuto: (sections: unknown) => es.esAuto(client, sections as never),
      esPreviewSettled: () => es.esPreviewSettled(),
      // Local adjustment masks (the `masks` shot surface).
      esAddMask: (type: unknown) => es.esAddMask(client, type as never),
      esUpdateMask: (i: number, patch: unknown) => es.esUpdateMask(client, i, patch as never),
      esSetActiveMask: (i: number | null) => es.esSetActiveMask(i),
      // Retouch spots (the `heal` / `healbrush` / `spotvis` shot surfaces).
      esSetHealing: (on: boolean) => es.esSetHealing(on),
      esSetActiveSpot: (i: number | null) => es.esSetActiveSpot(i),
      esBeginSpot: (spot: unknown) => es.esBeginSpot(client, spot as never),
      esUpdateSpot: (i: number, patch: unknown) => es.esUpdateSpot(client, i, patch as never),
      esFinishSpot: (i: number) => es.esFinishSpot(client, i),
      esSetSpotTool: (t: unknown) => es.esSetSpotTool(t as never),
      esSetSpotVisualize: (on: boolean) => es.esSetSpotVisualize(on),
      // User presets (the `presets` shot surface): seed, apply, hover, scrub.
      setUserPresets: (p: unknown) => us.updateUserPresets(client, p as never),
      esApplyUserPreset: (p: unknown) => es.esApplyUserPreset(client, p as never),
      esHoverPreset: (p: unknown) => es.esHoverPreset(client, p as never),
      esHoverEnd: () => es.esHoverEnd(client),
      esSetPresetAmount: (t: number) => es.esSetPresetAmount(client, t),
      esCommitPresetAmount: () => es.esCommitPresetAmount(client),
      // Suggested looks (the `suggestions` shot surface): apply + hover.
      esApplySuggestion: (s: unknown) => es.esApplySuggestion(client, s as never),
      esHoverSuggestion: (s: unknown) => es.esHoverSuggestion(client, s as never),
      // Server-persisted UI settings (optimistic store write + server call).
      setEditGroupOpen: (id: string, open: boolean) => us.updateEditGroupOpen(client, id, open),
      setTheme: (t: unknown) => us.updateTheme(client, t as never),
      setGapMinutes: (min: number | null) => us.updateGapMinutes(client, min),
      setFlagFilter: (f: unknown) => us.updateFolderFilters(client, { flagFilter: f as never }),
      setLibrarySort: (s: unknown) => us.updateLibrarySort(client, s as never),
      setLastSeenVersion: (v: string) => us.updateLastSeenVersion(client, v),
      // Folder hop for the `folderview` shot (per-folder view memory).
      openPath: (path: string) => lib.openShoot(client, { path } as never),
      // The changelog parser, probed by the `welcome` shot surface.
      changelog: cl,
      startExport: (req: unknown) => ex.startExport(client, req as never),
    };
  });
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ApiClientProvider value={client}>
      <ThemeProvider>
        <App />
      </ThemeProvider>
    </ApiClientProvider>
  </StrictMode>,
);
