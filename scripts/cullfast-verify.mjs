// End-to-end check of the cull fast path against a running `marrawd --dev`:
// on a COLD folder (fresh cache keys, pre-render still grinding), ?fast=1
// requests must answer with embedded-JPEG pixels in tens of milliseconds —
// provisional, no-store — while never triggering a visible-priority RAW
// render; and once the real 2048 lands, the same URL serves it immutable.
// Usage: node scripts/cullfast-verify.mjs "<disposable cold raw folder>"
const FOLDER = process.argv[2];
const PORT = process.env.MARRAW_PORT ?? 8483;
const HTTP = `http://127.0.0.1:${PORT}`;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
const pending = new Map();
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
};
const call = (method, params) => new Promise((resolve, reject) => {
  const i = String(nextId++);
  pending.set(i, { resolve, reject });
  ws.send(JSON.stringify({ type: 'request', id: i, method, params }));
  setTimeout(() => reject(new Error(`timeout: ${method}`)), 120_000);
});
const imgUrl = (p, level, extra = '') =>
  `${HTTP}/img/${p.id}/${level}?v=${p.cacheKey}${p.editHash && p.editHash !== 'base' ? `&e=${p.editHash}` : ''}${extra}`;
const timed = async (url) => {
  const t = Date.now();
  const res = await fetch(url);
  const body = await res.arrayBuffer();
  return { ms: Date.now() - t, status: res.status, bytes: body.byteLength,
    cc: res.headers.get('cache-control'), prov: res.headers.get('x-marraw-provisional') };
};

await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('ws connect failed')); });
await call('Library.SetSidecarWrites', [false]);
const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
console.log(`folder: ${photos.length} photos (cold: pre-render just started)`);

// Phase A — the cull skim, cold, WHILE the pre-render pass owns the decode
// pool: every fast request must come back fast with provisional pixels.
for (const p of photos) {
  const sharp = await timed(imgUrl(p, '2048', '&fast=1'));
  const under = await timed(imgUrl(p, '512', '&stale=1&fast=1'));
  console.log(`  photo ${p.id}: fast 2048 ${sharp.ms}ms (${sharp.status}, ${sharp.bytes}b, cc=${sharp.cc}, prov=${sharp.prov}) | stale+fast 512 ${under.ms}ms (${under.status}, prov=${under.prov})`);
  check(sharp.status === 200, `cold fast 2048 answers 200 (photo ${p.id})`);
  check(sharp.prov === '1' && sharp.cc === 'no-store', `cold fast 2048 is a no-store provisional (photo ${p.id})`);
  check(sharp.ms < 1500, `cold fast 2048 under 1.5s even mid-prerender (photo ${p.id}: ${sharp.ms}ms)`);
  check(under.status === 200, `cold stale+fast 512 answers 200 (photo ${p.id})`);
}
// Second pass: derivation cached on disk now — this is the per-keystroke cost.
{
  const p = photos[0];
  const again = await timed(imgUrl(p, '2048', '&fast=1'));
  console.log(`  photo ${p.id}: fast 2048 revisit ${again.ms}ms`);
  check(again.ms < 200, `revisit serves the on-disk provisional instantly (${again.ms}ms)`);
}

// Phase B — wait for the real 2048 (pre-render pass), then the same fast URL
// must serve it immutable, no provisional flag.
{
  const p = photos[0];
  const t = Date.now();
  let r;
  while (Date.now() - t < 120_000) {
    r = await timed(imgUrl(p, '2048', '&fast=1'));
    if (r.prov !== '1') break;
    await new Promise((s) => setTimeout(s, 1000));
  }
  console.log(`  photo ${p.id}: post-prerender fast 2048 (${r.status}, cc=${r.cc}, prov=${r.prov})`);
  check(r.status === 200 && r.prov === null && (r.cc ?? '').includes('immutable'),
    'real 2048 replaces the provisional under the same URL, immutable');
}
await call('Library.SetSidecarWrites', [true]);
ws.close();
console.log(failures ? `\n${failures} FAILURES` : '\nALL PASS');
process.exit(failures ? 1 : 0);
