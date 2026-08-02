import { useEffect, useState } from 'react';
import { Laptop, ShieldQuestion } from 'lucide-react';
import { toast } from 'sonner';
import { resolvePairing, useListPairingRequests, type PairingRequest } from '@/api/system';
import { useApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { backend } from '@/lib/backend';

/**
 * "Another computer wants to connect" — the approval half of remote pairing.
 *
 * This is the one place a machine is granted access to the library, so it is
 * a modal rather than a toast: a security decision should be answered, not
 * dismissed by accident. The daemon only ever sends these requests to windows
 * on this machine, so the dialog cannot appear on the computer doing the
 * asking.
 *
 * Requests arrive on a subscription, which means the dialog also closes by
 * itself when the request times out or is answered in another window.
 */
export function PairingApprovalDialog() {
  // A remote window's daemon lives on someone else's machine — approving from
  // there is exactly what the local-only rule exists to prevent. The server
  // returns an empty list either way; this just avoids the subscription.
  if (backend.isRemote) return null;
  return <PairingApprovalBody />;
}

function PairingApprovalBody() {
  const { data } = useListPairingRequests();
  const requests = data ?? [];
  // Oldest first, and one at a time: two dialogs stacked on top of each other
  // is how someone ends up approving the wrong one.
  const current = requests[0];

  if (!current) return null;
  return <ApprovalPrompt key={current.id} request={current} />;
}

function ApprovalPrompt({ request }: { request: PairingRequest }) {
  const client = useApiClient();
  const [busy, setBusy] = useState(false);
  const [remaining, setRemaining] = useState(() => secondsLeft(request.expiresAt));

  // The countdown is the honest thing to show: the request really does stop
  // being approvable, and someone staring at an unexplained dialog should
  // know it will not wait forever.
  useEffect(() => {
    const timer = setInterval(() => setRemaining(secondsLeft(request.expiresAt)), 1000);
    return () => clearInterval(timer);
  }, [request.expiresAt]);

  const decide = (approve: boolean) => {
    setBusy(true);
    resolvePairing(client, request.id, approve)
      .then(() => {
        if (approve) toast.success(`${request.name} can now open this library`);
      })
      .catch((err) => toast.error((err as Error).message))
      .finally(() => setBusy(false));
  };

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && decide(false)}>
      <DialogContent
        showCloseButton={false}
        className="sm:max-w-md"
        data-testid="pairing-approval"
      >
        <div className="flex items-start gap-3">
          <ShieldQuestion className="mt-0.5 size-5 shrink-0 text-accent-text" />
          <div className="min-w-0">
            <div className="font-heading text-base leading-none font-medium">
              Allow this computer to connect?
            </div>
            <div className="mt-1.5 text-xs text-muted-foreground">
              It will be able to browse and edit this library.
            </div>
          </div>
        </div>

        <div className="flex items-center gap-3 rounded-lg border bg-secondary px-3 py-2.5 dark:bg-white/5">
          <Laptop className="size-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] font-medium" data-testid="pairing-name">
              {request.name}
            </div>
            <div className="truncate font-mono text-[10.5px] text-faint">
              {request.addr}
              {request.platform && ` · ${request.platform}`}
            </div>
          </div>
        </div>

        <div className="flex flex-col items-center gap-1 rounded-lg border border-dashed py-3">
          <span className="text-[10px] tracking-[.06em] text-muted-foreground uppercase">
            Code on the other computer
          </span>
          <span
            className="font-mono text-2xl tracking-[.3em] tabular-nums select-text"
            data-testid="pairing-code"
          >
            {request.code}
          </span>
          <span className="text-[11px] text-muted-foreground">
            Only allow it if these match.
          </span>
        </div>

        <div className="flex items-center gap-1.5">
          <Button disabled={busy} onClick={() => decide(true)} data-testid="pairing-allow">
            Allow
          </Button>
          <Button variant="outline" disabled={busy} onClick={() => decide(false)}>
            Deny
          </Button>
          <span className="flex-1" />
          <span className="font-mono text-[10.5px] text-faint tabular-nums">
            {remaining > 0 ? `${remaining}s` : 'expired'}
          </span>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function secondsLeft(expiresAt: number): number {
  return Math.max(0, Math.round((expiresAt - Date.now()) / 1000));
}
