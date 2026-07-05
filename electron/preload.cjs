const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('marraw', {
  pickDirectory: () => ipcRenderer.invoke('marraw:pick-directory'),
  revealInExplorer: (path) => ipcRenderer.invoke('marraw:reveal', path),
});
