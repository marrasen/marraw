// End-to-end smoke test against a running `marrawd --dev --port 8483`.
// Exercises: folder scan, photo list, thumbnail pyramid, rating + push
// events, edit preview loop, persisted edits, and export.
//
//   node scripts/smoke.mjs "D:\Photos\2026-04-18 Velox Valor Trollhättan"

import { copyFileSync, existsSync, mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/smoke.mjs <raw-folder>');
  process.exit(1);
}
const HTTP = 'http://127.0.0.1:8483';

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
ws.binaryType = 'arraybuffer';
let nextId = 1;
const pending = new Map();
const pushes = [];
const patches = []; // subscription_patch frames

ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) {
    // Binary Blob result: 4-byte BE header length + JSON header + payload.
    const view = new DataView(ev.data);
    const headerLen = view.getUint32(0, false);
    const header = JSON.parse(new TextDecoder().decode(new Uint8Array(ev.data, 4, headerLen)));
    const payload = new Uint8Array(ev.data, 4 + headerLen);
    pending.get(header.id)?.resolve({ $binary: true, contentType: header.contentType, bytes: payload });
    pending.delete(header.id);
    return;
  }
  const msg = JSON.parse(ev.data);
  switch (msg.type) {
    case 'response': {
      pending.get(msg.id)?.resolve(msg.result);
      pending.delete(msg.id);
      break;
    }
    case 'error': {
      pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
      pending.delete(msg.id);
      break;
    }
    case 'subscription_patch': {
      patches.push(msg);
      break;
    }
    case 'push': {
      pushes.push(msg);
      break;
    }
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

// subscribe opens a patch-capable subscription; resolves with the initial
// result. Later patches land in `patches`.
function subscribe(method, params) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ type: 'subscribe', id, method, params, patch: true }));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout: subscribe ${method}`));
      }
    }, 120_000);
  });
}

// waitFor polls a predicate over incoming frames.
async function waitFor(name, pred, timeoutMs = 60_000) {
  const t = Date.now();
  while (Date.now() - t < timeoutMs) {
    const v = pred();
    if (v) return v;
    await new Promise((s) => setTimeout(s, 100));
  }
  throw new Error(`timeout waiting for ${name}`);
}

const t0 = Date.now();
const step = (name) => console.log(`[${String(Date.now() - t0).padStart(6)}ms] ${name}`);
let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});
step('connected');

// 1. Open folder (fast scan).
const info = await call('Library.OpenFolder', [FOLDER]);
step(`OpenFolder -> folderId=${info.folderId} photos=${info.photoCount}`);
check(info.photoCount > 0, 'folder contains RAW photos');

// 2. List photos.
const photos = await call('Library.ListPhotos', [info.folderId]);
step(`ListPhotos -> ${photos.length} rows`);
check(photos.length === info.photoCount, 'list matches scan count');
const p = photos[0];
check(typeof p.cacheKey === 'string' && p.cacheKey.length === 32, 'photo has cache key');

// 3. Thumbnail on demand (cold: embedded-thumb route).
let r = await fetch(`${HTTP}/img/${p.id}/512?v=${p.cacheKey}`);
const thumbBytes = (await r.arrayBuffer()).byteLength;
step(`GET /img/512 -> ${r.status}, ${thumbBytes} bytes`);
check(r.status === 200 && thumbBytes > 5_000, 'thumbnail generated and served');

// Cached second hit should be instant.
const tCache = Date.now();
r = await fetch(`${HTTP}/img/${p.id}/512?v=${p.cacheKey}`);
await r.arrayBuffer();
step(`GET /img/512 (cached) -> ${Date.now() - tCache}ms`);
// Generous bound: the pre-render pass kicked off by OpenFolder is
// saturating the decode pool while this runs.
check(Date.now() - tCache < 400, 'cached thumb serves fast');

// Stale cache key must 409.
r = await fetch(`${HTTP}/img/${p.id}/512?v=${'0'.repeat(32)}`);
check(r.status === 409, 'stale cache key -> 409');

// 4. Rating + subscription patch (aprot PatchSubscription, O(patch) wire).
const subPhotos = await subscribe('Library.ListPhotos', [info.folderId]);
check(subPhotos.length === info.photoCount, 'subscription initial result');
await call('Library.SetRating', [[p.id], 4]);
const ratingPatch = await waitFor('rating patch', () =>
  patches.find((m) => m.patch?.patches?.some((x) => x.id === p.id && x.rating === 4)), 5_000);
step('SetRating -> subscription_patch received');
check(!!ratingPatch, 'subscription patch carries rating');

// 5. Edit preview loop (+1 EV) — the preview JPEG rides back as a binary
// Blob frame — then commit.
const params = {
  expEV: 1, expPreserve: 0, wbMode: 'camera', wbMul: [0, 0, 0, 0],
  wbTemp: 0, wbTint: 0, bright: 0, gamma: 0, shadow: 0,
  highlight: 0, nrThreshold: 0, fbddNoiseRd: 0, medPasses: 0,
};
const tPrev = Date.now();
const prev = await call('Edits.PreviewEdit', [p.id, params]);
const coldPreviewMs = Date.now() - tPrev;
step(`PreviewEdit (cold) -> ${prev.bytes?.length ?? 0} bytes in ${coldPreviewMs}ms`);
check(prev.$binary && prev.contentType === 'image/jpeg' && prev.bytes.length > 30_000, 'preview arrives as binary JPEG blob');

// Warm re-render with a different EV must be fast (handle kept unpacked).
const params2 = { ...params, expEV: 0.5 };
const tWarm = Date.now();
const prev2 = await call('Edits.PreviewEdit', [p.id, params2]);
const warmMs = Date.now() - tWarm;
step(`PreviewEdit (warm) -> ${warmMs}ms`);
check(warmMs < 1500, `warm preview under 1.5s (${warmMs}ms)`);
check(prev2.$binary && prev2.bytes.length > 30_000, 'warm preview blob served');

await call('Edits.SetEditParams', [p.id, params2]);
step('SetEditParams committed');
const stored = await call('Edits.GetEditParams', [p.id]);
check(stored && stored.expEV === 0.5, 'edit params persisted');

// The commit pushed an editHash patch; the grid thumb serves for that hash.
const hashPatch = await waitFor('editHash patch', () =>
  patches.map((m) => m.patch?.patches?.find((x) => x.id === p.id && x.editHash)).find(Boolean), 5_000);
check(/^[0-9a-f]{12}$/.test(hashPatch.editHash), 'commit pushes edit hash patch');
r = await fetch(`${HTTP}/img/${p.id}/512?v=${p.cacheKey}&e=${hashPatch.editHash}`);
check(r.status === 200, 'edited grid thumb served');

// White balance pick: neutralize the image center.
const picked = await call('Edits.PickWhiteBalance', [p.id, params2, 0.5, 0.5]);
step(`PickWhiteBalance -> mul=[${picked.wbMul.map((m) => m.toFixed(2)).join(', ')}]`);
check(picked.wbMode === 'custom' && picked.wbMul[1] === 1 && picked.wbMul[0] > 0, 'picker returns custom multipliers');

// 6. Batch edit two photos. Untouched photos start from their seeded
// camera-mimic compensation (base_exp_ev), so the delta lands on top of it.
const ids = photos.slice(1, 3).map((x) => x.id);
const seedBefore = await call('Edits.GetEditParams', [ids[0]]);
const seedEV = seedBefore?.expEV ?? 0;
await call('Edits.ApplyBatchEdit', [ids, { expEV: 0.25, bright: null, highlight: null, nrThreshold: null, fbddNoiseRd: null, medPasses: null }]);
const batchParams = await call('Edits.GetEditParams', [ids[0]]);
step(`ApplyBatchEdit done (seed ${seedEV} EV)`);
check(batchParams && Math.abs(batchParams.expEV - Math.min(3, seedEV + 0.25)) < 1e-6, 'batch delta applied on top of seed');

// 7. Export three photos (one edited) as a background shared task.
const dest = mkdtempSync(join(tmpdir(), 'marraw-export-'));
const tExp = Date.now();
const destMissing = await call('Export.CheckDest', [join(dest, 'nope')]);
check(destMissing.exists === false, 'CheckDest reports missing dir');
const ref = await call('Export.StartExport', [{
  photoIds: photos.slice(0, 3).map((x) => x.id),
  destDir: dest, format: 'jpeg', jpegQuality: 90, longEdge: 0, createDir: false,
}]);
check(typeof ref.taskId === 'string' && ref.taskId.length > 0, 'StartExport returns task ref');
const doneTask = await waitFor('export task completion', () => {
  for (const m of pushes) {
    if (m.event !== 'TaskStateEvent') continue;
    const t = m.data.tasks?.find((t) => t.id === ref.taskId);
    if (t && (t.status === 'completed' || t.status === 'failed')) return t;
  }
  return null;
}, 120_000);
step(`StartExport -> task ${doneTask.status} in ${Date.now() - tExp}ms`);
check(doneTask.status === 'completed', 'export task completed');
const written = readdirSync(dest);
check(written.length === 3, `3 files written (${written.join(', ')})`);
rmSync(dest, { recursive: true, force: true });
// Progress events flowed while it ran.
const sawProgress = pushes.some((m) =>
  m.event === 'TaskStateEvent' && m.data.tasks?.some((t) => t.id === ref.taskId && t.total === 3));
check(sawProgress, 'export task reported progress');

// 8. DeletePhotos moves the file to the recycle bin (tested on a copy).
const delDir = mkdtempSync(join(tmpdir(), 'marraw-del-'));
copyFileSync(join(FOLDER, p.fileName), join(delDir, p.fileName));
const delInfo = await call('Library.OpenFolder', [delDir]);
check(delInfo.photoCount === 1, 'copy scanned into temp folder');
const delPhotos = await call('Library.ListPhotos', [delInfo.folderId]);
const delRes = await call('Library.DeletePhotos', [[delPhotos[0].id]]);
step('DeletePhotos done');
check(delRes.deleted === 1, 'DeletePhotos reports 1 deleted');
check(!existsSync(join(delDir, p.fileName)), 'file gone from disk (recycled)');
const delList = await call('Library.ListPhotos', [delInfo.folderId]);
check(delList.length === 0, 'photo row removed');
rmSync(delDir, { recursive: true, force: true });
// Restore the main folder's background jobs slot (deleting opened a folder).
await call('Library.OpenFolder', [FOLDER]);

// 9. Reset edits to leave a clean state. A reset photo has no stored edit
// row (editHash back to 'base'); GetEditParams then reports either null or
// the seeded baseline — neutral except the camera-mimic exposure seed.
await call('Edits.ResetEdits', [[p.id, ...ids]]);
const cleared = await call('Edits.GetEditParams', [p.id]);
const seededOnly =
  cleared != null &&
  Object.entries(cleared).every(([k, v]) =>
    k === 'expEV' ? true : Array.isArray(v) ? v.every((x) => !x) : !v);
const afterReset = (await call('Library.ListPhotos', [info.folderId])).find((x) => x.id === p.id);
check((cleared == null || seededOnly) && afterReset.editHash === 'base', 'edits reset to seeded baseline');

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
