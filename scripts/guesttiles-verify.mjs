// Opens the fixture folder and mints a share link, so the guest page can be
// driven in a browser. Prints the link URL. Run against a NON-dev daemon
// (--dev skips OnAuth, and the guest's token frame never matches).
//
//   MARRAW_TOKEN=x go run ./cmd/marrawd --port 8483
//   node scripts/guesttiles-verify.mjs /tmp/marraw-fixture [--revoke]

const FOLDER = process.argv[2];
const REVOKE = process.argv.includes('--revoke');
if (!FOLDER) {
  console.error('usage: node scripts/guesttiles-verify.mjs <raw-folder> [--revoke]');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();

ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  }
};

function call(method, params) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ type: 'request', id, method, params }));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout: ${method}`));
      }
    }, 120_000);
  });
}

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});
ws.send(JSON.stringify({ type: 'auth', token: process.env.MARRAW_TOKEN ?? 'verifytoken' }));
await new Promise((r) => setTimeout(r, 500));

const info = await call('Library.OpenFolder', [FOLDER]);
console.log(`OpenFolder -> ${(info.photos ?? info).length ?? '?'} photos`);

if (REVOKE) {
  const links = await call('Share.ListLinks', []);
  for (const l of links) await call('Share.RevokeLink', [l.id]);
  console.log(`revoked ${links.length} link(s)`);
  ws.close();
  process.exit(0);
}

const link = await call('Share.CreateLink', [
  FOLDER,
  { cull: true, edits: false, downloads: true },
  24,
  '',
]);
console.log('LINK ' + JSON.stringify(link));
ws.close();
