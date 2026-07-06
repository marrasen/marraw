// One-off e2e probe for the detail stage (sharpen/clarity/texture/dehaze):
// renders previews of the same photo with each op on vs. off and checks the
// JPEGs actually differ, plus reports warm-loop latency with detail active.
// Usage: node scripts/detail-verify.mjs "<raw folder>"
const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/detail-verify.mjs "<raw folder>"');
  process.exit(1);
}
const PORT = process.env.MARRAW_PORT ?? 8483;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
ws.binaryType = 'arraybuffer';

const pending = new Map();
let nextId = 1;
ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) {
    // Binary Blob result: 4-byte BE header length + JSON header + payload.
    const view = new DataView(ev.data);
    const headerLen = view.getUint32(0, false);
    const header = JSON.parse(new TextDecoder().decode(new Uint8Array(ev.data, 4, headerLen)));
    const payload = new Uint8Array(ev.data, 4 + headerLen);
    pending.get(header.id)?.resolve({ $binary: true, contentType: header.contentType, bytes: payload });
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
const p = photos[0];
console.log(`photo ${p.id} (${p.fileName})`);

const neutralish = { expEV: 0.75 }; // non-neutral base so every render is deterministic
const render = async (extra) => {
  const t = Date.now();
  const blob = await call('Edits.PreviewEdit', [p.id, { ...neutralish, ...extra }, 0]);
  return { bytes: blob.bytes, ms: Date.now() - t };
};

const base = await render({});
console.log(`base render: ${base.bytes.length} bytes in ${base.ms}ms`);
check(base.bytes.length > 30_000, 'base preview served');

const differs = (a, b) => a.length !== b.length || a.some((v, i) => v !== b[i]);

for (const [name, extra] of [
  ['sharpen', { sharpen: 1 }],
  ['texture', { texture: 1 }],
  ['clarity', { clarity: 1 }],
  ['dehaze +', { dehaze: 0.7 }],
  ['dehaze -', { dehaze: -0.7 }],
]) {
  const r = await render(extra);
  check(differs(r.bytes, base.bytes), `${name} changes the render (${r.ms}ms warm)`);
  check(r.ms < 1500, `${name} warm preview under 1.5s (${r.ms}ms)`);
}

// Validation: out-of-range values must be rejected.
const rejected = await call('Edits.PreviewEdit', [p.id, { ...neutralish, sharpen: 2 }, 0])
  .then(() => false, () => true);
check(rejected, 'out-of-range sharpen rejected by validation');

console.log(failures ? `${failures} FAILURES` : 'ALL CHECKS PASSED');
ws.close();
process.exit(failures ? 1 : 0);
