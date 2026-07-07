const { contextBridge, ipcRenderer, webUtils } = require('electron');

contextBridge.exposeInMainWorld('marraw', {
  pickDirectory: () => ipcRenderer.invoke('marraw:pick-directory'),
  revealInExplorer: (path) => ipcRenderer.invoke('marraw:reveal', path),
  // Absolute path of a dragged-in File (drop a folder anywhere to add it).
  getPathForFile: (file) => webUtils.getPathForFile(file),
  isDirectory: (path) => ipcRenderer.invoke('marraw:is-directory', path),
});

// Frameless-window controls (diff handoff "frameless window + baked-in controls").
contextBridge.exposeInMainWorld('win', {
  minimize: () => ipcRenderer.send('win:minimize'),
  toggleMax: () => ipcRenderer.send('win:toggleMax'),
  close: () => ipcRenderer.send('win:close'),
  toggleFullScreen: () => ipcRenderer.send('win:toggleFullScreen'),
  isMax: () => ipcRenderer.invoke('win:isMax'),
  onMaxChange: (cb) => ipcRenderer.on('win:maxChanged', (_e, v) => cb(v)),
  onFullScreenChange: (cb) => ipcRenderer.on('win:fullscreenChanged', (_e, v) => cb(v)),
});
