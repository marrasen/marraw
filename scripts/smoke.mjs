// End-to-end smoke test against a running `marrawd --dev --port 8483`.
// Exercises: folder scan, photo list, thumbnail pyramid, rating + push
// events, edit preview loop, persisted edits, and export.
//
//   node scripts/smoke.mjs "D:\Photos\2026-04-18 Velox Valor Trollhättan"

import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/smoke.mjs <raw-folder>');
  process.exit(1);
}
const HTTP = 'http://127.0.0.1:8483';

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
const pushes = [];
const streamItems = new Map();

ws.onmessage = (ev) => {
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
    case 'stream_item': {
      streamItems.get(msg.id)?.push(msg.item);
      break;
    }
    case 'stream_end': {
      const p = pending.get(msg.id);
      if (p) {
        if (msg.code) p.reject(new Error(`${msg.code}: ${msg.message}`));
        else p.resolve(streamItems.get(msg.id));
      }
      pending.delete(msg.id);
      break;
    }
    case 'push': {
      pushes.push(msg);
      break;
    }
  }
};

function call(method, params, stream = false) {
  const id = String(nextId++);
  if (stream) streamItems.set(id, []);
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
check(Date.now() - tCache < 100, 'cached thumb serves fast');

// Stale cache key must 409.
r = await fetch(`${HTTP}/img/${p.id}/512?v=${'0'.repeat(32)}`);
check(r.status === 409, 'stale cache key -> 409');

// 4. Rating + push event.
await call('Library.SetRating', [[p.id], 4]);
await new Promise((s) => setTimeout(s, 300));
const patch = pushes.find((m) => m.event === 'PhotoPatchEvent');
step('SetRating -> push received');
check(patch && patch.data.patches.some((x) => x.id === p.id && x.rating === 4), 'PhotoPatchEvent carries rating patch');

// 5. Edit preview loop (+1 EV), then commit.
const params = {
  expEV: 1, expPreserve: 0, wbMode: 'camera', wbMul: [0, 0, 0, 0],
  bright: 0, highlight: 0, nrThreshold: 0, fbddNoiseRd: 0, medPasses: 0,
};
const tPrev = Date.now();
const prev = await call('Edits.PreviewEdit', [p.id, params]);
const coldPreviewMs = Date.now() - tPrev;
step(`PreviewEdit (cold) -> hash=${prev.editHash} in ${coldPreviewMs}ms`);
check(/^[0-9a-f]{12}$/.test(prev.editHash), 'preview returns edit hash');

r = await fetch(`${HTTP}/img/${p.id}/2048?v=${p.cacheKey}&e=${prev.editHash}`);
check(r.status === 200, 'preview rendition served');

// Warm re-render with a different EV must be fast (handle kept unpacked).
const params2 = { ...params, expEV: 0.5 };
const tWarm = Date.now();
const prev2 = await call('Edits.PreviewEdit', [p.id, params2]);
const warmMs = Date.now() - tWarm;
step(`PreviewEdit (warm) -> ${warmMs}ms`);
check(warmMs < 1500, `warm preview under 1.5s (${warmMs}ms)`);
check(prev2.editHash !== prev.editHash, 'different params -> different hash');

await call('Edits.SetEditParams', [p.id, params2]);
step('SetEditParams committed');
const stored = await call('Edits.GetEditParams', [p.id]);
check(stored && stored.expEV === 0.5, 'edit params persisted');

// Edited grid thumb serves for the committed hash.
r = await fetch(`${HTTP}/img/${p.id}/512?v=${p.cacheKey}&e=${prev2.editHash}`);
check(r.status === 200, 'edited grid thumb served');

// 6. Batch edit two photos.
const ids = photos.slice(1, 3).map((x) => x.id);
await call('Edits.ApplyBatchEdit', [ids, { expEV: 0.25, bright: null, highlight: null, nrThreshold: null, fbddNoiseRd: null, medPasses: null }]);
const batchParams = await call('Edits.GetEditParams', [ids[0]]);
step('ApplyBatchEdit done');
check(batchParams && batchParams.expEV === 0.25, 'batch delta applied');

// 7. Export three photos (one edited).
const dest = mkdtempSync(join(tmpdir(), 'marraw-export-'));
const tExp = Date.now();
const items = await call('Export.ExportPhotos', [{
  photoIds: photos.slice(0, 3).map((x) => x.id),
  destDir: dest, format: 'jpeg', jpegQuality: 90, longEdge: 0,
}], true);
step(`ExportPhotos -> ${items.length} items in ${Date.now() - tExp}ms`);
check(items.length === 3 && items.every((i) => i.ok), 'all exports ok');
const written = readdirSync(dest);
check(written.length === 3, `3 files written (${written.join(', ')})`);
rmSync(dest, { recursive: true, force: true });

// 8. Reset edits to leave a clean state.
await call('Edits.ResetEdits', [[p.id, ...ids]]);
const cleared = await call('Edits.GetEditParams', [p.id]);
check(cleared === null || cleared === undefined, 'edits reset');

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
