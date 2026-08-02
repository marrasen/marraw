// Finding other marraw machines, and asking one to let us in.
//
// This all lives in the main process on purpose: there is no CORS here, and
// mDNS and the Tailscale CLI are not reachable from a renderer at all. The
// daemon's pairing endpoints deliberately send no CORS headers, so the main
// process is in fact the ONLY thing that can drive the pairing flow.
const os = require('node:os');
const fs = require('node:fs');
const { execFile } = require('node:child_process');

// The daemon's default remote port. A bare hostname gets it appended; the
// Tailscale sweep can only guess it, since a peer's real port is unknowable
// without asking (manual entry covers the rest).
const DEFAULT_PORT = 8482;

// The DNS-SD service type the host advertises and the laptop browses for.
const SERVICE_TYPE = 'marraw';

// How long to listen for mDNS answers. Long enough for a sleepy access point,
// short enough that "Looking for computers…" doesn't feel broken.
const BROWSE_MS = 2500;

// Per-host /hello timeout. These are LAN or tailnet round trips; a machine
// that cannot answer in this window is not one we can usefully offer.
const PROBE_MS = 1500;

// Concurrent /hello probes. A tailnet can have a lot of peers and we do not
// want to open a socket to each of them at once.
const PROBE_CONCURRENCY = 12;

/** "host" or "host:port" → "host:port" (the daemon's remote default port). */
const normalizeHost = (host) => {
  const h = String(host).trim().replace(/^https?:\/\//, '').replace(/\/+$/, '');
  // A lone hostname/IPv4 gets the default port; IPv6 literals and host:port
  // pass through.
  return /^[^:]+$/.test(h) ? `${h}:${DEFAULT_PORT}` : h;
};

// ---- Advertising (host side) ----

let bonjour = null;
let published = null;

/**
 * Announces this machine's daemon on the local network. Called only when
 * "Allow remote connections" is on — an unreachable daemon that advertises
 * itself would just be offering connections that cannot be made.
 */
function startAdvertising({ port, name }) {
  stopAdvertising();
  try {
    const { Bonjour } = require('bonjour-service');
    bonjour ??= new Bonjour();
    published = bonjour.publish({ name, type: SERVICE_TYPE, port, txt: { app: 'marraw' } });
    console.log(`[mdns] advertising ${name} on ${port}`);
  } catch (err) {
    // A blocked multicast socket, a locked-down macOS prompt, a container
    // with no network — none of these should stop the app starting. The host
    // is still reachable by address, and pairing still works.
    console.error(`[mdns] cannot advertise: ${err.message}`);
  }
}

function stopAdvertising() {
  try {
    published?.stop?.();
  } catch {
    /* already gone */
  }
  published = null;
}

/** Tears down the mDNS socket entirely (app shutdown). */
function closeDiscovery() {
  stopAdvertising();
  try {
    bonjour?.destroy?.();
  } catch {
    /* already gone */
  }
  bonjour = null;
}

// ---- Discovery (connecting side) ----

/** Browses for advertised daemons. Resolves to ["host:port", …]. */
function browseMdns(timeoutMs = BROWSE_MS) {
  return new Promise((resolve) => {
    let browser;
    const found = new Set();
    const done = () => {
      try {
        browser?.stop?.();
      } catch {
        /* nothing to stop */
      }
      resolve([...found]);
    };
    try {
      const { Bonjour } = require('bonjour-service');
      bonjour ??= new Bonjour();
      browser = bonjour.find({ type: SERVICE_TYPE }, (service) => {
        // Prefer a literal IPv4: the .local name needs working mDNS
        // resolution on top of working mDNS discovery, and on Windows that
        // second half often isn't there.
        const v4 = (service.addresses ?? []).find((a) => !a.includes(':'));
        const host = v4 ?? service.host;
        if (host) found.add(normalizeHost(`${host}:${service.port ?? DEFAULT_PORT}`));
      });
    } catch (err) {
      console.error(`[mdns] cannot browse: ${err.message}`);
      resolve([]);
      return;
    }
    setTimeout(done, timeoutMs);
  });
}

// Where Tailscale puts its CLI. There is no cross-platform way to ask, and no
// npm binding — but the JSON status output is stable and this is the only
// route to a tailnet, where mDNS cannot reach (multicast does not cross it).
const TAILSCALE_BINS = {
  darwin: [
    '/Applications/Tailscale.app/Contents/MacOS/Tailscale',
    '/usr/local/bin/tailscale',
    '/opt/homebrew/bin/tailscale',
  ],
  win32: [
    'C:\\Program Files\\Tailscale\\tailscale.exe',
    'C:\\Program Files (x86)\\Tailscale\\tailscale.exe',
  ],
  linux: ['/usr/bin/tailscale', '/usr/local/bin/tailscale', 'tailscale'],
};

function tailscaleBin() {
  for (const bin of TAILSCALE_BINS[process.platform] ?? []) {
    // A bare name (no separator) is left to PATH resolution.
    if (!bin.includes('/') && !bin.includes('\\')) return bin;
    if (fs.existsSync(bin)) return bin;
  }
  return null;
}

/**
 * Online tailnet peers, as "host:8482". Silently empty when Tailscale isn't
 * installed or isn't running — this is an extra way to find machines, never a
 * requirement.
 */
function tailscalePeers() {
  return new Promise((resolve) => {
    const bin = tailscaleBin();
    if (!bin) return resolve([]);
    execFile(bin, ['status', '--json'], { timeout: 3000, maxBuffer: 8 << 20 }, (err, stdout) => {
      if (err) {
        console.log(`[tailscale] status unavailable: ${err.message}`);
        return resolve([]);
      }
      try {
        const status = JSON.parse(stdout);
        const out = [];
        for (const peer of Object.values(status.Peer ?? {})) {
          if (!peer?.Online) continue;
          // MagicDNS name if the tailnet has it, else the 100.x address.
          const dns = typeof peer.DNSName === 'string' ? peer.DNSName.replace(/\.$/, '') : '';
          const host = dns || (peer.TailscaleIPs ?? [])[0];
          if (host) out.push(normalizeHost(host));
        }
        resolve(out);
      } catch (parseErr) {
        console.error(`[tailscale] unparseable status: ${parseErr.message}`);
        resolve([]);
      }
    });
  });
}

/**
 * Asks one address whether it is a marraw daemon and what it calls itself.
 * GET /hello is unauthenticated — it exists precisely so a machine with no
 * credential yet can show a name instead of a bare IP.
 */
async function probeHello(host, timeoutMs = PROBE_MS) {
  try {
    const res = await fetch(`http://${normalizeHost(host)}/hello`, {
      signal: AbortSignal.timeout(timeoutMs),
    });
    if (!res.ok) return null;
    const body = await res.json().catch(() => null);
    if (!body || body.app !== 'marraw') return null;
    return {
      name: typeof body.name === 'string' && body.name ? body.name : normalizeHost(host),
      version: typeof body.version === 'string' ? body.version : '',
      pairing: body.pairing !== false,
    };
  } catch {
    return null;
  }
}

/** This machine's own addresses, so a scan doesn't offer us ourselves. */
function selfAddresses() {
  const out = new Set(['localhost', '127.0.0.1', '::1', os.hostname(), `${os.hostname()}.local`]);
  for (const addrs of Object.values(os.networkInterfaces())) {
    for (const a of addrs ?? []) out.add(a.address);
  }
  return out;
}

/** Strips the ":port" from a "host:port", handling IPv6 literals. */
function hostOnly(hostPort) {
  const m = /^\[(.+)\]:\d+$/.exec(hostPort);
  if (m) return m[1];
  const i = hostPort.lastIndexOf(':');
  return i > 0 ? hostPort.slice(0, i) : hostPort;
}

/**
 * Finds marraw machines: mDNS on the local network, Tailscale peers across a
 * tailnet. Both run concurrently; every candidate from either is confirmed
 * with /hello, which is also what supplies the display name.
 *
 * `exclude` is a list of "host:port" already saved — a scan should surface
 * machines the user has not set up yet, not repeat the ones they have.
 */
async function scanForHosts({ exclude = [] } = {}) {
  const [mdns, tailscale] = await Promise.all([browseMdns(), tailscalePeers()]);

  const self = selfAddresses();
  const skip = new Set(exclude.map((h) => normalizeHost(h)));
  const sources = new Map();
  for (const [list, source] of [
    [mdns, 'mdns'],
    [tailscale, 'tailscale'],
  ]) {
    for (const host of list) {
      if (skip.has(host) || self.has(hostOnly(host))) continue;
      // mDNS wins the label when a machine turns up on both: "Local network"
      // is the more useful thing to tell someone standing next to it.
      if (!sources.has(host)) sources.set(host, source);
    }
  }

  const candidates = [...sources.keys()];
  const found = [];
  let next = 0;
  await Promise.all(
    Array.from({ length: Math.min(PROBE_CONCURRENCY, candidates.length) }, async () => {
      while (next < candidates.length) {
        const host = candidates[next++];
        const hello = await probeHello(host);
        if (hello) found.push({ host, source: sources.get(host), ...hello });
      }
    }),
  );
  found.sort((a, b) => a.name.localeCompare(b.name));
  return found;
}

// ---- Pairing (connecting side) ----

/**
 * Asks a host to let this machine in. Returns the request id and the code the
 * host is now showing — the user checks it matches before approving there.
 */
async function requestPairing(host, name) {
  const res = await fetch(`http://${normalizeHost(host)}/pair/request`, {
    method: 'POST',
    // The content type is load-bearing, not decoration: the daemon requires
    // it, which is what stops a web page driving this flow with a form post.
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, platform: process.platform }),
    signal: AbortSignal.timeout(5000),
  });
  if (res.status === 403) return { ok: false, error: 'That computer is not accepting new connections.' };
  if (res.status === 429) return { ok: false, error: 'That computer already has connection requests waiting.' };
  if (res.status === 404) return { ok: false, error: 'That computer has remote connections turned off.' };
  if (!res.ok) return { ok: false, error: `The computer answered with HTTP ${res.status}.` };
  const body = await res.json();
  return { ok: true, requestId: body.requestId, code: body.code, hostName: body.hostName };
}

// In-flight waits, so the renderer can cancel one when the user backs out.
const waits = new Map();

/**
 * Waits for the person at the host to decide. The daemon's long-poll returns
 * "pending" every 30s or so; we re-poll until it settles, the request ages
 * out, or the user cancels.
 */
async function waitForPairing(host, requestId) {
  const controller = new AbortController();
  waits.set(requestId, { controller, host });
  // Slightly past the daemon's 2-minute request TTL: if we are still asking
  // by then, the answer is "nobody came".
  const deadline = Date.now() + 150_000;
  try {
    while (Date.now() < deadline) {
      let body;
      try {
        const res = await fetch(
          `http://${normalizeHost(host)}/pair/wait?id=${encodeURIComponent(requestId)}`,
          { signal: controller.signal },
        );
        if (!res.ok) return { status: 'error', error: `HTTP ${res.status}` };
        body = await res.json();
      } catch (err) {
        if (controller.signal.aborted) return { status: 'canceled' };
        // A dropped poll is not a decision — the host may just have gone to
        // sleep mid-wait. Back off briefly and ask again.
        await new Promise((r) => setTimeout(r, 1000));
        continue;
      }
      if (body.status && body.status !== 'pending') return body;
    }
    return { status: 'expired' };
  } finally {
    waits.delete(requestId);
  }
}

/**
 * Gives up on a request and tells the host to drop it, so the dialog over
 * there disappears instead of waiting out its two-minute expiry. Best-effort:
 * if the host cannot be reached the request simply expires as before.
 */
function cancelPairing(host, requestId) {
  const wait = waits.get(requestId);
  wait?.controller.abort();
  const target = host ?? wait?.host;
  if (!target) return false;
  void fetch(`http://${normalizeHost(target)}/pair/withdraw`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ requestId }),
    signal: AbortSignal.timeout(3000),
  }).catch(() => {
    /* the host will expire it on its own */
  });
  return true;
}

module.exports = {
  DEFAULT_PORT,
  normalizeHost,
  startAdvertising,
  stopAdvertising,
  closeDiscovery,
  scanForHosts,
  probeHello,
  requestPairing,
  waitForPairing,
  cancelPairing,
};
