// Acceptance test for the pop-out photo window (Ctrl+N), driven through the
// real shell: launches Electron against the dev servers (marrawd :8483 + Vite
// :5173), runs scripts/viewer-verify.renderer.js in the main window, and
// checks that the shell wrote the viewer's geometry to preferences.json on the
// way out (the "remembers where it was" half of the feature).
//
//   node scripts/viewer-verify.mjs /tmp/marraw-fixture

import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/viewer-verify.mjs <raw-folder>');
  process.exit(1);
}

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(import.meta.url);
const electron = require('electron');

// Where the shell keeps its prefs in a dev run (app.getPath('userData')).
const prefsPath =
  process.platform === 'win32'
    ? join(process.env.APPDATA ?? '', 'marraw', 'preferences.json')
    : process.platform === 'darwin'
      ? join(homedir(), 'Library', 'Application Support', 'marraw', 'preferences.json')
      : join(process.env.XDG_CONFIG_HOME ?? join(homedir(), '.config'), 'marraw', 'preferences.json');
const readBounds = () => {
  try {
    return JSON.parse(readFileSync(prefsPath, 'utf8')).viewerBounds ?? null;
  } catch {
    return null;
  }
};
const boundsBefore = readBounds();

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE; // the harness sets this; it breaks Electron
env.MARRAW_DEV = '1';
env.MARRAW_PORT = '8483';
env.MARRAW_OPEN_FOLDER = FOLDER;
env.MARRAW_UITEST = join(root, 'scripts', 'viewer-verify.renderer.js');
env.MARRAW_UITEST_DELAY = '6000';

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
child.stderr.on('data', (d) => {
  const s = String(d).trim();
  if (s && !s.includes('DevTools')) console.error(`[electron] ${s}`);
});

const timer = setTimeout(() => {
  console.error('viewer-verify: timed out after 180s');
  child.kill();
  process.exit(1);
}, 180_000);

const code = await new Promise((resolve) => child.on('exit', resolve));
clearTimeout(timer);

if (!result) {
  console.error(`no UITEST_RESULT (electron exited ${code})`);
  process.exit(1);
}

let failed = 0;
const check = (cond, name, detail) => {
  if (!cond) failed++;
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}${cond || detail === undefined ? '' : ` -> ${detail}`}`);
};

for (const [k, v] of Object.entries(result)) {
  check(k !== 'fatal' && v === true, k, JSON.stringify(v));
}

// Closing the viewer must leave a usable rectangle behind: this is what the
// window reopens into, this session and after a restart.
const bounds = readBounds();
const sane =
  !!bounds &&
  [bounds.x, bounds.y, bounds.width, bounds.height].every((n) => Number.isFinite(n)) &&
  bounds.width >= 480 &&
  bounds.height >= 320;
check(sane, 'viewer geometry persisted to preferences.json', JSON.stringify(bounds));
if (sane && boundsBefore) {
  console.log(`  (prior bounds ${JSON.stringify(boundsBefore)})`);
}

console.log(failed ? `\n${failed} VIEWER CHECKS FAILED` : '\nALL VIEWER CHECKS PASSED');
process.exit(failed ? 1 : 0);
