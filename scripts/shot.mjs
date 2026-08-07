// Captures a screenshot of one app surface for visual review:
//   node scripts/shot.mjs <raw-folder> <cull|sheet|develop|crop|wb|masks|tonecurve|lens|original> [out.png]
// Needs the dev servers running (npm run dev); set MARRAW_VITE_PORT if Vite
// is not on 5173.
import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const FOLDER = process.argv[2];
const SHOT = process.argv[3] || 'cull';
if (!FOLDER) {
  console.error('usage: node scripts/shot.mjs <raw-folder> <surface> [out.png]');
  process.exit(1);
}

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const OUT = process.argv[4] || join(root, 'build', `shot-${SHOT}.png`);
const require = createRequire(import.meta.url);
const electron = require('electron');

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE;
env.MARRAW_DEV = '1';
env.MARRAW_PORT = '8483';
env.MARRAW_OPEN_FOLDER = FOLDER;
// The remote/pairing surfaces need a daemon that other machines could reach:
// the shared `marrawd --dev` on 8483 is loopback-only and serves none of the
// discovery or pairing routes. Dropping MARRAW_PORT makes the shell spawn its
// own daemon, bound per the run's preferences.json — pass MARRAW_SHOT_USERDATA
// to point that at a throwaway profile with remote access already on.
if (process.env.MARRAW_SHOT_OWN_DAEMON) delete env.MARRAW_PORT;
env.MARRAW_SHOT = SHOT;
env.MARRAW_UITEST = join(root, 'scripts', 'shot.renderer.js');
env.MARRAW_SCREENSHOT = OUT;

const args = ['.'];
if (process.env.MARRAW_SHOT_USERDATA) {
  args.push(`--user-data-dir=${process.env.MARRAW_SHOT_USERDATA}`);
}
const child = spawn(electron, args, { cwd: root, env, stdio: ['ignore', 'pipe', 'pipe'] });
child.stdout.on('data', (d) => {
  const s = String(d).trim();
  if (s.startsWith('UITEST_RESULT')) console.log(s);
});
const timer = setTimeout(() => {
  console.error('shot: timed out');
  child.kill();
  process.exit(1);
}, 120_000);
child.on('exit', () => {
  clearTimeout(timer);
  console.log(`screenshot: ${OUT}`);
});
