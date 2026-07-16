import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';
import App from './App.tsx';
import { ThemeProvider } from '@/components/theme-provider.tsx';
import { ApiClient, ApiClientProvider } from '@/api/client';
import { backend } from '@/lib/backend';
import { migrateLocalSettings } from '@/lib/migrateLocalSettings';

const client = new ApiClient(backend.ws);
client.connect();

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
      // Retouch spots (the `heal` shot surface).
      esSetHealing: (on: boolean) => es.esSetHealing(on),
      esSetActiveSpot: (i: number | null) => es.esSetActiveSpot(i),
      esBeginSpot: (spot: unknown) => es.esBeginSpot(client, spot as never),
      esUpdateSpot: (i: number, patch: unknown) => es.esUpdateSpot(client, i, patch as never),
      esFinishSpot: (i: number) => es.esFinishSpot(client, i),
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
