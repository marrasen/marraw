// Acceptance test for "Add as library folder", driven through the real UI.
//
// Seeds a managed parent over the wire, launches the Electron shell against the
// dev servers (marrawd :8483 + Vite :5173), and — while the app is running and
// untouched — creates a new folder on disk and copies a RAW into it. The rail
// must grow a row for it on its own.
//
//   node scripts/libfolder-verify.mjs "D:\Photos\<raw folder>"

import { spawn } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/libfolder-verify.mjs <raw-folder>');
  process.exit(1);
}

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(import.meta.url);
const electron = require('electron');

// ---- fixture ---------------------------------------------------------------

const raws = readdirSync(FOLDER).filter((f) => f.toLowerCase().endsWith('.arw'));
if (raws.length < 3) throw new Error('need at least three ARWs in the source folder');

const parent = mkdtempSync(join(tmpdir(), 'marraw-libfolder-'));
mkdirSync(join(parent, 'Ceremony'));
mkdirSync(join(parent, 'Reception'));
mkdirSync(join(parent, 'export'));
copyFileSync(join(FOLDER, raws[0]), join(parent, 'Ceremony', 'A.ARW'));
copyFileSync(join(FOLDER, raws[1]), join(parent, 'Ceremony', 'B.ARW'));
copyFileSync(join(FOLDER, raws[2]), join(parent, 'Reception', 'C.ARW'));
copyFileSync(join(FOLDER, raws[0]), join(parent, 'export', 'noise.ARW'));
copyFileSync(join(FOLDER, raws[1]), join(parent, 'Loose.ARW')); // the self-shoot

// ---- seed the parent root over the wire ------------------------------------

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
ws.onmessage = (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(msg.message));
    pending.delete(msg.id);
  }
};
const call = (method, params) => {
  const id = String(nextId++); // aprot drops non-string ids
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ type: 'request', id, method, params }));
  });
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('cannot connect to marrawd :8483'));
});
// Two roots standing in for external drives that are not plugged in: the paths
// simply do not exist. The driver creates `offline` mid-run to test recovery;
// `gone` is never created, and the UI test removes it from the library while it
// is still unreachable.
const offline = `${parent}-offline`;
const gone = `${parent}-gone`;

const before = await call('Library.GetLibraryRoots', []);
await call('Library.SetLibraryRoots', [
  [
    ...before,
    { path: parent, alias: '', includeSubfolders: false, photoCount: 0, isParent: true },
    { path: offline, alias: '', includeSubfolders: false, photoCount: 0, isParent: false },
    { path: gone, alias: '', includeSubfolders: false, photoCount: 0, isParent: false },
  ],
]);

// ---- launch the app --------------------------------------------------------

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE; // the harness sets this; it breaks Electron
env.MARRAW_DEV = '1';
env.MARRAW_PORT = '8483';
env.MARRAW_UITEST = join(root, 'scripts', 'libfolder-verify.renderer.js');
env.MARRAW_SCREENSHOT = join(root, 'build', 'libfolder-verify.png');
delete env.MARRAW_OPEN_FOLDER; // stay in Library mode, where the rail lives

const child = spawn(electron, ['.'], { cwd: root, env, stdio: ['ignore', 'pipe', 'pipe'] });

let result = null;
child.stdout.on('data', (d) => {
  for (const line of String(d).split(/\r?\n/)) {
    if (line.startsWith('UITEST_RESULT ')) {
      const payload = line.slice('UITEST_RESULT '.length);
      try {
        result = JSON.parse(payload);
      } catch {
        // The renderer script must `return` its results object; a bare
        // expression yields `undefined` and would crash the driver here.
        console.error(`bad UITEST_RESULT payload: ${payload}`);
      }
    } else if (line.trim()) console.log(`[electron] ${line}`);
  }
});
child.stderr.on('data', (d) => process.stderr.write(`[electron] ${d}`));

// The app is up and idle by now. Create the folder the way a user would: with
// Explorer, while marraw is open and nobody clicks anything.
setTimeout(() => {
  const party = join(parent, 'Party');
  mkdirSync(party);
  console.log(`[driver] created ${party}`);
  // Give the parent's dispatch time to attach a watch to the new directory —
  // exactly the gap a real user leaves between making a folder and dropping a
  // card into it. Copying instantly would let the parent's own re-listing find
  // the file and hide a missing child watch.
  setTimeout(() => {
    copyFileSync(join(FOLDER, raws[0]), join(party, 'P1.ARW'));
    console.log('[driver] copied P1.ARW');
    // "Plug the drive back in." Nothing watches a path that does not exist, so
    // only the availability poller can notice this.
    setTimeout(() => {
      mkdirSync(offline);
      copyFileSync(join(FOLDER, raws[1]), join(offline, 'D1.ARW'));
      console.log(`[driver] reconnected ${offline}`);
    }, 3000);
  }, 5000);
}, 9000);

const code = await new Promise((resolve) => child.on('exit', resolve));

// ---- restore ---------------------------------------------------------------

try {
  await call('Library.SetLibraryRoots', [before]);
} catch {}
ws.close();
for (const dir of [parent, offline]) {
  for (let i = 0; i < 20; i++) {
    try {
      rmSync(dir, { recursive: true, force: true });
      break;
    } catch {
      await new Promise((r) => setTimeout(r, 500)); // LibRaw may still hold files open
    }
  }
}

if (!result) {
  console.error(`no UITEST_RESULT (electron exited ${code})`);
  process.exit(1);
}
let failed = 0;
for (const [k, v] of Object.entries(result)) {
  if (k === 'finalRows') continue;
  const ok = k === 'fatal' ? false : v === true;
  if (!ok) failed++;
  console.log(`  ${ok ? 'PASS' : 'FAIL'}  ${k}${ok ? '' : ` -> ${JSON.stringify(v)}`}`);
}
if (result.finalRows) console.log(`  rows: ${result.finalRows}`);
console.log(failed ? `${failed} LIBRARY-FOLDER CHECKS FAILED` : 'ALL LIBRARY-FOLDER CHECKS PASSED');
process.exit(failed ? 1 : 0);
