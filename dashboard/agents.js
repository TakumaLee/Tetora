// === Agent Management ===

var _agentArchetypes = [];
var _agentRunning = {};  // name -> bool (is running)

async function refreshAgents() {
  var list = document.getElementById('agents-list');
  if (!list) return;
  list.innerHTML = '<div style="color:var(--muted);font-size:13px;padding:20px;text-align:center">Loading agents...</div>';

  try {
    var [roles, running] = await Promise.all([
      fetchJSON('/roles').catch(function() { return []; }),
      fetchJSON('/api/agents/running').catch(function() { return {}; }),
    ]);

    // Build running map: agent name -> array of tasks
    _agentRunning = {};
    if (running && typeof running === 'object') {
      Object.keys(running).forEach(function(k) {
        _agentRunning[k] = (running[k] || []).length > 0;
      });
    }

    if (!Array.isArray(roles) || roles.length === 0) {
      list.innerHTML = '<div style="color:var(--muted);font-size:13px;padding:40px;text-align:center">No agents configured.<br><br><button class="btn btn-add" onclick="openAgentModal()" style="padding:6px 16px">+ New Agent</button></div>';
      return;
    }

    // Sort alphabetically
    roles.sort(function(a, b) { return a.name.localeCompare(b.name); });

    var html = '<div class="agents-grid">';
    roles.forEach(function(r) {
      var isRunning = !!_agentRunning[r.name];
      var statusDot = isRunning
        ? '<span class="dot dot-green" title="Working" style="display:inline-block;width:8px;height:8px;border-radius:50%;background:var(--green);margin-right:6px;vertical-align:middle"></span>'
        : '<span class="dot dot-gray" title="Idle" style="display:inline-block;width:8px;height:8px;border-radius:50%;background:var(--muted);margin-right:6px;vertical-align:middle"></span>';
      var statusLabel = isRunning ? '<span style="color:var(--green);font-size:11px">Working</span>' : '<span style="color:var(--muted);font-size:11px">Idle</span>';
      var model = r.model || '—';
      var desc = esc(r.description || '');
      var preview = r.soulPreview ? esc(r.soulPreview.slice(0, 120)) + (r.soulPreview.length > 120 ? '…' : '') : '<span style="color:var(--muted)">No SOUL.md</span>';

      // Avatar: portrait image or gem-color fallback circle
      var gem = (typeof GEM_TEAM !== 'undefined' && GEM_TEAM[r.name]) || null;
      var avatarHtml;
      if (r.portraitURL) {
        var fallbackColor = gem ? gem.color : '#888';
        var initial = r.name.charAt(0).toUpperCase();
        avatarHtml = '<img class="agent-avatar" src="' + esc(r.portraitURL) + '" alt="' + esc(r.name) + '"'
          + ' onerror="this.style.display=\'none\';this.nextSibling.style.display=\'flex\'">'
          + '<span class="agent-avatar-fallback" style="display:none;background:' + fallbackColor + '">' + initial + '</span>';
      } else {
        var fallbackColor = gem ? gem.color : '#888';
        var initial = r.name.charAt(0).toUpperCase();
        avatarHtml = '<span class="agent-avatar-fallback" style="background:' + fallbackColor + '">' + initial + '</span>';
      }

      html += '<div class="agent-card" style="background:var(--surface);border:1px solid var(--border);border-radius:var(--panel-radius);padding:16px;display:flex;flex-direction:column;gap:10px">';
      html += '<div style="display:flex;align-items:center;justify-content:space-between;gap:8px">';
      html += '<div style="display:flex;align-items:center;gap:10px">' + avatarHtml + '<div style="display:flex;flex-direction:column;gap:2px"><div style="display:flex;align-items:center;gap:6px">' + statusDot + '<span style="font-weight:bold;font-size:14px">' + esc(r.name) + '</span></div></div></div>';
      html += '<div style="display:flex;align-items:center;gap:6px">' + statusLabel;
      html += '<button class="btn" onclick="openAgentModal(\'' + esc(r.name) + '\')" style="padding:3px 10px;font-size:11px">Edit</button>';
      html += '<button class="btn" onclick="deleteAgent(\'' + esc(r.name) + '\')" style="padding:3px 10px;font-size:11px;color:var(--red);border-color:var(--red)">Delete</button>';
      html += '</div></div>';

      html += '<div style="display:grid;grid-template-columns:1fr 1fr;gap:4px 12px;font-size:12px">';
      html += '<div><span style="color:var(--muted)">Model: </span>' + esc(model) + '</div>';
      html += '<div><span style="color:var(--muted)">Mode: </span>' + esc(r.permissionMode || 'default') + '</div>';
      if (desc) html += '<div style="grid-column:1/-1"><span style="color:var(--muted)">Description: </span>' + desc + '</div>';
      html += '</div>';

      html += '<div style="font-size:11px;color:var(--muted);background:var(--bg);border-radius:4px;padding:8px;font-family:var(--font-mono,monospace);white-space:pre-wrap;line-height:1.4;max-height:60px;overflow:hidden">' + preview + '</div>';
      html += '</div>'; // /agent-card
    });
    html += '</div>';
    list.innerHTML = html;
  } catch (e) {
    list.innerHTML = '<div style="color:var(--red);font-size:13px;padding:20px">Error: ' + esc(e.message) + '</div>';
  }
}

async function openAgentModal(editName) {
  // Load archetypes if not cached
  if (_agentArchetypes.length === 0) {
    try {
      _agentArchetypes = await fetchJSON('/roles/archetypes');
    } catch(e) { _agentArchetypes = []; }
  }

  var modal = document.getElementById('agent-modal');
  var form = document.getElementById('agent-form');
  form.reset();

  document.getElementById('agent-modal-title').textContent = editName ? 'Edit Agent' : 'New Agent';
  document.getElementById('af-mode').value = editName ? 'edit' : 'create';
  document.getElementById('af-name').disabled = !!editName;
  document.getElementById('af-name-row').style.display = editName ? 'none' : '';
  document.getElementById('af-archetype-row').style.display = editName ? 'none' : '';
  document.getElementById('af-submit').textContent = editName ? 'Save Changes' : 'Create Agent';

  // Populate archetype dropdown
  var archSel = document.getElementById('af-archetype');
  archSel.innerHTML = '<option value="">— custom —</option>';
  _agentArchetypes.forEach(function(a) {
    var opt = document.createElement('option');
    opt.value = a.name;
    opt.textContent = a.name + ' — ' + (a.description || '');
    archSel.appendChild(opt);
  });

  // Reset portrait state
  var previewEl = document.getElementById('af-portrait-preview');
  var deleteBtn = document.getElementById('af-portrait-delete');
  var fileInput = document.getElementById('af-portrait-file');
  if (fileInput) fileInput.value = '';

  if (editName) {
    // Load existing agent data
    try {
      var data = await fetchJSON('/roles/' + encodeURIComponent(editName));
      document.getElementById('af-name').value = editName;
      document.getElementById('af-model').value = data.model || '';
      document.getElementById('af-permission').value = data.permissionMode || '';
      document.getElementById('af-description').value = data.description || '';
      document.getElementById('af-soul').value = data.soulContent || '';

      // Load portrait preview
      if (previewEl) {
        var portraitURL = data.portraitURL || resolveAgentPortrait(editName);
        if (portraitURL) {
          previewEl.src = portraitURL;
          previewEl.style.display = 'block';
        } else {
          previewEl.style.display = 'none';
        }
        if (deleteBtn) deleteBtn.style.display = portraitURL ? '' : 'none';
      }
    } catch(e) {
      toast('Error loading agent: ' + e.message);
      return;
    }
  } else {
    if (previewEl) previewEl.style.display = 'none';
    if (deleteBtn) deleteBtn.style.display = 'none';
  }

  modal.style.display = 'flex';
}

function closeAgentModal() {
  document.getElementById('agent-modal').style.display = 'none';
}

function onArchetypeChange() {
  var sel = document.getElementById('af-archetype');
  var name = sel.value;
  if (!name) return;
  var arch = _agentArchetypes.find(function(a) { return a.name === name; });
  if (!arch) return;
  if (arch.model) document.getElementById('af-model').value = arch.model;
  if (arch.permissionMode) document.getElementById('af-permission').value = arch.permissionMode;
  if (arch.soulTemplate) document.getElementById('af-soul').value = arch.soulTemplate;
  // Pre-fill name field
  var nameField = document.getElementById('af-name');
  if (!nameField.value) nameField.value = name.toLowerCase().replace(/\s+/g, '-');
}

// Returns /dashboard/portraits/{name}.png if the built-in exists, else empty string.
// Used as a fallback when the API doesn't return portraitURL yet (e.g. during create).
function resolveAgentPortrait(name) {
  return '/dashboard/portraits/' + encodeURIComponent(name) + '.png';
}

async function submitAgentForm(e) {
  e.preventDefault();
  var mode = document.getElementById('af-mode').value;
  var name = document.getElementById('af-name').value.trim();
  var model = document.getElementById('af-model').value.trim();
  var permission = document.getElementById('af-permission').value.trim();
  var description = document.getElementById('af-description').value.trim();
  var soul = document.getElementById('af-soul').value;
  var fileInput = document.getElementById('af-portrait-file');

  if (mode === 'create' && !name) {
    toast('Name is required');
    return;
  }

  var btn = document.getElementById('af-submit');
  btn.disabled = true;
  btn.textContent = mode === 'create' ? 'Creating...' : 'Saving...';

  try {
    var payload = { model: model, permissionMode: permission, description: description, soulContent: soul };
    var resp;
    if (mode === 'create') {
      payload.name = name;
      resp = await fetch('/roles', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
    } else {
      resp = await fetch('/roles/' + encodeURIComponent(name), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
    }

    if (resp.ok) {
      // Upload portrait if a new file was selected
      if (fileInput && fileInput.files && fileInput.files.length > 0) {
        var agentName = mode === 'create' ? name : name;
        var fd = new FormData();
        fd.append('portrait', fileInput.files[0]);
        var upResp = await fetch('/api/agents/' + encodeURIComponent(agentName) + '/portrait', {
          method: 'POST', body: fd,
        });
        if (!upResp.ok) {
          var upData = await upResp.json().catch(function() { return {}; });
          toast('Portrait upload failed: ' + (upData.error || upResp.statusText));
        }
      }
      closeAgentModal();
      toast(mode === 'create' ? 'Agent created' : 'Agent updated');
      refreshAgents();
    } else {
      var data = await resp.json().catch(function() { return {}; });
      toast('Error: ' + (data.error || resp.statusText));
    }
  } catch(err) {
    toast('Error: ' + err.message);
  } finally {
    btn.disabled = false;
    btn.textContent = mode === 'create' ? 'Create Agent' : 'Save Changes';
  }
}

async function deleteAgentPortrait(agentName) {
  if (!confirm('Delete custom portrait for "' + agentName + '"? (Built-in portrait will be restored)')) return;
  try {
    var resp = await fetch('/api/agents/' + encodeURIComponent(agentName) + '/portrait', { method: 'DELETE' });
    if (resp.ok) {
      var data = await resp.json().catch(function() { return {}; });
      var previewEl = document.getElementById('af-portrait-preview');
      if (previewEl && data.portraitURL) {
        previewEl.src = data.portraitURL;
        previewEl.style.display = 'block';
      }
      toast('Custom portrait deleted');
      refreshAgents();
    } else {
      var data = await resp.json().catch(function() { return {}; });
      toast('Error: ' + (data.error || resp.statusText));
    }
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

async function deleteAgent(name) {
  if (!confirm('Delete agent "' + name + '"?\n\nThis will remove the agent from config. The SOUL.md file will remain on disk.')) return;
  try {
    var resp = await fetch('/roles/' + encodeURIComponent(name), { method: 'DELETE' });
    if (resp.ok) {
      toast('Agent "' + name + '" deleted');
      refreshAgents();
    } else {
      var data = await resp.json().catch(function() { return {}; });
      toast('Error: ' + (data.error || resp.statusText));
    }
  } catch(e) {
    toast('Error: ' + e.message);
  }
}
