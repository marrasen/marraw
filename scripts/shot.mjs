// Captures a screenshot of one app surface for visual review:
//   node scripts/shot.mjs <raw-folder> <cull|sheet|develop|crop|wb> [out.png]
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
env.MARRAW_SHOT = SHOT;
env.MARRAW_UITEST = join(root, 'scripts', 'shot.renderer.js');
env.MARRAW_SCREENSHOT = OUT;

const child = spawn(electron, ['.'], { cwd: root, env, stdio: ['ignore', 'pipe', 'pipe'] });
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
