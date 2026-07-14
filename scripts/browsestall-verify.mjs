// Measures the develop-browse render path server-side: for freshly edited
// photos (hash changed, nothing rendered yet — the state every photo is in
// during an editing session until the next folder open), how expensive are
// the level renditions the loupe requests while browsing, and do aborted
// requests actually free the pipeline for the focused photo?
// Usage: node scripts/browsestall-verify.mjs "<disposable raw folder>"
const FOLDER = process.argv[2];
const PORT = process.env.MARRAW_PORT ?? 8483;
const HTTP = `http://127.0.0.1:${PORT}`;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
ws.binaryType = 'arraybuffer';
const pending = new Map();
let nextId = 1;
ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) {
    // Binary Blob result (PreviewEdit): 4-byte BE header len + JSON header.
    const headerLen = new DataView(ev.data).getUint32(0, false);
    const header = JSON.parse(new TextDecoder().decode(new Uint8Array(ev.data, 4, headerLen)));
    pending.get(header.id)?.resolve({ $binary: true });
    return;
  }
  const m = JSON.parse(ev.data);
  if (m.type === 'response') pending.get(m.id)?.resolve(m.result);
  else if (m.type === 'error') pending.get(m.id)?.reject(new Error(m.message));
};
const call = (method, params) => new Promise((resolve, reject) => {
  const i = String(nextId++);
  pending.set(i, { resolve, reject });
  ws.send(JSON.stringify({ type: 'request', id: i, method, params }));
  setTimeout(() => reject(new Error(`timeout: ${method}`)), 300_000);
});
const imgUrl = (p, level, hash) =>
  `${HTTP}/img/${p.id}/${level}?v=${p.cacheKey}&r=r8${hash && hash !== 'base' ? `&e=${hash}` : ''}`;

await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('ws connect failed')); });
// Sidecar writes off for the run: each SetEditParams would otherwise write a
// sidecar next to the RAWs, waking the watcher whose ingest pre-render warms
// the very renditions this probe wants cold (it confounded the first run).
await call('Library.SetSidecarWrites', [false]);
const info = await call('Library.OpenFolder', [FOLDER]);
let photos = await call('Library.ListPhotos', [info.folderId]);
console.log(`folder: ${photos.length} photos`);

// Let the folder-open passes settle, then reproduce the REAL editing flow on
// every photo: commit (SetEditParams) + the client's settle render
// (PreviewEdit longEdge 0 → ensurePreview → WritePreview), which warms ONLY
// the 2048 — the exact mid-session state edited photos live in.
await new Promise((s) => setTimeout(s, 5000));
for (const p of photos) {
  const base = (await call('Edits.GetEditParams', [p.id])) ?? {};
  const params = { ...base, contrast: 0.32 };
  await call('Edits.SetEditParams', [p.id, params]);
  await call('Edits.PreviewEdit', [p.id, params, 0]);
}
photos = await call('Library.ListPhotos', [info.folderId]);
const edited = photos.filter((p) => p.editHash && p.editHash !== 'base');
console.log(`edited+settled: ${edited.length} (hash sample ${edited[0]?.editHash})`);

const timed = async (p, level, opts = {}) => {
  const t = Date.now();
  const res = await fetch(imgUrl(p, level, p.editHash), opts).catch((e) => ({ status: `ERR ${e.name}` }));
  return { ms: Date.now() - t, status: res.status };
};

// 1. Single-photo economics after a commit: 2048 must be warm (the settle
// wrote it), and 512/1024 must now DERIVE from it (~tens of ms) instead of
// costing a fresh RAW decode (~2.5 s before the derive fast path).
{
  const p = edited[0];
  for (const level of ['2048', '512', '1024']) {
    const r = await timed(p, level);
    console.log(`photo0 ${level}: ${r.ms}ms (${r.status})`);
  }
}

// 2. Browse pile-up: fire 512+1024 for photos 1..8 (the underlay + fit pair a
// browse step requests), abort each pair after 120ms — the img.src='' skim —
// then measure the "focused" photo 9 end-to-end.
{
  for (const p of edited.slice(1, 9)) {
    const ac = new AbortController();
    fetch(imgUrl(p, '512', p.editHash), { signal: ac.signal }).catch(() => {});
    fetch(imgUrl(p, '1024', p.editHash), { signal: ac.signal }).catch(() => {});
    await new Promise((s) => setTimeout(s, 120));
    ac.abort();
  }
  const focus = edited[9] ?? edited[edited.length - 1];
  const r512 = await timed(focus, '512');
  const r1024 = await timed(focus, '1024');
  console.log(`focused after 8 aborted skims: 512 ${r512.ms}ms, 1024 ${r1024.ms}ms`);
}
await call('Library.SetSidecarWrites', [true]); // restore
ws.close();
process.exit(0);
