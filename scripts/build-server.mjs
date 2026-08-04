// Builds the marrawd daemon with the platform's binary name, so one npm
// script serves all three OSes ("go build -o build/marrawd" would produce a
// directory-less .exe-less file on Windows that Electron never finds).
import { execFileSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync } from 'node:fs';

// The share page is embedded into the daemon (internal/guestui), so it has to
// be in place before the compiler runs. Copied rather than built straight into
// internal/: vite owns client/, and a checked-out tree keeps only the
// placeholder that makes the embed directive legal.
const guestSrc = 'client/dist-guest';
const guestDst = 'internal/guestui/bundle';
if (existsSync(guestSrc)) {
  for (const entry of readdirSync(guestDst)) {
    if (entry !== '.gitkeep') rmSync(`${guestDst}/${entry}`, { recursive: true, force: true });
  }
  mkdirSync(guestDst, { recursive: true });
  cpSync(guestSrc, guestDst, { recursive: true });
} else {
  console.warn(`build-server: ${guestSrc} missing — the daemon will serve no share page`);
}

const out = process.platform === 'win32' ? 'build/marrawd.exe' : 'build/marrawd';
execFileSync('go', ['build', '-ldflags', '-s -w', '-o', out, './cmd/marrawd'], {
  stdio: 'inherit',
});
