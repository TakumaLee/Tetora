const API = '';
let pollTimer;
let cachedJobs = [];
let cachedRoles = [];
let cachedArchetypes = [];
let historyPage = 1;
let historyTotal = 0;
const historyLimit = 20;

// ====== Dashboard View System ======
const DASH_VIEWS = ['selector', 'default', 'quest', 'calendar', 'stats'];
let currentDashView = localStorage.getItem('tetora-dash-view') || 'selector';
let calendarYear, calendarMonth; // set in calendarToday()
let statsReportDays = 7;

function selectDashView(view) {
  currentDashView = view;
  localStorage.setItem('tetora-dash-view', view);
  applyDashView();
  loadDashViewData();
}

function showViewSelector() {
  currentDashView = 'selector';
  localStorage.removeItem('tetora-dash-view');
  applyDashView();
}

function applyDashView() {
  DASH_VIEWS.forEach(function(v) {
    var el = document.getElementById('dash-view-' + v);
    if (el) el.classList.toggle('active', v === currentDashView);
  });
}

function loadDashViewData() {
  if (currentDashView === 'default') {
    refresh();
  } else if (currentDashView === 'quest') {
    refreshQuestLog();
  } else if (currentDashView === 'calendar') {
    if (!calendarYear) calendarToday();
    else refreshCalendarView();
  } else if (currentDashView === 'stats') {
    refreshStatsReport();
  }
}

// --- Dashboard SSE Activity Feed ---
const ACTIVITY_MAX = 50;
let activityItems = [];
let dashboardSSE = null;
let sseConnected = false;

function connectDashboardSSE() {
  if (dashboardSSE) { dashboardSSE.close(); dashboardSSE = null; }
  const es = new EventSource(API + '/events/dashboard');
  dashboardSSE = es;

  es.onopen = function() {
    sseConnected = true;
    updateSSEBadge();
    // Slow down polling when SSE is live.
    clearInterval(pollTimer);
    pollTimer = setInterval(refresh, 15000);
  };

  es.onerror = function() {
    sseConnected = false;
    updateSSEBadge();
    // Speed up polling on disconnect.
    clearInterval(pollTimer);
    pollTimer = setInterval(refresh, 5000);
  };

  var eventTypes = ['task_received', 'task_routing', 'started', 'completed', 'error', 'task_queued', 'tool_call', 'tool_result', 'output_chunk', 'discord_processing', 'discord_replying', 'agent_state', 'board_updated', 'worker_update', 'hook_event', 'plan_review', 'human_gate_waiting', 'human_gate_responded'];
  eventTypes.forEach(function(evType) {
    es.addEventListener(evType, function(e) {
      try {
        var data = JSON.parse(e.data);
        handleActivityEvent(evType, data);
      } catch(err) {}
    });
  });
}

var lastOutputChunkTime = 0;

function handleActivityEvent(type, ev) {
  var detail = '';
  var source = '';
  var badge = type;
  var data = ev.data || {};

  switch(type) {
    case 'task_received':
      badge = 'received';
      source = data.source || '';
      detail = truncateText(data.prompt || '', 120);
      if (data.author) detail = '<b>' + esc(data.author) + '</b>: ' + esc(truncateText(data.prompt || '', 100));
      else detail = esc(detail);
      break;
    case 'task_routing':
      badge = 'routing';
      source = data.source || '';
      detail = 'Role: <b>' + esc(data.role || '?') + '</b> via ' + esc(data.method || '?');
      if (data.confidence) detail += ' <span class="muted">(' + esc(data.confidence) + ')</span>';
      break;
    case 'started':
      badge = 'started';
      detail = '<b>' + esc(data.name || ev.taskId || '') + '</b>';
      if (data.role) detail += ' &middot; ' + esc(data.role);
      if (data.model) detail += ' <span class="muted">[' + esc(data.model) + ']</span>';
      if (ev.taskId) addLiveTaskItem(ev.taskId, data);
      break;
    case 'completed':
      badge = 'completed';
      detail = esc(data.status || 'success');
      if (data.durationMs) detail += ' in ' + formatDuration(data.durationMs);
      if (data.costUsd) detail += ' &middot; $' + Number(data.costUsd).toFixed(4);
      if (ev.taskId) removeLiveTaskItem(ev.taskId);
      if (data.name || ev.taskId) addNotification('Completed: ' + (data.name || ev.taskId), 'success', {tab:'operations',sub:'tasks'});
      play8BitSound('complete');
      break;
    case 'error':
      badge = 'error';
      detail = esc(data.error || data.status || 'error');
      if (data.durationMs) detail += ' <span class="muted">(' + formatDuration(data.durationMs) + ')</span>';
      if (ev.taskId) removeLiveTaskItem(ev.taskId);
      addNotification('Error: ' + (data.error || data.name || 'task'), 'error', {tab:'operations',sub:'tasks'});
      play8BitSound('error');
      break;
    case 'task_queued':
      badge = 'queued';
      detail = esc(data.name || 'task') + ' queued';
      if (data.error) detail += ' <span class="muted">' + esc(truncateText(data.error, 80)) + '</span>';
      break;
    case 'tool_call':
      badge = 'tool';
      detail = esc(data.name || data.id || '(tool)');
      if (data.preview) detail += ' <span class="muted" style="font-size:11px">' + esc(data.preview) + '</span>';
      break;
    case 'tool_result':
      badge = 'tool';
      detail = esc(data.name || data.id || data.toolUseId || '(tool)') + ' <span class="muted">' + (data.duration || 0) + 'ms</span>';
      if (data.isError) detail += ' <span style="color:var(--red)">error</span>';
      break;
    case 'output_chunk':
      var now = Date.now();
      if (now - lastOutputChunkTime < 2000) return;
      lastOutputChunkTime = now;
      badge = 'output';
      var chunk = data.chunk || '';
      detail = esc(chunk.length > 80 ? chunk.substring(0, 80) + '...' : chunk);
      break;
    case 'discord_processing':
      badge = 'processing';
      source = 'discord';
      detail = '<b>' + esc(data.role || '?') + '</b> processing';
      if (data.author) detail += ' for ' + esc(data.author);
      break;
    case 'discord_replying':
      badge = 'replying';
      source = 'discord';
      detail = '<b>' + esc(data.role || '?') + '</b> replying';
      if (data.author) detail += ' to ' + esc(data.author);
      if (data.status && data.status !== 'success') detail += ' <span style="color:var(--red)">[' + esc(data.status) + ']</span>';
      break;
    case 'board_updated':
      refreshBoard();
      return;
    case 'worker_update':
      if (currentTab === 'dashboard') refreshWorkers();
      badge = 'worker';
      var wName = data.name || '';
      if (wName.startsWith('tetora-worker-')) wName = wName.substring(14);
      if (data.action === 'state_changed') detail = '<b>' + esc(wName) + '</b> → ' + esc(data.state || '');
      else if (data.action === 'registered') detail = '<b>' + esc(wName) + '</b> registered';
      else if (data.action === 'unregistered') detail = '<b>' + esc(wName) + '</b> stopped';
      else detail = esc(wName) + ' ' + esc(data.action || '');
      break;
    case 'hook_event':
      badge = 'hook';
      var hookType = data.hookType || 'event';
      var toolName = data.toolName || '';
      if (hookType === 'notification') {
        detail = '<b>Notification</b>: ' + esc(data.message || '');
        addNotification(data.message || 'Notification', data.level === 'error' ? 'error' : 'info');
      } else if (toolName) {
        detail = '<b>' + esc(hookType) + '</b>: ' + esc(toolName);
      } else {
        detail = '<b>' + esc(hookType) + '</b>';
      }
      addHookEvent(data);
      break;
    case 'plan_review':
      badge = 'plan';
      if (data.readyForReview) {
        detail = 'Plan ready for review';
        if (data.sessionId) detail += ' <span class="muted">[' + esc(data.sessionId.substring(0, 8)) + ']</span>';
        addNotification('Plan ready for review', 'info', {tab:'dashboard'});
        play8BitSound('notification');
        refreshPlanReviews();
      } else if (data.action === 'approve') {
        detail = 'Plan <b>approved</b>';
        if (data.reviewer) detail += ' by ' + esc(data.reviewer);
        refreshPlanReviews();
      } else if (data.action === 'reject') {
        detail = 'Plan <b>rejected</b>';
        if (data.reviewer) detail += ' by ' + esc(data.reviewer);
        refreshPlanReviews();
      } else {
        detail = 'Plan update';
      }
      break;
    case 'human_gate_waiting':
      badge = 'gate';
      detail = 'Gate waiting: <b>' + esc(data.stepId || '') + '</b>';
      if (data.subtype) detail += ' <span class="muted">[' + esc(data.subtype) + ']</span>';
      if (typeof refreshHumanGates === 'function') refreshHumanGates();
      addNotification('Gate waiting: ' + (data.stepId || 'step'), 'info', {tab:'dashboard'});
      break;
    case 'human_gate_responded':
      badge = 'gate';
      detail = 'Gate responded: <b>' + esc(data.stepId || '') + '</b>';
      if (data.decision) detail += ' → ' + esc(data.decision);
      if (typeof refreshHumanGates === 'function') refreshHumanGates();
      break;
    default:
      detail = type;
  }

  var now = new Date();
  var timeStr = now.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  activityItems.unshift({ badge: badge, detail: detail, time: timeStr, taskId: ev.taskId || '' });
  if (activityItems.length > ACTIVITY_MAX) activityItems.length = ACTIVITY_MAX;
  renderActivityFeed();
}

function renderActivityFeed() {
  var el = document.getElementById('activity-list');
  if (activityItems.length === 0) {
    el.innerHTML = '<div style="padding:20px;text-align:center;color:var(--muted);font-size:13px">Waiting for events...</div>';
    return;
  }
  var html = activityItems.map(function(item) {
    return '<div class="activity-item">' +
      '<span class="activity-type ' + item.badge + '">' + item.badge + '</span>' +
      '<span class="activity-detail">' + item.detail + '</span>' +
      '<span class="activity-time">' + item.time + '</span>' +
      '</div>';
  }).join('');
  el.innerHTML = html;
}

function updateSSEBadge() {
  var el = document.getElementById('sse-status');
  if (sseConnected) {
    el.innerHTML = '<span class="sse-badge live"><span class="dot-live"></span>Live</span>';
  } else {
    el.innerHTML = '<span class="sse-badge disconnected">Disconnected</span>';
  }
}

function truncateText(s, n) {
  if (!s) return '';
  return s.length > n ? s.substring(0, n) + '...' : s;
}

function formatDuration(ms) {
  if (ms < 1000) return ms + 'ms';
  if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
  var s = Math.floor(ms / 1000);
  var h = Math.floor(s / 3600);
  var m = Math.floor((s % 3600) / 60);
  var sec = s % 60;
  if (h > 0) return h + 'h ' + m + 'm ' + sec + 's';
  return m + 'm ' + sec + 's';
}

// --- End Dashboard SSE ---

async function fetchJSON(url, opts) {
  const resp = await fetch(API + url, opts);
  const data = await resp.json();
  if (!resp.ok) throw new Error(data.error || ('HTTP ' + resp.status));
  return data;
}

// fetchAPI is an alias for fetchJSON (returns parsed JSON, throws on error).
async function fetchAPI(url, opts) {
  return fetchJSON(url, opts);
}

function toast(msg) {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.classList.add('show');
  setTimeout(() => el.classList.remove('show'), 2500);
}

// ====== Alert Banner ======
var _dismissedAlerts = new Set();
var _currentAlerts = [];

function _renderAlerts() {
  var banner = document.getElementById('alert-banner');
  var html = _currentAlerts
    .filter(function(a) { return !_dismissedAlerts.has(a.id); })
    .map(function(a) {
      return '<div class="alert-item alert-' + a.level + '">' +
        '<span>' + esc(a.msg) + '</span>' +
        '<button class="alert-dismiss" onclick="_dismissAlert(\'' + esc(a.id) + '\')" title="Dismiss">&times;</button>' +
        '</div>';
    }).join('');
  banner.innerHTML = html;
}

function _dismissAlert(id) {
  _dismissedAlerts.add(id);
  _renderAlerts();
}

function checkAlerts(cost, doingTasks) {
  var alerts = [];
  var now = Date.now();
  var TIMEOUT_MS = 30 * 60 * 1000;

  // Cost quota alerts
  var dailyLimit = cost.dailyLimit || 0;
  var weeklyLimit = cost.weeklyLimit || 0;
  if (dailyLimit > 0) {
    var dp = (cost.today || 0) / dailyLimit;
    if (dp >= 1.0) {
      alerts.push({ id: 'daily-over', level: 'error', msg: 'Daily cost limit reached: ' + costFmt(cost.today) + ' / ' + costFmt(dailyLimit) });
    } else if (dp >= 0.8) {
      alerts.push({ id: 'daily-warn', level: 'warn', msg: 'Daily cost at ' + Math.round(dp * 100) + '%: ' + costFmt(cost.today) + ' / ' + costFmt(dailyLimit) });
    }
  }
  if (weeklyLimit > 0) {
    var wp = (cost.week || 0) / weeklyLimit;
    if (wp >= 1.0) {
      alerts.push({ id: 'weekly-over', level: 'error', msg: 'Weekly cost limit reached: ' + costFmt(cost.week) + ' / ' + costFmt(weeklyLimit) });
    } else if (wp >= 0.8) {
      alerts.push({ id: 'weekly-warn', level: 'warn', msg: 'Weekly cost at ' + Math.round(wp * 100) + '%: ' + costFmt(cost.week) + ' / ' + costFmt(weeklyLimit) });
    }
  }

  // Task timeout alerts
  if (Array.isArray(doingTasks)) {
    doingTasks.forEach(function(t) {
      if (t.updatedAt) {
        var elapsed = now - new Date(t.updatedAt).getTime();
        if (elapsed >= TIMEOUT_MS) {
          var mins = Math.floor(elapsed / 60000);
          alerts.push({
            id: 'task-timeout-' + t.id,
            level: 'warn',
            msg: 'Task stalled (' + mins + ' min): ' + (t.title || t.id)
          });
        }
      }
    });
  }

  // Remove stale dismissals (alert no longer active)
  var activeIds = new Set(alerts.map(function(a) { return a.id; }));
  _dismissedAlerts.forEach(function(id) {
    if (!activeIds.has(id)) _dismissedAlerts.delete(id);
  });

  _currentAlerts = alerts;
  _renderAlerts();
}

function timeStr(iso) {
  if (!iso || iso.startsWith('0001')) return '-';
  const d = new Date(iso);
  return d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
}

function dateTimeStr(iso) {
  if (!iso || iso.startsWith('0001')) return '-';
  const d = new Date(iso);
  const now = new Date();
  const isToday = d.toDateString() === now.toDateString();
  if (isToday) return d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  return d.toLocaleDateString('en-GB', { month: 'short', day: 'numeric' }) + ' ' +
    d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
}

function costFmt(v) {
  if (v === 0) return '$0.00';
  if (v < 0.01) return '$' + v.toFixed(4);
  return '$' + v.toFixed(2);
}

function statusBadge(s) {
  const cls = s === 'success' ? 'status-success' : s === 'error' ? 'status-error' : s === 'timeout' ? 'status-timeout' : 'status-cancelled';
  return `<span class="status-badge ${cls}">${esc(s)}</span>`;
}

async function refresh() {
  try {
    const [health, jobs, cost, tasks, roles, doingTasks] = await Promise.all([
      fetchJSON('/healthz'),
      fetchJSON('/cron').catch(() => []),
      fetchJSON('/stats/cost').catch(() => ({ today: 0, week: 0, month: 0 })),
      fetchJSON('/tasks').catch(() => null),
      fetchJSON('/roles').catch(() => []),
      fetchJSON('/api/tasks?status=doing').catch(() => []),
    ]);

    // Cache for search
    window._latestAgents = Array.isArray(roles) ? roles : [];

    // Connection badge
    const badge = document.getElementById('conn-badge');
    badge.textContent = 'Connected';
    badge.className = 'badge badge-ok';

    // Version
    document.getElementById('version').textContent = 'v' + (health.version || '?');

    // Updated time
    document.getElementById('updated').textContent = new Date().toLocaleTimeString('en-GB');

    // Stats (cost + system)
    const cron = health.cron || {};
    const dispatch = health.dispatch || {};
    const dailyLimit = cost.dailyLimit || 0;
    const weeklyLimit = cost.weeklyLimit || 0;
    const opsBarHTML = [
      { label: 'Jobs', value: `${cron.enabled || 0} / ${cron.jobs || 0}` },
      { label: 'Running', value: cron.running || 0, color: (cron.running || 0) > 0 ? 'var(--yellow)' : '' },
      { label: 'Daily Burn', value: costFmt(cost.today || 0), limit: dailyLimit, current: cost.today || 0 },
      { label: 'Agent Status', value: (dispatch.discord && dispatch.discord.length > 0) ? dispatch.discord.length + ' active' : (dispatch.status || 'idle'), color: (dispatch.discord && dispatch.discord.length > 0) ? 'var(--accent2)' : '' },
      { label: 'CLI Sessions', value: cliUsageData.count > 0 ? cliUsageData.count + ' ($' + cliUsageData.totalCost.toFixed(2) + ')' : '-' },
    ].map(s => {
      let bar = '';
      if (s.limit > 0) {
        const pct = Math.min((s.current / s.limit) * 100, 100);
        const barColor = pct >= 100 ? 'var(--red)' : pct >= 80 ? 'var(--yellow)' : 'var(--green)';
        bar = `<div style="margin-top:6px;height:4px;background:var(--border);border-radius:2px;overflow:hidden">
          <div style="height:100%;width:${pct}%;background:${barColor};border-radius:2px"></div>
        </div>
        <div style="font-size:10px;color:var(--muted);margin-top:2px">limit: ${costFmt(s.limit)}</div>`;
      }
      return `<div class="ops-stat"><div class="ops-stat-label">${s.label}</div><div class="ops-stat-value" ${s.color ? `style="color:${s.color}"` : ''}>${s.value}</div>${bar}</div>`;
    }).join('');
    document.getElementById('ops-bar').innerHTML = opsBarHTML;

    // Executive Summary + Period Deltas + Agent Scorecard
    loadExecSummary();
    loadPeriodDeltas();
    loadAgentScorecard();

    // Alert banner
    checkAlerts(cost, doingTasks);

    // Onboarding banner
    var ob = document.getElementById('onboarding-banner');
    if (ob && !localStorage.getItem('tetora-onboarded')) {
      var noAgents = !Array.isArray(roles) || roles.length === 0;
      var noTasks = !tasks || (Array.isArray(tasks) && tasks.length === 0);
      ob.style.display = (noAgents && noTasks) ? '' : 'none';
    }

    // Agent World — update sprite states + toggle visibility
    var dispSprites = dispatch.sprites || {};
    updateAgentWorldToggle(dispSprites);
    if (spriteEngine && agentWorldOpen) {
      spriteEngine.updateAgentStates(dispSprites);
    }

    // Agent Activity section (dispatch tasks + Discord activities)
    const dSec = document.getElementById('dispatch-section');
    const discordActs = dispatch.discord || [];
    const hasDispatch = dispatch.status === 'dispatching' && dispatch.tasks;
    const hasDiscord = discordActs.length > 0;
    if (hasDispatch || hasDiscord) {
      dSec.style.display = '';
      let dHTML = '';
      if (hasDispatch) {
        dHTML += dispatch.tasks.map(t => {
          const dot = t.status === 'running' ? 'dot-yellow' : t.status === 'success' ? 'dot-green' : 'dot-red';
          const info = t.elapsed || t.duration || '';
          const roleLabel = t.agent ? `<span style="color:var(--accent);font-size:12px;margin-right:4px">[${esc(t.agent)}]</span>` : '';
          return `<div class="dispatch-task"><span class="dot ${dot}"></span>${roleLabel}${esc(t.name)} <span style="color:var(--muted);font-size:12px">${info}</span></div>`;
        }).join('');
      }
      if (hasDiscord) {
        dHTML += discordActs.map(d => {
          const dot = d.phase === 'routing' ? 'dot-blue' : d.phase === 'processing' ? 'dot-yellow' : 'dot-green';
          const label = d.role ? d.role + ' (' + d.phase + ')' : d.phase;
          const meta = [d.author, d.elapsed].filter(Boolean).join(' · ');
          const liveBtn = d.phase === 'processing' && d.taskId
            ? ` <button class="btn-live" id="live-btn-${esc(d.taskId)}" onclick="toggleLive('${esc(d.taskId)}')">Live</button>`
            : '';
          return `<div class="dispatch-task" id="task-${esc(d.taskId)}"><span class="dot ${dot}"></span>${esc(label)} <span style="color:var(--muted);font-size:12px">${esc(meta)}</span>${liveBtn}
            <div class="live-output" id="live-${esc(d.taskId)}" style="display:none"></div></div>`;
        }).join('');
        cleanupLiveStreams();
      }
      document.getElementById('dispatch-tasks').innerHTML = dHTML;
    } else {
      dSec.style.display = 'none';
    }

    // Running tasks
    await refreshRunning();

    // Sessions (Mission Control) — skip heavy poll when on Chat tab
    window._cachedRoles = {};
    if (Array.isArray(roles)) roles.forEach(r => { window._cachedRoles[r.name] = r; });
    if (currentTab === 'dashboard' && currentDashView === 'default') {
      await refreshSessions();

      // Trend chart
      await refreshTrend();

      // History Trends
      await refreshHistoryTrends();

      // System Health
      refreshHealth();
    }

    // Cache jobs for edit modal
    cachedJobs = Array.isArray(jobs) ? jobs : [];

    // Jobs table
    if (Array.isArray(jobs)) {
      const enabled = jobs.filter(j => j.enabled).length;
      const running = jobs.filter(j => j.running).length;
      document.getElementById('cron-meta').textContent = `${enabled} enabled / ${running} running`;

      const tbody = document.getElementById('jobs-body');
      tbody.innerHTML = jobs.map(j => {
        const dotClass = j.running ? 'dot-yellow' : j.enabled ? 'dot-green' : 'dot-gray';
        const statusText = j.running ? 'Running' :
          j.errors > 0 ? `Err x${j.errors}` : j.enabled ? 'Ready' : 'Off';
        const statusColor = j.running ? 'var(--yellow)' :
          j.errors > 0 ? 'var(--red)' : j.enabled ? '' : 'var(--muted)';
        let progressBar = '';
        if (j.enabled && !j.running && j.lastRun && !j.lastRun.startsWith('0001') && j.nextRun) {
          const last = new Date(j.lastRun).getTime();
          const next = new Date(j.nextRun).getTime();
          const now = Date.now();
          const total = next - last;
          if (total > 0) {
            const pct = Math.min(100, Math.max(0, ((now - last) / total) * 100));
            const pColor = pct > 95 ? 'var(--red)' : pct > 80 ? 'var(--yellow)' : 'var(--green)';
            progressBar = `<tr><td colspan="11" style="padding:0 8px 4px"><div class="cron-progress"><div class="cron-progress-fill" style="width:${pct.toFixed(1)}%;background:${pColor}"></div></div></td></tr>`;
          }
        }
        return `<tr>
          <td>
            <label class="toggle">
              <input type="checkbox" ${j.enabled ? 'checked' : ''} onchange="toggleJob('${esc(j.id)}', this.checked)" ${j.running ? 'disabled' : ''}>
              <span class="slider"></span>
            </label>
          </td>
          <td class="job-name"><span class="dot ${dotClass}"></span>${esc(j.name)}</td>
          <td class="job-schedule">${esc(j.schedule)}</td>
          <td class="job-role">${esc(j.agent || '-')}</td>
          <td style="font-size:11px;color:var(--muted)">${formatChain(j)}</td>
          <td class="job-next">${j.lastRun && !j.lastRun.startsWith('0001') ?
            (j.lastErr ? '<span style="color:var(--red)" title="' + esc(j.lastErr) + '">! </span>' : '') + dateTimeStr(j.lastRun)
            : '-'}</td>
          <td style="font-size:12px;font-family:monospace">${j.lastCost > 0 ? costFmt(j.lastCost) : '-'}</td>
          <td style="font-size:12px;font-family:monospace">${j.avgCost > 0 ? costFmt(j.avgCost) : '-'}</td>
          <td class="job-next">${timeStr(j.nextRun)}</td>
          <td style="font-size:12px;color:${statusColor}">${statusText}</td>
          <td style="white-space:nowrap">
            <button class="btn btn-run" onclick="triggerJob('${esc(j.id)}')" ${j.running ? 'disabled' : ''}>Run</button>
            <button class="btn btn-edit" onclick="editJob('${esc(j.id)}')" ${j.running ? 'disabled' : ''}>Edit</button>
            <button class="btn btn-del" onclick="deleteJob('${esc(j.id)}')" ${j.running ? 'disabled' : ''}>Del</button>
          </td>
        </tr>` + progressBar;
      }).join('');
    }

    // Populate job filter dropdown
    if (Array.isArray(jobs) && jobs.length > 0) {
      const jobFilter = document.getElementById('history-job-filter');
      const currentVal = jobFilter.value;
      const uniqueJobs = [...new Set(jobs.map(j => j.id))];
      jobFilter.innerHTML = '<option value="">All jobs</option>' +
        uniqueJobs.map(id => {
          const j = jobs.find(x => x.id === id);
          return `<option value="${esc(id)}"${id === currentVal ? ' selected' : ''}>${esc(j ? j.name : id)}</option>`;
        }).join('');
    }

    // History is refreshed separately
    await refreshHistory();

    // Routing stats refreshed separately
    await refreshRouting();

    // Roles table
    if (Array.isArray(roles) && roles.length > 0) {
      document.getElementById('roles-section').style.display = '';
      document.getElementById('roles-meta').textContent = `${roles.length} roles`;
      const rbody = document.getElementById('roles-body');
      rbody.innerHTML = roles.map(r => `<tr>
        <td class="job-name">${esc(r.name)}</td>
        <td style="font-size:12px">${esc(r.model || 'default')}</td>
        <td style="font-size:12px">${esc(r.permissionMode || '-')}</td>
        <td style="font-size:12px;color:var(--muted)">${esc(r.soulFile || '-')}</td>
        <td style="font-size:12px">${esc(r.description || '-')}</td>
        <td style="white-space:nowrap">
          <button class="btn" onclick="viewSoul('${esc(r.name)}')">Show</button>
          <button class="btn btn-edit" onclick="editRole('${esc(r.name)}')">Edit</button>
          <button class="btn btn-del" onclick="deleteRole('${esc(r.name)}')">Del</button>
        </td>
      </tr>`).join('');
      cachedRoles = roles;
    } else {
      document.getElementById('roles-section').style.display = 'none';
    }

    // Task stats
    if (tasks && typeof tasks === 'object' && tasks.total !== undefined) {
      const tsHTML = [
        { label: 'Todo', value: tasks.todo || 0 },
        { label: 'Running', value: tasks.running || 0 },
        { label: 'Review', value: tasks.review || 0 },
        { label: 'Done', value: tasks.done || 0 },
        { label: 'Failed', value: tasks.failed || 0, color: (tasks.failed || 0) > 0 ? 'var(--red)' : '' },
        { label: 'Total', value: tasks.total || 0 },
      ].map(s => `<div class="task-stat"><div class="task-stat-label">${s.label}</div><div class="task-stat-value" ${s.color ? `style="color:${s.color}"` : ''}>${s.value}</div></div>`).join('');
      document.getElementById('task-stats').innerHTML = tsHTML;
      document.getElementById('tasks-section').style.display = '';
    } else {
      document.getElementById('tasks-section').style.display = 'none';
    }

    // Refresh kanban board if on board tab.
    refreshBoard();

  } catch (e) {
    const badge = document.getElementById('conn-badge');
    badge.textContent = 'Disconnected';
    badge.className = 'badge badge-err';
  }
}

function formatChain(j) {
  const parts = [];
  if (j.onSuccess && j.onSuccess.length > 0) parts.push('OK\u2192' + j.onSuccess.join(','));
  if (j.onFailure && j.onFailure.length > 0) parts.push('ERR\u2192' + j.onFailure.join(','));
  return parts.length > 0 ? parts.join(' ') : '-';
}

function esc(s) {
  if (!s) return '';
  if (typeof s === 'object') s = s.name || s.id || String(s);
  const el = document.createElement('span');
  el.textContent = s;
  return el.innerHTML;
}

function agentName(a) {
  if (!a) return '';
  if (typeof a === 'object') return a.name || a.id || '';
  return String(a);
}

function parseDuration(s) {
  // Parse Go duration string like "5m30s", "1h2m3s", "45s" to seconds
  if (!s) return 0;
  let total = 0;
  const h = s.match(/(\d+)h/);
  const m = s.match(/(\d+)m/);
  const sec = s.match(/(\d+)s/);
  if (h) total += parseInt(h[1]) * 3600;
  if (m) total += parseInt(m[1]) * 60;
  if (sec) total += parseInt(sec[1]);
  return total;
}

function parseTimeout(s) {
  // Parse timeout string like "15m", "1h", "30s" to seconds
  if (!s) return 900; // default 15m
  let total = 0;
  const h = s.match(/(\d+)h/);
  const m = s.match(/(\d+)m/);
  const sec = s.match(/(\d+)s/);
  if (h) total += parseInt(h[1]) * 3600;
  if (m) total += parseInt(m[1]) * 60;
  if (sec) total += parseInt(sec[1]);
  return total || 900;
}

async function refreshRunning() {
  try {
    const tasks = await fetchJSON('/tasks/running');
    const section = document.getElementById('running-section');
    const container = document.getElementById('running-tasks');

    if (!Array.isArray(tasks) || tasks.length === 0) {
      section.style.display = 'none';
      return;
    }

    section.style.display = '';
    document.getElementById('running-meta').textContent = `${tasks.length} running`;

    // Build tree: group by parentId, walk recursively for ordered flat list.
    const byId = {};
    const byParent = {};
    const roots = [];
    tasks.forEach(t => {
      byId[t.id] = t;
      if (t.parentId && byId[t.parentId]) {
        (byParent[t.parentId] = byParent[t.parentId] || []).push(t);
      } else if (t.parentId) {
        // Orphan sub-agent (parent already finished) — treat as root with depth intact.
        roots.push(t);
      } else {
        roots.push(t);
      }
    });
    // Also index children whose parents were added after them.
    tasks.forEach(t => {
      if (t.parentId && byId[t.parentId] && !roots.includes(t)) {
        if (!byParent[t.parentId] || !byParent[t.parentId].includes(t)) {
          (byParent[t.parentId] = byParent[t.parentId] || []).push(t);
        }
      }
    });

    const ordered = [];
    function walkTree(list) {
      for (const t of list) {
        ordered.push(t);
        if (byParent[t.id]) walkTree(byParent[t.id]);
      }
    }
    walkTree(roots);
    // Include any tasks not yet in ordered (safety net).
    tasks.forEach(t => { if (!ordered.includes(t)) ordered.push(t); });

    container.innerHTML = ordered.map(t => {
      const elapsedSec = parseDuration(t.elapsed);
      const timeoutSec = parseTimeout(t.timeout);
      const pct = Math.min((elapsedSec / timeoutSec) * 100, 100);
      const barColor = pct >= 90 ? 'var(--red)' : pct >= 70 ? 'var(--yellow)' : 'var(--accent)';

      const remaining = timeoutSec - elapsedSec;
      const remainStr = remaining > 0
        ? (remaining >= 60 ? Math.floor(remaining/60) + 'm ' + (remaining%60) + 's left' : remaining + 's left')
        : 'overtime!';

      const pidInfo = t.pid > 0
        ? `<span class="${t.pidAlive ? 'pid-alive' : 'pid-dead'}">PID ${t.pid} ${t.pidAlive ? 'alive' : 'dead!'}</span>`
        : '';

      const depth = t.depth || 0;
      const indentStyle = depth > 0 ? ` style="margin-left:${depth * 24}px"` : '';
      const indentClass = depth > 0 ? ' task-indent' : '';
      const roleBadge = t.agent ? `<span class="task-role">${esc(t.agent)}</span>` : '';
      const subLabel = (t.source && t.source.startsWith('agent_dispatch')) ? '<span class="task-sub-label">sub-agent</span>' : '';
      const sourceDisplay = t.source || '';

      return `<div class="running-task${indentClass}" id="task-${esc(t.id)}"${indentStyle}>
        <div class="running-task-header">
          <span class="running-task-name">${roleBadge}<span class="dot dot-yellow"></span>${esc(t.name)}${subLabel}</span>
          <div style="display:flex;align-items:center;gap:8px">
            <span style="font-size:12px;color:var(--muted)">${esc(sourceDisplay)}</span>
            <button class="btn-live" id="live-btn-${esc(t.id)}" onclick="toggleLive('${esc(t.id)}')">Live</button>
            <button class="btn btn-del" onclick="cancelTask('${esc(t.id)}')" style="font-size:10px;padding:2px 8px">Cancel</button>
          </div>
        </div>
        <div class="running-task-meta">
          <span>Model: ${esc(t.model || '?')}</span>
          <span>Elapsed: ${esc(t.elapsed)}</span>
          <span>Timeout: ${esc(t.timeout || '15m')}</span>
          ${pidInfo}
        </div>
        <div class="timeout-bar">
          <div class="timeout-bar-fill" style="width:${pct}%;background:${barColor}"></div>
        </div>
        <div class="timeout-bar-info">
          <span>${Math.round(pct)}%</span>
          <span>${remainStr}</span>
        </div>
        ${t.prompt ? `<div class="running-prompt">${esc(t.prompt)}</div>` : ''}
        <div class="live-output" id="live-${esc(t.id)}" style="display:none"></div>
      </div>`;
    }).join('');
    cleanupLiveStreams();
  } catch(e) {
    document.getElementById('running-section').style.display = 'none';
  }
}

async function refreshTrend() {
  try {
    const stats = await fetchJSON('/stats/trend?days=7');
    const section = document.getElementById('trend-section');
    if (!Array.isArray(stats) || stats.length === 0) {
      section.style.display = 'none';
      return;
    }
    section.style.display = '';

    const maxTotal = Math.max(...stats.map(s => s.total), 1);
    const chartEl = document.getElementById('trend-chart');

    chartEl.innerHTML = '<div style="display:flex;gap:4px;align-items:flex-end">' +
      stats.map(s => {
        const successH = Math.round((s.success / maxTotal) * 80);
        const failH = Math.round((s.fail / maxTotal) * 80);
        const dateLabel = s.date ? s.date.slice(5) : '?'; // "MM-DD"
        return `<div class="trend-bar-group">
          <div class="trend-cost">${costFmt(s.cost)}</div>
          <div class="trend-bars">
            ${s.fail > 0 ? `<div class="trend-bar" style="height:${failH}px;background:var(--red)"></div>` : ''}
            ${s.success > 0 ? `<div class="trend-bar" style="height:${successH}px;background:var(--green)"></div>` : ''}
          </div>
          <div class="trend-label">${esc(dateLabel)}</div>
          <div class="trend-label">${s.total}</div>
        </div>`;
      }).join('') + '</div>';
  } catch(e) {
    document.getElementById('trend-section').style.display = 'none';
  }
}

