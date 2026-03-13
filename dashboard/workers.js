// --- Job Actions ---

async function toggleJob(id, enabled) {
  try {
    await fetch(`/cron/${id}/toggle`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled }),
    });
    toast(`${id} ${enabled ? 'enabled' : 'disabled'}`);
    setTimeout(refresh, 300);
  } catch (e) {
    toast('Error: ' + e.message);
  }
}

async function triggerJob(id) {
  try {
    await fetch(`/cron/${id}/run`, { method: 'POST' });
    toast(`${id} triggered`);
    setTimeout(refresh, 500);
  } catch (e) {
    toast('Error: ' + e.message);
  }
}

async function deleteJob(id) {
  if (!confirm(`Delete job "${id}"?`)) return;
  try {
    const resp = await fetch(`/cron/${id}`, { method: 'DELETE' });
    if (resp.ok) {
      toast(`${id} deleted`);
      setTimeout(refresh, 300);
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch (e) {
    toast('Error: ' + e.message);
  }
}

async function cancelTask(id) {
  if (!confirm(`Cancel task "${id}"?`)) return;
  try {
    const resp = await fetch(`/cancel/${id}`, { method: 'POST' });
    if (resp.ok) {
      toast(`Cancelling ${id}...`);
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
    setTimeout(refresh, 500);
  } catch (e) {
    toast('Error: ' + e.message);
  }
}

async function cancelDispatch() {
  try {
    await fetch('/cancel', { method: 'POST' });
    toast('Cancelling...');
    setTimeout(refresh, 500);
  } catch (e) {
    toast('Error: ' + e.message);
  }
}

// --- Job Modal ---

async function openJobModal(editId) {
  const modal = document.getElementById('job-modal');
  const form = document.getElementById('job-form');
  form.reset();

  if (editId) {
    document.getElementById('job-modal-title').textContent = 'Edit Job';
    document.getElementById('jf-mode').value = 'edit';
    document.getElementById('jf-original-id').value = editId;
    document.getElementById('jf-submit').textContent = 'Save Changes';
    document.getElementById('jf-id').readOnly = true;

    // Fetch full job config from API
    try {
      const j = await fetchJSON(`/cron/${editId}`);
      document.getElementById('jf-id').value = j.id;
      document.getElementById('jf-name').value = j.name;
      document.getElementById('jf-schedule').value = j.schedule;
      document.getElementById('jf-tz').value = j.tz || 'Asia/Taipei';
      document.getElementById('jf-role').value = j.agent || '';
      document.getElementById('jf-prompt').value = (j.task && j.task.prompt) || '';
      document.getElementById('jf-model').value = (j.task && j.task.model) || 'sonnet';
      document.getElementById('jf-timeout').value = (j.task && j.task.timeout) || '5m';
      document.getElementById('jf-budget').value = (j.task && j.task.budget) || 2.0;
      document.getElementById('jf-workdir').value = (j.task && j.task.workdir) || '';
      document.getElementById('jf-permission').value = (j.task && j.task.permissionMode) || '';
      document.getElementById('jf-onsuccess').value = (j.onSuccess || []).join(', ');
      document.getElementById('jf-onfailure').value = (j.onFailure || []).join(', ');
      document.getElementById('jf-notify').checked = j.notify || false;
      // Restore notify channel selection if present.
      if (j.notify && j.notifyChannel) {
        await populateDiscordChannels(j.notifyChannel);
      } else {
        document.getElementById('jf-notify-channel-row').style.display = 'none';
      }
    } catch (e) {
      toast('Error loading job: ' + e.message);
      return;
    }
  } else {
    document.getElementById('job-modal-title').textContent = 'Add Job';
    document.getElementById('jf-mode').value = 'add';
    document.getElementById('jf-original-id').value = '';
    document.getElementById('jf-submit').textContent = 'Add Job';
    document.getElementById('jf-id').readOnly = false;
  }

  populateProjectDropdown('jf-project');

  modal.classList.add('open');
}

function editJob(id) {
  openJobModal(id);
}

function closeJobModal() {
  document.getElementById('job-modal').classList.remove('open');
}

// Populate the Discord channel select and show the row.
// If selectedName is provided, pre-select that option.
async function populateDiscordChannels(selectedName) {
  const row = document.getElementById('jf-notify-channel-row');
  const sel = document.getElementById('jf-notify-channel');
  row.style.display = '';
  // Fetch channels from API.
  try {
    const channels = await fetchJSON('/api/discord/channels');
    sel.innerHTML = '<option value="">— default —</option>';
    (channels || []).forEach(ch => {
      const opt = document.createElement('option');
      opt.value = ch.name;
      opt.textContent = ch.name;
      if (ch.name === selectedName) opt.selected = true;
      sel.appendChild(opt);
    });
  } catch (_) {
    // If fetch fails, keep default option only.
    sel.innerHTML = '<option value="">— default —</option>';
  }
}

// Wire up notify checkbox toggle after DOM is ready.
document.addEventListener('DOMContentLoaded', () => {
  const notifyChk = document.getElementById('jf-notify');
  const channelRow = document.getElementById('jf-notify-channel-row');
  if (notifyChk) {
    notifyChk.addEventListener('change', async () => {
      if (notifyChk.checked) {
        await populateDiscordChannels('');
      } else {
        channelRow.style.display = 'none';
      }
    });
  }
});

async function submitJob(e) {
  e.preventDefault();
  const mode = document.getElementById('jf-mode').value;
  const id = document.getElementById('jf-id').value.trim();

  const payload = {
    id: id,
    name: document.getElementById('jf-name').value.trim(),
    enabled: true,
    schedule: document.getElementById('jf-schedule').value.trim(),
    tz: document.getElementById('jf-tz').value.trim() || 'Asia/Taipei',
    agent: document.getElementById('jf-role').value.trim(),
    task: {
      prompt: document.getElementById('jf-prompt').value.trim(),
      model: document.getElementById('jf-model').value.trim() || 'sonnet',
      timeout: document.getElementById('jf-timeout').value.trim() || '5m',
      budget: parseFloat(document.getElementById('jf-budget').value) || 2.0,
      workdir: document.getElementById('jf-workdir').value.trim(),
      permissionMode: document.getElementById('jf-permission').value,
    },
    notify: document.getElementById('jf-notify').checked,
    notifyChannel: document.getElementById('jf-notify-channel').value,
    onSuccess: document.getElementById('jf-onsuccess').value.split(',').map(s => s.trim()).filter(Boolean),
    onFailure: document.getElementById('jf-onfailure').value.split(',').map(s => s.trim()).filter(Boolean),
  };

  try {
    let resp;
    if (mode === 'edit') {
      const origId = document.getElementById('jf-original-id').value;
      resp = await fetch(`/cron/${origId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
    } else {
      resp = await fetch('/cron', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
    }

    if (resp.ok) {
      toast(mode === 'edit' ? `${id} updated` : `${id} created`);
      closeJobModal();
      setTimeout(refresh, 300);
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch (e) {
    toast('Error: ' + e.message);
  }
  return false;
}

// --- Output Modal ---

let currentRunData = null;

let fullOutputCache = null;

async function viewRun(runId) {
  try {
    const run = await fetchJSON(`/history/${runId}`);
    currentRunData = run;
    fullOutputCache = null;

    document.getElementById('output-title').textContent = run.name;
    document.getElementById('output-meta').innerHTML = [
      `<span>${statusBadge(run.status)}</span>`,
      `<span>Cost: ${costFmt(run.costUsd)}</span>`,
      `<span>Model: ${esc(run.model)}</span>`,
      `<span>${dateTimeStr(run.startedAt)}</span>`,
    ].join('');

    // Show "Full Output" button if output file exists.
    const tabFull = document.getElementById('tab-full');
    tabFull.style.display = run.outputFile ? '' : 'none';

    showOutputTab('output');
    document.getElementById('output-modal').classList.add('open');
  } catch (e) {
    toast('Error loading run: ' + e.message);
  }
}

function showOutputTab(tab) {
  if (!currentRunData) return;
  const content = document.getElementById('output-content');
  const tabOut = document.getElementById('tab-output');
  const tabErr = document.getElementById('tab-error');
  const tabFull = document.getElementById('tab-full');

  tabOut.style.fontWeight = '400';
  tabErr.style.fontWeight = '400';
  tabFull.style.fontWeight = '400';

  if (tab === 'error') {
    content.textContent = currentRunData.error || '(no error)';
    tabErr.style.fontWeight = '600';
  } else if (tab === 'full') {
    if (fullOutputCache) {
      try {
        const parsed = JSON.parse(fullOutputCache);
        content.textContent = parsed.result || JSON.stringify(parsed, null, 2);
      } catch {
        content.textContent = fullOutputCache;
      }
    } else {
      content.textContent = '(loading...)';
    }
    tabFull.style.fontWeight = '600';
  } else {
    content.textContent = currentRunData.outputSummary || '(no output)';
    tabOut.style.fontWeight = '600';
  }

  updateOutputDisplay();
}

async function loadFullOutput() {
  if (!currentRunData || !currentRunData.outputFile) return;

  if (fullOutputCache) {
    showOutputTab('full');
    return;
  }

  try {
    document.getElementById('output-content').textContent = '(loading full output...)';
    updateOutputDisplay();
    const resp = await fetch(`/outputs/${currentRunData.outputFile}`);
    if (!resp.ok) {
      toast('Error loading output file');
      return;
    }
    fullOutputCache = await resp.text();
    showOutputTab('full');
  } catch (e) {
    toast('Error: ' + e.message);
  }
}

async function copyOutput() {
  const content = document.getElementById('output-content').textContent;
  if (!content) return;
  try {
    await navigator.clipboard.writeText(content);
    toast('Copied to clipboard');
  } catch {
    // Fallback for older browsers.
    const ta = document.createElement('textarea');
    ta.value = content;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    document.body.removeChild(ta);
    toast('Copied to clipboard');
  }
}

function closeOutputModal() {
  document.getElementById('output-modal').classList.remove('open');
  currentRunData = null;
  fullOutputCache = null;
}

// --- Soul Modal ---

function viewSoul(name) {
  const r = cachedRoles.find(x => x.name === name);
  if (!r) return;

  document.getElementById('soul-title').textContent = name;
  document.getElementById('soul-meta').innerHTML = [
    `<span>Model: ${esc(r.model || 'default')}</span>`,
    r.permissionMode ? `<span>Permission: ${esc(r.permissionMode)}</span>` : '',
    `<span>File: ${esc(r.soulFile || '-')}</span>`,
    r.description ? `<span>${esc(r.description)}</span>` : '',
  ].filter(Boolean).join('');
  document.getElementById('soul-content').textContent = r.soulPreview || '(no soul file content)';
  document.getElementById('soul-modal').classList.add('open');
}

function closeSoulModal() {
  document.getElementById('soul-modal').classList.remove('open');
}

// --- Role Modal ---

async function loadArchetypes() {
  try {
    cachedArchetypes = await fetchJSON('/roles/archetypes');
    const sel = document.getElementById('rf-archetype');
    sel.innerHTML = '<option value="">— blank —</option>' +
      cachedArchetypes.map(a => `<option value="${esc(a.name)}">${esc(a.name)} — ${esc(a.description)}</option>`).join('');
  } catch(e) {}
}

function applyArchetype() {
  const name = document.getElementById('rf-archetype').value;
  const a = cachedArchetypes.find(x => x.name === name);
  if (a) {
    document.getElementById('rf-model').value = a.model;
    document.getElementById('rf-permission').value = a.permissionMode;
    const roleName = document.getElementById('rf-name').value || name;
    document.getElementById('rf-soul').value = a.soulTemplate.replace(/\{\{\.RoleName\}\}/g, roleName);
    if (!document.getElementById('rf-soulfile').value) {
      document.getElementById('rf-soulfile').value = 'SOUL-' + roleName + '.md';
    }
  }
}

function openRoleModal(editName) {
  const modal = document.getElementById('role-modal');
  const form = document.getElementById('role-form');
  form.reset();

  if (editName) {
    document.getElementById('role-modal-title').textContent = 'Edit Agent';
    document.getElementById('rf-mode').value = 'edit';
    document.getElementById('rf-submit').textContent = 'Save Changes';
    document.getElementById('rf-name').value = editName;
    document.getElementById('rf-name').readOnly = true;

    // Fetch full role info.
    fetchJSON(`/roles/${editName}`).then(r => {
      document.getElementById('rf-model').value = r.model || '';
      document.getElementById('rf-permission').value = r.permissionMode || '';
      document.getElementById('rf-desc').value = r.description || '';
      document.getElementById('rf-soulfile').value = r.soulFile || '';
      document.getElementById('rf-soul').value = r.soulContent || '';
    }).catch(e => toast('Error loading role: ' + e.message));
  } else {
    document.getElementById('role-modal-title').textContent = 'Add Agent';
    document.getElementById('rf-mode').value = 'add';
    document.getElementById('rf-submit').textContent = 'Add Agent';
    document.getElementById('rf-name').readOnly = false;
  }

  modal.classList.add('open');
}

function editRole(name) { openRoleModal(name); }

function closeRoleModal() {
  document.getElementById('role-modal').classList.remove('open');
}

async function submitRole(e) {
  e.preventDefault();
  const mode = document.getElementById('rf-mode').value;
  const name = document.getElementById('rf-name').value.trim();

  const payload = {
    name: name,
    model: document.getElementById('rf-model').value.trim(),
    permissionMode: document.getElementById('rf-permission').value,
    description: document.getElementById('rf-desc').value.trim(),
    soulFile: document.getElementById('rf-soulfile').value.trim(),
    soulContent: document.getElementById('rf-soul').value,
  };

  try {
    let resp;
    if (mode === 'edit') {
      resp = await fetch(`/roles/${name}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
    } else {
      resp = await fetch('/roles', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
    }

    if (resp.ok) {
      toast(mode === 'edit' ? `${name} updated` : `${name} created`);
      closeRoleModal();
      setTimeout(refresh, 300);
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch (e) {
    toast('Error: ' + e.message);
  }
  return false;
}

async function deleteRole(name) {
  if (!confirm(`Delete role "${name}"?`)) return;
  try {
    const resp = await fetch(`/roles/${name}`, { method: 'DELETE' });
    if (resp.ok) {
      toast(`${name} deleted`);
      setTimeout(refresh, 300);
    } else {
      const data = await resp.json();
      toast('Error: ' + (data.error || 'unknown'));
    }
  } catch (e) {
    toast('Error: ' + e.message);
  }
}

// --- History Pagination ---

async function refreshHistory() {
  try {
    const statusFilter = document.getElementById('history-status-filter').value;
    const jobFilter = document.getElementById('history-job-filter').value;
    let url = `/history?limit=${historyLimit}&page=${historyPage}`;
    if (statusFilter) url += `&status=${statusFilter}`;
    if (jobFilter) url += `&job_id=${jobFilter}`;

    const data = await fetchJSON(url);
    const runs = data.runs || [];
    historyTotal = data.total || 0;

    document.getElementById('history-section').style.display = '';
    document.getElementById('history-meta').textContent = `${historyTotal} total`;

    const hbody = document.getElementById('history-body');
    if (runs.length > 0) {
      hbody.innerHTML = runs.map(h => `<tr class="history-row" onclick="viewRun(${h.id})">
        <td class="job-name">${esc(h.name)}</td>
        <td style="font-size:12px">${esc(h.source)}</td>
        <td>${statusBadge(h.status)}</td>
        <td style="font-size:12px;font-family:monospace">${costFmt(h.costUsd)}</td>
        <td style="font-size:12px">${esc(h.model)}</td>
        <td class="job-next">${dateTimeStr(h.startedAt)}</td>
      </tr>`).join('');
    } else {
      hbody.innerHTML = '<tr><td colspan="6" style="text-align:center;color:var(--muted);padding:20px">No records found</td></tr>';
    }

    // Pagination controls.
    const totalPages = Math.ceil(historyTotal / historyLimit) || 1;
    document.getElementById('history-prev').disabled = historyPage <= 1;
    document.getElementById('history-next').disabled = historyPage >= totalPages;
    document.getElementById('history-page-info').textContent = `Page ${historyPage} of ${totalPages}`;
  } catch(e) {
    document.getElementById('history-section').style.display = 'none';
  }
}

function historyPrev() {
  if (historyPage > 1) { historyPage--; refreshHistory(); }
}

function historyNext() {
  const totalPages = Math.ceil(historyTotal / historyLimit) || 1;
  if (historyPage < totalPages) { historyPage++; refreshHistory(); }
}

// Close modals on overlay click
document.addEventListener('click', function(e) {
  if (e.target.classList.contains('modal-overlay')) {
    e.target.classList.remove('open');
  }
});

// Close modals on Escape key
document.addEventListener('keydown', function(e) {
  if (e.key === 'Escape') {
    document.querySelectorAll('.modal-overlay.open').forEach(m => m.classList.remove('open'));
  }
});

// --- Quick Dispatch Modal ---

let dispatchTimer = null;

function openDispatchModal() {
  const modal = document.getElementById('dispatch-modal');
  document.getElementById('dispatch-form').reset();
  document.getElementById('dispatch-form').style.display = '';
  document.getElementById('df-loading').style.display = 'none';
  document.getElementById('df-actions').style.display = 'flex';

  // Populate role dropdown from cached roles.
  const roleSel = document.getElementById('df-role');
  roleSel.innerHTML = '<option value="">— none —</option>' +
    cachedRoles.map(r => `<option value="${esc(r.name)}">${esc(r.name)}</option>`).join('');

  // Populate project dropdown.
  populateProjectDropdown('df-project');

  modal.classList.add('open');
}

function closeDispatchModal() {
  document.getElementById('dispatch-modal').classList.remove('open');
  if (dispatchTimer) { clearInterval(dispatchTimer); dispatchTimer = null; }
}

async function submitDispatch(e) {
  e.preventDefault();

  const prompt = document.getElementById('df-prompt').value.trim();
  if (!prompt) return false;

  const task = {
    prompt: prompt,
    model: document.getElementById('df-model').value.trim() || 'sonnet',
    timeout: document.getElementById('df-timeout').value.trim() || '5m',
    budget: parseFloat(document.getElementById('df-budget').value) || 2.0,
    workdir: document.getElementById('df-workdir').value.trim(),
    permissionMode: document.getElementById('df-permission').value,
  };

  // If role selected, inject as systemPrompt from role name.
  const role = document.getElementById('df-role').value;
  if (role) {
    try {
      const roleData = await fetchJSON(`/roles/${role}`);
      if (roleData.soulContent) {
        task.systemPrompt = roleData.soulContent;
      }
      if (roleData.model && !document.getElementById('df-model').value.trim()) {
        task.model = roleData.model;
      }
    } catch(e) {}
  }

  // Show loading state.
  document.getElementById('df-actions').style.display = 'none';
  document.getElementById('df-loading').style.display = '';
  const startTime = Date.now();
  dispatchTimer = setInterval(() => {
    const elapsed = Math.floor((Date.now() - startTime) / 1000);
    const m = Math.floor(elapsed / 60);
    const s = elapsed % 60;
    document.getElementById('df-elapsed').textContent = m > 0 ? `${m}m ${s}s` : `${s}s`;
  }, 1000);

  try {
    const result = await fetchJSON('/dispatch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify([task]),
    });

    if (dispatchTimer) { clearInterval(dispatchTimer); dispatchTimer = null; }
    closeDispatchModal();

    // Show result in output modal.
    if (result && result.tasks && result.tasks.length > 0) {
      const t = result.tasks[0];
      currentRunData = {
        name: t.name || 'Quick Dispatch',
        status: t.status,
        costUsd: t.costUsd || 0,
        model: t.model,
        outputSummary: t.output || '',
        error: t.error || '',
        outputFile: t.outputFile || '',
        startedAt: result.startedAt,
      };
      fullOutputCache = null;

      document.getElementById('output-title').textContent = 'Quick Dispatch Result';
      document.getElementById('output-meta').innerHTML = [
        `<span>${statusBadge(t.status)}</span>`,
        `<span>Cost: ${costFmt(t.costUsd || 0)}</span>`,
        `<span>Model: ${esc(t.model)}</span>`,
        `<span>${formatDuration(result.durationMs || 0)}</span>`,
      ].join('');

      const tabFull = document.getElementById('tab-full');
      tabFull.style.display = t.outputFile ? '' : 'none';

      showOutputTab('output');
      document.getElementById('output-modal').classList.add('open');
    }

    toast(result.summary || 'Dispatch complete');
    setTimeout(refresh, 500);

  } catch (e) {
    if (dispatchTimer) { clearInterval(dispatchTimer); dispatchTimer = null; }
    closeDispatchModal();
    toast('Dispatch error: ' + e.message);
  }

  return false;
}

