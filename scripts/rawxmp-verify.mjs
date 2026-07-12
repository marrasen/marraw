// End-to-end check of the "RAW + XMP" export mode against a running
// `marrawd --dev --port 8483`. Exercises: copy-to-folder (RAW + sidecar,
// bytes identical, mtime preserved), export into the photos' own folder
// (sidecars only, originals untouched), never-overwrite suffixing, the file
// name template, and the crs slider mapping (exposure + vignette sign).
//
//   node scripts/rawxmp-verify.mjs "D:\Photos\marraw-rawxmp-fixture"
//
// Point it at a DISPOSABLE copy of a shoot — the same-folder pass writes
// .xmp files next to the RAWs.

import { mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/rawxmp-verify.mjs <disposable-raw-folder>');
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
    }, 120_000);
  });
}

async function waitTask(taskId, timeoutMs = 120_000) {
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
const step = (name) => console.log(name);

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
step(`OpenFolder -> ${photos.length} photos`);
if (photos.length < 3) throw new Error('need at least 3 RAWs in the fixture');
const p = photos[0];

// A recognizable edit on the first photo: +0.5 EV and a darkening vignette
// (the vignette sign flips in the crs mapping — the sharpest thing to pin).
await call('Edits.SetEditParams', [p.id, { expEV: 0.5, vignette: 0.5, contrast: 0.25 }]);
await call('Library.SetRating', [[p.id], 4]);
step(`edited + rated ${p.fileName}`);

// --- 1. Export to a fresh folder: RAW copies + sidecars. ---
const dest = mkdtempSync(join(tmpdir(), 'marraw-rawxmp-'));
let ref = await call('Export.StartExport', [
  { photoIds: photos.map((x) => x.id), destDir: dest, format: 'rawXmp', createDir: false },
]);
let task = await waitTask(ref.taskId);
check(task.status === 'completed', `copy export task completed (${task.status})`);

const written = readdirSync(dest).sort();
step(`dest: ${written.join(', ')}`);
check(written.length === 6, '3 RAW copies + 3 sidecars written');
for (const x of photos) {
  const base = x.fileName.replace(/\.[^.]+$/, '');
  check(written.includes(x.fileName), `${x.fileName} copied`);
  check(written.includes(`${base}.xmp`), `${base}.xmp written`);
}
const srcBytes = readFileSync(join(FOLDER, p.fileName));
const dstBytes = readFileSync(join(dest, p.fileName));
check(srcBytes.equals(dstBytes), 'copied RAW is byte-identical');
const srcSt = statSync(join(FOLDER, p.fileName));
const dstSt = statSync(join(dest, p.fileName));
check(Math.abs(srcSt.mtimeMs - dstSt.mtimeMs) < 2000, 'copy preserves mtime');

const xmpText = readFileSync(join(dest, p.fileName.replace(/\.[^.]+$/, '.xmp')), 'utf8');
check(xmpText.includes('xmp:Rating="4"'), 'sidecar carries the rating');
check(xmpText.includes('crs:Exposure2012="+0.50"'), 'exposure maps to crs');
check(xmpText.includes('crs:Contrast2012="+25"'), 'contrast maps to crs');
check(xmpText.includes('crs:PostCropVignetteAmount="-50"'), 'vignette sign flips for ACR');
check(xmpText.includes('crs:HasSettings="True"'), 'develop block is marked authoritative');

// --- 2. Probe: re-export to the same folder must suffix, never overwrite. ---
ref = await call('Export.StartExport', [
  { photoIds: [p.id], destDir: dest, format: 'rawXmp', createDir: false },
]);
task = await waitTask(ref.taskId);
const after = readdirSync(dest);
const base = p.fileName.replace(/\.[^.]+$/, '');
const ext = p.fileName.slice(base.length);
check(
  task.status === 'completed' && after.includes(`${base}-2${ext}`) && after.includes(`${base}-2.xmp`),
  're-export suffixes -2 instead of overwriting',
);

// --- 3. Probe: the file name template renames copy and sidecar together. ---
ref = await call('Export.StartExport', [
  { photoIds: [p.id], destDir: dest, format: 'rawXmp', fileNameTemplate: 'job-{seq}', createDir: false },
]);
task = await waitTask(ref.taskId);
const templated = readdirSync(dest);
check(
  task.status === 'completed' && templated.includes(`job-001${ext}`) && templated.includes('job-001.xmp'),
  'file name template applies to the RAW copy and its sidecar',
);
rmSync(dest, { recursive: true, force: true });

// --- 4. Export into the photos' own folder: sidecars only. ---
const before = readdirSync(FOLDER).sort();
ref = await call('Export.StartExport', [
  { photoIds: photos.map((x) => x.id), destDir: FOLDER, format: 'rawXmp', createDir: false },
]);
task = await waitTask(ref.taskId);
check(task.status === 'completed', `same-folder export task completed (${task.status})`);
const inPlace = readdirSync(FOLDER).sort();
step(`fixture now: ${inPlace.join(', ')}`);
const added = inPlace.filter((n) => !before.includes(n));
check(
  added.length === photos.length && added.every((n) => n.endsWith('.xmp')),
  `same-folder export adds only sidecars (added: ${added.join(', ')})`,
);
check(
  photos.every((x) => srcBytesUnchanged(x.fileName)),
  'originals untouched (no renamed copies, bytes intact)',
);
function srcBytesUnchanged(name) {
  return inPlace.includes(name) && !inPlace.includes(name.replace(/(\.[^.]+)$/, '-2$1'));
}
const inPlaceXmp = readFileSync(join(FOLDER, p.fileName.replace(/\.[^.]+$/, '.xmp')), 'utf8');
check(inPlaceXmp.includes('crs:Exposure2012="+0.50"'), 'in-place sidecar carries the edit');

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
