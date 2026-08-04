// End-to-end check of mask-driven ML removal against a running
// `marrawd --dev --port 8483`. Exercises: the Remove flag surviving Normalize
// on eligible types and being cleared on the rest, the no-patch render
// contract (composites nothing), the consent gate (GenerateMaskFill refuses
// without allowDownload), real inference, fast-path idempotence, cache
// invalidation at an unchanged edit hash, the composite-only fields (feather,
// adjust) NOT re-keying the patch, a region change re-keying it, the area cap,
// and the sidecar carrying the flag.
//
//   node scripts/maskremove-verify.mjs /tmp/marraw-fixture
//
// Point it at a DISPOSABLE copy of a shoot — it writes .marraw.json sidecars.
// Brush masks carry the main path so the check works on any fixture; the
// subject mask covers the AI path (it always produces a matte, unlike person
// detection on a landscape).

import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/maskremove-verify.mjs <disposable-raw-folder>');
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

// Per-run jitter: patches persist on disk keyed by the region, so a repeated
// run against the same data dir would fast-path everywhere (including the
// consent check) with the previous run's patches. Steps of 1e-4 survive the
// server's quant4 quantization.
const j = (Date.now() % 400) / 10000; // 0 .. 0.0399
const jc = (v) => Math.round((v + j) * 1e4) / 1e4;

const marker = { contrast: 0.05 };
const brushStrokes = [{ radius: 0.04, feather: 0.4, pts: [jc(0.42), 0.5, jc(0.5), 0.52, jc(0.58), 0.5] }];
const brushMask = (extra = {}) => ({
  type: 'brush', strokes: brushStrokes, remove: true, adjust: {}, ...extra,
});

// --- 1. Remove survives Normalize where it's allowed, clears where not. ---
const eligible = { ...marker, masks: [brushMask()] };
await call('Edits.SetEditParams', [photo.id, eligible]);
let cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const saved = await call('Edits.GetEditParams', [photo.id]);
check(saved.masks?.[0]?.remove === true, 'remove survives Normalize on a brush mask');

// A same-path reference: the base render (nil edits) takes a different
// pipeline, so "composites nothing" must compare two PARAMS renders that
// differ only in the removal.
const plainMasks = [{ ...brushMask(), remove: undefined }];
await call('Edits.SetEditParams', [photo.id, { ...marker, masks: plainMasks }]);
const plainCur = (await call('Library.ListPhotos', [info.folderId]))[0];
const plain512 = await fetchImg(
  `http://127.0.0.1:8483/img/${photo.id}/512?v=${plainCur.cacheKey}&e=${plainCur.editHash}`);
check(plainCur.editHash !== cur.editHash, 'toggling remove advances the edit hash');

for (const [name, mask] of [
  ['radial', { type: 'radial', cx: 0.5, cy: 0.5, rx: 0.3, ry: 0.2, remove: true, adjust: {} }],
  ['linear', { type: 'linear', x0: 0, y0: 0, x1: 1, y1: 1, remove: true, adjust: {} }],
  ['range', { type: 'range', rangeLumaLo: 0.2, rangeLumaHi: 0.8, remove: true, adjust: {} }],
]) {
  await call('Edits.SetEditParams', [photo.id, { ...marker, masks: [mask] }]);
  const back = await call('Edits.GetEditParams', [photo.id]);
  check(!back.masks?.[0]?.remove, `remove clears on a ${name} mask`);
}

// --- 2. Without a patch the removal composites nothing. ---
await call('Edits.SetEditParams', [photo.id, eligible]);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const noPatch512 = await fetchImg(
  `http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(noPatch512), 'patchless removal 512 renders');
check(noPatch512.equals(plain512), 'a patchless removal composites nothing');

// --- 3. Consent gate: with the model gone, GenerateMaskFill must refuse. ---
const st0 = await call('Edits.FillModelStatus', []);
step(`FillModelStatus -> downloaded=${st0.downloaded} bytes=${st0.bytes}`);
if (st0.downloaded) await call('System.DeleteModel', ['migan-1.onnx']);
let refused = false;
try {
  await call('Edits.GenerateMaskFill', [photo.id, saved, 0, false]);
} catch (err) {
  refused = String(err.message).includes('model not downloaded');
}
check(refused, 'GenerateMaskFill without consent refuses with the sentinel');

// --- 4. With consent: download (hash-pinned), inpaint, cache. ---
step('GenerateMaskFill with allowDownload (downloads ~28 MB + one inference)...');
let t = Date.now();
const gen = await call('Edits.GenerateMaskFill', [photo.id, saved, 0, true]);
step(`  generated in ${Date.now() - t}ms`);
check(gen.fillVer === 'migan-1', `fill version tag is migan-1 (got ${gen.fillVer})`);
check(gen.generated === true, 'first GenerateMaskFill reports generated');
const again = await call('Edits.GenerateMaskFill', [photo.id, saved, 0, false]);
check(again.generated === false, 'second GenerateMaskFill is a fast-path no-op');

// --- 5. Same edit hash, new pixels: the cache was invalidated. ---
const filled512 = await fetchImg(
  `http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(filled512), `filled 512 renders (${filled512.length} B)`);
check(!filled512.equals(noPatch512), 'the patch changes the render at the SAME edit hash');
const tile = await fetchImg(
  `http://127.0.0.1:8483/img/${photo.id}/tile/0/0?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(tile), `filled 1:1 tile (0,0) renders (${tile.length} B)`);

// --- 6. Composite-only fields must NOT cost an inference. ---
for (const [name, patch] of [
  ['feather', { feather: 0.9 }],
  ['adjust', { adjust: { expEV: 0.8 } }],
  ['fx', { adjust: { blur: 0.5 } }],
]) {
  const tweaked = { ...saved, masks: [{ ...saved.masks[0], ...patch }] };
  const res = await call('Edits.GenerateMaskFill', [photo.id, tweaked, 0, false]);
  check(res.generated === false, `a ${name} change reuses the cached patch`);
}

// --- 7. Region and decode changes DO re-key. ---
const repainted = {
  ...saved,
  masks: [{ ...saved.masks[0], strokes: [{ ...brushStrokes[0], radius: 0.055 }] }],
};
check((await call('Edits.GenerateMaskFill', [photo.id, repainted, 0, false])).generated === true,
  'a repainted brush re-keys and regenerates');
const warm = { ...saved, wbTemp: 0.3 };
check((await call('Edits.GenerateMaskFill', [photo.id, warm, 0, false])).generated === true,
  'a WB change re-keys and regenerates');

// --- 8. The area cap refuses rather than spending an inference. ---
const huge = {
  ...marker,
  masks: [brushMask({ strokes: [{ radius: 0.5, feather: 0.2, pts: [0.35, 0.35, 0.65, 0.65] }] })],
};
let tooLarge = false;
try {
  await call('Edits.GenerateMaskFill', [photo.id, huge, 0, false]);
} catch (err) {
  tooLarge = String(err.message).includes('too large to remove');
}
check(tooLarge, 'a frame-swallowing region is refused by the area cap');

// --- 9. An AI subject mask removes too (the AI path, map already on disk). ---
const subj = await call('Edits.GenerateAIMap', [photo.id, 'subject', true]);
const aiParams = {
  ...marker,
  masks: [{ type: 'ai', aiKind: 'subject', mapVer: subj.mapVer, remove: true, feather: 0.2, adjust: {} }],
};
await call('Edits.SetEditParams', [photo.id, aiParams]);
const aiSaved = await call('Edits.GetEditParams', [photo.id]);
check(aiSaved.masks?.[0]?.remove === true, 'remove survives Normalize on a subject mask');
const aiGen = await call('Edits.GenerateMaskFill', [photo.id, aiSaved, 0, false]);
check(aiGen.generated === true, 'a subject-mask removal generates');
check((await call('Edits.GenerateMaskFill', [photo.id, aiSaved, 0, false])).generated === false,
  'the subject-mask patch is cached in turn');

// An inverted subject mask (= the background) has no surround to fill from.
const inverted = { ...aiParams, masks: [{ ...aiParams.masks[0], invert: true }] };
await call('Edits.SetEditParams', [photo.id, inverted]);
check(!(await call('Edits.GetEditParams', [photo.id])).masks?.[0]?.remove,
  'remove clears on an inverted subject mask');

// --- 10. The sidecar carries the flag. ---
await call('Edits.SetEditParams', [photo.id, eligible]);
await new Promise((r) => setTimeout(r, 500));
const sidecar = readFileSync(join(FOLDER, `${photo.fileName}.marraw.json`), 'utf8');
check(sidecar.includes('"remove"'), 'sidecar carries the remove flag');

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
