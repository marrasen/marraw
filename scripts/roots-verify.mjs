// Wire probe for the curated-library backend: library roots CRUD, the
// Add-folder picker queries (ListDirRaws / CountRaws), and recursive scan
// through OpenFolder. Needs `marrawd --dev --port 8483` and a RAW folder to
// borrow two files from.
//
//   node scripts/roots-verify.mjs "D:\Photos\<raw folder>"

import { copyFileSync, mkdirSync, mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/roots-verify.mjs <raw-folder>');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
ws.onmessage = (ev) => {
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
    }, 60_000);
  });
}

const results = {};
const check = (name, ok, detail = '') => {
  results[name] = ok;
  console.log(`  ${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` (${detail})` : ''}`);
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('cannot connect to marrawd :8483'));
});

// Build a nested fixture: root/IMG_A.ARW + root/sub/IMG_B.ARW + root/export/IMG_C.ARW
const raws = readdirSync(FOLDER).filter((f) => f.toLowerCase().endsWith('.arw'));
if (raws.length < 1) throw new Error('need at least one ARW in the source folder');
const root = mkdtempSync(join(tmpdir(), 'marraw-roots-'));
mkdirSync(join(root, 'sub'));
mkdirSync(join(root, 'export'));
copyFileSync(join(FOLDER, raws[0]), join(root, 'IMG_A.ARW'));
copyFileSync(join(FOLDER, raws[0]), join(root, 'sub', 'IMG_B.ARW'));
copyFileSync(join(FOLDER, raws[0]), join(root, 'export', 'IMG_C.ARW'));

try {
  // Roots CRUD round-trip.
  const before = await call('Library.GetLibraryRoots', []);
  const mine = { path: root, alias: 'Probe Shoot', includeSubfolders: true, photoCount: 0 };
  await call('Library.SetLibraryRoots', [[...before, mine]]);
  const after = await call('Library.GetLibraryRoots', []);
  const stored = after.find((r) => r.path.toLowerCase() === root.toLowerCase());
  check('rootsRoundTrip', !!stored && stored.alias === 'Probe Shoot' && stored.includeSubfolders);

  // Picker queries.
  const entries = await call('Library.ListDirRaws', [root]);
  const sub = entries.find((e) => e.name === 'sub');
  check('listDirRaws', entries.length === 2 && sub?.rawCount === 1, JSON.stringify(entries.map((e) => `${e.name}:${e.rawCount}`)));
  const flat = await call('Library.CountRaws', [[root], false]);
  const deep = await call('Library.CountRaws', [[root], true]);
  check('countRaws', flat.files === 1 && deep.files === 2, `flat=${flat.files} deep=${deep.files}`);

  // Recursive scan through OpenFolder (root has includeSubfolders=true);
  // the export/ noise folder must be skipped.
  const info = await call('Library.OpenFolder', [root]);
  check('recursiveOpen', info.photoCount === 2, `photoCount=${info.photoCount}`);
  const photos = await call('Library.ListPhotos', [info.folderId]);
  const names = photos.map((p) => p.fileName).sort();
  check(
    'nestedNames',
    names.length === 2 && names[0] === 'IMG_A.ARW' && names[1].replace('/', '\\') === 'sub\\IMG_B.ARW',
    JSON.stringify(names),
  );

  // Flip the root to flat and rescan: the nested photo drops out.
  await call('Library.SetLibraryRoots', [[...before, { ...mine, includeSubfolders: false }]]);
  const flatInfo = await call('Library.OpenFolder', [root]);
  check('flatRescan', flatInfo.photoCount === 1, `photoCount=${flatInfo.photoCount}`);

  // Restore the original roots list.
  await call('Library.SetLibraryRoots', [before]);
  const restored = await call('Library.GetLibraryRoots', []);
  check('rootsRestored', restored.length === before.length);
} finally {
  ws.close();
  // Background decode jobs may still hold the copied ARWs open (LibRaw
  // fopen) — retry for a while, then leave the temp dir to the OS.
  for (let i = 0; i < 20; i++) {
    try {
      rmSync(root, { recursive: true, force: true });
      break;
    } catch {
      await new Promise((r) => setTimeout(r, 500));
    }
  }
}

const failed = Object.values(results).filter((v) => !v).length;
console.log(failed ? `${failed} ROOTS CHECKS FAILED` : 'ALL ROOTS CHECKS PASSED');
process.exit(failed ? 1 : 0);
