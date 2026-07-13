// marraw Electron shell: spawns the Go daemon, waits for its READY
// handshake, and loads the client pointed at the daemon's port + token.
// Single-instance: relaunching the exe opens a new window in the running
// instance instead of a second process (two daemons on one SQLite file
// clobbered each other's settings).
const { app, BrowserWindow, ipcMain, dialog, shell } = require('electron');
const { spawn } = require('node:child_process');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const readline = require('node:readline');

// Taskbar/window icon. Only needed for the dev/unpackaged run — the packaged
// exe carries its own icon (electron-builder win.icon), and build/ isn't
// bundled, so fall back to Electron's default there.
const ICON_PATH = path.join(__dirname, '..', 'assets', 'icon.ico');
const WINDOW_ICON = fs.existsSync(ICON_PATH) ? ICON_PATH : undefined;

const DEV = process.env.MARRAW_DEV === '1';
// Preview: production performance without packaging. Loads the built client
// (client/dist) and spawns the built daemon (build/marrawd.exe) — same code
// paths as the installed app, but run straight from the repo with no Vite
// dev server, no HMR, and no DevTools. See `npm run preview`.
const PREVIEW = process.env.MARRAW_PREVIEW === '1';
// Run from the repo (build/marrawd.exe) rather than the packaged resources dir.
const UNPACKAGED = DEV || PREVIEW;
// Scripted harness runs (ui-verify, shot): animation frames must keep
// flowing even when the window is occluded. webPreferences.backgroundThrottling
// alone does not cover Chromium's compositor-side occlusion backgrounding —
// an occluded window stops getting BeginFrames and every rAF-coalesced code
// path (edit-draft flushes, the zoom tween) silently stalls mid-test.
const UITEST = !!(process.env.MARRAW_UITEST || process.env.MARRAW_SCREENSHOT);
if (UITEST) {
  app.commandLine.appendSwitch('disable-renderer-backgrounding');
  app.commandLine.appendSwitch('disable-backgrounding-occluded-windows');
}
// Trackpad pinch: let Chromium deliver pinch as synthetic ctrl+wheel events for
// the loupe's onWheel zoom to consume. On macOS the compositor visual-viewport
// pinch inverts and springs back on release, so disable it there — ctrl+wheel
// still flows. On Windows this same switch swallows the precision-touchpad pinch
// BEFORE Chromium turns it into ctrl+wheel, killing loupe zoom, so it must NOT be
// set; the default page/viewport zoom is suppressed in JS instead (App.tsx guard).
if (process.platform === 'darwin') {
  app.commandLine.appendSwitch('disable-pinch');
}
let child = null;
let quitting = false;

// Shell preferences, kept out of the daemon's uiSettings on purpose: the
// updater has to make a decision at launch, before (and even if) marrawd ever
// comes up. userData survives reinstalls, which is what an update opt-out
// wants.
function prefsPath() {
  return path.join(app.getPath('userData'), 'preferences.json');
}
function readPrefs() {
  try {
    return JSON.parse(fs.readFileSync(prefsPath(), 'utf8'));
  } catch {
    return {}; // absent or corrupt: fall back to defaults
  }
}
function writePrefs(prefs) {
  try {
    fs.writeFileSync(prefsPath(), JSON.stringify(prefs, null, 2));
  } catch (err) {
    console.error(`[prefs] write failed: ${err.message}`);
  }
}
// Opt-out, not opt-in: an unsigned app that silently goes stale is worse than
// one that updates itself.
const autoUpdateEnabled = () => readPrefs().autoUpdate !== false;

// Check GitHub Releases on launch, download a newer version in the background,
// swap it in on quit. Draft releases are invisible here, so an unpublished
// draft never reaches anyone. Never fatal: an unreachable update server must
// not stop the app from starting.
let autoUpdater = null;
function initAutoUpdater() {
  // Dev/preview run from the repo with no update metadata; UITEST owns its
  // process and must not race a background download.
  if (!app.isPackaged || UITEST) return;
  // Squirrel.Mac refuses to update a bundle without a valid signature, so
  // there is nothing to start until marraw has an Apple Developer ID.
  if (process.platform === 'darwin') return;
  if (!autoUpdateEnabled()) {
    console.log('[updater] disabled in settings');
    return;
  }
  if (autoUpdater) {
    // Re-enabled mid-session: the listeners are already attached, just look.
    autoUpdater.autoInstallOnAppQuit = true;
    autoUpdater.checkForUpdatesAndNotify().catch(() => {});
    return;
  }
  try {
    ({ autoUpdater } = require('electron-updater'));
  } catch (err) {
    console.error(`[updater] unavailable: ${err.message}`);
    return;
  }
  autoUpdater.on('error', (err) => console.error(`[updater] ${err?.message ?? err}`));
  autoUpdater.on('update-available', (i) => console.log(`[updater] ${i.version} available`));
  autoUpdater.on('update-not-available', () => console.log('[updater] up to date'));
  autoUpdater.on('update-downloaded', (i) => console.log(`[updater] ${i.version} installs on quit`));
  autoUpdater.checkForUpdatesAndNotify().catch((err) => {
    console.error(`[updater] check failed: ${err?.message ?? err}`);
  });
}

ipcMain.handle('marraw:get-auto-update', () => autoUpdateEnabled());
ipcMain.handle('marraw:set-auto-update', (_ev, on) => {
  const prefs = readPrefs();
  prefs.autoUpdate = !!on;
  writePrefs(prefs);
  if (prefs.autoUpdate) {
    initAutoUpdater();
  } else if (autoUpdater) {
    // Stop a staged update from being applied on quit. A download already in
    // flight finishes; it just never gets installed.
    autoUpdater.autoInstallOnAppQuit = false;
  }
  return prefs.autoUpdate;
});

async function startDaemon() {
  // Dev convenience: attach to an already-running `marrawd --dev`.
  if (DEV && process.env.MARRAW_PORT) {
    return { port: process.env.MARRAW_PORT, token: '' };
  }
  const token = crypto.randomUUID();
  const bin = process.platform === 'win32' ? 'marrawd.exe' : 'marrawd';
  const exe = UNPACKAGED
    ? path.join(__dirname, '..', 'build', bin)
    : path.join(process.resourcesPath, bin);
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

// One daemon per app instance, however many windows race into createWindow.
let backendPromise = null;
let startupFailed = false;
function ensureDaemon() {
  backendPromise ??= startDaemon();
  return backendPromise;
}

const windows = new Set();

async function createWindow(opts = {}) {
  // { initial?, openFolder? }
  let backend;
  try {
    backend = await ensureDaemon();
  } catch (err) {
    if (!startupFailed) {
      startupFailed = true;
      dialog.showErrorBox('marraw', `Cannot start backend: ${err.message}`);
      app.quit();
    }
    return;
  }

  const win = new BrowserWindow({
    width: 1500,
    height: 950,
    minWidth: 1280, // the handoff's "minimum comfortable window"
    frame: false, // no native title bar — marraw draws its own controls
    backgroundColor: '#0c0d0f',
    icon: WINDOW_ICON,
    show: false,
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      // Harness runs must keep animation frames flowing even when the window
      // is occluded: draft updates and the zoom tween are rAF-driven. Works
      // together with the occlusion switches set at startup (see UITEST).
      backgroundThrottling: !UITEST,
    },
  });
  windows.add(win);
  win.on('closed', () => windows.delete(win));
  win.setMenuBarVisibility(false);
  win.once('ready-to-show', () => win.show());
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));

  // Maximize/fullscreen state flows back so glyphs and Esc behave. These are
  // window events (not ipcMain), so per-window registration is correct.
  win.on('maximize', () => win.webContents.send('win:maxChanged', true));
  win.on('unmaximize', () => win.webContents.send('win:maxChanged', false));
  win.on('enter-full-screen', () => win.webContents.send('win:fullscreenChanged', true));
  win.on('leave-full-screen', () => win.webContents.send('win:fullscreenChanged', false));

  const query = { apiPort: String(backend.port), token: backend.token };
  if (opts.openFolder) query.openFolder = opts.openFolder;
  if (opts.initial) {
    // Env-derived params apply only to the first window (harness/dev hooks).
    if (process.env.MARRAW_OPEN_FOLDER && !query.openFolder) query.openFolder = process.env.MARRAW_OPEN_FOLDER;
    if (process.env.MARRAW_LOUPE) query.loupe = '1';
    if (process.env.MARRAW_SHOT) query.shot = process.env.MARRAW_SHOT; // scripts/shot.mjs
    // Seed for the `welcome` shot: an old version makes the "What's new"
    // card render ("" = fresh-install state).
    if (process.env.MARRAW_SEED_LAST_SEEN != null)
      query.seedLastSeen = process.env.MARRAW_SEED_LAST_SEEN;
    // Second fixture folder for the `folderview` shot (A/B switch probe).
    if (process.env.MARRAW_ALT_FOLDER) query.altFolder = process.env.MARRAW_ALT_FOLDER;
  }

  if (DEV && !PREVIEW) {
    const qs = new URLSearchParams(query).toString();
    // Vite auto-increments its port when 5173 is taken by another project;
    // MARRAW_VITE_PORT points dev Electron at the right instance.
    const vitePort = process.env.MARRAW_VITE_PORT || '5173';
    await win.loadURL(`http://localhost:${vitePort}/?${qs}`);
    // The detached DevTools window opens right on top of the app window —
    // in harness runs that occlusion is what used to stall rAF (see UITEST).
    // Only the initial window auto-opens it (Ctrl+Shift+I elsewhere).
    if (!UITEST && opts.initial) win.webContents.openDevTools({ mode: 'detach' });
  } else {
    await win.loadFile(path.join(__dirname, '..', 'client', 'dist', 'index.html'), { query });
  }

  if (opts.initial) runHarnessHooks(win);
}

function runHarnessHooks(win) {
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

// Baked-in window controls (frameless): renderer buttons drive these, routed
// to the window that sent the message so every window controls itself.
const senderWin = (e) => BrowserWindow.fromWebContents(e.sender);
ipcMain.on('win:minimize', (e) => senderWin(e)?.minimize());
ipcMain.on('win:toggleMax', (e) => {
  const w = senderWin(e);
  if (w) w.isMaximized() ? w.unmaximize() : w.maximize();
});
ipcMain.on('win:close', (e) => senderWin(e)?.close());
ipcMain.on('win:toggleFullScreen', (e) => {
  const w = senderWin(e);
  w?.setFullScreen(!w.isFullScreen());
});
ipcMain.handle('win:isMax', (e) => senderWin(e)?.isMaximized() ?? false);
ipcMain.on('win:openNew', (_e, folderPath) => {
  void createWindow({ openFolder: typeof folderPath === 'string' && folderPath ? folderPath : undefined });
});

ipcMain.handle('marraw:pick-directory', async () => {
  const res = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] });
  return res.canceled ? null : res.filePaths[0];
});
ipcMain.handle('marraw:pick-image', async () => {
  const res = await dialog.showOpenDialog({
    properties: ['openFile'],
    filters: [{ name: 'Images', extensions: ['png', 'jpg', 'jpeg'] }],
  });
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

// Single instance: a second launch hands its MARRAW_OPEN_FOLDER over via
// additionalData (the first instance can't see the second's env) and exits;
// we answer by opening a new window. Harness runs bypass the lock — they
// must own their process to read UITEST_RESULT from its stdout.
const gotLock = UITEST ? true : app.requestSingleInstanceLock({
  openFolder: process.env.MARRAW_OPEN_FOLDER ?? null,
});
if (!gotLock) {
  app.quit();
} else {
  app.on('second-instance', (_e, _argv, _wd, additionalData) => {
    const folder =
      additionalData && typeof additionalData.openFolder === 'string' && additionalData.openFolder
        ? additionalData.openFolder
        : undefined;
    void createWindow({ openFolder: folder });
  });
  app.whenReady().then(() => {
    void createWindow({ initial: true });
    initAutoUpdater();
  });
}
app.on('before-quit', () => {
  quitting = true;
});
app.on('will-quit', () => {
  child?.kill();
});
app.on('window-all-closed', () => app.quit());
