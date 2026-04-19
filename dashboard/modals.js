// --- Markdown Renderer ---

let outputMode = 'rendered';

function renderMarkdown(text) {
  if (!text) return '';

  // Escape HTML first.
  const escHtml = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

  // Extract code blocks first to protect them.
  const codeBlocks = [];
  text = text.replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) => {
    const idx = codeBlocks.length;
    codeBlocks.push(`<div class="code-block-wrap"><button class="copy-btn" onclick="copyCodeBlock(this)">Copy</button><pre><code class="lang-${escHtml(lang || 'text')}">${escHtml(code.replace(/\n$/, ''))}</code></pre></div>`);
    return `\x00CODEBLOCK${idx}\x00`;
  });

  // Process line by line.
  const lines = text.split('\n');
  const result = [];
  let inList = false;

  for (let i = 0; i < lines.length; i++) {
    let line = lines[i];

    // Code block placeholder.
    if (line.match(/^\x00CODEBLOCK\d+\x00$/)) {
      if (inList) { result.push('</ul>'); inList = false; }
      const idx = parseInt(line.match(/\d+/)[0]);
      result.push(codeBlocks[idx]);
      continue;
    }

    // Escape HTML in regular lines.
    line = escHtml(line);

    // Headings.
    const headingMatch = line.match(/^(#{1,6})\s+(.+)$/);
    if (headingMatch) {
      if (inList) { result.push('</ul>'); inList = false; }
      const level = headingMatch[1].length;
      result.push(`<h${level}>${inlineFormat(headingMatch[2])}</h${level}>`);
      continue;
    }

    // Horizontal rule.
    if (/^---+$/.test(line.trim()) || /^\*\*\*+$/.test(line.trim())) {
      if (inList) { result.push('</ul>'); inList = false; }
      result.push('<hr>');
      continue;
    }

    // List items.
    const listMatch = line.match(/^[\s]*[-*]\s+(.+)$/);
    if (listMatch) {
      if (!inList) { result.push('<ul>'); inList = true; }
      result.push(`<li>${inlineFormat(listMatch[1])}</li>`);
      continue;
    }

    // Numbered list items.
    const numMatch = line.match(/^[\s]*\d+\.\s+(.+)$/);
    if (numMatch) {
      if (!inList) { result.push('<ul>'); inList = true; }
      result.push(`<li>${inlineFormat(numMatch[1])}</li>`);
      continue;
    }

    // Blockquote.
    const bqMatch = line.match(/^&gt;\s?(.*)$/);
    if (bqMatch) {
      if (inList) { result.push('</ul>'); inList = false; }
      result.push(`<blockquote>${inlineFormat(bqMatch[1])}</blockquote>`);
      continue;
    }

    // Table rows (lines containing |).
    if (line.indexOf('|') >= 0 && line.trim().startsWith('|')) {
      if (inList) { result.push('</ul>'); inList = false; }
      const cells = line.split('|').slice(1, -1).map(c => c.trim());
      // Skip separator rows (---|---|---)
      if (cells.every(c => /^[-:]+$/.test(c))) continue;
      // Detect if this is a header row (next line is separator)
      const nextLine = (i + 1 < lines.length) ? escHtml(lines[i + 1]) : '';
      const nextIsSep = nextLine.indexOf('|') >= 0 && nextLine.split('|').slice(1, -1).every(c => /^[-:\s]+$/.test(c.trim()));
      if (nextIsSep && result[result.length - 1] !== '<table>') {
        result.push('<div class="table-wrap"><table>');
        result.push('<tr>' + cells.map(c => '<th>' + inlineFormat(c) + '</th>').join('') + '</tr>');
      } else {
        if (result.length > 0 && !result[result.length - 1].includes('<table') && !result[result.length - 1].includes('<tr>') && !result[result.length - 1].includes('<th>')) {
          // Not in a table yet but got a | row — start table
          result.push('<div class="table-wrap"><table>');
        }
        result.push('<tr>' + cells.map(c => '<td>' + inlineFormat(c) + '</td>').join('') + '</tr>');
      }
      // Check if next line is NOT a table row — close table
      const nextLineRaw = (i + 1 < lines.length) ? lines[i + 1] : '';
      if (!nextLineRaw.trim().startsWith('|')) {
        result.push('</table></div>');
      }
      continue;
    }

    // Close list if needed.
    if (inList) { result.push('</ul>'); inList = false; }

    // Empty line = paragraph break.
    if (line.trim() === '') {
      result.push('<br>');
      continue;
    }

    // Regular paragraph.
    result.push(`<p>${inlineFormat(line)}</p>`);
  }

  if (inList) result.push('</ul>');
  return result.join('\n');
}

function inlineFormat(text) {
  // Inline code (before other formatting to avoid conflicts).
  text = text.replace(/`([^`]+)`/g, '<code>$1</code>');
  // Bold.
  text = text.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  // Italic.
  text = text.replace(/\*(.+?)\*/g, '<em>$1</em>');
  // Links. Restrict href to http(s) or relative paths to block javascript: /
  // data: scheme XSS (renderMarkdown is also used for task/memory views where
  // input may not be fully trusted).
  text = text.replace(/\[([^\]]+)\]\(([^)]+)\)/g, function(_, label, url) {
    var safe = /^(https?:\/\/|\/|#|mailto:)/i.test(url) ? url : '#';
    return '<a href="' + safe + '" target="_blank" rel="noopener">' + label + '</a>';
  });
  return text;
}

function copyCodeBlock(btn) {
  var code = btn.parentElement.querySelector('code');
  if (!code) return;
  navigator.clipboard.writeText(code.textContent).then(function() {
    btn.textContent = 'Copied!';
    setTimeout(function() { btn.textContent = 'Copy'; }, 1500);
  });
}

function setOutputMode(mode) {
  outputMode = mode;
  document.getElementById('mode-rendered').classList.toggle('active', mode === 'rendered');
  document.getElementById('mode-raw').classList.toggle('active', mode === 'raw');
  updateOutputDisplay();
}

function updateOutputDisplay() {
  const rawEl = document.getElementById('output-content');
  const renderedEl = document.getElementById('output-rendered');
  const text = rawEl.textContent || '';

  if (outputMode === 'rendered' && text && text !== '(no output)' && text !== '(no error)' && text !== '(loading...)') {
    rawEl.style.display = 'none';
    renderedEl.style.display = '';
    renderedEl.innerHTML = renderMarkdown(text);
  } else {
    rawEl.style.display = '';
    renderedEl.style.display = 'none';
  }
}

// --- Prompts ---

let cachedPrompts = [];

async function refreshPrompts() {
  try {
    const prompts = await fetchJSON('/prompts');
    cachedPrompts = Array.isArray(prompts) ? prompts : [];
    const section = document.getElementById('prompts-section');

    if (cachedPrompts.length === 0) {
      section.style.display = 'none';
      return;
    }

    section.style.display = '';
    document.getElementById('prompts-meta').textContent = `${cachedPrompts.length} prompts`;

    const tbody = document.getElementById('prompts-body');
    tbody.innerHTML = cachedPrompts.map(p => `<tr>
      <td class="job-name">${esc(p.name)}</td>
      <td style="font-size:12px;color:var(--muted);max-width:400px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(p.preview || '')}</td>
      <td style="white-space:nowrap">
        <button class="btn" onclick="viewPrompt('${esc(p.name)}')">View</button>
        <button class="btn btn-edit" onclick="editPrompt('${esc(p.name)}')">Edit</button>
        <button class="btn btn-del" onclick="deletePromptUI('${esc(p.name)}')">Del</button>
      </td>
    </tr>`).join('');
  } catch(e) {
    document.getElementById('prompts-section').style.display = 'none';
  }
}

async function viewPrompt(name) {
  try {
    const data = await fetchJSON(`/prompts/${name}`);
    currentRunData = {
      name: name,
      status: 'success',
      costUsd: 0,
      model: '',
      outputSummary: data.content || '',
      error: '',
    };
    fullOutputCache = null;
    document.getElementById('output-title').textContent = 'Prompt: ' + name;
    document.getElementById('output-meta').innerHTML = '';
    document.getElementById('tab-full').style.display = 'none';
    showOutputTab('output');
    document.getElementById('output-modal').classList.add('open');
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

function openPromptModal(editName) {
  const modal = document.getElementById('prompt-modal');
  document.getElementById('prompt-form').reset();

  if (editName) {
    document.getElementById('prompt-modal-title').textContent = 'Edit Prompt';
    document.getElementById('pf-mode').value = 'edit';
    document.getElementById('pf-submit').textContent = 'Save';
    document.getElementById('pf-name').value = editName;
    document.getElementById('pf-name').readOnly = true;

    fetchJSON(`/prompts/${editName}`).then(data => {
      document.getElementById('pf-content').value = data.content || '';
    }).catch(e => toast('Error: ' + e.message));
  } else {
    document.getElementById('prompt-modal-title').textContent = 'Add Prompt';
    document.getElementById('pf-mode').value = 'add';
    document.getElementById('pf-submit').textContent = 'Add Prompt';
    document.getElementById('pf-name').readOnly = false;
  }

  modal.classList.add('open');
}

function editPrompt(name) { openPromptModal(name); }

function closePromptModal() {
  document.getElementById('prompt-modal').classList.remove('open');
}

async function submitPrompt(e) {
  e.preventDefault();
  const name = document.getElementById('pf-name').value.trim();
  const content = document.getElementById('pf-content').value;

  try {
    const resp = await fetch('/prompts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, content }),
    });
    if (resp.ok) {
      toast(`Prompt "${name}" saved`);
      closePromptModal();
      refreshPrompts();
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch(e) {
    toast('Error: ' + e.message);
  }
  return false;
}

async function deletePromptUI(name) {
  if (!confirm(`Delete prompt "${name}"?`)) return;
  try {
    const resp = await fetch(`/prompts/${name}`, { method: 'DELETE' });
    if (resp.ok) {
      toast(`"${name}" deleted`);
      refreshPrompts();
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

// --- MCP Servers ---

let cachedMCPs = [];

async function refreshMCP() {
  try {
    const mcps = await fetchJSON('/mcp');
    cachedMCPs = Array.isArray(mcps) ? mcps : [];
    const section = document.getElementById('mcp-section');

    if (cachedMCPs.length === 0) {
      section.style.display = 'none';
      return;
    }

    section.style.display = '';
    document.getElementById('mcp-meta').textContent = `${cachedMCPs.length} servers`;

    const tbody = document.getElementById('mcp-body');
    tbody.innerHTML = cachedMCPs.map(m => `<tr>
      <td class="job-name">${esc(m.name)}</td>
      <td style="font-family:monospace;font-size:12px">${esc(m.command || '-')}</td>
      <td style="font-size:12px;color:var(--muted);max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(m.args || '')}</td>
      <td style="white-space:nowrap">
        <button class="btn" onclick="viewMCPConfig('${esc(m.name)}')">View</button>
        <button class="btn btn-run" onclick="testMCPServer('${esc(m.name)}')">Test</button>
        <button class="btn btn-edit" onclick="editMCP('${esc(m.name)}')">Edit</button>
        <button class="btn btn-del" onclick="deleteMCPUI('${esc(m.name)}')">Del</button>
      </td>
    </tr>`).join('');
  } catch(e) {
    document.getElementById('mcp-section').style.display = 'none';
  }
}

async function viewMCPConfig(name) {
  try {
    const data = await fetchJSON(`/mcp/${name}`);
    const configStr = JSON.stringify(data.config, null, 2);
    currentRunData = {
      name: name, status: 'success', costUsd: 0, model: '',
      outputSummary: configStr, error: '',
    };
    fullOutputCache = null;
    document.getElementById('output-title').textContent = 'MCP Config: ' + name;
    document.getElementById('output-meta').innerHTML = '';
    document.getElementById('tab-full').style.display = 'none';
    showOutputTab('output');
    document.getElementById('output-modal').classList.add('open');
  } catch(e) { toast('Error: ' + e.message); }
}

async function testMCPServer(name) {
  try {
    toast('Testing ' + name + '...');
    const resp = await fetch(`/mcp/${name}/test`, { method: 'POST' });
    const data = await resp.json();
    if (data.ok) {
      toast('OK: ' + (data.output || 'server reachable'));
    } else {
      toast('FAIL: ' + (data.output || 'unknown error'));
    }
  } catch(e) { toast('Error: ' + e.message); }
}

function openMCPModal(editName) {
  const modal = document.getElementById('mcp-modal');
  document.getElementById('mcp-form').reset();
  if (editName) {
    document.getElementById('mcp-modal-title').textContent = 'Edit MCP Server';
    document.getElementById('mcpf-mode').value = 'edit';
    document.getElementById('mcpf-submit').textContent = 'Save';
    document.getElementById('mcpf-name').value = editName;
    document.getElementById('mcpf-name').readOnly = true;
    fetchJSON(`/mcp/${editName}`).then(data => {
      document.getElementById('mcpf-config').value = JSON.stringify(data.config, null, 2);
    }).catch(e => toast('Error: ' + e.message));
  } else {
    document.getElementById('mcp-modal-title').textContent = 'Add MCP Server';
    document.getElementById('mcpf-mode').value = 'add';
    document.getElementById('mcpf-submit').textContent = 'Add Server';
    document.getElementById('mcpf-name').readOnly = false;
  }
  modal.classList.add('open');
}

function editMCP(name) { openMCPModal(name); }
function closeMCPModal() { document.getElementById('mcp-modal').classList.remove('open'); }

async function submitMCP(e) {
  e.preventDefault();
  const name = document.getElementById('mcpf-name').value.trim();
  const configStr = document.getElementById('mcpf-config').value;
  let config;
  try {
    config = JSON.parse(configStr);
  } catch(e) {
    toast('Invalid JSON: ' + e.message);
    return false;
  }
  try {
    const resp = await fetch('/mcp', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, config }),
    });
    if (resp.ok) {
      toast(`MCP "${name}" saved`);
      closeMCPModal();
      refreshMCP();
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch(e) { toast('Error: ' + e.message); }
  return false;
}

async function deleteMCPUI(name) {
  if (!confirm(`Delete MCP config "${name}"?`)) return;
  try {
    const resp = await fetch(`/mcp/${name}`, { method: 'DELETE' });
    if (resp.ok) {
      toast(`"${name}" deleted`);
      refreshMCP();
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch(e) { toast('Error: ' + e.message); }
}

// --- Agent Memory ---

let cachedMemory = [];

async function refreshMemory() {
  try {
    const role = document.getElementById('memory-role-filter').value;
    const url = role ? `/memory?role=${encodeURIComponent(role)}` : '/memory';
    const entries = await fetchJSON(url);
    cachedMemory = Array.isArray(entries) ? entries : [];
    const section = document.getElementById('memory-section');

    // Always show section (even if empty, user can add)
    section.style.display = '';
    document.getElementById('memory-meta').textContent = `${cachedMemory.length} entries`;

    // Populate role filter
    const filter = document.getElementById('memory-role-filter');
    const currentRole = filter.value;
    const memRoles = cachedMemory.map(m => m.role);
    const cfgRoles = cachedRoles.map(r => r.name || '');
    const uniqueRoles = [...new Set([...memRoles, ...cfgRoles])].filter(Boolean).sort();
    filter.innerHTML = '<option value="">All Agents</option>' +
      uniqueRoles.map(r => `<option value="${esc(r)}"${r===currentRole?' selected':''}>${esc(r)}</option>`).join('');

    const tbody = document.getElementById('memory-body');
    if (cachedMemory.length === 0) {
      tbody.innerHTML = '<tr><td colspan="5" style="text-align:center;color:var(--muted);padding:20px">No memory entries</td></tr>';
      return;
    }
    tbody.innerHTML = cachedMemory.map(m => {
      let val = m.value || '';
      if (val.length > 80) val = val.substring(0, 80) + '...';
      val = val.replace(/\n/g, ' ');
      return `<tr>
      <td style="font-size:12px">${esc(m.role)}</td>
      <td class="job-name">${esc(m.key)}</td>
      <td style="font-size:12px;max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(val)}</td>
      <td style="font-size:11px;color:var(--muted)">${m.updatedAt ? timeStr(m.updatedAt) : ''}</td>
      <td style="white-space:nowrap">
        <button class="btn btn-edit" onclick="editMemory('${esc(m.role)}','${esc(m.key)}')">Edit</button>
        <button class="btn btn-del" onclick="deleteMemoryUI('${esc(m.role)}','${esc(m.key)}')">Del</button>
      </td>
    </tr>`;
    }).join('');
  } catch(e) {
    // Silently hide if memory API not available
    document.getElementById('memory-section').style.display = 'none';
  }
}

function openMemoryModal(editRole, editKey) {
  const modal = document.getElementById('memory-modal');
  document.getElementById('memory-form').reset();
  if (editRole && editKey) {
    document.getElementById('memory-modal-title').textContent = 'Edit Memory';
    document.getElementById('mem-mode').value = 'edit';
    document.getElementById('mem-role').value = editRole;
    document.getElementById('mem-role').readOnly = true;
    document.getElementById('mem-key').value = editKey;
    document.getElementById('mem-key').readOnly = true;
    fetchJSON(`/memory/${editRole}/${editKey}`).then(data => {
      document.getElementById('mem-value').value = data.value || '';
    }).catch(e => toast('Error: ' + e.message));
  } else {
    document.getElementById('memory-modal-title').textContent = 'Add Memory';
    document.getElementById('mem-mode').value = 'add';
    document.getElementById('mem-role').readOnly = false;
    document.getElementById('mem-key').readOnly = false;
  }
  modal.classList.add('open');
}

function editMemory(role, key) { openMemoryModal(role, key); }
function closeMemoryModal() { document.getElementById('memory-modal').classList.remove('open'); }

async function submitMemory(e) {
  e.preventDefault();
  const role = document.getElementById('mem-role').value.trim();
  const key = document.getElementById('mem-key').value.trim();
  const value = document.getElementById('mem-value').value;
  try {
    const resp = await fetch('/memory', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ role, key, value }),
    });
    if (resp.ok) {
      toast(`Memory "${role}.${key}" saved`);
      closeMemoryModal();
      refreshMemory();
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch(e) {
    toast('Error: ' + e.message);
  }
  return false;
}

async function deleteMemoryUI(role, key) {
  if (!confirm(`Delete memory "${role}.${key}"?`)) return;
  try {
    const resp = await fetch(`/memory/${role}/${key}`, { method: 'DELETE' });
    if (resp.ok) {
      toast(`"${role}.${key}" deleted`);
      refreshMemory();
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

// --- Routing ---

async function refreshRouting() {
  try {
    const data = await fetchJSON('/stats/routing?limit=50');
    const section = document.getElementById('routing-section');
    const history = data.history || [];
    const byRole = data.byRole || {};
    const roleNames = Object.keys(byRole).sort();

    if (history.length === 0 && roleNames.length === 0) {
      section.style.display = 'none';
      return;
    }

    section.style.display = '';
    document.getElementById('routing-meta').textContent = `${history.length} recent routes`;

    // Role stats cards
    const totalRoutes = history.length;
    const statsHTML = roleNames.map(name => {
      const s = byRole[name];
      const pct = totalRoutes > 0 ? Math.round((s.total / totalRoutes) * 100) : 0;
      return `<div class="stat">
        <div class="stat-label">${esc(name)}</div>
        <div class="stat-value">${s.total}</div>
        <div style="margin-top:4px;height:4px;background:var(--border);border-radius:2px;overflow:hidden">
          <div style="height:100%;width:${pct}%;background:var(--accent);border-radius:2px"></div>
        </div>
        <div style="font-size:10px;color:var(--muted);margin-top:2px">${pct}% of routes</div>
      </div>`;
    }).join('');
    document.getElementById('routing-stats').innerHTML = statsHTML;

    // History table
    const tbody = document.getElementById('routing-body');
    if (history.length > 0) {
      tbody.innerHTML = history.map(h => {
        const confColor = h.confidence === 'high' ? 'var(--green)' :
          h.confidence === 'medium' ? 'var(--yellow)' : 'var(--red)';
        let prompt = h.prompt || '';
        if (prompt.length > 60) prompt = prompt.substring(0, 60) + '...';
        return `<tr>
          <td class="job-next">${dateTimeStr(h.timestamp)}</td>
          <td style="font-size:12px;max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${esc(h.prompt)}">${esc(prompt)}</td>
          <td class="job-name">${esc(h.agent)}</td>
          <td style="font-size:12px">${esc(h.method)}</td>
          <td style="font-size:12px;color:${confColor}">${esc(h.confidence)}</td>
          <td style="font-size:12px;color:var(--muted)">${esc(h.source)}</td>
        </tr>`;
      }).join('');
    } else {
      tbody.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--muted);padding:20px">No routing history</td></tr>';
    }
  } catch(e) {
    document.getElementById('routing-section').style.display = 'none';
  }
}

// --- Sessions ---
let sessionRolesPopulated = false;
async function refreshSessions() {
  try {
    const roleFilter = document.getElementById('session-role-filter').value;
    let url = '/sessions?limit=20';
    if (roleFilter) url += '&role=' + encodeURIComponent(roleFilter);

    const data = await fetchJSON(url).catch(() => ({ sessions: [], total: 0 }));
    const sessions = data.sessions || [];
    const total = data.total || 0;
    window._latestSessions = sessions;

    const section = document.getElementById('sessions-section');
    section.style.display = (total > 0 || roleFilter) ? '' : 'none';

    document.getElementById('sessions-meta').textContent = total > 0 ? `${sessions.length} of ${total}` : '';

    // Populate role filter (once).
    if (!sessionRolesPopulated && Array.isArray(cachedJobs)) {
      const select = document.getElementById('session-role-filter');
      const roleSet = new Set();
      sessions.forEach(s => { if (s.agent) roleSet.add(s.agent); });
      for (const [name] of Object.entries(window._cachedRoles || {})) roleSet.add(name);
      roleSet.forEach(r => {
        if ([...select.options].some(o => o.value === r)) return;
        const opt = document.createElement('option');
        opt.value = r; opt.textContent = r;
        select.appendChild(opt);
      });
      sessionRolesPopulated = roleSet.size > 0;
    }

    document.getElementById('sessions-body').innerHTML = sessions.map(s => {
      const dot = s.status === 'active' ? 'dot-green' : s.status === 'completed' ? 'dot-gray' : 'dot-red';
      const title = esc((s.title || '(untitled)').substring(0, 60));
      const shortId = s.id.length > 12 ? s.id.substring(0, 12) : s.id;
      const ctxSize = s.contextSize || 0;
      const ctxWindow = 200000;
      const ctxPct = Math.min(100, (ctxSize / ctxWindow) * 100);
      const ctxColor = ctxPct >= 80 ? '#ef4444' : ctxPct >= 50 ? '#f59e0b' : '#22c55e';
      const ctxLabel = ctxSize > 0 ? (ctxSize >= 1000 ? Math.round(ctxSize/1000)+'k' : ctxSize) + '/200k' : '-';
      const ctxBar = ctxSize > 0 ? `<div style="width:80px">
        <div style="height:4px;background:var(--border);border-radius:2px;overflow:hidden;margin-bottom:2px">
          <div style="height:100%;width:${ctxPct.toFixed(1)}%;background:${ctxColor};border-radius:2px"></div>
        </div>
        <div style="font-size:10px;color:var(--muted);font-family:monospace">${ctxLabel}</div>
      </div>` : '<span style="color:var(--muted);font-size:11px">-</span>';
      return `<tr>
        <td><span style="color:var(--accent)">${esc(s.agent || '-')}</span></td>
        <td style="max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${esc(s.title)}">${title}</td>
        <td><span class="dot ${dot}"></span>${esc(s.status)}</td>
        <td>${s.messageCount || 0}</td>
        <td style="font-family:monospace;font-size:12px">${costFmt(s.totalCost)}</td>
        <td>${ctxBar}</td>
        <td style="font-size:12px;color:var(--muted)">${dateTimeStr(s.updatedAt)}</td>
        <td><button class="btn btn-sm" onclick="viewSessionWithStream('${esc(shortId)}','${esc(s.id)}','${esc(s.status)}')">View</button></td>
      </tr>`;
    }).join('');
  } catch(e) { console.error('refreshSessions:', e); }
}

async function viewSession(shortId, fullId) {
  try {
    const data = await fetchJSON('/sessions/' + fullId);
    if (!data || !data.session) return;

    const s = data.session;
    sessionSSERole = s.agent || 'agent';
    document.getElementById('session-modal-title').textContent = s.agent ? `${s.agent} Session` : 'Session';
    document.getElementById('session-modal-meta').innerHTML =
      `ID: ${esc(s.id)}<br>Source: ${esc(s.source)} | Status: ${esc(s.status)} | ` +
      `Messages: ${s.messageCount} | Cost: ${costFmt(s.totalCost)} | ` +
      `Tokens: ${(s.totalTokensIn||0)}/${(s.totalTokensOut||0)} | Updated: ${dateTimeStr(s.updatedAt)}`;

    const msgs = data.messages || [];
    document.getElementById('session-modal-messages').innerHTML = msgs.map(m => {
      const isUser = m.role === 'user';
      const isSys = m.role === 'system';
      const bgColor = isUser ? 'color-mix(in srgb, var(--accent) 12%, var(--surface))' : isSys ? 'var(--surface)' : 'color-mix(in srgb, var(--green) 10%, var(--surface))';
      const label = isUser ? 'USER' : isSys ? 'SYSTEM' : (s.agent || 'AGENT');
      const labelColor = isUser ? 'var(--accent)' : isSys ? 'var(--muted)' : 'var(--green)';
      const costInfo = m.costUsd > 0 ? ` | ${costFmt(m.costUsd)}` : '';
      const modelInfo = m.model ? ` | ${esc(m.model)}` : '';
      return `<div style="background:${bgColor};border:1px solid var(--border);border-radius:8px;padding:12px">
        <div style="display:flex;justify-content:space-between;margin-bottom:6px">
          <span style="font-size:11px;font-weight:600;color:${labelColor}">${label}</span>
          <span style="font-size:10px;color:var(--muted)">${dateTimeStr(m.createdAt)}${costInfo}${modelInfo}</span>
        </div>
        <div style="font-size:13px;white-space:pre-wrap;word-break:break-word;max-height:300px;overflow-y:auto">${esc(m.content)}</div>
      </div>`;
    }).join('');

    document.getElementById('session-modal').style.display = 'flex';
  } catch(e) { console.error('viewSession:', e); }
}

// --- SSE Live Streaming ---
const liveStreams = {}; // taskId -> EventSource

function toggleLive(taskId) {
  if (liveStreams[taskId]) {
    closeLive(taskId);
  } else {
    openLive(taskId);
  }
}

function openLive(taskId) {
  const output = document.getElementById('live-' + taskId);
  const btn = document.getElementById('live-btn-' + taskId);
  if (!output || !btn) return;

  output.style.display = '';
  output.innerHTML = '<span class="ev-started">Connecting...</span>\n';
  btn.classList.add('active');
  btn.textContent = 'Stop';

  const url = '/dispatch/' + encodeURIComponent(taskId) + '/stream';
  const es = new EventSource(url);
  liveStreams[taskId] = es;

  es.addEventListener('started', function(e) {
    try {
      const d = JSON.parse(e.data);
      output.innerHTML += '<span class="ev-started">[started] ' + esc(d.data?.name || taskId) + ' (' + esc(d.data?.model || '') + ')</span>\n';
      scrollLive(output);
    } catch(err) {}
  });

  es.addEventListener('output_chunk', function(e) {
    try {
      const d = JSON.parse(e.data);
      const chunk = d.data?.chunk || '';
      if (chunk) {
        output.innerHTML += '<span class="chunk">' + esc(chunk) + '</span>\n';
        scrollLive(output);
      }
    } catch(err) {}
  });

  es.addEventListener('tool_call', function(e) {
    try {
      const d = JSON.parse(e.data);
      const name = d.data?.name || d.data?.id || '(tool)';
      output.innerHTML += '<span class="ev-tool">[tool] ' + esc(name) + '</span>\n';
      scrollLive(output);
    } catch(err) {}
  });

  es.addEventListener('tool_result', function(e) {
    try {
      const d = JSON.parse(e.data);
      const content = d.data?.content || '';
      const preview = content.length > 200 ? content.substring(0, 200) + '...' : content;
      output.innerHTML += '<span class="ev-tool-result">[done] ' + esc(preview) + '</span>\n';
      scrollLive(output);
    } catch(err) {}
  });

  es.addEventListener('completed', function(e) {
    try {
      const d = JSON.parse(e.data);
      const cost = d.data?.costUsd ? ' $' + Number(d.data.costUsd).toFixed(4) : '';
      const dur = d.data?.durationMs ? ' ' + formatDuration(d.data.durationMs) : '';
      output.innerHTML += '<span class="ev-completed">[completed]' + cost + dur + '</span>\n';
      scrollLive(output);
    } catch(err) {}
    closeLive(taskId);
  });

  es.addEventListener('error', function(e) {
    if (e.data) {
      try {
        const d = JSON.parse(e.data);
        output.innerHTML += '<span class="ev-error">[error] ' + esc(d.data?.error || 'unknown') + '</span>\n';
        scrollLive(output);
      } catch(err) {}
    }
    closeLive(taskId);
  });

  es.onerror = function() {
    // EventSource will auto-reconnect; if task is done, connection closes cleanly.
    closeLive(taskId);
  };
}

function closeLive(taskId) {
  if (liveStreams[taskId]) {
    liveStreams[taskId].close();
    delete liveStreams[taskId];
  }
  const btn = document.getElementById('live-btn-' + taskId);
  if (btn) {
    btn.classList.remove('active');
    btn.textContent = 'Live';
  }
}

function scrollLive(el) {
  // Cap live output to 300 children to prevent unbounded DOM growth.
  while (el.childNodes.length > 300) el.removeChild(el.firstChild);
  el.scrollTop = el.scrollHeight;
}

// Close all live streams when tasks are no longer running.
function cleanupLiveStreams() {
  for (const taskId in liveStreams) {
    if (!document.getElementById('task-' + taskId)) {
      closeLive(taskId);
    }
  }
}

// --- Session Live Streaming ---
let sessionSSE = null;
let sessionSSEId = null;  // track which session is being watched
let sessionSSERole = '';  // role label for message rendering

function viewSessionWithStream(shortId, fullId, status) {
  viewSession(shortId, fullId);
  // Connect persistent watch stream for active sessions.
  if (status === 'active') {
    connectSessionStream(fullId);
  }
}

function connectSessionStream(sessionId) {
  disconnectSessionStream();
  sessionSSEId = sessionId;
  const url = '/sessions/' + encodeURIComponent(sessionId) + '/watch';
  sessionSSE = new EventSource(url);

  const badge = document.getElementById('session-live-badge');

  sessionSSE.onopen = function() {
    if (badge) badge.style.display = '';
  };

  // Helper: append element to messages container with auto-scroll.
  function appendToModal(html) {
    const container = document.getElementById('session-modal-messages');
    if (!container) return;
    const nearBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 100;
    container.insertAdjacentHTML('beforeend', html);
    if (nearBottom) container.scrollTop = container.scrollHeight;
  }

  // Helper: render a progress/status line (muted, compact).
  function appendStatusLine(text) {
    appendToModal('<div style="font-size:11px;color:var(--muted);padding:2px 12px">' + text + '</div>');
  }

  // Helper: render a full message bubble (same style as static messages).
  function appendMessageBubble(role, content, ts) {
    const isUser = role === 'user';
    const isSys = role === 'system';
    const bgColor = isUser ? 'color-mix(in srgb, var(--accent) 12%, var(--surface))' : isSys ? 'var(--surface)' : 'color-mix(in srgb, var(--green) 10%, var(--surface))';
    const label = isUser ? 'USER' : isSys ? 'SYSTEM' : (sessionSSERole || 'AGENT').toUpperCase();
    const labelColor = isUser ? 'var(--accent)' : isSys ? 'var(--muted)' : 'var(--green)';
    const timeStr = ts ? dateTimeStr(ts) : dateTimeStr(new Date().toISOString());
    appendToModal(
      '<div style="background:' + bgColor + ';border:1px solid var(--border);border-radius:8px;padding:12px">' +
        '<div style="display:flex;justify-content:space-between;margin-bottom:6px">' +
          '<span style="font-size:11px;font-weight:600;color:' + labelColor + '">' + label + '</span>' +
          '<span style="font-size:10px;color:var(--muted)">' + esc(timeStr) + '</span>' +
        '</div>' +
        '<div style="font-size:13px;white-space:pre-wrap;word-break:break-word;max-height:300px;overflow-y:auto">' + esc(content) + '</div>' +
      '</div>'
    );
  }

  var eventTypes = ['task_received', 'task_routing', 'discord_processing', 'discord_replying',
    'tool_call', 'tool_result', 'session_message', 'output_chunk', 'completed', 'error', 'started'];
  var toolStarts = {};

  eventTypes.forEach(function(evType) {
    sessionSSE.addEventListener(evType, function(e) {
      try {
        var d = JSON.parse(e.data);
        var data = d.data || {};
        var ts = d.timestamp || '';

        switch(evType) {
          case 'task_received':
            var author = data.author || 'user';
            var prompt = data.prompt || '';
            if (prompt.length > 300) prompt = prompt.substring(0, 297) + '...';
            appendMessageBubble('user', prompt, ts);
            break;

          case 'task_routing':
            var role = data.role || '?';
            var conf = data.confidence ? ' (' + Number(data.confidence).toFixed(2) + ')' : '';
            var method = data.method ? ' via ' + esc(data.method) : '';
            appendStatusLine('&#x1F500; Routing &#x2192; <b>' + esc(role) + '</b>' + esc(conf) + method);
            break;

          case 'discord_processing':
            appendStatusLine('&#x2699;&#xFE0F; Processing (' + esc(data.role || '') + ')...');
            break;

          case 'tool_call':
            var toolName = data.name || data.id || '(tool)';
            var toolId = data.toolUseId || '';
            if (toolId) toolStarts[toolId] = Date.now();
            appendStatusLine('&#x1F527; ' + esc(toolName) + '...');
            break;

          case 'tool_result':
            var tid = data.toolUseId || '';
            var dur = '';
            if (tid && toolStarts[tid]) {
              dur = ' (' + ((Date.now() - toolStarts[tid]) / 1000).toFixed(1) + 's)';
              delete toolStarts[tid];
            } else if (data.duration) {
              dur = ' (' + formatDuration(data.duration) + ')';
            }
            var resName = data.name || '';
            var isErr = data.isError ? ' <span style="color:var(--red)">error</span>' : '';
            appendStatusLine('&nbsp;&nbsp;&#x2714; ' + esc(resName) + ' done' + dur + isErr);
            break;

          case 'session_message':
            var msgRole = data.role || 'assistant';
            var content = data.content || '';
            appendMessageBubble(msgRole, content, ts);
            break;

          case 'discord_replying':
            if (data.status && data.status !== 'success') {
              appendStatusLine('&#x274C; ' + esc(data.status));
            }
            break;

          case 'completed':
            // Task cycle done — refresh metadata but keep stream open.
            if (sessionSSEId) {
              fetchJSON('/sessions/' + sessionSSEId).then(function(result) {
                if (result && result.session) {
                  var s = result.session;
                  var meta = document.getElementById('session-modal-meta');
                  if (meta) {
                    meta.innerHTML = 'ID: ' + esc(s.id) + '<br>Source: ' + esc(s.source) + ' | Status: ' + esc(s.status) + ' | ' +
                      'Messages: ' + (s.messageCount||0) + ' | Cost: ' + costFmt(s.totalCost) + ' | ' +
                      'Tokens: ' + (s.totalTokensIn||0) + '/' + (s.totalTokensOut||0) + ' | Updated: ' + dateTimeStr(s.updatedAt);
                  }
                }
              }).catch(function(){});
            }
            break;

          case 'error':
            var errMsg = data.error || data.status || 'unknown error';
            appendStatusLine('&#x274C; <span style="color:var(--red)">' + esc(errMsg) + '</span>');
            break;

          case 'output_chunk':
            // Show streaming output chunks in a live area.
            var chunk = data.chunk || '';
            if (chunk) {
              var streamEl = document.getElementById('session-stream-output');
              if (!streamEl) {
                appendToModal('<div id="session-stream-output" class="live-output" style="font-size:12px;color:var(--muted);padding:4px 12px;white-space:pre-wrap"></div>');
                streamEl = document.getElementById('session-stream-output');
              }
              if (streamEl) {
                streamEl.textContent += chunk;
                // Cap stream text to ~50KB to prevent memory bloat.
                if (streamEl.textContent.length > 50000) {
                  streamEl.textContent = streamEl.textContent.slice(-40000);
                }
                var container = document.getElementById('session-modal-messages');
                if (container) {
                  var nearBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 100;
                  if (nearBottom) container.scrollTop = container.scrollHeight;
                }
              }
            }
            break;

          case 'started':
            // Remove any streaming output area from previous task cycle.
            var old = document.getElementById('session-stream-output');
            if (old) old.remove();
            break;
        }
      } catch(err) { console.error('sessionSSE event:', evType, err); }
    });
  });

  sessionSSE.onerror = function() {
    if (badge) badge.style.display = 'none';
    // Auto-reconnect after 3s if modal is still open.
    var modal = document.getElementById('session-modal');
    if (modal && modal.style.display === 'flex' && sessionSSEId) {
      var reconnectId = sessionSSEId;
      setTimeout(function() {
        if (sessionSSEId === reconnectId) {
          connectSessionStream(reconnectId);
        }
      }, 3000);
    }
  };
}

function disconnectSessionStream() {
  if (sessionSSE) {
    sessionSSE.close();
    sessionSSE = null;
  }
  sessionSSEId = null;
  var badge = document.getElementById('session-live-badge');
  if (badge) badge.style.display = 'none';
  var streamEl = document.getElementById('session-stream-output');
  if (streamEl) streamEl.remove();
}

// --- Chat Tab ---
var currentTab = 'dashboard';
var chatSessionId = null;
var chatSSE = null;
var chatSending = false;
var chatSessionRole = '';
var liveTaskItems = {}; // taskId -> { name, role }
var taskViewId = null;
var taskViewSSE = null;

