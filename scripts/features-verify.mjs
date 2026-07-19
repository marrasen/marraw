// End-to-end check of the feature-toggle settings (Settings → Features)
// against a running `marrawd --dev --port 8483`. Exercises: the features map
// on GetUISettings, SetFeature persisting an explicit override, unknown ids
// round-tripping untouched, and the empty-id rejection.
//
//   node scripts/features-verify.mjs

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

let failed = false;
function check(name, ok, detail = '') {
  console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${detail ? ` — ${detail}` : ''}`);
  if (!ok) failed = true;
}

await new Promise((r) => (ws.onopen = r));

const before = await call('Settings.GetUISettings', []);
check('features map served', typeof before.features === 'object' && before.features !== null);

// Explicit override persists and echoes back.
await call('Settings.SetFeature', ['suggestions', true]);
let s = await call('Settings.GetUISettings', []);
check('SetFeature(suggestions, true) persisted', s.features.suggestions === true);

// Unknown ids are stored verbatim — the server never interprets them.
await call('Settings.SetFeature', ['from-a-newer-build', false]);
s = await call('Settings.GetUISettings', []);
check('unknown id round-trips', s.features['from-a-newer-build'] === false);

// Empty id is rejected.
const err = await call('Settings.SetFeature', ['', true]).then(
  () => null,
  (e) => e,
);
check('empty id rejected', err != null, err?.message ?? 'no error');

// Restore the as-found state for both keys touched above.
await call('Settings.SetFeature', ['suggestions', before.features.suggestions ?? false]);
s = await call('Settings.GetUISettings', []);
check('restore', s.features.suggestions === (before.features.suggestions ?? false));

ws.close();
process.exit(failed ? 1 : 0);
