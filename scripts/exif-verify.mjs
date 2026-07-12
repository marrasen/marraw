// End-to-end check of the export metadata option against a running
// `marrawd --dev --port 8483`. Exercises: exifMode all/copyright/none across
// JPEG, PNG, and TIFF, the Artist/Copyright credit, lens extraction landing
// in LensModel, and the persisted export-options round trip.
//
//   node scripts/exif-verify.mjs "D:\Photos\marraw-exif-fixture"
//
// Point it at a DISPOSABLE copy of a shoot. GPS assertions are conditional:
// the usual fixtures come from bodies without GPS, so the GPS write path is
// covered by the Go unit tests and this script only reports what it saw.

import { mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/exif-verify.mjs <disposable-raw-folder>');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
const pushes = [];

ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  } else if (msg.type === 'push') {
    pushes.push(msg);
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
    }, 120_000);
  });
}

async function waitTask(taskId, timeoutMs = 120_000) {
  const t = Date.now();
  while (Date.now() - t < timeoutMs) {
    for (const m of pushes) {
      if (m.event !== 'TaskStateEvent') continue;
      const task = m.data.tasks?.find((x) => x.id === taskId);
      if (task && (task.status === 'completed' || task.status === 'failed')) return task;
    }
    await new Promise((s) => setTimeout(s, 100));
  }
  throw new Error('timeout waiting for export task');
}

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const step = (name) => console.log(name);

// ---- tiny TIFF/EXIF reader (little-endian, what marraw writes) ----

// parseTiffBlock reads IFD0 at buf[base:] and follows the Exif (34665) and
// GPS (34853) pointers. Values stay raw: { typ, count, value } with inline
// small values, offsets otherwise.
function parseTiffBlock(buf, base) {
  if (buf.toString('latin1', base, base + 2) !== 'II' || buf.readUInt16LE(base + 2) !== 42) {
    throw new Error('bad TIFF header');
  }
  const readIFD = (off) => {
    const n = buf.readUInt16LE(base + off);
    const fields = new Map();
    let prev = -1;
    for (let i = 0; i < n; i++) {
      const e = base + off + 2 + 12 * i;
      const tag = buf.readUInt16LE(e);
      if (tag <= prev) throw new Error(`IFD out of order: ${tag} after ${prev}`);
      prev = tag;
      fields.set(tag, {
        typ: buf.readUInt16LE(e + 2),
        count: buf.readUInt32LE(e + 4),
        value: buf.readUInt32LE(e + 8),
      });
    }
    return fields;
  };
  const ifd0 = readIFD(buf.readUInt32LE(base + 4));
  const exif = ifd0.has(34665) ? readIFD(ifd0.get(34665).value) : new Map();
  const gps = ifd0.has(34853) ? readIFD(ifd0.get(34853).value) : new Map();
  return { ifd0, exif, gps };
}

function ascii(buf, base, f) {
  if (!f) return undefined;
  if (f.count <= 4) {
    const b = Buffer.alloc(4);
    b.writeUInt32LE(f.value);
    return b.toString('latin1', 0, f.count).replace(/\0+$/, '');
  }
  return buf.toString('latin1', base + f.value, base + f.value + f.count).replace(/\0+$/, '');
}

// exifFromJpeg returns the parsed APP1 Exif block, or null if there is none.
function exifFromJpeg(buf) {
  let i = 2; // past SOI
  while (i + 4 <= buf.length && buf[i] === 0xff) {
    const marker = buf[i + 1];
    if (marker === 0xda) break; // SOS: entropy data follows
    const len = buf.readUInt16BE(i + 2);
    if (marker === 0xe1 && buf.toString('latin1', i + 4, i + 10) === 'Exif\0\0') {
      return { buf, base: i + 10, ...parseTiffBlock(buf, i + 10) };
    }
    i += 2 + len;
  }
  return null;
}

function exifFromPng(buf) {
  let i = 8;
  while (i + 8 <= buf.length) {
    const len = buf.readUInt32BE(i);
    const type = buf.toString('latin1', i + 4, i + 8);
    if (type === 'eXIf') return { buf, base: i + 8, ...parseTiffBlock(buf, i + 8) };
    i += 12 + len;
  }
  return null;
}

// ---- run ----

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
let photos = await call('Library.ListPhotos', [info.folderId]);
step(`OpenFolder -> ${photos.length} photos`);

// The metadata pass (which now also reads lens + GPS) runs in the background;
// exports read the catalog, so wait for it.
for (let i = 0; i < 100 && !photos.every((p) => p.metaLoaded); i++) {
  await new Promise((s) => setTimeout(s, 200));
  photos = await call('Library.ListPhotos', [info.folderId]);
}
check(photos.every((p) => p.metaLoaded), 'metadata pass finished');
const p = photos[0];

const dest = mkdtempSync(join(tmpdir(), 'marraw-exif-'));
const ARTIST = 'Test Artist';
const COPYRIGHT = '(c) 2026 Test Artist';

async function exportOne(name, extra) {
  const ref = await call('Export.StartExport', [
    { photoIds: [p.id], destDir: dest, fileNameTemplate: name, createDir: false, ...extra },
  ]);
  const task = await waitTask(ref.taskId);
  if (task.status !== 'completed') throw new Error(`export ${name} ${task.status}`);
  const file = readdirSync(dest).find((f) => f.startsWith(name));
  return readFileSync(join(dest, file));
}

// --- 1. mode=all (default when omitted) carries everything + the credit. ---
step('exifMode: all');
const all = exifFromJpeg(
  await exportOne('all', { format: 'jpeg', exifMode: 'all', artist: ARTIST, copyright: COPYRIGHT }),
);
check(!!all, 'JPEG has an Exif APP1');
if (all) {
  const make = ascii(all.buf, all.base, all.ifd0.get(271));
  check(!!make && make === p.make, `Make matches the catalog ("${make}")`);
  check(!!all.ifd0.get(306), 'DateTime present');
  check(ascii(all.buf, all.base, all.ifd0.get(315)) === ARTIST, `Artist = ${ARTIST}`);
  check(ascii(all.buf, all.base, all.ifd0.get(33432)) === COPYRIGHT, 'Copyright carried');
  check(!!all.exif.get(33434), 'ExposureTime present');
  const lens = ascii(all.buf, all.base, all.exif.get(42036));
  check(!!lens, `LensModel present ("${lens ?? ''}")`);
  step(`  GPS IFD: ${all.gps.size > 0 ? 'present' : 'absent (fixture has no fix — expected)'}`);
}

// --- 2. mode=copyright strips the shoot, keeps the credit. ---
step('exifMode: copyright');
const cr = exifFromJpeg(
  await exportOne('cr', { format: 'jpeg', exifMode: 'copyright', artist: ARTIST, copyright: COPYRIGHT }),
);
check(!!cr, 'JPEG still has an Exif APP1');
if (cr) {
  check(!cr.ifd0.has(271) && !cr.ifd0.has(272), 'Make/Model stripped');
  check(!cr.ifd0.has(306), 'DateTime stripped');
  check(!cr.exif.has(33434) && !cr.exif.has(34855), 'exposure/ISO stripped');
  check(!cr.exif.has(42036), 'LensModel stripped');
  check(cr.gps.size === 0, 'no GPS IFD');
  check(ascii(cr.buf, cr.base, cr.ifd0.get(315)) === ARTIST, 'Artist kept');
  check(ascii(cr.buf, cr.base, cr.ifd0.get(33432)) === COPYRIGHT, 'Copyright kept');
  check(!!cr.exif.get(40962), 'pixel dimensions kept');
}

// --- 3. mode=none writes no EXIF at all. ---
step('exifMode: none');
check(
  exifFromJpeg(await exportOne('nonej', { format: 'jpeg', exifMode: 'none', artist: ARTIST })) === null,
  'JPEG has no Exif APP1',
);
check(
  exifFromPng(await exportOne('nonep', { format: 'png', exifMode: 'none' })) === null,
  'PNG has no eXIf chunk',
);

// --- 4. TIFF: all-mode main IFD carries the credit; none-mode drops it. ---
step('TIFF');
{
  const buf = await exportOne('tall', { format: 'tiff8', exifMode: 'all', artist: ARTIST, copyright: COPYRIGHT });
  const t = parseTiffBlock(buf, 0);
  check(ascii(buf, 0, t.ifd0.get(315)) === ARTIST, 'TIFF Artist carried');
  check(ascii(buf, 0, t.ifd0.get(33432)) === COPYRIGHT, 'TIFF Copyright carried');
  check(!!t.ifd0.get(34665), 'TIFF has an Exif IFD');
  check(ascii(buf, 0, t.exif.get(42036)) !== undefined, 'TIFF LensModel carried');
}
{
  const buf = await exportOne('tnone', { format: 'tiff8', exifMode: 'none' });
  const t = parseTiffBlock(buf, 0);
  check(!t.ifd0.has(271) && !t.ifd0.has(305) && !t.ifd0.has(315), 'TIFF none drops the descriptive tags');
  check(!t.ifd0.has(34665) && !t.ifd0.has(34853), 'TIFF none drops both sub-IFD pointers');
  check(t.ifd0.has(256) && t.ifd0.has(279), 'TIFF none keeps the structural tags');
}

// --- 5. Persisted export options round-trip through the server. ---
step('persisted options');
await call('Settings.SetExportOptions', [
  {
    format: 'jpeg', jpegQuality: 90, resizeMode: 'full', edgePx: 2160, colorSpace: 'srgb',
    sharpenTarget: 'off', sharpenAmount: 'standard', fileNameTemplate: '',
    exifMode: 'copyright', removeLocation: true, artist: `  ${ARTIST}  `, copyright: COPYRIGHT,
  },
]);
const ui = await call('Settings.GetUISettings', []);
check(ui.exportOptions.exifMode === 'copyright', 'exifMode persists');
check(ui.exportOptions.removeLocation === true, 'removeLocation persists');
check(ui.exportOptions.artist === ARTIST, 'artist persists (trimmed)');
check(ui.exportOptions.copyright === COPYRIGHT, 'copyright persists');
// Reset to defaults so the dev DB isn't left in a test state.
await call('Settings.SetExportOptions', [
  {
    format: 'jpeg', jpegQuality: 90, resizeMode: 'full', edgePx: 2160, colorSpace: 'srgb',
    sharpenTarget: 'off', sharpenAmount: 'standard', fileNameTemplate: '',
    exifMode: 'all', removeLocation: false, artist: '', copyright: '',
  },
]);

rmSync(dest, { recursive: true, force: true });
console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
