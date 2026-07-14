// Verifies the stale-while-revalidate bridge: a missing rendition requested
// with stale=1 serves the photo's freshest same-level rendition (no-store),
// while the plain request 404s for an unknown hash. Warm exact requests stay
// immutable. Usage: node scripts/stale-probe.mjs "<raw folder>"
const FOLDER = process.argv[2];
const ws = new WebSocket('ws://127.0.0.1:8483/ws');
const pending = new Map();
let id = 1;
ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const m = JSON.parse(ev.data);
  if (m.type === 'response') pending.get(m.id)?.(m.result);
};
const call = (method, params) => new Promise((res) => {
  const i = String(id++);
  pending.set(i, res);
  ws.send(JSON.stringify({ type: 'request', id: i, method, params }));
});
let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
ws.onopen = async () => {
  const info = await call('Library.OpenFolder', [FOLDER]);
  const photos = await call('Library.ListPhotos', [info.folderId]);
  const p = photos[0];
  const url = (hash, extra = '') => `http://127.0.0.1:8483/img/${p.id}/512?v=${p.cacheKey}&r=r8&e=${hash}${extra}`;

  // Bogus hash = "rendition missing and not generatable".
  const missing = await fetch(url('feedfacefeed'));
  check(missing.status === 404, `plain unknown-hash request 404s (${missing.status})`);

  const t = Date.now();
  const stale = await fetch(url('feedfacefeed', '&stale=1'));
  const body = await stale.arrayBuffer();
  check(stale.status === 200 && body.byteLength > 5000,
    `stale request serves a real rendition in ${Date.now() - t}ms (${stale.status}, ${body.byteLength}B)`);
  check(stale.headers.get('cache-control') === 'no-store',
    `stale response is no-store (${stale.headers.get('cache-control')})`);

  // Warm exact request stays immutable.
  const exact = await fetch(`http://127.0.0.1:8483/img/${p.id}/512?v=${p.cacheKey}&r=r8`);
  check(exact.status === 200 && (exact.headers.get('cache-control') ?? '').includes('immutable'),
    `exact warm request stays immutable (${exact.headers.get('cache-control')})`);

  console.log(failures === 0 ? 'ALL CHECKS PASSED' : `${failures} FAILED`);
  process.exit(failures === 0 ? 0 : 1);
};
