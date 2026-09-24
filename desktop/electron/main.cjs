const { app, BrowserWindow, ipcMain, clipboard, dialog } = require('electron');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { pathToFileURL } = require('node:url');
const { readBackend } = require('./backend.cjs');

function readOption(args, name, fallback) {
  const index = args.indexOf(name);
  return index >= 0 && args[index + 1] ? args[index + 1] : fallback;
}

let binaryPath = readOption(process.argv, '--relay-binary', process.env.AGENT_RELAY_BINARY || path.join(__dirname, '..', '..', 'bin', process.platform === 'win32' ? 'agent-relay.exe' : 'agent-relay'));
let homePath = readOption(process.argv, '--relay-home', path.join(os.homedir(), '.agent-relay'));
const rendererUrl = pathToFileURL(path.join(__dirname, '..', 'dist', 'index.html')).href;
let pendingSnapshot;

function validateSender(event) {
  if (!mainWindow || event.sender !== mainWindow.webContents || event.senderFrame !== mainWindow.webContents.mainFrame || event.senderFrame.url !== rendererUrl) {
    throw new Error('This window cannot access Relay data.');
  }
}

let mainWindow;

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1360,
    height: 900,
    minWidth: 960,
    minHeight: 640,
    backgroundColor: '#111312',
    title: 'Agent Relay',
    titleBarStyle: 'hiddenInset',
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
      sandbox: true,
      preload: path.join(__dirname, 'preload.cjs')
    }
  });

  mainWindow.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  mainWindow.webContents.on('will-navigate', event => event.preventDefault());
  mainWindow.loadFile(path.join(__dirname, '..', 'dist', 'index.html'));
  mainWindow.on('closed', () => {
    mainWindow = null;
  });
}

if (!app.requestSingleInstanceLock({ binaryPath, homePath })) {
  app.quit();
} else {
  app.on('second-instance', (event, args, workingDirectory, launchData) => {
    if (typeof launchData?.binaryPath === 'string' && path.isAbsolute(launchData.binaryPath)) {
      binaryPath = launchData.binaryPath;
    }
    if (typeof launchData?.homePath === 'string' && path.isAbsolute(launchData.homePath)) {
      homePath = launchData.homePath;
    }
    pendingSnapshot = null;
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.show();
      mainWindow.focus();
      mainWindow.reload();
    } else if (app.isReady()) {
      createWindow();
    }
  });

  app.whenReady().then(() => {
    ipcMain.handle('relay:snapshot', event => {
      validateSender(event);
      if (!pendingSnapshot) {
        const snapshotRequest = readBackend(binaryPath, homePath).finally(() => {
          if (pendingSnapshot === snapshotRequest) pendingSnapshot = null;
        });
        pendingSnapshot = snapshotRequest;
      }
      return pendingSnapshot;
    });
    ipcMain.handle('relay:history', (event, conversationId) => {
      validateSender(event);
      return readBackend(binaryPath, homePath, conversationId);
    });
    ipcMain.handle('relay:copy', (event, text) => {
      validateSender(event);
      if (typeof text !== 'string' || text.length > 32 * 1024 * 1024) throw new Error('Invalid conversation text.');
      clipboard.writeText(text);
    });
    ipcMain.handle('relay:export', async (event, text) => {
      validateSender(event);
      if (typeof text !== 'string' || text.length > 32 * 1024 * 1024) throw new Error('Invalid conversation text.');
      const result = await dialog.showSaveDialog(mainWindow, { defaultPath: 'relay-conversation.txt', filters: [{ name: 'Text file', extensions: ['txt'] }] });
      if (result.canceled || !result.filePath) return false;
      await fs.writeFile(result.filePath, text, { mode: 0o600 });
      return true;
    });
    createWindow();
  });
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
  });
}
