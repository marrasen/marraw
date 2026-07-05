import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';
import App from './App.tsx';
import { ThemeProvider } from '@/components/theme-provider.tsx';
import { ApiClient, ApiClientProvider } from '@/api/client';
import { backend } from '@/lib/backend';

const client = new ApiClient(backend.ws);
client.connect();

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider defaultTheme="dark">
      <ApiClientProvider value={client}>
        <App />
      </ApiClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
