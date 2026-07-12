// One-shot check of the memory-aware export admission against a running
// `marrawd --dev --port 8483`: exports a JPEG batch and verifies every file
// lands. The budget decision itself is a daemon log line —
//   export: N photos, mem budget X MiB (70% of Y MiB avail), cpu limit Z
// — checked by the caller in the daemon's stderr/log file.
//
//   node scripts/export-mem-verify.mjs "D:\Photos\marraw-memverify-fixture"

import { mkdtempSync, readdirSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/export-mem-verify.mjs <disposable-raw-folder>');
  process.exit(1);
}

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
    }, 180_000);
  });
}

async function waitTask(taskId, timeoutMs = 180_000) {
  const t = Date.now();
  while (Date.now() - t < timeoutMs) {
    for (const m of pushes) {
      if (m.event !== 'TaskStateEvent') continue;
      const task = m.data.tasks?.find((x) => x.id === taskId);
      if (task && (task.status === 'completed' || task.status === 'failed')) return task;
    }
    await new Promise((s) => setTimeout(s, 100));
  }
  throw new Error('timeout waiting for export task');
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
if (photos.length < 2) throw new Error('need at least 2 RAWs in the fixture');

const dest = mkdtempSync(join(tmpdir(), 'marraw-memverify-'));
const ref = await call('Export.StartExport', [
  { photoIds: photos.map((x) => x.id), destDir: dest, format: 'jpeg', jpegQuality: 90, createDir: false },
]);
const task = await waitTask(ref.taskId);
check(task.status === 'completed', `export task completed (${task.status})`);

const written = readdirSync(dest).filter((n) => n.toLowerCase().endsWith('.jpg'));
check(written.length === photos.length, `${photos.length} JPEGs written (got ${written.length})`);
check(written.every((n) => statSync(join(dest, n)).size > 100_000), 'every JPEG is non-trivial (>100 KB)');
rmSync(dest, { recursive: true, force: true });

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
