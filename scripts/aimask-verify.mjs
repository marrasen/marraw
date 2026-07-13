// End-to-end probe for AI masks against a running marrawd: generates the
// subject and depth maps via Edits.GenerateAIMap, adds ai masks with the
// returned mapVer, and checks the preview pixels actually change (and that
// unrelated guarantees hold: idempotence, class unavailability, missing-map
// no-op).
// Usage: node scripts/aimask-verify.mjs "<disposable raw folder>"
const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/aimask-verify.mjs "<disposable raw folder>"');
  process.exit(1);
}
const PORT = process.env.MARRAW_PORT ?? 8483;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
ws.binaryType = 'arraybuffer';

const pending = new Map();
let nextId = 1;
ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) {
    const view = new DataView(ev.data);
    const headerLen = view.getUint32(0, false);
    const header = JSON.parse(new TextDecoder().decode(new Uint8Array(ev.data, 4, headerLen)));
    const payload = new Uint8Array(ev.data, 4 + headerLen);
    pending.get(header.id)?.resolve({ $binary: true, contentType: header.contentType, bytes: payload });
    pending.delete(header.id);
    return;
  }
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  }
};

function call(method, params, timeoutMs = 300_000) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ type: 'request', id, method, params }));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout: ${method}`));
      }
    }, timeoutMs);
  });
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
const photos = await call('Library.ListPhotos', [info.folderId]);
console.log(`OpenFolder -> ${photos.length} photos`);
const p = photos[0];
const base = await call('Edits.GetEditParams', [p.id]); // seeded camera-mimic expEV

// --- Subject map generation ---
let t = Date.now();
const sub = await call('Edits.GenerateAIMap', [p.id, 'subject']);
console.log(`GenerateAIMap subject -> ${sub.mapVer} in ${Date.now() - t}ms`);
check(sub.mapVer === 'isnet-1', `subject mapVer is isnet-1 (${sub.mapVer})`);

t = Date.now();
const sub2 = await call('Edits.GenerateAIMap', [p.id, 'subject']);
check(sub2.mapVer === sub.mapVer && Date.now() - t < 2000, `second call idempotent + fast (${Date.now() - t}ms)`);

// --- Depth map generation ---
t = Date.now();
const dep = await call('Edits.GenerateAIMap', [p.id, 'depth']);
console.log(`GenerateAIMap depth -> ${dep.mapVer} in ${Date.now() - t}ms`);
check(dep.mapVer === 'depthany2s-1', `depth mapVer is depthany2s-1 (${dep.mapVer})`);

// --- Class kind reports unavailable ---
const classErr = await call('Edits.GenerateAIMap', [p.id, 'class']).then(() => null, (e) => e);
check(classErr != null && /no model available/.test(classErr.message), `class kind unavailable (${classErr?.message})`);

// --- Preview pixels change when an ai mask carries an adjustment ---
const plain = await call('Edits.PreviewEdit', [p.id, base, 1024]);
check(plain.$binary && plain.bytes.length > 10_000, `plain preview renders (${plain.bytes.length}B)`);

const subjectMask = { type: 'ai', aiKind: 'subject', mapVer: sub.mapVer, adjust: { expEV: 1.5 } };
const masked = await call('Edits.PreviewEdit', [p.id, { ...base, masks: [subjectMask] }, 1024]);
check(masked.$binary && masked.bytes.length > 10_000, `subject-masked preview renders (${masked.bytes.length}B)`);
check(Buffer.compare(Buffer.from(plain.bytes), Buffer.from(masked.bytes)) !== 0, 'subject mask changes preview pixels');

// Inverted mask must differ from both.
const inverted = await call('Edits.PreviewEdit', [
  p.id, { ...base, masks: [{ ...subjectMask, invert: true }] }, 1024,
]);
check(Buffer.compare(Buffer.from(inverted.bytes), Buffer.from(masked.bytes)) !== 0, 'inverted subject differs from subject');

// Depth window mask.
const depthMask = { type: 'ai', aiKind: 'depth', mapVer: dep.mapVer, depthLo: 0.6, depthHi: 1, feather: 0.3, adjust: { expEV: -1.5 } };
const depthPrev = await call('Edits.PreviewEdit', [p.id, { ...base, masks: [depthMask] }, 1024]);
check(Buffer.compare(Buffer.from(depthPrev.bytes), Buffer.from(plain.bytes)) !== 0, 'depth mask changes preview pixels');

// Missing map (bogus version) renders identically to a mask-free render.
// Baseline re-rendered HERE: OpenFolder's background calibration writes
// look_gamma mid-run, so the t=0 plain render can differ from later ones for
// reasons unrelated to masks.
const plain2 = await call('Edits.PreviewEdit', [p.id, base, 1024]);
const stale = await call('Edits.PreviewEdit', [
  p.id, { ...base, masks: [{ ...subjectMask, mapVer: 'isnet-999' }] }, 1024,
]);
check(Buffer.compare(Buffer.from(stale.bytes), Buffer.from(plain2.bytes)) === 0, 'missing map renders as no-op');

// Neutral-adjust ai mask is also a no-op.
const neutral = await call('Edits.PreviewEdit', [
  p.id, { ...base, masks: [{ type: 'ai', aiKind: 'subject', mapVer: sub.mapVer, adjust: {} }] }, 1024,
]);
check(Buffer.compare(Buffer.from(neutral.bytes), Buffer.from(plain2.bytes)) === 0, 'neutral ai mask is a no-op');

// --- Persist + settle path (2048 cache render) sees the mask too ---
await call('Edits.SetEditParams', [p.id, { ...base, masks: [subjectMask] }]);
const settled = await call('Edits.PreviewEdit', [p.id, { ...base, masks: [subjectMask] }, 0]);
check(settled.$binary && settled.bytes.length > 10_000, `2048 cache-backed render works (${settled.bytes.length}B)`);
await call('Edits.SetEditParams', [p.id, base]); // restore

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
