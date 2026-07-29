// End-to-end check of the luminance/colour range mask against a running
// `marrawd --dev --port 8483`. Exercises: a range mask surviving Normalize,
// the committed render pipeline (ApplyMasks' rangeEval) changing pixels at a
// downscaled level AND a 1:1 tile, Invert, and the eyedropper RPC
// (Edits.PickRangeColor → developed-pixel sample → seeded hue window).
//
//   node scripts/rangemask-verify.mjs /tmp/marraw-fixture
//
// Point it at a DISPOSABLE copy of a shoot — it writes .marraw.json sidecars.

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/rangemask-verify.mjs <disposable-raw-folder>');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();

ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
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
    }, 30_000);
  });
}

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const step = (name) => console.log(name);

async function fetchImg(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`GET ${url} -> ${res.status}`);
  return Buffer.from(await res.arrayBuffer());
}
const isJpeg = (b) => b.length > 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff;

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
step(`OpenFolder -> ${photos.length} photos`);
if (photos.length < 1) throw new Error('need at least 1 RAW in the fixture');
const photo = photos[0];
const idOf = (p) => `${p.cacheKey}`;

// Baseline render (neutral) so we can prove the mask actually changes pixels.
await call('Edits.SetEditParams', [photo.id, {}]);
let cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const baseHash = cur.editHash;
const base512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${baseHash}`);
check(isJpeg(base512), `baseline 512 renders (${base512.length} B, hash ${baseHash})`);

// --- 1. A luminance-window range mask survives Normalize, advances the hash,
// keeps the cacheKey (masks are post-decode), and changes the render. ---
const lumaMask = {
  type: 'range',
  rangeLumaLo: 0.3, rangeLumaHi: 0.7, rangeHueLo: 0, rangeHueHi: 1,
  feather: 0.25, adjust: { expEV: -3 },
};
await call('Edits.SetEditParams', [photo.id, { masks: [lumaMask] }]);
const saved = await call('Edits.GetEditParams', [photo.id]);
check(
  saved.masks?.length === 1 && saved.masks[0].type === 'range' &&
    saved.masks[0].rangeLumaLo === 0.3 && saved.masks[0].rangeLumaHi === 0.7,
  'range mask survives Normalize with its luma window',
);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
step(`committed range mask; editHash ${baseHash} -> ${cur.editHash}`);
check(cur.editHash !== baseHash, 'a range mask advances the edit hash');
check(idOf(cur) === idOf(photo), 'cacheKey is unchanged (masks are post-decode)');
const luma512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(luma512), `range-mask 512 renders (${luma512.length} B)`);
check(!luma512.equals(base512), 'the luma-window mask changes the 512 render vs baseline');
const lumaTile = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/tile/0/0?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(lumaTile), `range-mask 1:1 tile (0,0) renders (${lumaTile.length} B)`);

// --- 2. Invert selects the complement — a different render. ---
await call('Edits.SetEditParams', [photo.id, { masks: [{ ...lumaMask, invert: true }] }]);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const inv512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(inv512), 'inverted range mask renders');
check(!inv512.equals(luma512), 'invert changes the selection (different render)');

// --- 3. Hue window: a narrow hue band renders (colour dimension active). ---
const hueMask = {
  type: 'range',
  rangeLumaLo: 0, rangeLumaHi: 1, rangeHueLo: 0.25, rangeHueHi: 0.45,
  rangeSatMin: 0.15, feather: 0.3, adjust: { saturation: -1, expEV: 1 },
};
await call('Edits.SetEditParams', [photo.id, { masks: [hueMask] }]);
cur = (await call('Library.ListPhotos', [info.folderId]))[0];
const hue512 = await fetchImg(`http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`);
check(isJpeg(hue512), `hue-window range mask renders (${hue512.length} B)`);

// --- 4. The eyedropper: PickRangeColor samples the developed colour and seeds
// the mask's hue window. Sweep a grid until a colourful point seeds a window
// (a real photo has colour somewhere; greys/darks are legitimately refused). ---
await call('Edits.SetEditParams', [photo.id, { masks: [hueMask] }]);
let seeded = null;
let refusals = 0;
outer: for (const y of [0.3, 0.5, 0.7]) {
  for (const x of [0.3, 0.5, 0.7]) {
    try {
      const params = await call('Edits.PickRangeColor', [photo.id, { masks: [hueMask] }, x, y, 0]);
      const m = params.masks[0];
      // A seeded window is narrower than the whole wheel (not 0..1) and carries
      // a saturation floor.
      if (!(m.rangeHueLo === 0 && m.rangeHueHi === 1)) {
        seeded = { x, y, m };
        break outer;
      }
    } catch {
      refusals++; // too-dark / too-grey patch: a valid refusal
    }
  }
}
if (seeded) {
  const { rangeHueLo: lo, rangeHueHi: hi, rangeSatMin: sat } = seeded.m;
  let width = hi - lo;
  if (width < 0) width += 1; // wrapped window
  step(`PickRangeColor seeded at (${seeded.x}, ${seeded.y}) -> hue [${lo?.toFixed(3)}, ${hi?.toFixed(3)}] (width ${width.toFixed(3)}), satMin ${sat?.toFixed(3)}`);
  check(true, 'PickRangeColor seeds a hue window from a photo colour');
  // The seeded window is the ±0.045 tolerance (~0.09 wide) centred on the pick.
  check(Math.abs(width - 0.09) < 1e-3, 'the seeded hue window is the ±0.045 tolerance');
  check((sat ?? -1) >= 0 && (sat ?? 1) < 1, 'the pick sets a valid saturation floor');
} else {
  step(`PickRangeColor refused every sampled point (${refusals} refusals)`);
  check(false, 'PickRangeColor seeds a hue window from a photo colour');
}

// The server-rendered tint (Edits.MaskTintPreview) returns a BINARY blob frame,
// which this JSON-only probe can't decode — it is exercised by the app and by
// the MaskWeightPlane range-coverage unit tests, not here.

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
