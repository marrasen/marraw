// marraw Electron shell: spawns the Go daemon, waits for its READY
// handshake, and loads the client pointed at the daemon's port + token.
const { app, BrowserWindow, ipcMain, dialog, shell } = require('electron');
const { spawn } = require('node:child_process');
const crypto = require('node:crypto');
const path = require('node:path');
const readline = require('node:readline');

const DEV = process.env.MARRAW_DEV === '1';
let child = null;
let quitting = false;

async function startDaemon() {
  // Dev convenience: attach to an already-running `marrawd --dev`.
  if (DEV && process.env.MARRAW_PORT) {
    return { port: process.env.MARRAW_PORT, token: '' };
  }
  const token = crypto.randomUUID();
  const exe = DEV
    ? path.join(__dirname, '..', 'build', 'marrawd.exe')
    : path.join(process.resourcesPath, 'marrawd.exe');
  child = spawn(exe, ['--port', '0', '--data-dir', app.getPath('userData')], {
    env: { ...process.env, MARRAW_TOKEN: token, MARRAW_PARENT_WATCH: '1' },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  // Keep recent stderr so an unexpected exit can say *why*.
  const stderrTail = [];
  child.stderr.on('data', (d) => {
    console.error(`[marrawd] ${d}`.trimEnd());
    stderrTail.push(String(d));
    while (stderrTail.length > 20) stderrTail.shift();
  });
  child.on('exit', (code) => {
    child = null;
    if (!quitting) {
      const detail = stderrTail.length ? `\n\n${stderrTail.join('').slice(-1500)}` : '';
      dialog.showErrorBox('marraw', `Backend exited unexpectedly (code ${code}).${detail}`);
      app.quit();
    }
  });

  const port = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('backend did not become ready within 15s')), 15_000);
    const rl = readline.createInterface({ input: child.stdout });
    rl.on('line', (line) => {
      console.log(`[marrawd] ${line}`);
      const m = line.match(/^MARRAW_READY port=(\d+)$/);
      if (m) {
        clearTimeout(timer);
        resolve(m[1]);
      }
    });
    child.on('error', reject);
  });
  return { port, token };
}

async function createWindow() {
  let backend;
  try {
    backend = await startDaemon();
  } catch (err) {
    dialog.showErrorBox('marraw', `Cannot start backend: ${err.message}`);
    app.quit();
    return;
  }

  const win = new BrowserWindow({
    width: 1500,
    height: 950,
    backgroundColor: '#0a0a0a',
    show: false,
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
  win.setMenuBarVisibility(false);
  win.once('ready-to-show', () => win.show());
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));

  const query = { apiPort: String(backend.port), token: backend.token };
  if (process.env.MARRAW_OPEN_FOLDER) query.openFolder = process.env.MARRAW_OPEN_FOLDER;
  if (process.env.MARRAW_LOUPE) query.loupe = '1';
  if (process.env.MARRAW_SHOT) query.shot = process.env.MARRAW_SHOT; // scripts/shot.mjs

  if (DEV) {
    const qs = new URLSearchParams(query).toString();
    // Vite auto-increments its port when 5173 is taken by another project;
    // MARRAW_VITE_PORT points dev Electron at the right instance.
    const vitePort = process.env.MARRAW_VITE_PORT || '5173';
    await win.loadURL(`http://localhost:${vitePort}/?${qs}`);
    win.webContents.openDevTools({ mode: 'detach' });
  } else {
    await win.loadFile(path.join(__dirname, '..', 'client', 'dist', 'index.html'), { query });
  }

  // Scripted UI verification: MARRAW_UITEST=<renderer-script.js> runs the
  // script in the page (async IIFE, must return a JSON-serializable value),
  // prints it as a UITEST_RESULT line, and exits — used by
  // scripts/ui-verify.mjs.
  if (process.env.MARRAW_UITEST) {
    setTimeout(async () => {
      try {
        const src = require('node:fs').readFileSync(process.env.MARRAW_UITEST, 'utf8');
        const result = await win.webContents.executeJavaScript(`(async () => { ${src}\n })()`);
        console.log(`UITEST_RESULT ${JSON.stringify(result)}`);
      } catch (err) {
        console.log(`UITEST_RESULT ${JSON.stringify({ fatal: String(err) })}`);
      }
      if (process.env.MARRAW_SCREENSHOT) {
        try {
          const img = await win.webContents.capturePage();
          require('node:fs').writeFileSync(process.env.MARRAW_SCREENSHOT, img.toPNG());
        } catch {}
      }
      app.quit();
    }, Number(process.env.MARRAW_UITEST_DELAY ?? 4000));
    return;
  }

  // Headless UI smoke: MARRAW_SCREENSHOT=out.png captures the window after
  // load and exits — used by scripts/ui-smoke.mjs since there is no display
  // assertion harness.
  if (process.env.MARRAW_SCREENSHOT) {
    setTimeout(async () => {
      try {
        const img = await win.webContents.capturePage();
        require('node:fs').writeFileSync(process.env.MARRAW_SCREENSHOT, img.toPNG());
        console.log(`screenshot written: ${process.env.MARRAW_SCREENSHOT}`);
      } catch (err) {
        console.error('screenshot failed:', err);
      }
      app.quit();
    }, Number(process.env.MARRAW_SCREENSHOT_DELAY ?? 4000));
  }
}

ipcMain.handle('marraw:pick-directory', async () => {
  const res = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] });
  return res.canceled ? null : res.filePaths[0];
});
ipcMain.handle('marraw:reveal', (_ev, p) => {
  if (typeof p === 'string') shell.showItemInFolder(p);
});
ipcMain.handle('marraw:is-directory', (_ev, p) => {
  if (typeof p !== 'string') return false;
  try {
    return require('node:fs').statSync(p).isDirectory();
  } catch {
    return false;
  }
});

app.whenReady().then(createWindow);
app.on('before-quit', () => {
  quitting = true;
});
app.on('will-quit', () => {
  child?.kill();
});
app.on('window-all-closed', () => app.quit());
