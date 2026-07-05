// Scripted UI verification: launches the Electron shell against the running
// dev servers (marrawd :8483 + Vite :5173), executes
// scripts/ui-verify.renderer.js in the page, and reports its assertions.
//
//   node scripts/ui-verify.mjs "D:\Photos\<raw folder>"
import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/ui-verify.mjs <raw-folder>');
  process.exit(1);
}

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(import.meta.url);
const electron = require('electron'); // path to electron.exe

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE; // the harness sets this; it breaks Electron
env.MARRAW_DEV = '1';
env.MARRAW_PORT = '8483';
env.MARRAW_OPEN_FOLDER = FOLDER;
env.MARRAW_UITEST = join(root, 'scripts', 'ui-verify.renderer.js');
env.MARRAW_SCREENSHOT = join(root, 'build', 'ui-verify.png');

const child = spawn(electron, ['.'], { cwd: root, env, stdio: ['ignore', 'pipe', 'pipe'] });

let result = null;
child.stdout.on('data', (d) => {
  for (const line of String(d).split(/\r?\n/)) {
    if (line.startsWith('UITEST_RESULT ')) {
      result = JSON.parse(line.slice('UITEST_RESULT '.length));
    } else if (line.trim()) {
      console.log(`[electron] ${line}`);
    }
  }
});
child.stderr.on('data', (d) => {
  const s = String(d).trim();
  if (s && !s.includes('DevTools')) console.error(`[electron] ${s}`);
});

const timer = setTimeout(() => {
  console.error('ui-verify: timed out after 180s');
  child.kill();
  process.exit(1);
}, 180_000);

child.on('exit', () => {
  clearTimeout(timer);
  if (!result) {
    console.error('ui-verify: no UITEST_RESULT line seen');
    process.exit(1);
  }
  let failures = 0;
  for (const [name, value] of Object.entries(result)) {
    const ok = value === true || (name === 'photosLoaded' && value > 0) || (name === 'exportDefaultDir' && typeof value === 'string' && value.length > 0);
    if (!ok) failures++;
    console.log(`  ${ok ? 'PASS' : 'FAIL'}  ${name}${ok ? (typeof value === 'boolean' ? '' : ` (${JSON.stringify(value)})`) : ` -> ${JSON.stringify(value)}`}`);
  }
  console.log(failures === 0 ? '\nALL UI CHECKS PASSED' : `\n${failures} UI CHECKS FAILED`);
  console.log(`screenshot: ${env.MARRAW_SCREENSHOT}`);
  process.exit(failures === 0 ? 0 : 1);
});
