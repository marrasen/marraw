// End-to-end check of zoomed-in windows for edits that read the whole
// frame (dehaze, mask effects) against a running `marrawd --dev`: with a
// photo open in the editor and such an edit saved, Edits.PreviewWindow
// answers a JPEG (it used to answer null), and a window over a 1:1 tile is
// that tile, byte for byte.
// Usage: node scripts/fxwindow-verify.mjs "<disposable raw folder>"
const FOLDER = process.argv[2];
const PORT = process.env.MARRAW_PORT ?? 8483;
const HTTP = `http://127.0.0.1:${PORT}`;
const RENDER_VERSION = 'r16';
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
ws.binaryType = 'arraybuffer';
const pending = new Map();
let nextId = 1;
let failures = 0;
const check = (ok, label) => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${label}`);
  if (!ok) failures++;
};
ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') {
    // Binary response: 4-byte header length, JSON header, payload.
    const view = new DataView(ev.data);
    const n = view.getUint32(0, false);
    const header = JSON.parse(new TextDecoder().decode(new Uint8Array(ev.data, 4, n)));
    pending.get(header.id)?.resolve(new Uint8Array(ev.data, 4 + n));
    return;
  }
  const m = JSON.parse(ev.data);
  if (m.type === 'response') {
    const b = m.result?.$blob;
    pending.get(m.id)?.resolve(b ? Uint8Array.from(Buffer.from(b.data ?? '', 'base64')) : m.result);
  } else if (m.type === 'error') pending.get(m.id)?.reject(new Error(m.message));
};
const call = (method, params) => new Promise((resolve, reject) => {
  const i = String(nextId++);
  pending.set(i, { resolve, reject });
  ws.send(JSON.stringify({ type: 'request', id: i, method, params }));
  setTimeout(() => reject(new Error(`timeout: ${method}`)), 180_000);
});
const tile = async (p, hash, tx, ty) => {
  const res = await fetch(`${HTTP}/img/${p.id}/tile/${tx}/${ty}?v=${p.cacheKey}&r=${RENDER_VERSION}&e=${hash}`);
  return new Uint8Array(await res.arrayBuffer());
};
const same = (a, b) => a.length === b.length && a.every((v, i) => v === b[i]);

await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('ws connect failed')); });
await call('Library.SetSidecarWrites', [false]);
const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
const p = photos[0];
console.log(`photo ${p.id}: ${p.width}x${p.height}`);
call('Edits.OpenInEditor', [p.id]).catch(() => {}); // held while the editor is open

const edits = {
  dehaze: { contrast: 0.2, dehaze: 0.5 },
  effects: {
    contrast: 0.2, clarity: 0.3, dehaze: 0.4,
    masks: [
      { type: 'radial', cx: 0.5, cy: 0.5, rx: 0.3, ry: 0.25, feather: 0.5, invert: true, adjust: { blur: 0.5, expEV: -0.3 } },
      { type: 'linear', x0: 0.2, y0: 0.1, x1: 0.6, y1: 0.7, adjust: { glow: 0.7, streaks: 0.5 } },
    ],
  },
};
for (const [name, params] of Object.entries(edits)) {
  const saved = await call('Edits.SetEditParams', [p.id, params]);
  const t0 = Date.now();
  const want = await tile(p, saved.hash, 1, 1);
  console.log(`  ${name}: tile (1,1) ${want.length} bytes after ${Date.now() - t0} ms`);
  const t1 = Date.now();
  const got = await call('Edits.PreviewWindow', [p.id, params, 1024, 1024, 1024, 1024]);
  const ms = Date.now() - t1;
  check(got instanceof Uint8Array && got[0] === 0xff && got[1] === 0xd8, `${name}: the window is a JPEG, not null (${ms} ms)`);
  check(got instanceof Uint8Array && same(got, want), `${name}: the window over tile (1,1) is that tile, byte for byte`);
  // A drag frame: the same edit a little further, not saved.
  const drag = structuredClone(params);
  drag.dehaze += 0.05;
  const t2 = Date.now();
  const frame = await call('Edits.PreviewWindow', [p.id, drag, 800, 600, 1920, 1080]);
  check(frame instanceof Uint8Array && frame.length > 1000, `${name}: a drag frame's 1920x1080 window comes (${Date.now() - t2} ms)`);
}
await call('Edits.ResetEdits', [[p.id]]).catch((e) => console.log(`  reset: ${e.message}`));
await call('Library.SetSidecarWrites', [true]);
console.log(failures ? `${failures} FAILED` : 'all passed');
process.exit(failures ? 1 : 0);
