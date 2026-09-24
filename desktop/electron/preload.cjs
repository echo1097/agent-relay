const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('relay', {
  snapshot: () => ipcRenderer.invoke('relay:snapshot'),
  history: conversationId => ipcRenderer.invoke('relay:history', conversationId),
  copy: text => ipcRenderer.invoke('relay:copy', text),
  export: text => ipcRenderer.invoke('relay:export', text)
});
