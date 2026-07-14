// End-to-end check of the Settings → Models backend against a running
// `marrawd --dev --port 8483`. Exercises: System.GetModelsInfo (empty dir,
// known specs joined with name/purpose, orphaned files listed nameless,
// largest-first order), System.DeleteModel (file gone, info pushed to the
// GetModelsInfo subscription, path traversal / non-.onnx rejected).
//
//   node scripts/models-verify.mjs
//
// Seeds sparse fake weights in the daemon's real models dir (learned from
// GetModelsInfo) and leaves them behind on success — `shot.mjs <f> models`
// wants a populated list. Safe: fakes only ever share names with real
// weights, and a real 170 MB+ model is never deleted by this script.

import { openSync, closeSync, ftruncateSync, existsSync, mkdirSync, rmSync } from 'node:fs';
import { join } from 'node:path';

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
// Server-driven subscription refreshes are extra `response` frames carrying
// the subscription's id — collected here once the initial response settled.
const subUpdates = [];

ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    if (pending.has(msg.id)) {
      pending.get(msg.id).resolve(msg.result);
      pending.delete(msg.id);
    } else {
      subUpdates.push(msg);
    }
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
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
    }, 30_000);
  });
}

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

// The three pinned specs (internal/aimask/aimask.go) plus one orphan a
// version bump would strand. Sparse files: full apparent size, no disk cost.
const SPECS = [
  { file: 'adeseg-1.onnx', bytes: 1_375_613_437, name: 'Semantic classes' },
  { file: 'isnet-1.onnx', bytes: 178_648_008, name: 'Subject' },
  { file: 'depthany2s-1.onnx', bytes: 99_060_839, name: 'Depth' },
];
const ORPHAN = { file: 'isnet-0.onnx', bytes: 55_555 };
const seed = (dir, f) => {
  const fd = openSync(join(dir, f.file), 'w');
  ftruncateSync(fd, f.bytes);
  closeSync(fd);
};

// --- 1. Location + baseline. ---
let info = await call('System.GetModelsInfo', []);
step(`models dir: ${info.dir}`);
check(typeof info.dir === 'string' && info.dir.length > 0, 'GetModelsInfo reports the models dir');
// Clean slate, deleting ONLY files this script seeds (by name). Anything
// else in the dir — say real downloaded weights — aborts the run.
const fakes = new Set([...SPECS, ORPHAN].map((f) => f.file));
for (const m of info.models ?? []) {
  if (!fakes.has(m.fileName)) {
    throw new Error(`refusing to run: ${join(info.dir, m.fileName)} is not a fixture of this script`);
  }
  rmSync(join(info.dir, m.fileName), { force: true });
}
info = await call('System.GetModelsInfo', []);
check((info.models ?? []).length === 0, 'empty models dir lists nothing');

// --- 2. Seed three known specs + one orphan; list must join the catalog. ---
mkdirSync(info.dir, { recursive: true });
for (const f of [...SPECS, ORPHAN]) seed(info.dir, f);
seed(info.dir, { file: 'isnet-1.onnx.part-x1', bytes: 10 }); // in-flight temp: hidden

info = await call('System.GetModelsInfo', []);
const names = info.models.map((m) => m.fileName);
step(`listed: ${names.join(', ')}`);
check(info.models.length === 4, `lists the 4 .onnx files, not the .part temp (got ${info.models.length})`);
for (const s of SPECS) {
  const m = info.models.find((x) => x.fileName === s.file);
  check(!!m && m.bytes === s.bytes, `${s.file} listed with exact size`);
  check(!!m && (m.name ?? '').startsWith(s.name), `${s.file} joined to catalog name "${m?.name}"`);
  check(!!m && (m.purpose ?? '').length > 0, `${s.file} carries a purpose line`);
}
const orphan = info.models.find((x) => x.fileName === ORPHAN.file);
check(!!orphan && !orphan.name && !orphan.purpose, 'orphaned old-version file listed without catalog identity');
check(
  info.models.every((m, i) => i === 0 || info.models[i - 1].bytes >= m.bytes),
  'models sorted largest first',
);

// --- 3. Subscribe, delete the orphan: file gone + refresh pushed. ---
const subId = String(nextId++);
const initial = new Promise((resolve, reject) => pending.set(subId, { resolve, reject }));
ws.send(JSON.stringify({ type: 'subscribe', id: subId, method: 'System.GetModelsInfo', params: [] }));
check((await initial).models.length === 4, 'subscription snapshot sees all 4 files');

info = await call('System.DeleteModel', [ORPHAN.file]);
check(!existsSync(join(info.dir, ORPHAN.file)), 'DeleteModel removes the file from disk');
check(info.models.length === 3 && !info.models.some((m) => m.fileName === ORPHAN.file), 'DeleteModel returns the updated list');
await new Promise((s) => setTimeout(s, 500));
const push = subUpdates.find((p) => p.id === subId && (p.result?.models ?? []).length === 3);
check(!!push, 'GetModelsInfo subscribers get the post-delete list pushed');

// --- 4. Probe: hostile / wrong names must be rejected, disk untouched. ---
for (const bad of ['../marraw.db', 'isnet-1.onnx/../../marraw.db', 'isnet-1.txt']) {
  let rejected = false;
  try {
    await call('System.DeleteModel', [bad]);
  } catch {
    rejected = true;
  }
  check(rejected, `DeleteModel("${bad}") rejected`);
}
check(existsSync(join(info.dir, 'isnet-1.onnx')), 'remaining models untouched by rejected deletes');
// A missing file is a plain error, not a crash.
let missingRejected = false;
try {
  await call('System.DeleteModel', ['ghost-9.onnx']);
} catch {
  missingRejected = true;
}
check(missingRejected, 'DeleteModel on a missing file errors cleanly');

// Leave a populated dir behind — three specs plus the orphan — so a
// follow-up `shot.mjs <folder> models` has something to show.
seed(info.dir, ORPHAN);

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
