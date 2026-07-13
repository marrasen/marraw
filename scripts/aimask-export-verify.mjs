// Full-resolution export probe for AI masks: exports one photo twice (with
// and without a subject mask) and checks the guided-refinement path holds up
// at sensor resolution — different pixels, sane timing, no failure.
// Usage: node scripts/aimask-export-verify.mjs "<disposable raw folder>"
import { mkdtempSync, readFileSync, statSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// jpegDims parses the SOF marker for width×height.
function jpegDims(path) {
  const b = readFileSync(path);
  for (let i = 2; i < b.length - 8; ) {
    if (b[i] !== 0xff) { i++; continue; }
    const m = b[i + 1];
    if (m >= 0xc0 && m <= 0xcf && m !== 0xc4 && m !== 0xc8 && m !== 0xcc) {
      return [b.readUInt16BE(i + 7), b.readUInt16BE(i + 5)];
    }
    i += 2 + b.readUInt16BE(i + 2);
  }
  return [0, 0];
}

const FOLDER = process.argv[2];
const ws = new WebSocket('ws://127.0.0.1:8483/ws');
const pending = new Map();
const pushes = [];
let nextId = 1;
ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') { pending.get(msg.id)?.resolve(msg.result); pending.delete(msg.id); }
  else if (msg.type === 'error') { pending.get(msg.id)?.reject(new Error(msg.message)); pending.delete(msg.id); }
  else if (msg.type === 'push') pushes.push(msg);
};
const call = (method, params) => new Promise((resolve, reject) => {
  const id = String(nextId++);
  pending.set(id, { resolve, reject });
  ws.send(JSON.stringify({ type: 'request', id, method, params }));
  setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error(`timeout: ${method}`)); } }, 300_000);
});
const waitTask = async (taskId) => {
  const t = Date.now();
  while (Date.now() - t < 300_000) {
    for (const m of pushes) {
      if (m.event !== 'TaskStateEvent') continue;
      const task = m.data.tasks?.find((x) => x.id === taskId);
      if (task && (task.status === 'completed' || task.status === 'failed')) return task;
    }
    await new Promise((s) => setTimeout(s, 100));
  }
  throw new Error('export task timeout');
};

await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('ws connect failed')); });
const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
const p = photos[0];
// Strip any crop left behind by UI probes — this check wants sensor pixels.
const base = { ...(await call('Edits.GetEditParams', [p.id])), cropX: 0, cropY: 0, cropW: 0, cropH: 0, cropAngle: 0 };
const sub = await call('Edits.GenerateAIMap', [p.id, 'subject', true]);

const dest = mkdtempSync(join(tmpdir(), 'marraw-aiexp-'));
const exportOnce = async (params, name) => {
  await call('Edits.SetEditParams', [p.id, params]);
  const t = Date.now();
  const ref = await call('Export.StartExport', [{ photoIds: [p.id], destDir: dest, format: 'jpeg', fileNameTemplate: name }]);
  const task = await waitTask(ref.taskId);
  if (task.status !== 'completed') throw new Error(`export ${name} ${task.status}`);
  return Date.now() - t;
};

const t1 = await exportOnce(base, 'plain');
const t2 = await exportOnce({ ...base, masks: [{ type: 'ai', aiKind: 'subject', mapVer: sub.mapVer, adjust: { expEV: 1.2 } }] }, 'masked');
await call('Edits.SetEditParams', [p.id, base]);

const s1 = statSync(join(dest, 'plain.jpg')).size;
const s2 = statSync(join(dest, 'masked.jpg')).size;
const [w, h] = jpegDims(join(dest, 'masked.jpg'));
console.log(`plain: ${t1}ms ${s1}B   masked(+guided): ${t2}ms ${s2}B ${w}x${h}   overhead: ${t2 - t1}ms`);
// Full resolution (sensor long edge ≥ 7000 for the A7R II fixtures), the
// masked render differs, and guided refinement stays cheap.
const pass = Math.max(w, h) >= 7000 && s1 !== s2 && t2 - t1 < 5000;
console.log(pass ? 'PASS full-res export with guided AI mask' : 'FAIL export check');
rmSync(dest, { recursive: true, force: true });
ws.close();
process.exit(pass ? 0 : 1);
