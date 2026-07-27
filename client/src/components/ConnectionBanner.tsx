import { ShieldAlert, Wifi } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useApiClient, useConnectionState } from '@/api/client';
import { backend } from '@/lib/backend';
import { useConnectionRejected } from '@/stores/connectionStore';

/**
 * Full-width banner for remote sessions: reconnect progress while the link to
 * the remote daemon is down, and a manual retry after an auth rejection (aprot
 * stops auto-reconnecting once the server rejects the token). Local windows
 * keep the subtle StatusBar dot — a local daemon outage quits the app anyway.
 */
export function ConnectionBanner() {
  const client = useApiClient();
  const state = useConnectionState();
  const rejectedMessage = useConnectionRejected((s) => s.rejectedMessage);
  const setRejected = useConnectionRejected((s) => s.setRejected);

  if (!backend.isRemote) return null;

  const target = backend.remoteName || backend.http.replace(/^http:\/\//, '');

  if (rejectedMessage) {
    return (
      <div className="flex shrink-0 items-center justify-center gap-3 border-b border-destructive/30 bg-destructive/15 px-4 py-1.5 text-[13px] text-destructive">
        <ShieldAlert className="size-4 shrink-0" />
        <span>
          Access to {target} denied — the pairing token may have changed. Check Settings → Remote on
          the host.
        </span>
        <Button
          size="sm"
          variant="destructive"
          className="h-6 px-2 text-[12px]"
          onClick={() => {
            setRejected(null);
            client.connect();
          }}
        >
          Retry
        </Button>
      </div>
    );
  }

  if (state === 'connected') return null;

  return (
    <div className="flex shrink-0 items-center justify-center gap-3 border-b border-rating/30 bg-rating/15 px-4 py-1.5 text-[13px] text-accent-text">
      <Wifi className="size-4 shrink-0 animate-pulse" />
      <span>
        {state === 'connecting' ? 'Connecting' : 'Reconnecting'} to {target}…
      </span>
    </div>
  );
}
