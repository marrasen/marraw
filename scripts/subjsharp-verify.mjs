// End-to-end check of subject-aware sharpness against a running marrawd.
// Real inference would need the 178 MB ISNet download, so this seeds
// synthetic subject mattes straight into the daemon's aimaps store (the
// models-verify seeding precedent) and expects the calibrate pass to
// backfill subjectSharpness on the next folder open: a half-frame matte
// yields a score, an empty matte yields the hidden "no subject" sentinel,
// and unseeded photos stay untouched.
//
//   node scripts/subjsharp-verify.mjs "<disposable raw folder>"
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { deflateSync } from 'node:zlib';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/subjsharp-verify.mjs "<disposable raw folder>"');
  process.exit(1);
}
const PORT = process.env.MARRAW_PORT ?? 8483;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);

const pending = new Map();
let nextId = 1;
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

function call(method, params, timeoutMs = 60_000) {
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
const step = (name) => console.log(name);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// grayPNG encodes a width×height 8-bit grayscale PNG (filter 0 per scanline).
function grayPNG(width, height, pixAt) {
  const raw = Buffer.alloc(height * (width + 1));
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) raw[y * (width + 1) + 1 + x] = pixAt(x, y);
  }
  const chunk = (type, data) => {
    const out = Buffer.alloc(12 + data.length);
    out.writeUInt32BE(data.length, 0);
    out.write(type, 4, 'ascii');
    data.copy(out, 8);
    let crc = 0xffffffff;
    for (let i = 4; i < 8 + data.length; i++) {
      crc ^= out[i];
      for (let b = 0; b < 8; b++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
    }
    out.writeUInt32BE((crc ^ 0xffffffff) >>> 0, 8 + data.length);
    return out;
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 0; // grayscale
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

// --- 1. Open the folder and let the calibrate pass finish (all photos get a
// global sharpness score). ---
step('1. open folder, wait for baseline sharpness');
const info = await call('Library.OpenFolder', [FOLDER]);
let photos;
for (let i = 0; i < 120; i++) {
  photos = await call('Library.ListPhotos', [info.folderId]);
  if (photos.length && photos.every((p) => p.sharpness != null)) break;
  await sleep(1000);
}
check(photos.length >= 3, `folder has >= 3 photos (${photos.length})`);
check(photos.every((p) => p.sharpness != null), 'every photo has a global sharpness score');
check(photos.every((p) => p.subjectSharpness == null), 'no photo has subjectSharpness before a matte exists');

// --- 2. Seed mattes into the daemon's aimaps store: photo[0] gets a
// half-frame subject, photo[1] an empty (subjectless) matte. ---
step('2. seed synthetic subject mattes');
const modelsDir = (await call('System.GetModelsInfo', [])).dir;
const aimapsDir = join(modelsDir, '..', 'aimaps');
const mattePath = (p) => {
  const dir = join(aimapsDir, p.cacheKey.slice(0, 2));
  mkdirSync(dir, { recursive: true });
  return join(dir, `${p.cacheKey}_ai-subject_isnet-1.png`);
};
const matteDims = (p) => {
  const swap = p.orientation === 5 || p.orientation === 6;
  const [w, h] = swap ? [p.height, p.width] : [p.width, p.height];
  return w >= h ? [1024, Math.max(1, Math.round((1024 * h) / w))] : [Math.max(1, Math.round((1024 * w) / h)), 1024];
};
{
  const [w, h] = matteDims(photos[0]);
  writeFileSync(mattePath(photos[0]), grayPNG(w, h, (x) => (x < w / 2 ? 255 : 0)));
  step(`   seeded half-frame matte ${w}x${h} for ${photos[0].fileName}`);
}
{
  const [w, h] = matteDims(photos[1]);
  writeFileSync(mattePath(photos[1]), grayPNG(w, h, () => 0));
  step(`   seeded empty matte ${w}x${h} for ${photos[1].fileName}`);
}

// --- 3. Reopen the folder: the calibrate pass must pick the mattes up and
// backfill subject sharpness for the seeded photo only. ---
step('3. reopen folder, wait for subject-sharpness backfill');
const [seededId, emptyId, otherId] = photos.map((p) => p.id);
await call('Library.OpenFolder', [FOLDER]);
let seeded;
for (let i = 0; i < 60; i++) {
  photos = await call('Library.ListPhotos', [info.folderId]);
  seeded = photos.find((p) => p.id === seededId);
  if (seeded?.subjectSharpness != null) break;
  await sleep(1000);
}
check(seeded?.subjectSharpness != null, `seeded photo got subjectSharpness (${seeded?.subjectSharpness?.toFixed?.(1)})`);
check(seeded?.subjectSharpness > 0, 'subject score is positive');
check(photos.find((p) => p.id === emptyId)?.subjectSharpness == null, 'empty-matte photo reports no subject score (sentinel hidden)');
check(photos.find((p) => p.id === otherId)?.subjectSharpness == null, 'unseeded photo reports no subject score');

console.log(failures ? `\n${failures} FAILURE(S)` : '\nALL PASS');
ws.close();
process.exit(failures ? 1 : 0);
