import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';
import App from './App.tsx';
import { ThemeProvider } from '@/components/theme-provider.tsx';
import { ApiClient, ApiClientProvider } from '@/api/client';
import { backend } from '@/lib/backend';

const client = new ApiClient(backend.ws);
client.connect();

// Dev-only test hooks for the scripted UI verification (scripts/ui-verify.mjs).
if (import.meta.env.DEV) {
  void Promise.all([import('@/stores/uiStore'), import('@/lib/editSession')]).then(
    ([ui, es]) => {
      (window as unknown as Record<string, unknown>).__marraw = {
        useUIStore: ui.useUIStore,
        useEditSession: es.useEditSession,
        // Action fns the scripted UI test drives directly (client-bound calls
        // like a crop drag are awkward to synthesize as raw pointer events).
        esUpdate: (patch: unknown) => es.esUpdate(client, patch as never),
        esCommit: () => es.esCommit(client),
        esSetCropping: (on: boolean) => es.esSetCropping(client, on),
      };
    },
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider defaultTheme="dark">
      <ApiClientProvider value={client}>
        <App />
      </ApiClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
