// End-to-end probe for Edits.SubjectBounds (the crop tool's auto crop)
// against a running marrawd: generates/loads the subject matte and checks the
// returned bounding box is a sane fractional rect, is idempotent, and follows
// the edit's orientation — a quarter turn / mirror of the params must permute
// the box exactly the way the crop rectangle itself would remap
// (client/src/lib/crop.ts rotateCropPatch / flipCropPatch).
// Usage: node scripts/subjectbounds-verify.mjs "<disposable raw folder>"
const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/subjectbounds-verify.mjs "<disposable raw folder>"');
  process.exit(1);
}
const PORT = process.env.MARRAW_PORT ?? 8483;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
ws.binaryType = 'arraybuffer';

const pending = new Map();
let nextId = 1;
ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  }
};

function call(method, params, timeoutMs = 300_000) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ type: 'request', id, method, params }));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout: ${method}`));
      }
    }, timeoutMs);
  });
}

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const near = (a, b, eps = 1e-6) => Math.abs(a - b) <= eps;
const fmt = (b) => `x=${b.x.toFixed(4)} y=${b.y.toFixed(4)} w=${b.w.toFixed(4)} h=${b.h.toFixed(4)}`;

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
console.log(`OpenFolder -> ${photos.length} photos`);
const p = photos[0];
const base = await call('Edits.GetEditParams', [p.id]);

// --- Base orientation ---
let t = Date.now();
const b0 = await call('Edits.SubjectBounds', [p.id, base, true]);
console.log(`SubjectBounds -> ${fmt(b0)} in ${Date.now() - t}ms`);
check(b0.found === true, 'subject found');
check(b0.w > 0 && b0.h > 0 && b0.w <= 1 && b0.h <= 1, `box has positive size (${fmt(b0)})`);
check(b0.x >= 0 && b0.y >= 0 && b0.x + b0.w <= 1 + 1e-9 && b0.y + b0.h <= 1 + 1e-9, 'box inside the frame');
check(b0.w < 1 || b0.h < 1, 'box is not the whole frame');

t = Date.now();
const b1 = await call('Edits.SubjectBounds', [p.id, base, true]);
check(Date.now() - t < 2000, `second call rides the cached matte (${Date.now() - t}ms)`);
check(b1.x === b0.x && b1.y === b0.y && b1.w === b0.w && b1.h === b0.h, 'idempotent');

// --- One CW quarter turn: the frame point map is (x,y) -> (1-y, x), so the
// box must land at x' = 1-(y+h), y' = x with w/h swapped. ---
const cw = await call('Edits.SubjectBounds', [p.id, { ...base, rotate: 1 }, true]);
console.log(`SubjectBounds rotate=1 -> ${fmt(cw)}`);
check(
  near(cw.x, 1 - (b0.y + b0.h)) && near(cw.y, b0.x) && near(cw.w, b0.h) && near(cw.h, b0.w),
  'quarter-turn box is the exact CW remap of the base box',
);

// --- Mirror: FlipH reflects about the vertical axis, x' = 1-(x+w). ---
const fl = await call('Edits.SubjectBounds', [p.id, { ...base, flipH: true }, true]);
console.log(`SubjectBounds flipH -> ${fmt(fl)}`);
check(
  near(fl.x, 1 - (b0.x + b0.w)) && near(fl.y, b0.y) && near(fl.w, b0.w) && near(fl.h, b0.h),
  'mirrored box is the exact FlipH remap of the base box',
);

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
