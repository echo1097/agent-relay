import './style.css';
import codexIcon from '../../assets/codex.png';
import claudeIcon from '../../assets/claude.png';

const appRoot = document.querySelector('#app');
const state = {
  page: 'Inbox', selectedId: null, filter: 'All', query: '', agentId: '', sort: 'newest',
  snapshot: null, error: '', loading: false, history: [], historyId: null,
  historyError: '', historyLoading: false, historyRequest: 0, visibleCount: 50, messageLimit: 100,
  agentQuery: '', agentFilter: 'All', details: false, drafts: {}, sending: false, sendError: '', notice: ''
};
const iconPaths = {
  Overview: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  Inbox: '<path d="M4 4h16l2 12v4H2v-4L4 4Z"/><path d="M2 15h6l2 3h4l2-3h6"/>',
  Agents: '<circle cx="9" cy="8" r="3"/><path d="M3 21v-3a6 6 0 0 1 12 0v3M16 5a3 3 0 0 1 0 6m2 4a5 5 0 0 1 3 5"/>',
  search: '<circle cx="10" cy="10" r="6.5"/><path d="m15 15 5 5"/>',
  refresh: '<path d="M20 8a8 8 0 1 0 0 8M20 3v5h-5"/>',
  left: '<path d="m14 6-6 6 6 6"/>', right: '<path d="m10 6 6 6-6 6"/>',
  close: '<path d="m6 6 12 12M6 18 18 6"/>',
  copy: '<rect x="8" y="8" width="12" height="13" rx="2"/><path d="M15 8V3H3v13h5"/>',
  download: '<path d="M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-10v1"/>',
  send: '<path d="m3 3 18 9-18 9 4-9-4-9Zm4 9h14"/>',
  arrow: '<path d="M5 19 19 5M5 5h14v14"/>'
};

function icon(name) {
  return `<svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">${iconPaths[name] || iconPaths.info}</svg>`;
}

function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
}

function getAgents() { return state.snapshot?.agents ?? []; }
function getConversations() { return state.snapshot?.conversations ?? []; }
function getAgent(agentId) {
  return getAgents().find(agent => agent.id === agentId) ?? { id: agentId, display_name: agentId ? `Agent ${agentId.slice(-8)}` : 'Unknown agent', status: 'unknown', node: { name: 'Unavailable' } };
}
function agentName(agentId) { return escapeHtml(getAgent(agentId).display_name); }
function isActive(agent) { return !agent.archived && ['online', 'busy', 'idle'].includes(agent.status); }

function formatTime(value, short = false) {
  const date = new Date(value);
  if (!value || Number.isNaN(date.getTime())) return 'Unknown time';
  const today = date.toDateString() === new Date().toDateString();
  return date.toLocaleString(undefined, short && !today ? { month: 'short', day: 'numeric' } : short ? { hour: 'numeric', minute: '2-digit' } : { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
}

function statusLabel(conversation) {
  return conversation.needsReply ? 'Needs reply' : String(conversation.status || 'empty').replaceAll('_', ' ');
}

function renderAvatar(agentId) {
  const agent = getAgent(agentId);
  const provider = agent.provider || agent.display_name || '';
  const name = /claude/i.test(provider) ? 'Claude' : /codex/i.test(provider) ? 'Codex' : '';
  return name ? `<span class="avatar providerIcon ${name.toLowerCase()}Icon"><img src="${name === 'Claude' ? claudeIcon : codexIcon}" alt="${name}" width="32" height="32" /></span>` : `<span class="avatar" aria-label="Unknown provider">${icon('Agents')}</span>`;
}

function filteredConversations() {
  const query = state.query.trim().toLowerCase();
  const items = getConversations().filter(conversation => {
    const matchesFilter = state.filter === 'All' || (state.filter === 'Unread' && conversation.unread) || (state.filter === 'Needs reply' && conversation.needsReply) || (state.filter === 'Failed' && conversation.status === 'failed');
    const matchesAgent = !state.agentId || [conversation.localAgentId, conversation.remoteAgentId].includes(state.agentId);
    const text = [conversation.title, conversation.preview, conversation.id, getAgent(conversation.localAgentId).display_name, getAgent(conversation.remoteAgentId).display_name].join(' ').toLowerCase();
    return matchesFilter && matchesAgent && text.includes(query);
  });
  return items.sort((first, second) => state.sort === 'oldest' ? first.updatedAt.localeCompare(second.updatedAt) : state.sort === 'messages' ? second.messageCount - first.messageCount : second.updatedAt.localeCompare(first.updatedAt));
}

function renderRows(items) {
  return items.map(conversation => `<button class="conversationRow ${conversation.id === state.selectedId && state.page === 'Inbox' ? 'selected' : ''}" data-conversation="${escapeHtml(conversation.id)}" ${conversation.id === state.selectedId ? 'aria-current="true"' : ''}>
    ${renderAvatar(conversation.localAgentId)}<span class="rowContent"><span class="rowTop"><strong>${agentName(conversation.localAgentId)}</strong><time title="${escapeHtml(formatTime(conversation.updatedAt))}">${escapeHtml(formatTime(conversation.updatedAt, true))}</time></span>
    <span class="rowHeading">${conversation.unread ? '<i class="unreadDot" title="Unread by agent"></i>' : ''}${escapeHtml(conversation.title)}</span>
    <span class="preview">${escapeHtml(conversation.preview)}</span><span class="rowBottom"><span>With ${agentName(conversation.remoteAgentId)}</span>${conversation.needsReply ? '<span class="replyLabel">Needs reply</span>' : `<span>${conversation.messageCount} messages</span>`}</span></span></button>`).join('');
}

function emptyState(title, description, action = '') {
  return `<div class="empty"><span class="emptyIcon">${icon('Inbox')}</span><h2>${title}</h2><p>${description}</p>${action}</div>`;
}

function renderOverview() {
  const conversations = getConversations();
  const active = getAgents().filter(isActive);
  return `<section class="pageIntro"><h1>Your workspace, at a glance.</h1><p>Follow the conversations between your agents.</p></section>
    <div class="summaryStrip"><button data-page="Agents"><strong>${active.length}</strong> active agents</button><button data-page="Inbox"><strong>${conversations.length}</strong> conversations</button><button data-needs-reply="true"><strong>${conversations.filter(item => item.needsReply).length}</strong> need a reply</button></div>
    <section class="recent"><div class="sectionHeading"><h2>Recent conversations</h2><button class="textButton" data-page="Inbox">Open inbox ${icon('right')}</button></div>${renderRows(conversations.slice(0, 6)) || emptyState('Your history starts here', 'Conversations appear when connected agents exchange messages through Relay.')}</section>
    <div class="overviewNote">${icon('info')} This computer stores your history. Connected peers provide the latest agent details.</div>`;
}

function renderAgents() {
  const query = state.agentQuery.toLowerCase();
  const agents = getAgents().filter(agent => (state.agentFilter === 'All' || (state.agentFilter === 'Active' ? isActive(agent) : agent.archived)) && [agent.display_name, agent.task, agent.provider, agent.node.name].join(' ').toLowerCase().includes(query));
  return `<section class="pageIntro"><h1>Agents <span class="headingCount">${agents.length}</span></h1><p>Choose an agent to explore their conversations.</p></section><div class="agentTools"><label class="search">${icon('search')}<input id="agentSearch" placeholder="Search agents or tasks" aria-label="Search agents" value="${escapeHtml(state.agentQuery)}" /></label><select id="agentStatus" aria-label="Agent status">${['All', 'Active', 'Archived'].map(value => `<option ${state.agentFilter === value ? 'selected' : ''}>${value}</option>`).join('')}</select></div>
    <div class="agentTable"><div class="agentTableHeader"><span>Agent</span><span>Current task</span><span>Computer</span><span>Status</span></div>${agents.slice(0, state.visibleCount).map(agent => `<button class="agentRow" data-agent="${escapeHtml(agent.id)}"><span class="agentIdentity">${renderAvatar(agent.id)}<span><strong>${escapeHtml(agent.display_name)}</strong><small>${escapeHtml(agent.provider || 'Unknown provider')}</small></span></span><span class="agentTask">${escapeHtml(agent.task || 'No task shared')}</span><span>${escapeHtml(agent.node.name)}</span><span class="presence ${isActive(agent) ? 'active' : ''}"><i></i>${escapeHtml(agent.archived ? 'Archived' : agent.status)}</span></button>`).join('') || emptyState('No matching agents', 'Try a different search or status filter.')}</div>${agents.length > state.visibleCount ? '<button class="secondary loadMore" data-more="true">Show more agents</button>' : ''}`;
}

function getDraft() {
  if (!state.drafts[state.selectedId]) state.drafts[state.selectedId] = { text: '', replyTo: '', messageId: crypto.randomUUID() };
  return state.drafts[state.selectedId];
}

function renderComposer(conversation) {
  const draft = getDraft();
  const questions = state.historyId === conversation.id ? state.history.filter(message => message.Type === 'question' && message.ReceivedAt && message.RecipientAgentID === conversation.localAgentId && ['delivered', 'pending'].includes(message.Status) && (!message.ExpiresAt || new Date(message.ExpiresAt) > new Date())) : [];
  if (draft.replyTo && state.historyId === conversation.id && !questions.some(message => message.ID === draft.replyTo)) draft.replyTo = '';
  return `<form class="composer" id="replyForm"><div class="composerIdentity"><span>Send as <strong>${agentName(conversation.localAgentId)}</strong></span>${questions.length ? `<select id="replyTarget" aria-label="Reply type" ${state.sending ? 'disabled' : ''}><option value="">New message</option>${questions.map(message => `<option value="${escapeHtml(message.ID)}" ${draft.replyTo === message.ID ? 'selected' : ''}>Answer: ${escapeHtml(message.Text.slice(0, 65))}</option>`).join('')}</select>` : ''}</div><textarea id="replyText" aria-label="Reply message" placeholder="Write a message…" maxlength="16000" ${state.sending ? 'disabled' : ''}>${escapeHtml(draft.text)}</textarea>${state.sendError ? `<p class="sendError" role="alert">${escapeHtml(state.sendError)}</p>` : ''}<div class="composerActions"><span>${!window.relay?.send ? 'Reply setup pending: choose a sending identity.' : state.snapshot.daemonRunning ? '⌘ Enter to send' : 'Relay is stopped. Messages will queue.'}</span><button class="primary" type="submit" ${state.sending || !window.relay?.send || !draft.text.trim() ? 'disabled' : ''}>${state.sending ? 'Sending…' : 'Send message'} ${icon('send')}</button></div></form>`;
}

function renderThread() {
  const conversation = getConversations().find(item => item.id === state.selectedId);
  if (!conversation) return emptyState('A clear view of the conversation', 'Choose a conversation on the left to read its history and send a reply.');
  const items = filteredConversations();
  const index = items.findIndex(item => item.id === conversation.id);
  let content = '<div class="skeleton" role="status" aria-label="Loading conversation"><i></i><i></i><i></i></div>';
  if (state.historyError) content = emptyState('Conversation could not load', escapeHtml(state.historyError), '<button class="secondary" data-retry-history="true">Try again</button>');
  else if (state.historyId === conversation.id) {
    content = `<div class="messages">${state.history.length > state.messageLimit ? '<button class="secondary loadMore" data-older="true">Show older messages</button>' : ''}${state.history.slice(-state.messageLimit).map(message => `<article class="message">${renderAvatar(message.SenderAgentID)}<div><div class="messageHeading"><strong>${agentName(message.SenderAgentID)}</strong><time>${escapeHtml(formatTime(message.CreatedAt))}</time></div><p>${escapeHtml(message.Text)}</p><div class="messageStatus">${escapeHtml(message.Type)} · ${escapeHtml(message.Type === 'question' && message.Status !== 'answered' && message.ExpiresAt && new Date(message.ExpiresAt) <= new Date() ? 'expired' : String(message.Status).replaceAll('_', ' '))}</div></div></article>`).join('') || emptyState('No messages yet', 'Send the first message below.')}</div>`;
  }
  return `<div class="threadToolbar"><span>${index >= 0 ? `${index + 1} of ${items.length}` : 'Outside current filter'}</span><div class="toolbarButtons"><button class="iconButton" data-step="-1" title="Previous conversation" aria-label="Previous conversation" ${index <= 0 ? 'disabled' : ''}>${icon('left')}</button><button class="iconButton" data-step="1" title="Next conversation" aria-label="Next conversation" ${index < 0 || index >= items.length - 1 ? 'disabled' : ''}>${icon('right')}</button><span class="toolbarDivider"></span><button class="iconButton" data-copy="true" title="Copy conversation" aria-label="Copy conversation" ${state.historyId !== conversation.id ? 'disabled' : ''}>${icon('copy')}</button><button class="iconButton" data-export="true" title="Export conversation" aria-label="Export conversation" ${state.historyId !== conversation.id ? 'disabled' : ''}>${icon('download')}</button><button class="iconButton" data-details="true" title="Conversation details" aria-label="Conversation details" aria-pressed="${state.details}">${icon('info')}</button></div></div>
    <div class="threadBody"><div class="threadHeader"><h2>${escapeHtml(conversation.title)}</h2><p>${agentName(conversation.localAgentId)} <span>to</span> ${agentName(conversation.remoteAgentId)}</p><span class="badge ${conversation.needsReply ? 'amber' : ''}">${escapeHtml(statusLabel(conversation))}</span>${state.details ? `<dl class="threadDetails"><dt>Conversation ID</dt><dd>${escapeHtml(conversation.id)}</dd><dt>Messages</dt><dd>${conversation.messageCount}</dd><dt>Unread</dt><dd>${conversation.unread ? 'Unread by the receiving agent' : 'Read by the receiving agent'}</dd></dl>` : ''}</div>${content}</div>${renderComposer(conversation)}`;
}

function renderList() {
  const items = filteredConversations();
  return `<div class="listCount">${items.length} ${items.length === 1 ? 'conversation' : 'conversations'}</div>` + (renderRows(items.slice(0, state.visibleCount)) || emptyState('No conversations found', 'Try clearing the search and filters.', '<button class="secondary" data-reset="true">Clear filters</button>')) + (items.length > state.visibleCount ? '<button class="secondary loadMore" data-more="true">Show more</button>' : '');
}

function renderInbox() {
  const agentIds = [...new Set(getConversations().flatMap(item => [item.localAgentId, item.remoteAgentId]))];
  return `<section class="inbox"><div class="inboxList"><div class="inboxTitle"><h1>Inbox</h1><span>${getConversations().length}</span></div><div class="inboxTools"><label class="search">${icon('search')}<input id="searchInput" aria-label="Search conversations" placeholder="Search conversations" value="${escapeHtml(state.query)}" /><kbd>⌘ K</kbd></label><div class="filters" aria-label="Conversation status">${['All', 'Unread', 'Needs reply', 'Failed'].map(filter => `<button data-filter="${filter}" aria-pressed="${state.filter === filter}">${filter}</button>`).join('')}</div><div class="filterRow"><select id="agentFilter" aria-label="Filter by agent"><option value="">All agents</option>${agentIds.map(id => `<option value="${escapeHtml(id)}" ${state.agentId === id ? 'selected' : ''}>${agentName(id)}</option>`).join('')}</select><select id="sortOrder" aria-label="Sort conversations">${[['newest', 'Newest first'], ['oldest', 'Oldest first'], ['messages', 'Most messages']].map(([value, label]) => `<option value="${value}" ${state.sort === value ? 'selected' : ''}>${label}</option>`).join('')}</select></div></div><div id="conversationList">${renderList()}</div><div class="listFooter">Unread status belongs to your agents.</div></div><section class="thread" aria-label="Conversation">${renderThread()}</section></section>`;
}

function render() {
  const focused = document.activeElement;
  const focusEntry = focused?.matches('button') ? Object.entries(focused.dataset)[0] : null;
  const connectionOpen = document.querySelector('.connection')?.open;
  const inputId = focused?.matches('input, textarea, select') ? focused.id : '';
  const selection = focused?.matches('input, textarea') ? [focused.selectionStart, focused.selectionEnd] : null;
  const scrolls = ['.threadBody', '#conversationList', '.pageContent'].map(selector => [selector, document.querySelector(selector)?.scrollTop || 0]);
  const snapshot = state.snapshot;
  let content = '<div class="skeleton workspaceLoading" role="status" aria-label="Loading workspace"><i></i><i></i><i></i></div>';
  if (snapshot) content = state.page === 'Overview' ? renderOverview() : state.page === 'Inbox' ? renderInbox() : renderAgents();
  else if (state.error) content = emptyState('Could not load your workspace', 'Try refreshing to reconnect to the local backend.');
  appRoot.innerHTML = `<aside class="sidebar"><div class="brand"><span class="brandMark">${icon('arrow')}</span><span>agent<span class="muted">relay</span></span></div><nav aria-label="Main navigation">${['Overview', 'Inbox', 'Agents'].map(page => `<button data-page="${page}" class="navButton ${state.page === page ? 'current' : ''}" ${state.page === page ? 'aria-current="page"' : ''}>${icon(page)}<span>${page}</span>${page === 'Inbox' ? `<small>${getConversations().filter(item => item.unread).length}</small>` : ''}</button>`).join('')}</nav><div class="sidebarBottom"><details class="connection"><summary><span class="presence ${snapshot?.daemonRunning ? 'active' : ''}"><i></i>${snapshot?.daemonRunning ? 'Relay running' : snapshot ? 'Relay stopped' : 'Connecting'}</span></summary><p>${snapshot?.daemonRunning ? 'Checks for updates every 15 seconds.' : 'Start delivery with:'}</p>${snapshot && !snapshot.daemonRunning ? '<code>agent-relay service start</code>' : ''}${snapshot?.unavailableNodes?.length ? `<p>${snapshot.unavailableNodes.length} peer computer unavailable. Saved history is still available.</p>` : ''}${snapshot?.dnd ? '<p>Do not disturb is on.</p>' : ''}<p>${escapeHtml(snapshot?.home || '')}</p></details><div class="workspaceName">${escapeHtml(snapshot?.node.name || 'Local workspace')}<span>Local workspace</span></div></div></aside><main><header class="topbar"><span>${state.page}</span><div class="topbarActions"><span class="syncNote">${snapshot ? `Updated ${escapeHtml(formatTime(snapshot.updatedAt, true))}` : 'Connecting to Relay'}</span><button class="iconButton" data-refresh="true" title="Refresh workspace" aria-label="Refresh workspace" ${state.loading ? 'disabled' : ''}>${icon('refresh')}</button></div></header>${state.error ? `<div class="connectionNotice" role="alert">${escapeHtml(state.error)} <button class="textButton" data-refresh="true">Try again</button></div>` : ''}<div class="pageContent ${state.page === 'Inbox' ? 'inboxPage' : ''}">${content}</div></main>${state.notice ? `<div class="toast" role="status">${escapeHtml(state.notice)}</div>` : ''}`;
  for (const [selector, scrollTop] of scrolls) {
    const element = document.querySelector(selector);
    if (element) element.scrollTop = scrollTop;
  }
  if (inputId) {
    const input = document.getElementById(inputId);
    input?.focus({ preventScroll: true });
    if (selection && input?.setSelectionRange) input.setSelectionRange(...selection);
  } else if (focusEntry) {
    const button = [...document.querySelectorAll('button')].find(item => item.dataset[focusEntry[0]] === focusEntry[1]);
    button?.focus({ preventScroll: true });
  }
  if (connectionOpen) document.querySelector('.connection').open = true;
}

function notify(message) {
  state.notice = message;
  render();
  setTimeout(() => { if (state.notice === message) { state.notice = ''; render(); } }, 4000);
}

async function loadHistory(force = false) {
  const conversationId = state.selectedId;
  if (!conversationId || state.page !== 'Inbox') return;
  if (!force && (state.historyLoading || state.historyId === conversationId)) return;
  const requestId = ++state.historyRequest;
  state.historyLoading = true;
  state.historyError = '';
  render();
  try {
    const history = await window.relay.history(conversationId);
    if (requestId !== state.historyRequest || conversationId !== state.selectedId) return;
    state.history = history;
    state.historyId = conversationId;
  } catch (error) {
    if (requestId === state.historyRequest && conversationId === state.selectedId) state.historyError = error.message;
  } finally {
    if (requestId === state.historyRequest) { state.historyLoading = false; render(); }
  }
}

async function refresh() {
  if (state.loading) return;
  state.loading = true;
  render();
  try {
    if (!window.relay) throw new Error('Open with agent-relay app to connect to your backend.');
    state.snapshot = await window.relay.snapshot();
    state.error = '';
    if (!getConversations().some(item => item.id === state.selectedId)) {
      state.selectedId = null;
      state.historyId = null;
    }
    await loadHistory(true);
  } catch (error) { state.error = error.message; }
  finally { state.loading = false; render(); }
}

function selectConversation(id) {
  state.page = 'Inbox';
  state.selectedId = id;
  state.historyLoading = false;
  state.historyError = '';
  state.sendError = '';
  state.messageLimit = 100;
  state.details = false;
  render();
  const body = document.querySelector('.threadBody');
  if (body) body.scrollTop = 0;
  loadHistory();
}

function conversationText() {
  const conversation = getConversations().find(item => item.id === state.selectedId);
  return `${conversation.title}\n${conversation.id}\n\n` + state.history.map(message => `${getAgent(message.SenderAgentID).display_name} · ${formatTime(message.CreatedAt)}\n${message.Text}`).join('\n\n');
}

async function sendReply() {
  if (!window.relay?.send) { notify('Reply setup is waiting for the sending identity choice.'); return; }
  const conversationId = state.selectedId;
  const draft = getDraft();
  if (state.sending || !draft.text.trim()) return;
  state.sending = true;
  state.sendError = '';
  render();
  try {
    await window.relay.send({ conversationId, text: draft.text, replyTo: draft.replyTo, messageId: `msg_${draft.messageId}` });
    delete state.drafts[conversationId];
    notify(state.snapshot.daemonRunning ? 'Message queued for delivery' : 'Message queued. Start Relay to deliver it.');
    await loadHistory(true);
    refresh();
  } catch (error) {
    if (state.selectedId === conversationId) state.sendError = error.message;
    else notify(`Message was not sent: ${error.message}`);
  } finally { state.sending = false; render(); }
}

appRoot.addEventListener('submit', event => {
  if (event.target.id === 'replyForm') { event.preventDefault(); sendReply(); }
});

appRoot.addEventListener('click', async event => {
  const button = event.target.closest('button');
  if (!button || button.disabled || (button.closest('form') && button.type === 'submit')) return;
  if (button.dataset.refresh) { refresh(); return; }
  if (button.dataset.retryHistory) { loadHistory(true); return; }
  if (button.dataset.conversation) { selectConversation(button.dataset.conversation); return; }
  if (button.dataset.step) {
    const items = filteredConversations();
    const index = items.findIndex(item => item.id === state.selectedId);
    const next = items[index + Number(button.dataset.step)];
    if (next) selectConversation(next.id);
    return;
  }
  if (button.dataset.copy || button.dataset.export) {
    try {
      if (button.dataset.copy) { await window.relay.copy(conversationText()); notify('Conversation copied'); }
      else if (await window.relay.export(conversationText())) notify('Conversation exported');
    } catch (error) { notify(error.message); }
    return;
  }
  if (button.dataset.page) { state.page = button.dataset.page; state.visibleCount = 50; }
  if (button.dataset.filter) { state.filter = button.dataset.filter; state.visibleCount = 50; }
  if (button.dataset.more) state.visibleCount += 50;
  if (button.dataset.older) state.messageLimit += 100;
  if (button.dataset.details) state.details = !state.details;
  if (button.dataset.reset) { state.filter = 'All'; state.agentId = ''; state.query = ''; }
  if (button.dataset.needsReply) { state.page = 'Inbox'; state.filter = 'Needs reply'; state.agentId = ''; state.query = ''; }
  if (button.dataset.agent) { state.page = 'Inbox'; state.agentId = button.dataset.agent; state.query = ''; state.filter = 'All'; state.selectedId = null; }
  render();
  loadHistory();
});

appRoot.addEventListener('input', event => {
  if (event.target.id === 'replyText') {
    getDraft().text = event.target.value;
    document.querySelector('#replyForm button[type="submit"]').disabled = state.sending || !window.relay?.send || !event.target.value.trim();
    return;
  }
  if (event.target.id === 'searchInput') { state.query = event.target.value; state.visibleCount = 50; render(); }
  if (event.target.id === 'agentSearch') { state.agentQuery = event.target.value; state.visibleCount = 50; render(); }
});

appRoot.addEventListener('change', event => {
  const fields = { agentFilter: 'agentId', sortOrder: 'sort', agentStatus: 'agentFilter' };
  if (fields[event.target.id]) { state[fields[event.target.id]] = event.target.value; state.visibleCount = 50; render(); }
  if (event.target.id === 'replyTarget') getDraft().replyTo = event.target.value;
});

document.addEventListener('keydown', event => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); state.page = 'Inbox'; render(); document.querySelector('#searchInput')?.focus(); }
  if ((event.metaKey || event.ctrlKey) && event.key === 'Enter' && event.target.id === 'replyText') { event.preventDefault(); sendReply(); }
  if (event.key === 'Escape' && event.target.id === 'searchInput') { state.query = ''; render(); }
});

refresh();
setInterval(() => { if (!document.hidden && !state.sending) refresh(); }, 15000);
