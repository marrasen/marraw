import { useState } from 'react';
import { Check, Copy, Globe, Loader2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { useApiClient } from '@/api/client';
import type { ExportPreset } from '@/api/settings';
import { createLink, useStatus } from '@/api/share';
import type { GuestCaps, ShareLink } from '@/api/share';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Segmented } from '@/components/ui/segmented';
import { Switch } from '@/components/ui/switch';
import { expiryLabel } from '@/lib/relativeTime';
import { useUIStore } from '@/stores/uiStore';
import { cn } from '@/lib/utils';

// Sharing a shoot with someone who is not a photographer: a link they open on
// their phone, cull in, and send back. The capabilities are the whole
// interface — what the link can do is decided here, once, and enforced by the
// daemon for as long as it lives.

// Hours. A share is usually wanted for an afternoon or a weekend, not a month,
// and the URL is a bearer credential — the shorter it lives, the less it
// matters where the message it arrived in ends up.
const EXPIRY_OPTIONS = [
  { value: '4', label: '4 hours' },
  { value: '24', label: '1 day' },
  { value: '168', label: '7 days' },
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
  // One day by default: long enough for someone to get to it, short
  // enough that a forwarded link stops mattering quickly.
  const [expiry, setExpiry] = useState('24');
  const [creating, setCreating] = useState(false);
  const [link, setLink] = useState<ShareLink | null>(null);
  const [copied, setCopied] = useState(false);
  // Which export preset renders this link's downloads; empty = full size.
  const [presetID, setPresetID] = useState('');

  const copy = (url: string) => {
    void navigator.clipboard.writeText(url);
    setCopied(true);
    toast.success('Link copied');
  };

  const create = async () => {
    setCreating(true);
    try {
      const created = await createLink(
        client,
        path,
        caps,
        Number(expiry),
        caps.downloads ? presetID : '',
      );
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
            hint="They can save JPEGs of any photo."
            checked={caps.downloads}
            onChange={(v) => setCaps((c) => ({ ...c, downloads: v }))}
          />
          {caps.downloads && <DownloadPreset presetID={presetID} onChange={setPresetID} />}
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
          // Nothing to mint against: the daemon would hand back a link with no
          // URL on it. Better to refuse here, where the note beside the button
          // says which of the two switches to go and turn on.
          <Button onClick={() => void create()} disabled={creating || !status.data?.base}>
            {creating && <Loader2 className="size-3.5 animate-spin" />}
            Create link
          </Button>
        )}
      </div>
    </>
  );
}

/**
 * Which export preset a guest's downloads are rendered with. The settings are
 * copied onto the link when it is minted, not looked up later — a link can
 * outlive the preset, and a photo someone already downloaded should not change
 * because the preset was edited afterwards.
 */
function DownloadPreset({ presetID, onChange }: { presetID: string; onChange: (id: string) => void }) {
  const presets = useUIStore((s) => s.exportPresets);
  const chosen = presets.find((p) => p.id === presetID);
  return (
    <div className="mt-1 mb-1 ml-11 flex flex-col gap-1">
      <div className="flex items-center justify-between gap-2">
        <span className="text-[12.5px] text-muted-foreground">Download settings</span>
        <DropdownMenu>
          <DropdownMenuTrigger className="flex h-[30px] items-center gap-2 rounded-lg border border-input bg-secondary px-2.5 text-xs text-secondary-foreground dark:bg-white/5">
            <span className="max-w-[180px] truncate">{chosen?.name ?? 'Full size'}</span>
            <span className="text-[10px] opacity-60">▾</span>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="end"
            className="w-[220px] rounded-[11px] border-glass-border bg-popover/98 p-[7px]"
          >
            <DropdownMenuItem
              className="flex h-8 rounded-[7px] px-2.5 text-[13px] text-muted-foreground"
              onClick={() => onChange('')}
            >
              Full size
            </DropdownMenuItem>
            {presets.map((p) => (
              <DropdownMenuItem
                key={p.id}
                className={cn(
                  'flex h-8 rounded-[7px] px-2.5 text-[13px]',
                  p.id === presetID && 'font-semibold text-foreground',
                )}
                onClick={() => onChange(p.id)}
              >
                <span className="truncate">{p.name}</span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <span className="text-[11.5px] text-muted-foreground">{presetSummary(chosen)}</span>
    </div>
  );
}

/** One line of what a guest will actually receive. */
function presetSummary(preset?: ExportPreset): string {
  if (!preset) return 'Full resolution, quality 92.';
  const o = preset.options;
  const parts = [
    o.resizeMode === 'edge' ? `${o.edgePx} px long edge` : 'full resolution',
    `quality ${o.jpegQuality}`,
  ];
  if (o.watermarkId) parts.push('watermarked');
  // Presets can name other formats; downloads are always JPEG.
  return `${parts.join(', ')}. Saved as it is now — later edits to the preset don’t change this link.`;
}

function Created({ link, copied, onCopy }: { link: ShareLink; copied: boolean; onCopy: () => void }) {
  // The share exists but has no address yet — Tailscale went down between the
  // dialog's last status push and the mint. Not an error: the URL is derived
  // per read, so the link starts working on its own once there is somewhere to
  // serve it from, and Settings → Remote will have the copy button by then.
  if (!link.url) {
    return (
      <div className="px-5 py-4 text-[12.5px] text-muted-foreground">
        “{link.name}” is shared, but this computer is not reachable from anywhere right now, so
        there is no link to copy yet. It will appear under Settings → Remote once it is.
      </div>
    );
  }
  return (
    <div className="px-5 py-4">
      {/* min-w-0: the dialog is a grid, and a grid item's min-width is auto,
          so an unbreakable URL would push the whole dialog wider than its
          fixed width rather than wrapping inside it. */}
      <div className="flex min-w-0 items-start gap-2 overflow-hidden rounded-[7px] border bg-muted/40 p-2">
        <code className="line-clamp-2 min-w-0 flex-1 font-mono text-[11.5px] break-all">
          {link.url}
        </code>
        <Button size="sm" variant="secondary" className="shrink-0" onClick={onCopy}>
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      <p className="mt-2 text-[12px] text-muted-foreground">
        {link.expiresAt ? `${capitalize(expiryLabel(link.expiresAt))}.` : 'Does not expire.'}{' '}
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
  // Nowhere to serve from. Said first and said plainly: whatever Tailscale's
  // trouble is, the outcome is that there is no link to hand out, and the two
  // ways out of it are the only useful thing to read here.
  if (!status.base) {
    return (
      <Note tone="warn">
        {status.err
          ? `Tailscale could not publish this machine: ${status.err} `
          : 'Tailscale isn’t running, '}
        and this computer has no other address anyone else can reach. Start Tailscale, or turn on
        Settings → Remote → Allow remote connections to share over your local network.
      </Note>
    );
  }
  if (status.err) {
    return (
      <Note tone="warn">
        Tailscale could not publish this machine: {status.err} The link still works where this
        computer is reachable, at <span className="font-mono">{baseHost(status.base)}</span>.
      </Note>
    );
  }
  if (!status.available) {
    return (
      <Note tone="warn">
        Tailscale isn’t running, so the link only works on your local network, at{' '}
        <span className="font-mono">{baseHost(status.base)}</span>. Start Tailscale and enable Funnel
        to share it over the internet.
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

const capitalize = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** The host:port out of a share origin, which is the part worth reading. */
function baseHost(base: string): string {
  try {
    return new URL(base).host;
  } catch {
    return base;
  }
}
