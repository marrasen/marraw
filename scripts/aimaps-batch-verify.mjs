// End-to-end probe for Edits.GenerateAIMaps against a running marrawd: the
// batch materialization behind a preset-with-AI-mask apply across a selection.
// Mimics the client flow — GenerateAIMap for the focused photo, PasteEditParams
// of the mask recipe to the whole selection, then GenerateAIMaps for the rest —
// and checks the aggregate task completes, photos with maps already on disk are
// skipped, AIMapsGeneratedEvent broadcasts per landed map (to every
// connection, no folder subscription needed), the second call is a no-op
// (nil ref), and the persisted mask actually changes a non-focused photo's
// pixels.
//
// Works on throwaway copies of a single RAW (cache keys include the absolute
// path, so copies have independent maps and never collide with the fixture's).
// Usage: node scripts/aimaps-batch-verify.mjs "<any raw file to copy>"
import { copyFileSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const RAW = process.argv[2];
if (!RAW) {
  console.error('usage: node scripts/aimaps-batch-verify.mjs "<raw file>"');
  process.exit(1);
}
const PORT = process.env.MARRAW_PORT ?? 8483;

const dir = mkdtempSync(join(tmpdir(), 'marraw-aimaps-batch-'));
for (const n of ['a.arw', 'b.arw', 'c.arw']) copyFileSync(RAW, join(dir, n));

const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
ws.binaryType = 'arraybuffer';
const pending = new Map();
const pushes = [];
let nextId = 1;
ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) {
    const view = new DataView(ev.data);
    const headerLen = view.getUint32(0, false);
    const header = JSON.parse(new TextDecoder().decode(new Uint8Array(ev.data, 4, headerLen)));
    pending.get(header.id)?.resolve({ $binary: true, bytes: new Uint8Array(ev.data, 4 + headerLen) });
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
  } else if (msg.type === 'push') {
    pushes.push(msg);
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

async function waitFor(name, pred, timeoutMs = 300_000) {
  const t = Date.now();
  while (Date.now() - t < timeoutMs) {
    const v = pred();
    if (v) return v;
    await new Promise((s) => setTimeout(s, 100));
  }
  throw new Error(`timeout waiting for ${name}`);
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

try {
  const info = await call('Library.OpenFolder', [dir]);
  // Deliberately a plain request, no ListPhotos subscription: the bust event
  // must arrive on a connection that is NOT subscribed to the folder.
  const photos = await call('Library.ListPhotos', [info.folderId]);
  console.log(`OpenFolder -> ${photos.length} photos`);
  const [p0, p1, p2] = photos;
  const base = await call('Edits.GetEditParams', [p0.id]);

  // Phase 1 of a preset apply: the focused photo's map, via the single path.
  let t = Date.now();
  const sub = await call('Edits.GenerateAIMap', [p0.id, 'subject', true]);
  console.log(`GenerateAIMap subject (focused) -> ${sub.mapVer} in ${Date.now() - t}ms`);

  // Phase 2: the recipe persists to the WHOLE selection...
  const mask = { type: 'ai', aiKind: 'subject', mapVer: sub.mapVer, adjust: { expEV: 1.5 } };
  await call('Edits.PasteEditParams', [[p0.id, p1.id, p2.id], { ...base, masks: [mask] }]);

  // ...and the batch call materializes the rest. All three ids on purpose:
  // p0's map is on disk, so work must be the other two.
  t = Date.now();
  const ref = await call('Edits.GenerateAIMaps', [[p0.id, p1.id, p2.id], ['subject'], false]);
  check(typeof ref?.taskId === 'string' && ref.taskId.length > 0, 'GenerateAIMaps returns a task ref');

  const done = await waitFor('batch task completion', () => {
    for (const m of pushes) {
      if (m.event !== 'TaskStateEvent') continue;
      const tk = m.data.tasks?.find((x) => x.id === ref.taskId);
      if (tk && (tk.status === 'completed' || tk.status === 'failed')) return tk;
    }
    return null;
  });
  console.log(`GenerateAIMaps -> task ${done.status} in ${Date.now() - t}ms`);
  check(done.status === 'completed', 'batch task completed');
  const sawTotal = pushes.some((m) => m.event === 'TaskStateEvent' &&
    m.data.tasks?.some((x) => x.id === ref.taskId && x.total === 2));
  check(sawTotal, 'task total is 2 — the focused photo (map on disk) was skipped');

  // Each landed map must broadcast the cache-bust ping — for exactly the two
  // photos that were missing maps, not the focused one.
  const changed = await waitFor('AIMapsGeneratedEvent broadcasts', () => {
    const ids = new Set();
    for (const m of pushes) {
      if (m.event === 'AIMapsGeneratedEvent') ids.add(m.data.photoId);
    }
    return ids.has(p1.id) && ids.has(p2.id) ? ids : null;
  }, 15_000);
  check(changed.size === 2 && !changed.has(p0.id), `AIMapsGeneratedEvent broadcast for the 2 generated photos only (got ${[...changed].length})`);

  // Idempotent: every map now exists — nothing to do, nil ref.
  const again = await call('Edits.GenerateAIMaps', [[p0.id, p1.id, p2.id], ['subject'], false]);
  check(again == null, `second call returns nil ref (${JSON.stringify(again)})`);

  // The persisted mask now really renders on a non-focused photo.
  const masked = await call('Edits.PreviewEdit', [p1.id, { ...base, masks: [mask] }, 1024]);
  const plain = await call('Edits.PreviewEdit', [p1.id, base, 1024]);
  check(masked.$binary && plain.$binary &&
    Buffer.compare(Buffer.from(masked.bytes), Buffer.from(plain.bytes)) !== 0,
    'subject mask changes a non-focused photo\'s pixels after the batch');
} finally {
  ws.close();
  rmSync(dir, { recursive: true, force: true });
}

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
process.exit(failures === 0 ? 0 : 1);
