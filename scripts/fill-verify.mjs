// End-to-end check of ML content-aware fill against a running
// `marrawd --dev --port 8483`. Exercises: the fill spot mode surviving
// Normalize (source reference zeroed), the no-patch render contract
// (composites nothing), the consent gate (GenerateFill refuses without
// allowDownload after the model is deleted), the real download + inference,
// the fast-path idempotence, cache invalidation (same edit hash, new
// pixels), re-keying on a decode change, and the sidecar carrying the mode.
//
//   node scripts/fill-verify.mjs /tmp/marraw-fixture
//
// Point it at a DISPOSABLE copy of a shoot — it writes .marraw.json sidecars.
// NOTE: deletes and re-downloads the migan model (~28 MB) to prove the
// consent gate; needs network to the marraw-models GitHub release.

import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/fill-verify.mjs <disposable-raw-folder>');
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
    }, 300_000);
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

// Baseline render (neutral) to prove the fill actually changes pixels.
await call('Edits.SetEditParams', [photo.id, {}]);
let cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const baseHash = cur.editHash;
const base512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${baseHash}`);
check(isJpeg(base512), `baseline 512 renders (${base512.length} B)`);

// A same-path reference: the base render (nil edits) takes a different
// pipeline (camera-mimic seeding), so the composites-nothing check must
// compare two PARAMS renders that differ only in the spot.
const marker = { contrast: 0.05 };
await call('Edits.SetEditParams', [photo.id, marker]);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const plain512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);

// Per-run jitter: fill patches persist on disk keyed by the spot's geometry,
// so a repeated run against the same data dir would fast-path everywhere
// (including the consent check) with the previous run's patches. A fresh
// center per run keeps the generate paths honest. Steps of 1e-4 survive the
// server's quant4 quantization.
const j = (Date.now() % 400) / 10000; // 0 .. 0.0399
const jc = (v) => Math.round((v + j) * 1e4) / 1e4;

// --- 1. A fill spot survives Normalize; its source reference zeroes. ---
const params = {
  ...marker,
  spots: [{ mode: 'fill', cx: jc(0.45), cy: jc(0.48), radius: 0.05, sx: 0.62, sy: 0.55, feather: 0.5 }],
};
await call('Edits.SetEditParams', [photo.id, params]);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const saved = await call('Edits.GetEditParams', [photo.id]);
step(`committed fill spot; editHash ${baseHash} -> ${cur.editHash}`);
check(cur.editHash !== baseHash, 'a fill spot advances the edit hash');
check(saved.spots?.[0]?.mode === 'fill', 'fill mode survives Normalize');
check(!saved.spots[0].sx && !saved.spots[0].sy, 'fill source reference zeroes in Normalize');

// --- 2. Without a patch the spot composites nothing. ---
const noPatch512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(noPatch512), 'patchless fill 512 renders');
check(noPatch512.equals(plain512), 'a patchless fill composites nothing (matches the same edit sans spot)');

// --- 3. Consent gate: delete the model, GenerateFill must refuse. ---
const st0 = await call('Edits.FillModelStatus', []);
step(`FillModelStatus -> downloaded=${st0.downloaded} bytes=${st0.bytes}`);
if (st0.downloaded) {
  await call('System.DeleteModel', ['migan-1.onnx']);
}
const stGone = await call('Edits.FillModelStatus', []);
check(!stGone.downloaded, 'model reads as not downloaded after delete');
let refused = false;
try {
  await call('Edits.GenerateFill', [photo.id, saved, 0, false]);
} catch (err) {
  refused = String(err.message).includes('model not downloaded');
}
check(refused, 'GenerateFill without consent refuses with the sentinel');

// --- 4. With consent: download (hash-pinned), inpaint, cache. ---
step('GenerateFill with allowDownload (downloads ~28 MB + one inference)...');
const gen = await call('Edits.GenerateFill', [photo.id, saved, 0, true]);
check(gen.fillVer === 'migan-1', `fill version tag is migan-1 (got ${gen.fillVer})`);
check(gen.generated === true, 'first GenerateFill reports generated');
const again = await call('Edits.GenerateFill', [photo.id, saved, 0, false]);
check(again.generated === false, 'second GenerateFill is a fast-path no-op');

// --- 5. Same edit hash, new pixels: the cache was invalidated. ---
const filled512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(filled512), `filled 512 renders (${filled512.length} B)`);
check(!filled512.equals(noPatch512), 'the generated patch changes the render at the SAME edit hash');
const tile = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/tile/0/0?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(tile), `filled 1:1 tile (0,0) renders (${tile.length} B)`);

// --- 6. A decode change re-keys the patch (WB shifts the input pixels). ---
const warm = { ...saved, wbTemp: 0.3 };
const rekeyed = await call('Edits.GenerateFill', [photo.id, warm, 0, false]);
check(rekeyed.generated === true, 'a WB change re-keys and regenerates the patch');
const rekeyedAgain = await call('Edits.GenerateFill', [photo.id, warm, 0, false]);
check(rekeyedAgain.generated === false, 'the re-keyed patch is cached in turn');

// --- 7. A brush-region fill generates too. ---
const strokeFill = {
  spots: [{
    kind: 'stroke', mode: 'fill', cx: 0.5, cy: 0.5, radius: 0, sx: 0, sy: 0,
    strokes: [{ radius: 0.02, feather: 0.4, pts: [jc(0.4), 0.5, jc(0.48), 0.52, jc(0.56), 0.5] }],
  }],
};
const strokeGen = await call('Edits.GenerateFill', [photo.id, strokeFill, 0, false]);
check(strokeGen.generated === true, 'a stroke-region fill generates');

// --- 8. The sidecar carries the fill mode. ---
await new Promise((r) => setTimeout(r, 500));
const sidecar = readFileSync(join(FOLDER, `${photo.fileName}.marraw.json`), 'utf8');
check(sidecar.includes('"fill"'), 'sidecar carries the fill mode');

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
