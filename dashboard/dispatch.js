// --- Project dropdown in dispatch/job modals ---

async function populateProjectDropdown(selectId) {
  if (cachedProjects.length === 0) {
    try {
      var data = await fetchJSON('/api/projects?status=active');
      cachedProjects = data.projects || [];
    } catch(e) {}
  }
  var sel = document.getElementById(selectId);
  var current = sel.value;
  sel.innerHTML = '<option value="">— none —</option>';
  cachedProjects.filter(function(p) { return p.status === 'active'; }).forEach(function(p) {
    var opt = document.createElement('option');
    opt.value = p.id;
    opt.textContent = p.name + (p.workdir ? ' (' + p.workdir + ')' : '');
    opt.dataset.workdir = p.workdir || '';
    sel.appendChild(opt);
  });
  if (current) sel.value = current;
}

function onDispatchProjectChange() {
  var sel = document.getElementById('df-project');
  var opt = sel.options[sel.selectedIndex];
  if (opt && opt.dataset && opt.dataset.workdir) {
    document.getElementById('df-workdir').value = opt.dataset.workdir;
  }
}

function onJobProjectChange() {
  var sel = document.getElementById('jf-project');
  var opt = sel.options[sel.selectedIndex];
  if (opt && opt.dataset && opt.dataset.workdir) {
    document.getElementById('jf-workdir').value = opt.dataset.workdir;
  }
}

var TAB_LIST = ['dashboard','chat','operations','team-builder','store','docs','settings','war-room'];

// Backward-compat mapping: old tab names → new tab + sub-tab
var TAB_COMPAT = {
  workflows:    {tab:'operations', sub:'workflows'},
  integrations: {tab:'settings',   sub:'integrations'},
  agents:       {tab:'operations', sub:'agents'},
  workspace:    {tab:'operations', sub:'tasks'},
  kanban:       {tab:'operations', sub:'tasks'},
  sessions:     {tab:'chat',      sub:null}
};

function switchTab(tab) {
  // Handle old tab names via compat map
  if (TAB_COMPAT[tab]) {
    var m = TAB_COMPAT[tab];
    switchTab(m.tab);
    if (m.sub) switchSubTab(m.tab, m.sub);
    return;
  }
  currentTab = tab;
  TAB_LIST.forEach(function(t) {
    var content = document.getElementById(t + '-content');
    if (content) content.style.display = tab === t ? '' : 'none';
  });
  // Sidebar highlight handled by updateSidebarHighlight
  if (tab === 'chat') {
    refreshChatSidebar();
    populateChatRoleFilter();
  }
  if (tab === 'operations') {
    var activeSub = getActiveSubTab('operations');
    refreshOperationsSubTab(activeSub);
  }
  if (tab === 'settings') {
    refreshSettings();
    var activeSub = getActiveSubTab('settings');
    refreshSettingsSubTab(activeSub);
  }
  if (tab === 'team-builder') {
    refreshTeamBuilder();
  }
  if (tab === 'store') {
    refreshStore();
  }
  if (tab === 'docs') {
    refreshDocs();
  }
  if (tab === 'dashboard') {
    applyDashView();
    loadDashViewData();
  }
  if (tab === 'war-room') {
    if (typeof loadWarRoom === 'function') loadWarRoom();
  }
  updateSidebarHighlight();
  closeSidebar();
}

function switchSubTab(parentTab, subTab) {
  var container = document.getElementById(parentTab + '-content');
  if (!container) return;
  // Update sub-tab nav buttons (hidden but kept for state tracking)
  var nav = container.querySelector('.sub-tab-nav');
  if (nav) {
    nav.querySelectorAll('button').forEach(function(btn) {
      btn.classList.toggle('active', btn.getAttribute('data-sub') === subTab);
    });
  }
  // Hide all sub panels, show selected
  container.querySelectorAll('[id^="' + parentTab + '-sub-"]').forEach(function(el) {
    el.style.display = 'none';
  });
  var target = document.getElementById(parentTab + '-sub-' + subTab);
  if (target) target.style.display = '';
  // Refresh data
  if (parentTab === 'operations') refreshOperationsSubTab(subTab);
  if (parentTab === 'settings') refreshSettingsSubTab(subTab);
  updateSidebarHighlight();
  closeSidebar();
}

function getActiveSubTab(parentTab) {
  var container = document.getElementById(parentTab + '-content');
  if (!container) return '';
  var nav = container.querySelector('.sub-tab-nav');
  if (!nav) return '';
  var active = nav.querySelector('button.active');
  return active ? active.getAttribute('data-sub') : '';
}

function refreshOperationsSubTab(sub) {
  if (sub === 'agents') refreshAgents();
  if (sub === 'workflows') { refreshWorkflowRuns(); loadWorkflowDefs(); }
  if (sub === 'tasks') refreshBoard();
  if (sub === 'capabilities') refreshCapabilities();
  if (sub === 'files') loadMemoryBrowser();
}

function refreshSettingsSubTab(sub) {
  if (sub === 'integrations') refreshIntegrations();
}

// --- Sidebar Navigation Helpers ---
function updateSidebarHighlight() {
  var sidebar = document.getElementById('sidebar-nav');
  if (!sidebar) return;
  sidebar.querySelectorAll('.nav-item').forEach(function(btn) { btn.classList.remove('active'); });

  var activeId = '';
  if (currentTab === 'dashboard') {
    activeId = 'tab-dashboard';
  } else if (currentTab === 'chat') {
    activeId = 'tab-chat';
  } else if (currentTab === 'operations') {
    var sub = getActiveSubTab('operations');
    activeId = 'tab-operations-' + (sub || 'agents');
  } else if (currentTab === 'settings') {
    var sub = getActiveSubTab('settings');
    activeId = 'tab-settings-' + (sub || 'general');
  } else if (currentTab === 'war-room') {
    activeId = 'tab-war-room';
  }
  var el = document.getElementById(activeId);
  if (el) el.classList.add('active');
}

function toggleSidebar() {
  document.getElementById('sidebar-nav').classList.toggle('open');
  document.getElementById('sidebar-overlay').classList.toggle('open');
}
function closeSidebar() {
  document.getElementById('sidebar-nav').classList.remove('open');
  document.getElementById('sidebar-overlay').classList.remove('open');
}

function populateChatRoleFilter() {
  var select = document.getElementById('chat-role-filter');
  if (select.options.length > 1) return;
  try {
    fetch('/roles').then(function(r) { return r.json(); }).then(function(roles) {
      (Array.isArray(roles) ? roles : []).forEach(function(r) {
        var opt = document.createElement('option');
        opt.value = r.name; opt.textContent = r.name;
        select.appendChild(opt);
      });
    });
  } catch(e) {}
}

var chatRoleColors = { '\u7460\u7483': 'var(--accent)', '\u7fe1\u7fe0': 'var(--accent2)', '\u9ed2\u66dc': 'var(--red)', '\u7425\u73c0': 'var(--yellow)' };
function chatRoleColor(role) { return chatRoleColors[role] || 'var(--green)'; }

async function refreshChatSidebar() {
  try {
    var roleFilter = document.getElementById('chat-role-filter').value;
    var url = '/sessions?limit=50';
    if (roleFilter) url += '&role=' + encodeURIComponent(roleFilter);

    // Fetch session list and system log in parallel.
    var results = await Promise.all([
      fetch(url).then(function(r) { return r.json(); }).catch(function() { return { sessions: [] }; }),
      fetch('/sessions/system:logs').then(function(r) { return r.json(); }).catch(function() { return null; }),
    ]);
    var sessions = results[0].sessions || [];
    var sysData = results[1];
    var sysSession = sysData && sysData.session ? sysData.session : null;

    var list = document.getElementById('chat-sidebar-list');
    var html = '';

    // Pin system log at the top.
    if (sysSession) {
      var isActiveSys = !taskViewId && sysSession.id === chatSessionId;
      html += '<div class="chat-session-item system-log' + (isActiveSys ? ' active' : '') + '" onclick="openChatSession(\'system:logs\')">' +
        '<div class="chat-session-role"><span class="dot dot-gray"></span>System Log</div>' +
        '<div class="chat-session-title">All dispatch outputs</div>' +
        '<div class="chat-session-meta">' + (sysSession.messageCount || 0) + ' entries &middot; ' + costFmt(sysSession.totalCost || 0) + '</div>' +
      '</div>';
      if (sessions.length > 0) {
        html += '<div class="chat-session-divider">Sessions</div>';
      }
    }

    if (sessions.length === 0 && !sysSession) {
      list.innerHTML = '<div style="text-align:center;color:var(--muted);padding:20px;font-size:13px">No sessions yet</div>';
      return;
    }

    html += sessions.map(function(s) {
      var isActive = !taskViewId && s.id === chatSessionId;
      var dotCls = s.status === 'active' ? 'dot-green' : 'dot-gray';
      var title = (s.title || '(untitled)').substring(0, 50);
      var color = chatRoleColor(s.agent);
      return '<div class="chat-session-item' + (isActive ? ' active' : '') + '" onclick="openChatSession(\'' + esc(s.id) + '\')">' +
        '<div class="chat-session-role" style="color:' + color + '"><span class="dot ' + dotCls + '"></span>' + esc(s.agent || 'unknown') + '</div>' +
        '<div class="chat-session-title">' + esc(title) + '</div>' +
        '<div class="chat-session-meta">' + (s.messageCount || 0) + ' msgs &middot; ' + costFmt(s.totalCost || 0) + ' &middot; ' + dateTimeStr(s.updatedAt) + '</div>' +
      '</div>';
    }).join('');

    list.innerHTML = html;
  } catch(e) { console.error('refreshChatSidebar:', e); }
}

function renderLiveTaskSection() {
  var ids = Object.keys(liveTaskItems);
  var section = document.getElementById('live-tasks-section');
  var list = document.getElementById('live-tasks-list');
  if (!section || !list) return;
  if (ids.length === 0) {
    section.style.display = 'none';
    return;
  }
  section.style.display = '';
  list.innerHTML = ids.map(function(id) {
    var t = liveTaskItems[id];
    var isActive = id === taskViewId;
    var color = chatRoleColor(t.role);
    return '<div class="live-task-item' + (isActive ? ' active' : '') + '" onclick="openTaskView(\'' + esc(id) + '\')">' +
      '<div class="chat-session-role" style="color:' + color + '"><span class="dot dot-green"></span>' + esc(t.role) + '</div>' +
      '<div class="chat-session-title">' + esc((t.name || id).substring(0, 50)) + '</div>' +
    '</div>';
  }).join('');
}

function addLiveTaskItem(taskId, data) {
  liveTaskItems[taskId] = { name: data.name || taskId, role: data.role || 'Agent' };
  renderLiveTaskSection();
}

function removeLiveTaskItem(taskId) {
  delete liveTaskItems[taskId];
  renderLiveTaskSection();
  // If currently viewing this task, show completion marker
  if (taskViewId === taskId) {
    var container = document.getElementById('chat-messages');
    if (container) {
      container.insertAdjacentHTML('beforeend', renderChatBubble({
        role: 'system', content: 'Task finished.', createdAt: new Date().toISOString()
      }));
      container.scrollTop = container.scrollHeight;
    }
  }
}

function disconnectTaskView() {
  if (taskViewSSE) { taskViewSSE.close(); taskViewSSE = null; }
  window._taskViewStream = null;
  taskViewId = null;
}

function _finalizeTaskStreamBubble(data) {
  var stream = window._taskViewStream;
  if (!stream) return;
  var el = document.getElementById(stream.id);
  if (el) {
    el.classList.remove('chat-bubble-streaming');
    var contentEl = document.getElementById(stream.id + '-content');
    if (contentEl && stream.text) contentEl.innerHTML = renderMarkdown(stream.text);
    if (!stream.text) el.remove();
    else if (data && (data.costUsd > 0 || data.durationMs)) {
      var metaParts = [];
      if (data.costUsd > 0) metaParts.push(costFmt(data.costUsd));
      if (data.durationMs) metaParts.push(formatDuration(data.durationMs));
      if (metaParts.length > 0) {
        el.insertAdjacentHTML('beforeend',
          '<div class="chat-bubble-meta">' + metaParts.map(function(p){ return '<span>' + esc(p) + '</span>'; }).join('') + '</div>');
      }
    }
  }
  window._taskViewStream = null;
}

function _startTaskStreamBubble(taskId, role, container) {
  var streamId = 'tv-' + taskId + '-' + Date.now();
  container.insertAdjacentHTML('beforeend',
    '<div id="' + streamId + '" class="chat-bubble chat-bubble-agent chat-bubble-streaming">' +
    '<div class="chat-bubble-label" style="color:' + chatRoleColor(role) + '">' + esc(role) + '</div>' +
    '<div id="' + streamId + '-content" class="md-rendered"></div>' +
    '</div>');
  window._taskViewStream = { id: streamId, text: '' };
}

function openTaskView(taskId) {
  disconnectChatSSE();
  disconnectTaskView();
  taskViewId = taskId;
  var info = liveTaskItems[taskId] || { name: taskId, role: 'Agent' };

  document.getElementById('chat-empty').style.display = 'none';
  document.getElementById('chat-messages').style.display = 'flex';
  document.getElementById('chat-input-area').style.display = 'none';
  document.getElementById('chat-header').style.display = '';
  document.getElementById('chat-back-btn').style.display = '';
  document.getElementById('chat-archive-btn').style.display = 'none';
  document.getElementById('chat-header-role').textContent = info.role;
  document.getElementById('chat-header-role').style.color = chatRoleColor(info.role);
  document.getElementById('chat-header-meta').textContent = info.name;

  var container = document.getElementById('chat-messages');
  container.innerHTML = renderChatBubble({
    role: 'system', content: 'Task: ' + info.name, createdAt: new Date().toISOString()
  });

  renderLiveTaskSection();
  refreshChatSidebar();

  _startTaskStreamBubble(taskId, info.role, container);
  container.scrollTop = container.scrollHeight;

  var es = new EventSource('/dispatch/' + encodeURIComponent(taskId) + '/stream');
  taskViewSSE = es;

  es.addEventListener('output_chunk', function(e) {
    try {
      var d = JSON.parse(e.data);
      var chunk = (d.data && d.data.chunk) || '';
      if (!chunk || !window._taskViewStream) return;
      window._taskViewStream.text += chunk;
      var contentEl = document.getElementById(window._taskViewStream.id + '-content');
      if (contentEl) contentEl.innerHTML = renderMarkdown(window._taskViewStream.text);
      container.scrollTop = container.scrollHeight;
    } catch(err) {}
  });

  es.addEventListener('tool_call', function(e) {
    try {
      var d = JSON.parse(e.data);
      var name = (d.data && (d.data.name || d.data.id)) || '(tool)';
      _finalizeTaskStreamBubble(null);
      container.insertAdjacentHTML('beforeend', renderChatBubble({
        role: 'system', content: '\uD83D\uDD27 ' + name, createdAt: new Date().toISOString()
      }));
      _startTaskStreamBubble(taskId, info.role, container);
      container.scrollTop = container.scrollHeight;
    } catch(err) {}
  });

  es.addEventListener('completed', function(e) {
    try {
      var d = JSON.parse(e.data);
      _finalizeTaskStreamBubble(d.data);
    } catch(err) { _finalizeTaskStreamBubble(null); }
    taskViewSSE = null;
    container.scrollTop = container.scrollHeight;
  });

  es.addEventListener('error', function(e) {
    if (e.data) {
      try {
        var d = JSON.parse(e.data);
        _finalizeTaskStreamBubble(null);
        container.insertAdjacentHTML('beforeend', renderChatBubble({
          role: 'system', content: 'Error: ' + ((d.data && d.data.error) || 'unknown'), createdAt: new Date().toISOString()
        }));
        container.scrollTop = container.scrollHeight;
      } catch(err) {}
    }
    taskViewSSE = null;
  });

  es.onerror = function() { taskViewSSE = null; };
}

function closeTaskView() {
  disconnectTaskView();
  renderLiveTaskSection();
  document.getElementById('chat-back-btn').style.display = 'none';
  document.getElementById('chat-archive-btn').style.display = '';
  if (chatSessionId) {
    openChatSession(chatSessionId);
  } else {
    document.getElementById('chat-header').style.display = 'none';
    document.getElementById('chat-messages').style.display = 'none';
    document.getElementById('chat-empty').style.display = '';
    document.getElementById('chat-input-area').style.display = 'none';
  }
}

async function openChatSession(sessionId) {
  disconnectChatSSE();
  disconnectTaskView();
  chatSessionId = sessionId;

  document.getElementById('chat-empty').style.display = 'none';
  document.getElementById('chat-messages').style.display = 'flex';
  document.getElementById('chat-input-area').style.display = '';
  document.getElementById('chat-header').style.display = '';
  document.getElementById('chat-back-btn').style.display = 'none';
  document.getElementById('chat-archive-btn').style.display = '';

  refreshChatSidebar();

  try {
    var data = await fetch('/sessions/' + encodeURIComponent(sessionId)).then(function(r) { return r.json(); });
    if (!data || !data.session) return;

    var s = data.session;
    chatSessionRole = s.agent || 'Agent';
    document.getElementById('chat-header-role').textContent = chatSessionRole;
    document.getElementById('chat-header-role').style.color = chatRoleColor(chatSessionRole);
    document.getElementById('chat-header-meta').textContent =
      (s.messageCount || 0) + ' msgs \u00b7 ' + costFmt(s.totalCost || 0) + ' \u00b7 ' + (s.status || '');

    renderChatMessages(data.messages || []);
    connectChatSSE(sessionId);
    document.getElementById('chat-input').focus();
  } catch(e) { console.error('openChatSession:', e); }
}

function renderChatMessages(messages) {
  var container = document.getElementById('chat-messages');
  container.innerHTML = messages.map(function(m) { return renderChatBubble(m); }).join('');
  requestAnimationFrame(function() { container.scrollTop = container.scrollHeight; });
}

function renderChatBubble(m) {
  var isUser = m.role === 'user';
  var isSys = m.role === 'system';
  var bubbleCls = isUser ? 'chat-bubble-user' : isSys ? 'chat-bubble-system' : 'chat-bubble-agent';
  var label = isUser ? 'You' : isSys ? 'System' : chatSessionRole;
  var labelColor = isUser ? 'var(--accent)' : isSys ? 'var(--muted)' : chatRoleColor(chatSessionRole);
  var contentHTML = isUser
    ? '<div style="white-space:pre-wrap;word-break:break-word">' + esc(m.content || '') + '</div>'
    : '<div class="md-rendered">' + renderMarkdown(m.content || '') + '</div>';
  var metaParts = [];
  if (m.createdAt) metaParts.push(dateTimeStr(m.createdAt));
  if (m.costUsd > 0) metaParts.push(costFmt(m.costUsd));
  if (m.model) metaParts.push(m.model);
  if (m.tokensIn > 0 || m.tokensOut > 0) metaParts.push((m.tokensIn||0) + '/' + (m.tokensOut||0) + ' tok');
  var metaHTML = metaParts.length > 0
    ? '<div class="chat-bubble-meta">' + metaParts.map(function(p){ return '<span>' + esc(p) + '</span>'; }).join('') + '</div>'
    : '';
  return '<div class="chat-bubble ' + bubbleCls + '">' +
    '<div class="chat-bubble-label" style="color:' + labelColor + '">' + esc(label) + '</div>' +
    contentHTML + metaHTML + '</div>';
}

// --- Cost Estimate Helper ---
async function getChatEstimate(prompt, role) {
  try {
    var task = { prompt: prompt };
    if (role) task.agent = role;
    var resp = await fetch('/dispatch/estimate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify([task])
    });
    return await resp.json();
  } catch(e) { return null; }
}

// --- Send Message with SSE Streaming ---
async function sendChatMessage() {
  if (chatSending || !chatSessionId) return;
  var input = document.getElementById('chat-input');
  var message = input.value.trim();
  if (!message) return;

  input.value = '';
  autoResizeInput(input);
  chatSending = true;
  document.getElementById('chat-send-btn').disabled = true;
  document.getElementById('chat-typing').classList.add('visible');

  var container = document.getElementById('chat-messages');
  // Prune old messages to prevent unbounded DOM growth.
  while (container.children.length > 200) container.removeChild(container.firstChild);
  container.insertAdjacentHTML('beforeend', renderChatBubble({
    role: 'user', content: message, createdAt: new Date().toISOString()
  }));
  container.scrollTop = container.scrollHeight;

  // Show cost estimate badge.
  var costEst = await getChatEstimate(message, chatSessionRole);
  if (costEst && costEst.totalEstimatedCostUsd > 0) {
    var breakdown = costEst.tasks && costEst.tasks[0] ? costEst.tasks[0].breakdown : '';
    container.insertAdjacentHTML('beforeend',
      '<div class="cost-estimate-badge" title="' + esc(breakdown) + '">~$' +
      costEst.totalEstimatedCostUsd.toFixed(4) + '</div>');
    container.scrollTop = container.scrollHeight;
  }

  var streamId = 'stream-' + Date.now();
  container.insertAdjacentHTML('beforeend',
    '<div id="' + streamId + '" class="chat-bubble chat-bubble-agent chat-bubble-streaming">' +
    '<div class="chat-bubble-label" style="color:' + chatRoleColor(chatSessionRole) + '">' + esc(chatSessionRole) + '</div>' +
    '<div id="' + streamId + '-content" class="md-rendered"></div>' +
    '</div>');
  container.scrollTop = container.scrollHeight;

  window._chatStream = { id: streamId, text: '' };

  try {
    var resp = await fetch('/sessions/' + encodeURIComponent(chatSessionId) + '/message', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ prompt: message, async: true })
    }).then(function(r) { return r.json(); });

    if (!resp || !resp.taskId) throw new Error(resp.error || 'No task ID');
    window._chatStream.taskId = resp.taskId;
  } catch(e) {
    var el = document.getElementById(streamId);
    if (el) el.remove();
    container.insertAdjacentHTML('beforeend', renderChatBubble({
      role: 'system', content: '[error] ' + e.message, createdAt: new Date().toISOString()
    }));
    container.scrollTop = container.scrollHeight;
    unlockChatInput();
  }
}

function unlockChatInput() {
  chatSending = false;
  document.getElementById('chat-send-btn').disabled = false;
  document.getElementById('chat-typing').classList.remove('visible');
  document.querySelectorAll('.chat-bubble-streaming').forEach(function(el) {
    el.classList.remove('chat-bubble-streaming');
  });
}

// --- Chat SSE ---
function connectChatSSE(sessionId) {
  disconnectChatSSE();
  var url = '/sessions/' + encodeURIComponent(sessionId) + '/stream';
  chatSSE = new EventSource(url);

  chatSSE.addEventListener('output_chunk', function(e) {
    try {
      var d = JSON.parse(e.data);
      var chunk = (d.data && d.data.chunk) || '';
      if (!chunk || !window._chatStream) return;
      window._chatStream.text += chunk;
      var contentEl = document.getElementById(window._chatStream.id + '-content');
      if (contentEl) contentEl.textContent = window._chatStream.text;
      var container = document.getElementById('chat-messages');
      if (container) container.scrollTop = container.scrollHeight;
    } catch(err) {}
  });

  chatSSE.addEventListener('completed', function(e) {
    try {
      var d = JSON.parse(e.data);
      finalizeChatBubble(d.data);
    } catch(err) {}
    unlockChatInput();
    refreshChatSidebar();
    setTimeout(function() { if (chatSessionId) connectChatSSE(chatSessionId); }, 100);
  });

  chatSSE.addEventListener('error', function(e) {
    try {
      if (e.data) {
        var d = JSON.parse(e.data);
        finalizeChatBubble(d.data, true);
      }
    } catch(err) {}
    if (chatSending) unlockChatInput();
  });

  chatSSE.onerror = function() {
    if (chatSending) {
      if (window._chatStream && !document.getElementById(window._chatStream.id)) {
        unlockChatInput();
      }
    }
  };
}

function disconnectChatSSE() {
  if (chatSSE) { chatSSE.close(); chatSSE = null; }
  window._chatStream = null;
}

function finalizeChatBubble(data, isError) {
  var stream = window._chatStream;
  if (!stream) return;
  var el = document.getElementById(stream.id);
  if (!el) return;

  el.classList.remove('chat-bubble-streaming');
  var contentEl = document.getElementById(stream.id + '-content');
  if (contentEl && stream.text) {
    contentEl.innerHTML = renderMarkdown(stream.text);
  }

  var metaParts = [];
  if (data) {
    if (data.costUsd > 0) metaParts.push(costFmt(data.costUsd));
    if (data.durationMs) metaParts.push(formatDuration(data.durationMs));
    if (data.tokensIn || data.tokensOut) metaParts.push((data.tokensIn||0) + '/' + (data.tokensOut||0) + ' tok');
    if (isError && data.error) metaParts.push('Error: ' + data.error);
  }
  if (metaParts.length > 0) {
    el.insertAdjacentHTML('beforeend',
      '<div class="chat-bubble-meta">' + metaParts.map(function(p){ return '<span>' + esc(p) + '</span>'; }).join('') + '</div>');
  }
  window._chatStream = null;
  var container = document.getElementById('chat-messages');
  if (container) container.scrollTop = container.scrollHeight;
}

// --- Chat Input Handling ---
function chatKeydown(e) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); sendChatMessage(); }
}

function autoResizeInput(el) {
  el.style.height = 'auto';
  el.style.height = Math.min(el.scrollHeight, 120) + 'px';
}

// --- New Chat Modal ---
function openNewChatModal() {
  var select = document.getElementById('new-chat-role');
  if (select.options.length === 0) {
    fetch('/roles').then(function(r) { return r.json(); }).then(function(roles) {
      (Array.isArray(roles) ? roles : []).forEach(function(r) {
        var opt = document.createElement('option');
        opt.value = r.name; opt.textContent = r.name;
        select.appendChild(opt);
      });
    });
  }
  document.getElementById('new-chat-modal').classList.add('open');
}
function closeNewChatModal() { document.getElementById('new-chat-modal').classList.remove('open'); }

async function createNewChat() {
  var role = document.getElementById('new-chat-role').value;
  if (!role) { toast('Select a role'); return; }
  try {
    var sess = await fetch('/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent: role })
    }).then(function(r) {
      if (!r.ok) return r.json().then(function(e) { throw new Error(e.error || r.statusText); });
      return r.json();
    });
    if (sess && sess.id) {
      closeNewChatModal();
      await refreshChatSidebar();
      openChatSession(sess.id);
      toast('New chat with ' + role);
    }
  } catch(e) { toast('Failed: ' + e.message); }
}

// --- Archive Chat ---
function archiveChat() {
  if (!chatSessionId || !confirm('Archive this session?')) return;
  fetch('/sessions/' + encodeURIComponent(chatSessionId), { method: 'DELETE' })
    .then(function() {
      toast('Session archived');
      disconnectChatSSE();
      chatSessionId = null;
      chatSessionRole = '';
      document.getElementById('chat-empty').style.display = '';
      document.getElementById('chat-messages').style.display = 'none';
      document.getElementById('chat-input-area').style.display = 'none';
      document.getElementById('chat-header').style.display = 'none';
      refreshChatSidebar();
    })
    .catch(function(e) { toast('Archive failed: ' + e.message); });
}

