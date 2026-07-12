// Drives the lastSeenVersion setting behind the Welcome "What's new" card
// against a running `marrawd --dev --port 8483`.
//
//   node scripts/changelog-verify.mjs set 0.0.1   # seed an old version
//   node scripts/changelog-verify.mjs set ""      # fresh-install state
//   node scripts/changelog-verify.mjs get         # print the stored value
//
// The GUI half lives in shot.renderer.js (`welcome` surface): seed, shoot,
// then `get` again to confirm the mount marked the current version seen.

const [, , cmd, value] = process.argv;
if (cmd !== 'set' && cmd !== 'get') {
  console.error('usage: node scripts/changelog-verify.mjs set <version> | get');
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
    }, 30_000);
  });
}

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

if (cmd === 'set') {
  await call('Settings.SetLastSeenVersion', [value ?? '']);
  const s = await call('Settings.GetUISettings', []);
  console.log(`lastSeenVersion = ${JSON.stringify(s.lastSeenVersion)}`);
  if (s.lastSeenVersion !== (value ?? '')) {
    console.error('FAIL: readback mismatch');
    process.exit(1);
  }
} else {
  const s = await call('Settings.GetUISettings', []);
  console.log(`lastSeenVersion = ${JSON.stringify(s.lastSeenVersion)}`);
}
ws.close();
