// Bridge for the connect screen (connect.html): saved remote connections CRUD
// plus open actions. Deliberately separate from preload.cjs — this window
// never talks to a daemon, so it gets none of the app bridges.
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('connectApi', {
  listRemotes: () => ipcRenderer.invoke('marraw:remotes-list'),
  saveRemote: (conn) => ipcRenderer.invoke('marraw:remotes-save', conn),
  deleteRemote: (id) => ipcRenderer.invoke('marraw:remotes-delete', id),
  testRemote: (host, token) => ipcRenderer.invoke('marraw:remote-test', host, token),
  openRemote: (id) => ipcRenderer.invoke('marraw:open-remote', id),
  openLocal: () => ipcRenderer.invoke('marraw:open-local'),
  getLaunchMode: () => ipcRenderer.invoke('marraw:get-launch-mode'),
  setLaunchMode: (mode) => ipcRenderer.invoke('marraw:set-launch-mode', mode),
  close: () => ipcRenderer.send('win:close'),
});
