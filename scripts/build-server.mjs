// Builds the marrawd daemon with the platform's binary name, so one npm
// script serves all three OSes ("go build -o build/marrawd" would produce a
// directory-less .exe-less file on Windows that Electron never finds).
import { execFileSync } from 'node:child_process';

const out = process.platform === 'win32' ? 'build/marrawd.exe' : 'build/marrawd';
execFileSync('go', ['build', '-ldflags', '-s -w', '-o', out, './cmd/marrawd'], {
  stdio: 'inherit',
});
