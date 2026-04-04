// === Agent Management ===

var _agentArchetypes = [];
var _agentRunning = {};  // name -> bool (is running)
var _providerPresets = null; // cached presets from /api/provider-presets
var _inferenceMode = ''; // current global mode

function isLocalProvider(name) { return name === 'ollama' || name === 'lmstudio'; }

// --- Provider Presets Cache ---
// Cache expires after 30 seconds so dynamic providers (Ollama) get re-checked.
var _providerPresetsTime = 0;

async function getProviderPresets() {
  var now = Date.now();
  if (_providerPresets && (now - _providerPresetsTime) < 30000) return _providerPresets;
  try {
    _providerPresets = await fetchJSON('/api/provider-presets');
    _providerPresetsTime = now;
  } catch(e) { _providerPresets = []; }
  return _providerPresets;
}

// --- Inference Mode ---
async function refreshInferenceMode() {
  try {
    var data = await fetchJSON('/api/inference-mode');
    _inferenceMode = data.mode || 'mixed';
    var badge = document.getElementById('agents-mode-badge');
    var btn = document.getElementById('agents-mode-toggle');
    if (badge) {
      badge.textContent = data.cloud + ' cloud / ' + data.local + ' local';
      badge.style.borderColor = _inferenceMode === 'local' ? 'var(--green)' : 'var(--border)';
    }
    if (btn) {
      if (_inferenceMode === 'local') {
        btn.textContent = 'Switch to Cloud';
        btn.style.borderColor = 'var(--blue)';
      } else {
        btn.textContent = 'Switch to Local';
        btn.style.borderColor = 'var(--green)';
      }
    }
  } catch(e) {}
}

async function toggleInferenceMode() {
  var newMode = _inferenceMode === 'local' ? 'cloud' : 'local';
  var btn = document.getElementById('agents-mode-toggle');
  if (btn) { btn.disabled = true; btn.textContent = 'Switching...'; }
  try {
    var result = await fetchJSON('/api/inference-mode', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mode: newMode }),
    });
    if (result.errors && result.errors.length > 0) {
      toast('Switched ' + result.switched + ' agents, errors: ' + result.errors.join(', '));
    } else {
      toast('Switched ' + result.switched + ' agents to ' + newMode + (result.pinned > 0 ? ' (' + result.pinned + ' pinned)' : ''));
    }
    refreshAgents(); // refreshAgents() calls refreshInferenceMode() internally
  } catch(e) {
    toast('Error: ' + e.message);
  } finally {
    if (btn) btn.disabled = false;
  }
}

// --- Model Picker (Agent Editor) ---
async function populateModelPicker(currentModel, currentProvider) {
  var bar = document.getElementById('af-provider-bar');
  var sel = document.getElementById('af-model-select');
  var input = document.getElementById('af-model');
  var hidden = document.getElementById('af-provider');
  var status = document.getElementById('af-provider-status');
  if (!bar || !sel) return;

  var presets = await getProviderPresets();
  bar.innerHTML = '';

  presets.forEach(function(p) {
    if (p.name === 'custom') return;
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'btn';
    btn.textContent = p.displayName;
    btn.style.cssText = 'padding:3px 10px;font-size:11px;white-space:nowrap;';

    // Local providers get green tint
    if (isLocalProvider(p.name)) {
      btn.style.borderColor = 'var(--green)';
    }

    // Dim unavailable providers
    if (p.dynamic && !p.available) {
      btn.style.opacity = '0.4';
      btn.title = 'Offline';
    }

    // Highlight current provider
    if (p.name === currentProvider) {
      btn.style.background = 'var(--accent)';
      btn.style.color = 'var(--bg)';
      btn.style.borderColor = 'var(--accent)';
    }

    btn.onclick = function() {
      selectProvider(p, currentModel);
    };
    bar.appendChild(btn);
  });

  // Auto-select current provider
  if (currentProvider) {
    var preset = presets.find(function(p) { return p.name === currentProvider; });
    if (preset) selectProvider(preset, currentModel);
  }
}

function selectProvider(preset, currentModel) {
  var bar = document.getElementById('af-provider-bar');
  var sel = document.getElementById('af-model-select');
  var input = document.getElementById('af-model');
  var hidden = document.getElementById('af-provider');
  var status = document.getElementById('af-provider-status');

  // Update button highlights
  if (bar) {
    Array.from(bar.children).forEach(function(btn) {
      btn.style.background = '';
      btn.style.color = '';
      if (btn.textContent === preset.displayName) {
        btn.style.background = 'var(--accent)';
        btn.style.color = 'var(--bg)';
        btn.style.borderColor = 'var(--accent)';
      }
    });
  }

  // Set hidden provider
  if (hidden) hidden.value = preset.name;

  // Populate model dropdown
  var models = (preset.fetchedModels && preset.fetchedModels.length > 0)
    ? preset.fetchedModels
    : (preset.models || []);

  if (models.length > 0) {
    sel.innerHTML = '';
    models.forEach(function(m) {
      var opt = document.createElement('option');
      opt.value = m;
      opt.textContent = m;
      if (m === currentModel) opt.selected = true;
      sel.appendChild(opt);
    });
    // Add "Custom..." option
    var customOpt = document.createElement('option');
    customOpt.value = '__custom__';
    customOpt.textContent = '— type custom model —';
    sel.appendChild(customOpt);

    sel.style.display = '';
    // If current model matches, hide text input; otherwise show custom
    if (models.indexOf(currentModel) >= 0) {
      input.style.display = 'none';
      input.value = currentModel;
    } else if (currentModel) {
      sel.value = '__custom__';
      input.style.display = '';
      input.value = currentModel;
    } else {
      input.style.display = 'none';
      input.value = models[0];
      sel.selectedIndex = 0;
    }
  } else {
    sel.style.display = 'none';
    input.style.display = '';
  }

  // Show provider status
  if (status) {
    if (preset.dynamic && !preset.available) {
      status.innerHTML = '<span style="color:var(--red)">Offline</span>';
    } else if (preset.requiresKey) {
      status.innerHTML = '<span style="color:var(--muted)">API key required</span>';
    } else if (preset.name === 'ollama' || preset.name === 'lmstudio') {
      status.innerHTML = '<span style="color:var(--green)">Local — free</span>';
    } else {
      status.innerHTML = '';
    }
  }
}

function onModelSelectChange() {
  var sel = document.getElementById('af-model-select');
  var input = document.getElementById('af-model');
  if (sel.value === '__custom__') {
    input.style.display = '';
    input.value = '';
    input.focus();
  } else {
    input.style.display = 'none';
    input.value = sel.value;
  }
}

// --- Quick-Switch (Agent Card) ---
var _quickSwitchOpen = null;

function openQuickSwitch(el, event) {
  event.stopPropagation();
  closeQuickSwitch();
  var agentName = el.dataset.agent;
  var model = el.dataset.model;
  var providerName = el.dataset.prov;

  var anchor = event.target;
  var rect = anchor.getBoundingClientRect();

  var dd = document.createElement('div');
  dd.id = 'quick-switch-dropdown';
  dd.style.cssText = 'position:fixed;z-index:9999;background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:6px 0;min-width:200px;max-height:300px;overflow-y:auto;box-shadow:0 4px 16px rgba(0,0,0,0.3);font-size:12px;';
  dd.style.top = (rect.bottom + 4) + 'px';
  dd.style.left = rect.left + 'px';

  // Prevent clicks inside dropdown from closing it
  dd.addEventListener('click', function(e) { e.stopPropagation(); });

  function addItem(text, bold, onClick) {
    var item = document.createElement('div');
    item.style.cssText = 'padding:6px 12px;cursor:pointer;';
    if (bold) { item.style.color = 'var(--accent)'; item.style.fontWeight = 'bold'; }
    item.textContent = text;
    item.onmouseenter = function() { this.style.background = 'var(--hover)'; };
    item.onmouseleave = function() { this.style.background = ''; };
    item.addEventListener('click', function() {
      closeQuickSwitch();
      onClick();
    });
    dd.appendChild(item);
  }

  function addSeparator() {
    var sep = document.createElement('div');
    sep.style.cssText = 'border-top:1px solid var(--border);margin:4px 0';
    dd.appendChild(sep);
  }

  function addLabel(text) {
    var lbl = document.createElement('div');
    lbl.style.cssText = 'padding:4px 12px;color:var(--muted);font-size:10px;text-transform:uppercase';
    lbl.textContent = text;
    dd.appendChild(lbl);
  }

  // Quick actions
  addLabel('Quick Actions');
  var switchLabel = isLocalProvider(providerName) ? '☁ Switch to Cloud' : '🏠 Switch to Local';
  var switchMode = isLocalProvider(providerName) ? 'cloud' : 'local';
  addItem(switchLabel, false, function() { quickSwitchMode(agentName, switchMode); });

  addSeparator();
  addLabel('Models');

  // Load models for current provider
  getProviderPresets().then(function(presets) {
    var preset = presets.find(function(p) { return p.name === providerName; });
    if (!preset) return;
    var models = (preset.fetchedModels && preset.fetchedModels.length > 0)
      ? preset.fetchedModels : (preset.models || []);

    models.forEach(function(m) {
      var modelName = m; // capture
      addItem(m, m === model, function() { quickSwitchModel(agentName, modelName, providerName); });
    });

    addSeparator();
    addItem('More providers...', false, function() { openAgentModal(agentName); });
  });

  document.body.appendChild(dd);
  _quickSwitchOpen = dd;

  // Close on outside click
  setTimeout(function() {
    document.addEventListener('click', closeQuickSwitch, { once: true });
  }, 10);
}

function closeQuickSwitch() {
  if (_quickSwitchOpen) {
    _quickSwitchOpen.remove();
    _quickSwitchOpen = null;
  }
}

async function quickSwitchModel(agentName, model, providerName) {
  try {
    await fetchJSON('/roles/' + encodeURIComponent(agentName), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: model, provider: providerName }),
    });
    toast(agentName + ' → ' + model);
    refreshAgents();
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

async function quickSwitchMode(agentName, mode) {
  // Use the inference-mode endpoint logic but for a single agent.
  // We simulate it by directly PUTting the agent with appropriate model.
  if (mode === 'local') {
    var presets = await getProviderPresets();
    var ollama = presets.find(function(p) { return p.name === 'ollama'; });
    if (!ollama || !ollama.available) {
      toast('Ollama is offline');
      return;
    }
    var models = (ollama.fetchedModels && ollama.fetchedModels.length > 0)
      ? ollama.fetchedModels : [];
    if (models.length === 0) {
      toast('No Ollama models available');
      return;
    }
    quickSwitchModel(agentName, models[0], 'ollama');
  } else {
    // Switch to cloud — open editor so user can pick
    openAgentModal(agentName);
  }
}

async function refreshAgents() {
  refreshInferenceMode();
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

      // Provider badge
      var prov = r.provider || '';
      var isLocal = (isLocalProvider(prov));
      var provBadge = isLocal
        ? '<span style="display:inline-block;font-size:10px;padding:1px 6px;border-radius:8px;background:var(--green);color:#000;margin-left:6px">LOCAL</span>'
        : (prov ? '<span style="display:inline-block;font-size:10px;padding:1px 6px;border-radius:8px;background:var(--blue);color:#fff;margin-left:6px">CLOUD</span>' : '');

      html += '<div style="display:grid;grid-template-columns:1fr 1fr;gap:4px 12px;font-size:12px">';
      html += '<div><span style="color:var(--muted)">Model: </span><span class="model-quickswitch" style="cursor:pointer;text-decoration:underline dotted;text-underline-offset:2px" data-agent="' + esc(r.name) + '" data-model="' + esc(model) + '" data-prov="' + esc(prov) + '" onclick="openQuickSwitch(this,event)">' + esc(model) + ' ▾</span>' + provBadge + '</div>';
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

  // Reset provider picker state
  document.getElementById('af-provider').value = '';
  document.getElementById('af-provider-bar').innerHTML = '';
  document.getElementById('af-model-select').style.display = 'none';
  document.getElementById('af-model').style.display = '';
  document.getElementById('af-provider-status').innerHTML = '';

  if (editName) {
    // Load existing agent data
    try {
      var data = await fetchJSON('/roles/' + encodeURIComponent(editName));
      document.getElementById('af-name').value = editName;
      document.getElementById('af-model').value = data.model || '';
      document.getElementById('af-provider').value = data.provider || '';
      document.getElementById('af-permission').value = data.permissionMode || '';
      document.getElementById('af-description').value = data.description || '';
      document.getElementById('af-soul').value = data.soulContent || '';

      // Populate model picker with current provider
      populateModelPicker(data.model || '', data.provider || '');

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
    // Populate model picker for new agent (no current selection)
    populateModelPicker('', '');
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
  var providerVal = document.getElementById('af-provider').value.trim();
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
    var payload = { model: model, provider: providerVal, permissionMode: permission, description: description, soulContent: soul };
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
