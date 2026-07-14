// End-to-end check of near-duplicate burst grouping against a running
// marrawd. The fixture folder holds identical copies of one RAW (same EXIF
// capture time, same embedded thumb), so the calibrate pass must backfill a
// perceptual hash for every photo and ListPhotos must derive one burst
// spanning all of them, id'd by the lead (capture-ordered first) photo.
//
//   node scripts/neardup-verify.mjs "<disposable raw folder>"
const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/neardup-verify.mjs "<disposable raw folder>"');
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

// --- 1. Open the folder; the calibrate pass backfills phash, and groupId is
// derived on the next list. ---
step('1. open folder, wait for burst grouping');
const info = await call('Library.OpenFolder', [FOLDER]);
let photos;
for (let i = 0; i < 120; i++) {
  photos = await call('Library.ListPhotos', [info.folderId]);
  if (photos.length && photos.every((p) => p.groupId != null)) break;
  await sleep(1000);
}
check(photos.length >= 3, `folder has >= 3 photos (${photos.length})`);
check(photos.every((p) => p.groupId != null), 'every identical copy carries a groupId');

// --- 2. The copies are byte-identical (same capture second, same thumb), so
// they must form ONE group led by the capture-ordered first photo. ---
step('2. group shape');
const ids = new Set(photos.map((p) => p.groupId));
check(ids.size === 1, `all copies share one group (${[...ids].join(', ')})`);
check(photos[0].groupId === photos[0].id, 'group id is the lead photo\'s id');
check(photos.every((p) => p.sharpness != null), 'sharpness landed alongside the hash (same pass)');

console.log(failures ? `\n${failures} FAILURE(S)` : '\nALL PASS');
ws.close();
process.exit(failures ? 1 : 0);
