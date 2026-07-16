// End-to-end check of near-duplicate burst grouping against a running
// marrawd. The fixture folder holds identical copies of one RAW (same EXIF
// capture time, same embedded thumb), so the calibrate pass must backfill a
// perceptual hash for every photo and ListPhotos must derive one burst
// spanning all of them, id'd by the lead (capture-ordered first) photo.
// Also exercises the burst settings (hamming cutoff + time window): setter
// round-trip, validation, and the live re-cluster push into a subscribed
// ListPhotos.
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
// TriggerRefresh re-runs arrive as extra `response` frames on the
// subscription id (not subscription_patch — that's the PushPatch path).
const reruns = new Map(); // subscription id -> re-run count
let nextId = 1;
ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    if (pending.has(msg.id)) {
      pending.get(msg.id).resolve(msg.result);
      pending.delete(msg.id);
    } else if (reruns.has(msg.id)) {
      reruns.set(msg.id, reruns.get(msg.id) + 1);
    }
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

// subscribe opens a subscription; resolves with {id, result} and registers
// the id in `reruns` so later TriggerRefresh re-sends are counted.
function subscribe(method, params, timeoutMs = 60_000) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, {
      resolve: (result) => {
        reruns.set(id, 0);
        resolve({ id, result });
      },
      reject,
    });
    ws.send(JSON.stringify({ type: 'subscribe', id, method, params }));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout: subscribe ${method}`));
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

// --- 3. Burst time window setting: defaults, validation, and the
// burstSettings refresh trigger re-running a subscribed ListPhotos. The
// identical copies share one capture second, so grouping itself is
// window-invariant here — the time math is unit-tested (neardup_test.go);
// this checks the plumbing. ---
step('3. burst time window setting');
const settings = await call('Settings.GetUISettings', []);
check(Number.isInteger(settings.burstGapSeconds), `burstGapSeconds present (${settings.burstGapSeconds})`);
const origGap = settings.burstGapSeconds;

const sub = await subscribe('Library.ListPhotos', [info.folderId]);
const newGap = origGap === 10 ? 8 : 10;
await call('Settings.SetBurstGapSeconds', [newGap]);
check((await call('Settings.GetUISettings', [])).burstGapSeconds === newGap, `setter round-trips (${newGap})`);
let repushed = false;
for (let i = 0; i < 50 && !repushed; i++) {
  repushed = reruns.get(sub.id) > 0;
  await sleep(100);
}
check(repushed, 'SetBurstGapSeconds re-runs subscribed ListPhotos (live re-cluster)');

let rejected = 0;
for (const bad of [0, 31]) {
  await call('Settings.SetBurstGapSeconds', [bad]).then(
    () => {},
    () => rejected++,
  );
}
check(rejected === 2, 'out-of-range window (0, 31) rejected');
await call('Settings.SetBurstGapSeconds', [origGap]); // restore as found

console.log(failures ? `\n${failures} FAILURE(S)` : '\nALL PASS');
ws.close();
process.exit(failures ? 1 : 0);
