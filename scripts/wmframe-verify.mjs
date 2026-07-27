// End-to-end check of the rect-element + polaroid-frame watermark path
// against a running `marrawd --dev --port 8483`. Exercises: SetWatermarks
// round-trips the new fields, RenderClipboard with a framed watermark hits
// the requested long edge exactly on the framed canvas, longEdge 0 grows the
// canvas around the full-res photo, and a rect-only watermark keeps the
// photo's own dimensions. The pre-existing watermark list is restored.
//
//   node scripts/wmframe-verify.mjs /tmp/marraw-fixture

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/wmframe-verify.mjs <raw-folder>');
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

const pngDims = (bytes) => {
  const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  return { w: v.getUint32(16, false), h: v.getUint32(20, false) };
};

// Twin of Frame.Layout (internal/watermark/frame.go) for the expected dims.
function frameLayout(pw, ph, longEdge, b, c) {
  const a = pw / ph;
  if (longEdge > 0) {
    const hf = longEdge / (a * (1 - 2 * b - c) + 2 * b);
    if (hf <= longEdge) {
      const sf = Math.round(hf);
      return { w: longEdge, h: sf };
    }
    const sf = Math.round((a * longEdge) / (1 - 2 * b + a * (2 * b + c)));
    return { w: sf, h: longEdge };
  }
  return null;
}

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
console.log(`OpenFolder -> ${photos.length} photos`);
const p = photos[0];

const prior = (await call('Settings.GetUISettings', [])).watermarks ?? [];
console.log(`existing watermarks preserved: ${prior.length}`);

const WM_ID = 'wmframe-verify';
const testWm = {
  id: WM_ID,
  name: 'wmframe verify',
  elements: [
    {
      id: 'rect1', type: 'rect', text: '', font: 'sans',
      color: '#000000', asset: '', assetWidth: 0, assetHeight: 0,
      fill: 'gradient', color2: '#000000', opacity2: 0, gradientDir: 'up',
      widthPct: 100, heightPct: 20,
      anchor: 'bottom', sizePct: 4, marginPct: 0, opacity: 0.6,
    },
  ],
  frame: { enabled: true, widthPct: 5, bottomPct: 15, color: '#fafaf0' },
};

try {
  await call('Settings.SetWatermarks', [[...prior.filter((w) => w.id !== WM_ID), testWm]]);

  // --- 1. Round-trip: the new fields survive normalization. ---
  const stored = (await call('Settings.GetUISettings', [])).watermarks.find((w) => w.id === WM_ID);
  check(!!stored, 'test watermark stored');
  check(stored?.frame?.enabled === true, 'frame enabled round-trips');
  check(stored?.frame?.widthPct === 5 && stored?.frame?.bottomPct === 15, 'frame geometry round-trips');
  check(stored?.frame?.color === '#fafaf0', 'frame color round-trips');
  const el = stored?.elements?.[0];
  check(el?.type === 'rect' && el?.fill === 'gradient' && el?.gradientDir === 'up', 'rect fields round-trip');
  check(el?.opacity2 === 0 && el?.widthPct === 100 && el?.heightPct === 20, 'rect geometry round-trips');

  // --- 2. Framed render at longEdge 1000: framed canvas hits it exactly. ---
  let res = await call('Export.RenderClipboard', [
    { photoId: p.id, longEdge: 1000, sharpenTarget: 'screen', sharpenAmount: 'standard', watermarkId: WM_ID },
  ]);
  let dims = pngDims(res.bytes);
  console.log(`  framed render: ${dims.w}x${dims.h} (photo ${p.width}x${p.height})`);
  check(Math.max(dims.w, dims.h) === 1000, `framed long edge exactly 1000 (${dims.w}x${dims.h})`);
  const want = frameLayout(p.width, p.height, 1000, 0.05, 0.15);
  check(
    Math.abs(dims.w - want.w) <= 1 && Math.abs(dims.h - want.h) <= 1,
    `framed dims match the layout solve ${want.w}x${want.h}`,
  );

  // --- 3. Framed render at longEdge 0 grows the canvas around full res. ---
  res = await call('Export.RenderClipboard', [
    { photoId: p.id, longEdge: 0, sharpenTarget: 'off', sharpenAmount: '', watermarkId: WM_ID },
  ]);
  dims = pngDims(res.bytes);
  console.log(`  full-res framed render: ${dims.w}x${dims.h}`);
  check(
    dims.w > p.width && dims.h > p.height,
    `full-res framed canvas grows beyond the photo (${dims.w}x${dims.h} > ${p.width}x${p.height})`,
  );

  // --- 4. Rect-only watermark (frame off) keeps the photo's dims. ---
  await call('Settings.SetWatermarks', [
    [...prior.filter((w) => w.id !== WM_ID), { ...testWm, frame: { ...testWm.frame, enabled: false } }],
  ]);
  res = await call('Export.RenderClipboard', [
    { photoId: p.id, longEdge: 800, sharpenTarget: 'off', sharpenAmount: '', watermarkId: WM_ID },
  ]);
  dims = pngDims(res.bytes);
  console.log(`  rect-only render: ${dims.w}x${dims.h}`);
  check(Math.max(dims.w, dims.h) === 800, `rect-only render keeps plain resize dims (${dims.w}x${dims.h})`);
} finally {
  await call('Settings.SetWatermarks', [prior]);
  console.log('restored prior watermark list');
}

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
