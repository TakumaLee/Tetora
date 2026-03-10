// --- History Trends ---

let historyPeriodDays = 14;

function setHistoryPeriod(days) {
  historyPeriodDays = days;
  document.querySelectorAll('#ht-btn-7,#ht-btn-14,#ht-btn-30').forEach(b => b.classList.remove('btn-period-active'));
  const btn = document.getElementById(`ht-btn-${days}`);
  if (btn) btn.classList.add('btn-period-active');
  refreshHistoryTrends();
}

function makeSVGLineChart(data, series, opts) {
  opts = opts || {};
  const W = 500, H = 110;
  const PL = 38, PR = 8, PT = 8, PB = 22;
  const cW = W - PL - PR, cH = H - PT - PB;

  if (!data || data.length === 0) {
    return `<svg viewBox="0 0 ${W} ${H}" style="width:100%;height:auto"><text x="${W/2}" y="${H/2}" fill="#6b6b80" font-size="11" text-anchor="middle">No data</text></svg>`;
  }

  const allVals = series.reduce((acc, s) => acc.concat(data.map(d => typeof d[s.key] === 'number' ? d[s.key] : 0)), []);
  const maxVal = Math.max.apply(null, allVals.concat([0.0001]));

  const n = data.length;
  const xStep = n > 1 ? cW / (n - 1) : 0;

  const gridLines = [0.25, 0.5, 0.75, 1].map(pct => {
    const y = (PT + cH - pct * cH).toFixed(1);
    const val = maxVal * pct;
    const label = opts.formatVal ? opts.formatVal(val) : (val < 1 ? val.toFixed(3) : Math.round(val).toString());
    return `<line x1="${PL}" y1="${y}" x2="${PL+cW}" y2="${y}" stroke="#1e1e2e" stroke-width="1"/><text x="${PL-3}" y="${(parseFloat(y)+3.5).toFixed(1)}" font-size="8" fill="#6b6b80" text-anchor="end">${label}</text>`;
  }).join('');

  const lines = series.map(s => {
    const pts = data.map((d, i) => {
      const x = (PL + i * xStep).toFixed(1);
      const v = typeof d[s.key] === 'number' ? d[s.key] : 0;
      const y = (PT + cH - (v / maxVal) * cH).toFixed(1);
      return x + ',' + y;
    }).join(' ');
    return `<polyline points="${pts}" fill="none" stroke="${s.color}" stroke-width="1.8" stroke-linejoin="round" stroke-linecap="round"/>`;
  }).join('');

  const dots = series.map(s => {
    const d = data[data.length - 1];
    const v = typeof d[s.key] === 'number' ? d[s.key] : 0;
    const x = (PL + (n - 1) * xStep).toFixed(1);
    const y = (PT + cH - (v / maxVal) * cH).toFixed(1);
    return `<circle cx="${x}" cy="${y}" r="2.5" fill="${s.color}"/>`;
  }).join('');

  const skip = Math.max(1, Math.ceil(n / 7));
  const xLabels = data.map((d, i) => {
    if (i % skip !== 0 && i !== n - 1) return '';
    const x = (PL + i * xStep).toFixed(1);
    const lbl = esc((d.date || d.Date || '').slice(5));
    return `<text x="${x}" y="${H - 4}" font-size="8" fill="#6b6b80" text-anchor="middle">${lbl}</text>`;
  }).join('');

  const axes = `<line x1="${PL}" y1="${PT}" x2="${PL}" y2="${PT+cH}" stroke="#2a2a3e" stroke-width="1"/><line x1="${PL}" y1="${PT+cH}" x2="${PL+cW}" y2="${PT+cH}" stroke="#2a2a3e" stroke-width="1"/>`;

  return `<svg viewBox="0 0 ${W} ${H}" style="width:100%;height:auto" overflow="visible">${gridLines}${axes}${lines}${dots}${xLabels}</svg>`;
}

async function refreshHistoryTrends() {
  const days = historyPeriodDays;
  const section = document.getElementById('history-trends-section');
  section.style.display = '';

  document.querySelectorAll('#ht-btn-7,#ht-btn-14,#ht-btn-30').forEach(b => b.classList.remove('btn-period-active'));
  const ab = document.getElementById('ht-btn-' + days);
  if (ab) ab.classList.add('btn-period-active');

  const [taskData, usageData] = await Promise.all([
    fetchJSON('/api/tasks/trend?days=' + days).catch(() => []),
    fetchJSON('/api/usage/trend?days=' + days).catch(() => [])
  ]);

  const taskArr = Array.isArray(taskData) ? taskData : [];
  const usageArr = Array.isArray(usageData) ? usageData : [];

  document.getElementById('ht-task-chart').innerHTML = makeSVGLineChart(
    taskArr,
    [
      { key: 'created', color: 'var(--accent2)' },
      { key: 'done', color: 'var(--green)' }
    ]
  );

  document.getElementById('ht-usage-chart').innerHTML = makeSVGLineChart(
    usageArr,
    [{ key: 'costUsd', color: 'var(--accent)' }],
    { formatVal: function(v) { return '$' + v.toFixed(v < 0.01 ? 4 : 2); } }
  );
}

// ====== Quest Log View ======
async function refreshQuestLog() {
  try {
    var data = await fetchJSON('/api/tasks/board');
    var cols = data.columns || {};
    var sections = [
      { title: 'In Progress', keys: ['doing'], cls: 'quest-doing' },
      { title: 'Under Review', keys: ['review'], cls: 'quest-review' },
      { title: 'Commissions Accepted', keys: ['todo', 'backlog', 'needs-thought', 'idea'], cls: '' },
      { title: 'Completed (Recent 20)', keys: ['done'], cls: 'quest-done', limit: 20 },
      { title: 'Failed', keys: ['failed'], cls: 'quest-failed' },
    ];
    var html = '';
    sections.forEach(function(sec) {
      var tasks = [];
      sec.keys.forEach(function(k) {
        (cols[k] || []).forEach(function(t) {
          t._status = k;
          tasks.push(t);
        });
      });
      if (sec.limit) tasks = tasks.slice(0, sec.limit);
      if (tasks.length === 0) return;
      html += '<div class="quest-section-title">' + esc(sec.title) + ' (' + tasks.length + ')</div>';
      tasks.forEach(function(t) {
        var statusCls = sec.cls || ('quest-' + t._status);
        var rank = questRank(t.priority);
        var meta = [];
        if (t.project) meta.push('<span class="quest-project">' + esc(t.project) + '</span>');
        if (t.assignee) meta.push('<span class="quest-agent">' + esc(typeof t.assignee === 'object' ? t.assignee.name : t.assignee) + '</span>');
        if (t.cost > 0) meta.push('<span class="quest-cost">' + costFmt(t.cost) + '</span>');
        if (t.createdAt) meta.push('<span>' + dateTimeStr(t.createdAt) + '</span>');
        html += '<div class="quest-card ' + statusCls + '" onclick="openTaskDetail(\'' + esc(t.id) + '\')">' +
          '<div class="quest-card-header">' +
            '<span class="quest-card-title">' + esc(t.title || t.name || t.id) + '</span>' +
            '<span class="quest-rank ' + rank.cls + '">' + rank.label + '</span>' +
          '</div>' +
          '<div class="quest-card-meta">' + meta.join('') + '</div>' +
        '</div>';
      });
    });
    document.getElementById('quest-board').innerHTML = html || '<div style="color:var(--muted);padding:20px;text-align:center">No quests found</div>';
  } catch(e) {
    document.getElementById('quest-board').innerHTML = '<div style="color:var(--red);padding:20px">Error: ' + esc(e.message) + '</div>';
  }
}

function questRank(priority) {
  switch((priority || '').toLowerCase()) {
    case 'urgent': return { label: 'S Rank', cls: 'quest-rank-s' };
    case 'high': return { label: 'A Rank', cls: 'quest-rank-a' };
    case 'normal': case '': case undefined: return { label: 'B Rank', cls: 'quest-rank-b' };
    default: return { label: 'C Rank', cls: 'quest-rank-c' };
  }
}

// ====== Calendar View ======
function calendarToday() {
  var now = new Date();
  calendarYear = now.getFullYear();
  calendarMonth = now.getMonth();
  refreshCalendarView();
}

function calendarPrev() {
  calendarMonth--;
  if (calendarMonth < 0) { calendarMonth = 11; calendarYear--; }
  refreshCalendarView();
}

function calendarNext() {
  calendarMonth++;
  if (calendarMonth > 11) { calendarMonth = 0; calendarYear++; }
  refreshCalendarView();
}

async function refreshCalendarView() {
  var titleEl = document.getElementById('cal-title');
  var months = ['January','February','March','April','May','June','July','August','September','October','November','December'];
  titleEl.textContent = months[calendarMonth] + ' ' + calendarYear;

  // Calculate days range for API
  var firstDay = new Date(calendarYear, calendarMonth, 1);
  var lastDay = new Date(calendarYear, calendarMonth + 1, 0);
  var daysInMonth = lastDay.getDate();
  // Pad to cover full API range
  var apiDays = daysInMonth + 14; // extra buffer

  try {
    var [taskData, usageData, sessData] = await Promise.all([
      fetchJSON('/api/tasks/trend?days=' + apiDays).catch(function() { return []; }),
      fetchJSON('/api/usage/trend?days=' + apiDays).catch(function() { return []; }),
      fetchJSON('/sessions?limit=200').catch(function() { return { sessions: [] }; }),
    ]);

    // Build daily map from API data
    var dayMap = {};
    (Array.isArray(taskData) ? taskData : []).forEach(function(d) {
      var key = d.date || d.Date;
      if (!key) return;
      if (!dayMap[key]) dayMap[key] = { tasks: 0, done: 0, cost: 0, sessions: 0 };
      dayMap[key].tasks += (d.created || 0);
      dayMap[key].done += (d.done || 0);
    });
    (Array.isArray(usageData) ? usageData : []).forEach(function(d) {
      var key = d.date || d.Date;
      if (!key) return;
      if (!dayMap[key]) dayMap[key] = { tasks: 0, done: 0, cost: 0, sessions: 0 };
      dayMap[key].cost += (d.costUsd || 0);
    });
    var sessions = (sessData && sessData.sessions) || [];
    sessions.forEach(function(s) {
      var key = (s.startedAt || s.createdAt || '').slice(0, 10);
      if (!key) return;
      if (!dayMap[key]) dayMap[key] = { tasks: 0, done: 0, cost: 0, sessions: 0 };
      dayMap[key].sessions++;
    });

    renderCalendarGrid(dayMap);
  } catch(e) {
    document.getElementById('calendar-grid').innerHTML = '<div style="color:var(--red);padding:20px;grid-column:1/-1">Error: ' + esc(e.message) + '</div>';
  }
}

function renderCalendarGrid(dayMap) {
  var grid = document.getElementById('calendar-grid');
  var today = new Date();
  var todayStr = today.getFullYear() + '-' + String(today.getMonth() + 1).padStart(2, '0') + '-' + String(today.getDate()).padStart(2, '0');

  var firstDay = new Date(calendarYear, calendarMonth, 1);
  var startDow = firstDay.getDay(); // 0=Sun
  var daysInMonth = new Date(calendarYear, calendarMonth + 1, 0).getDate();
  var daysInPrev = new Date(calendarYear, calendarMonth, 0).getDate();

  var html = '';

  // Previous month filler
  for (var i = startDow - 1; i >= 0; i--) {
    var d = daysInPrev - i;
    html += '<div class="calendar-cell other-month"><div class="cal-date">' + d + '</div></div>';
  }

  // Current month
  for (var d = 1; d <= daysInMonth; d++) {
    var dateStr = calendarYear + '-' + String(calendarMonth + 1).padStart(2, '0') + '-' + String(d).padStart(2, '0');
    var isToday = dateStr === todayStr;
    var data = dayMap[dateStr] || { tasks: 0, done: 0, cost: 0, sessions: 0 };
    var statsHtml = '';
    if (data.done > 0) statsHtml += '<div class="cal-done">' + data.done + ' done</div>';
    if (data.tasks > 0 && data.tasks !== data.done) statsHtml += '<div>' + data.tasks + ' created</div>';
    if (data.sessions > 0) statsHtml += '<div class="cal-sessions">' + data.sessions + ' sessions</div>';
    if (data.cost > 0) statsHtml += '<div class="cal-cost">' + costFmt(data.cost) + '</div>';

    html += '<div class="calendar-cell' + (isToday ? ' today' : '') + '" onclick="showCalendarDayDetail(\'' + dateStr + '\')">' +
      '<div class="cal-date">' + d + '</div>' +
      '<div class="cal-stats">' + statsHtml + '</div>' +
    '</div>';
  }

  // Next month filler (fill to complete row)
  var totalCells = startDow + daysInMonth;
  var remaining = totalCells % 7 === 0 ? 0 : 7 - (totalCells % 7);
  for (var i = 1; i <= remaining; i++) {
    html += '<div class="calendar-cell other-month"><div class="cal-date">' + i + '</div></div>';
  }

  grid.innerHTML = html;
}

function showCalendarDayDetail(dateStr) {
  var detail = document.getElementById('calendar-day-detail');
  var parts = dateStr.split('-');
  var label = parts[1] + '/' + parts[2] + '/' + parts[0];
  detail.style.display = '';
  detail.innerHTML = '<h3>' + label + '</h3><div style="color:var(--muted);font-size:12px">Loading...</div>';

  fetchJSON('/sessions?limit=50&date=' + dateStr).catch(function() { return { sessions: [] }; }).then(function(data) {
    var sessions = (data && data.sessions) || [];
    var html = '<h3>' + label + ' <button class="btn" onclick="document.getElementById(\'calendar-day-detail\').style.display=\'none\'" style="float:right;padding:2px 8px;font-size:11px">Close</button></h3>';
    if (sessions.length === 0) {
      html += '<div style="color:var(--muted);font-size:12px">No sessions on this day</div>';
    } else {
      html += '<div style="font-size:12px;color:var(--muted);margin-bottom:8px">' + sessions.length + ' sessions</div>';
      html += sessions.map(function(s) {
        var role = s.role || s.agent || '-';
        var cost = s.cost > 0 ? costFmt(s.cost) : '';
        var dur = s.duration || '';
        var status = s.status || 'done';
        return '<div style="padding:6px 8px;border-bottom:1px solid var(--border);font-size:12px">' +
          '<span style="color:var(--accent);font-weight:500">' + esc(typeof role === 'object' ? role.name : role) + '</span> ' +
          '<span style="color:var(--muted)">' + esc(dur) + '</span> ' +
          (cost ? '<span style="color:var(--green);font-family:var(--font-mono)">' + cost + '</span> ' : '') +
          statusBadge(status) +
          (s.task ? '<div style="margin-top:3px;color:var(--text);font-size:11px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:400px">' + esc(s.task) + '</div>' : '') +
        '</div>';
      }).join('');
    }
    detail.innerHTML = html;
  });
}

// ====== Stats Report View ======
function setStatsReportPeriod(days, btn) {
  statsReportDays = days;
  var filters = document.getElementById('stats-filters');
  filters.querySelectorAll('button').forEach(function(b) { b.classList.remove('active'); });
  if (btn) btn.classList.add('active');
  refreshStatsReport();
}

async function refreshStatsReport() {
  try {
    var [taskData, usageData, boardData] = await Promise.all([
      fetchJSON('/api/tasks/trend?days=' + statsReportDays).catch(function() { return []; }),
      fetchJSON('/api/usage/trend?days=' + statsReportDays).catch(function() { return []; }),
      fetchJSON('/api/tasks/board').catch(function() { return { columns: {}, stats: {}, projects: [], agents: [] }; }),
    ]);

    var taskArr = Array.isArray(taskData) ? taskData : [];
    var usageArr = Array.isArray(usageData) ? usageData : [];

    // Task completion trend
    document.getElementById('sr-task-chart').innerHTML = makeSVGLineChart(
      taskArr,
      [{ key: 'created', color: 'var(--accent2)' }, { key: 'done', color: 'var(--green)' }]
    );

    // Cost trend
    document.getElementById('sr-cost-chart').innerHTML = makeSVGLineChart(
      usageArr,
      [{ key: 'costUsd', color: 'var(--accent)' }],
      { formatVal: function(v) { return '$' + v.toFixed(v < 0.01 ? 4 : 2); } }
    );

    // Aggregate tasks by project for pie chart
    var projectCounts = {};
    var agentCounts = {};
    var cols = boardData.columns || {};
    ['idea','backlog','needs-thought','todo','doing','review','done','failed'].forEach(function(s) {
      (cols[s] || []).forEach(function(t) {
        var proj = t.project || 'Unassigned';
        projectCounts[proj] = (projectCounts[proj] || 0) + 1;
        var agent = t.assignee ? (typeof t.assignee === 'object' ? t.assignee.name : t.assignee) : 'Unassigned';
        agentCounts[agent] = (agentCounts[agent] || 0) + 1;
      });
    });

    // Render pie charts
    document.getElementById('sr-project-pie').innerHTML = makeSVGPieChart(projectCounts, 'Tasks by Project');
    document.getElementById('sr-agent-pie').innerHTML = makeSVGPieChart(agentCounts, 'Tasks by Agent');

  } catch(e) {
    document.getElementById('stats-report-grid').innerHTML = '<div style="color:var(--red);padding:20px;grid-column:1/-1">Error: ' + esc(e.message) + '</div>';
  }
}

function makeSVGPieChart(data) {
  var PIE_COLORS = ['#06b6d4','#8b5cf6','#34d399','#f59e0b','#f87171','#60a5fa','#ec4899','#a78bfa','#fbbf24','#14b8a6'];
  var entries = Object.keys(data).map(function(k) { return { label: k, value: data[k] }; });
  entries.sort(function(a, b) { return b.value - a.value; });

  var total = entries.reduce(function(s, e) { return s + e.value; }, 0);
  if (total === 0) {
    return '<div style="color:var(--muted);font-size:12px;padding:10px">No data</div>';
  }

  var R = 60, CX = 70, CY = 70;
  var svg = '<svg viewBox="0 0 140 140" style="width:140px;height:140px;flex-shrink:0">';
  var startAngle = -Math.PI / 2;

  entries.forEach(function(e, i) {
    var sliceAngle = (e.value / total) * 2 * Math.PI;
    var endAngle = startAngle + sliceAngle;
    var largeArc = sliceAngle > Math.PI ? 1 : 0;
    var x1 = CX + R * Math.cos(startAngle);
    var y1 = CY + R * Math.sin(startAngle);
    var x2 = CX + R * Math.cos(endAngle);
    var y2 = CY + R * Math.sin(endAngle);
    var color = PIE_COLORS[i % PIE_COLORS.length];

    if (entries.length === 1) {
      svg += '<circle cx="' + CX + '" cy="' + CY + '" r="' + R + '" fill="' + color + '"/>';
    } else {
      svg += '<path d="M ' + CX + ' ' + CY + ' L ' + x1.toFixed(2) + ' ' + y1.toFixed(2) + ' A ' + R + ' ' + R + ' 0 ' + largeArc + ' 1 ' + x2.toFixed(2) + ' ' + y2.toFixed(2) + ' Z" fill="' + color + '"/>';
    }
    startAngle = endAngle;
  });
  svg += '</svg>';

  var legend = '<div class="pie-legend">';
  entries.slice(0, 8).forEach(function(e, i) {
    var color = PIE_COLORS[i % PIE_COLORS.length];
    var pct = ((e.value / total) * 100).toFixed(0);
    legend += '<div class="pie-legend-item"><span class="pie-legend-dot" style="background:' + color + '"></span>' + esc(e.label) + '<span class="pie-legend-val">' + e.value + ' (' + pct + '%)</span></div>';
  });
  if (entries.length > 8) {
    legend += '<div class="pie-legend-item" style="color:var(--muted)">...and ' + (entries.length - 8) + ' more</div>';
  }
  legend += '</div>';

  return svg + legend;
}

