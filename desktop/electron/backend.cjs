const { execFile } = require('node:child_process');

function readBackend(binaryPath, homePath, conversationId) {
  const args = ['desktop', '--home', homePath];
  if (conversationId !== undefined) {
    if (typeof conversationId !== 'string' || !conversationId.trim() || conversationId.length > 512) {
      return Promise.reject(new Error('Invalid conversation ID.'));
    }
    args.push('--conversation', conversationId);
  }

  return new Promise((resolve, reject) => {
    execFile(binaryPath, args, { timeout: 20000, maxBuffer: 32 * 1024 * 1024, windowsHide: true }, (error, stdout, stderr) => {
      if (error) {
        const detail = stderr.trim().split('\n').at(-1);
        reject(new Error(detail || `Cannot read Relay data: ${error.message}`));
        return;
      }
      try {
        resolve(JSON.parse(stdout));
      } catch {
        reject(new Error('Relay returned invalid data. Rebuild the Agent Relay binary and reopen the app.'));
      }
    });
  });
}

module.exports = { readBackend };
