  // ===== War Room =====
  var _warRoomData = null;
  var _wrCurrentFrontId = null;

  var WR_CATEGORY_ICON = {
    finance:       '&#x1F4B0;',
    dev:           '&#x2699;&#xFE0F;',
    content:       '&#x1F3AC;',
    marketing:     '&#x1F4E2;',
    business:      '&#x1F4BC;',
    collaboration: '&#x1F91D;',
    planning:      '&#x1F3AF;',
    freelance:     '&#x1F4BB;',
    company:       '&#x1F3E2;'
  };

  var WR_TYPE_LABEL = {
    metrics:  '金融',
    strategy: '策略',
    collab:   '協作'
  };

  function wrStatusColor(status) {
    switch (status) {
      case 'green':  return 'var(--green)';
      case 'yellow': return 'var(--yellow)';
      case 'red':    return 'var(--red)';
      case 'paused': return '#9ca3af';
      default:       return '#6b7280';
    }
  }

  function wrStatusLabel(status) {
    switch (status) {
      case 'green':  return '運行中';
      case 'yellow': return '注意';
      case 'red':    return '阻塞';
      case 'paused': return '暫停';
      default:       return '未設定';
    }
  }

  function wrFormatTime(iso) {
    if (!iso) return '--';
    try {
      var d = new Date(iso);
      return d.toLocaleString('zh-TW', { month:'numeric', day:'numeric', hour:'2-digit', minute:'2-digit' });
    } catch(e) { return iso; }
  }

  // ── Connection status helpers ──────────────────────────────────
  function wrConnClass(val) {
    if (!val || val === 'unknown' || val === 'paused') return 'neutral';
    if (val === 'down') return 'warn';
    if (val === 'ok' || val === 'up') return 'ok';
    return 'neutral';
  }
  function wrConnLabel(val) {
    if (!val || val === 'unknown') return '—';
    if (val === 'down') return '&#x1F534; 斷線';
    if (val === 'ok' || val === 'up') return '&#x1F7E2; 正常';
    if (val === 'paused') return '&#x23F8; 暫停';
    return esc(val);
  }

  // ── Load ──────────────────────────────────────────────────────
  function loadWarRoom() {
    document.getElementById('wr-last-updated').textContent = '讀取中...';
    fetch('/api/workspace/file?path=memory/war-room/status.json')
      .then(function(r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function(data) {
        var parsed = data;
        if (typeof data === 'object' && data !== null && typeof data.content === 'string') {
          parsed = JSON.parse(data.content);
        }
        _warRoomData = parsed;
        renderWarRoom(parsed);
      })
      .catch(function(err) {
        document.getElementById('wr-grid').innerHTML =
          '<div class="wr-error">&#x26A0; 無法讀取 status.json：' + err.message + '</div>';
        document.getElementById('wr-last-updated').textContent = '讀取失敗';
      });
  }

  // ── Render grid ───────────────────────────────────────────────
  function renderWarRoom(data) {
    var allFronts = (data && Array.isArray(data.fronts)) ? data.fronts : [];
    var fronts = allFronts.filter(function(f) { return !f.archived; });
    var genAt  = (data && data.generated_at) ? data.generated_at : '';
    document.getElementById('wr-last-updated').textContent =
      genAt ? '上次更新：' + wrFormatTime(genAt) : '無更新紀錄';

    var grid = document.getElementById('wr-grid');
    if (fronts.length === 0) {
      grid.innerHTML = '<div class="wr-empty">尚無戰線資料。按「+ 新增戰線」開始。</div>';
      return;
    }
    grid.innerHTML = fronts.map(renderWarRoomCard).join('');
    loadAutoUpdateMeta();
  }

  // ── Per-type card dispatch ─────────────────────────────────────
  function renderWarRoomCard(front) {
    var type = front.card_type || 'strategy';
    if (type === 'metrics') return renderWrCardMetrics(front);
    if (type === 'collab')  return renderWrCardCollab(front);
    return renderWrCardStrategy(front);
  }

  // ── Shared card scaffold helpers ───────────────────────────────
  function _wrCardOpen(front, isStale) {
    var color = wrStatusColor(front.status);
    var dotPulse = front.status === 'red' ? ' pulse-red' : '';
    var typeLabel = WR_TYPE_LABEL[front.card_type] || front.card_type || '';
    return [
      '<div class="wr-card' + (isStale ? ' is-stale' : '') + '" id="wr-card-' + esc(front.id) + '"',
      '  role="button" tabindex="0" aria-label="' + escAttr(front.name || front.id) + ' 詳細"',
      '  onclick="openWrModal(\'' + esc(front.id) + '\')"',
      '  onkeydown="if(event.key===\'Enter\'||event.key===\' \'){event.preventDefault();openWrModal(\'' + esc(front.id) + '\')}"',
      '>',
      '  <div class="wr-card-head">',
      '    <div class="wr-status-dot' + dotPulse + '" style="background:' + color + ';color:' + color + '"></div>',
      '    <div class="wr-card-title">' + esc(front.name || front.id) + (front.auto ? ' &#x1F916;' : '') + '</div>',
      '    <div class="wr-type-badge">' + esc(typeLabel) + '</div>',
      '  </div>'
    ].join('\n');
  }

  function _wrCardBadges(front, isStale) {
    var parts = [];
    if (isStale) parts.push('<span class="wr-stale-badge">&#x23F0; 資訊已過期</span>');
    var mo = front.manual_override;
    if (mo && mo.active) {
      var moExpired = mo.expires_at && new Date(mo.expires_at) <= new Date();
      if (!moExpired) {
        var moExpStr = mo.expires_at ? wrFormatTime(mo.expires_at) : '無限期';
        parts.push(
          '<span class="wr-override-badge" role="button" tabindex="0"' +
          ' title="點擊清除覆蓋" onclick="event.stopPropagation();clearWrOverride(\'' + esc(front.id) + '\')"' +
          ' style="cursor:pointer">&#x1F512; 覆蓋中 到 ' + esc(moExpStr) + ' &#x2715;</span>'
        );
      }
    }
    if (parts.length === 0) return '';
    return '<div style="display:flex;gap:4px;flex-wrap:wrap;margin-bottom:8px">' + parts.join('') + '</div>';
  }

  function _wrDepWarning(front) {
    if (!Array.isArray(front.depends_on) || front.depends_on.length === 0) return '';
    if (!_warRoomData || !Array.isArray(_warRoomData.fronts)) return '';
    var redNames = [];
    front.depends_on.forEach(function(depId) {
      var dep = _warRoomData.fronts.find(function(f) { return f.id === depId; });
      if (dep && dep.status === 'red') redNames.push(esc(dep.name || dep.id));
    });
    if (redNames.length === 0) return '';
    return '<div class="wr-dep-warning">&#x26A0; 依賴 ' + redNames.join('、') + ' 異常</div>';
  }

  function _wrInlineEditForm(front) {
    if (front.auto) return '';
    return [
      '<div class="wr-edit-form" id="wr-edit-' + esc(front.id) + '" style="display:none" onclick="event.stopPropagation()">',
      '  <div><label>狀態</label>',
      '    <select id="wr-ef-status-' + esc(front.id) + '">',
      '      <option value="green"'  + (front.status==='green'  ? ' selected':'') + '>&#x1F7E2; 運行中</option>',
      '      <option value="yellow"' + (front.status==='yellow' ? ' selected':'') + '>&#x1F7E1; 注意</option>',
      '      <option value="red"'    + (front.status==='red'    ? ' selected':'') + '>&#x1F534; 阻塞</option>',
      '      <option value="paused"' + (front.status==='paused' ? ' selected':'') + '>&#x23F8; 暫停</option>',
      '      <option value="unknown"'+ (front.status==='unknown'? ' selected':'') + '>&#x26AA; 未設定</option>',
      '    </select>',
      '  </div>',
      '  <div><label>Summary</label>',
      '    <input type="text" id="wr-ef-summary-' + esc(front.id) + '" value="' + escAttr(front.summary||'') + '" placeholder="概況說明">',
      '  </div>',
      '  <div><label>Blocking</label>',
      '    <input type="text" id="wr-ef-blocking-' + esc(front.id) + '" value="' + escAttr(front.blocking||'') + '" placeholder="無">',
      '  </div>',
      '  <div><label>Next Action</label>',
      '    <input type="text" id="wr-ef-next-' + esc(front.id) + '" value="' + escAttr(front.next_action||'') + '" placeholder="無">',
      '  </div>',
      '  <button class="wr-save-btn" onclick="event.stopPropagation();saveWarRoomFront(\'' + esc(front.id) + '\')">儲存</button>',
      '</div>'
    ].join('\n');
  }

  function _wrCardActionBar(front) {
    var editBtn = !front.auto
      ? '<button class="wr-btn wr-btn-sec" onclick="event.stopPropagation();editWarRoomFront(\'' + esc(front.id) + '\')" title="快速編輯">&#x270F;&#xFE0F;</button>'
      : '';
    return [
      '<div class="wr-action-bar">',
      '  ' + _wrStatusCapsule(front),
      '  <button class="wr-btn wr-btn-primary" onclick="event.stopPropagation();openWrModal(\'' + esc(front.id) + '\',true)">&#x1F4E5; Add Intel</button>',
      '  ' + editBtn,
      '</div>'
    ].join('\n');
  }

  function _wrStatusCapsule(front) {
    var id = esc(front.id);
    var cur = front.status || '';
    function btn(st, icon, title) {
      var active = cur === st ? ' is-active' : '';
      return '<button class="wr-stat-btn' + active + '" title="' + title + '"' +
        ' onclick="event.stopPropagation();setWrStatus(\'' + id + '\',\'' + st + '\')">' +
        icon + '</button>';
    }
    return '<div class="wr-stat-capsule" onclick="event.stopPropagation()">' +
      btn('green',  '&#x1F7E2;', '運行中') +
      btn('yellow', '&#x1F7E1;', '注意') +
      btn('red',    '&#x1F534;', '阻塞') +
      btn('paused', '&#x23F8;&#xFE0F;', '暫停') +
      '</div>';
  }

  function _wrCardFooter(front) {
    return '<div class="wr-card-footer"><span>更新: ' + wrFormatTime(front.last_updated) + '</span></div>';
  }

  // ── Card type: metrics ─────────────────────────────────────────
  function renderWrCardMetrics(front) {
    var isStale = _wrIsStale(front);
    var m = front.metrics || {};

    // paper_days cell
    var pdVal   = (m.paper_days != null) ? m.paper_days + '天' : '—';
    var pdClass = (m.paper_days != null && m.paper_days > 0) ? 'warn' : 'neutral';
    var pdLabel = (m.paper_days != null && m.paper_days > 0) ? '0 交易' : '紙上天數';

    // win_rate cell
    var wrVal   = (m.win_rate != null) ? (m.win_rate * 100).toFixed(0) + '%' : '—';
    var wrClass = (m.win_rate != null) ? (m.win_rate >= 0.5 ? 'ok' : 'warn') : 'neutral';

    // connection_status cell
    var connHtml  = wrConnLabel(m.connection_status || '');
    var connClass = wrConnClass(m.connection_status || '');

    // active_hypo cell
    var hypoVal   = (m.active_hypo_count != null) ? String(m.active_hypo_count) : '0';
    var hypoClass = (m.active_hypo_count > 0) ? 'caution' : 'neutral';

    // summary strip: show only if red or yellow
    var stripHtml = '';
    var s = front.status;
    if ((s === 'red' || s === 'yellow') && front.summary) {
      stripHtml = '<div class="wr-summary-strip ' + (s === 'yellow' ? 'yellow' : '') + '">' + esc(front.summary) + '</div>';
    }

    return [
      _wrCardOpen(front, isStale),
      _wrCardBadges(front, isStale),
      '  <div class="wr-metrics-row">',
      '    <div class="wr-metric"><div class="wr-metric-value ' + pdClass + '">' + pdVal + '</div><div class="wr-metric-label">' + pdLabel + '</div></div>',
      '    <div class="wr-metric"><div class="wr-metric-value ' + wrClass + '">' + wrVal + '</div><div class="wr-metric-label">勝率</div></div>',
      '    <div class="wr-metric"><div class="wr-metric-value ' + connClass + '">' + connHtml + '</div><div class="wr-metric-label">連線</div></div>',
      '    <div class="wr-metric"><div class="wr-metric-value ' + hypoClass + '">' + esc(hypoVal) + '</div><div class="wr-metric-label">HYPO</div></div>',
      '  </div>',
      stripHtml,
      _wrCardActionBar(front),
      _wrCardFooter(front),
      _wrDepWarning(front),
      _wrInlineEditForm(front),
      '</div>'
    ].join('\n');
  }

  // ── Card type: strategy ────────────────────────────────────────
  function renderWrCardStrategy(front) {
    var isStale = _wrIsStale(front);
    var summaryHtml = '';
    if (front.summary) {
      var s = front.status;
      if (s === 'red' || s === 'yellow') {
        summaryHtml = '<div class="wr-summary-strip ' + (s === 'yellow' ? 'yellow' : '') + '">' + esc(front.summary) + '</div>';
      } else {
        summaryHtml = '<div class="wr-summary-plain">' + esc(front.summary) + '</div>';
      }
    }

    var blockers = Array.isArray(front.top_blockers) ? front.top_blockers.slice(0, 2) : [];
    var blockersHtml = '';
    if (blockers.length > 0) {
      blockersHtml = '<div class="wr-blockers">'
        + blockers.map(function(b) { return '<div class="wr-blocker-item">' + esc(b) + '</div>'; }).join('')
        + '</div>';
    }

    var intelHint = '';
    if (front.last_intel_at) {
      var hoursAgo = (Date.now() - new Date(front.last_intel_at).getTime()) / 3600000;
      if (hoursAgo < 48) {
        intelHint = '<div class="wr-intel-hint">&#x1F4E5; Intel ' + wrFormatTime(front.last_intel_at) + '</div>';
      }
    }

    return [
      _wrCardOpen(front, isStale),
      _wrCardBadges(front, isStale),
      summaryHtml,
      blockersHtml,
      intelHint,
      _wrCardActionBar(front),
      _wrCardFooter(front),
      _wrDepWarning(front),
      _wrInlineEditForm(front),
      '</div>'
    ].join('\n');
  }

  // ── Card type: collab ──────────────────────────────────────────
  function renderWrCardCollab(front) {
    var isStale = _wrIsStale(front);
    var cat = front.category || '';
    var catClass = 'cat-' + cat;
    var catLabels = { company: '公司', freelance: '接案', collaboration: '協作' };
    var catDisplay = catLabels[cat] || cat;

    var summaryHtml = '';
    if (front.summary) {
      var s = front.status;
      if (s === 'red' || s === 'yellow') {
        summaryHtml = '<div class="wr-summary-strip ' + (s === 'yellow' ? 'yellow' : '') + '">' + esc(front.summary) + '</div>';
      } else {
        summaryHtml = '<div class="wr-summary-plain">' + esc(front.summary) + '</div>';
      }
    }

    var blockers = Array.isArray(front.top_blockers) ? front.top_blockers.slice(0, 2) : [];
    var blockersHtml = '';
    if (blockers.length > 0) {
      blockersHtml = '<div class="wr-blockers">'
        + blockers.map(function(b) { return '<div class="wr-blocker-item">' + esc(b) + '</div>'; }).join('')
        + '</div>';
    }

    var intelHint = '';
    if (front.last_intel_at) {
      var hoursAgo = (Date.now() - new Date(front.last_intel_at).getTime()) / 3600000;
      if (hoursAgo < 48) {
        intelHint = '<div class="wr-intel-hint">&#x1F4E5; Intel ' + wrFormatTime(front.last_intel_at) + '</div>';
      }
    }

    // Inject collab badge into the header area after the open
    var collabBadge = '<div style="margin-bottom:8px"><span class="wr-collab-badge ' + escAttr(catClass) + '">' + esc(catDisplay) + '</span></div>';

    return [
      _wrCardOpen(front, isStale),
      _wrCardBadges(front, isStale),
      collabBadge,
      summaryHtml,
      blockersHtml,
      intelHint,
      _wrCardActionBar(front),
      _wrCardFooter(front),
      _wrDepWarning(front),
      _wrInlineEditForm(front),
      '</div>'
    ].join('\n');
  }

  function _wrIsStale(front) {
    if (front.auto || front.staleness_threshold_hours == null || !front.last_updated) return false;
    var hoursSince = (Date.now() - new Date(front.last_updated).getTime()) / 3600000;
    return hoursSince > front.staleness_threshold_hours;
  }

  // ── Inline edit (kept for non-auto fronts via card quick-edit btn) ──
  function editWarRoomFront(id) {
    var form = document.getElementById('wr-edit-' + id);
    if (!form) return;
    form.style.display = form.style.display === 'none' ? '' : 'none';
  }

  function saveWarRoomFront(id) {
    var statusEl  = document.getElementById('wr-ef-status-'  + id);
    var summaryEl = document.getElementById('wr-ef-summary-' + id);
    var blockEl   = document.getElementById('wr-ef-blocking-'+ id);
    var nextEl    = document.getElementById('wr-ef-next-'    + id);
    if (!statusEl) return;

    var body = {
      front_id: id,
      status: statusEl.value,
      summary: summaryEl ? summaryEl.value : '',
      blocking: blockEl ? blockEl.value : '',
      next_action: nextEl ? nextEl.value : ''
    };
    fetch('/api/war-room/front/status', {
      method: 'POST',
      headers: {'Content-Type':'application/json'},
      body: JSON.stringify(body)
    })
    .then(function(r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      closeWrModal();
      loadWarRoom();
    })
    .catch(function(err) {
      alert('儲存失敗：' + err.message);
    });
  }

  // ── Quick actions (T1) ────────────────────────────────────────
  function setWrStatus(frontId, status) {
    if (!_warRoomData || !Array.isArray(_warRoomData.fronts)) return;
    var front = _warRoomData.fronts.find(function(f) { return f.id === frontId; });
    if (!front) return;

    // Auto fronts prompt for override duration; non-auto just flip status.
    if (front.auto) {
      _wrOverridePrompt(frontId, status);
      return;
    }
    _wrPostStatus(frontId, status, null);
  }

  function _wrOverridePrompt(frontId, status) {
    // Build a tiny popover. Use prompt() fallback for simplicity.
    var hours = prompt('此戰線為自動更新。手動覆寫此狀態多少小時？ (1 / 4 / 24，0 表示不設覆蓋)', '4');
    if (hours === null) return;
    var h = parseInt(hours, 10);
    if (isNaN(h) || h < 0) { alert('請輸入非負整數'); return; }
    _wrPostStatus(frontId, status, h > 0 ? h : null);
  }

  function _wrPostStatus(frontId, status, overrideHours) {
    var body = { front_id: frontId, status: status };
    if (overrideHours != null) body.override_hours = overrideHours;
    fetch('/api/war-room/front/status', {
      method: 'POST',
      headers: {'Content-Type':'application/json'},
      body: JSON.stringify(body)
    }).then(function(r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      loadWarRoom();
    }).catch(function(err) { alert('更新失敗：' + err.message); });
  }

  function clearWrOverride(frontId) {
    fetch('/api/war-room/front/override', {
      method: 'POST',
      headers: {'Content-Type':'application/json'},
      body: JSON.stringify({ front_id: frontId, active: false })
    }).then(function(r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      loadWarRoom();
    }).catch(function(err) { alert('清除覆蓋失敗：' + err.message); });
  }

  function triggerAutoUpdate() {
    var btn = document.getElementById('wr-autoupdate-btn');
    if (btn) { btn.disabled = true; btn.textContent = '⏳ 執行中...'; }
    fetch('/api/war-room/autoupdate/trigger', { method: 'POST' })
      .then(function(r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function() { loadWarRoom(); })
      .catch(function(err) { alert('autoupdate 觸發失敗：' + err.message); })
      .finally(function() {
        if (btn) { btn.disabled = false; btn.innerHTML = '&#x26A1; 立即跑 autoupdate'; }
      });
  }

  function loadAutoUpdateMeta() {
    var el = document.getElementById('wr-autoupdate-meta');
    if (!el) return;
    fetch('/api/war-room/autoupdate/meta')
      .then(function(r) { return r.ok ? r.json() : null; })
      .then(function(m) {
        if (!m) { el.textContent = ''; return; }
        var parts = [];
        if (m.last_run && m.last_run !== '0001-01-01T00:00:00Z') {
          parts.push('上次: ' + wrFormatTime(m.last_run));
        } else {
          parts.push('上次: 未執行');
        }
        if (m.next_run && m.next_run !== '0001-01-01T00:00:00Z') {
          parts.push('下次: ' + wrFormatTime(m.next_run));
        }
        if (!m.enabled) parts.push('(disabled)');
        if (m.running) parts.push('⏳ 執行中');
        el.textContent = parts.join(' · ');
      })
      .catch(function() { /* silent */ });
  }

  // ── Modal ─────────────────────────────────────────────────────
  function openWrModal(frontId, focusIntel) {
    if (!_warRoomData || !Array.isArray(_warRoomData.fronts)) return;
    var front = _warRoomData.fronts.find(function(f) { return f.id === frontId; });
    if (!front) return;

    _wrCurrentFrontId = frontId;

    // Header
    var color = wrStatusColor(front.status);
    var dotEl = document.getElementById('wr-modal-dot');
    dotEl.style.background = color;
    dotEl.style.color      = color;
    dotEl.className = 'wr-status-dot' + (front.status === 'red' ? ' pulse-red' : '');
    document.getElementById('wr-modal-title').textContent = front.name || front.id;
    document.getElementById('wr-modal-type-badge').textContent = WR_TYPE_LABEL[front.card_type] || front.card_type || '';

    // Add Intel button focuses textarea
    document.getElementById('wr-modal-add-intel-btn').onclick = function() {
      var ta = document.getElementById('wr-intel-input');
      if (ta) ta.focus();
    };

    // Submit intel
    document.getElementById('wr-intel-submit-btn').onclick = function() {
      _wrSubmitIntel(frontId);
    };

    // Render main body
    _wrRenderModalMain(front);

    // Open
    var overlay = document.getElementById('wr-modal');
    overlay.classList.add('open');
    document.body.style.overflow = 'hidden';

    if (focusIntel) {
      setTimeout(function() {
        var ta = document.getElementById('wr-intel-input');
        if (ta) ta.focus();
      }, 80);
    }

    // Fetch md (non-blocking)
    _wrFetchMd(frontId);
    // Fetch intel list (non-blocking)
    _wrFetchIntelList(frontId);
  }

  function closeWrModal() {
    var overlay = document.getElementById('wr-modal');
    overlay.classList.remove('open');
    document.body.style.overflow = '';
    _wrCurrentFrontId = null;
  }

  // Keyboard close
  document.addEventListener('keydown', function(e) {
    if (e.key === 'Escape' && document.getElementById('wr-modal').classList.contains('open')) {
      closeWrModal();
    }
  });

  function _wrDependsGraph(front) {
    var deps = Array.isArray(front.depends_on) ? front.depends_on : [];
    var dependents = [];
    if (_warRoomData && Array.isArray(_warRoomData.fronts)) {
      _warRoomData.fronts.forEach(function(f) {
        if (!f.archived && Array.isArray(f.depends_on) && f.depends_on.indexOf(front.id) !== -1) {
          dependents.push(f);
        }
      });
    }
    if (deps.length === 0 && dependents.length === 0) return '';
    function chip(f) {
      var cls = f.status === 'red' ? ' red' : (f.status === 'yellow' ? ' yellow' : '');
      var name = f.name || f.id;
      return '<span class="wr-depends-chip' + cls + '" onclick="event.stopPropagation();openWrModal(\'' +
        esc(f.id) + '\')" style="cursor:pointer">' + esc(name) + '</span>';
    }
    var depsHtml = deps.map(function(id) {
      var f = _warRoomData.fronts.find(function(x) { return x.id === id; });
      return f ? chip(f) : '<span class="wr-depends-chip">' + esc(id) + ' (?)</span>';
    }).join('');
    var dependentsHtml = dependents.map(chip).join('');
    var parts = [];
    if (depsHtml) parts.push('<div><span style="font-size:10px;color:var(--muted);margin-right:6px">依賴 →</span>' + depsHtml + '</div>');
    if (dependentsHtml) parts.push('<div><span style="font-size:10px;color:var(--muted);margin-right:6px">← 被依賴</span>' + dependentsHtml + '</div>');
    return '<div style="margin-bottom:12px;padding:8px 0;border-bottom:1px dashed var(--border);display:flex;flex-direction:column;gap:4px">' + parts.join('') + '</div>';
  }

  function _wrRenderModalMain(front) {
    var type = front.card_type || 'strategy';
    var html = _wrDependsGraph(front);
    if (type === 'metrics') {
      html += _wrModalMetrics(front);
    } else {
      html += _wrModalStrategy(front);
    }
    // Always append collapsible md placeholder
    html += _wrExpandSection(front);
    // Edit form for non-auto
    if (!front.auto) {
      html += '<div style="margin-top:16px;border-top:1px solid var(--border);padding-top:14px">'
        + '<div style="font-size:11px;color:var(--muted);margin-bottom:8px;text-transform:uppercase;letter-spacing:0.8px">快速編輯</div>'
        + _wrInlineEditForm(front)
        + '</div>';
    }
    document.getElementById('wr-modal-main').innerHTML = html;

    // Bind expand toggle
    var toggle = document.getElementById('wr-expand-toggle');
    var content = document.getElementById('wr-expand-content');
    var arrow   = document.getElementById('wr-expand-arrow');
    if (toggle && content && arrow) {
      toggle.addEventListener('click', function() {
        var open = content.classList.toggle('open');
        arrow.textContent = open ? '▼' : '▶';
      });
    }
  }

  function _wrModalMetrics(front) {
    var m = front.metrics || {};
    var pdVal   = (m.paper_days != null) ? String(m.paper_days) : 'N/A';
    var pdClass = (m.paper_days != null && m.paper_days > 0) ? 'warn' : 'neutral';
    var wrVal   = (m.win_rate != null) ? (m.win_rate * 100).toFixed(0) + '%' : 'N/A';
    var wrClass = (m.win_rate != null) ? (m.win_rate >= 0.5 ? 'ok' : 'warn') : 'neutral';
    var hypoVal = (m.active_hypo_count != null) ? String(m.active_hypo_count) : '0';
    var hypoClass = (m.active_hypo_count > 0) ? 'caution' : 'neutral';

    var stripHtml = '';
    var s = front.status;
    if ((s === 'red' || s === 'yellow') && front.summary) {
      stripHtml = '<div class="wr-modal-strip ' + (s === 'yellow' ? 'yellow' : 'red') + '">' + esc(front.summary) + '</div>';
    }

    return [
      '<div class="wr-modal-metrics">',
      '  <div class="wr-modal-metric">',
      '    <div class="wr-modal-metric-value ' + pdClass + '">' + esc(pdVal) + '</div>',
      '    <div class="wr-modal-metric-label">天 — 0 交易</div>',
      '  </div>',
      '  <div class="wr-modal-metric">',
      '    <div class="wr-modal-metric-value ' + wrClass + '">' + esc(wrVal) + '</div>',
      '    <div class="wr-modal-metric-label">勝率</div>',
      '  </div>',
      '  <div class="wr-modal-metric">',
      '    <div class="wr-modal-metric-value ' + wrConnClass(m.connection_status||'') + '">' + wrConnLabel(m.connection_status||'') + '</div>',
      '    <div class="wr-modal-metric-label">連線狀態</div>',
      '  </div>',
      '  <div class="wr-modal-metric">',
      '    <div class="wr-modal-metric-value ' + hypoClass + '">' + esc(hypoVal) + '</div>',
      '    <div class="wr-modal-metric-label">Active HYPO</div>',
      '  </div>',
      '</div>',
      stripHtml,
      '<div id="wr-modal-sections"><div style="color:var(--muted);font-size:12px">載入 md 中…</div></div>'
    ].join('\n');
  }

  function _wrModalStrategy(front) {
    var blockers = Array.isArray(front.top_blockers) ? front.top_blockers : [];
    var blockersHtml = '';
    if (blockers.length > 0) {
      blockersHtml = [
        '<div class="wr-section">',
        '  <div class="wr-section-title">&#x26A0; 當前阻塞</div>',
        '  <ul class="wr-bullet-list">',
        blockers.map(function(b) { return '    <li>' + esc(b) + '</li>'; }).join('\n'),
        '  </ul>',
        '</div>'
      ].join('\n');
    }

    var summaryHtml = '';
    if (front.summary) {
      var s = front.status;
      if (s === 'red' || s === 'yellow') {
        summaryHtml = '<div class="wr-modal-strip ' + (s === 'yellow' ? 'yellow' : 'red') + '">' + esc(front.summary) + '</div>';
      } else {
        summaryHtml = '<div class="wr-section"><div class="wr-section-title">概況</div><p style="font-size:12px;color:var(--text);line-height:1.5">' + esc(front.summary) + '</p></div>';
      }
    }

    return [
      summaryHtml,
      blockersHtml,
      '<div id="wr-modal-sections"><div style="color:var(--muted);font-size:12px">載入 md 中…</div></div>'
    ].join('\n');
  }

  function _wrExpandSection(front) {
    return [
      '<div class="wr-expand-section">',
      '  <button class="wr-expand-toggle" id="wr-expand-toggle">',
      '    <span id="wr-expand-arrow">&#x25B6;</span> &#x1F4C4; 查看完整 md',
      '  </button>',
      '  <div class="wr-expand-content" id="wr-expand-content">尚未載入</div>',
      '</div>'
    ].join('\n');
  }

  // ── Fetch md from backend ──────────────────────────────────────
  function _wrFetchMd(frontId) {
    fetch('/api/war-room/md/' + encodeURIComponent(frontId))
      .then(function(r) {
        if (r.status === 404) return null;
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.text();
      })
      .then(function(text) {
        var expandEl = document.getElementById('wr-expand-content');
        if (!expandEl) return;
        if (text === null) {
          expandEl.textContent = '尚未建立 md';
          _wrRenderMdSections(null, frontId);
        } else {
          expandEl.textContent = text;
          _wrRenderMdSections(text, frontId);
        }
      })
      .catch(function() {
        var expandEl = document.getElementById('wr-expand-content');
        if (expandEl) expandEl.textContent = '尚未建立 md';
        _wrRenderMdSections(null, frontId);
      });
  }

  function _wrRenderMdSections(md, frontId) {
    var sectionsEl = document.getElementById('wr-modal-sections');
    if (!sectionsEl) return;
    if (!md) {
      sectionsEl.innerHTML = '<div style="color:var(--muted);font-size:12px;font-style:italic">尚未建立 md — 請透過 Add Intel 累積資訊</div>';
      return;
    }
    // Parse sections from md
    var sections = _wrParseMdSections(md);
    if (sections.length === 0) {
      sectionsEl.innerHTML = '<div style="color:var(--muted);font-size:12px">（md 無可解析的段落）</div>';
      return;
    }
    var html = sections.map(function(sec) {
      var bullets = sec.bullets.slice(0, 3);
      return [
        '<div class="wr-section">',
        '  <div class="wr-section-title">' + esc(sec.title) + '</div>',
        '  <ul class="wr-bullet-list">',
        bullets.map(function(b) { return '    <li>' + esc(b) + '</li>'; }).join('\n'),
        '  </ul>',
        '</div>'
      ].join('\n');
    }).join('\n');
    sectionsEl.innerHTML = html;
  }

  function _wrParseMdSections(md) {
    var sections = [];
    var lines = md.split('\n');
    var current = null;
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];
      var headMatch = line.match(/^##\s+(.+)/);
      if (headMatch) {
        if (current) sections.push(current);
        current = { title: headMatch[1].trim(), bullets: [] };
        continue;
      }
      if (current) {
        // Collect bullets / list items / non-empty lines
        var bullet = line.match(/^[\s]*[-*]\s+(.+)/);
        if (bullet) {
          current.bullets.push(bullet[1].trim());
        } else if (line.trim() && !line.match(/^#+/) && !line.match(/^\|/) && !line.match(/^>/)) {
          if (current.bullets.length < 5) current.bullets.push(line.trim());
        }
      }
    }
    if (current) sections.push(current);
    return sections.slice(0, 5); // max 5 sections
  }

  // ── Fetch intel list ───────────────────────────────────────────
  function _wrFetchIntelList(frontId) {
    var listEl = document.getElementById('wr-intel-list');
    if (!listEl) return;
    fetch('/api/war-room/intel?front_id=' + encodeURIComponent(frontId))
      .then(function(r) {
        if (r.status === 404) return null;
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function(data) {
        if (!data || !Array.isArray(data.intels) || data.intels.length === 0) {
          listEl.innerHTML = '<div class="wr-intel-placeholder">尚無 Intel 記錄</div>';
          return;
        }
        listEl.innerHTML = data.intels.map(function(entry) {
          return [
            '<div class="wr-intel-entry">',
            '  <div class="wr-intel-date">' + esc(entry.date || '') + '</div>',
            '  <div class="wr-intel-text">' + esc(entry.text || '') + '</div>',
            '</div>'
          ].join('\n');
        }).join('\n');
      })
      .catch(function() {
        listEl.innerHTML = '<div class="wr-intel-placeholder">尚無 Intel 記錄</div>';
      });
  }

  // ── Submit intel ───────────────────────────────────────────────
  function _wrSubmitIntel(frontId) {
    var ta = document.getElementById('wr-intel-input');
    if (!ta || !ta.value.trim()) return;
    var text = ta.value.trim();

    fetch('/api/war-room/intel', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ front_id: frontId, text: text })
    })
    .then(function(r) {
      if (r.status === 404 || r.status === 501) {
        _wrShowToast('功能開發中');
        return;
      }
      if (!r.ok) throw new Error('HTTP ' + r.status);
      ta.value = '';
      _wrFetchIntelList(frontId);
      _wrShowToast('Intel 已提交');
    })
    .catch(function() {
      _wrShowToast('功能開發中');
    });
  }

  function _wrShowToast(msg) {
    var toast = document.createElement('div');
    toast.textContent = msg;
    toast.style.cssText = [
      'position:fixed;bottom:24px;left:50%;transform:translateX(-50%)',
      'background:var(--surface);border:1px solid var(--border);border-radius:8px',
      'padding:8px 18px;font-size:13px;color:var(--text);z-index:400',
      'box-shadow:0 4px 16px rgba(0,0,0,0.3);pointer-events:none'
    ].join(';');
    document.body.appendChild(toast);
    setTimeout(function() { toast.remove(); }, 2400);
  }

  // ── Create front (T2) ─────────────────────────────────────────
  function openCreateFrontModal() {
    var ids = ['wr-cf-id','wr-cf-name','wr-cf-depends'];
    ids.forEach(function(id) { var el = document.getElementById(id); if (el) el.value = ''; });
    var auto = document.getElementById('wr-cf-auto'); if (auto) auto.checked = false;
    var m = document.getElementById('wr-create-modal');
    if (m) { m.classList.add('open'); document.body.style.overflow = 'hidden'; }
  }
  function closeCreateFrontModal() {
    var m = document.getElementById('wr-create-modal');
    if (m) { m.classList.remove('open'); document.body.style.overflow = ''; }
  }
  function submitCreateFront() {
    var id = (document.getElementById('wr-cf-id').value || '').trim();
    var name = (document.getElementById('wr-cf-name').value || '').trim();
    var cat = document.getElementById('wr-cf-category').value;
    var ct = document.getElementById('wr-cf-cardtype').value;
    var auto = document.getElementById('wr-cf-auto').checked;
    var depStr = (document.getElementById('wr-cf-depends').value || '').trim();
    var deps = depStr ? depStr.split(',').map(function(s) { return s.trim(); }).filter(Boolean) : [];

    if (!/^[a-z0-9][a-z0-9-]*$/.test(id)) { alert('ID 格式錯誤：小寫字母數字連字號'); return; }
    if (!name) { alert('請填名稱'); return; }

    fetch('/api/war-room/front', {
      method: 'POST',
      headers: {'Content-Type':'application/json'},
      body: JSON.stringify({ id: id, name: name, category: cat, card_type: ct, auto: auto, depends_on: deps })
    }).then(function(r) {
      if (r.status === 409) throw new Error('ID 已存在');
      if (!r.ok) throw new Error('HTTP ' + r.status);
      closeCreateFrontModal();
      loadWarRoom();
    }).catch(function(err) { alert('建立失敗：' + err.message); });
  }

  // ── Archive / delete (T2) ─────────────────────────────────────
  function archiveCurrentFront() {
    if (!_wrCurrentFrontId) return;
    if (!confirm('封存「' + _wrCurrentFrontId + '」？卡片會從戰情室消失，但資料保留（可手動改 archived=false 還原）')) return;
    fetch('/api/war-room/front/' + encodeURIComponent(_wrCurrentFrontId), { method: 'DELETE' })
      .then(function(r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        closeWrModal(); loadWarRoom();
      }).catch(function(err) { alert('封存失敗：' + err.message); });
  }
  function deleteCurrentFront() {
    if (!_wrCurrentFrontId) return;
    if (!confirm('確定要【永久刪除】「' + _wrCurrentFrontId + '」嗎？此動作不可還原。')) return;
    fetch('/api/war-room/front/' + encodeURIComponent(_wrCurrentFrontId) + '?hard=true', { method: 'DELETE' })
      .then(function(r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        closeWrModal(); loadWarRoom();
      }).catch(function(err) { alert('刪除失敗：' + err.message); });
  }

  // ── Copy / export (T3) ────────────────────────────────────────
  function copyFrontSummary() {
    if (!_warRoomData || !_wrCurrentFrontId) return;
    var f = _warRoomData.fronts.find(function(x) { return x.id === _wrCurrentFrontId; });
    if (!f) return;
    var lines = [
      '### ' + (f.name || f.id) + ' (' + (f.status || 'unknown') + ')',
      f.summary ? '- 摘要：' + f.summary : '',
      f.blocking ? '- 阻礙：' + f.blocking : '',
      f.next_action ? '- 下一步：' + f.next_action : '',
      f.last_updated ? '- 更新：' + f.last_updated : ''
    ].filter(Boolean);
    _wrCopy(lines.join('\n'));
  }
  function exportWarRoomMarkdown() {
    if (!_warRoomData) return;
    var fronts = (_warRoomData.fronts || []).filter(function(f) { return !f.archived; });
    var out = ['# 戰情室 — ' + new Date().toISOString().slice(0,10), ''];
    fronts.forEach(function(f) {
      out.push('## ' + (f.name || f.id) + ' (' + (f.status || 'unknown') + ')');
      if (f.summary) out.push('- 摘要：' + f.summary);
      if (f.blocking) out.push('- 阻礙：' + f.blocking);
      if (f.next_action) out.push('- 下一步：' + f.next_action);
      if (f.last_updated) out.push('- 更新：' + f.last_updated);
      out.push('');
    });
    _wrCopy(out.join('\n'));
  }
  function _wrCopy(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text)
        .then(function() { _wrShowToast('已複製到剪貼簿'); })
        .catch(function() { _wrFallbackCopy(text); });
    } else { _wrFallbackCopy(text); }
  }
  function _wrFallbackCopy(text) {
    var ta = document.createElement('textarea');
    ta.value = text; ta.style.position = 'fixed'; ta.style.left = '-9999px';
    document.body.appendChild(ta); ta.select();
    try { document.execCommand('copy'); _wrShowToast('已複製到剪貼簿'); }
    catch (e) { alert('複製失敗，請手動：\n\n' + text); }
    document.body.removeChild(ta);
  }

  // ── Intel filter (T3) ─────────────────────────────────────────
  function filterIntelList() {
    var q = (document.getElementById('wr-intel-search').value || '').toLowerCase();
    var entries = document.querySelectorAll('#wr-intel-list .wr-intel-entry');
    entries.forEach(function(el) {
      var txt = el.textContent.toLowerCase();
      el.style.display = (!q || txt.indexOf(q) !== -1) ? '' : 'none';
    });
  }

  function esc(str) {
    return String(str || '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }
  function escAttr(str) {
    return String(str || '')
      .replace(/&/g, '&amp;')
      .replace(/"/g, '&quot;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
  }
