const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { randomUUID } = require('node:crypto');

function loadUi() {
  const handlers = {};
  const root = { innerHTML: '', addEventListener: (name, handler) => { handlers[name] = handler; } };
  const source = fs.readFileSync(path.join(__dirname, '../src/main.js'), 'utf8')
    .replace(/^import .*;\n/gm, '')
    .replace(/\nrefresh\(\);\nsetInterval[\s\S]*$/, '');
  const context = vm.createContext({
    document: { querySelector: selector => selector === '#app' ? root : null, addEventListener() {}, activeElement: null },
    window: { relay: {} }, crypto: { randomUUID }, setTimeout() {}, codexIcon: 'codex.png', claudeIcon: 'claude.png'
  });
  vm.runInContext(source, context);
  return { run: code => vm.runInContext(code, context), root, handlers, context };
}

test('inbox combines filters and sorting without changing stored conversations', () => {
  const ui = loadUi();
  ui.run(`state.snapshot = { agents: [], conversations: [
    { id: 'old', localAgentId: 'local', remoteAgentId: 'remote', title: 'First', preview: 'hello', updatedAt: '2026-01-01', messageCount: 8, unread: true, status: 'delivered' },
    { id: 'new', localAgentId: 'other', remoteAgentId: 'remote', title: 'Latest', preview: 'world', updatedAt: '2026-02-01', messageCount: 2, needsReply: true, status: 'failed' }
  ] };`);
  assert.equal(ui.run("filteredConversations().map(item => item.id).join(',')"), 'new,old');
  ui.run("state.sort = 'messages'");
  assert.equal(ui.run('filteredConversations()[0].id'), 'old');
  ui.run("state.agentId = 'other'; state.query = 'world'; state.filter = 'Failed'");
  assert.equal(ui.run('filteredConversations()[0].id'), 'new');
  ui.run("state.filter = 'Unread'");
  assert.equal(ui.run('filteredConversations().length'), 0);
  assert.equal(ui.run('state.snapshot.conversations[0].id'), 'old');
});

test('drafts stay separate across conversation switches and rendering escapes message content', () => {
  const ui = loadUi();
  ui.run("state.selectedId = 'one'; getDraft().text = 'First draft'; state.selectedId = 'two'; getDraft().text = 'Second draft'; state.selectedId = 'one'");
  assert.equal(ui.run('getDraft().text'), 'First draft');
  assert.equal(ui.run("escapeHtml('<img src=x onerror=alert(1)>')"), '&lt;img src=x onerror=alert(1)&gt;');
});

test('late history responses cannot overwrite the selected conversation', async () => {
  const ui = loadUi();
  const pending = {};
  ui.context.window.relay.history = id => new Promise(resolve => { pending[id] = resolve; });
  ui.run("state.selectedId = 'one'");
  const first = ui.run('loadHistory()');
  ui.run("state.selectedId = 'two'; state.historyLoading = false");
  const second = ui.run('loadHistory()');
  pending.two([{ Text: 'Second history' }]);
  await second;
  pending.one([{ Text: 'First history' }]);
  await first;
  assert.equal(ui.run('state.historyId'), 'two');
  assert.equal(ui.run('state.history[0].Text'), 'Second history');
});
