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
  child.stderr.on('data', (d) => console.error(`[marrawd] ${d}`.trimEnd()));
  child.on('exit', (code) => {
    child = null;
    if (!quitting) {
      dialog.showErrorBox('marraw', `Backend exited unexpectedly (code ${code}).`);
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
  if (DEV) {
    const qs = new URLSearchParams(query).toString();
    await win.loadURL(`http://localhost:5173/?${qs}`);
    win.webContents.openDevTools({ mode: 'detach' });
  } else {
    await win.loadFile(path.join(__dirname, '..', 'client', 'dist', 'index.html'), { query });
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

app.whenReady().then(createWindow);
app.on('before-quit', () => {
  quitting = true;
});
app.on('will-quit', () => {
  child?.kill();
});
app.on('window-all-closed', () => app.quit());
