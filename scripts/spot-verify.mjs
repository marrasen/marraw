// End-to-end check of the spot-removal / healing feature against a running
// `marrawd --dev --port 8483`. Exercises: SuggestHealSource determinism +
// placement, the committed render pipeline (ApplyHeal) at a downscaled level
// AND a 1:1 tile, edit-hash advance, and the .marraw.json sidecar carrying
// spots.
//
//   node scripts/spot-verify.mjs /tmp/marraw-fixture
//
// Point it at a DISPOSABLE copy of a shoot — it writes .marraw.json sidecars.

import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/spot-verify.mjs <disposable-raw-folder>');
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

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const step = (name) => console.log(name);

// Fetch an /img rendition and return the raw bytes (throws on non-200).
async function fetchImg(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`GET ${url} -> ${res.status}`);
  return Buffer.from(await res.arrayBuffer());
}
const isJpeg = (b) => b.length > 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff;

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
step(`OpenFolder -> ${photos.length} photos`);
if (photos.length < 1) throw new Error('need at least 1 RAW in the fixture');
const photo = photos[0];
const idOf = (p) => `${p.cacheKey}`;

// Baseline render (neutral) so we can prove the spot actually changes pixels.
await call('Edits.SetEditParams', [photo.id, {}]);
let cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const baseHash = cur.editHash;
const base512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${baseHash}`);
check(isJpeg(base512), `baseline 512 renders (${base512.length} B, hash ${baseHash})`);

// --- 1. SuggestHealSource: deterministic, in-frame, clear of the spot. ---
const spot = { cx: 0.5, cy: 0.5, radius: 0.03 };
const s1 = await call('Edits.SuggestHealSource', [photo.id, {}, spot]);
const s2 = await call('Edits.SuggestHealSource', [photo.id, {}, spot]);
step(`SuggestHealSource -> (${s1.sx.toFixed(4)}, ${s1.sy.toFixed(4)})`);
check(s1.sx === s2.sx && s1.sy === s2.sy, 'SuggestHealSource is deterministic');
check(s1.sx >= 0 && s1.sx <= 1 && s1.sy >= 0 && s1.sy <= 1, 'suggested source is in-frame');
check(
  Math.hypot(s1.sx - spot.cx, s1.sy - spot.cy) >= 2 * spot.radius,
  'suggested source clears the destination disc',
);

// --- 2. Commit a spot; the edit hash must advance. ---
const spots = [{ ...spot, sx: s1.sx, sy: s1.sy, feather: 0.5 }];
await call('Edits.SetEditParams', [photo.id, { spots }]);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
step(`committed spot; editHash ${baseHash} -> ${cur.editHash}`);
check(cur.editHash !== baseHash, 'a spot advances the edit hash');
check(idOf(cur) === idOf(photo), 'cacheKey is unchanged (spots are post-decode)');

// --- 3. The committed pipeline heals at a downscaled level AND a 1:1 tile. ---
const spot512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(spot512), `spot 512 renders (${spot512.length} B)`);
check(!spot512.equals(base512), 'the spot changes the 512 render vs baseline');
const tile = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/tile/0/0?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(tile), `spot 1:1 tile (0,0) renders (${tile.length} B)`);

// --- 4. A clone spot renders too (the other mode). ---
await call('Edits.SetEditParams', [photo.id, { spots: [{ ...spots[0], mode: 'clone' }] }]);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const clone512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(clone512), 'clone-mode 512 renders');
check(!clone512.equals(spot512), 'clone differs from heal (mode is honored)');

// --- 5. The .marraw.json sidecar carries the spots. ---
// Small settle so the sidecar write lands.
await new Promise((r) => setTimeout(r, 500));
const sidecar = readFileSync(join(FOLDER, `${photo.fileName}.marraw.json`), 'utf8');
check(sidecar.includes('"spots"'), 'sidecar carries the spots array');

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
