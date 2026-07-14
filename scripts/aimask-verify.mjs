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
const sub = await call('Edits.GenerateAIMap', [p.id, 'subject', true]);
console.log(`GenerateAIMap subject -> ${sub.mapVer} in ${Date.now() - t}ms`);
check(sub.mapVer === 'isnet-1', `subject mapVer is isnet-1 (${sub.mapVer})`);

t = Date.now();
const sub2 = await call('Edits.GenerateAIMap', [p.id, 'subject', true]);
check(sub2.mapVer === sub.mapVer && Date.now() - t < 2000, `second call idempotent + fast (${Date.now() - t}ms)`);
check(sub2.generated === false, `cached map reports generated=false (${sub2.generated}) — the client must not repaint`);

// --- Depth map generation ---
t = Date.now();
const dep = await call('Edits.GenerateAIMap', [p.id, 'depth', true]);
console.log(`GenerateAIMap depth -> ${dep.mapVer} in ${Date.now() - t}ms`);
check(dep.mapVer === 'depthany2s-1', `depth mapVer is depthany2s-1 (${dep.mapVer})`);

// --- Class map: scene detection on a real outdoor photo ---
t = Date.now();
const cls = await call('Edits.GenerateAIMap', [p.id, 'class', true]);
console.log(`GenerateAIMap class -> ${cls.mapVer} in ${Date.now() - t}ms:`,
  (cls.categories ?? []).map((c) => `${c.name} ${(c.fraction * 100).toFixed(0)}%`).join(', ') || '(none)');
check(cls.mapVer === 'adeseg-1', `class mapVer is adeseg-1 (${cls.mapVer})`);
const catNames = (cls.categories ?? []).map((c) => c.name);
check(catNames.includes('Sky'), `outdoor scene detects Sky (got: ${catNames.join(', ')})`);
check((cls.categories ?? []).length >= 2, 'multiple regions detected');

// A class mask over the largest region changes preview pixels.
if (cls.categories?.length) {
  const classMask = { type: 'ai', aiKind: 'class', mapVer: cls.mapVer, classId: cls.categories[0].id, feather: 0.25, adjust: { expEV: 1.2 } };
  const clsPrev = await call('Edits.PreviewEdit', [p.id, { ...base, masks: [classMask] }, 1024]);
  const clsPlain = await call('Edits.PreviewEdit', [p.id, base, 1024]);
  check(Buffer.compare(Buffer.from(clsPrev.bytes), Buffer.from(clsPlain.bytes)) !== 0, 'class mask changes preview pixels');
}

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

// --- Download consent gate ---
// With the model file hidden, generation WITHOUT consent must refuse (the
// client shows its dialog on this error), status must report the download,
// and nothing must have been fetched behind the user's back.
{
  const { renameSync, existsSync } = await import('node:fs');
  const { join } = await import('node:path');
  const { homedir } = await import('node:os');
  const dataDir = process.env.APPDATA
    ? join(process.env.APPDATA, 'marraw')
    : join(homedir(), '.config', 'marraw');
  const modelPath = join(dataDir, 'models', 'isnet-1.onnx');
  if (existsSync(modelPath)) {
    renameSync(modelPath, modelPath + '.bak');
    try {
      const p2 = photos[1]; // no cached subject map → reaches the model check
      const gateErr = await call('Edits.GenerateAIMap', [p2.id, 'subject', false]).then(() => null, (e) => e);
      check(gateErr != null && /model not downloaded/.test(gateErr.message),
        `no-consent generation refuses (${gateErr?.message})`);
      const st = await call('Edits.AIModelStatus', ['subject']);
      check(st.downloaded === false && st.bytes === 178648008,
        `status reports pending download (downloaded=${st.downloaded} bytes=${st.bytes})`);
      check(!existsSync(modelPath), 'refusal downloaded nothing');
    } finally {
      renameSync(modelPath + '.bak', modelPath);
    }
    const st2 = await call('Edits.AIModelStatus', ['subject']);
    check(st2.downloaded === true, 'status reports downloaded after restore');
  } else {
    check(false, `consent-gate fixture missing: ${modelPath}`);
  }
}

// --- Persist + settle path (2048 cache render) sees the mask too ---
await call('Edits.SetEditParams', [p.id, { ...base, masks: [subjectMask] }]);
const settled = await call('Edits.PreviewEdit', [p.id, { ...base, masks: [subjectMask] }, 0]);
check(settled.$binary && settled.bytes.length > 10_000, `2048 cache-backed render works (${settled.bytes.length}B)`);
await call('Edits.SetEditParams', [p.id, base]); // restore

// --- Cancelled decode leaves the cached handle healthy ---
// A second connection fires a decode-forcing render (fresh NR threshold →
// new decode key) and drops mid-flight; the connection close cancels the
// request server-side and LibRaw aborts at its next checkpoint, recycling
// the handle. The cancel path must re-Open it: a fresh decode of the SAME
// photo on the main connection must then succeed, not inherit a dead handle.
{
  const ws2 = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
  await new Promise((res) => { ws2.onopen = res; });
  ws2.send(JSON.stringify({
    type: 'request', id: 'doomed', method: 'Edits.PreviewEdit',
    params: [p.id, { ...base, nrThreshold: 100 }, 1024],
  }));
  await new Promise((s) => setTimeout(s, 150)); // let the decode start
  ws2.close();
  await new Promise((s) => setTimeout(s, 200)); // server notices the close
  const after = await call('Edits.PreviewEdit', [p.id, { ...base, nrThreshold: 120 }, 1024]);
  check(after.$binary && after.bytes.length > 10_000,
    `decode after cancelled decode succeeds (${after.bytes?.length ?? 0}B)`);
}

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
