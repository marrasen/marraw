const { contextBridge, ipcRenderer, webUtils } = require('electron');

contextBridge.exposeInMainWorld('marraw', {
  pickDirectory: () => ipcRenderer.invoke('marraw:pick-directory'),
  pickImage: () => ipcRenderer.invoke('marraw:pick-image'),
  revealInExplorer: (path) => ipcRenderer.invoke('marraw:reveal', path),
  // Absolute path of a dragged-in File (drop a folder anywhere to add it).
  getPathForFile: (file) => webUtils.getPathForFile(file),
  isDirectory: (path) => ipcRenderer.invoke('marraw:is-directory', path),
  // Background auto-update opt-out. Unsigned macOS builds can never update
  // themselves, and on Linux only the AppImage packaging self-updates (a .deb
  // install has no updater) — hide the setting rather than show a dead toggle.
  updatesSupported:
    process.platform === 'win32' ||
    (process.platform === 'linux' && !!process.env.APPIMAGE),
  getAutoUpdate: () => ipcRenderer.invoke('marraw:get-auto-update'),
  setAutoUpdate: (on) => ipcRenderer.invoke('marraw:set-auto-update', on),
  // Beta-channel opt-in (GitHub pre-releases). Unset follows the running
  // version; see main.cjs betaChannelEnabled.
  getBetaChannel: () => ipcRenderer.invoke('marraw:get-beta-channel'),
  setBetaChannel: (on) => ipcRenderer.invoke('marraw:set-beta-channel', on),
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
