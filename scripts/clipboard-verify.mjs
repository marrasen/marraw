// End-to-end check of Export.RenderClipboard against a running
// `marrawd --dev --port 8483`. Exercises: PNG magic + IHDR dimensions,
// the longEdge resize, full-res when longEdge is 0, and the error path
// for an unknown photo id. Unlike the JSON-only sibling scripts this one
// parses aprot binary response frames (4-byte BE header length, JSON
// header, payload) — the shape client/src/api/client.ts decodes.
//
//   node scripts/clipboard-verify.mjs "D:\Photos\marraw-clipboard-fixture"
//
// Read-only against the fixture: nothing is written next to the RAWs.

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/clipboard-verify.mjs <raw-folder>');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
ws.binaryType = 'arraybuffer';
let nextId = 1;
const pending = new Map();

ws.onmessage = async (ev) => {
  if (typeof ev.data === 'string') {
    const msg = JSON.parse(ev.data);
    if (msg.type === 'response') {
      pending.get(msg.id)?.resolve(msg.result);
      pending.delete(msg.id);
    } else if (msg.type === 'error') {
      pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
      pending.delete(msg.id);
    }
    return;
  }
  const buffer = ev.data instanceof ArrayBuffer ? ev.data : await ev.data.arrayBuffer();
  const view = new DataView(buffer);
  const headerLen = view.getUint32(0, false);
  const header = JSON.parse(new TextDecoder().decode(new Uint8Array(buffer, 4, headerLen)));
  const payload = new Uint8Array(buffer, 4 + headerLen);
  pending.get(header.id)?.resolve({ contentType: header.contentType, bytes: payload });
  pending.delete(header.id);
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

const PNG_MAGIC = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
const pngDims = (bytes) => {
  const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  return { w: v.getUint32(16, false), h: v.getUint32(20, false) };
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
console.log(`OpenFolder -> ${photos.length} photos`);
const p = photos[0];

// --- 1. Resized render: PNG bytes, long edge honored. ---
let res = await call('Export.RenderClipboard', [
  { photoId: p.id, longEdge: 800, sharpenTarget: 'screen', sharpenAmount: 'standard', watermarkId: '' },
]);
check(res.contentType === 'image/png', `content type is image/png (${res.contentType})`);
check(PNG_MAGIC.every((b, i) => res.bytes[i] === b), 'payload starts with the PNG magic');
let dims = pngDims(res.bytes);
console.log(`  render: ${dims.w}x${dims.h}, ${res.bytes.length} bytes`);
check(Math.max(dims.w, dims.h) === 800, `long edge resized to 800 (${dims.w}x${dims.h})`);
check(Math.min(dims.w, dims.h) > 0, 'short edge is sane');

// --- 2. longEdge 0 keeps full resolution. ---
res = await call('Export.RenderClipboard', [
  { photoId: p.id, longEdge: 0, sharpenTarget: 'off', sharpenAmount: '', watermarkId: '' },
]);
dims = pngDims(res.bytes);
console.log(`  full-res render: ${dims.w}x${dims.h}, ${res.bytes.length} bytes`);
check(
  Math.max(dims.w, dims.h) >= Math.max(p.width, p.height) - 64,
  `full-res close to catalog size ${p.width}x${p.height}`,
);

// --- 3. Unknown photo id fails cleanly. ---
let errored = false;
try {
  await call('Export.RenderClipboard', [
    { photoId: 99_999_999, longEdge: 800, sharpenTarget: 'off', sharpenAmount: '', watermarkId: '' },
  ]);
} catch {
  errored = true;
}
check(errored, 'unknown photo id rejects with an error');

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
