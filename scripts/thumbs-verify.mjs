// End-to-end check of rendered thumbnails against a running `marrawd --dev`:
// on a COLD folder, an unedited photo's small levels first come from the
// camera's embedded JPEG and go out no-cache with a tag; once the
// pre-render pass draws the photo, the backend pushes BaseRenderedEvent
// and the same URL serves marraw's render, immutable.
// Usage: node scripts/thumbs-verify.mjs "<disposable cold raw folder>"
const FOLDER = process.argv[2];
const PORT = process.env.MARRAW_PORT ?? 8483;
const HTTP = `http://127.0.0.1:${PORT}`;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
const pending = new Map();
const rendered = new Set();
let nextId = 1;
let failures = 0;
const check = (ok, label) => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${label}`);
  if (!ok) failures++;
};
ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const m = JSON.parse(ev.data);
  if (m.type === 'response') pending.get(m.id)?.resolve(m.result);
  else if (m.type === 'error') pending.get(m.id)?.reject(new Error(m.message));
  else if (m.type === 'push' && m.event === 'BaseRenderedEvent') rendered.add(m.data.photoId);
};
const call = (method, params) => new Promise((resolve, reject) => {
  const i = String(nextId++);
  pending.set(i, { resolve, reject });
  ws.send(JSON.stringify({ type: 'request', id: i, method, params }));
  setTimeout(() => reject(new Error(`timeout: ${method}`)), 120_000);
});
const get = async (p, etag) => {
  const res = await fetch(`${HTTP}/img/${p.id}/256?v=${p.cacheKey}`, { headers: etag ? { 'If-None-Match': etag } : {} });
  await res.arrayBuffer();
  return { status: res.status, cc: res.headers.get('cache-control'), etag: res.headers.get('etag') };
};

await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('ws connect failed')); });
await call('Library.SetSidecarWrites', [false]);
const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
console.log(`folder: ${photos.length} photos (cold)`);

const camera = [];
for (const p of photos) {
  const r = await get(p);
  console.log(`  photo ${p.id}: 256 ${r.status} cc=${r.cc} etag=${r.etag}`);
  if (r.cc === 'no-cache') {
    camera.push({ p, etag: r.etag });
    check(r.status === 200 && r.etag?.startsWith('"camera-'), `camera 256 goes out no-cache with a tag (photo ${p.id})`);
    const again = await get(p, r.etag);
    check(again.status === 304, `asked again unchanged, it answers not modified (photo ${p.id}: ${again.status})`);
  }
}
check(camera.length > 0, `some photos showed the camera's 256 first (${camera.length} of ${photos.length})`);

const t = Date.now();
while (Date.now() - t < 180_000 && !camera.every(({ p }) => rendered.has(p.id))) {
  await new Promise((s) => setTimeout(s, 500));
}
for (const { p, etag } of camera) {
  check(rendered.has(p.id), `BaseRenderedEvent came for photo ${p.id}`);
  const r = await get(p, etag);
  check(r.status === 200 && (r.cc ?? '').includes('immutable'), `then the same URL serves the render, immutable (photo ${p.id}: ${r.status}, ${r.cc})`);
}
await call('Library.SetSidecarWrites', [true]);
console.log(failures ? `${failures} FAILED` : 'all passed');
process.exit(failures ? 1 : 0);
