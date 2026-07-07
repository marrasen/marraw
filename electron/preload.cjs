const { contextBridge, ipcRenderer, webUtils } = require('electron');

contextBridge.exposeInMainWorld('marraw', {
  pickDirectory: () => ipcRenderer.invoke('marraw:pick-directory'),
  revealInExplorer: (path) => ipcRenderer.invoke('marraw:reveal', path),
  // Absolute path of a dragged-in File (drop a folder anywhere to add it).
  getPathForFile: (file) => webUtils.getPathForFile(file),
  isDirectory: (path) => ipcRenderer.invoke('marraw:is-directory', path),
});
