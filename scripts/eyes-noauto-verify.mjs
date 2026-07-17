// Regression check: closed-eye detection is USER-INITIATED ONLY. The
// calibrate pass must not backfill eye scores anymore (it used to once the
// models were on disk), while Library.AnalyzeEyes keeps working as a shared
// task with live progress and tray cancellation. Run it with the eye models
// already installed — the whole point is that installed models alone no
// longer trigger scoring.
//
//   node scripts/eyes-noauto-verify.mjs "<fresh folder A>" "<fresh folder B>"
//
// Both folders must be DISPOSABLE copies never opened before (fresh DB rows,
// so nothing is scored). A gets the full scan + no-op re-scan checks; B (make
// it bigger) gets the mid-flight cancel check.
const [FOLDER_A, FOLDER_B] = [process.argv[2], process.argv[3]];
if (!FOLDER_A || !FOLDER_B) {
  console.error('usage: node scripts/eyes-noauto-verify.mjs "<folder A>" "<folder B>"');
  process.exit(1);
}
const PORT = process.env.MARRAW_PORT ?? 8483;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);

const pending = new Map();
let nextId = 1;
// Latest full task snapshot + per-task progress ticks, straight off the wire.
let taskSnapshot = [];
const ticks = new Map(); // taskId -> [{current, total}]
ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  } else if (msg.type === 'push' && msg.event === 'TaskStateEvent') {
    taskSnapshot = msg.data.tasks ?? [];
  } else if (msg.type === 'push' && msg.event === 'TaskUpdateEvent') {
    const t = msg.data;
    if (t.current != null || t.total != null) {
      if (!ticks.has(t.taskId)) ticks.set(t.taskId, []);
      ticks.get(t.taskId).push({ current: t.current, total: t.total });
    }
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

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const step = (name) => console.log(name);

// Wait until a shared task matching pred has APPEARED and left the snapshot
// (terminal tasks drop out of TaskStateEvent) or gone terminal in place.
// Returns the last state seen.
async function awaitTaskGone(pred, timeoutMs = 180_000) {
  const t0 = Date.now();
  let seen = null;
  while (Date.now() - t0 < timeoutMs) {
    const live = taskSnapshot.find(pred);
    if (live) seen = live;
    const terminal = live && (live.status === 'completed' || live.status === 'failed');
    if ((seen && !live) || terminal) return live ?? seen;
    await sleep(100);
  }
  throw new Error('timeout waiting for task');
}

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const status = await call('Library.EyeModelStatus', []);
if (!status.downloaded) {
  console.error('eye models not installed — run eyes-verify.mjs first; this check needs them on disk');
  process.exit(1);
}

async function openAndCalibrate(folder) {
  const info = await call('Library.OpenFolder', [folder]);
  let photos;
  for (let i = 0; i < 120; i++) {
    photos = await call('Library.ListPhotos', [info.folderId]);
    if (photos.length) break;
    await sleep(1000);
  }
  // The calibrate pass runs right after the metadata pass on open; wait for
  // it to come and go so "no eyes scores" below means "calibrate chose not
  // to", not "calibrate hasn't gotten there yet".
  const calib = await awaitTaskGone(
    (t) => t.meta?.kind === 'calibrate' && t.meta?.folderPath === folder,
  );
  return { info, photos, calib };
}

// --- 1. Folder A: calibrate completes WITHOUT writing eye scores. ---
step('1. open folder A — calibrate must not score eyes');
const a = await openAndCalibrate(FOLDER_A);
check(a.calib != null, `calibrate task ran (${a.calib?.title})`);
let photosA = await call('Library.ListPhotos', [a.info.folderId]);
check(photosA.length >= 3, `folder A has photos (${photosA.length})`);
check(
  photosA.every((p) => !p.eyesAnalyzed && p.eyesClosed == null),
  'no photo has eye scores after calibrate (models ARE installed)',
);

// --- 2. AnalyzeEyes still works: shared task, live progress, full result. ---
step('2. AnalyzeEyes on A — task with progress, all photos analyzed');
const refA = await call('Library.AnalyzeEyes', [photosA.map((p) => p.id), false], 120_000);
check(typeof refA?.taskId === 'string', 'scan started without needing download consent');
const doneA = await awaitTaskGone((t) => t.id === refA.taskId);
check(doneA?.status !== 'failed', `eyes task did not fail (${doneA?.status}${doneA?.error ? `: ${doneA.error}` : ''})`);
check(doneA?.meta?.kind === 'eyes', `task carries kind "eyes" (${doneA?.meta?.kind})`);
const ticksA = ticks.get(refA.taskId) ?? [];
check(
  ticksA.length > 0 && ticksA.every((x) => x.total === photosA.length),
  `progress ticked over the wire (${ticksA.length} ticks, total=${ticksA[0]?.total})`,
);
photosA = await call('Library.ListPhotos', [a.info.folderId]);
check(photosA.every((p) => p.eyesAnalyzed), 'every photo in A is eyesAnalyzed');

// --- 3. Idempotence: a second scan has nothing to do. ---
const again = await call('Library.AnalyzeEyes', [photosA.map((p) => p.id), false]);
check(again == null, 're-scan returns nothing-to-do');

// --- 4. Folder B: cancel the scan mid-flight from the tray RPC. ---
step('4. folder B — cancel AnalyzeEyes mid-flight');
const b = await openAndCalibrate(FOLDER_B);
let photosB = await call('Library.ListPhotos', [b.info.folderId]);
check(
  photosB.every((p) => !p.eyesAnalyzed),
  `no eye scores in B after calibrate (${photosB.length} photos)`,
);
const refB = await call('Library.AnalyzeEyes', [photosB.map((p) => p.id), false], 120_000);
await call('tasksHandler.CancelTask', [refB.taskId]);
const doneB = await awaitTaskGone((t) => t.id === refB.taskId);
photosB = await call('Library.ListPhotos', [b.info.folderId]);
const analyzedB = photosB.filter((p) => p.eyesAnalyzed).length;
if (doneB?.error === 'canceled' || analyzedB < photosB.length) {
  check(true, `cancel stopped the scan (${analyzedB}/${photosB.length} analyzed, error=${doneB?.error ?? 'n/a'})`);
} else {
  // The scan is thumb-domain and fast — losing the race on a small folder is
  // a fixture-size problem, not a product bug. Flag it distinctly.
  check(false, `cancel lost the race — scan finished all ${photosB.length} first; use a bigger folder B`);
}

// Park the daemon back on A so B's pre-render pass doesn't keep churning.
await call('Library.OpenFolder', [FOLDER_A]);

console.log(failures ? `\n${failures} FAILURE(S)` : '\nALL PASS');
ws.close();
process.exit(failures ? 1 : 0);
