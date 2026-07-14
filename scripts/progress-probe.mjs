// One-shot: a cold, visible-priority level render must emit RenderProgress
// events (the browse chip's determinate source). Usage:
//   node scripts/progress-probe.mjs "<disposable raw folder>"
const FOLDER = process.argv[2];
const ws = new WebSocket('ws://127.0.0.1:8483/ws');
ws.binaryType = 'arraybuffer';
const pending = new Map();
const events = [];
let id = 1;
ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) return;
  const m = JSON.parse(ev.data);
  if (m.type === 'response') pending.get(m.id)?.(m.result);
  else if (m.type === 'push' && m.event === 'RenderProgressEvent') events.push(m.data);
};
const call = (method, params) => new Promise((res) => {
  const i = String(id++);
  pending.set(i, res);
  ws.send(JSON.stringify({ type: 'request', id: i, method, params }));
});
ws.onopen = async () => {
  await call('Library.SetSidecarWrites', [false]);
  const info = await call('Library.OpenFolder', [FOLDER]);
  const photos = await call('Library.ListPhotos', [info.folderId]);
  const p = photos[photos.length - 1];
  const base = (await call('Edits.GetEditParams', [p.id])) ?? {};
  await call('Edits.SetEditParams', [p.id, { ...base, contrast: 0.33 }]);
  const fresh = (await call('Library.ListPhotos', [info.folderId])).find((x) => x.id === p.id);
  // Cold browse request: 512 at visible priority; no settle ran for this hash.
  const t = Date.now();
  const r = await fetch(`http://127.0.0.1:8483/img/${p.id}/512?v=${fresh.cacheKey}&r=r8&e=${fresh.editHash}`);
  const mine = events.filter((e) => e.photoId === p.id);
  console.log(`cold 512: ${Date.now() - t}ms (${r.status}); progress events: ${mine.length}`);
  console.log('fractions:', mine.map((e) => e.fraction.toFixed(2)).join(' '));
  await call('Library.SetSidecarWrites', [true]);
  process.exit(mine.length >= 3 ? 0 : 1);
};
