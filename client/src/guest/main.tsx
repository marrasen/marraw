import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import './guest.css';
import { ApiClient, ApiClientProvider } from '@/api/client';
import { backend } from '@/lib/backend';

import { connection } from './connection';
import { GuestApp } from './GuestApp';

// The token comes from the URL path (/s/<token>/) — see lib/backend — and
// authenticates in the first-message auth frame like any other client. A
// rejection means the owner revoked the share or it expired while the page
// was open, which is a thing the visitor needs told, not retried forever.
const client = new ApiClient(backend.ws, {
  getAuthToken: () => backend.token,
  onConnectionRejected: (err) => connection.reject(err.message || 'This link is no longer valid.'),
});
client.connect();

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ApiClientProvider value={client}>
      <GuestApp />
    </ApiClientProvider>
  </StrictMode>,
);
