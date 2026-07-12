// End-to-end check of 1:1 render cancellation + progress reporting against a
// running `marrawd --dev --port 8483`. Exercises: (1) aborting a cold tile
// request stops the render (nothing lands in the cache — the retry pays the
// full render again), (2) an uncancelled 1:1 render streams monotonically
// rising RenderProgressEvent pushes ending at 1, (3) a warm-cache tile serve
// emits no progress events.
//
//   node scripts/cancel-verify.mjs "D:\Photos\marraw-cancel-fixture"
//
// Point it at a DISPOSABLE copy of a shoot.

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/cancel-verify.mjs <disposable-raw-folder>');
  process.exit(1);
}

const HTTP = 'http://127.0.0.1:8483';
const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
const pushes = [];

ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  } else if (msg.type === 'push') {
    pushes.push(msg);
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

const progressFor = (photoId) =>
  pushes
    .filter((m) => m.event === 'RenderProgressEvent' && m.data.photoId === photoId)
    .map((m) => m.data.fraction);

const sleep = (ms) => new Promise((s) => setTimeout(s, ms));

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const step = (name) => console.log(name);

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
step(`OpenFolder -> ${photos.length} photos`);
if (photos.length < 2) throw new Error('need at least 2 RAWs in the fixture');

const tileUrl = (p) => {
  const params = new URLSearchParams({ v: p.cacheKey, r: 'r7' });
  if (p.editHash && p.editHash !== 'base') params.set('e', p.editHash);
  return `${HTTP}/img/${p.id}/tile/0/0?${params}`;
};

// Let the folder-open background passes (metadata, calibrate, 2048 prerender)
// settle so they don't compete for pool workers during the timed probes.
await sleep(4000);

// --- 1. Abort a cold 1:1 tile request: the render must stop, not complete. ---
const p1 = photos[0];
step(`1: abort cold tile render of ${p1.fileName}`);
const ac = new AbortController();
const t0 = Date.now();
const aborted = fetch(tileUrl(p1), { signal: ac.signal }).then(
  () => 'completed',
  () => 'aborted',
);
setTimeout(() => ac.abort(), 100);
check((await aborted) === 'aborted', 'client fetch aborted at ~100ms');
// Give the cancelled render time to have finished IF it were going to.
await sleep(5000);
const afterAbort = progressFor(p1.id);
check(!afterAbort.includes(1), `no render ran to completion (progress seen: [${afterAbort.join(', ')}])`);

// The definitive black-box proof: a retry pays the full render again (a
// completed render would serve the tile from disk in milliseconds).
const t1 = Date.now();
const retry = await fetch(tileUrl(p1));
const retryMs = Date.now() - t1;
check(retry.ok, `retry serves the tile (${retry.status})`);
check(retryMs > 500, `retry paid a fresh render (${retryMs}ms — cancelled run left no tiles)`);
step(`   abort->retry: aborted at ${Date.now() - t0 - retryMs - 5000}ms, retry took ${retryMs}ms`);

// --- 2. The uncancelled render streamed progress ending at 1. ---
await sleep(500); // let the trailing progress pushes drain
const fracs = progressFor(p1.id).filter((f) => !afterAbort.includes(f) || f === 1);
const all = progressFor(p1.id);
step(`2: progress events for ${p1.fileName}: [${all.map((f) => f.toFixed(2)).join(', ')}]`);
check(all.length >= 3, `progress streamed (${all.length} events)`);
check(all[all.length - 1] === 1, 'final fraction is 1');
const tail = all.slice(afterAbort.length);
check(
  tail.every((f, i) => i === 0 || f >= tail[i - 1]),
  'fractions are monotonically non-decreasing within the run',
);

// --- 3. Warm serve: no new progress events. ---
const before = progressFor(p1.id).length;
const t2 = Date.now();
const warm = await fetch(tileUrl(p1));
const warmMs = Date.now() - t2;
await sleep(700);
step(`3: warm tile serve took ${warmMs}ms`);
check(warm.ok && warmMs < 300, `warm serve is a disk hit (${warmMs}ms)`);
check(progressFor(p1.id).length === before, 'warm serve emitted no progress events');

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
