// End-to-end check that edit geometry (rotate/cropW/cropH) reaches the grid:
// the Photo list carries it, and every edit save pushes it on the folder
// subscription patch — including explicit zeros on reset — so natural-layout
// cells can reshape live (the "cropped thumb keeps its old aspect" fix).
//
//   node scripts/cropaspect-verify.mjs <disposable-raw-folder> [apply|reset]
//
// `apply` (default) leaves a crop on photo #2 and a quarter turn on photo #3
// so a shot.mjs run can capture the reshaped natural grid; `reset` clears
// them and asserts the zero-geometry patch.

const FOLDER = process.argv[2];
const PHASE = process.argv[3] || 'apply';
if (!FOLDER) {
  console.error('usage: node scripts/cropaspect-verify.mjs <disposable-raw-folder> [apply|reset]');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
const patches = [];

ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  } else if (msg.type === 'subscription_patch') {
    patches.push(msg);
  }
};

function send(kind, method, params, extra) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ type: kind, id, method, params, ...extra }));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout: ${method}`));
      }
    }, 60_000);
  });
}
const call = (method, params) => send('request', method, params);
const subscribe = (method, params) => send('subscribe', method, params, { patch: true });

async function waitFor(name, fn, ms = 5_000) {
  const t = Date.now();
  for (;;) {
    const v = fn();
    if (v) return v;
    if (Date.now() - t > ms) throw new Error(`timeout waiting for ${name}`);
    await new Promise((s) => setTimeout(s, 50));
  }
}

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await subscribe('Library.ListPhotos', [info.folderId]);
check(photos.length >= 3, `folder has >= 3 photos (${photos.length})`);
const [, cropP, rotP] = photos;

const geomPatch = (id) =>
  patches
    .map((m) => m.patch?.patches?.find((x) => x.id === id && x.editHash != null))
    .find(Boolean);

if (PHASE === 'apply') {
  // Untouched (seeded-look) photos must carry neutral geometry in the list.
  check(
    photos.every((p) => !p.rotate && !p.cropW && !p.cropH),
    'unedited photos list neutral geometry',
  );

  // Crop photo #2 to the left half; the patch must carry the fractions.
  const base = (await call('Edits.GetEditParams', [cropP.id])) ?? {};
  await call('Edits.SetEditParams', [cropP.id, { ...base, cropX: 0, cropY: 0, cropW: 0.5, cropH: 1 }]);
  const cp = await waitFor('crop patch', () => geomPatch(cropP.id));
  check(cp.cropW === 0.5 && cp.cropH === 1 && cp.rotate === 0, 'crop commit patches cropW/cropH');

  // Quarter-turn photo #3; the patch must carry rotate with zero crop.
  const base3 = (await call('Edits.GetEditParams', [rotP.id])) ?? {};
  await call('Edits.SetEditParams', [rotP.id, { ...base3, rotate: 1 }]);
  const rp = await waitFor('rotate patch', () => geomPatch(rotP.id));
  check(rp.rotate === 1 && !rp.cropW && !rp.cropH, 'rotate commit patches rotate');

  // A fresh list re-read serves the same geometry (the ListPhotos path).
  const relisted = await call('Library.ListPhotos', [info.folderId]);
  const rc = relisted.find((p) => p.id === cropP.id);
  const rr = relisted.find((p) => p.id === rotP.id);
  check(rc.cropW === 0.5 && rc.cropH === 1, 'ListPhotos serves crop geometry');
  check(rr.rotate === 1, 'ListPhotos serves rotate geometry');
} else {
  await call('Edits.ResetEdits', [[cropP.id, rotP.id]]);
  const cp = await waitFor('crop reset patch', () => geomPatch(cropP.id));
  const rp = await waitFor('rotate reset patch', () => geomPatch(rotP.id));
  // Explicit zeros, not nulls — null means "unchanged" to the patch reducer.
  check(cp.cropW === 0 && cp.cropH === 0 && cp.rotate === 0, 'reset patches explicit zero crop');
  check(rp.rotate === 0, 'reset patches explicit zero rotate');
}

ws.close();
console.log(failures ? `FAILED (${failures})` : 'OK');
process.exit(failures ? 1 : 0);
