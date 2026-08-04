import { useState } from 'react';
import { Check, Copy, Globe, Loader2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { useApiClient } from '@/api/client';
import { createLink, useStatus } from '@/api/share';
import type { GuestCaps, ShareLink } from '@/api/share';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { Segmented } from '@/components/ui/segmented';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';

// Sharing a shoot with someone who is not a photographer: a link they open on
// their phone, cull in, and send back. The capabilities are the whole
// interface — what the link can do is decided here, once, and enforced by the
// daemon for as long as it lives.

const EXPIRY_OPTIONS = [
  { value: '7', label: '7 days' },
  { value: '30', label: '30 days' },
  { value: '0', label: 'Never' },
];

interface Props {
  path: string;
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function ShareDialog({ path, name, open, onOpenChange }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="w-[440px] max-w-none gap-0 p-0 sm:max-w-none">
        {/* Mounted only while open, and keyed by shoot: reopening starts
            clean, so a dialog for one album never shows another's link. */}
        {open && <ShareForm key={path} path={path} name={name} onClose={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  );
}

function ShareForm({ path, name, onClose }: { path: string; name: string; onClose: () => void }) {
  const client = useApiClient();
  const status = useStatus();
  const [caps, setCaps] = useState<GuestCaps>({ cull: true, edits: true, downloads: false });
  const [expiry, setExpiry] = useState('7');
  const [creating, setCreating] = useState(false);
  const [link, setLink] = useState<ShareLink | null>(null);
  const [copied, setCopied] = useState(false);

  const copy = (url: string) => {
    void navigator.clipboard.writeText(url);
    setCopied(true);
    toast.success('Link copied');
  };

  const create = async () => {
    setCreating(true);
    try {
      const created = await createLink(client, path, caps, Number(expiry));
      setLink(created);
      // Copy on creation: the reason anyone opened this dialog was to send the
      // link, and making them hunt for a copy button afterwards is a step for
      // nothing.
      if (created.url) copy(created.url);
    } catch (err) {
      toast.error(`Could not create the link: ${(err as Error).message}`);
    } finally {
      setCreating(false);
    }
  };

  return (
    <>
      <div className="border-b px-5 py-4">
        <h2 className="text-base font-semibold">Share “{name}”</h2>
        <p className="mt-0.5 text-[12.5px] text-muted-foreground">
          Anyone with the link can open this album in a browser.
        </p>
      </div>

      {link ? (
        <Created link={link} copied={copied} onCopy={() => copy(link.url)} />
      ) : (
        <div className="flex flex-col gap-1 px-5 py-4">
          <Capability
            label="Rate and pick"
            hint="They can star and pick photos. Their choices appear in your library."
            checked={caps.cull}
            onChange={(v) => setCaps((c) => ({ ...c, cull: v }))}
          />
          <Capability
            label="Show my edits"
            hint="Off shows the photos straight out of camera."
            checked={caps.edits}
            onChange={(v) => setCaps((c) => ({ ...c, edits: v }))}
          />
          <Capability
            label="Allow downloads"
            hint="They can save full-size JPEGs of any photo."
            checked={caps.downloads}
            onChange={(v) => setCaps((c) => ({ ...c, downloads: v }))}
          />
          <div className="mt-3 flex items-center justify-between">
            <span className="text-[13px]">Link expires</span>
            <Segmented value={expiry} onValueChange={setExpiry} items={EXPIRY_OPTIONS} size="sm" />
          </div>
        </div>
      )}

      <Reachability status={status.data} />

      <div className="flex justify-end gap-2 border-t px-5 py-3">
        <Button variant="ghost" onClick={onClose}>
          {link ? 'Done' : 'Cancel'}
        </Button>
        {!link && (
          <Button onClick={() => void create()} disabled={creating}>
            {creating && <Loader2 className="size-3.5 animate-spin" />}
            Create link
          </Button>
        )}
      </div>
    </>
  );
}

function Created({ link, copied, onCopy }: { link: ShareLink; copied: boolean; onCopy: () => void }) {
  return (
    <div className="px-5 py-4">
      <div className="flex items-center gap-2 rounded-[7px] border bg-muted/40 p-2">
        <code className="min-w-0 flex-1 truncate font-mono text-[11.5px]">{link.url}</code>
        <Button size="sm" variant="secondary" onClick={onCopy}>
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      <p className="mt-2 text-[12px] text-muted-foreground">
        {link.expiresAt
          ? `Expires ${new Date(link.expiresAt).toLocaleDateString()}.`
          : 'Does not expire.'}{' '}
        You can withdraw it any time in Settings → Remote.
      </p>
    </div>
  );
}

// What the visitor will actually be able to reach. A link that only works on
// the tailnet is a perfectly good link between two of your own machines, and a
// useless one to send a band — so say which one this is.
function Reachability({ status }: { status: ReturnType<typeof useStatus>['data'] }) {
  if (!status) return null;
  if (status.err) {
    return (
      <Note tone="warn">
        Tailscale could not publish this machine: {status.err} The link still works over your tailnet
        or local network.
      </Note>
    );
  }
  if (!status.available) {
    return (
      <Note tone="warn">
        Tailscale isn’t running, so the link only works on your local network. Start Tailscale and
        enable Funnel to share it over the internet.
      </Note>
    );
  }
  if (!status.running) {
    // Whether the tailnet actually permits Funnel is only discoverable by
    // trying, so promise nothing until the link is minted.
    return (
      <Note tone="info">
        Will publish from <span className="font-mono">{status.hostname}</span> over Tailscale Funnel
        when you create the link.
      </Note>
    );
  }
  return (
    <Note tone="info">
      Served from <span className="font-mono">{status.hostname}</span> over Tailscale Funnel.
    </Note>
  );
}

function Note({ tone, children }: { tone: 'info' | 'warn'; children: React.ReactNode }) {
  const Icon = tone === 'warn' ? TriangleAlert : Globe;
  return (
    <div
      className={cn(
        'flex items-start gap-2 border-t px-5 py-3 text-[12px]',
        tone === 'warn' ? 'text-amber-500' : 'text-muted-foreground',
      )}
    >
      <Icon className="mt-px size-3.5 shrink-0" strokeWidth={1.5} />
      <span>{children}</span>
    </div>
  );
}

function Capability({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex cursor-pointer items-start gap-3 rounded-[7px] py-2 hover:bg-accent/40">
      <Switch checked={checked} onCheckedChange={onChange} className="mt-0.5" />
      <span className="flex min-w-0 flex-col gap-px">
        <span className="text-[13px] text-foreground">{label}</span>
        <span className="text-[11.5px] text-muted-foreground">{hint}</span>
      </span>
    </label>
  );
}
