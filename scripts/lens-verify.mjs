// End-to-end check of lens profile correction against a running
// `marrawd --dev --port 8483`. Exercises: the Edits.LensProfile lookup, the
// correction landing in the committed render at a downscaled level AND a 1:1
// tile, the Off switch restoring the uncorrected frame, the per-component
// amounts, and an export carrying the same correction the loupe showed.
//
//   node scripts/lens-verify.mjs /tmp/marraw-fixture
//
// Point it at a DISPOSABLE copy of a shoot — it writes .marraw.json sidecars.
//
// The fixture body must be one the embedded database knows. The dev fixture
// (Panasonic DC-LX100M2) is a fixed-lens compact, which also exercises the
// match-by-mount path that every compact depends on.

import { mkdtempSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/lens-verify.mjs <disposable-raw-folder>');
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

// --- 1. The profile lookup. ---------------------------------------------
const prof = await call('Edits.LensProfile', [photo.id]);
step(`LensProfile: ${JSON.stringify(prof)}`);
check(prof.cameraKnown, 'the fixture body is in the lens database');
check(!!prof.profile, `a profile matched (${prof.profile || 'none'})`);
check(prof.focal > 0, `the focal length came through (${prof.focal}mm)`);
check(
  prof.hasDistortion || prof.hasVignetting || prof.hasCA,
  'the profile carries at least one correction',
);
if (!prof.profile) {
  console.error('\nNo profile matched — the rest of this script cannot verify anything.');
  process.exit(1);
}

// --- 2. Correction on (the default) vs explicitly off. -------------------
// Auto is the zero value, so the "corrected" state is the one an empty edit
// produces; Off is the deviation.
await call('Edits.SetEditParams', [photo.id, {}]);
let cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const autoHash = cur.editHash;
const auto512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${autoHash}`);
check(isJpeg(auto512), `default (corrected) 512 renders (${auto512.length} B, hash ${autoHash})`);

await call('Edits.SetEditParams', [photo.id, { lensMode: 'off' }]);
const savedOff = await call('Edits.GetEditParams', [photo.id]);
check(savedOff.lensMode === 'off', 'lensMode "off" survives Normalize');
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const offHash = cur.editHash;
check(offHash !== autoHash, 'switching the correction off advances the edit hash');
check(cur.cacheKey === photo.cacheKey, 'cacheKey is unchanged (the correction is post-decode)');
const off512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${offHash}`);
check(isJpeg(off512), `uncorrected 512 renders (${off512.length} B)`);
check(!off512.equals(auto512), 'the correction changes the 512 render');

const offTile = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/tile/0/0?v=${cur.cacheKey}&e=${offHash}`);
check(isJpeg(offTile), `uncorrected 1:1 tile (0,0) renders (${offTile.length} B)`);
await call('Edits.SetEditParams', [photo.id, {}]);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const autoTile = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/tile/0/0?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(autoTile), `corrected 1:1 tile (0,0) renders (${autoTile.length} B)`);
check(!autoTile.equals(offTile), 'the correction reaches the 1:1 tiles, not just the previews');

// --- 3. Per-component amounts each move the render on their own. ---------
// Each component is dialled to zero in turn against the fully-corrected
// baseline; a component the profile does not carry is skipped rather than
// asserted, since there is nothing for it to change.
const components = [
  ['lensDistortion', prof.hasDistortion, 'distortion'],
  ['lensVignetting', prof.hasVignetting, 'vignetting'],
  ['lensCA', prof.hasCA, 'chromatic aberration'],
];
for (const [field, available, label] of components) {
  if (!available) {
    step(`  (skip ${label}: this profile has no ${label} calibration)`);
    continue;
  }
  await call('Edits.SetEditParams', [photo.id, { [field]: -1 }]);
  const c = (await call('Library.ListPhotos', [info.folderId]))[0];
  const img = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${c.cacheKey}&e=${c.editHash}`);
  check(!img.equals(auto512), `zeroing ${label} changes the render`);
  check(!img.equals(off512), `zeroing ${label} alone is not the same as switching the profile off`);
}

// A half-strength amount round-trips through Normalize rather than being
// clamped or dropped as a near-zero value.
await call('Edits.SetEditParams', [photo.id, { lensDistortion: -0.5, lensVignetting: 0.25 }]);
const savedAmt = await call('Edits.GetEditParams', [photo.id]);
check(
  savedAmt.lensDistortion === -0.5 && savedAmt.lensVignetting === 0.25,
  'per-component amounts survive Normalize',
);

// Off wins over the amounts, and clears them so the same picture hashes once.
await call('Edits.SetEditParams', [photo.id, { lensMode: 'off', lensDistortion: -0.5 }]);
const savedOffAmt = await call('Edits.GetEditParams', [photo.id]);
check(
  savedOffAmt.lensMode === 'off' && !savedOffAmt.lensDistortion,
  'switching off clears the amounts so one look has one hash',
);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
check(cur.editHash === offHash, 'off-with-amounts hashes identically to plain off');

// --- 4. Export carries the same correction. ------------------------------
const outDir = mkdtempSync(join(tmpdir(), 'marraw-lens-'));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const exportOnce = async (params, tag) => {
  await call('Edits.SetEditParams', [photo.id, params]);
  const destDir = join(outDir, tag);
  await call('Export.StartExport', [
    { photoIds: [photo.id], destDir, format: 'jpeg', jpegQuality: 92, longEdge: 1200, createDir: true },
  ]);
  let file = null;
  for (let i = 0; i < 120 && !file; i++) {
    await sleep(500);
    try {
      const files = readdirSync(destDir).filter((f) => f.toLowerCase().endsWith('.jpg'));
      if (files.length > 0) file = join(destDir, files[0]);
    } catch {
      // dest not created yet
    }
  }
  if (!file) throw new Error(`export ${tag}: no output file`);
  await sleep(500); // let the atomic rename settle
  return readFileSync(file);
};
try {
  const expAuto = await exportOnce({}, 'auto');
  const expOff = await exportOnce({ lensMode: 'off' }, 'off');
  check(isJpeg(expAuto) && isJpeg(expOff), `both exports produced a JPEG (${expAuto.length} / ${expOff.length} B)`);
  check(!expAuto.equals(expOff), 'the export carries the correction (corrected != uncorrected)');
} finally {
  rmSync(outDir, { recursive: true, force: true });
}

// Leave the fixture as we found it.
await call('Edits.SetEditParams', [photo.id, {}]);

console.log(failures === 0 ? '\nAll checks passed.' : `\n${failures} check(s) FAILED.`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
