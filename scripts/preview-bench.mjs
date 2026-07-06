// Quick wire probe: warm preview latency, draft (1024, in-memory) vs full
// (2048, cache-backed). Varies a look-stage param each frame so the decode
// cache stays hot but every render is fresh (no pyramid-cache hits).
//
//   node scripts/preview-bench.mjs "D:\Photos\..."

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/preview-bench.mjs <raw-folder>');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
ws.binaryType = 'arraybuffer';
let nextId = 1;
const pending = new Map();

ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) {
    const view = new DataView(ev.data);
    const headerLen = view.getUint32(0, false);
    const header = JSON.parse(new TextDecoder().decode(new Uint8Array(ev.data, 4, headerLen)));
    const payload = new Uint8Array(ev.data, 4 + headerLen);
    pending.get(header.id)?.resolve({ bytes: payload });
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
  });
}

await new Promise((r) => (ws.onopen = r));
const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
const p = photos[0];
console.log(`photo ${p.id} (${p.fileName})`);

const base = { expEV: 0.55 }; // odd value: cold decode for this run, then hot
const t0 = Date.now();
await call('Edits.PreviewEdit', [p.id, base, 1024]);
console.log(`decode warm-up: ${Date.now() - t0}ms`);

const bench = async (label, longEdge, n) => {
  const times = [];
  let bytes = 0;
  for (let i = 0; i < n; i++) {
    const params = { ...base, contrast: 0.011 * (i + 1) }; // fresh hash every frame
    const t = Date.now();
    const blob = await call('Edits.PreviewEdit', [p.id, params, longEdge]);
    times.push(Date.now() - t);
    bytes = blob.bytes.length;
  }
  times.sort((a, b) => a - b);
  const med = times[Math.floor(n / 2)];
  console.log(`${label}: median ${med}ms  min ${times[0]}ms  max ${times[n - 1]}ms  blob ~${Math.round(bytes / 1024)}KB`);
};

await bench('draft 1024 (in-memory)', 1024, 12);
await bench('full  2048 (cache-backed)', 0, 12);

ws.close();
process.exit(0);
