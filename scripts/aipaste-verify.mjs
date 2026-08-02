// Scripted verification that pasted AI masks render without visiting the
// Local tab: drives scripts/aipaste-verify.renderer.js against the running
// dev servers.
//
//   node scripts/aipaste-verify.mjs <raw-folder>
//
// The bug only shows on a photo whose subject map has never been generated,
// and a map is cached under a key derived from the file's PATH
// (store.CacheKeyFor) — so a second run against the same folder would find the
// map warm and pass no matter what. Two RAWs are therefore copied into a fresh
// disposable folder per run (which also keeps sidecar writes off the original
// shoot); the folder is removed afterwards, though its library row stays in
// the dev DB.
import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import { copyFileSync, mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/aipaste-verify.mjs <raw-folder>');
  process.exit(1);
}

const RAW_RE = /\.(arw|cr2|cr3|nef|raf|rw2|dng|orf|pef)$/i;
const raws = readdirSync(FOLDER).filter((f) => RAW_RE.test(f)).sort().slice(0, 2);
if (raws.length < 2) {
  console.error(`aipaste-verify: need two RAW files in ${FOLDER}`);
  process.exit(1);
}
const work = mkdtempSync(join(tmpdir(), 'marraw-aipaste-'));
for (const f of raws) copyFileSync(join(FOLDER, f), join(work, f));
console.log(`fixture: ${work} (${raws.join(', ')})`);

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(import.meta.url);
const electron = require('electron');

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE; // the harness sets this; it breaks Electron
env.MARRAW_DEV = '1';
env.MARRAW_PORT = '8483';
env.MARRAW_OPEN_FOLDER = work;
env.MARRAW_UITEST = join(root, 'scripts', 'aipaste-verify.renderer.js');
env.MARRAW_SCREENSHOT = join(root, 'build', 'aipaste-verify.png');

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
  console.error('aipaste-verify: timed out after 300s');
  child.kill();
  rmSync(work, { recursive: true, force: true });
  process.exit(1);
}, 300_000);

child.on('exit', () => {
  clearTimeout(timer);
  rmSync(work, { recursive: true, force: true });
  if (!result) {
    console.error('aipaste-verify: no UITEST_RESULT line seen');
    process.exit(1);
  }
  let failures = 0;
  for (const [name, value] of Object.entries(result)) {
    const ok = value === true;
    if (!ok) failures++;
    console.log(`  ${ok ? 'PASS' : 'FAIL'}  ${name}${ok ? '' : ` -> ${JSON.stringify(value)}`}`);
  }
  console.log(failures === 0 ? '\nALL AI-PASTE CHECKS PASSED' : `\n${failures} AI-PASTE CHECKS FAILED`);
  process.exit(failures === 0 ? 0 : 1);
});
