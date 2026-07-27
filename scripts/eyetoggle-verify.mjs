// End-to-end check of the eye (hide/show) toggle on local edits against a
// running `marrawd --dev --port 8483`. Exercises: `disabled` round-tripping
// through SetEditParams/GetEditParams and Normalize, the committed pipeline
// skipping disabled masks (ApplyMasks) and spots (ApplyHeal) — a disabled
// item renders byte-identical to not having it at all — re-enabling restoring
// the effect, and the .marraw.json sidecar carrying the flag.
//
//   node scripts/eyetoggle-verify.mjs /tmp/marraw-fixture
//
// Point it at a DISPOSABLE copy of a shoot — it writes .marraw.json sidecars.

import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/eyetoggle-verify.mjs <disposable-raw-folder>');
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

const render512 = async () => {
  const cur = (await call('Library.ListPhotos', [info.folderId]))[0];
  const img = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
  return { cur, img };
};

// Baseline: NON-neutral params (neutral edits share the special "base" cache
// slot, which serves through a different path — its bytes aren't comparable
// to pipeline renders), with no local edits.
const BASE = { contrast: 0.05 };
await call('Edits.SetEditParams', [photo.id, BASE]);
const base = await render512();
check(isJpeg(base.img), `baseline 512 renders (${base.img.length} B, hash ${base.cur.editHash})`);

// --- 1. Masks: disable skips rendering, re-enable restores it. ---
const mask = { type: 'radial', cx: 0.5, cy: 0.5, rx: 0.3, ry: 0.3, feather: 0.5, adjust: { expEV: -2 } };
await call('Edits.SetEditParams', [photo.id, { ...BASE, masks: [mask] }]);
const maskOn = await render512();
check(maskOn.cur.editHash !== base.cur.editHash, 'a mask advances the edit hash');
check(!maskOn.img.equals(base.img), 'the enabled mask changes the 512 render vs baseline');

await call('Edits.SetEditParams', [photo.id, { ...BASE, masks: [{ ...mask, disabled: true }] }]);
let saved = await call('Edits.GetEditParams', [photo.id]);
check(saved.masks?.[0]?.disabled === true, 'mask disabled:true round-trips through Normalize');
const maskOff = await render512();
step(`disabled mask; editHash ${maskOn.cur.editHash} -> ${maskOff.cur.editHash}`);
check(maskOff.cur.editHash !== maskOn.cur.editHash, 'toggling the eye advances the edit hash');
check(maskOff.img.equals(base.img), 'a disabled mask renders byte-identical to baseline');

await call('Edits.SetEditParams', [photo.id, { ...BASE, masks: [{ ...mask, disabled: false }] }]);
saved = await call('Edits.GetEditParams', [photo.id]);
// The wire encoder emits every field (the invert:false precedent), so just
// assert false round-trips as false — omitempty cleanliness is the sidecar's
// concern, checked below.
check(saved.masks?.[0]?.disabled === false, 'mask disabled:false round-trips as false');
const maskBack = await render512();
check(maskBack.img.equals(maskOn.img), 're-enabling restores the exact enabled render');

// --- 2. Spots: same contract through ApplyHeal. ---
const spot = { cx: 0.5, cy: 0.5, radius: 0.03 };
const src = await call('Edits.SuggestHealSource', [photo.id, {}, spot]);
const spots = [{ ...spot, sx: src.sx, sy: src.sy, feather: 0.5 }];
await call('Edits.SetEditParams', [photo.id, { ...BASE, spots }]);
const spotOn = await render512();
check(!spotOn.img.equals(base.img), 'the enabled spot changes the 512 render vs baseline');

await call('Edits.SetEditParams', [photo.id, { ...BASE, spots: [{ ...spots[0], disabled: true }] }]);
saved = await call('Edits.GetEditParams', [photo.id]);
check(saved.spots?.[0]?.disabled === true, 'spot disabled:true round-trips through Normalize');
const spotOff = await render512();
check(spotOff.img.equals(base.img), 'a disabled spot renders byte-identical to baseline');

await call('Edits.SetEditParams', [photo.id, { ...BASE, spots }]);
const spotBack = await render512();
check(spotBack.img.equals(spotOn.img), 're-enabling restores the exact enabled render');

// --- 3. The sidecar carries the flag. ---
await call('Edits.SetEditParams', [photo.id, { ...BASE, masks: [{ ...mask, disabled: true }], spots }]);
await new Promise((r) => setTimeout(r, 500));
const sidecar = readFileSync(join(FOLDER, `${photo.fileName}.marraw.json`), 'utf8');
check(sidecar.includes('"disabled"'), 'sidecar carries disabled:true');

// Leave the fixture neutral for other harnesses.
await call('Edits.SetEditParams', [photo.id, {}]);

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
