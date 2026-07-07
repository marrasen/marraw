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
  ]).then(([ui, es, us, ex]) => {
    (window as unknown as Record<string, unknown>).__marraw = {
      useUIStore: ui.useUIStore,
      useEditSession: es.useEditSession,
      // Action fns the scripted UI test drives directly (client-bound calls
      // like a crop drag are awkward to synthesize as raw pointer events).
      esUpdate: (patch: unknown) => es.esUpdate(client, patch as never),
      esCommit: () => es.esCommit(client),
      esSetCropping: (on: boolean) => es.esSetCropping(client, on),
      esAuto: (sections: unknown) => es.esAuto(client, sections as never),
      // Server-persisted UI settings (optimistic store write + server call).
      setEditGroupOpen: (id: string, open: boolean) => us.updateEditGroupOpen(client, id, open),
      setTheme: (t: unknown) => us.updateTheme(client, t as never),
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
