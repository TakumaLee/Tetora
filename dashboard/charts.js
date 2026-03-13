// Inject pulse animation for running/waiting indicators
(function() {
  if (!document.getElementById('wf-charts-style')) {
    var style = document.createElement('style');
    style.id = 'wf-charts-style';
    style.textContent = '@keyframes pulse{0%,100%{opacity:1}50%{opacity:0.3}}';
    document.head.appendChild(style);
  }
})();

// --- Agent Communication ---
async function refreshAgentComm() {
  try {
    const [handoffs, messages] = await Promise.all([
      fetchJSON('/handoffs').catch(() => []),
      fetchJSON('/agent-messages?limit=20').catch(() => [])
    ]);

    const section = document.getElementById('agent-comm-section');
    const total = (handoffs || []).length + (messages || []).length;
    section.style.display = total > 0 ? '' : 'none';
    document.getElementById('agent-comm-meta').textContent = total > 0 ?
      `${(handoffs||[]).length} handoffs, ${(messages||[]).length} messages` : '';

    const body = document.getElementById('agent-comm-body');
    let html = '';

    // Render handoffs.
    if (handoffs && handoffs.length > 0) {
      html += '<div style="font-size:12px;font-weight:600;color:var(--accent);margin-top:4px">Handoffs</div>';
      handoffs.forEach(function(h) {
        const statusColor = h.status === 'completed' ? 'var(--green)' :
          h.status === 'active' ? 'var(--accent)' :
          h.status === 'error' ? '#f87171' : 'var(--muted)';
        const shortId = h.id.length > 8 ? h.id.substring(0, 8) : h.id;
        const inst = (h.instruction || '').substring(0, 80);
        html += '<div style="background:var(--card);border:1px solid var(--border);border-radius:6px;padding:8px 12px;font-size:12px">' +
          '<span style="color:var(--accent)">' + esc(h.fromRole) + '</span>' +
          ' <span style="color:var(--muted)">\u2192</span> ' +
          '<span style="color:var(--green)">' + esc(h.toRole) + '</span>' +
          ' <span style="color:' + statusColor + ';font-size:11px;margin-left:8px">[' + esc(h.status) + ']</span>' +
          ' <span style="color:var(--muted);font-size:11px;margin-left:8px">' + esc(shortId) + '</span>' +
          (inst ? '<div style="color:var(--muted);margin-top:4px;font-size:11px">' + esc(inst) + '</div>' : '') +
          '</div>';
      });
    }

    // Render recent messages.
    if (messages && messages.length > 0) {
      html += '<div style="font-size:12px;font-weight:600;color:var(--accent);margin-top:8px">Recent Messages</div>';
      messages.forEach(function(m) {
        const typeColor = m.type === 'handoff' ? 'var(--accent)' :
          m.type === 'response' ? 'var(--green)' :
          m.type === 'request' ? '#fbbf24' : 'var(--muted)';
        const content = (m.content || '').substring(0, 120);
        const ts = (m.createdAt || '').substring(0, 19);
        html += '<div style="background:var(--card);border:1px solid var(--border);border-radius:6px;padding:6px 12px;font-size:11px">' +
          '<span style="color:' + typeColor + '">[' + esc(m.type) + ']</span> ' +
          '<span style="color:var(--accent)">' + esc(m.fromRole) + '</span>' +
          ' \u2192 ' +
          '<span style="color:var(--green)">' + esc(m.toRole) + '</span>' +
          '<span style="color:var(--muted);float:right">' + esc(ts) + '</span>' +
          '<div style="color:var(--text);margin-top:2px">' + esc(content) + '</div>' +
          '</div>';
      });
    }

    body.innerHTML = html;
  } catch(e) { console.error('refreshAgentComm:', e); }
}

// --- Version History ---
async function refreshVersions() {
  try {
    var filter = document.getElementById('version-type-filter').value;
    var url = '/versions?limit=30';
    if (filter) url += '&type=' + filter;
    const res = await fetchAPI(url);
    const versions = await res.json();
    const section = document.getElementById('version-section');
    const body = document.getElementById('version-body');
    const meta = document.getElementById('version-meta');

    section.style.display = '';
    meta.textContent = versions.length + ' version' + (versions.length !== 1 ? 's' : '');

    if (versions.length === 0) {
      body.innerHTML = '<tr><td colspan="7" style="text-align:center;color:var(--muted);padding:16px">No version history</td></tr>';
      return;
    }

    var html = '';
    versions.forEach(function(v) {
      var ts = v.createdAt || '';
      if (ts.length > 19) ts = ts.substring(0, 19);
      var diff = v.diffSummary || '';
      if (diff.length > 60) diff = diff.substring(0, 60) + '...';
      var typeColor = v.entityType === 'config' ? '#3b82f6' : (v.entityType === 'workflow' ? '#8b5cf6' : '#eab308');

      html += '<tr>' +
        '<td><code style="font-size:11px">' + esc(v.versionId) + '</code></td>' +
        '<td><span style="background:' + typeColor + ';color:#fff;padding:1px 6px;border-radius:8px;font-size:10px">' + esc(v.entityType) + '</span></td>' +
        '<td>' + esc(v.entityName) + '</td>' +
        '<td style="font-size:12px;color:var(--muted)">' + esc(diff) + '</td>' +
        '<td>' + esc(v.changedBy) + '</td>' +
        '<td style="font-size:12px">' + esc(ts) + '</td>' +
        '<td><button class="btn" style="font-size:11px;padding:2px 6px" onclick="showVersion(\'' + esc(v.versionId) + '\')">View</button>';
      if (v.entityType === 'config') {
        html += ' <button class="btn" style="font-size:11px;padding:2px 6px" onclick="restoreVersion(\'' + esc(v.versionId) + '\')">Restore</button>';
      }
      html += '</td></tr>';
    });
    body.innerHTML = html;
  } catch(e) { console.error('refreshVersions:', e); }
}

async function snapshotConfig() {
  try {
    await fetchAPI('/config/versions', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({reason:'manual snapshot'})});
    refreshVersions();
  } catch(e) { alert('Snapshot failed: ' + e.message); }
}

async function showVersion(vid) {
  try {
    const res = await fetchAPI('/config/versions/' + vid);
    const ver = await res.json();
    var modal = document.getElementById('version-diff-modal');
    var title = document.getElementById('diff-title');
    var content = document.getElementById('diff-content');
    title.textContent = 'Version ' + ver.versionId + ' (' + ver.entityType + '/' + ver.entityName + ')';
    try {
      var parsed = JSON.parse(ver.contentJson);
      content.textContent = JSON.stringify(parsed, null, 2);
    } catch(e) {
      content.textContent = ver.contentJson;
    }
    modal.style.display = '';
  } catch(e) { alert('Error: ' + e.message); }
}

async function restoreVersion(vid) {
  if (!confirm('Restore config to version ' + vid + '? The daemon will need a restart.')) return;
  try {
    await fetchAPI('/config/versions/' + vid + '/restore', {method:'POST'});
    alert('Config restored. Restart the daemon for changes to take effect.');
    refreshVersions();
  } catch(e) { alert('Restore failed: ' + e.message); }
}

// --- Trust Gradient ---
async function refreshTrust() {
  try {
    const res = await fetchAPI('/trust');
    const statuses = await res.json();
    const section = document.getElementById('trust-section');
    const cards = document.getElementById('trust-cards');
    const meta = document.getElementById('trust-meta');

    if (!statuses || statuses.length === 0) {
      section.style.display = 'none';
      return;
    }

    section.style.display = '';
    meta.textContent = statuses.length + ' agent' + (statuses.length !== 1 ? 's' : '');

    var html = '';
    statuses.forEach(function(s) {
      var color, bg, icon;
      if (s.level === 'observe') { color = '#3b82f6'; bg = 'rgba(59,130,246,0.1)'; icon = 'O'; }
      else if (s.level === 'suggest') { color = '#eab308'; bg = 'rgba(234,179,8,0.1)'; icon = 'S'; }
      else { color = '#22c55e'; bg = 'rgba(34,211,153,0.06)'; icon = 'A'; }

      html += '<div style="background:' + bg + ';border:1px solid var(--border);border-radius:8px;padding:12px 16px;min-width:180px;flex:1;max-width:240px">' +
        '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">' +
          '<strong style="color:var(--text)">' + esc(s.agent) + '</strong>' +
          '<span style="background:' + color + ';color:#fff;padding:2px 8px;border-radius:10px;font-size:11px;font-weight:600">' + icon + ' ' + s.level + '</span>' +
        '</div>' +
        '<div style="font-size:12px">' +
          '<div><span style="color:var(--muted)">Streak:</span> ' + s.consecutiveSuccess + '</div>' +
          '<div><span style="color:var(--muted)">Tasks:</span> ' + s.totalTasks + '</div>';
      if (s.promoteReady) {
        html += '<div style="margin-top:4px;color:#22c55e;font-weight:600">Ready: ' + s.level + ' -> ' + s.nextLevel + '</div>';
      }
      html += '</div></div>';
    });
    cards.innerHTML = html;
  } catch(e) { console.error('refreshTrust:', e); }
}

// --- Agent SLA ---
async function refreshSLA() {
  try {
    const res = await fetchAPI('/stats/sla');
    const data = await res.json();
    const statuses = data.statuses || [];
    const section = document.getElementById('sla-section');
    const cards = document.getElementById('sla-cards');
    const meta = document.getElementById('sla-meta');

    if (statuses.length === 0) {
      section.style.display = 'none';
      return;
    }

    section.style.display = '';
    var violations = statuses.filter(function(s) { return s.status === 'violation'; }).length;
    var warnings = statuses.filter(function(s) { return s.status === 'warning'; }).length;
    var parts = [statuses.length + ' role' + (statuses.length !== 1 ? 's' : '')];
    if (violations > 0) parts.push(violations + ' violation' + (violations !== 1 ? 's' : ''));
    if (warnings > 0) parts.push(warnings + ' warning' + (warnings !== 1 ? 's' : ''));
    meta.textContent = parts.join(', ');

    var html = '';
    statuses.forEach(function(s) {
      var color, label, bg;
      if (s.status === 'violation') { color = '#ef4444'; label = 'Violation'; bg = 'rgba(239,68,68,0.1)'; }
      else if (s.status === 'warning') { color = '#eab308'; label = 'Warning'; bg = 'rgba(234,179,8,0.1)'; }
      else { color = '#22c55e'; label = 'OK'; bg = 'rgba(34,211,153,0.06)'; }

      var rate = s.total > 0 ? (s.successRate * 100).toFixed(1) + '%' : 'N/A';
      var latency = s.avgLatencyMs > 0 ? (s.avgLatencyMs / 1000).toFixed(1) + 's' : 'N/A';
      var p95 = s.p95LatencyMs > 0 ? (s.p95LatencyMs / 1000).toFixed(1) + 's' : 'N/A';
      var cost = s.totalCost > 0 ? '$' + s.totalCost.toFixed(2) : '$0';

      html += '<div style="background:' + bg + ';border:1px solid var(--border);border-radius:8px;padding:12px 16px;min-width:200px;flex:1;max-width:280px">' +
        '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">' +
          '<strong style="color:var(--text)">' + esc(s.agent) + '</strong>' +
          '<span style="background:' + color + ';color:#fff;padding:2px 8px;border-radius:10px;font-size:11px;font-weight:600">' + label + '</span>' +
        '</div>' +
        '<div style="display:grid;grid-template-columns:1fr 1fr;gap:4px;font-size:12px">' +
          '<div><span style="color:var(--muted)">Success:</span> <span style="color:' + (s.successRate >= 0.95 ? 'var(--green)' : s.successRate >= 0.9 ? 'var(--yellow)' : 'var(--red)') + '">' + rate + '</span></div>' +
          '<div><span style="color:var(--muted)">Tasks:</span> ' + s.total + ' (' + s.success + '/' + s.fail + ')</div>' +
          '<div><span style="color:var(--muted)">Avg:</span> ' + latency + '</div>' +
          '<div><span style="color:var(--muted)">P95:</span> ' + p95 + '</div>' +
          '<div style="grid-column:span 2"><span style="color:var(--muted)">Cost:</span> ' + cost + '</div>' +
        '</div>';
      if (s.violation) {
        html += '<div style="margin-top:6px;font-size:11px;color:' + color + '">' + esc(s.violation) + '</div>';
      }
      html += '</div>';
    });
    cards.innerHTML = html;
  } catch(e) { console.error('refreshSLA:', e); }
}

// --- Provider Health (Circuit Breakers) ---
async function refreshCircuits() {
  try {
    const res = await fetchAPI('/circuits');
    const data = await res.json();
    const providers = Object.keys(data);
    const section = document.getElementById('circuits-section');
    const list = document.getElementById('circuits-list');
    const meta = document.getElementById('circuits-meta');

    if (providers.length === 0) {
      section.style.display = 'none';
      return;
    }

    section.style.display = '';
    const openCount = providers.filter(p => data[p].state === 'open').length;
    meta.textContent = providers.length + ' provider' + (providers.length !== 1 ? 's' : '') +
      (openCount > 0 ? ' (' + openCount + ' open)' : '');

    let html = '';
    providers.sort().forEach(function(name) {
      const info = data[name];
      const st = info.state || 'closed';
      let color = '#22c55e'; // green for closed
      let label = 'Healthy';
      if (st === 'open') { color = '#ef4444'; label = 'Open'; }
      else if (st === 'half-open') { color = '#eab308'; label = 'Recovering'; }

      html += '<div style="background:var(--card);border:1px solid var(--border);border-radius:8px;padding:12px 16px;min-width:180px;display:flex;flex-direction:column;gap:4px">' +
        '<div style="display:flex;justify-content:space-between;align-items:center">' +
          '<strong style="color:var(--text)">' + esc(name) + '</strong>' +
          '<span style="background:' + color + ';color:#fff;padding:2px 8px;border-radius:10px;font-size:11px;font-weight:600">' + label + '</span>' +
        '</div>' +
        '<div style="font-size:12px;color:var(--muted)">' +
          'Failures: ' + (info.failures || 0) +
          (info.lastFailure ? ' &middot; Last: ' + info.lastFailure.replace('T',' ').slice(0,19) : '') +
        '</div>';

      if (st !== 'closed') {
        html += '<button onclick="resetCircuit(\'' + esc(name) + '\')" style="margin-top:4px;padding:4px 10px;background:var(--accent);color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:11px;align-self:flex-start">Reset</button>';
      }
      html += '</div>';
    });
    list.innerHTML = html;
  } catch(e) { console.error('refreshCircuits:', e); }
}

async function resetCircuit(provider) {
  try {
    await fetchAPI('/circuits/' + encodeURIComponent(provider) + '/reset', { method: 'POST' });
    refreshCircuits();
  } catch(e) { alert('Reset failed: ' + e.message); }
}

// --- P18.1: Cost Dashboard ---

var costPeriod = 'today';

async function loadCostDashboard(period) {
  if (period) costPeriod = period;
  // Update button styles.
  ['today','week','month'].forEach(function(p) {
    var btn = document.getElementById('cost-btn-' + p);
    if (btn) btn.style.fontWeight = (p === costPeriod) ? '700' : '400';
  });

  try {
    var [summary, models, roles, sessions] = await Promise.all([
      fetchJSON('/api/usage/summary?period=' + costPeriod).catch(function() { return {}; }),
      fetchJSON('/api/usage/breakdown?by=model&days=30').catch(function() { return []; }),
      fetchJSON('/api/usage/breakdown?by=role&days=30').catch(function() { return []; }),
      fetchJSON('/api/usage/sessions?limit=5&days=30').catch(function() { return []; }),
    ]);

    // Summary cards.
    var sumEl = document.getElementById('cost-summary');
    var budgetBar = '';
    if (summary.budgetLimit > 0) {
      var pct = Math.min((summary.totalCostUsd / summary.budgetLimit) * 100, 100);
      var barColor = pct >= 100 ? 'var(--red)' : pct >= 80 ? 'var(--yellow)' : 'var(--green)';
      budgetBar = '<div style="margin-top:6px;height:4px;background:var(--border);border-radius:2px;overflow:hidden"><div style="height:100%;width:' + pct + '%;background:' + barColor + ';border-radius:2px"></div></div><div style="font-size:10px;color:var(--muted);margin-top:2px">limit: ' + costFmt(summary.budgetLimit) + ' (' + pct.toFixed(1) + '%)</div>';
    }
    sumEl.innerHTML = [
      { label: 'Cost (' + costPeriod + ')', value: costFmt(summary.totalCostUsd || 0), extra: budgetBar },
      { label: 'Tasks', value: summary.totalTasks || 0 },
      { label: 'Tokens In', value: (summary.totalTokensIn || 0).toLocaleString() },
      { label: 'Tokens Out', value: (summary.totalTokensOut || 0).toLocaleString() },
    ].map(function(s) {
      return '<div class="stat"><div class="stat-label">' + s.label + '</div><div class="stat-value cost">' + s.value + '</div>' + (s.extra || '') + '</div>';
    }).join('');

    // Model breakdown.
    var modelBody = document.getElementById('cost-model-body');
    if (models.length === 0) {
      modelBody.innerHTML = '<tr><td colspan="4" style="color:var(--muted);text-align:center">No data</td></tr>';
    } else {
      modelBody.innerHTML = models.map(function(m) {
        return '<tr><td>' + esc(m.model) + '</td><td>' + m.tasks + '</td><td>' + costFmt(m.costUsd) + '</td><td>' + m.pct.toFixed(1) + '%</td></tr>';
      }).join('');
    }

    // Role breakdown.
    var roleBody = document.getElementById('cost-role-body');
    if (roles.length === 0) {
      roleBody.innerHTML = '<tr><td colspan="4" style="color:var(--muted);text-align:center">No data</td></tr>';
    } else {
      roleBody.innerHTML = roles.map(function(r) {
        return '<tr><td>' + esc(r.agent) + '</td><td>' + r.tasks + '</td><td>' + costFmt(r.costUsd) + '</td><td>' + r.pct.toFixed(1) + '%</td></tr>';
      }).join('');
    }

    // Expensive sessions.
    var sessBody = document.getElementById('cost-sessions-body');
    if (sessions.length === 0) {
      sessBody.innerHTML = '<tr><td colspan="6" style="color:var(--muted);text-align:center">No data</td></tr>';
    } else {
      sessBody.innerHTML = sessions.map(function(s) {
        var title = s.title || '(untitled)';
        if (title.length > 40) title = title.substring(0, 40) + '...';
        var ctxLimit = 200000;
        var ctxUsed = s.tokensIn || 0;
        var ctxPct = Math.min((ctxUsed / ctxLimit) * 100, 100);
        var ctxColor = ctxPct >= 90 ? 'var(--red)' : ctxPct >= 70 ? 'var(--yellow)' : 'var(--green)';
        var ctxBar = '<div style="height:3px;background:var(--border);border-radius:2px;margin-top:3px;overflow:hidden"><div style="height:100%;width:' + ctxPct.toFixed(1) + '%;background:' + ctxColor + ';border-radius:2px"></div></div>';
        var tokens = ctxUsed.toLocaleString() + ' / 200K (' + ctxPct.toFixed(0) + '%)' + ctxBar;
        var created = s.createdAt ? new Date(s.createdAt).toLocaleDateString() : '';
        return '<tr><td>' + esc(s.agent) + '</td><td title="' + esc(s.title || '') + '">' + esc(title) + '</td><td>' + s.messages + '</td><td style="font-size:12px">' + tokens + '</td><td>' + costFmt(s.totalCostUsd) + '</td><td style="font-size:12px">' + created + '</td></tr>';
      }).join('');
    }
    // Cost projection
    updateCostForecast(summary);
  } catch(e) {
    console.error('Cost dashboard error:', e);
  }
}

// --- Executive Summary ---
async function loadExecSummary() {
  try {
    const [summary, taskTrend] = await Promise.all([
      fetchJSON('/api/usage/summary?period=week').catch(() => ({})),
      fetchJSON('/api/tasks/trend?days=7').catch(() => []),
    ]);

    const doneTasks = taskTrend.reduce((sum, d) => sum + (d.done || 0), 0);
    const hoursPerTask = 2.0;
    const freelancerRate = 50;
    const hoursSaved = doneTasks * hoursPerTask;
    const humanCost = hoursSaved * freelancerRate;
    const aiCost = summary.totalCostUsd || 0;
    const roi = aiCost > 0 ? ((humanCost - aiCost) / aiCost * 100) : 0;

    document.getElementById('exec-tasks-val').textContent = doneTasks;
    document.getElementById('exec-hours-val').textContent = '~' + hoursSaved + 'h';
    document.getElementById('exec-cost-val').textContent = costFmt(aiCost);
    document.getElementById('exec-roi-val').textContent = roi > 0 ? roi.toFixed(0) + '%' : '-';

    document.getElementById('exec-summary').style.display = doneTasks > 0 || aiCost > 0 ? '' : 'none';
  } catch(e) { console.error('loadExecSummary:', e); }
}

// --- Period Comparison Deltas ---
async function loadPeriodDeltas() {
  try {
    const data = await fetchJSON('/api/usage/compare?period=week');
    const costEl = document.getElementById('exec-cost');
    if (costEl && data.costDelta !== undefined) {
      let existing = costEl.querySelector('.stat-delta');
      if (!existing) { existing = document.createElement('div'); costEl.appendChild(existing); }
      const arrow = data.costDelta > 0 ? '▲' : data.costDelta < 0 ? '▼' : '—';
      // For cost, up is bad (red), down is good (green) — invert colors
      const costCls = data.costDelta > 0 ? 'down' : data.costDelta < 0 ? 'up' : 'neutral';
      existing.className = 'stat-delta ' + costCls;
      existing.textContent = arrow + ' ' + Math.abs(data.costDelta).toFixed(1) + '% vs prev';
    }
  } catch(e) { /* silently fail — deltas are supplementary */ }
}

// --- Engineering Details Toggle ---
function toggleEngineering() {
  var zone = document.getElementById('zone-engineering');
  if (!zone) return;
  zone.classList.toggle('open');
  localStorage.setItem('tetora-eng-open', zone.classList.contains('open') ? '1' : '0');
}
(function() {
  if (localStorage.getItem('tetora-eng-open') === '1') {
    var zone = document.getElementById('zone-engineering');
    if (zone) zone.classList.add('open');
  }
})();

// --- Cost Projection ---
function updateCostForecast(summary) {
  var el = document.getElementById('cost-forecast');
  var cards = document.getElementById('cost-forecast-cards');
  if (!el || !cards) return;

  var cost = summary.totalCostUsd || 0;
  var period = costPeriod;
  var projectedMonthly = 0;
  var daysInPeriod = 1;

  if (period === 'today') { projectedMonthly = cost * 30; daysInPeriod = 1; }
  else if (period === 'week') { projectedMonthly = (cost / 7) * 30; daysInPeriod = 7; }
  else if (period === 'month') { projectedMonthly = cost; daysInPeriod = 30; }

  var dailyBurn = cost / daysInPeriod;
  var html = '<div class="stat"><div class="stat-label">Daily Burn Rate</div><div class="stat-value cost">' + costFmt(dailyBurn) + '/d</div></div>';
  html += '<div class="stat"><div class="stat-label">Projected Monthly</div><div class="stat-value cost">' + costFmt(projectedMonthly) + '</div></div>';

  cards.innerHTML = html;
  el.style.display = cost > 0 ? '' : 'none';
}

// --- Agent Team Scorecard ---
async function loadAgentScorecard() {
  try {
    var data = await fetchJSON('/api/agents/scorecard?days=7');
    var agents = data.agents || [];
    var section = document.getElementById('agent-scorecard-section');
    var grid = document.getElementById('agent-scorecard-grid');
    var meta = document.getElementById('scorecard-meta');

    if (agents.length === 0) { section.style.display = 'none'; return; }

    section.style.display = '';
    meta.textContent = agents.length + ' agent' + (agents.length !== 1 ? 's' : '') + ' (7d)';

    grid.innerHTML = agents.map(function(a) {
      var successColor = a.successRate >= 90 ? 'var(--green)' : a.successRate >= 70 ? 'var(--yellow)' : 'var(--red)';
      var avgDuration = a.avgDurationMs > 0 ? formatDuration(a.avgDurationMs) : '-';
      return '<div class="stat" style="text-align:center">' +
        '<div class="stat-label">' + esc(a.agent) + '</div>' +
        '<div class="stat-value" style="font-size:22px">' + a.doneTasks + '<span style="font-size:12px;color:var(--muted)"> done</span></div>' +
        '<div style="font-size:12px;margin-top:4px;color:' + successColor + '">' + a.successRate.toFixed(0) + '% success</div>' +
        '<div style="font-size:11px;color:var(--muted);margin-top:2px">' + costFmt(a.totalCost) + ' · ' + costFmt(a.avgCostPerTask) + '/task</div>' +
        '<div style="font-size:11px;color:var(--muted)">' + avgDuration + ' avg</div>' +
        '</div>';
    }).join('');
  } catch(e) { console.error('loadAgentScorecard:', e); }
}

// --- Export / Copy Report ---
async function exportStatsJSON() {
  try {
    const [summary, trend, agents] = await Promise.all([
      fetchJSON('/api/usage/summary?period=week').catch(() => ({})),
      fetchJSON('/api/tasks/trend?days=7').catch(() => []),
      fetchJSON('/api/agents/scorecard?days=7').catch(() => ({ agents: [] })),
    ]);
    const report = {
      generated: new Date().toISOString(),
      period: 'week',
      summary: summary,
      taskTrend: trend,
      agents: agents.agents || [],
    };
    const blob = new Blob([JSON.stringify(report, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'tetora-report-' + new Date().toISOString().slice(0,10) + '.json';
    a.click();
    URL.revokeObjectURL(url);
    toast('Report exported');
  } catch(e) { toast('Export failed: ' + e.message); }
}

async function copyStatsSummary() {
  try {
    const [summary, trend, agents] = await Promise.all([
      fetchJSON('/api/usage/summary?period=week').catch(() => ({})),
      fetchJSON('/api/tasks/trend?days=7').catch(() => []),
      fetchJSON('/api/agents/scorecard?days=7').catch(() => ({ agents: [] })),
    ]);
    const doneTasks = trend.reduce((s, d) => s + (d.done || 0), 0);
    const hoursSaved = doneTasks * 2;
    const cost = summary.totalCostUsd || 0;
    const roi = cost > 0 ? (((hoursSaved * 50) - cost) / cost * 100).toFixed(0) : 0;
    const now = new Date();
    const weekStart = new Date(now); weekStart.setDate(now.getDate() - 7);
    const fmt = d => d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });

    let topAgent = '-';
    const agentList = agents.agents || [];
    if (agentList.length > 0) {
      const top = agentList[0];
      topAgent = top.agent + ' (' + top.doneTasks + ' tasks, ' + costFmt(top.totalCost) + ')';
    }

    const text = [
      'Tetora AI Team — Weekly Report (' + fmt(weekStart) + ' - ' + fmt(now) + ')',
      'Tasks: ' + doneTasks + ' | Cost: ' + costFmt(cost) + ' | Hours Saved: ~' + hoursSaved + 'h',
      'ROI: ' + roi + '% | Effective Rate: ' + (hoursSaved > 0 ? costFmt(cost / hoursSaved) + '/hr' : '-'),
      'Top Agent: ' + topAgent,
    ].join('\n');

    await navigator.clipboard.writeText(text);
    toast('Summary copied to clipboard');
  } catch(e) { toast('Copy failed: ' + e.message); }
}

// Init
applyDashView();
loadCostDashboard('today');
if (currentDashView === 'default' || currentDashView === 'selector') {
  refresh();
} else {
  // Still do a lightweight refresh for top stats, then load view-specific data
  refresh();
  loadDashViewData();
}
refreshWorkers();
refreshPlanReviews();
refreshPrompts();
refreshMCP();
refreshMemory();
refreshRouting();
refreshCircuits();
refreshTrust();
refreshVersions();
refreshSLA();
refreshSessions();
refreshAgentComm();
loadArchetypes();

// --- Workflow Visualization ---

var currentWfRun = null;
var currentWfRunData = null;
var wfSSE = null;

function refreshWorkflowRuns() {
  fetch('/workflow-runs', {credentials:'same-origin'}).then(function(resp) {
    return resp.json();
  }).then(function(runs) {
    renderWfRunsList(runs || []);
  }).catch(function() {
    document.getElementById('wf-runs-list').innerHTML = '<div style="text-align:center;color:var(--muted);padding:20px;font-size:13px">Failed to load workflow runs</div>';
  });
}

function renderWfRunsList(runs) {
  var el = document.getElementById('wf-runs-list');
  if (!runs.length) {
    el.innerHTML = '<div style="text-align:center;color:var(--muted);padding:20px;font-size:13px">No workflow runs yet</div>';
    return;
  }
  var html = '<div class="table-wrap"><table class="wf-runs-table"><thead><tr><th>ID</th><th>Workflow</th><th>Status</th><th>Duration</th><th>Cost</th><th>Started</th></tr></thead><tbody>';
  runs.forEach(function(r) {
    var id = (r.id || '').substring(0, 8);
    var dur = r.durationMs ? formatDuration(r.durationMs) : '-';
    var cost = r.totalCostUsd != null ? '$' + r.totalCostUsd.toFixed(4) : '-';
    var started = r.startedAt ? r.startedAt.substring(0, 19).replace('T', ' ') : '-';
    var statusCls = r.status === 'success' ? 'badge-ok' : (r.status === 'error' || r.status === 'timeout') ? 'badge-err' : r.status === 'resumed' ? '' : 'badge-warn';
    var statusLabel = r.status || '';
    if (r.resumedFrom) statusLabel += ' (resumed)';
    html += '<tr onclick="openWfRun(\'' + escAttr(r.id) + '\')"><td><code style="font-size:11px">' + esc(id) + '</code></td><td>' + esc(r.workflowName || '') + '</td><td><span class="badge ' + statusCls + '">' + esc(statusLabel) + '</span></td><td>' + dur + '</td><td><span class="stat-value cost" style="font-size:13px">' + cost + '</span></td><td style="font-size:12px;color:var(--muted)">' + started + '</td></tr>';
  });
  html += '</tbody></table></div>';
  el.innerHTML = html;
}

function openWfRun(runId) {
  currentWfRun = runId;
  fetch('/workflow-runs/' + runId, {credentials:'same-origin'}).then(function(resp) {
    return resp.json();
  }).then(function(data) {
    currentWfRunData = data;
    // Support both flat run object and new {run, handoffs, messages, callbacks} shape
    var runObj = data.run || data;
    return fetch('/workflows/' + encodeURIComponent(runObj.workflowName), {credentials:'same-origin'}).then(function(r) {
      return r.json();
    }).catch(function() { return null; }).then(function(wfDef) {
      document.getElementById('wf-dag-section').style.display = '';
      document.getElementById('wf-dag-title').textContent = runObj.workflowName + ' / ' + runId.substring(0, 8);
      var statusCls = runObj.status === 'success' ? 'badge-ok' : (runObj.status === 'error' || runObj.status === 'timeout') ? 'badge-err' : 'badge-warn';
      document.getElementById('wf-dag-status').textContent = runObj.status;
      document.getElementById('wf-dag-status').className = 'badge ' + statusCls;

      renderWfTimeline(runObj);
      renderWfDAG(runObj, wfDef);
      renderWfStepList(data);
      renderWfCostBar(data);

      // Subscribe to SSE if running
      if (runObj.status === 'running') {
        subscribeWfSSE(runId);
      } else if (wfSSE) {
        wfSSE.close();
        wfSSE = null;
      }

      // Scroll to DAG section
      document.getElementById('wf-dag-section').scrollIntoView({behavior: 'smooth', block: 'start'});
    });
  }).catch(function(e) {
    console.error('openWfRun error:', e);
  });
}

// Step results list below DAG
function renderWfStepList(data) {
  var container = document.getElementById('wf-step-list');
  if (!container) {
    // Create container after DAG
    container = document.createElement('div');
    container.id = 'wf-step-list';
    container.style.cssText = 'margin-top:12px;border-top:1px solid var(--border);padding-top:12px';
    var dagSection = document.getElementById('wf-dag-section');
    if (dagSection) dagSection.appendChild(container);
    else return;
  }

  var run = data.run || data;
  var stepResults = run.stepResults || {};
  var callbacks = data.callbacks || [];
  var steps = Object.values(stepResults);

  // Sort by startedAt
  steps.sort(function(a, b) {
    if (!a.startedAt) return 1;
    if (!b.startedAt) return -1;
    return a.startedAt < b.startedAt ? -1 : 1;
  });

  var html = '<div style="font-size:13px;font-weight:600;margin-bottom:8px">Step Results</div>';
  steps.forEach(function(sr) {
    var statusCls = sr.status === 'success' ? 'badge-ok' : (sr.status === 'error' || sr.status === 'timeout') ? 'badge-err' : sr.status === 'waiting' ? 'badge-warn' : sr.status === 'running' ? 'badge-warn' : '';
    var dur = sr.durationMs ? formatDuration(sr.durationMs) : '-';
    // Show live elapsed time for running steps.
    if (sr.status === 'running' && sr.startedAt && !sr.durationMs) {
      var elapsed = Math.round((Date.now() - new Date(sr.startedAt).getTime()) / 1000);
      dur = elapsed + 's...';
    }
    var cost = sr.costUsd != null && sr.costUsd > 0 ? '$' + sr.costUsd.toFixed(4) : '-';

    html += '<div id="wf-step-row-' + escAttr(sr.stepId) + '" style="background:var(--surface);border:1px solid var(--border);border-radius:6px;padding:10px;margin-bottom:6px;cursor:pointer" onclick="toggleStepDetail(this)">';
    html += '<div style="display:flex;justify-content:space-between;align-items:center">';
    html += '<div style="display:flex;align-items:center;gap:8px">';
    if (sr.status === 'running') html += '<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:#fbbf24;animation:pulse 1s infinite"></span>';
    else if (sr.status === 'waiting') html += '<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:#a78bfa;animation:pulse 1.5s infinite"></span>';
    html += '<strong style="font-size:12px">' + esc(sr.stepId) + '</strong>';
    html += '<span class="badge ' + statusCls + '" style="font-size:10px">' + esc(sr.status) + '</span>';
    html += '</div>';
    html += '<div style="display:flex;gap:12px;font-size:11px;color:var(--muted)">';
    html += '<span>' + dur + '</span>';
    html += '<span>' + cost + '</span>';
    if (sr.retries) html += '<span>retries: ' + sr.retries + '</span>';
    html += '</div>';
    html += '</div>';

    // Expandable detail (hidden by default)
    html += '<div class="step-detail" style="display:none;margin-top:8px;padding-top:8px;border-top:1px solid var(--border);font-size:12px">';
    if (sr.error) html += '<div style="color:#f87171;margin-bottom:4px"><strong>Error:</strong> ' + esc(sr.error) + '</div>';
    if (sr.output) {
      var output = sr.output.length > 1000 ? sr.output.substring(0, 1000) + '...' : sr.output;
      html += '<div style="white-space:pre-wrap;background:var(--bg);padding:8px;border-radius:4px;max-height:200px;overflow-y:auto;font-family:monospace;font-size:11px">' + esc(output) + '</div>';
    }
    if (sr.taskId) html += '<div style="margin-top:4px;color:var(--muted)">Task: <code>' + esc(sr.taskId.substring(0, 8)) + '</code></div>';

    // Streaming output container for running steps.
    if (sr.status === 'running') {
      html += '<div id="wf-stream-' + escAttr(sr.stepId) + '" style="white-space:pre-wrap;background:var(--bg);padding:8px;border-radius:4px;max-height:300px;overflow-y:auto;font-family:monospace;font-size:11px;margin-top:6px;color:var(--text)"><span style="color:var(--muted)">Streaming...</span></div>';
    }

    // Callback info for waiting steps
    if (sr.status === 'waiting') {
      var cb = callbacks.find(function(c) { return c.step_id === sr.stepId; });
      if (cb) {
        var cbUrl = location.origin + '/api/callbacks/' + encodeURIComponent(cb.key);
        html += '<div style="margin-top:8px;padding:8px;background:var(--bg);border-radius:4px">';
        html += '<div style="font-size:11px;color:var(--muted);margin-bottom:4px">Waiting for external callback</div>';
        html += '<div style="font-size:11px">Key: <code style="cursor:pointer" onclick="event.stopPropagation();copyText(\'' + cbUrl.replace(/'/g, "\\'") + '\')" title="Click to copy URL">' + esc(cb.key) + '</code></div>';
        if (cb.timeout_at) {
          var timeoutAt = new Date(cb.timeout_at);
          var remaining = Math.max(0, Math.floor((timeoutAt - Date.now()) / 1000));
          if (remaining > 0) {
            var mins = Math.floor(remaining / 60);
            var secs = remaining % 60;
            html += '<div style="font-size:11px;color:#fbbf24;margin-top:4px">Timeout in: ' + mins + 'm ' + secs + 's</div>';
          } else {
            html += '<div style="font-size:11px;color:#f87171;margin-top:4px">Timeout expired</div>';
          }
        }
        html += '</div>';
      }
    }
    html += '</div>';
    html += '</div>';
  });

  container.innerHTML = html;
}

function toggleStepDetail(el) {
  var detail = el.querySelector('.step-detail');
  if (detail) {
    detail.style.display = detail.style.display === 'none' ? '' : 'none';
  }
}

// Cost breakdown horizontal bar
function renderWfCostBar(data) {
  var container = document.getElementById('wf-cost-bar');
  if (!container) {
    container = document.createElement('div');
    container.id = 'wf-cost-bar';
    container.style.cssText = 'margin-top:8px';
    var dagSection = document.getElementById('wf-dag-section');
    if (dagSection) dagSection.appendChild(container);
    else return;
  }

  var run = data.run || data;
  var total = run.totalCostUsd || 0;
  if (total <= 0) { container.innerHTML = ''; return; }

  var stepResults = run.stepResults || {};
  var steps = Object.values(stepResults).filter(function(s) { return s.costUsd > 0; });
  steps.sort(function(a, b) { return b.costUsd - a.costUsd; });

  var colors = ['#60a5fa', '#34d399', '#fbbf24', '#f87171', '#a78bfa', '#fb923c', '#2dd4bf', '#e879f9'];

  var html = '<div style="font-size:13px;font-weight:600;margin-bottom:4px">Cost: $' + total.toFixed(4) + '</div>';
  html += '<div style="display:flex;height:16px;border-radius:4px;overflow:hidden;background:var(--bg)">';
  steps.forEach(function(s, i) {
    var pct = (s.costUsd / total * 100).toFixed(1);
    var color = colors[i % colors.length];
    html += '<div title="' + escAttr(s.stepId) + ': $' + s.costUsd.toFixed(4) + ' (' + pct + '%)" style="width:' + pct + '%;background:' + color + ';min-width:2px"></div>';
  });
  html += '</div>';

  // Legend
  if (steps.length > 1) {
    html += '<div style="display:flex;flex-wrap:wrap;gap:8px;margin-top:4px;font-size:11px">';
    steps.forEach(function(s, i) {
      var color = colors[i % colors.length];
      html += '<span><span style="display:inline-block;width:8px;height:8px;border-radius:2px;background:' + color + '"></span> ' + esc(s.stepId) + ' $' + s.costUsd.toFixed(4) + '</span>';
    });
    html += '</div>';
  }

  container.innerHTML = html;
}

function closeWfDag() {
  document.getElementById('wf-dag-section').style.display = 'none';
  document.getElementById('wf-node-detail').style.display = 'none';
  currentWfRun = null;
  currentWfRunData = null;
  if (wfSSE) { wfSSE.close(); wfSSE = null; }
}

// DAG Layout Algorithm (layered graph)
function layoutDAG(stepResults, wfDef) {
  var steps = [];
  var depMap = {};

  // Build from workflow definition if available
  if (wfDef && wfDef.steps) {
    wfDef.steps.forEach(function(s) {
      depMap[s.id] = s.dependsOn || [];
      steps.push({id: s.id, type: s.type || 'dispatch', agent: s.agent || '', deps: s.dependsOn || [], handoffFrom: s.handoffFrom || ''});
    });
  } else {
    // Fallback: infer from stepResults keys (no dependency info)
    Object.keys(stepResults).forEach(function(id) {
      depMap[id] = [];
      steps.push({id: id, type: 'dispatch', agent: '', deps: [], handoffFrom: ''});
    });
  }

  // Assign layers using longest path from sources
  var layers = {};
  var visited = {};
  function getLayer(id) {
    if (visited[id]) return layers[id] || 0;
    visited[id] = true;
    var deps = depMap[id] || [];
    var maxDepLayer = -1;
    deps.forEach(function(d) { maxDepLayer = Math.max(maxDepLayer, getLayer(d)); });
    layers[id] = maxDepLayer + 1;
    return layers[id];
  }
  steps.forEach(function(s) { getLayer(s.id); });

  // Group by layer
  var layerGroups = {};
  var maxLayer = 0;
  steps.forEach(function(s) {
    var l = layers[s.id] || 0;
    if (!layerGroups[l]) layerGroups[l] = [];
    layerGroups[l].push(s);
    maxLayer = Math.max(maxLayer, l);
  });

  // Position nodes
  var nodeW = 140, nodeH = 50, gapX = 60, gapY = 30, padX = 40, padY = 30;
  var nodes = {};
  for (var l = 0; l <= maxLayer; l++) {
    var group = layerGroups[l] || [];
    for (var i = 0; i < group.length; i++) {
      nodes[group[i].id] = {
        x: padX + l * (nodeW + gapX),
        y: padY + i * (nodeH + gapY),
        w: nodeW, h: nodeH,
        step: group[i]
      };
    }
  }

  // Build edges
  var edges = [];
  var handoffSet = {};
  steps.forEach(function(s) {
    if (s.handoffFrom && nodes[s.handoffFrom] && nodes[s.id]) {
      handoffSet[s.handoffFrom + '->' + s.id] = true;
    }
    (s.deps || []).forEach(function(dep) {
      if (nodes[dep] && nodes[s.id]) {
        edges.push({from: dep, to: s.id, isHandoff: handoffSet[dep + '->' + s.id] || (s.handoffFrom === dep)});
      }
    });
  });

  // Calculate SVG size
  var svgW = padX * 2 + (maxLayer + 1) * (nodeW + gapX);
  var maxNodesInLayer = 0;
  for (var ll = 0; ll <= maxLayer; ll++) {
    maxNodesInLayer = Math.max(maxNodesInLayer, (layerGroups[ll] || []).length);
  }
  var svgH = padY * 2 + maxNodesInLayer * (nodeH + gapY);

  return {nodes: nodes, edges: edges, width: Math.max(svgW, 300), height: Math.max(svgH, 150)};
}

// Render DAG as SVG
function renderWfDAG(run, wfDef) {
  var svg = document.getElementById('wf-dag-svg');
  var layout = layoutDAG(run.stepResults || {}, wfDef);

  svg.setAttribute('width', layout.width);
  svg.setAttribute('height', layout.height);
  svg.setAttribute('viewBox', '0 0 ' + layout.width + ' ' + layout.height);

  var html = '<defs>';
  html += '<marker id="wf-arrow" viewBox="0 0 10 7" refX="10" refY="3.5" markerWidth="8" markerHeight="6" orient="auto-start-reverse"><polygon points="0 0, 10 3.5, 0 7" fill="var(--muted)"/></marker>';
  html += '<marker id="wf-arrow-accent" viewBox="0 0 10 7" refX="10" refY="3.5" markerWidth="8" markerHeight="6" orient="auto-start-reverse"><polygon points="0 0, 10 3.5, 0 7" fill="var(--accent2)"/></marker>';
  html += '</defs>';

  // Draw edges first (behind nodes)
  layout.edges.forEach(function(e) {
    var from = layout.nodes[e.from];
    var to = layout.nodes[e.to];
    if (!from || !to) return;
    var x1 = from.x + from.w;
    var y1 = from.y + from.h / 2;
    var x2 = to.x;
    var y2 = to.y + to.h / 2;
    var cx = (x1 + x2) / 2;
    var cls = 'wf-edge' + (e.isHandoff ? ' handoff' : '');
    var marker = e.isHandoff ? 'url(#wf-arrow-accent)' : 'url(#wf-arrow)';
    html += '<path class="' + cls + '" d="M' + x1 + ' ' + y1 + ' C' + cx + ' ' + y1 + ' ' + cx + ' ' + y2 + ' ' + x2 + ' ' + y2 + '" marker-end="' + marker + '"/>';
  });

  // Draw nodes
  var nodeIds = Object.keys(layout.nodes);
  nodeIds.forEach(function(id) {
    var n = layout.nodes[id];
    var sr = (run.stepResults || {})[id] || {};
    var status = sr.status || 'pending';
    var label = id.length > 16 ? id.substring(0, 14) + '..' : id;
    var role = n.step.agent || '';
    var roleLabel = role.length > 14 ? role.substring(0, 12) + '..' : role;

    html += '<g class="wf-node ' + status + '" onclick="showWfNodeDetail(\'' + escAttr(id) + '\')" id="wf-node-' + escAttr(id) + '">';
    html += '<rect x="' + n.x + '" y="' + n.y + '" width="' + n.w + '" height="' + n.h + '"/>';
    html += '<text x="' + (n.x + n.w/2) + '" y="' + (n.y + (roleLabel ? 20 : 28)) + '" text-anchor="middle" font-weight="600">' + esc(label) + '</text>';
    if (roleLabel) {
      html += '<text x="' + (n.x + n.w/2) + '" y="' + (n.y + 35) + '" text-anchor="middle" font-size="9" fill="var(--muted)">' + esc(roleLabel) + '</text>';
    }
    html += '</g>';
  });

  svg.innerHTML = html;
}

// Render timeline bar
function renderWfTimeline(run) {
  var el = document.getElementById('wf-timeline');
  if (!run.stepResults) { el.innerHTML = ''; return; }

  var steps = [];
  var keys = Object.keys(run.stepResults);
  keys.forEach(function(k) { steps.push(run.stepResults[k]); });
  if (steps.length === 0) { el.innerHTML = ''; return; }

  var totalDur = run.durationMs || 1;
  var html = '';
  steps.forEach(function(sr) {
    var pct = Math.max(2, ((sr.durationMs || 1) / totalDur) * 100);
    var status = sr.status || 'pending';
    var title = (sr.stepId || '') + ': ' + status + ' (' + (sr.durationMs || 0) + 'ms)';
    html += '<div class="wf-timeline-bar ' + status + '" style="width:' + pct + '%" title="' + escAttr(title) + '"></div>';
  });
  el.innerHTML = html;
}

// Show node detail panel
function showWfNodeDetail(stepId) {
  var panel = document.getElementById('wf-node-detail');
  if (!currentWfRunData) { panel.style.display = 'none'; return; }
  var runObj = currentWfRunData.run || currentWfRunData;
  if (!runObj.stepResults) { panel.style.display = 'none'; return; }
  var sr = runObj.stepResults[stepId];
  if (!sr) { panel.style.display = 'none'; return; }

  var statusCls = sr.status === 'success' ? 'badge-ok' : (sr.status === 'error' || sr.status === 'timeout') ? 'badge-err' : sr.status === 'waiting' ? 'badge-info' : 'badge-warn';
  var html = '<h4>' + esc(stepId) + ' <span class="badge ' + statusCls + '">' + esc(sr.status || '') + '</span></h4>';
  html += '<div class="wf-detail-grid">';
  var detailDur = sr.durationMs ? sr.durationMs + 'ms' : '0ms';
  if (sr.status === 'running' && sr.startedAt && !sr.durationMs) {
    detailDur = Math.round((Date.now() - new Date(sr.startedAt).getTime()) / 1000) + 's (running)';
  }
  html += '<div class="wf-detail-item"><div class="label">Duration</div><div class="value">' + detailDur + '</div></div>';
  html += '<div class="wf-detail-item"><div class="label">Cost</div><div class="value">$' + (sr.costUsd != null ? sr.costUsd.toFixed(4) : '0.0000') + '</div></div>';
  if (sr.startedAt) html += '<div class="wf-detail-item"><div class="label">Started</div><div class="value" style="font-size:11px">' + esc(sr.startedAt.substring(0, 19).replace('T', ' ')) + '</div></div>';
  if (sr.finishedAt) html += '<div class="wf-detail-item"><div class="label">Finished</div><div class="value" style="font-size:11px">' + esc(sr.finishedAt.substring(0, 19).replace('T', ' ')) + '</div></div>';
  if (sr.retries) html += '<div class="wf-detail-item"><div class="label">Retries</div><div class="value">' + sr.retries + '</div></div>';
  if (sr.taskId) html += '<div class="wf-detail-item"><div class="label">Task ID</div><div class="value" style="font-size:11px"><code>' + esc(sr.taskId.substring(0, 8)) + '</code></div></div>';
  if (sr.sessionId) html += '<div class="wf-detail-item"><div class="label">Session</div><div class="value" style="font-size:11px"><code>' + esc(sr.sessionId.substring(0, 8)) + '</code></div></div>';
  html += '</div>';
  if (sr.error) html += '<div class="wf-detail-output" style="color:#f87171;margin-bottom:8px"><strong>Error:</strong> ' + esc(sr.error) + '</div>';
  if (sr.output) {
    var output = sr.output.length > 2000 ? sr.output.substring(0, 2000) + '...' : sr.output;
    html += '<div class="wf-detail-output">' + esc(output) + '</div>';
  }

  // Manual resolve button for waiting external steps.
  if (sr.status === 'waiting') {
    html += '<div style="margin-top:12px;padding-top:12px;border-top:1px solid var(--border)">';
    html += '<div style="font-size:11px;color:var(--muted);margin-bottom:8px">This step is waiting for an external callback.</div>';
    // Show callback info if available
    var callbacks = (currentWfRunData && currentWfRunData.callbacks) || [];
    var cb = callbacks.find(function(c) { return c.step_id === stepId; });
    if (cb) {
      var cbUrl = location.origin + '/api/callbacks/' + encodeURIComponent(cb.key);
      html += '<div style="font-size:11px;margin-bottom:8px">';
      html += '<div>Callback Key: <code style="cursor:pointer" onclick="copyText(\'' + cbUrl.replace(/'/g, "\\'") + '\')" title="Click to copy URL">' + esc(cb.key) + '</code></div>';
      html += '<div style="margin-top:2px">Mode: ' + esc(cb.mode || 'single') + ' \u00b7 Auth: ' + esc(cb.auth_mode || 'bearer') + '</div>';
      if (cb.timeout_at) {
        var timeoutAt = new Date(cb.timeout_at);
        var remaining = Math.max(0, Math.floor((timeoutAt - Date.now()) / 1000));
        if (remaining > 0) {
          var mins = Math.floor(remaining / 60);
          var secs = remaining % 60;
          html += '<div style="color:#fbbf24;margin-top:2px">Timeout: ' + mins + 'm ' + secs + 's remaining</div>';
        } else {
          html += '<div style="color:#f87171;margin-top:2px">Timeout expired</div>';
        }
      }
      html += '</div>';
    }
    html += '<textarea id="wf-resolve-body" rows="3" class="wfed-prop-input wfed-prop-textarea" placeholder=\'{"status":"success","data":{}}\' style="width:100%;box-sizing:border-box;margin-bottom:8px"></textarea>';
    html += '<button class="btn" style="font-size:12px" onclick="manualResolveCallback(\'' + escAttr(stepId) + '\')">Resolve Manually</button>';
    html += '</div>';
  }

  // Cancel button for running/waiting workflows.
  var _cancelRunObj = currentWfRunData && (currentWfRunData.run || currentWfRunData);
  if (_cancelRunObj && (_cancelRunObj.status === 'running' || _cancelRunObj.status === 'waiting')) {
    html += '<div style="margin-top:8px">';
    html += '<button class="btn btn-danger" style="font-size:11px" onclick="cancelWorkflowRun()">Cancel Workflow</button>';
    html += '</div>';
  }

  // Resume button for failed/cancelled/timeout workflows.
  if (_cancelRunObj && (_cancelRunObj.status === 'error' || _cancelRunObj.status === 'cancelled' || _cancelRunObj.status === 'timeout')) {
    html += '<div style="margin-top:8px">';
    html += '<button class="btn" style="font-size:11px;background:var(--accent);color:#fff" onclick="resumeWorkflowRun()">Resume from Checkpoint</button>';
    html += '</div>';
  }

  // Show resumedFrom link if this run was resumed from another.
  if (_cancelRunObj && _cancelRunObj.resumedFrom) {
    html += '<div style="margin-top:8px;font-size:11px;color:var(--muted)">';
    html += 'Resumed from <a href="#" onclick="openWfRun(\'' + escAttr(_cancelRunObj.resumedFrom) + '\');return false" style="color:var(--accent)">' + esc(_cancelRunObj.resumedFrom.substring(0, 8)) + '...</a>';
    html += '</div>';
  }

  panel.innerHTML = html;
  panel.style.display = '';
  panel.scrollIntoView({behavior: 'smooth', block: 'nearest'});
}

// Manual resolve: send callback for a waiting external step.
async function manualResolveCallback(stepId) {
  if (!currentWfRunData) return;
  var _manualRunObj = currentWfRunData.run || currentWfRunData;
  var runId = _manualRunObj.id;

  // Find the callback key from pending callbacks API.
  try {
    var resp = await fetchJSON('/api/callbacks');
    var callbacks = (resp && resp.callbacks) || [];
    var cb = callbacks.find(function(c) {
      return c.run_id === runId && c.step_id === stepId;
    });
    if (!cb) {
      toast('No pending callback found for this step');
      return;
    }

    var bodyEl = document.getElementById('wf-resolve-body');
    var body = bodyEl ? bodyEl.value.trim() : '{}';
    if (!body) body = '{}';

    await fetchJSON('/api/callbacks/' + encodeURIComponent(cb.key), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body,
    });
    toast('Callback resolved for ' + stepId);
    // Refresh after a short delay.
    setTimeout(function() {
      if (currentWfRun) openWfRun(currentWfRun);
    }, 800);
  } catch(e) {
    toast('Resolve failed: ' + (e.message || e));
  }
}

// Cancel a running/waiting workflow.
async function cancelWorkflowRun() {
  if (!currentWfRunData) return;
  var _cancelRun = currentWfRunData.run || currentWfRunData;
  var runId = _cancelRun.id;
  if (!confirm('Cancel workflow run ' + runId.substring(0, 8) + '...?')) return;
  try {
    await fetchJSON('/workflow-runs/' + encodeURIComponent(runId) + '/cancel', {
      method: 'POST',
    });
    toast('Workflow cancelled');
    setTimeout(function() {
      refreshWorkflowRuns();
      if (currentWfRun === runId) openWfRun(runId);
    }, 800);
  } catch(e) {
    toast('Cancel failed: ' + (e.message || e));
  }
}

// Resume a failed/cancelled/timeout workflow from checkpoint.
async function resumeWorkflowRun() {
  if (!currentWfRunData) return;
  var _resumeRun = currentWfRunData.run || currentWfRunData;
  var runId = _resumeRun.id;
  if (!confirm('Resume workflow run ' + runId.substring(0, 8) + '... from checkpoint?')) return;
  try {
    await fetchJSON('/workflow-runs/' + encodeURIComponent(runId) + '/resume', {
      method: 'POST',
    });
    toast('Workflow resume started');
    setTimeout(function() {
      refreshWorkflowRuns();
    }, 1500);
  } catch(e) {
    toast('Resume failed: ' + (e.message || e));
  }
}

// SSE subscription for live workflow updates
function subscribeWfSSE(runId) {
  if (wfSSE) { wfSSE.close(); }
  var url = '/dispatch/workflow:' + runId + '/stream';
  wfSSE = new EventSource(url);
  wfSSE.onmessage = function(e) {
    try {
      var ev = JSON.parse(e.data);
      if (ev.type === 'step_started' && ev.data) {
        updateWfNodeStatus(ev.data.stepId, 'running');
        updateWfStepRow(ev.data.stepId, 'running');
      }
      if (ev.type === 'step_waiting' && ev.data) {
        updateWfNodeStatus(ev.data.stepId, 'waiting');
        updateWfStepRow(ev.data.stepId, 'waiting');
      }
      if (ev.type === 'step_callback_received' && ev.data) {
        // Keep node in waiting state but could update a counter
        updateWfNodeStatus(ev.data.stepId, 'waiting');
        updateWfStepRow(ev.data.stepId, 'waiting');
      }
      if (ev.type === 'output_chunk' && ev.data && ev.data.chunk) {
        appendWfStepOutput(ev);
      }
      if (ev.type === 'step_completed' && ev.data) {
        updateWfNodeStatus(ev.data.stepId, ev.data.status);
        updateWfStepRow(ev.data.stepId, ev.data.status);
      }
      if (ev.type === 'workflow_completed') {
        if (wfSSE) { wfSSE.close(); wfSSE = null; }
        // Refresh after a short delay to let DB settle
        setTimeout(function() {
          refreshWorkflowRuns();
          if (currentWfRun === runId) openWfRun(runId);
        }, 500);
      }
    } catch(err) { /* ignore parse errors */ }
  };
  wfSSE.onerror = function() {
    if (wfSSE) { wfSSE.close(); wfSSE = null; }
  };
}

function updateWfStepRow(stepId, status) {
  var row = document.getElementById('wf-step-row-' + stepId);
  if (!row) return;
  // Update badge
  var badge = row.querySelector('.badge');
  if (badge) {
    var cls = status === 'success' ? 'badge-ok' : (status === 'error' || status === 'timeout') ? 'badge-err' : 'badge-warn';
    badge.className = 'badge ' + cls;
    badge.textContent = status;
  }
  // Update pulse indicator
  var indicator = row.querySelector('span[style*="border-radius:50%"]');
  if (indicator) {
    if (status === 'running') {
      indicator.style.background = '#fbbf24';
      indicator.style.display = 'inline-block';
    } else if (status === 'waiting') {
      indicator.style.background = '#a78bfa';
      indicator.style.display = 'inline-block';
    } else {
      indicator.style.display = 'none';
    }
  }
}

function updateWfNodeStatus(stepId, status) {
  var node = document.getElementById('wf-node-' + stepId);
  if (node) {
    node.setAttribute('class', 'wf-node ' + status);
  }
}

// Append streaming output_chunk to the running step's stream container.
function appendWfStepOutput(ev) {
  // Find which step is running by matching taskId from the run data.
  var runObj = currentWfRunData && (currentWfRunData.run || currentWfRunData);
  if (!runObj || !runObj.stepResults) return;

  var targetStepId = null;
  for (var sid in runObj.stepResults) {
    var sr = runObj.stepResults[sid];
    if (sr.status === 'running' && sr.taskId === ev.taskId) {
      targetStepId = sid;
      break;
    }
  }
  // Fallback: just find any running step.
  if (!targetStepId) {
    for (var sid in runObj.stepResults) {
      if (runObj.stepResults[sid].status === 'running') {
        targetStepId = sid;
        break;
      }
    }
  }
  if (!targetStepId) return;

  var container = document.getElementById('wf-stream-' + targetStepId);
  if (!container) return;

  // Clear the initial "Streaming..." placeholder on first chunk.
  if (container.dataset.started !== '1') {
    container.textContent = '';
    container.dataset.started = '1';
    // Auto-expand the step detail.
    var row = document.getElementById('wf-step-row-' + targetStepId);
    if (row) {
      var detail = row.querySelector('.step-detail');
      if (detail) detail.style.display = '';
    }
  }

  var chunk = ev.data && ev.data.chunk ? ev.data.chunk : '';
  container.textContent += chunk;
  // Auto-scroll to bottom.
  container.scrollTop = container.scrollHeight;
}

function escAttr(s) {
  return String(s).replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/'/g,'&#39;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

// --- End Workflow Visualization ---
