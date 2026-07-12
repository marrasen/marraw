// Acceptance test for library-rail folder sort & time grouping, driven through
// the real UI. Seeds a managed parent with three shoots plus a loose RAW,
// launches the Electron shell against the dev servers (marrawd :8483 + Vite
// :5173), and lets scripts/railgroups-verify.renderer.js drive the sort/group
// dropdown, open a shoot (so the metadata pass dates it), and assert grouping,
// collapse, and "Collapse previous years".
//
//   node scripts/railgroups-verify.mjs "D:\Photos\<raw folder>"

import { spawn } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/railgroups-verify.mjs <raw-folder>');
  process.exit(1);
}

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(import.meta.url);
const electron = require('electron');

// ---- fixture ---------------------------------------------------------------

const raws = readdirSync(FOLDER).filter((f) => f.toLowerCase().endsWith('.arw'));
if (raws.length < 3) throw new Error('need at least three ARWs in the source folder');

const parent = mkdtempSync(join(tmpdir(), 'marraw-railgroups-'));
mkdirSync(join(parent, 'Alpha'));
mkdirSync(join(parent, 'Bravo'));
mkdirSync(join(parent, 'Charlie'));
copyFileSync(join(FOLDER, raws[0]), join(parent, 'Alpha', 'A1.ARW'));
copyFileSync(join(FOLDER, raws[1]), join(parent, 'Alpha', 'A2.ARW'));
copyFileSync(join(FOLDER, raws[1]), join(parent, 'Bravo', 'B1.ARW'));
copyFileSync(join(FOLDER, raws[2]), join(parent, 'Charlie', 'C1.ARW'));
copyFileSync(join(FOLDER, raws[0]), join(parent, 'Loose.ARW')); // the self-shoot

// ---- seed over the wire ------------------------------------------------------

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

const rootsBefore = await call('Library.GetLibraryRoots', []);
const settingsBefore = await call('Settings.GetUISettings', []);
await call('Library.SetLibraryRoots', [
  [
    ...rootsBefore,
    { path: parent, alias: '', includeSubfolders: false, photoCount: 0, isParent: true },
  ],
]);
// Deterministic starting point; restored below.
await call('Settings.SetShootSort', ['nameAsc']);
await call('Settings.SetShootGroup', ['none']);

// ---- launch the app ----------------------------------------------------------

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE; // the harness sets this; it breaks Electron
env.MARRAW_DEV = '1';
env.MARRAW_PORT = '8483';
env.MARRAW_UITEST = join(root, 'scripts', 'railgroups-verify.renderer.js');
env.MARRAW_SCREENSHOT = join(root, 'build', 'railgroups-verify.png');
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
        console.error(`bad UITEST_RESULT payload: ${payload}`);
      }
    } else if (line.trim()) console.log(`[electron] ${line}`);
  }
});
child.stderr.on('data', (d) => process.stderr.write(`[electron] ${d}`));

const timer = setTimeout(() => {
  console.error('railgroups-verify: timed out after 240s');
  child.kill();
}, 240_000);
const code = await new Promise((resolve) => child.on('exit', resolve));
clearTimeout(timer);

// ---- restore -----------------------------------------------------------------

try {
  await call('Library.SetLibraryRoots', [rootsBefore]);
  await call('Settings.SetShootSort', [settingsBefore.shootSort ?? 'nameAsc']);
  await call('Settings.SetShootGroup', [settingsBefore.shootGroup ?? 'none']);
  // "Collapse previous years" runs across every managed parent (the setting is
  // global), so drop any collapse entries the run added — including the user's
  // real parents' — leaving pre-existing ones alone.
  const settingsAfter = await call('Settings.GetUISettings', []);
  for (const k of Object.keys(settingsAfter.railGroups ?? {})) {
    if (!(k in (settingsBefore.railGroups ?? {}))) {
      await call('Settings.SetRailGroupOpen', [k, true]);
    }
  }
} catch {}
ws.close();
for (let i = 0; i < 20; i++) {
  try {
    rmSync(parent, { recursive: true, force: true });
    break;
  } catch {
    await new Promise((r) => setTimeout(r, 500)); // LibRaw may still hold files open
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
console.log(failed ? `${failed} RAIL-GROUP CHECKS FAILED` : 'ALL RAIL-GROUP CHECKS PASSED');
process.exit(failed ? 1 : 0);
