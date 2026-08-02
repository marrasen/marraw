import { useEffect } from 'react';
import { create } from 'zustand';
import type { ApiClient } from '@/api/client';
import { scanForHosts, type DiscoveredHost } from '@/api/system';
import { backend } from '@/lib/backend';
import type {
  PairRequestResult,
  PairWaitResult,
  RemoteConnection,
  RemoteProbe,
} from '@/lib/electron';

// Saved connections to other machines' libraries. They live in the Electron
// shell's preferences.json, reached over IPC — the daemon knows nothing about
// them, which is also why a remote host's own connections are invisible from
// here: each machine's list is its own, and every window connects direct.

// How often a mounted consumer re-probes every connection. A probe is a
// 3s-timeout GET /authz in the main process, so an asleep host costs nothing.
const POLL_MS = 30_000;

interface RemoteState {
  conns: RemoteConnection[];
  /** Probe result per connection id; absent while the first probe is in flight. */
  probes: Record<string, RemoteProbe>;
  /** The list has been read at least once — tells "none saved" from "not yet". */
  loaded: boolean;
}

export const useRemoteStore = create<RemoteState>(() => ({
  conns: [],
  probes: {},
  loaded: false,
}));

/** Remote connections need the Electron shell; a browser tab has no prefs. */
export const remotesSupported = (): boolean => !!window.marraw?.listRemotes;

function probeAll(conns: RemoteConnection[]) {
  for (const c of conns) void probeRemote(c);
}

/** Probes one connection and files the result under its id. */
export async function probeRemote(conn: RemoteConnection): Promise<void> {
  const res = await window.marraw?.testRemote?.(conn.host, conn.token);
  if (!res) return;
  useRemoteStore.setState((s) => ({ probes: { ...s.probes, [conn.id]: res } }));
}

export async function refreshRemotes(): Promise<RemoteConnection[]> {
  const conns = (await window.marraw?.listRemotes?.()) ?? [];
  useRemoteStore.setState((s) => ({
    conns,
    loaded: true,
    // Keep the probes of connections that survived, so a poll doesn't blink
    // every row back to "checking…".
    probes: Object.fromEntries(
      conns.filter((c) => s.probes[c.id]).map((c) => [c.id, s.probes[c.id]]),
    ),
  }));
  return conns;
}

/** Adds (no id) or updates (with id) a connection, then re-probes it. */
export async function saveRemote(conn: Partial<RemoteConnection>): Promise<RemoteConnection[]> {
  const list = (await window.marraw?.saveRemote?.(conn)) ?? [];
  useRemoteStore.setState({ conns: list, loaded: true });
  probeAll(list);
  return list;
}

export async function deleteRemote(id: string): Promise<void> {
  const list = (await window.marraw?.deleteRemote?.(id)) ?? [];
  useRemoteStore.setState({ conns: list, loaded: true });
}

/**
 * Scanning runs on the daemon this window is connected to, which in a remote
 * window is someone else's machine — it would search THEIR network, not the
 * one the user is sitting on. Saved connections belong to this computer, so
 * that answer would be useless; remote windows get manual entry instead.
 */
export const discoverySupported = (): boolean => remotesSupported() && !backend.isRemote;

/**
 * Looks for other marraw machines: mDNS on the local network, Tailscale peers
 * across a tailnet. Takes a couple of seconds — the mDNS browse has to wait
 * out its answer window — so callers should show that it is working.
 *
 * `exclude` is the already-saved connections: a scan should surface machines
 * the user has not set up yet, not repeat the ones they have. The daemon
 * filters out this computer's own addresses on top of that.
 */
export async function scanRemotes(
  client: ApiClient,
  exclude: string[],
): Promise<DiscoveredHost[]> {
  return scanForHosts(client, exclude);
}

/**
 * Asks a host to let this machine in, putting an approval dialog on its
 * screen. Nothing is saved here: the token only exists once someone over
 * there says yes.
 */
export async function pairWithHost(host: string): Promise<PairRequestResult> {
  return (
    (await window.marraw?.pairRemote?.(host)) ?? {
      ok: false,
      error: 'This build cannot pair automatically.',
    }
  );
}

/** Waits for the decision at the other end. Resolves once, when it settles. */
export async function waitForPairing(host: string, requestId: string): Promise<PairWaitResult> {
  return (
    (await window.marraw?.waitRemotePairing?.(host, requestId)) ?? {
      status: 'error',
      error: 'This build cannot pair automatically.',
    }
  );
}

/**
 * Abandons a request when the user backs out, and withdraws it on the host so
 * its dialog closes instead of waiting out the expiry.
 */
export function cancelPairing(host: string, requestId: string): void {
  void window.marraw?.cancelRemotePairing?.(host, requestId);
}

/** One line of status for a connection — the same wording everywhere. */
export function remoteStatusText(probe: RemoteProbe | undefined): string {
  if (!probe) return 'checking…';
  return probe.ok ? (probe.version ? `online · ${probe.version}` : 'online') : probe.error;
}

/** Opens a saved connection in a new window. The caller's window stays put. */
export function openRemoteWindow(id: string): void {
  void window.marraw?.openRemote?.(id);
}

/** Opens this machine's own library in a new window (the way back from a remote). */
export function openLocalWindow(): void {
  void window.marraw?.openLocal?.();
}

// Ref-counted polling: the rail and Settings both want fresh statuses, and
// they must share one timer rather than each running their own.
let consumers = 0;
let timer: ReturnType<typeof setInterval> | null = null;

/** Subscribes to the connection list, keeping statuses fresh while mounted. */
export function useRemotes(): RemoteState {
  useEffect(() => {
    if (!remotesSupported()) return;
    consumers++;
    if (consumers === 1) {
      const tick = () => void refreshRemotes().then(probeAll);
      tick();
      timer = setInterval(tick, POLL_MS);
    }
    return () => {
      consumers--;
      if (consumers === 0 && timer) {
        clearInterval(timer);
        timer = null;
      }
    };
  }, []);
  return useRemoteStore();
}
