const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { readBackend } = require('./backend.cjs');

test('reads a real isolated backend and reports missing histories', async () => {
  const homePath = await fs.mkdtemp(path.join(os.tmpdir(), 'relay desktop test '));
  const binaryPath = path.join(__dirname, '..', '..', 'bin', 'agent-relay');
  try {
    const snapshot = await readBackend(binaryPath, homePath);
    assert.deepEqual(snapshot.agents, []);
    assert.deepEqual(snapshot.conversations, []);
    assert.equal(snapshot.daemonRunning, false);
    assert.equal(snapshot.home, homePath);
    await assert.rejects(readBackend(binaryPath, homePath, 'missing; echo unexpected'), /not found/);
    await assert.rejects(readBackend(binaryPath, homePath, {}), /Invalid conversation ID/);
    await assert.rejects(readBackend(path.join(homePath, 'absent-binary'), homePath), /Cannot read Relay data/);
  } finally {
    await fs.rm(homePath, { recursive: true, force: true });
  }
});
