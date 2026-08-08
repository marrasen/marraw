// Everything under Settings -> Remote: the saved connections to other
// machines, pairing a new one, hosting this library for them, the devices
// that have been approved, and the shared albums handed out as links.
//
// This is a multi-step network feature that happens to be reached from the
// settings dialog. It lived inside it, which meant remote work and the
// slider-toolbar preferences churned the same file; the SECTIONS registry
// there was already the seam.

import { SettingRow } from '@/components/settings/SettingRow';
import { Button } from '@/components/ui/button';
import type { DiscoveredHost } from '@/api/system';
import type { RemoteAccessPrefs, RemoteConnection, RemoteProbe } from '@/lib/electron';
import { ShareReach, revokeLink, useListLinks } from '@/api/share';
import { Switch } from '@/components/ui/switch';
import { backend } from '@/lib/backend';
import { cancelPairing, deleteRemote, discoverySupported, openRemoteWindow, pairWithHost, remoteStatusText, saveRemote, scanRemotes, useRemotes, waitForPairing } from '@/stores/remoteStore';
import { cn } from '@/lib/utils';
import { expiryLabel, relativeTime } from '@/lib/relativeTime';
import { regeneratePairingToken, revokeRemoteDevice, setDeviceName, setPairingOpen, useGetRemoteAccess, useListRemoteDevices } from '@/api/system';
import { toast } from 'sonner';
import { useApiClient } from '@/api/client';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
// RemoteSection: the two halves of remote work. "Connections" is the list of
// other machines' libraries this machine can open — shell prefs, so it works
// in a remote window too. "Host this library" is the other direction, and
// only makes sense for the daemon on THIS machine.
export function RemoteSection() {
  return (
    <div className="flex flex-col gap-6">
      <ConnectionsSection />
      {!backend.isRemote && <HostSection />}
    </div>
  );
}

/**
 * Saved connections to libraries on other machines. Adding one normally means
 * picking this machine off a scan and having someone approve it over there —
 * no address to find, no token to copy. Manual entry stays for the cases a
 * scan cannot reach: blocked multicast, a non-default port, or a host set up
 * with the shared pairing token.
 */
function ConnectionsSection() {
  const { conns, probes } = useRemotes();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<RemoteConnection | null>(null);
  // Stable identity: this reaches the pairing panel's effect, and a fresh
  // closure on every render of this list would restart the pairing it is in
  // the middle of. The panel guards against that itself, but handing it a
  // changing callback would still be a trap for the next caller.
  const closeAdding = useCallback(() => setAdding(false), []);
  // Joined into one string so the scan effect keys off the CONTENT of the
  // list, not the array identity a poll hands back every 30 seconds.
  const savedKey = conns.map((c) => c.host).join(',');
  const savedHosts = useMemo(() => (savedKey ? savedKey.split(',') : []), [savedKey]);

  return (
    <div className="flex flex-col">
      <div className="mb-1.5 text-[10px] tracking-[.06em] text-muted-foreground uppercase">
        Connections
      </div>
      <div className="flex flex-col gap-1.5">
        {conns.length === 0 && !adding && !editing && (
          <div className="rounded-lg border border-dashed px-3 py-4 text-center text-xs text-muted-foreground">
            No connections yet. Turn on “Allow remote connections” on the computer that holds the
            library, then add it here — it will ask you to approve this computer.
          </div>
        )}
        {conns.map((c) => (
          <div
            key={c.id}
            className="flex items-center gap-3 rounded-lg border bg-secondary px-3 py-2 dark:bg-white/5"
          >
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] font-medium">{c.name}</div>
              <div className="truncate font-mono text-[10.5px] text-faint">{c.host}</div>
            </div>
            <RemoteStatus probe={probes[c.id]} />
            <Button variant="outline" size="sm" onClick={() => openRemoteWindow(c.id)}>
              Open
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setEditing(c)}>
              Edit
            </Button>
          </div>
        ))}
      </div>
      {editing ? (
        <ConnectionEditor conn={editing} onClose={() => setEditing(null)} />
      ) : adding ? (
        <AddConnectionPanel onClose={closeAdding} exclude={savedHosts} />
      ) : (
        <div className="mt-2.5">
          <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
            Add connection…
          </Button>
        </div>
      )}
    </div>
  );
}

/**
 * Add a connection: scan first, type only if you have to.
 *
 * The scan starts the moment this opens — someone who clicked "Add" has
 * already told us what they want, and making them press a second button to
 * begin looking is pure ceremony.
 */
function AddConnectionPanel({
  onClose,
  exclude,
}: {
  onClose: () => void;
  /** Hosts already saved — stable identity, it feeds the scan effect. */
  exclude: string[];
}) {
  const client = useApiClient();
  const [hosts, setHosts] = useState<DiscoveredHost[] | null>(null);
  // The scan starts on mount, so "scanning" is the state this opens in rather
  // than something an effect flips on afterwards. A shell too old to scan goes
  // straight to the manual form.
  const [scanning, setScanning] = useState(discoverySupported);
  const [manual, setManual] = useState(() => !discoverySupported());
  const [pairing, setPairing] = useState<{ host: string; name: string } | null>(null);

  const runScan = useCallback(
    (alive: () => boolean) => {
      return scanRemotes(client, exclude)
        .catch(() => [] as DiscoveredHost[])
        .then((found) => {
          if (!alive()) return;
          setHosts(found);
          setScanning(false);
        });
    },
    [client, exclude],
  );

  useEffect(() => {
    if (!discoverySupported()) return;
    let live = true;
    void runScan(() => live);
    return () => {
      live = false;
    };
  }, [runScan]);

  const rescan = () => {
    setScanning(true);
    void runScan(() => true);
  };

  if (pairing) {
    return (
      <PairingWaitPanel
        host={pairing.host}
        hostName={pairing.name}
        onDone={onClose}
        onCancel={() => setPairing(null)}
      />
    );
  }

  if (manual) {
    return (
      <ConnectionEditor
        conn={{}}
        onClose={onClose}
        onBack={discoverySupported() ? () => setManual(false) : undefined}
      />
    );
  }

  return (
    <div className="mt-2.5 flex flex-col gap-2 rounded-lg border border-primary/50 bg-primary/5 p-3">
      <div className="flex items-center gap-2">
        <span className="flex-1 text-[11px] text-muted-foreground">
          {scanning
            ? 'Looking for computers…'
            : hosts && hosts.length > 0
              ? 'Computers found on your network'
              : 'No other computers found'}
        </span>
        <Button variant="ghost" size="sm" disabled={scanning} onClick={rescan}>
          {scanning ? 'Scanning…' : 'Scan again'}
        </Button>
      </div>

      <div className="flex flex-col gap-1.5" data-testid="remote-scan-results">
        {(hosts ?? []).map((h) => (
          <div
            key={h.host}
            className="flex items-center gap-3 rounded-lg border bg-secondary px-3 py-2 dark:bg-white/5"
          >
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] font-medium">{h.name}</div>
              <div className="truncate font-mono text-[10.5px] text-faint">
                {h.host} · {h.source === 'tailscale' ? 'Tailscale' : 'Local network'}
              </div>
            </div>
            <Button
              size="sm"
              disabled={!h.pairing}
              title={h.pairing ? undefined : 'That computer is not accepting new connections'}
              onClick={() => setPairing({ host: h.host, name: h.name })}
            >
              Connect
            </Button>
          </div>
        ))}
        {!scanning && hosts?.length === 0 && (
          <div className="rounded-lg border border-dashed px-3 py-3 text-center text-[11px] text-muted-foreground">
            Check that the other computer is awake and has “Allow remote connections” turned on. On
            some networks the search is blocked — you can still add it by address.
          </div>
        )}
      </div>

      <div className="mt-0.5 flex items-center gap-1.5">
        <Button variant="outline" size="sm" onClick={() => setManual(true)}>
          Enter details manually
        </Button>
        <Button variant="ghost" size="sm" onClick={onClose}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

/**
 * The waiting half of pairing: this machine has asked, and someone at the
 * other end has to say yes. The code shown here is the same one on their
 * screen — it is what makes "Allow" a decision rather than a reflex.
 */
function PairingWaitPanel({
  host,
  hostName,
  onDone,
  onCancel,
}: {
  host: string;
  hostName: string;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [code, setCode] = useState('');
  const [error, setError] = useState('');

  // Asking to be let in puts a dialog on someone else's screen, so a re-run of
  // this effect is not free the way a re-fetch would be. Two guards:
  //
  // `onDone` is held in a ref rather than being a dependency — it arrives as a
  // fresh closure whenever the connections list re-renders, which saving the
  // connection itself causes at the exact moment of approval. Depending on it
  // restarted the pairing and popped a second request on the host.
  //
  // Cleanup then withdraws whatever this run created, including when it is
  // torn down before the request even comes back (StrictMode's double-invoke
  // in development does exactly that). Between them, at most one request is
  // ever live, and none is left behind to expire on the host's screen.
  const onDoneRef = useRef(onDone);
  useEffect(() => {
    onDoneRef.current = onDone;
  });

  useEffect(() => {
    let live = true;
    let requestId = '';
    void (async () => {
      const req = await pairWithHost(host);
      if (!req.ok) {
        if (live) setError(req.error);
        return;
      }
      requestId = req.requestId;
      if (!live) {
        // Torn down while we were asking: take it back off their screen.
        cancelPairing(host, requestId);
        return;
      }
      setCode(req.code);

      const res = await waitForPairing(host, requestId);
      if (!live) return;
      if (res.status === 'approved' && res.token) {
        await saveRemote({ name: res.hostName || hostName, host, token: res.token });
        toast.success(`Connected to ${res.hostName || hostName}`);
        onDoneRef.current();
        return;
      }
      setError(
        res.status === 'denied'
          ? 'That computer declined the connection.'
          : res.status === 'expired'
            ? 'Nobody approved it in time. Try again when someone is at that computer.'
            : res.status === 'canceled'
              ? ''
              : (res.error ?? 'The connection could not be set up.'),
      );
    })();
    return () => {
      live = false;
      // Withdraws the request too, so backing out here clears the dialog on
      // the other machine instead of leaving it up until it expires.
      if (requestId) cancelPairing(host, requestId);
    };
  }, [host, hostName]);

  return (
    <div className="mt-2.5 flex flex-col gap-2 rounded-lg border border-primary/50 bg-primary/5 p-3">
      {error ? (
        <div className="text-[11px] text-destructive">{error}</div>
      ) : (
        <>
          <div className="text-[11px] text-muted-foreground">
            Waiting for someone to approve this computer on{' '}
            <span className="text-foreground">{hostName}</span>.
          </div>
          <div className="flex flex-col items-center gap-1 rounded-lg border border-dashed py-3">
            <span className="text-[10px] tracking-[.06em] text-muted-foreground uppercase">
              Check this code matches
            </span>
            <span
              className="font-mono text-2xl tracking-[.3em] tabular-nums select-text"
              data-testid="pairing-wait-code"
            >
              {code || '····'}
            </span>
          </div>
        </>
      )}
      <div className="mt-0.5 flex items-center gap-1.5">
        <Button variant="ghost" size="sm" onClick={onCancel}>
          {error ? 'Back' : 'Cancel'}
        </Button>
      </div>
    </div>
  );
}

/** Live reachability of one saved connection, as of the last 30s poll. */
function RemoteStatus({ probe }: { probe: RemoteProbe | undefined }) {
  const label = remoteStatusText(probe);
  return (
    <span
      className={cn(
        'shrink-0 font-mono text-[10.5px]',
        !probe ? 'text-faint' : probe.ok ? 'text-emerald-500' : 'text-destructive',
      )}
      title={probe && !probe.ok ? probe.error : undefined}
    >
      {label}
    </span>
  );
}

/**
 * Add/edit one connection by hand. This is the fallback path — a scan and an
 * approval is the normal way in — so it keeps the pairing-token field: a host
 * behind blocked multicast, on a non-default port, or set up before pairing
 * existed is still reachable this way.
 *
 * The token is tested before saving: a wrong token would only bounce at
 * connect time, but an asleep host is not an error — that saves with a
 * warning, since it will answer later.
 */
function ConnectionEditor({
  conn,
  onClose,
  onBack,
}: {
  conn: Partial<RemoteConnection>;
  onClose: () => void;
  onBack?: () => void;
}) {
  const [name, setName] = useState(conn.name ?? '');
  const [host, setHost] = useState(conn.host ?? '');
  const [token, setToken] = useState(conn.token ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const submit = async () => {
    if (!host.trim()) {
      setError('Host is required.');
      return;
    }
    setBusy(true);
    setError('');
    const probe = await window.marraw?.testRemote?.(host.trim(), token.trim());
    if (probe && !probe.ok && probe.error === 'invalid token') {
      setBusy(false);
      setError('The daemon answered, but rejected this token.');
      return;
    }
    await saveRemote({ id: conn.id, name: name.trim(), host: host.trim(), token: token.trim() });
    setBusy(false);
    if (probe && !probe.ok) toast.warning(`Saved — ${host.trim()} is not answering (${probe.error})`);
    else toast.success(`Saved ${name.trim() || host.trim()}`);
    onClose();
  };

  const remove = async () => {
    if (conn.id) await deleteRemote(conn.id);
    onClose();
  };

  return (
    <div className="mt-2.5 flex flex-col gap-2 rounded-lg border border-primary/50 bg-primary/5 p-3">
      <ConnectionField label="Name" value={name} onChange={setName} placeholder="Home desktop" />
      <ConnectionField
        label="Host (name or IP, optionally :port)"
        value={host}
        onChange={setHost}
        placeholder="100.64.0.12 or desktop:8482"
        mono
      />
      <ConnectionField
        label="Pairing token (Settings → Remote on that machine)"
        value={token}
        onChange={setToken}
        placeholder="32-character token"
        mono
      />
      {error && <div className="text-[11px] text-destructive">{error}</div>}
      <div className="mt-0.5 flex items-center gap-1.5">
        <Button size="sm" disabled={busy} onClick={() => void submit()}>
          {busy ? 'Checking…' : 'Save'}
        </Button>
        <Button variant="ghost" size="sm" disabled={busy} onClick={onBack ?? onClose}>
          {onBack ? 'Back' : 'Cancel'}
        </Button>
        <span className="flex-1" />
        {conn.id && (
          <Button variant="destructive" size="sm" disabled={busy} onClick={() => void remove()}>
            Remove
          </Button>
        )}
      </div>
    </div>
  );
}

function ConnectionField({
  label,
  value,
  onChange,
  placeholder,
  mono,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-[11px] text-muted-foreground">{label}</span>
      <input
        className={cn(
          'h-8 rounded-lg border border-input bg-secondary px-2.5 text-xs outline-none focus:border-ring dark:bg-white/5',
          mono && 'font-mono',
        )}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}

// HostSection: host this library to other machines (e.g. a laptop over a
// Tailscale network). The listen/port toggle is a shell preference applied at
// daemon spawn — hence the relaunch dance — while the pairing token lives in
// the daemon and swaps live.
function HostSection() {
  const client = useApiClient();
  const [prefs, setPrefs] = useState<RemoteAccessPrefs | null>(null);
  // Subscribed, not fetched once: renaming this machine or approving a device
  // pushes a fresh snapshot, so two open windows never disagree.
  const { data: info } = useGetRemoteAccess();
  const [port, setPort] = useState('');
  const [confirmRegen, setConfirmRegen] = useState(false);
  const [name, setName] = useState<string | null>(null);

  useEffect(() => {
    void window.marraw?.getRemoteAccess?.().then((p) => {
      setPrefs(p);
      setPort(String(p.port));
    });
  }, []);

  // The name field tracks the server until the user starts typing; `name`
  // being null means "not edited", which is what keeps a push from yanking
  // characters out from under them mid-edit.
  const nameValue = name ?? info?.deviceName ?? '';
  const applyName = () => {
    if (name === null || name === info?.deviceName) {
      setName(null);
      return;
    }
    setDeviceName(client, name.trim())
      .then(() => setName(null))
      .catch((err) => {
        setName(null);
        toast.error((err as Error).message);
      });
  };

  const update = (patch: Partial<RemoteAccessPrefs>) =>
    window.marraw
      ?.setRemoteAccess?.(patch)
      .then((p) => {
        setPrefs(p);
        setPort(String(p.port));
      })
      .catch((err) => toast.error((err as Error).message));

  const applyPort = () => {
    const n = Number(port);
    if (!prefs || !Number.isInteger(n) || n < 1 || n > 65535 || n === prefs.port) {
      setPort(prefs ? String(prefs.port) : '');
      return;
    }
    void update({ port: n });
  };

  const regen = () => {
    setConfirmRegen(false);
    regeneratePairingToken(client)
      .then(() => toast.success('Pairing token regenerated — saved connections need the new token'))
      .catch((err) => toast.error((err as Error).message));
  };

  return (
    <div className="flex flex-col">
      <div className="mb-1.5 text-[10px] tracking-[.06em] text-muted-foreground uppercase">
        Host this library
      </div>
      <SettingRow
        title="Allow remote connections"
        description="Let marraw on another machine (e.g. your laptop over Tailscale) open this library. The daemon listens on all interfaces on the port below, and other computers can find it by name — but nothing gets in until you approve it here."
        control={
          <Switch
            checked={prefs?.enabled ?? false}
            disabled={!prefs}
            onCheckedChange={(v) => update({ enabled: v })}
            aria-label="Allow remote connections"
          />
        }
      />
      {prefs?.enabled && (
        <SettingRow
          title="This computer's name"
          description="What other computers see when they find this one."
          control={
            <input
              className="h-8 w-44 rounded-lg border border-input bg-secondary px-2.5 text-xs outline-none focus:border-ring dark:bg-white/5"
              value={nameValue}
              placeholder="…"
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && applyName()}
              onBlur={applyName}
              aria-label="This computer's name"
            />
          }
        />
      )}
      {prefs?.enabled && (
        <SettingRow
          title="Accept new connection requests"
          description="When off, computers you have already approved keep working, but nobody new can ask. Turn it off once your machines are set up."
          control={
            <Switch
              checked={info?.pairingOpen ?? true}
              disabled={!info}
              onCheckedChange={(v) =>
                setPairingOpen(client, v).catch((err) => toast.error((err as Error).message))
              }
              aria-label="Accept new connection requests"
            />
          }
        />
      )}
      {prefs?.enabled && (
        <SettingRow
          title="Port"
          description="Remote machines connect to this port. Pick one that's free on this machine; saved connections on other machines include it."
          control={
            <input
              className="h-8 w-20 rounded-lg border border-input bg-secondary px-2 text-right font-mono text-xs outline-none focus:border-ring dark:bg-white/5"
              value={port}
              onChange={(e) => setPort(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && applyPort()}
              onBlur={applyPort}
              aria-label="Remote access port"
            />
          }
        />
      )}
      {prefs?.restartRequired && (
        <SettingRow
          title={<span className="text-accent-text">Restart required</span>}
          description="Remote access settings apply when the app starts."
          control={
            <Button variant="outline" size="sm" onClick={() => void window.marraw?.relaunch?.()}>
              Restart now
            </Button>
          }
        />
      )}
      {prefs?.enabled && !prefs.restartRequired && (
        <SettingRow
          title="Reachable at"
          description={
            <div className="flex flex-col gap-1">
              <span>
                Other computers can use any of these. Enter one under Add connection → Enter details
                manually if the search does not find this computer.
              </span>
              <span className="font-mono text-[11.5px] text-foreground select-text">
                {info?.addresses?.length ? info.addresses.join('  ·  ') : 'no network addresses'}
              </span>
            </div>
          }
        />
      )}
      {/* The one failure a user cannot otherwise see: the daemon is healthy
          and the toggle says yes, but nothing announces it, so a scan on the
          next desk finds nothing. On Windows this is a declined firewall
          prompt, which is why the wording points there. */}
      {prefs?.enabled && !prefs.restartRequired && info && !info.advertising && (
        <SettingRow
          title={<span className="text-accent-text">Not visible to searches</span>}
          description={
            <div className="flex flex-col gap-1">
              <span>
                This computer is running, but it is not announcing itself on the local network, so
                other computers will not find it by name. They can still connect using an address
                above. On Windows this usually means a firewall prompt was declined — allow marraw
                through Windows Defender Firewall, then restart it.
              </span>
              {info.advertiseError && (
                <span className="font-mono text-[10.5px] text-faint select-text">
                  {info.advertiseError}
                </span>
              )}
            </div>
          }
        />
      )}
      {prefs?.enabled && <ApprovedDevices />}
      {/* Not gated on remote access: a share is served through the Tailscale
          tunnel, which reaches the daemon on loopback. Sharing an album does
          not require opening this machine to the local network. */}
      <SharedAlbums />
      <SettingRow
        title="Pairing token"
        description={
          <div className="flex flex-col gap-1">
            <span>
              The manual way in, for a computer the search cannot reach. Enter it there under
              Settings → Remote → Add connection → Enter details manually. Regenerating locks out
              every connection set up this way; approved computers above are unaffected.
            </span>
            <span className="font-mono text-[11.5px] text-foreground select-text">
              {info ? info.pairingToken : '…'}
            </span>
          </div>
        }
        control={
          <div className="flex gap-1.5">
            <Button
              variant="outline"
              size="sm"
              disabled={!info}
              onClick={() => {
                void navigator.clipboard.writeText(info!.pairingToken);
                toast.success('Pairing token copied');
              }}
            >
              Copy
            </Button>
            {confirmRegen ? (
              <>
                <Button variant="ghost" size="sm" onClick={() => setConfirmRegen(false)}>
                  Cancel
                </Button>
                <Button variant="destructive" size="sm" onClick={regen}>
                  Regenerate
                </Button>
              </>
            ) : (
              <Button
                variant="destructive"
                size="sm"
                disabled={!info}
                onClick={() => setConfirmRegen(true)}
              >
                Regenerate
              </Button>
            )}
          </div>
        }
      />
    </div>
  );
}

/**
 * The computers approved through the pairing dialog. Each holds a token of
 * its own, so revoking one here is exactly that — the others keep working,
 * unlike regenerating the shared pairing token.
 */
function ApprovedDevices() {
  const client = useApiClient();
  const { data } = useListRemoteDevices();
  const devices = data ?? [];
  const [confirmID, setConfirmID] = useState('');

  if (devices.length === 0) return null;

  const revoke = (id: string, name: string) => {
    setConfirmID('');
    revokeRemoteDevice(client, id)
      .then(() => toast.success(`${name} can no longer connect`))
      .catch((err) => toast.error((err as Error).message));
  };

  return (
    <SettingRow
      title="Approved computers"
      description="Computers you let in. Revoking one disconnects it now and leaves the others alone."
      control={
        <div className="flex w-64 flex-col gap-1.5" data-testid="approved-devices">
          {devices.map((d) => (
            <div
              key={d.id}
              className="flex items-center gap-2 rounded-lg border bg-secondary px-2.5 py-1.5 dark:bg-white/5"
            >
              <div className="min-w-0 flex-1">
                <div className="truncate text-[12px] font-medium">{d.name}</div>
                <div className="truncate font-mono text-[10px] text-faint">
                  last seen {relativeTime(d.lastSeen)}
                </div>
              </div>
              {confirmID === d.id ? (
                <>
                  <Button variant="ghost" size="sm" onClick={() => setConfirmID('')}>
                    Cancel
                  </Button>
                  <Button variant="destructive" size="sm" onClick={() => revoke(d.id, d.name)}>
                    Revoke
                  </Button>
                </>
              ) : (
                <Button variant="ghost" size="sm" onClick={() => setConfirmID(d.id)}>
                  Revoke
                </Button>
              )}
            </div>
          ))}
        </div>
      }
    />
  );
}

/**
 * Albums shared as links. Unlike an approved computer, the holder of one of
 * these is not the user and not trusted with the library — see the guest gate
 * in the daemon. Withdrawing one drops whoever is holding it immediately.
 */
function SharedAlbums() {
  const client = useApiClient();
  const { data } = useListLinks();
  const links = data ?? [];
  const [confirmID, setConfirmID] = useState('');

  if (links.length === 0) return null;

  const revoke = (id: string, name: string) => {
    setConfirmID('');
    revokeLink(client, id)
      .then(() => toast.success(`“${name}” is no longer shared`))
      .catch((err) => toast.error((err as Error).message));
  };

  // A list of its own rather than a SettingRow control: an album name and when
  // the link runs out are the two things worth reading here, and neither fits
  // in the narrow column beside a title. Same shape as the connections list,
  // but it keeps the SettingRow spacing so the section's rhythm is unbroken.
  return (
    <div className="flex flex-col border-b py-4 first:pt-0 last:border-0">
      <div className="text-sm font-medium">Shared albums</div>
      <p className="mt-0.5 mb-2.5 text-xs leading-normal text-muted-foreground">
        Links you have handed out. Withdrawing one stops it working immediately, wherever it has
        been forwarded.
      </p>
      <div className="flex flex-col gap-1.5" data-testid="shared-albums">
        {links.map((l) => (
          <div
            key={l.id}
            className="flex items-center gap-3 rounded-lg border bg-secondary px-3 py-2 dark:bg-white/5"
          >
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-1.5">
                <span className="truncate text-[13px] font-medium">{l.name}</span>
                {l.online && (
                  <span
                    className="size-1.5 shrink-0 animate-pulse rounded-full bg-emerald-500"
                    title="Someone has this link open right now"
                  />
                )}
              </div>
              {/* Reach ahead of the photo count: "who did I hand this to" is
                  the thing worth reading, and it is the one property of a
                  share that cannot be changed after the fact. */}
              <div className="truncate font-mono text-[10.5px] text-faint">
                {l.expired ? 'expired' : expiryLabel(l.expiresAt)}
                {' · '}
                {l.reach === ShareReach.Tailnet ? 'my devices' : 'anyone with the link'}
                {' · '}
                {l.photoCount} photo{l.photoCount === 1 ? '' : 's'}
                {l.caps.downloads && ` · ${l.exportName || 'full size'}`}
              </div>
              {/* Its own line: the two together overrun the row even at full
                  width, and the expiry is the half worth reading. */}
              <div className="truncate font-mono text-[10.5px] text-faint">
                {l.online ? 'viewing now' : `opened ${relativeTime(l.lastSeen)}`}
              </div>
            </div>
            {l.url && !l.expired && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  void navigator.clipboard.writeText(l.url);
                  toast.success('Link copied');
                }}
              >
                Copy
              </Button>
            )}
            {confirmID === l.id ? (
              <>
                <Button variant="ghost" size="sm" onClick={() => setConfirmID('')}>
                  Cancel
                </Button>
                <Button variant="destructive" size="sm" onClick={() => revoke(l.id, l.name)}>
                  Withdraw
                </Button>
              </>
            ) : (
              <Button variant="ghost" size="sm" onClick={() => setConfirmID(l.id)}>
                Withdraw
              </Button>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
