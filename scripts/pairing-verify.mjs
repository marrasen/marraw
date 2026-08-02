// Wire probe for remote discovery + pairing: the unauthenticated /hello and
// /pair/* endpoints, the local-only approval RPCs, per-device tokens, and
// revocation.
//
// Unlike the other verify scripts this one starts its OWN daemon — the flow
// only exists on a daemon that is reachable from another machine, so `--dev`
// (loopback-only by construction) cannot exercise it at all. It runs the
// daemon twice: once on 0.0.0.0 to drive the whole flow, and once on
// 127.0.0.1 to prove the endpoints are absent there.
//
//   node scripts/pairing-verify.mjs

import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const BIN = join(process.cwd(), 'build', process.platform === 'win32' ? 'marrawd.exe' : 'marrawd');
if (!existsSync(BIN)) {
  console.error(`missing ${BIN} — run: node scripts/build-server.mjs`);
  process.exit(1);
}

const PORT = 8489;
const LAUNCH_TOKEN = 'launch-token-for-the-pairing-probe';

const results = {};
const check = (name, ok, detail = '') => {
  results[name] = ok;
  console.log(`  ${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` (${detail})` : ''}`);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** Starts marrawd on `listen:port` against a throwaway data dir. */
async function startDaemon(listen, port, dataDir) {
  const child = spawn(BIN, ['--port', String(port), '--listen', listen, '--data-dir', dataDir], {
    env: { ...process.env, MARRAW_TOKEN: LAUNCH_TOKEN },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  child.stderr.on('data', (d) => process.env.VERBOSE && console.error(`[marrawd] ${d}`));
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('daemon not ready in 15s')), 15_000);
    child.stdout.on('data', (d) => {
      if (/MARRAW_READY port=\d+/.test(String(d))) {
        clearTimeout(timer);
        resolve();
      }
    });
    child.on('error', reject);
  });
  return child;
}

/** One authenticated WS client, with call/subscribe helpers. */
async function connect(token) {
  const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
  let nextId = 1;
  const pending = new Map();
  const subs = new Map();
  const client = { ws, closed: false, authError: null };

  ws.onclose = () => {
    client.closed = true;
  };
  ws.onmessage = (ev) => {
    if (typeof ev.data !== 'string') return;
    const msg = JSON.parse(ev.data);
    if (msg.type === 'auth_error') client.authError = msg.message ?? 'rejected';
    if (msg.type === 'response') {
      const sub = subs.get(msg.id);
      if (sub) {
        sub.pushes.push(msg.result);
        return;
      }
      pending.get(msg.id)?.resolve(msg.result);
      pending.delete(msg.id);
    } else if (msg.type === 'error') {
      pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
      pending.delete(msg.id);
    }
  };

  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = () => reject(new Error(`cannot connect to :${PORT}`));
  });
  ws.send(JSON.stringify({ type: 'auth', token }));
  // Give the server a moment to accept or reject before anything is asserted.
  await sleep(250);

  client.call = (method, params = []) => {
    const id = String(nextId++);
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      ws.send(JSON.stringify({ type: 'request', id, method, params }));
      setTimeout(() => {
        if (pending.has(id)) {
          pending.delete(id);
          reject(new Error(`timeout: ${method}`));
        }
      }, 20_000);
    });
  };
  client.subscribe = (method, params = []) => {
    const id = String(nextId++);
    const handle = { id, pushes: [] };
    subs.set(id, handle);
    ws.send(JSON.stringify({ type: 'subscribe', id, method, params }));
    return handle;
  };
  return client;
}

const post = (path, body, headers = { 'Content-Type': 'application/json' }) =>
  fetch(`http://127.0.0.1:${PORT}${path}`, { method: 'POST', headers, body: JSON.stringify(body) });
const get = (path) => fetch(`http://127.0.0.1:${PORT}${path}`);

const dataDir = mkdtempSync(join(tmpdir(), 'marraw-pairing-'));
let daemon = await startDaemon('0.0.0.0', PORT, dataDir);

try {
  // ---- discovery ----
  const hello = await get('/hello').then((r) => r.json());
  check('hello identifies the daemon', hello.app === 'marraw' && !!hello.name, `name=${hello.name}`);
  check('hello reports pairing open', hello.pairing === true);
  check('authz still needs a token', (await get('/authz')).status === 403);

  // ---- a browser must not be able to drive pairing ----
  const formPost = await post('/pair/request', { name: 'X' }, { 'Content-Type': 'text/plain' });
  check('pair/request rejects non-JSON content types', formPost.status === 415, `${formPost.status}`);
  check(
    'pair endpoints send no CORS headers',
    (await get('/hello')).headers.get('access-control-allow-origin') === null,
  );

  // ---- the happy path ----
  const local = await connect(LAUNCH_TOKEN);
  check('launch token authenticates', !local.closed && !local.authError);

  const pendingSub = local.subscribe('System.ListPairingRequests');
  await sleep(200);

  const req = await post('/pair/request', { name: "Marcus's laptop", platform: 'darwin' }).then(
    (r) => r.json(),
  );
  check('pair/request returns an id and a code', !!req.requestId && /^\d{4}$/.test(req.code));

  // The dialog opens because the subscription pushed, not because anything
  // polled — that is the mechanism the UI depends on.
  await sleep(400);
  const pushed = pendingSub.pushes.at(-1) ?? [];
  check(
    'local window is pushed the pending request',
    pushed.length === 1 && pushed[0].id === req.requestId && pushed[0].code === req.code,
    `${pushed.length} pending`,
  );
  check('request carries the peer address', !!pushed[0]?.addr, pushed[0]?.addr);

  // A remote client — even a valid one — must never see or answer requests.
  const pairingTok = (await local.call('System.GetRemoteAccess')).pairingToken;
  const remote = await connect(pairingTok);
  check('pairing token authenticates', !remote.closed && !remote.authError);
  const remoteView = await remote.call('System.ListPairingRequests');
  check('remote client sees no pending requests', remoteView.length === 0, `${remoteView.length}`);
  let remoteDenied = false;
  await remote
    .call('System.ResolvePairing', [req.requestId, true])
    .catch(() => (remoteDenied = true));
  check('remote client cannot approve a request', remoteDenied);

  // Wait BEFORE approving, the way the shell does.
  const waiting = get(`/pair/wait?id=${req.requestId}`).then((r) => r.json());
  await local.call('System.ResolvePairing', [req.requestId, true]);
  const decision = await waiting;
  check(
    'approval hands back a device token',
    decision.status === 'approved' && /^[0-9a-f]{32}$/.test(decision.token ?? ''),
    decision.status,
  );

  await sleep(300);
  check('the dialog closes after approval', (pendingSub.pushes.at(-1) ?? []).length === 0);

  // ---- the minted token really works ----
  const device = await connect(decision.token);
  check('device token authenticates over WS', !device.closed && !device.authError);
  check(
    'device token authorizes HTTP',
    (await get(`/authz?t=${decision.token}`)).status === 200,
  );
  const deviceView = await device.call('System.ListPairingRequests');
  check('an approved device is still not local', deviceView.length === 0);

  const devices = await local.call('System.ListRemoteDevices');
  check('device is listed', devices.length === 1 && devices[0].name === "Marcus's laptop");
  check(
    'listed device carries no token',
    devices.length === 1 && !('token' in devices[0]),
    Object.keys(devices[0] ?? {}).join(','),
  );

  // ---- limits ----
  const codes = [];
  for (let i = 0; i < 4; i++) {
    codes.push((await post('/pair/request', { name: `Spammer ${i}` })).status);
  }
  // All four come from 127.0.0.1, so each supersedes the last rather than
  // stacking: the per-IP rule, not the cap, is what is being proved here.
  check('repeat requests from one address do not stack', codes.every((s) => s === 200), codes.join(','));
  const stillPending = await local.call('System.ListPairingRequests');
  check('only one request pending from one address', stillPending.length === 1, `${stillPending.length}`);
  for (const p of stillPending) await local.call('System.ResolvePairing', [p.id, false]);

  const denied = await post('/pair/request', { name: 'Denied' }).then((r) => r.json());
  const deniedWait = await get(`/pair/wait?id=${denied.requestId}`).then((r) => r.json());
  // Nothing has answered it yet, so a first poll must report it still waiting.
  check('an unanswered request reports pending', deniedWait.status === 'pending', deniedWait.status);
  await local.call('System.ResolvePairing', [denied.requestId, false]);
  const afterDeny = await get(`/pair/wait?id=${denied.requestId}`).then((r) => r.json());
  check('a denial carries no token', afterDeny.status === 'denied' && !afterDeny.token, afterDeny.status);

  check(
    'an unknown request id is not approvable',
    (await get('/pair/wait?id=deadbeef').then((r) => r.json())).status === 'expired',
  );

  // ---- closing the door ----
  await local.call('System.SetPairingOpen', [false]);
  const shut = await post('/pair/request', { name: 'Too late' });
  check('a closed daemon refuses new requests', shut.status === 403, `${shut.status}`);
  check('hello advertises that it is closed', (await get('/hello').then((r) => r.json())).pairing === false);
  check(
    'an approved device still works while closed',
    (await get(`/authz?t=${decision.token}`)).status === 200,
  );
  await local.call('System.SetPairingOpen', [true]);

  // ---- renaming ----
  await local.call('System.SetDeviceName', ['Studio desktop']);
  check(
    'hello reports the chosen name',
    (await get('/hello').then((r) => r.json())).name === 'Studio desktop',
  );

  // ---- revocation ----
  await local.call('System.RevokeRemoteDevice', [devices[0].id]);
  check('revoked token stops authorizing', (await get(`/authz?t=${decision.token}`)).status === 403);
  await sleep(500);
  check('revoked device is disconnected', device.closed);
  check('other credentials survive a revoke', (await get(`/authz?t=${pairingTok}`)).status === 200);
  check('device list is empty again', (await local.call('System.ListRemoteDevices')).length === 0);

  // ---- devices survive a restart ----
  const survivor = await post('/pair/request', { name: 'Persistent laptop' }).then((r) => r.json());
  const survivorWait = get(`/pair/wait?id=${survivor.requestId}`).then((r) => r.json());
  const survivorPending = await local.call('System.ListPairingRequests');
  await local.call('System.ResolvePairing', [survivorPending[0].id, true]);
  const survivorToken = (await survivorWait).token;

  daemon.kill();
  await sleep(800);
  daemon = await startDaemon('0.0.0.0', PORT, dataDir);
  check(
    'an approved device still works after a restart',
    (await get(`/authz?t=${survivorToken}`)).status === 200,
  );

  // ---- loopback-only daemons must not serve any of this ----
  daemon.kill();
  await sleep(800);
  daemon = await startDaemon('127.0.0.1', PORT, dataDir);
  check('loopback daemon does not serve /hello', (await get('/hello')).status === 404);
  check(
    'loopback daemon does not serve /pair/request',
    (await post('/pair/request', { name: 'X' })).status === 404,
  );
  check('loopback daemon still serves /healthz', (await get('/healthz')).status === 200);
} finally {
  daemon?.kill();
  await sleep(300);
  rmSync(dataDir, { recursive: true, force: true });
}

const failed = Object.entries(results).filter(([, ok]) => !ok);
console.log(`\n${Object.keys(results).length - failed.length}/${Object.keys(results).length} passed`);
if (failed.length) {
  console.log(`FAILED: ${failed.map(([n]) => n).join(', ')}`);
  process.exit(1);
}
