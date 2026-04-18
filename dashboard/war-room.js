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
    var fronts = (data && Array.isArray(data.fronts)) ? data.fronts : [];
    var genAt  = (data && data.generated_at) ? data.generated_at : '';
    document.getElementById('wr-last-updated').textContent =
      genAt ? '上次更新：' + wrFormatTime(genAt) : '無更新紀錄';

    var grid = document.getElementById('wr-grid');
    if (fronts.length === 0) {
      grid.innerHTML = '<div class="wr-empty">尚無戰線資料。請在 status.json 中新增 fronts。</div>';
      return;
    }
    grid.innerHTML = fronts.map(renderWarRoomCard).join('');
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
        parts.push('<span class="wr-override-badge">&#x1F512; 覆蓋中 到 ' + esc(moExpStr) + '</span>');
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
      '  <button class="wr-btn wr-btn-primary" onclick="event.stopPropagation();openWrModal(\'' + esc(front.id) + '\',true)">&#x1F4E5; Add Intel</button>',
      '  ' + editBtn,
      '</div>'
    ].join('\n');
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
    if (!_warRoomData || !Array.isArray(_warRoomData.fronts)) return;
    var front = _warRoomData.fronts.find(function(f) { return f.id === id; });
    if (!front) return;

    var statusEl  = document.getElementById('wr-ef-status-'  + id);
    var summaryEl = document.getElementById('wr-ef-summary-' + id);
    var blockEl   = document.getElementById('wr-ef-blocking-'+ id);
    var nextEl    = document.getElementById('wr-ef-next-'    + id);

    front.status      = statusEl  ? statusEl.value  : front.status;
    front.summary     = summaryEl ? summaryEl.value : front.summary;
    front.blocking    = blockEl   ? blockEl.value   : front.blocking;
    front.next_action = nextEl    ? nextEl.value    : front.next_action;
    front.last_updated = new Date().toISOString();
    _warRoomData.generated_at = new Date().toISOString();

    var body = JSON.stringify(_warRoomData, null, 2);
    fetch('/api/workspace/file', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: 'memory/war-room/status.json', content: body })
    })
    .then(function(r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      renderWarRoom(_warRoomData);
      closeWrModal();
    })
    .catch(function(err) {
      alert('儲存失敗：' + err.message);
    });
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

  function _wrRenderModalMain(front) {
    var type = front.card_type || 'strategy';
    var html = '';
    if (type === 'metrics') {
      html = _wrModalMetrics(front);
    } else {
      html = _wrModalStrategy(front);
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
