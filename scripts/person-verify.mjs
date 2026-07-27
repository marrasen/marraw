// End-to-end probe for per-person instance masks against a running marrawd:
// runs real RF-DETR-Seg inference via Edits.GenerateAIMap (person), then
// seeds a synthetic two-person plane (the subjsharp-verify precedent) for
// the deterministic checks — instance chips, the AIMapPlane hit-test
// plane, per-instance tint separation, and render behavior — so the checks
// hold even when the fixture photo contains no people.
// Usage: node scripts/person-verify.mjs "<disposable raw folder>"
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { deflateSync, inflateSync } from 'node:zlib';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/person-verify.mjs "<disposable raw folder>"');
  process.exit(1);
}
const PORT = process.env.MARRAW_PORT ?? 8483;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
ws.binaryType = 'arraybuffer';

const pending = new Map();
let nextId = 1;
ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) {
    const view = new DataView(ev.data);
    const headerLen = view.getUint32(0, false);
    const header = JSON.parse(new TextDecoder().decode(new Uint8Array(ev.data, 4, headerLen)));
    const payload = new Uint8Array(ev.data, 4 + headerLen);
    pending.get(header.id)?.resolve({ $binary: true, contentType: header.contentType, bytes: payload });
    pending.delete(header.id);
    return;
  }
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

// decodeGrayPNG parses an 8-bit grayscale PNG (any scanline filter — Go's
// encoder picks adaptively) into { width, height, pix }.
function decodeGrayPNG(bytes) {
  const buf = Buffer.from(bytes);
  let off = 8;
  let width = 0;
  let height = 0;
  const idat = [];
  while (off < buf.length) {
    const len = buf.readUInt32BE(off);
    const type = buf.toString('ascii', off + 4, off + 8);
    const data = buf.subarray(off + 8, off + 8 + len);
    if (type === 'IHDR') {
      width = data.readUInt32BE(0);
      height = data.readUInt32BE(4);
      if (data[8] !== 8 || data[9] !== 0) throw new Error(`not 8-bit grayscale (depth ${data[8]} color ${data[9]})`);
    } else if (type === 'IDAT') {
      idat.push(data);
    }
    off += 12 + len;
  }
  const raw = inflateSync(Buffer.concat(idat));
  const pix = Buffer.alloc(width * height);
  const stride = width + 1;
  for (let y = 0; y < height; y++) {
    const filter = raw[y * stride];
    for (let x = 0; x < width; x++) {
      const v = raw[y * stride + 1 + x];
      const a = x > 0 ? pix[y * width + x - 1] : 0; // left
      const b = y > 0 ? pix[(y - 1) * width + x] : 0; // up
      const c = x > 0 && y > 0 ? pix[(y - 1) * width + x - 1] : 0; // up-left
      let out;
      switch (filter) {
        case 0: out = v; break;
        case 1: out = v + a; break;
        case 2: out = v + b; break;
        case 3: out = v + ((a + b) >> 1); break;
        case 4: {
          const p = a + b - c;
          const pa = Math.abs(p - a);
          const pb = Math.abs(p - b);
          const pc = Math.abs(p - c);
          out = v + (pa <= pb && pa <= pc ? a : pb <= pc ? b : c);
          break;
        }
        default: throw new Error(`unsupported filter ${filter}`);
      }
      pix[y * width + x] = out & 0xff;
    }
  }
  return { width, height, pix };
}

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
console.log(`OpenFolder -> ${photos.length} photos`);
if (photos.length < 3) {
  console.error('need >= 3 photos in the fixture folder');
  process.exit(1);
}
const p = photos[0];
const base = await call('Edits.GetEditParams', [p.id]);

// --- 1. Real inference on the fixture photo ---
let t = Date.now();
const gen = await call('Edits.GenerateAIMap', [p.id, 'person', true]);
console.log(`GenerateAIMap person -> ${gen.mapVer} in ${Date.now() - t}ms:`,
  (gen.instances ?? []).map((i) => `#${i.id} ${(i.fraction * 100).toFixed(1)}% cx${i.cx.toFixed(2)}`).join(', ') || '(no people)');
check(gen.mapVer === 'rfdetrseg-1', `person mapVer is rfdetrseg-1 (${gen.mapVer})`);

t = Date.now();
const gen2 = await call('Edits.GenerateAIMap', [p.id, 'person', true]);
check(gen2.mapVer === gen.mapVer && Date.now() - t < 2000, `second call idempotent + fast (${Date.now() - t}ms)`);
check(gen2.generated === false, `cached map reports generated=false (${gen2.generated})`);
const insA = gen2.instances ?? [];
check(insA.every((v, i) => i === 0 || insA[i - 1].cx <= v.cx), 'instances sorted left-to-right');

// The real plane is fetchable and dimensioned like a 1024-long-edge map.
const realPlane = decodeGrayPNG((await call('Edits.AIMapPlane', [p.id, 'person', base])).bytes);
check(Math.max(realPlane.width, realPlane.height) === 1024,
  `instance plane long edge is 1024 (${realPlane.width}x${realPlane.height})`);
{
  const ids = new Set(realPlane.pix.filter((v) => v > 0));
  check(ids.size === insA.length && insA.every((i) => ids.has(i.id)),
    `plane ID set matches instances (${[...ids].join(',') || 'empty'} vs ${insA.map((i) => i.id).join(',') || 'none'})`);
}

// --- 2. Missing map is a clean error, never a download ---
const missErr = await call('Edits.AIMapPlane', [photos[2].id, 'person', base]).then(() => null, (e) => e);
check(missErr != null && /no person map/.test(missErr.message), `missing map errors cleanly (${missErr?.message})`);

// --- 3. Seed a deterministic two-person plane for photo[1] ---
const q = photos[1];
const modelsDir = (await call('System.GetModelsInfo', [])).dir;
const aimapsDir = join(modelsDir, '..', 'aimaps');
const planeDims = (ph) => {
  const swap = ph.orientation === 5 || ph.orientation === 6;
  const [w, h] = swap ? [ph.height, ph.width] : [ph.width, ph.height];
  return w >= h ? [1024, Math.max(1, Math.round((1024 * h) / w))] : [Math.max(1, Math.round((1024 * w) / h)), 1024];
};
const [pw, ph] = planeDims(q);
const planePath = join(aimapsDir, q.cacheKey.slice(0, 2), `${q.cacheKey}_ai-person_rfdetrseg-1.png`);
mkdirSync(join(aimapsDir, q.cacheKey.slice(0, 2)), { recursive: true });
// Person 1: left-quarter column; person 2: right-quarter column (order
// matches the left-to-right ID convention DetectInstances reports).
const inLeft = (x, y) => x > pw * 0.1 && x < pw * 0.3 && y > ph * 0.2 && y < ph * 0.9;
const inRight = (x, y) => x > pw * 0.7 && x < pw * 0.9 && y > ph * 0.2 && y < ph * 0.9;
writeFileSync(planePath, grayPNG(pw, ph, (x, y) => (inLeft(x, y) ? 1 : inRight(x, y) ? 2 : 0)));
console.log(`seeded two-person plane ${pw}x${ph} for ${q.fileName}`);

try {
  const qbase = await call('Edits.GetEditParams', [q.id]);
  const seeded = await call('Edits.GenerateAIMap', [q.id, 'person', true]);
  const ins = seeded.instances ?? [];
  check(seeded.generated === false, 'seeded map hits the fast path (no inference)');
  check(ins.length === 2 && ins[0].id === 1 && ins[1].id === 2, `two instances reported (${JSON.stringify(ins)})`);
  check(ins.length === 2 && ins[0].cx < 0.5 && ins[1].cx > 0.5, 'centroids on the expected sides');

  // The oriented plane round-trips: same IDs, and rotate swaps the dims.
  const plane = decodeGrayPNG((await call('Edits.AIMapPlane', [q.id, 'person', qbase])).bytes);
  check(plane.width === pw && plane.height === ph, `plane dims match (${plane.width}x${plane.height})`);
  check(plane.pix[Math.round(ph * 0.5) * pw + Math.round(pw * 0.2)] === 1, 'left person samples as ID 1');
  check(plane.pix[Math.round(ph * 0.5) * pw + Math.round(pw * 0.8)] === 2, 'right person samples as ID 2');
  const rotated = decodeGrayPNG((await call('Edits.AIMapPlane', [q.id, 'person', { ...qbase, rotate: 1 }])).bytes);
  check(rotated.width === ph && rotated.height === pw, `rotated plane swaps dims (${rotated.width}x${rotated.height})`);

  // --- 4. Per-instance tints are non-empty and differ from each other ---
  const maskFor = (id) => ({ type: 'ai', aiKind: 'person', mapVer: seeded.mapVer, classId: id, feather: 0.15, adjust: {} });
  const tintParams = (id) => [q.id, { ...qbase, masks: [...(qbase.masks ?? []), maskFor(id)] }, (qbase.masks ?? []).length, 1024];
  const tint1 = await call('Edits.MaskTintPreview', tintParams(1));
  const tint2 = await call('Edits.MaskTintPreview', tintParams(2));
  check(tint1.$binary && tint1.bytes.length > 1000, `person 1 tint renders (${tint1.bytes.length}B)`);
  check(Buffer.compare(Buffer.from(tint1.bytes), Buffer.from(tint2.bytes)) !== 0, 'person 1 and 2 tints differ');

  // --- 5. Render behavior: a person mask changes pixels; bogus ver no-ops ---
  const plain = await call('Edits.PreviewEdit', [q.id, qbase, 1024]);
  const boosted = await call('Edits.PreviewEdit', [
    q.id, { ...qbase, masks: [{ ...maskFor(1), adjust: { expEV: 1.5 } }] }, 1024,
  ]);
  check(Buffer.compare(Buffer.from(plain.bytes), Buffer.from(boosted.bytes)) !== 0, 'person mask changes preview pixels');
  const other = await call('Edits.PreviewEdit', [
    q.id, { ...qbase, masks: [{ ...maskFor(2), adjust: { expEV: 1.5 } }] }, 1024,
  ]);
  check(Buffer.compare(Buffer.from(boosted.bytes), Buffer.from(other.bytes)) !== 0, 'the two people mask differently');
  const stale = await call('Edits.PreviewEdit', [
    q.id, { ...qbase, masks: [{ ...maskFor(1), mapVer: 'rfdetrseg-999', adjust: { expEV: 1.5 } }] }, 1024,
  ]);
  check(Buffer.compare(Buffer.from(stale.bytes), Buffer.from(plain.bytes)) === 0, 'missing-version map renders as no-op');
} finally {
  // Never leave a fake person plane in the store — later real runs would
  // silently serve it (the subject-matte fake-weights lesson).
  if (existsSync(planePath)) rmSync(planePath);
  console.log('removed seeded plane');
}

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
