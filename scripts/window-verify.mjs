// Acceptance test for remembered window state, driven through the real shell:
// three Electron launches against the dev servers (marrawd :8483 + Vite :5173),
// each with preferences.json seeded to set up the next question.
//
//   1. save      — no stored rectangle: the window opens at the default and
//                  must write where it ended up on the way out. Also flips the
//                  info aside off with its own toolbar button.
//   2. restore   — a stored rectangle on-screen: the window must open exactly
//                  there, the viewer must come back with it, and the info aside
//                  must still be off (the half run 1 could not see).
//   3. offscreen — a stored rectangle on a monitor that is no longer there: the
//                  window must land on a real display instead. Flips the info
//                  aside back on, leaving the daemon as it was found.
//
//   node scripts/window-verify.mjs <raw-folder>
//
// Runs in a throwaway userData dir, so the seeding never touches real prefs.
// (The daemon is the shared dev one — MARRAW_PORT attaches to it — so the info
// aside setting does land in the dev database. Step 3 puts it back.)

import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/window-verify.mjs <raw-folder>');
  process.exit(1);
}

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(import.meta.url);
const electron = require('electron');

const userData = mkdtempSync(join(tmpdir(), 'marraw-window-verify-'));
const prefsPath = join(userData, 'preferences.json');
const readPrefs = () => {
  try {
    return JSON.parse(readFileSync(prefsPath, 'utf8'));
  } catch {
    return {};
  }
};
const writePrefs = (p) => writeFileSync(prefsPath, JSON.stringify(p, null, 2));

// One scripted launch. Resolves to the renderer script's return value.
async function run(name, rendererScript, prefs) {
  writePrefs(prefs);
  const env = { ...process.env };
  delete env.ELECTRON_RUN_AS_NODE; // set in some shells; it breaks Electron
  env.MARRAW_DEV = '1';
  env.MARRAW_PORT = '8483';
  env.MARRAW_OPEN_FOLDER = FOLDER;
  env.MARRAW_UITEST = join(root, 'scripts', rendererScript);
  env.MARRAW_UITEST_DELAY = '1000'; // the renderer scripts do their own waiting
  env.MARRAW_WINDOW_STATE = '1'; // opt back into geometry that harness runs skip

  const child = spawn(electron, ['.', `--user-data-dir=${userData}`], {
    cwd: root,
    env,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

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
      } else if (line.trim()) console.log(`[${name}] ${line}`);
    }
  });
  child.stderr.on('data', (d) => {
    const s = String(d).trim();
    if (s && !s.includes('DevTools')) console.error(`[${name}] ${s}`);
  });

  const timer = setTimeout(() => {
    console.error(`window-verify: ${name} timed out after 120s`);
    child.kill();
  }, 120_000);
  const code = await new Promise((resolve) => child.on('exit', resolve));
  clearTimeout(timer);

  if (!result) throw new Error(`${name}: no UITEST_RESULT (electron exited ${code})`);
  if (result.fatal) throw new Error(`${name}: ${result.fatal}`);
  return result;
}

let failed = 0;
const check = (cond, name, detail) => {
  if (!cond) failed++;
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}${cond || detail === undefined ? '' : ` -> ${detail}`}`);
};
// Window managers may nudge a placement by a pixel or two; a remembered
// rectangle only has to come back recognizably, not bit-exactly.
const near = (a, b, slack = 8) => Math.abs(a - b) <= slack;
// Landed on a real display: the harness reads the work area of whatever screen
// the window ended up on, so this holds with any monitor arrangement.
const onScreen = (r) =>
  r.x + r.width > r.availLeft &&
  r.x < r.availLeft + r.availWidth &&
  r.y + r.height > r.availTop &&
  r.y < r.availTop + r.availHeight;

try {
  // --- 1. Nothing remembered yet: open at the default, write it down. ---
  const one = await run('save', 'window-verify.editpanel.js', {});
  check(one.before === true, 'info aside starts shown (nothing stored)', JSON.stringify(one.before));
  check(one.flipped === true, 'toolbar toggle hides it, and the daemon agrees', JSON.stringify(one));

  const saved = readPrefs().mainBounds;
  const savedSane =
    !!saved &&
    [saved.x, saved.y, saved.width, saved.height].every((n) => Number.isFinite(n)) &&
    saved.width >= 1280;
  check(savedSane, 'main window geometry persisted to preferences.json', JSON.stringify(saved));
  check(savedSane && near(saved.width, one.width) && near(saved.height, one.height),
    'what was written is where the window actually was',
    `${JSON.stringify(saved)} vs ${one.width}x${one.height}`);

  // --- 2. A remembered rectangle, and a viewer that was up at quit. ---
  // (60,60) overlaps every plausible display, so this is about the shell
  // honouring the rectangle, not about clamping.
  const want = { x: 60, y: 60, width: 1320, height: 820, maximized: false };
  const two = await run('restore', 'window-verify.renderer.js', {
    mainBounds: want,
    viewerOpen: true,
  });
  check(near(two.x, want.x) && near(two.y, want.y), 'window reopens at the remembered position',
    `${two.x},${two.y} want ${want.x},${want.y}`);
  check(near(two.width, want.width) && near(two.height, want.height), 'window reopens at the remembered size',
    `${two.width}x${two.height} want ${want.width}x${want.height}`);
  check(two.viewerOpen === true, 'pop-out viewer comes back with it', JSON.stringify(two.viewerOpen));
  check(two.showEditPanel === false, 'info aside is still hidden after a restart',
    JSON.stringify(two.showEditPanel));

  // --- 3. The second screen is gone. ---
  const gone = { x: 30000, y: 30000, width: 1400, height: 900, maximized: false };
  const three = await run('offscreen', 'window-verify.editpanel.js', { mainBounds: gone });
  check(onScreen(three), 'a rectangle on a vanished monitor lands on a real display',
    JSON.stringify({ x: three.x, y: three.y, w: three.width, h: three.height }));
  check(three.viewerOpen === false, 'no viewer when none was up at quit', JSON.stringify(three.viewerOpen));
  check(three.before === false && three.flipped === true, 'info aside restored to shown',
    JSON.stringify({ before: three.before, after: three.after }));
} catch (err) {
  failed++;
  console.error(`  FAIL  ${err.message}`);
} finally {
  rmSync(userData, { recursive: true, force: true });
}

console.log(failed ? `\n${failed} WINDOW CHECKS FAILED` : '\nALL WINDOW CHECKS PASSED');
process.exit(failed ? 1 : 0);
