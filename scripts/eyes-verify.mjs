// End-to-end check of closed-eye detection against a running marrawd.
// The eye models are tiny (~280 KB together), so unlike subjsharp-verify
// this drives the REAL pipeline: the consent gate (AnalyzeEyes without
// allowDownload must refuse while weights are missing), the download, the
// scan task, and the eyesAnalyzed/eyesClosed plumbing back out of
// ListPhotos. Fixture folders are typically faceless scenes, so the usual
// outcome asserted is the analyzed-with-hidden-sentinel path; when a face
// is present the score is just reported.
//
//   node scripts/eyes-verify.mjs "<disposable raw folder>"
const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/eyes-verify.mjs "<disposable raw folder>"');
  process.exit(1);
}
const PORT = process.env.MARRAW_PORT ?? 8483;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);

const pending = new Map();
let nextId = 1;
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

function call(method, params, timeoutMs = 60_000) {
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
const step = (name) => console.log(name);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

// --- 1. Open the folder and read the starting state. ---
step('1. open folder, check model status');
const info = await call('Library.OpenFolder', [FOLDER]);
let photos;
for (let i = 0; i < 120; i++) {
  photos = await call('Library.ListPhotos', [info.folderId]);
  if (photos.length) break;
  await sleep(1000);
}
check(photos.length >= 1, `folder has photos (${photos.length})`);
const status = await call('Library.EyeModelStatus', []);
step(`   models downloaded=${status.downloaded} missing bytes=${status.bytes}`);

// --- 2. Consent gate: without the models on disk, a scan that may not
// download must refuse up front. ---
if (!status.downloaded) {
  step('2. consent gate refuses without allowDownload');
  let refused = false;
  try {
    await call('Library.AnalyzeEyes', [photos.map((p) => p.id), false]);
  } catch (err) {
    refused = /model not downloaded/.test(err.message);
  }
  check(refused, 'AnalyzeEyes without consent fails with the download sentinel');
} else {
  step('2. (models already on disk — consent gate not exercisable)');
}

// --- 3. Run the real scan with consent; every photo must come back
// analyzed. ---
step('3. AnalyzeEyes with consent, wait for the scan');
const ref = await call('Library.AnalyzeEyes', [photos.map((p) => p.id), true], 120_000);
check(ref == null || typeof ref.taskId === 'string', 'scan started (or nothing to do)');
for (let i = 0; i < 120; i++) {
  photos = await call('Library.ListPhotos', [info.folderId]);
  if (photos.every((p) => p.eyesAnalyzed)) break;
  await sleep(1000);
}
check(photos.every((p) => p.eyesAnalyzed), 'every photo is eyesAnalyzed');
for (const p of photos) {
  step(`   ${p.fileName}: eyesClosed=${p.eyesClosed ?? '(none/hidden)'}`);
}
check(photos.every((p) => p.eyesClosed == null || (p.eyesClosed >= 0 && p.eyesClosed <= 1)), 'scores, when present, are 0..1');

// --- 4. The weights are now installed, listed, and a re-scan is a no-op. ---
step('4. models installed, re-scan is a no-op');
const after = await call('Library.EyeModelStatus', []);
check(after.downloaded && after.bytes === 0, 'EyeModelStatus reports both models on disk');
const models = (await call('System.GetModelsInfo', [])).models.map((m) => m.fileName);
check(models.includes('yunet-2023mar.onnx'), 'GetModelsInfo lists yunet');
check(models.includes('openclosedeye-0001.onnx'), 'GetModelsInfo lists the eye classifier');
const again = await call('Library.AnalyzeEyes', [photos.map((p) => p.id), false]);
check(again == null, 'second scan returns nothing-to-do');

console.log(failures ? `\n${failures} FAILURE(S)` : '\nALL PASS');
ws.close();
process.exit(failures ? 1 : 0);
