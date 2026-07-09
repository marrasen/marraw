// Launcher for the icon generator: spawns the Electron shell on
// scripts/gen-icon.cjs with a clean env. The Claude Code harness exports
// ELECTRON_RUN_AS_NODE, which makes the electron binary behave as plain Node
// (so `require('electron')` returns a path, not the API) — cross-env can only
// set it empty, not unset it, and empty still trips Electron. So, like
// scripts/ui-verify.mjs, we delete it and spawn directly.
//
//   npm run gen:icon
import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(import.meta.url);
const electron = require('electron'); // path to electron.exe

const env = { ...process.env };
delete env.ELECTRON_RUN_AS_NODE;

const child = spawn(electron, ['scripts/gen-icon.cjs'], { cwd: root, env, stdio: 'inherit' });
child.on('exit', (code) => process.exit(code ?? 1));
