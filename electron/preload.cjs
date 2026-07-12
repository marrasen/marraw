const { contextBridge, ipcRenderer, webUtils } = require('electron');

contextBridge.exposeInMainWorld('marraw', {
  pickDirectory: () => ipcRenderer.invoke('marraw:pick-directory'),
  pickImage: () => ipcRenderer.invoke('marraw:pick-image'),
  revealInExplorer: (path) => ipcRenderer.invoke('marraw:reveal', path),
  // Absolute path of a dragged-in File (drop a folder anywhere to add it).
  getPathForFile: (file) => webUtils.getPathForFile(file),
  isDirectory: (path) => ipcRenderer.invoke('marraw:is-directory', path),
  // Background auto-update opt-out. Unsigned macOS builds can never update
  // themselves, so the setting is hidden rather than shown as a dead toggle.
  updatesSupported: process.platform !== 'darwin',
  getAutoUpdate: () => ipcRenderer.invoke('marraw:get-auto-update'),
  setAutoUpdate: (on) => ipcRenderer.invoke('marraw:set-auto-update', on),
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
