// marraw Electron shell: spawns the Go daemon, waits for its READY
// handshake, and loads the client pointed at the daemon's port + token.
// Single-instance: relaunching the exe opens a new window in the running
// instance instead of a second process (two daemons on one SQLite file
// clobbered each other's settings).
const { app, BrowserWindow, ipcMain, dialog, shell, clipboard, nativeImage } = require('electron');
const { spawn } = require('node:child_process');
const crypto = require('node:crypto');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const readline = require('node:readline');
const remote = require('./remote.cjs');

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
// Beta channel is tri-state: unset defers to electron-updater's own default
// (prereleases only when the running version is itself a prerelease, so a
// beta install tracks its cycle with nothing stored), while an explicit
// choice pins the channel across updates — including past the stable release
// that would otherwise drop a beta install back to the stable channel.
const IS_PRERELEASE = app.getVersion().includes('-');
const betaChannelEnabled = () => readPrefs().betaChannel ?? IS_PRERELEASE;
function applyUpdateChannel() {
  const beta = readPrefs().betaChannel;
  if (beta != null) autoUpdater.allowPrerelease = !!beta;
}

// Only Windows (NSIS) and the Linux AppImage can replace themselves. A .deb
// install is owned by the package manager, and Squirrel.Mac refuses to update
// a bundle without a valid signature — nothing to start until marraw has an
// Apple Developer ID. Mirrored in preload.cjs, which hides the UI.
const UPDATES_SUPPORTED =
  process.platform === 'win32' || (process.platform === 'linux' && !!process.env.APPIMAGE);

// Check GitHub Releases, download a newer version, swap it in on relaunch.
// Draft releases are invisible here, so an unpublished draft never reaches
// anyone. Never fatal: an unreachable update server must not stop the app
// from starting.
//
// Every phase is pushed to the renderers as one state object instead of a
// transient OS notification: the whole point is that the user can find out
// where an update stands whenever they look, not only in the seconds after
// it lands. See client/src/stores/updateStore.ts.
let autoUpdater = null;
let updateState = {
  /** idle | checking | available | downloading | downloaded | error */
  status: 'idle',
  /** The version on offer (available/downloading/downloaded), else ''. */
  version: '',
  releaseNotes: '',
  releaseDate: '',
  percent: 0,
  transferred: 0,
  total: 0,
  bytesPerSecond: 0,
  error: '',
  /** epoch ms of the last completed check; 0 = never checked this session. */
  checkedAt: 0,
};

// Whether the pop-out viewer is up, pushed to every window so their toolbar
// toggles read true — the window can also be closed from its own Ctrl+N, or
// by the last library window going away, neither of which the opener sees.
function pushViewerOpen(open) {
  for (const w of BrowserWindow.getAllWindows()) {
    if (!w.isDestroyed()) w.webContents.send('win:viewerOpen', open);
  }
}

function pushUpdateState(patch) {
  updateState = { ...updateState, ...patch };
  for (const w of BrowserWindow.getAllWindows()) {
    if (!w.isDestroyed()) w.webContents.send('marraw:update-state', updateState);
  }
}

/**
 * Loads electron-updater on first use and wires its events to updateState.
 * Returns null where updating can't work (dev run, unsupported packaging,
 * UITEST — which owns its process and must not race a download).
 */
function updater() {
  if (autoUpdater) return autoUpdater;
  if (!app.isPackaged || UITEST || !UPDATES_SUPPORTED) return null;
  try {
    ({ autoUpdater } = require('electron-updater'));
  } catch (err) {
    console.error(`[updater] unavailable: ${err.message}`);
    return null;
  }
  // The UI decides when to fetch; a background download is started explicitly
  // below when automatic updates are on.
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = autoUpdateEnabled();
  applyUpdateChannel();
  autoUpdater.on('error', (err) => {
    const msg = String(err?.message ?? err);
    console.error(`[updater] ${msg}`);
    pushUpdateState({ status: 'error', error: msg, percent: 0 });
  });
  autoUpdater.on('checking-for-update', () => pushUpdateState({ status: 'checking', error: '' }));
  autoUpdater.on('update-available', (i) => {
    console.log(`[updater] ${i.version} available`);
    pushUpdateState({
      status: 'available',
      version: i.version ?? '',
      releaseNotes: typeof i.releaseNotes === 'string' ? i.releaseNotes : '',
      releaseDate: i.releaseDate ?? '',
      error: '',
      percent: 0,
      checkedAt: Date.now(),
    });
    // "Automatic updates" means the bytes arrive without being asked for; the
    // install still waits for the user (or for quit).
    if (autoUpdateEnabled()) startDownload();
  });
  autoUpdater.on('update-not-available', () => {
    console.log('[updater] up to date');
    pushUpdateState({ status: 'idle', version: '', error: '', checkedAt: Date.now() });
  });
  autoUpdater.on('download-progress', (p) => {
    pushUpdateState({
      status: 'downloading',
      percent: p?.percent ?? 0,
      transferred: p?.transferred ?? 0,
      total: p?.total ?? 0,
      bytesPerSecond: p?.bytesPerSecond ?? 0,
    });
  });
  autoUpdater.on('update-downloaded', (i) => {
    console.log(`[updater] ${i.version} ready to install`);
    pushUpdateState({ status: 'downloaded', version: i.version ?? '', percent: 100, error: '' });
  });
  return autoUpdater;
}

function startCheck() {
  const up = updater();
  if (!up) {
    pushUpdateState({
      status: 'error',
      error: app.isPackaged
        ? 'This installation cannot update itself.'
        : 'Updates only run in an installed build.',
      checkedAt: Date.now(),
    });
    return;
  }
  pushUpdateState({ status: 'checking', error: '' });
  up.checkForUpdates().catch((err) => {
    // The error event covers most failures, but a rejection here (no network,
    // bad metadata) can arrive without one.
    const msg = String(err?.message ?? err);
    console.error(`[updater] check failed: ${msg}`);
    pushUpdateState({ status: 'error', error: msg, checkedAt: Date.now() });
  });
}

function startDownload() {
  const up = updater();
  if (!up) return;
  // A download the user asked for should still land if they quit mid-wait,
  // even with the background updater switched off.
  up.autoInstallOnAppQuit = true;
  pushUpdateState({ status: 'downloading', percent: 0, error: '' });
  up.downloadUpdate().catch((err) => {
    const msg = String(err?.message ?? err);
    console.error(`[updater] download failed: ${msg}`);
    pushUpdateState({ status: 'error', error: msg, percent: 0 });
  });
}

function initAutoUpdater() {
  if (!autoUpdateEnabled()) {
    console.log('[updater] automatic check disabled in settings');
    return;
  }
  const up = updater();
  if (!up) return;
  // Also the re-enabled-mid-session path: a staged update that set-auto-update
  // parked must start installing on quit again.
  up.autoInstallOnAppQuit = true;
  applyUpdateChannel();
  startCheck();
}

ipcMain.handle('marraw:get-update-state', () => ({
  ...updateState,
  supported: UPDATES_SUPPORTED,
  currentVersion: app.getVersion(),
}));
ipcMain.handle('marraw:check-updates', () => {
  startCheck();
  return true;
});
ipcMain.handle('marraw:download-update', () => {
  startDownload();
  return true;
});
ipcMain.handle('marraw:install-update', () => {
  if (updateState.status !== 'downloaded' || !autoUpdater) return false;
  // The daemon's exit must read as expected, not as a crash (see child.on
  // 'exit'); before-quit would set this anyway, but quitAndInstall tears the
  // process down on its own schedule.
  quitting = true;
  child?.kill();
  autoUpdater.quitAndInstall();
  return true;
});

ipcMain.handle('marraw:get-auto-update', () => autoUpdateEnabled());
ipcMain.handle('marraw:set-auto-update', (_ev, on) => {
  const prefs = readPrefs();
  prefs.autoUpdate = !!on;
  writePrefs(prefs);
  if (prefs.autoUpdate) {
    initAutoUpdater();
  } else if (autoUpdater) {
    // Stop a staged update from being applied on quit. A download already in
    // flight finishes; it just never gets installed until asked for.
    autoUpdater.autoInstallOnAppQuit = false;
  }
  return prefs.autoUpdate;
});

ipcMain.handle('marraw:get-beta-channel', () => betaChannelEnabled());
ipcMain.handle('marraw:set-beta-channel', (_ev, on) => {
  const prefs = readPrefs();
  prefs.betaChannel = !!on;
  writePrefs(prefs);
  if (autoUpdater) autoUpdater.allowPrerelease = prefs.betaChannel;
  // Joining the channel should surface a pending beta now, not next launch —
  // including on a first check, where updater() reads the pref we just wrote.
  // Leaving it only affects future checks: a beta already downloaded still
  // installs on quit (there is no API to discard a staged update).
  if (prefs.betaChannel) startCheck();
  return prefs.betaChannel;
});

// Remote access prefs: when enabled, the local daemon binds a reachable
// address on a STABLE port (a random one would break every saved laptop
// connection on restart). Spawn-time only — changing it needs a relaunch.
const remoteAccessPrefs = () => {
  const ra = readPrefs().remoteAccess ?? {};
  return {
    enabled: ra.enabled === true,
    listen: typeof ra.listen === 'string' && ra.listen ? ra.listen : '0.0.0.0',
    port: Number.isInteger(ra.port) && ra.port > 0 && ra.port < 65536 ? ra.port : 8482,
  };
};
// The config the running daemon was actually spawned with (null until spawned)
// — lets set-remote-access answer whether a relaunch is needed.
let daemonRemoteAccess = null;

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
  const ra = remoteAccessPrefs();
  daemonRemoteAccess = ra;
  const args = ra.enabled
    ? ['--port', String(ra.port), '--listen', ra.listen, '--data-dir', app.getPath('userData')]
    : ['--port', '0', '--data-dir', app.getPath('userData')];
  child = spawn(exe, args, {
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

// Every ordinary window. The pop-out viewer is deliberately NOT in here — it
// must never be the window that keeps the app alive (see the 'closed' handler
// in createWindow).
const windows = new Set();

// ---- Pop-out photo window (Ctrl+N) ----
// A chromeless always-on-top window showing the same photo as the window that
// drives it, with its own zoom and pan. One per app instance: a second Ctrl+N
// from any window closes the one that is up rather than stacking another.
let viewerWin = null;
// Which daemon the open viewer is showing photos from. Photo ids only mean
// something inside one library, so a window talking to a different daemon must
// not steer it.
let viewerBackendKey = null;
// Last {folderId, photoId} pushed by each backend's windows. A freshly opened
// viewer pulls from here: the focus change that preceded it happened long
// before its page existed to receive a push.
const lastViewerPhoto = new Map();

// The viewer is a bare photo surface — it may legitimately sit small in the
// corner of a second monitor, so the main window's minimum does not apply.
const VIEWER_MIN_W = 480;
const VIEWER_MIN_H = 320;

// The backend params a window was loaded with, read back off its own URL — the
// shell doesn't otherwise remember which daemon each window talks to. Covers
// both load paths: loadURL's query string in dev, loadFile's { query } (which
// lands in the file:// URL just the same) when packaged. Inheriting them from
// the opener is what makes a viewer popped out of a remote window show that
// machine's photos instead of the local daemon's.
function backendParamsOf(win) {
  try {
    const p = new URL(win.webContents.getURL()).searchParams;
    if (p.get('apiHost')) {
      return {
        apiHost: p.get('apiHost'),
        token: p.get('token') ?? '',
        remote: '1',
        remoteName: p.get('remoteName') ?? '',
      };
    }
    if (p.get('apiPort')) return { apiPort: p.get('apiPort'), token: p.get('token') ?? '' };
  } catch {
    // A window still loading (or already gone) has no usable URL.
  }
  return null;
}
const backendKeyOf = (win) => {
  const b = win ? backendParamsOf(win) : null;
  if (!b) return null;
  return b.apiHost ? `remote:${b.apiHost}` : `local:${b.apiPort}`;
};

// Viewer geometry lives in the shell's prefs, not the daemon's uiSettings: the
// window has to be placed before any renderer — or any daemon — exists.
function viewerBoundsPrefs() {
  const b = readPrefs().viewerBounds;
  if (!b || typeof b !== 'object') return null;
  if (![b.x, b.y, b.width, b.height].every((n) => Number.isFinite(n))) return null;
  if (b.width < VIEWER_MIN_W || b.height < VIEWER_MIN_H) return null;
  return {
    x: Math.round(b.x),
    y: Math.round(b.y),
    width: Math.round(b.width),
    height: Math.round(b.height),
    maximized: b.maximized === true,
  };
}
function saveViewerBounds(win) {
  if (!win || win.isDestroyed()) return;
  const prefs = readPrefs();
  // getNormalBounds, not getBounds: a maximized window must remember the
  // rectangle it will be restored to, not the screen-filling one.
  prefs.viewerBounds = { ...win.getNormalBounds(), maximized: win.isMaximized() };
  writePrefs(prefs);
}
// Where to open the viewer: its remembered rectangle when enough of it still
// lands on a display, else centred on the primary one. A monitor unplugged (or
// a resolution change) since last time would otherwise put it off-screen — and
// being frameless, with no controls of its own, it could not be dragged back.
function viewerPlacement(saved) {
  const { screen } = require('electron');
  const width = saved?.width ?? 1100;
  const height = saved?.height ?? 750;
  if (saved) {
    const area = screen.getDisplayMatching(saved).workArea;
    const onX = Math.min(saved.x + width, area.x + area.width) - Math.max(saved.x, area.x);
    const onY = Math.min(saved.y + height, area.y + area.height) - Math.max(saved.y, area.y);
    // A sliver is not grabbable; require a usable corner on both axes.
    if (onX >= 100 && onY >= 100) return { x: saved.x, y: saved.y, width, height };
  }
  const area = screen.getPrimaryDisplay().workArea;
  const w = Math.min(width, area.width);
  const h = Math.min(height, area.height);
  return {
    x: Math.round(area.x + (area.width - w) / 2),
    y: Math.round(area.y + (area.height - h) / 2),
    width: w,
    height: h,
  };
}

async function createWindow(opts = {}) {
  // { initial?, openFolder?, remote?, viewer? } — remote {name, host, token}
  // windows talk to another machine's daemon and never touch the local one;
  // viewer {backend} is the pop-out photo window, which inherits its opener's
  // daemon rather than resolving one of its own.
  let backend;
  if (!opts.remote && !opts.viewer) {
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
  }

  // The viewer reopens where it was left; ordinary windows open at 3:2,
  // matching the photos — full-bleed frames fill the window cleanly (and
  // screenshots of the app read like photographs).
  const savedViewer = opts.viewer ? viewerBoundsPrefs() : null;
  const geometry = opts.viewer ? viewerPlacement(savedViewer) : { width: 1500, height: 1000 };

  const win = new BrowserWindow({
    ...geometry,
    minWidth: opts.viewer ? VIEWER_MIN_W : 1280, // the handoff's "minimum comfortable window"
    minHeight: opts.viewer ? VIEWER_MIN_H : undefined,
    // The viewer floats over every app, not just marraw's own windows: the
    // whole point of popping a photo onto a second monitor is that it stays
    // visible while you work elsewhere. Electron has no "above my own windows
    // only" level.
    alwaysOnTop: !!opts.viewer,
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
  // Set from the viewer's own close handler: a load that is still in flight
  // when the window goes away rejects, and that is not a failure worth
  // reporting (see the load below).
  let viewerClosing = false;
  if (opts.viewer) {
    viewerWin = win;
    pushViewerOpen(true);
    win.on('closed', () => {
      if (viewerWin === win) viewerWin = null;
      pushViewerOpen(false);
    });
    if (savedViewer?.maximized) win.once('ready-to-show', () => win.maximize());
    // Geometry is written as it changes, not only on close: a crash — or a quit
    // that tears the window down without a 'close' — would otherwise forget
    // wherever it had been dragged this session.
    let saveTimer = null;
    const remember = () => {
      clearTimeout(saveTimer);
      saveTimer = setTimeout(() => saveViewerBounds(win), 400);
    };
    win.on('move', remember);
    win.on('resize', remember);
    win.on('close', () => {
      viewerClosing = true;
      clearTimeout(saveTimer);
      saveViewerBounds(win);
    });
  } else {
    windows.add(win);
    win.on('closed', () => {
      windows.delete(win);
      // A lone viewer would strand the app: it is chromeless, has no library
      // behind it, and window-all-closed never fires while it is up. It
      // follows the last real window out.
      if (windows.size === 0 && viewerWin && !viewerWin.isDestroyed()) viewerWin.close();
    });
  }
  win.setMenuBarVisibility(false);
  win.once('ready-to-show', () => win.show());
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));

  // Maximize/fullscreen state flows back so glyphs and Esc behave. These are
  // window events (not ipcMain), so per-window registration is correct.
  win.on('maximize', () => win.webContents.send('win:maxChanged', true));
  win.on('unmaximize', () => win.webContents.send('win:maxChanged', false));
  win.on('enter-full-screen', () => win.webContents.send('win:fullscreenChanged', true));
  win.on('leave-full-screen', () => win.webContents.send('win:fullscreenChanged', false));

  const query = opts.viewer
    ? { ...opts.viewer.backend, view: 'viewer' }
    : opts.remote
      ? { apiHost: opts.remote.host, token: opts.remote.token, remote: '1', remoteName: opts.remote.name }
      : { apiPort: String(backend.port), token: backend.token };
  if (opts.openFolder) query.openFolder = opts.openFolder;
  if (opts.initial) {
    // Env-derived params apply only to the first window (harness/dev hooks).
    if (process.env.MARRAW_OPEN_FOLDER && !query.openFolder) query.openFolder = process.env.MARRAW_OPEN_FOLDER;
    if (process.env.MARRAW_LOUPE) query.loupe = '1';
    if (process.env.MARRAW_SHOT) query.shot = process.env.MARRAW_SHOT; // scripts/shot.mjs
    // Focus a specific frame (by file name) before the shot runs — lets a
    // capture aim at a chosen photo instead of the renderer's default.
    if (process.env.MARRAW_SHOT_FOCUS) query.shotFocus = process.env.MARRAW_SHOT_FOCUS;
    // Override the time-gap grouping (minutes) for the capture session.
    if (process.env.MARRAW_SHOT_GAP) query.shotGap = process.env.MARRAW_SHOT_GAP;
    // Leave the auto-hiding chrome (filmstrip deck) hidden in the capture.
    if (process.env.MARRAW_SHOT_NO_WAKE) query.shotNoWake = '1';
    // AI mask kind for the aitint shot (subject|depth|scene).
    if (process.env.MARRAW_SHOT_AI) query.shotAI = process.env.MARRAW_SHOT_AI;
    // Seed for the `welcome` shot: an old version makes the "What's new"
    // card render ("" = fresh-install state).
    if (process.env.MARRAW_SEED_LAST_SEEN != null)
      query.seedLastSeen = process.env.MARRAW_SEED_LAST_SEEN;
    // Second fixture folder for the `folderview` shot (A/B switch probe).
    if (process.env.MARRAW_ALT_FOLDER) query.altFolder = process.env.MARRAW_ALT_FOLDER;
  }

  // The viewer never stops fetching renditions and tiles, so its page can
  // still count as loading when it is closed — which rejects the load with
  // ERR_FAILED. A window on its way out has nothing to report; a viewer that
  // is still up genuinely failed to show anything, and says so.
  const settle = (p) =>
    opts.viewer
      ? p.catch((err) => {
          if (!viewerClosing) console.error(`[viewer] load failed: ${err.message}`);
        })
      : p;

  if (DEV && !PREVIEW) {
    const qs = new URLSearchParams(query).toString();
    // Vite auto-increments its port when 5173 is taken by another project;
    // MARRAW_VITE_PORT points dev Electron at the right instance.
    const vitePort = process.env.MARRAW_VITE_PORT || '5173';
    await settle(win.loadURL(`http://localhost:${vitePort}/?${qs}`));
    // The detached DevTools window opens right on top of the app window —
    // in harness runs that occlusion is what used to stall rAF (see UITEST).
    // Only the initial window auto-opens it (Ctrl+Shift+I elsewhere).
    if (!UITEST && opts.initial) win.webContents.openDevTools({ mode: 'detach' });
  } else {
    await settle(win.loadFile(path.join(__dirname, '..', 'client', 'dist', 'index.html'), { query }));
  }

  // A viewer can be closed while it is still loading (a quick second Ctrl+N,
  // or the last library window going away). Its webContents is torn down
  // first, so isDestroyed() on the window alone is not enough to tell — go by
  // the close handler's own flag.
  if (viewerClosing || win.isDestroyed()) return;

  // Chromium persists per-host zoom (a stray Ctrl+wheel/pinch in a dev window
  // lands in the profile's Preferences) and re-applies it to every later
  // window on that origin. A zoomed harness viewport flips container-query
  // breakpoints and shifts every measured rect, so probes fail on layouts no
  // assertion expects — pin harness windows to 100%.
  if (UITEST) win.webContents.setZoomFactor(1);

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

// ---- Pop-out photo window ----
// Ctrl+N from any window toggles it. The viewer closes itself through the
// ordinary win:close above, so there is only ever one way in and one way out.
ipcMain.on('win:toggleViewer', (e) => {
  if (viewerWin && !viewerWin.isDestroyed()) {
    viewerWin.close();
    return;
  }
  const opener = senderWin(e);
  const params = opener ? backendParamsOf(opener) : null;
  if (!params) return;
  viewerBackendKey = backendKeyOf(opener);
  void createWindow({ viewer: { backend: params } });
});
// The driving window's current photo. Cached per backend even when no viewer
// is up, so opening one lands on the right frame immediately instead of
// waiting for the next navigation.
ipcMain.on('win:viewerPhoto', (e, state) => {
  if (!Number.isInteger(state?.folderId) || !Number.isInteger(state?.photoId)) return;
  const key = backendKeyOf(senderWin(e));
  if (!key) return;
  const next = { folderId: state.folderId, photoId: state.photoId };
  lastViewerPhoto.set(key, next);
  if (key !== viewerBackendKey) return; // another library's id space
  if (viewerWin && !viewerWin.isDestroyed()) viewerWin.webContents.send('win:viewerPhoto', next);
});
// Pull path for a viewer that has just booted: its page loads well after the
// push that opened it. Also what the verify harness probes.
ipcMain.handle('win:getViewerPhoto', (e) => ({
  open: !!viewerWin && !viewerWin.isDestroyed(),
  ...(lastViewerPhoto.get(backendKeyOf(senderWin(e))) ?? { folderId: null, photoId: null }),
}));

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
ipcMain.handle('marraw:copy-image', (_ev, buf) => {
  if (!(buf instanceof ArrayBuffer) && !ArrayBuffer.isView(buf)) return false;
  const img = nativeImage.createFromBuffer(
    ArrayBuffer.isView(buf) ? Buffer.from(buf.buffer, buf.byteOffset, buf.byteLength) : Buffer.from(buf),
  );
  if (img.isEmpty()) return false;
  clipboard.writeImage(img);
  return true;
});
ipcMain.handle('marraw:is-directory', (_ev, p) => {
  if (typeof p !== 'string') return false;
  try {
    return require('node:fs').statSync(p).isDirectory();
  } catch {
    return false;
  }
});

// ---- Remote connections ----
// Saved remotes live in preferences.json (not the daemon's settings DB): the
// connect screen must list them before — and even without — any daemon.
const remotesList = () => {
  const list = readPrefs().remoteConnections;
  return Array.isArray(list) ? list : [];
};
// "host" or "host:port" → "host:port" (the daemon's remote default port).
// Shared with the discovery/pairing code, which has to agree on it exactly.
const { normalizeHost } = remote;

ipcMain.handle('marraw:remotes-list', () => remotesList());
ipcMain.handle('marraw:remotes-save', (_ev, conn) => {
  if (!conn || typeof conn.host !== 'string' || !conn.host.trim()) return remotesList();
  const entry = {
    id: typeof conn.id === 'string' && conn.id ? conn.id : crypto.randomUUID(),
    name: typeof conn.name === 'string' && conn.name.trim() ? conn.name.trim() : normalizeHost(conn.host),
    host: normalizeHost(conn.host),
    token: typeof conn.token === 'string' ? conn.token.trim() : '',
  };
  const prefs = readPrefs();
  const list = Array.isArray(prefs.remoteConnections) ? prefs.remoteConnections : [];
  const i = list.findIndex((c) => c && c.id === entry.id);
  if (i >= 0) list[i] = entry;
  else list.push(entry);
  prefs.remoteConnections = list;
  writePrefs(prefs);
  return list;
});
ipcMain.handle('marraw:remotes-delete', (_ev, id) => {
  const prefs = readPrefs();
  prefs.remoteConnections = remotesList().filter((c) => c && c.id !== id);
  writePrefs(prefs);
  return prefs.remoteConnections;
});
// Reachability probe from the MAIN process: no CORS in play, and it proves
// host + token in one authenticated round trip against GET /authz.
ipcMain.handle('marraw:remote-test', async (_ev, host, token) => {
  try {
    const res = await fetch(`http://${normalizeHost(host)}/authz`, {
      headers: token ? { 'X-Marraw-Token': token } : {},
      signal: AbortSignal.timeout(3000),
    });
    if (res.status === 403) return { ok: false, error: 'invalid token' };
    if (!res.ok) return { ok: false, error: `HTTP ${res.status}` };
    const body = await res.json().catch(() => null);
    if (!body || body.app !== 'marraw') return { ok: false, error: 'not a marraw daemon' };
    return { ok: true, version: body.version ?? '' };
  } catch (err) {
    // fetch's own wording ("fetch failed") says nothing to a photographer.
    // The two cases that matter are "not answering" and "answering slowly".
    return { ok: false, error: err?.name === 'TimeoutError' ? 'no answer (timed out)' : 'unreachable' };
  }
});
// Ask a discovered host to let us in. Nothing is saved until the person at
// that machine approves — this only puts a dialog on their screen.
ipcMain.handle('marraw:remote-pair', async (_ev, host) => {
  try {
    return await remote.requestPairing(host, os.hostname());
  } catch (err) {
    return { ok: false, error: `Could not reach ${normalizeHost(host)}.` };
  }
});
ipcMain.handle('marraw:remote-pair-wait', (_ev, host, requestId) =>
  remote.waitForPairing(host, requestId),
);
ipcMain.handle('marraw:remote-pair-cancel', (_ev, host, requestId) =>
  remote.cancelPairing(host, requestId),
);
ipcMain.handle('marraw:get-remote-access', () => ({
  ...remoteAccessPrefs(),
  // The daemon binds at spawn: prefs changed after that need a relaunch.
  restartRequired:
    daemonRemoteAccess != null &&
    JSON.stringify(daemonRemoteAccess) !== JSON.stringify(remoteAccessPrefs()),
}));
ipcMain.handle('marraw:set-remote-access', (_ev, patch) => {
  const prefs = readPrefs();
  const cur = remoteAccessPrefs();
  prefs.remoteAccess = {
    enabled: typeof patch?.enabled === 'boolean' ? patch.enabled : cur.enabled,
    listen: typeof patch?.listen === 'string' && patch.listen.trim() ? patch.listen.trim() : cur.listen,
    port:
      Number.isInteger(patch?.port) && patch.port > 0 && patch.port < 65536 ? patch.port : cur.port,
  };
  writePrefs(prefs);
  return {
    ...prefs.remoteAccess,
    restartRequired:
      daemonRemoteAccess != null &&
      JSON.stringify(daemonRemoteAccess) !== JSON.stringify(prefs.remoteAccess),
  };
});
ipcMain.handle('marraw:relaunch', () => {
  app.relaunch();
  app.quit();
});
// Opening a library always means a NEW window, never a swap: the caller's
// window stays up while the daemon spawns. (It also has to — closing the last
// window fires window-all-closed, which quits the app.)
ipcMain.handle('marraw:open-remote', (_ev, id) => {
  const conn = remotesList().find((c) => c && c.id === id);
  if (!conn) return false;
  void createWindow({ remote: { name: conn.name, host: conn.host, token: conn.token } });
  return true;
});
ipcMain.handle('marraw:open-local', () => {
  void createWindow({});
  return true;
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
