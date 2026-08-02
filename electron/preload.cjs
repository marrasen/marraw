const { contextBridge, ipcRenderer, webUtils } = require('electron');

contextBridge.exposeInMainWorld('marraw', {
  pickDirectory: () => ipcRenderer.invoke('marraw:pick-directory'),
  pickImage: () => ipcRenderer.invoke('marraw:pick-image'),
  revealInExplorer: (path) => ipcRenderer.invoke('marraw:reveal', path),
  // Absolute path of a dragged-in File (drop a folder anywhere to add it).
  getPathForFile: (file) => webUtils.getPathForFile(file),
  isDirectory: (path) => ipcRenderer.invoke('marraw:is-directory', path),
  // Puts an encoded image (PNG/JPEG bytes) on the system clipboard. Native
  // clipboard has no document-focus requirement, unlike navigator.clipboard.
  copyImageToClipboard: (buf) => ipcRenderer.invoke('marraw:copy-image', buf),
  // Background auto-update opt-out. Unsigned macOS builds can never update
  // themselves, and on Linux only the AppImage packaging self-updates (a .deb
  // install has no updater) — hide the setting rather than show a dead toggle.
  updatesSupported:
    process.platform === 'win32' ||
    (process.platform === 'linux' && !!process.env.APPIMAGE),
  getAutoUpdate: () => ipcRenderer.invoke('marraw:get-auto-update'),
  setAutoUpdate: (on) => ipcRenderer.invoke('marraw:set-auto-update', on),
  // Update check/download/install, driven from Settings → Updates. The state
  // object covers every phase; onUpdateState pushes each change and returns
  // its own unsubscribe.
  getUpdateState: () => ipcRenderer.invoke('marraw:get-update-state'),
  checkForUpdates: () => ipcRenderer.invoke('marraw:check-updates'),
  downloadUpdate: () => ipcRenderer.invoke('marraw:download-update'),
  installUpdate: () => ipcRenderer.invoke('marraw:install-update'),
  onUpdateState: (cb) => {
    const handler = (_e, state) => cb(state);
    ipcRenderer.on('marraw:update-state', handler);
    return () => ipcRenderer.off('marraw:update-state', handler);
  },
  // Beta-channel opt-in (GitHub pre-releases). Unset follows the running
  // version; see main.cjs betaChannelEnabled.
  getBetaChannel: () => ipcRenderer.invoke('marraw:get-beta-channel'),
  setBetaChannel: (on) => ipcRenderer.invoke('marraw:set-beta-channel', on),
  // Saved connections to other machines' libraries. They live in the shell's
  // prefs, not the daemon's settings, so they are the same list in every
  // window — including a remote one, whose shell is still this machine's.
  listRemotes: () => ipcRenderer.invoke('marraw:remotes-list'),
  saveRemote: (conn) => ipcRenderer.invoke('marraw:remotes-save', conn),
  deleteRemote: (id) => ipcRenderer.invoke('marraw:remotes-delete', id),
  // Reachability + token check, run in the main process (no CORS in play).
  testRemote: (host, token) => ipcRenderer.invoke('marraw:remote-test', host, token),
  openRemote: (id) => ipcRenderer.invoke('marraw:open-remote', id),
  openLocal: () => ipcRenderer.invoke('marraw:open-local'),
  // Finding machines (mDNS + Tailscale) and asking one to let us in. All of
  // it runs in the main process: no CORS, and the daemon's pairing endpoints
  // send no CORS headers precisely so only a native client can drive them.
  scanRemotes: () => ipcRenderer.invoke('marraw:remote-scan'),
  pairRemote: (host) => ipcRenderer.invoke('marraw:remote-pair', host),
  waitRemotePairing: (host, requestId) =>
    ipcRenderer.invoke('marraw:remote-pair-wait', host, requestId),
  cancelRemotePairing: (requestId) => ipcRenderer.invoke('marraw:remote-pair-cancel', requestId),
  // Hosting this library to other machines: shell prefs (spawn flags, so a
  // change needs a relaunch).
  getRemoteAccess: () => ipcRenderer.invoke('marraw:get-remote-access'),
  setRemoteAccess: (patch) => ipcRenderer.invoke('marraw:set-remote-access', patch),
  relaunch: () => ipcRenderer.invoke('marraw:relaunch'),
});

// Frameless-window controls (diff handoff "frameless window + baked-in controls").
contextBridge.exposeInMainWorld('win', {
  minimize: () => ipcRenderer.send('win:minimize'),
  toggleMax: () => ipcRenderer.send('win:toggleMax'),
  close: () => ipcRenderer.send('win:close'),
  toggleFullScreen: () => ipcRenderer.send('win:toggleFullScreen'),
  isMax: () => ipcRenderer.invoke('win:isMax'),
  // Opens another window in this instance; folderPath auto-opens there.
  openNewWindow: (folderPath) => ipcRenderer.send('win:openNew', folderPath ?? null),
  onMaxChange: (cb) => ipcRenderer.on('win:maxChanged', (_e, v) => cb(v)),
  onFullScreenChange: (cb) => ipcRenderer.on('win:fullscreenChanged', (_e, v) => cb(v)),
});
