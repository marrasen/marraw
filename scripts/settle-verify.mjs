// Wire-level check of the PreviewEdit cancel contract the immediate-settle
// scheduler leans on, against a running `marrawd --dev --port 8483`.
// Exercises: 1024/2048 renders return JPEG blobs (binary frames), a cancel
// frame sent mid-2048 settles the request with the Canceled error (no blob),
// and a follow-up render of the same params still succeeds — i.e. cancelling
// a settle never wedges the photo's hot LibRaw handle.
//
//   node scripts/settle-verify.mjs "D:\Photos\marraw-settle-fixture"
//
// Point it at a DISPOSABLE copy of a shoot — edits write .marraw.json
// sidecars next to the RAWs.

import { connect } from './lib/rpc.mjs';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/settle-verify.mjs <disposable-raw-folder>');
  process.exit(1);
}

const CANCELED = -32800;

const { call, send, cancel, close } = await connect();

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
console.log(`OpenFolder -> ${photos.length} photos`);
const p = photos[0];
// Untouched photos report null params (the server seeds camera-mimic values
// internally) — a partial object is a valid non-neutral edit state.
const params = (await call('Edits.GetEditParams', [p.id])) ?? {};

// --- 1. Baseline: draft (1024) and settle (2048) renders return JPEG blobs. ---
const edited = { ...params, expEV: (params.expEV ?? 0) + 0.4, contrast: 0.2 };
let t0 = Date.now();
const draft = await call('Edits.PreviewEdit', [p.id, edited, 1024]);
const draftMs = Date.now() - t0;
check(draft.blob?.length > 5_000 && draft.contentType === 'image/jpeg', `1024 draft renders (${Math.round(draft.blob.length / 1024)} KB in ${draftMs}ms)`);

t0 = Date.now();
const full = await call('Edits.PreviewEdit', [p.id, edited, 2048]);
const fullMs = Date.now() - t0;
check(full.blob?.length > draft.blob.length, `2048 settle renders (${Math.round(full.blob.length / 1024)} KB in ${fullMs}ms)`);

// --- 2. Cancel mid-flight: a superseded 2048 must settle Canceled, no blob. ---
// New params so the render can't be served from the rendition written above.
const edited2 = { ...edited, expEV: edited.expEV + 0.3 };
const req = send('Edits.PreviewEdit', [p.id, edited2, 2048]);
setTimeout(() => cancel(req.id), 30); // mid-decode/mid-wait
let cancelErr = null;
let cancelBlob = null;
t0 = Date.now();
try {
  cancelBlob = await req.promise;
} catch (err) {
  cancelErr = err;
}
const cancelMs = Date.now() - t0;
check(cancelErr != null && cancelBlob == null, `cancelled 2048 returns no blob (settled in ${cancelMs}ms)`);
check(cancelErr?.code === CANCELED, `cancelled 2048 settles with Canceled (${cancelErr?.code}: ${cancelErr?.message})`);

// --- 3. The successor renders fine: the hot handle survived the cancel. ---
t0 = Date.now();
const after = await call('Edits.PreviewEdit', [p.id, edited2, 1024]);
check(after.blob?.length > 5_000, `follow-up 1024 after cancel renders (${Math.round(after.blob.length / 1024)} KB in ${Date.now() - t0}ms)`);
t0 = Date.now();
const settleAfter = await call('Edits.PreviewEdit', [p.id, edited2, 2048]);
check(settleAfter.blob?.length > 5_000, `re-issued 2048 of the cancelled params renders (${Math.round(settleAfter.blob.length / 1024)} KB in ${Date.now() - t0}ms)`);

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
close();
process.exit(failures === 0 ? 0 : 1);
