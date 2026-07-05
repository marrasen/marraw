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
