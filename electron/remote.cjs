// Asking another machine to let us in.
//
// Finding machines lives in the daemon (internal/discovery), not here: mDNS
// needs a listening socket, and on Windows every program that opens one gets
// its own firewall prompt. Keeping it in one program means one rule to allow.
//
// What stays here is outbound-only, so it needs no firewall rule of its own —
// and it has to be here rather than in the renderer, because the daemon's
// pairing endpoints send no CORS headers on purpose. A native client is the
// only thing that can drive them.

// The daemon's default remote port, for a host typed without one.
const DEFAULT_PORT = 8482;

/** "host" or "host:port" → "host:port" (the daemon's remote default port). */
const normalizeHost = (host) => {
  const h = String(host).trim().replace(/^https?:\/\//, '').replace(/\/+$/, '');
  // A lone hostname/IPv4 gets the default port; IPv6 literals and host:port
  // pass through.
  return /^[^:]+$/.test(h) ? `${h}:${DEFAULT_PORT}` : h;
};

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
  requestPairing,
  waitForPairing,
  cancelPairing,
};
