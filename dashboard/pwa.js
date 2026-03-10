// --- PWA ---
if ('serviceWorker' in navigator) {
  // Nuclear: unregister ALL existing service workers and clear ALL caches first
  navigator.serviceWorker.getRegistrations().then(function(regs) {
    regs.forEach(function(reg) { reg.unregister(); });
  });
  if ('caches' in window) {
    caches.keys().then(function(keys) {
      keys.forEach(function(k) { caches.delete(k); });
    });
  }
  // Re-register after a brief delay to ensure clean state
  setTimeout(function() {
    navigator.serviceWorker.register('/dashboard/sw.js', { scope: '/' })
      .then(function(reg) {
        reg.addEventListener('updatefound', function() {
          var nw = reg.installing;
          nw.addEventListener('statechange', function() {
            if (nw.state === 'activated') window.location.reload();
          });
        });
      })
      .catch(function() {});
  }, 500);
}

var deferredInstallPrompt = null;
window.addEventListener('beforeinstallprompt', function(e) {
  e.preventDefault();
  deferredInstallPrompt = e;
  var btn = document.getElementById('pwa-install-btn');
  if (btn) btn.style.display = 'inline-block';
});
window.addEventListener('appinstalled', function() {
  deferredInstallPrompt = null;
  var btn = document.getElementById('pwa-install-btn');
  if (btn) btn.style.display = 'none';
  toast('App installed successfully');
});
function pwaInstall() {
  if (!deferredInstallPrompt) return;
  deferredInstallPrompt.prompt();
  deferredInstallPrompt.userChoice.then(function() {
    deferredInstallPrompt = null;
    var btn = document.getElementById('pwa-install-btn');
    if (btn) btn.style.display = 'none';
  });
}

// --- P22.4: Integrations ---

function refreshIntegrations() {
  fetch('/api/integrations/status')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      renderChannelStatus(data.channels || []);
      renderOAuthStatus(data.oauthServices || []);
      renderKnowledgeStatus(data.knowledgeDocs);
      renderBrowserRelayStatus(data.browserRelay);
      if (data.homeAssistant !== 'not_configured') {
        document.getElementById('integration-ha-section').style.display = '';
        renderHAStatus(data.homeAssistant);
      }
    })
    .catch(function(e) { toast('Failed to load integrations: ' + e); });

  // Also load reminders and triggers.
  refreshReminders();
  refreshTriggers();
}

function renderChannelStatus(channels) {
  var el = document.getElementById('integration-channels');
  if (!channels.length) { el.innerHTML = '<div style="color:var(--muted);padding:12px">No channels configured</div>'; return; }
  var html = '<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:8px">';
  channels.forEach(function(ch) {
    var color = ch.status === 'connected' ? 'var(--green)' : ch.status === 'not_configured' ? 'var(--muted)' : 'var(--red)';
    var icon = ch.status === 'connected' ? '&#9679;' : ch.status === 'not_configured' ? '&#9675;' : '&#9888;';
    html += '<div style="background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:12px">';
    html += '<div style="display:flex;justify-content:space-between;align-items:center">';
    html += '<span style="font-weight:600;text-transform:capitalize">' + ch.name + '</span>';
    html += '<span style="color:' + color + ';font-size:12px">' + icon + ' ' + ch.status.replace('_',' ') + '</span>';
    html += '</div></div>';
  });
  html += '</div>';
  el.innerHTML = html;
}

function renderOAuthStatus(services) {
  var el = document.getElementById('integration-oauth');
  if (!services || !services.length) { el.innerHTML = '<div style="color:var(--muted);padding:12px">No OAuth services configured</div>'; return; }
  var html = '<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:8px">';
  services.forEach(function(svc) {
    var connected = svc.connected;
    var color = connected ? 'var(--green)' : 'var(--yellow)';
    var status = connected ? 'Connected' : 'Not Connected';
    html += '<div style="background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:12px">';
    html += '<div style="font-weight:600;text-transform:capitalize">' + svc.name + '</div>';
    html += '<div style="font-size:12px;color:' + color + ';margin-top:4px">' + status + '</div>';
    if (svc.scopes) html += '<div style="font-size:11px;color:var(--muted);margin-top:2px">' + svc.scopes + '</div>';
    html += '</div>';
  });
  html += '</div>';
  el.innerHTML = html;
}

function refreshReminders() {
  fetch('/api/reminders')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var el = document.getElementById('integration-reminders');
      var reminders = Array.isArray(data) ? data : (data.reminders || []);
      if (!reminders.length) { el.innerHTML = '<div style="color:var(--muted);padding:12px">No active reminders</div>'; return; }
      var html = '<div class="table-wrap"><table><thead><tr><th>Message</th><th>Due</th><th>Repeat</th><th>Channel</th><th>Actions</th></tr></thead><tbody>';
      reminders.forEach(function(r) {
        html += '<tr>';
        html += '<td>' + (r.message || r.text || '').substring(0, 50) + '</td>';
        html += '<td style="font-size:12px">' + (r.dueTime || r.dueAt || '') + '</td>';
        html += '<td style="font-size:12px">' + (r.recurring || 'once') + '</td>';
        html += '<td style="font-size:12px">' + (r.channel || 'any') + '</td>';
        html += '<td><button class="btn" style="font-size:11px;padding:2px 8px" onclick="cancelReminder(\'' + r.id + '\')">Cancel</button></td>';
        html += '</tr>';
      });
      html += '</tbody></table></div>';
      el.innerHTML = html;
      mirrorToSettings('integration-reminders');
    })
    .catch(function() {
      document.getElementById('integration-reminders').innerHTML = '<div style="color:var(--muted);padding:12px">Could not load reminders</div>';
    });
}

function cancelReminder(id) {
  if (!confirm('Cancel this reminder?')) return;
  fetch('/api/reminders/' + id, {method: 'DELETE'})
    .then(function() { toast('Reminder cancelled'); refreshReminders(); })
    .catch(function(e) { toast('Error: ' + e); });
}

function refreshTriggers() {
  fetch('/api/triggers')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var el = document.getElementById('integration-triggers');
      var triggers = Array.isArray(data) ? data : (data.triggers || []);
      if (!triggers.length) { el.innerHTML = '<div style="color:var(--muted);padding:12px">No workflow triggers configured. Click "+ Create" to add one.</div>'; return; }
      var html = '';
      triggers.forEach(function(t) {
        var typeBadge = t.type === 'cron' ? '&#9200; cron' : t.type === 'event' ? '&#9889; event' : '&#128279; webhook';
        var typeColor = t.type === 'cron' ? '#60a5fa' : t.type === 'event' ? '#fbbf24' : '#a78bfa';
        var detail = '';
        if (t.type === 'cron' && t.nextCron) {
          detail = 'Next: ' + t.nextCron.substring(0, 19).replace('T', ' ');
        } else if (t.type === 'webhook') {
          var webhookUrl = location.origin + '/api/triggers/webhook/' + encodeURIComponent(t.name);
          detail = '<code style="font-size:10px;cursor:pointer" onclick="event.stopPropagation();copyText(\'' + webhookUrl.replace(/'/g, "\\'") + '\')" title="Click to copy">' + esc(webhookUrl) + '</code>';
        } else if (t.type === 'event') {
          detail = 'Pattern: ' + esc(t.name);
        }
        var cooldownInfo = t.cooldownLeft ? ' <span style="color:var(--muted);font-size:11px">&#9203; ' + esc(t.cooldownLeft) + '</span>' : '';
        var lastFiredInfo = t.lastFired ? '<div style="font-size:11px;color:var(--muted)">Last: ' + t.lastFired.substring(0, 19).replace('T', ' ') + '</div>' : '';
        html += '<div style="background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:12px;margin-bottom:8px">';
        html += '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">';
        html += '<div><strong>' + esc(t.name) + '</strong> <span style="background:' + typeColor + '22;color:' + typeColor + ';padding:2px 6px;border-radius:4px;font-size:11px">' + typeBadge + '</span>' + cooldownInfo + '</div>';
        html += '<label style="cursor:pointer;font-size:12px"><input type="checkbox" ' + (t.enabled ? 'checked' : '') + ' onchange="toggleTrigger(\'' + escAttr(t.name) + '\')" style="cursor:pointer"> Enabled</label>';
        html += '</div>';
        html += '<div style="font-size:12px;color:var(--muted);margin-bottom:4px">&#8594; <strong>' + esc(t.workflowName) + '</strong>' + (t.cooldown ? ' · cooldown: ' + esc(t.cooldown) : '') + '</div>';
        if (detail) html += '<div style="font-size:12px;margin-bottom:4px">' + detail + '</div>';
        html += lastFiredInfo;
        html += '<div style="display:flex;gap:4px;margin-top:8px">';
        html += '<button class="btn" style="font-size:11px;padding:2px 8px" onclick="fireTrigger(\'' + escAttr(t.name) + '\')">&#9654; Fire Now</button>';
        html += '<button class="btn" style="font-size:11px;padding:2px 8px" onclick="openTriggerModal(\'' + escAttr(t.name) + '\')">Edit</button>';
        html += '<button class="btn btn-danger" style="font-size:11px;padding:2px 8px" onclick="deleteTrigger(\'' + escAttr(t.name) + '\')">Delete</button>';
        html += '<button class="btn" style="font-size:11px;padding:2px 8px" onclick="showTriggerRuns(\'' + escAttr(t.name) + '\')">History</button>';
        html += '</div></div>';
      });
      el.innerHTML = html;
      mirrorToSettings('integration-triggers');
    })
    .catch(function() {
      document.getElementById('integration-triggers').innerHTML = '<div style="color:var(--muted);padding:12px">Could not load triggers</div>';
    });
}

function toggleTrigger(name) {
  fetch('/api/triggers/' + encodeURIComponent(name) + '/toggle', {method:'POST'})
    .then(function(r) { return r.json(); })
    .then(function(data) { toast('Trigger ' + name + ': ' + (data.enabled ? 'enabled' : 'disabled')); refreshTriggers(); })
    .catch(function(e) { toast('Toggle failed: ' + e); });
}

function fireTrigger(name) {
  fetch('/api/triggers/' + encodeURIComponent(name) + '/fire', {method:'POST'})
    .then(function(r) { return r.json(); })
    .then(function() { toast('Trigger fired: ' + name); })
    .catch(function(e) { toast('Fire failed: ' + e); });
}

function deleteTrigger(name) {
  if (!confirm('Delete trigger "' + name + '"?')) return;
  fetch('/api/triggers/' + encodeURIComponent(name), {method:'DELETE'})
    .then(function(r) { return r.json(); })
    .then(function() { toast('Trigger deleted'); refreshTriggers(); })
    .catch(function(e) { toast('Delete failed: ' + e); });
}

function showTriggerRuns(name) {
  fetch('/api/triggers/' + encodeURIComponent(name) + '/runs?limit=10')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var runs = data.runs || [];
      if (!runs.length) { toast('No runs for ' + name); return; }
      var html = '<div style="margin-top:8px;padding:8px;background:var(--bg);border-radius:6px;border:1px solid var(--border)">';
      html += '<div style="font-size:12px;font-weight:600;margin-bottom:6px">Recent Runs for ' + esc(name) + '</div>';
      html += '<table style="width:100%;font-size:11px"><thead><tr><th>Status</th><th>Workflow</th><th>Started</th><th>Run ID</th></tr></thead><tbody>';
      runs.forEach(function(r) {
        var sc = r.status === 'success' ? 'var(--green)' : r.status === 'error' ? '#f87171' : 'var(--muted)';
        html += '<tr><td><span style="color:' + sc + '">' + esc(r.status || '') + '</span></td>';
        html += '<td>' + esc(r.workflow_name || '') + '</td>';
        html += '<td>' + esc((r.started_at||'').substring(0,19).replace('T',' ')) + '</td>';
        html += '<td><code style="font-size:10px">' + esc((r.workflow_run_id||'').substring(0,8)) + '</code></td></tr>';
      });
      html += '</tbody></table></div>';
      var el = document.getElementById('integration-triggers');
      el.innerHTML += html;
    })
    .catch(function(e) { toast('Load runs failed: ' + e); });
}

function copyText(text) {
  if (navigator.clipboard) {
    navigator.clipboard.writeText(text).then(function() { toast('Copied!'); });
  } else {
    var ta = document.createElement('textarea');
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    document.body.removeChild(ta);
    toast('Copied!');
  }
}

var _editingTriggerName = null;

function openTriggerModal(editName) {
  _editingTriggerName = editName || null;
  document.getElementById('trigger-modal-title').textContent = editName ? 'Edit Trigger' : 'Create Trigger';
  document.getElementById('trigger-modal').style.display = 'flex';
  // Load workflows for dropdown
  fetch('/workflows').then(function(r) { return r.json(); }).then(function(wfs) {
    var sel = document.getElementById('trig-workflow');
    sel.innerHTML = '';
    (wfs || []).forEach(function(w) {
      sel.innerHTML += '<option value="' + escAttr(w.name) + '">' + esc(w.name) + '</option>';
    });
  });
  if (editName) {
    fetch('/api/triggers').then(function(r) { return r.json(); }).then(function(data) {
      var triggers = data.triggers || [];
      var t = triggers.find(function(tr) { return tr.name === editName; });
      if (!t) return;
      document.getElementById('trig-name').value = t.name;
      document.getElementById('trig-name').disabled = true;
      document.getElementById('trig-type').value = t.type;
      setTimeout(function() { document.getElementById('trig-workflow').value = t.workflowName; }, 100);
      document.getElementById('trig-cooldown').value = t.cooldown || '';
      document.getElementById('trig-enabled').checked = t.enabled;
      updateTriggerTypeFields();
    });
  } else {
    document.getElementById('trig-name').value = '';
    document.getElementById('trig-name').disabled = false;
    document.getElementById('trig-type').value = 'cron';
    document.getElementById('trig-cron').value = '';
    document.getElementById('trig-tz').value = '';
    document.getElementById('trig-event').value = '';
    document.getElementById('trig-webhook').value = '';
    document.getElementById('trig-cooldown').value = '';
    document.getElementById('trig-vars').value = '';
    document.getElementById('trig-enabled').checked = true;
    updateTriggerTypeFields();
  }
}

function closeTriggerModal() {
  document.getElementById('trigger-modal').style.display = 'none';
  _editingTriggerName = null;
}

function updateTriggerTypeFields() {
  var type = document.getElementById('trig-type').value;
  document.getElementById('trig-cron-fields').style.display = type === 'cron' ? '' : 'none';
  document.getElementById('trig-event-fields').style.display = type === 'event' ? '' : 'none';
  document.getElementById('trig-webhook-fields').style.display = type === 'webhook' ? '' : 'none';
}

function saveTrigger() {
  var name = document.getElementById('trig-name').value.trim();
  var type = document.getElementById('trig-type').value;
  var payload = {
    name: name,
    workflowName: document.getElementById('trig-workflow').value,
    enabled: document.getElementById('trig-enabled').checked,
    trigger: { type: type },
    cooldown: document.getElementById('trig-cooldown').value.trim()
  };
  if (type === 'cron') {
    payload.trigger.cron = document.getElementById('trig-cron').value.trim();
    payload.trigger.tz = document.getElementById('trig-tz').value.trim();
  } else if (type === 'event') {
    payload.trigger.event = document.getElementById('trig-event').value.trim();
  } else if (type === 'webhook') {
    payload.trigger.webhook = document.getElementById('trig-webhook').value.trim();
  }
  var varsText = document.getElementById('trig-vars').value.trim();
  if (varsText) {
    try { payload.variables = JSON.parse(varsText); } catch(e) { toast('Invalid JSON in variables'); return; }
  }
  var url, method;
  if (_editingTriggerName) {
    url = '/api/triggers/' + encodeURIComponent(_editingTriggerName);
    method = 'PUT';
  } else {
    url = '/api/triggers';
    method = 'POST';
  }
  fetch(url, {
    method: method,
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(payload)
  }).then(function(r) {
    if (!r.ok) return r.json().then(function(d) { throw new Error((d.errors || [d.error]).join(', ')); });
    return r.json();
  }).then(function() {
    toast(_editingTriggerName ? 'Trigger updated' : 'Trigger created');
    closeTriggerModal();
    refreshTriggers();
  }).catch(function(e) {
    toast('Save failed: ' + e.message);
  });
}

// Mirror content to settings-sub-integrations duplicate panels.
function mirrorToSettings(primaryId) {
  var src = document.getElementById(primaryId);
  var dst = document.getElementById('stg-' + primaryId);
  if (src && dst) dst.innerHTML = src.innerHTML;
}

function renderKnowledgeStatus(count) {
  var el = document.getElementById('integration-knowledge');
  el.innerHTML = '<div style="background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:16px">' +
    '<div class="stat-label">INDEXED DOCUMENTS</div>' +
    '<div class="stat-value">' + (count || 0) + '</div></div>';
  mirrorToSettings('integration-knowledge');
}

function renderBrowserRelayStatus(status) {
  // Included in channel status area
}

function renderHAStatus(status) {
  var el = document.getElementById('integration-ha');
  var color = status === 'connected' ? 'var(--green)' : 'var(--muted)';
  el.innerHTML = '<div style="background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:16px">' +
    '<div style="display:flex;justify-content:space-between;align-items:center">' +
    '<span class="stat-label">HOME ASSISTANT</span>' +
    '<span style="color:' + color + ';font-size:12px">&#9679; ' + status + '</span>' +
    '</div></div>';
  mirrorToSettings('integration-ha');
  var stgSec = document.getElementById('stg-integration-ha-section');
  if (stgSec) stgSec.style.display = '';
}

// --- P22.5: Settings ---

function refreshSettings() {
  document.getElementById('settings-loading').style.display = '';
  document.getElementById('settings-container').style.display = 'none';

  fetch('/api/config/summary')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      document.getElementById('settings-loading').style.display = 'none';
      document.getElementById('settings-container').style.display = '';
      renderSettingsGeneral(data.general || {});
      renderSettingsChannels(data.channels || {});
      renderSettingsIntegrations(data.integrations || {});
      renderSettingsTools(data.tools || {});
      renderSettingsBudgets(data.budgets || {});
      renderSettingsSecurity(data.security || {});
      renderSettingsHeartbeat(data.heartbeat || {});
      renderSettingsTaskBoard(data.taskBoard || {});
      refreshNotificationsTable();
      refreshDiscordSettings();
      refreshHooksStatus();
    })
    .catch(function(e) {
      document.getElementById('settings-loading').innerHTML = '<span style="color:var(--red)">Failed to load: ' + e + '</span>';
    });
}

// --- v3: Claude Code Hooks Settings ---

function refreshHooksStatus() {
  fetch(API + '/api/hooks/install-status').then(function(r){return r.json()}).then(function(d) {
    var el = document.getElementById('hooks-status-display');
    var installBtn = document.getElementById('hooks-install-btn');
    var removeBtn = document.getElementById('hooks-remove-btn');
    if (d.installed) {
      el.innerHTML = '<span style="color:var(--green)">Installed</span>' +
        (d.hookCount ? ' &middot; ' + d.hookCount + ' hook(s) active' : '') +
        (d.eventCount ? ' &middot; ' + d.eventCount + ' events received' : '') +
        (d.mcpBridge ? '<br><span style="color:var(--green)">MCP Bridge</span> configured' : '');
      installBtn.textContent = 'Reinstall';
      installBtn.style.background = 'var(--accent)';
      removeBtn.style.display = '';
    } else {
      el.innerHTML = '<span style="color:var(--yellow)">Not installed</span> &mdash; Claude Code hooks are not configured yet';
      installBtn.textContent = 'Install Hooks';
      installBtn.style.background = 'var(--green)';
      removeBtn.style.display = 'none';
    }
  }).catch(function() {
    document.getElementById('hooks-status-display').innerHTML = '<span style="color:var(--muted)">Could not check status</span>';
  });
}

function installHooksFromDashboard() {
  var btn = document.getElementById('hooks-install-btn');
  var result = document.getElementById('hooks-result');
  btn.disabled = true;
  btn.textContent = 'Installing...';
  result.style.display = 'none';

  fetch(API + '/api/hooks/install', {method:'POST', headers:{'Content-Type':'application/json'}})
    .then(function(r){return r.json()})
    .then(function(d) {
      btn.disabled = false;
      result.style.display = '';
      if (d.error) {
        result.innerHTML = '<span style="color:var(--red)">Error: ' + esc(d.error) + '</span>';
      } else {
        result.innerHTML = '<span style="color:var(--green)">Hooks installed successfully!</span>' +
          (d.mcpBridge ? '<br>MCP bridge config: ' + esc(d.mcpBridge) : '');
      }
      refreshHooksStatus();
    })
    .catch(function(err) {
      btn.disabled = false;
      result.style.display = '';
      result.innerHTML = '<span style="color:var(--red)">Failed: ' + esc(String(err)) + '</span>';
    });
}

function removeHooksFromDashboard() {
  if (!confirm('Remove Tetora hooks from Claude Code settings?')) return;
  var btn = document.getElementById('hooks-remove-btn');
  var result = document.getElementById('hooks-result');
  btn.disabled = true;
  result.style.display = 'none';

  fetch(API + '/api/hooks/remove', {method:'POST', headers:{'Content-Type':'application/json'}})
    .then(function(r){return r.json()})
    .then(function(d) {
      btn.disabled = false;
      result.style.display = '';
      if (d.error) {
        result.innerHTML = '<span style="color:var(--red)">Error: ' + esc(d.error) + '</span>';
      } else {
        result.innerHTML = '<span style="color:var(--green)">Hooks removed.</span>';
      }
      refreshHooksStatus();
    })
    .catch(function(err) {
      btn.disabled = false;
      result.style.display = '';
      result.innerHTML = '<span style="color:var(--red)">Failed: ' + esc(String(err)) + '</span>';
    });
}

function renderSettingsKV(el, items) {
  var html = '<div style="display:grid;grid-template-columns:1fr 1fr;gap:1px;background:var(--border);border:1px solid var(--border);border-radius:8px;overflow:hidden">';
  items.forEach(function(item) {
    html += '<div style="background:var(--surface);padding:10px 14px;font-size:13px;color:var(--muted)">' + item.label + '</div>';
    html += '<div style="background:var(--surface);padding:10px 14px;font-size:13px;font-family:monospace">' + item.value + '</div>';
  });
  html += '</div>';
  el.innerHTML = html;
}

function renderSettingsGeneral(data) {
  renderSettingsKV(document.getElementById('settings-general'), [
    {label: 'Listen Address', value: data.listenAddr || 'default'},
    {label: 'Max Concurrent', value: data.maxConcurrent || 3},
    {label: 'Default Model', value: data.defaultModel || 'sonnet'},
    {label: 'Default Timeout', value: data.defaultTimeout || '15m'},
    {label: 'API Token', value: data.apiToken || 'not set'},
    {label: 'TLS Enabled', value: data.tlsEnabled ? 'Yes' : 'No'}
  ]);
}

function renderSettingsChannels(data) {
  var items = [];
  Object.keys(data).forEach(function(k) {
    items.push({label: k.charAt(0).toUpperCase() + k.slice(1), value: data[k] ? '<span style="color:var(--green)">Enabled</span>' : '<span style="color:var(--muted)">Disabled</span>'});
  });
  renderSettingsKV(document.getElementById('settings-channels'), items);
}

function renderSettingsIntegrations(data) {
  var items = [];
  Object.keys(data).forEach(function(k) {
    var v = data[k];
    if (typeof v === 'object') {
      items.push({label: k, value: v.enabled ? '<span style="color:var(--green)">Enabled</span>' : '<span style="color:var(--muted)">Disabled</span>'});
    } else {
      items.push({label: k, value: v ? '<span style="color:var(--green)">Enabled</span>' : '<span style="color:var(--muted)">Disabled</span>'});
    }
  });
  renderSettingsKV(document.getElementById('settings-integrations'), items);
}

function renderSettingsTools(data) {
  renderSettingsKV(document.getElementById('settings-tools'), [
    {label: 'Total Registered', value: data.totalRegistered || 0}
  ]);
}

function renderSettingsBudgets(data) {
  renderSettingsKV(document.getElementById('settings-budgets'), [
    {label: 'Daily Limit', value: data.dailyLimit ? '$' + data.dailyLimit.toFixed(2) : 'None'},
    {label: 'Weekly Limit', value: data.weeklyLimit ? '$' + data.weeklyLimit.toFixed(2) : 'None'},
    {label: 'Action', value: data.action || 'warn'}
  ]);
}

function renderSettingsSecurity(data) {
  renderSettingsKV(document.getElementById('settings-security'), [
    {label: 'TLS', value: data.tlsEnabled ? '<span style="color:var(--green)">Enabled</span>' : '<span style="color:var(--muted)">Disabled</span>'},
    {label: 'Rate Limiting', value: data.rateLimit ? '<span style="color:var(--green)">Enabled</span> (' + data.rateLimitMax + '/min)' : '<span style="color:var(--muted)">Disabled</span>'},
    {label: 'IP Allowlist', value: data.ipAllowlist > 0 ? data.ipAllowlist + ' entries' : 'None'},
    {label: 'Dashboard Auth', value: data.dashboardAuth ? '<span style="color:var(--green)">Enabled</span>' : '<span style="color:var(--muted)">Disabled</span>'}
  ]);
}

function renderSettingsHeartbeat(data) {
  var el = document.getElementById('settings-heartbeat');
  if (!el) return;
  var makeToggle = function(label, key, enabled) {
    var color = enabled ? 'var(--green)' : 'var(--muted)';
    var text = enabled ? 'Enabled' : 'Disabled';
    return '<div style="background:var(--surface);padding:10px 14px;font-size:13px;color:var(--muted)">' + label + '</div>' +
      '<div style="background:var(--surface);padding:8px 14px;display:flex;align-items:center;gap:8px">' +
        '<span style="color:' + color + ';font-size:13px">' + text + '</span>' +
        '<button class="btn" onclick="toggleConfigKey(\'' + key + '\',' + !enabled + ')" style="padding:2px 10px;font-size:11px">' + (enabled ? 'Disable' : 'Enable') + '</button>' +
      '</div>';
  };
  var makeRow = function(label, value) {
    return '<div style="background:var(--surface);padding:10px 14px;font-size:13px;color:var(--muted)">' + label + '</div>' +
      '<div style="background:var(--surface);padding:10px 14px;font-size:13px;font-family:monospace">' + value + '</div>';
  };
  var html = '<div style="display:grid;grid-template-columns:1fr 1fr;gap:1px;background:var(--border);border:1px solid var(--border);border-radius:8px;overflow:hidden">';
  html += makeToggle('Heartbeat Monitor', 'heartbeat.enabled', data.enabled);
  html += makeToggle('Auto-Cancel Stalled', 'heartbeat.autoCancel', data.autoCancel);
  html += makeToggle('Notify on Stall', 'heartbeat.notifyOnStall', data.notifyOnStall);
  html += makeRow('Check Interval', data.interval || '30s');
  html += makeRow('Stall Threshold', data.stallThreshold || '5m');
  html += makeRow('Timeout Warn Ratio', (data.timeoutWarnRatio || 0.8) * 100 + '%');
  html += '</div>';
  el.innerHTML = html;
}

function renderSettingsTaskBoard(data) {
  var el = document.getElementById('settings-taskboard');
  if (!el) return;
  var makeToggle = function(label, key, enabled) {
    var color = enabled ? 'var(--green)' : 'var(--muted)';
    var text = enabled ? 'Enabled' : 'Disabled';
    return '<div style="background:var(--surface);padding:10px 14px;font-size:13px;color:var(--muted)">' + label + '</div>' +
      '<div style="background:var(--surface);padding:8px 14px;display:flex;align-items:center;gap:8px">' +
        '<span style="color:' + color + ';font-size:13px">' + text + '</span>' +
        '<button class="btn" onclick="toggleConfigKey(\'' + key + '\',' + !enabled + ')" style="padding:2px 10px;font-size:11px">' + (enabled ? 'Disable' : 'Enable') + '</button>' +
      '</div>';
  };
  var html = '<div style="display:grid;grid-template-columns:1fr 1fr;gap:1px;background:var(--border);border:1px solid var(--border);border-radius:8px;overflow:hidden">';
  html += makeToggle('Task Board', 'taskBoard.enabled', data.enabled);
  html += makeToggle('Auto-Dispatch', 'taskBoard.autoDispatch.enabled', data.autoDispatch);
  html += '<div style="background:var(--surface);padding:10px 14px;font-size:13px;color:var(--muted)">Max Retries</div>';
  html += '<div style="background:var(--surface);padding:10px 14px;font-size:13px;font-family:monospace">' + (data.maxRetries || 3) + '</div>';
  html += '</div>';
  el.innerHTML = html;
}

function toggleConfigKey(key, value) {
  fetch('/api/config/toggle', {
    method: 'PATCH',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({key: key, value: value})
  })
  .then(function(r) { return r.json(); })
  .then(function(data) {
    if (data.status === 'ok') {
      refreshSettings();
    } else {
      alert('Error: ' + (data.error || 'unknown'));
    }
  })
  .catch(function(e) { alert('Error: ' + e); });
}

// --- Discord Settings ---

function refreshDiscordSettings() {
  fetch('/api/settings/discord')
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var cb = document.getElementById('discord-show-progress');
      if (cb) cb.checked = !!data.showProgress;
    })
    .catch(function() {});
}

function toggleDiscordShowProgress(enabled) {
  fetch('/api/settings/discord', {
    method: 'PATCH',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({showProgress: enabled})
  })
  .then(function(r) { return r.json(); })
  .then(function(data) {
    var cb = document.getElementById('discord-show-progress');
    if (cb) cb.checked = !!data.showProgress;
  })
  .catch(function(e) { alert('Error: ' + e); });
}

// --- Discord Notification Channels ---

function refreshNotificationsTable() {
  var loadingEl = document.getElementById('settings-notifications-loading');
  var tableEl = document.getElementById('settings-notifications-table');
  if (!loadingEl || !tableEl) return;

  loadingEl.style.display = '';
  tableEl.style.display = 'none';

  fetch('/api/discord/channels')
    .then(function(r) { return r.json(); })
    .then(function(channels) {
      loadingEl.style.display = 'none';
      tableEl.style.display = '';
      if (!channels || channels.length === 0) {
        tableEl.innerHTML = '<div style="color:var(--muted);font-size:13px;padding:8px 0">No Discord webhook channels configured.</div>';
        return;
      }
      var rows = channels.map(function(ch) {
        var events = (ch.events || ['all']).join(', ');
        return '<tr>' +
          '<td style="padding:8px 12px;font-family:monospace;font-size:13px">' + esc(ch.name) + '</td>' +
          '<td style="padding:8px 12px;font-size:12px;color:var(--muted);word-break:break-all">' + esc(ch.webhookUrl || '') + '</td>' +
          '<td style="padding:8px 12px;font-size:12px">' + esc(events) + '</td>' +
          '<td style="padding:8px 12px;white-space:nowrap">' +
            '<button class="btn" style="padding:3px 10px;font-size:11px;margin-right:4px" onclick="testDiscordChannel(' + JSON.stringify(ch.name) + ')">Test</button>' +
            '<button class="btn" style="padding:3px 10px;font-size:11px;color:var(--red);border-color:var(--red)" onclick="removeDiscordChannel(' + JSON.stringify(ch.name) + ')">Remove</button>' +
          '</td>' +
        '</tr>';
      });
      tableEl.innerHTML = '<table style="width:100%;border-collapse:collapse;border:1px solid var(--border);border-radius:8px;overflow:hidden">' +
        '<thead><tr style="background:var(--surface)">' +
          '<th style="padding:8px 12px;text-align:left;font-size:12px;color:var(--muted);font-weight:500">Name</th>' +
          '<th style="padding:8px 12px;text-align:left;font-size:12px;color:var(--muted);font-weight:500">Webhook</th>' +
          '<th style="padding:8px 12px;text-align:left;font-size:12px;color:var(--muted);font-weight:500">Events</th>' +
          '<th style="padding:8px 12px;text-align:left;font-size:12px;color:var(--muted);font-weight:500">Actions</th>' +
        '</tr></thead>' +
        '<tbody>' + rows.join('') + '</tbody>' +
        '</table>';
    })
    .catch(function(e) {
      loadingEl.style.display = 'none';
      tableEl.style.display = '';
      tableEl.innerHTML = '<div style="color:var(--red);font-size:13px">Failed to load: ' + e + '</div>';
    });
}

function testDiscordChannel(name) {
  fetch('/api/discord/channels/' + encodeURIComponent(name) + '/test', {method:'POST'})
    .then(function(r) { return r.json(); })
    .then(function(data) {
      if (data.error) { toast('Test failed: ' + data.error); return; }
      toast('Test message sent to ' + name);
    })
    .catch(function(e) { toast('Error: ' + e); });
}

function removeDiscordChannel(name) {
  if (!confirm('Remove Discord channel "' + name + '"?')) return;
  fetch('/api/discord/channels/' + encodeURIComponent(name), {method:'DELETE'})
    .then(function(r) { return r.json(); })
    .then(function(data) {
      if (data.error) { toast('Error: ' + data.error); return; }
      toast('Channel "' + name + '" removed');
      refreshNotificationsTable();
    })
    .catch(function(e) { toast('Error: ' + e); });
}

// Wizard state
var dcmStep = 1;

function openDiscordChannelModal() {
  dcmStep = 1;
  document.getElementById('dcm-name').value = '';
  document.getElementById('dcm-url').value = '';
  document.getElementById('dcm-events').value = 'all';
  document.getElementById('dcm-name-error').textContent = '';
  document.getElementById('dcm-url-error').textContent = '';
  document.getElementById('dcm-save-error').textContent = '';
  document.getElementById('dcm-send-test').checked = false;
  dcmShowPanel(1);
  document.getElementById('discord-channel-modal').style.display = 'flex';
}

function closeDiscordChannelModal() {
  document.getElementById('discord-channel-modal').style.display = 'none';
}

function dcmShowPanel(step) {
  [1,2,3].forEach(function(n) {
    document.getElementById('dcm-panel-' + n).style.display = n === step ? '' : 'none';
    var el = document.getElementById('dcm-step-' + n);
    el.className = 'dcm-step';
    if (n < step) el.classList.add('dcm-step-done');
    else if (n === step) el.classList.add('dcm-step-active');
  });
  dcmStep = step;
}

function dcmNext(fromStep) {
  if (fromStep === 1) {
    var name = document.getElementById('dcm-name').value.trim();
    var errEl = document.getElementById('dcm-name-error');
    if (!name) { errEl.textContent = 'Name is required.'; return; }
    if (!/^[a-zA-Z0-9_-]+$/.test(name) || name.length > 64) {
      errEl.textContent = 'Only letters, numbers, hyphens, underscores (max 64 chars).';
      return;
    }
    errEl.textContent = '';
    dcmShowPanel(2);
  } else if (fromStep === 2) {
    var url = document.getElementById('dcm-url').value.trim();
    var urlErr = document.getElementById('dcm-url-error');
    if (!url) { urlErr.textContent = 'Webhook URL is required.'; return; }
    if (!url.startsWith('https://discord.com/api/webhooks/') && !url.startsWith('https://discordapp.com/api/webhooks/')) {
      urlErr.textContent = 'URL must start with https://discord.com/api/webhooks/';
      return;
    }
    urlErr.textContent = '';
    // Populate confirm panel
    var events = document.getElementById('dcm-events').value;
    var eventsLabel = {'all':'All events','error':'Errors only','success':'Successes only'}[events] || events;
    document.getElementById('dcm-confirm-name').textContent = document.getElementById('dcm-name').value.trim();
    document.getElementById('dcm-confirm-events').textContent = eventsLabel;
    document.getElementById('dcm-confirm-url').textContent = url;
    dcmShowPanel(3);
  }
}

function dcmBack(fromStep) {
  dcmShowPanel(fromStep - 1);
}

function dcmSave() {
  var name = document.getElementById('dcm-name').value.trim();
  var url = document.getElementById('dcm-url').value.trim();
  var events = document.getElementById('dcm-events').value;
  var sendTest = document.getElementById('dcm-send-test').checked;
  var errEl = document.getElementById('dcm-save-error');
  var btn = document.getElementById('dcm-save-btn');

  btn.disabled = true;
  btn.textContent = 'Saving...';
  errEl.textContent = '';

  fetch('/api/discord/channels', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({name: name, webhookUrl: url, events: [events]})
  })
    .then(function(r) {
      if (!r.ok) return r.json().then(function(d) { throw new Error(d.error || 'HTTP ' + r.status); });
      return r.json();
    })
    .then(function() {
      if (sendTest) {
        return fetch('/api/discord/channels/' + encodeURIComponent(name) + '/test', {method:'POST'})
          .then(function(r) { return r.json(); })
          .then(function(d) {
            if (d.error) toast('Saved, but test failed: ' + d.error);
            else toast('Channel saved. Test message sent.');
          });
      } else {
        toast('Channel "' + name + '" saved.');
      }
    })
    .then(function() {
      closeDiscordChannelModal();
      refreshNotificationsTable();
    })
    .catch(function(e) {
      errEl.textContent = 'Error: ' + e.message;
    })
    .finally(function() {
      btn.disabled = false;
      btn.textContent = 'Save Channel';
    });
}

// --- Theme System ---
var THEMES = ['default', 'clean', 'material', 'boardroom', 'nord', 'dracula', 'solarized', 'rosepine', 'classic', 'nes', 'gameboy', 'amber'];
// Themes that default to light mode (dark toggle available)
var LIGHT_DEFAULT_THEMES = ['boardroom'];

function setTheme(name) {
  THEMES.forEach(function(t) { document.body.classList.remove('theme-' + t); });
  if (name && name !== 'default') document.body.classList.add('theme-' + name);
  localStorage.setItem('tetora-theme', name || 'default');
  THEMES.forEach(function(t) {
    var b = document.getElementById('theme-btn-' + t);
    if (b) b.style.borderColor = (t === (name || 'default')) ? 'var(--accent)' : '';
  });
  // Auto-expand Retro Lab if a retro theme is selected
  var retroThemes = ['classic','nes','gameboy','amber'];
  var lab = document.getElementById('retro-lab-panel');
  if (lab && retroThemes.indexOf(name) >= 0) lab.style.display = '';
  // Close dropdown after selection
  var dd = document.getElementById('theme-dropdown');
  if (dd) dd.classList.remove('open');
  // Update mode toggle icon
  updateModeToggle();
}

function setColorMode(mode) {
  document.body.classList.remove('mode-light', 'mode-dark');
  if (mode) document.body.classList.add('mode-' + mode);
  localStorage.setItem('tetora-color-mode', mode || '');
  updateModeToggle();
}

function toggleColorMode() {
  var isLight = document.body.classList.contains('mode-light');
  var currentTheme = localStorage.getItem('tetora-theme') || 'default';
  // Boardroom defaults to light, so toggle = dark
  if (LIGHT_DEFAULT_THEMES.indexOf(currentTheme) >= 0) {
    if (document.body.classList.contains('mode-dark')) {
      setColorMode('');
    } else {
      setColorMode('dark');
    }
  } else {
    setColorMode(isLight ? '' : 'light');
  }
}

function updateModeToggle() {
  var btn = document.getElementById('mode-toggle-btn');
  if (!btn) return;
  var isLight = document.body.classList.contains('mode-light');
  var currentTheme = localStorage.getItem('tetora-theme') || 'default';
  // Boardroom is light by default
  if (LIGHT_DEFAULT_THEMES.indexOf(currentTheme) >= 0 && !document.body.classList.contains('mode-dark')) {
    isLight = true;
  }
  btn.textContent = isLight ? '\u2600' : '\u263E';
  btn.title = isLight ? 'Switch to dark mode' : 'Switch to light mode';
  var si = document.getElementById('settings-mode-icon');
  if (si) si.innerHTML = isLight ? '&#x2600;' : '&#x263E;';
}

function toggleThemeDropdown() {
  var dd = document.getElementById('theme-dropdown');
  if (dd) dd.classList.toggle('open');
}
document.addEventListener('click', function(e) {
  var sw = document.getElementById('theme-switcher');
  var dd = document.getElementById('theme-dropdown');
  if (sw && dd && !sw.contains(e.target)) dd.classList.remove('open');
});
(function() {
  var s = localStorage.getItem('tetora-theme'); if (s && s !== 'default') setTheme(s);
  var m = localStorage.getItem('tetora-color-mode'); if (m) setColorMode(m);
  updateModeToggle();
  var st = document.getElementById('sound-toggle');
  if (st) st.checked = _soundEnabled;
})();

// --- Quick Search (Cmd+K) ---
document.addEventListener('keydown', function(e) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'k') { e.preventDefault(); openSearch(); }
  if (e.key === 'Escape' && document.getElementById('search-overlay').classList.contains('open')) { closeSearch(); e.stopPropagation(); }
});
var _searchItems = [];
function openSearch() {
  document.getElementById('search-overlay').classList.add('open');
  var input = document.getElementById('search-input');
  input.value = '';
  input.focus();
  document.getElementById('search-results').innerHTML = '<div class="search-empty">Type to search... (Cmd+K)</div>';
}
function closeSearch() { document.getElementById('search-overlay').classList.remove('open'); }
function onSearchInput(q) {
  var results = document.getElementById('search-results');
  q = q.toLowerCase().trim();
  if (!q) { results.innerHTML = '<div class="search-empty">Type to search...</div>'; return; }
  var items = [];
  if (typeof _kanbanTasks !== 'undefined' && _kanbanTasks) {
    _kanbanTasks.forEach(function(t) {
      if ((t.title||'').toLowerCase().indexOf(q) >= 0 || (t.project||'').toLowerCase().indexOf(q) >= 0)
        items.push({type:'Task', title:t.title, snippet:t.status+' · '+(t.project||''), action:function(){switchTab('operations');switchSubTab('operations','tasks');closeSearch();}});
    });
  }
  if (typeof _latestAgents !== 'undefined' && _latestAgents) {
    _latestAgents.forEach(function(a) {
      if ((a.name||'').toLowerCase().indexOf(q) >= 0)
        items.push({type:'Agent', title:a.name, snippet:a.model||'', action:function(){switchTab('dashboard');closeSearch();}});
    });
  }
  if (typeof _latestSessions !== 'undefined' && _latestSessions) {
    _latestSessions.forEach(function(s) {
      if ((s.title||'').toLowerCase().indexOf(q) >= 0 || (s.agent||'').toLowerCase().indexOf(q) >= 0)
        items.push({type:'Session', title:s.title||s.id, snippet:s.agent+' · '+s.status, action:function(){switchTab('chat');closeSearch();}});
    });
  }
  if (items.length === 0) { results.innerHTML = '<div class="search-empty">No results for "'+q.replace(/</g,'&lt;')+'"</div>'; return; }
  _searchItems = items;
  var html = '';
  items.slice(0,20).forEach(function(it,i) {
    html += '<div class="search-result" onclick="_searchItems['+i+'].action()"><span class="search-result-type">'+it.type+'</span><span class="search-result-title">'+(it.title||'').replace(/</g,'&lt;')+'</span><span class="search-result-snippet">'+(it.snippet||'').replace(/</g,'&lt;')+'</span></div>';
  });
  results.innerHTML = html;
}

// --- Notification Center ---
var _notifications = [], _notifUnread = 0;
function addNotification(msg, type, link) {
  _notifications.unshift({msg:msg, type:type||'info', time:new Date().toLocaleTimeString(), read:false, link:link||null});
  if (_notifications.length > 50) _notifications.pop();
  _notifUnread++;
  renderNotifications();
}
function notifNavigate(idx) {
  var n = _notifications[idx];
  if (!n || !n.link) return;
  document.getElementById('notif-dropdown').classList.remove('open');
  switchTab(n.link.tab);
  if (n.link.sub) switchSubTab(n.link.tab, n.link.sub);
  if (n.link.highlight) {
    setTimeout(function() {
      var el = document.querySelector(n.link.highlight);
      if (el) { el.classList.add('highlight-flash'); el.scrollIntoView({behavior:'smooth',block:'nearest'}); setTimeout(function(){el.classList.remove('highlight-flash')},2200); }
    }, 200);
  }
}
function renderNotifications() {
  var badge = document.getElementById('notif-badge');
  if (_notifUnread > 0) { badge.style.display = ''; badge.textContent = _notifUnread > 99 ? '99+' : _notifUnread; }
  else { badge.style.display = 'none'; }
  var dd = document.getElementById('notif-dropdown');
  if (_notifications.length === 0) { dd.innerHTML = '<div class="notif-empty">No notifications</div>'; return; }
  var html = '';
  _notifications.slice(0,20).forEach(function(n, i) {
    var clickable = n.link ? ' style="cursor:pointer" onclick="notifNavigate('+i+')"' : '';
    html += '<div class="notif-item"'+clickable+'><div class="notif-item-time">'+n.time+'</div><div class="notif-item-msg">'+n.msg.replace(/</g,'&lt;')+'</div></div>';
  });
  dd.innerHTML = html;
}
function toggleNotifDropdown() {
  var dd = document.getElementById('notif-dropdown');
  dd.classList.toggle('open');
  if (dd.classList.contains('open')) { _notifUnread = 0; renderNotifications(); }
}
document.addEventListener('click', function(e) {
  var bell = document.getElementById('notif-bell-container');
  if (bell && !bell.contains(e.target)) document.getElementById('notif-dropdown').classList.remove('open');
});

// --- Memory Browser ---
var _memBrowserFiles = [], _memBrowserActive = '';
async function loadMemoryBrowser() {
  try {
    var data = await fetchJSON(API + '/api/workspace/files');
    _memBrowserFiles = data.files || [];
    renderMemoryTree();
  } catch(e) { console.warn('memBrowser: no workspace API'); }
}
function renderMemoryTree() {
  var tree = document.getElementById('memory-tree');
  if (!tree) return;
  var byFolder = {};
  _memBrowserFiles.forEach(function(f) {
    if (!byFolder[f.folder]) byFolder[f.folder] = [];
    byFolder[f.folder].push(f);
  });
  var html = '';
  ['rules','memory','knowledge','skills'].forEach(function(folder) {
    var files = byFolder[folder] || [];
    if (files.length === 0) return;
    html += '<div class="memory-tree-folder">' + folder + ' (' + files.length + ')</div>';
    files.forEach(function(f) {
      var active = f.path === _memBrowserActive ? ' active' : '';
      html += '<div class="memory-tree-item' + active + '" onclick="loadMemoryFile(\'' + f.path.replace(/'/g,"\\'") + '\')">' + f.name + '</div>';
    });
  });
  tree.innerHTML = html || '<div class="search-empty">No workspace files</div>';
}
async function loadMemoryFile(path) {
  _memBrowserActive = path;
  renderMemoryTree();
  var editor = document.getElementById('memory-editor');
  try {
    var data = await fetchJSON(API + '/api/workspace/file?path=' + encodeURIComponent(path));
    editor.innerHTML = '<div class="memory-meta">' + path + '</div><textarea id="memory-editor-textarea">' + (data.content||'').replace(/</g,'&lt;') + '</textarea><button class="btn btn-add memory-save-btn" onclick="saveMemoryFile()">Save</button>';
  } catch(e) { editor.innerHTML = '<div class="search-empty">Failed to load file</div>'; }
}
async function saveMemoryFile() {
  if (!_memBrowserActive) return;
  var content = document.getElementById('memory-editor-textarea').value;
  try {
    await fetch(API + '/api/workspace/file', { method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify({path:_memBrowserActive, content:content}) });
  } catch(e) { console.warn('save failed', e); }
}

// --- System Health ---
function refreshHealth() {
  fetch(API + '/api/health').then(function(r){return r.json()}).then(function(d) {
    var h = document.getElementById('health-stats');
    if (!h) return;
    var items = [
      {label:'Uptime',value:d.uptime||'-'},{label:'DB Size',value:d.dbSize||'-'},
      {label:'SSE Clients',value:''+(d.sseClients||0)},{label:'Last Cron',value:d.lastCron||'-'},
      {label:'Provider',value:d.provider||'-'}
    ];
    h.innerHTML = '';
    items.forEach(function(it) {
      var el = document.createElement('div'); el.className = 'stat';
      el.innerHTML = '<div class="stat-label">'+it.label+'</div><div class="stat-value" style="font-size:16px">'+it.value+'</div>';
      h.appendChild(el);
    });
  }).catch(function(){});
  // Codex quota (appended to health section).
  refreshCodexQuota();
}

function refreshCodexQuota() {
  fetch(API + '/api/codex/status').then(function(r) {
    if (!r.ok) return;
    return r.json();
  }).then(function(q) {
    if (!q) return;
    var h = document.getElementById('health-stats');
    if (!h) return;
    // Remove old codex quota if present.
    var old = document.getElementById('codex-quota-panel');
    if (old) old.remove();
    var panel = document.createElement('div');
    panel.id = 'codex-quota-panel';
    panel.className = 'stat';
    panel.style.cssText = 'grid-column:1/-1';
    var bars = '';
    if (q.hourlyPct > 0 || q.hourlyText) {
      bars += '<div style="margin-bottom:4px"><span style="color:var(--muted);font-size:11px">5h limit</span> ' + buildQuotaBar(q.hourlyPct) + ' <span style="font-size:11px;color:var(--muted)">' + esc(q.hourlyText || '') + '</span></div>';
    }
    if (q.weeklyPct > 0 || q.weeklyText) {
      bars += '<div><span style="color:var(--muted);font-size:11px">Weekly</span> ' + buildQuotaBar(q.weeklyPct) + ' <span style="font-size:11px;color:var(--muted)">' + esc(q.weeklyText || '') + '</span></div>';
    }
    if (bars) {
      panel.innerHTML = '<div class="stat-label">Codex Quota</div><div style="margin-top:4px">' + bars + '</div>';
      h.appendChild(panel);
    }
  }).catch(function(){});
}

function buildQuotaBar(pct) {
  var filled = Math.round(pct / 5);
  var empty = 20 - filled;
  var color = pct > 50 ? 'var(--green)' : pct > 20 ? 'var(--yellow)' : 'var(--red,#e74c3c)';
  return '<span style="font-family:monospace;font-size:12px;color:' + color + '">[' + '\u2588'.repeat(filled) + '\u2591'.repeat(empty) + '] ' + pct.toFixed(0) + '%</span>';
}

// --- Workers Tab ---
var cliUsageData = { count: 0, totalCost: 0 };

function refreshWorkers() {
  fetch(API + '/api/workers').then(function(r){return r.json()}).then(function(d) {
    var workers = d.workers || [];
    var section = document.getElementById('workers-section');
    var grid = document.getElementById('workers-grid-inline');
    // Always show section (has toggle switches), update count.
    section.style.display = '';
    document.getElementById('workers-count-inline').textContent = workers.length || '0';
    grid.innerHTML = '';
    workers.forEach(function(w) {
      var stateColor = w.state === 'working' ? 'var(--green)' : w.state === 'approval' ? 'var(--yellow)' : w.state === 'question' ? 'var(--yellow)' : w.state === 'waiting' ? 'var(--accent)' : 'var(--muted)';
      var card = document.createElement('div');
      card.className = 'pixel-panel';
      card.style.cssText = 'padding:12px;cursor:pointer';
      card.onclick = (function(sid, name) { return function() { openWorkerDetail(sid, name); }; })(w.sessionId, w.name);
      var displayName = w.taskName || w.name;
      var sourceLabel = w.source || 'manual';
      var sourceBadgeColor = sourceLabel === 'cron' ? 'var(--yellow)' : sourceLabel === 'manual' ? 'var(--muted)' : 'var(--accent)';
      card.innerHTML =
        '<div style="display:flex;align-items:center;gap:8px;margin-bottom:6px">' +
          '<span style="width:8px;height:8px;border-radius:50%;background:' + stateColor + ';flex-shrink:0"></span>' +
          '<b style="font-size:12px;color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1">' + esc(displayName) + '</b>' +
          '<span style="font-size:11px;color:var(--muted)">' + esc(w.uptime) + '</span>' +
        '</div>' +
        '<div style="font-size:11px;color:var(--muted);display:flex;align-items:center;gap:6px">' +
          '<span style="padding:1px 5px;border-radius:3px;background:var(--surface);border:1px solid var(--border);color:' + stateColor + '">' + esc(w.state) + '</span>' +
          (w.agent ? '<span>' + esc(w.agent) + '</span>' : '') +
          '<span style="margin-left:auto;padding:1px 5px;border-radius:3px;background:var(--surface);border:1px solid var(--border);color:' + sourceBadgeColor + ';font-size:10px">' + esc(sourceLabel) + '</span>' +
        '</div>' +
        (w.costUsd > 0 || w.contextPct > 0 ?
          '<div style="font-size:11px;color:var(--muted);margin-top:4px;display:flex;align-items:center;gap:6px">' +
            (w.costUsd > 0 ? '<span style="color:var(--yellow)">$' + w.costUsd.toFixed(4) + '</span>' : '') +
            (w.inputTokens > 0 ? '<span>' + Math.round((w.inputTokens + (w.outputTokens||0))/1000) + 'k tokens</span>' : '') +
            (w.contextPct > 0 ? '<span>' + w.contextPct + '% ctx</span>' : '') +
            (w.model ? '<span style="margin-left:auto">' + esc(w.model) + '</span>' : '') +
          '</div>' : '');
      grid.appendChild(card);
    });
    var totalCli = { count: 0, totalCost: 0 };
    workers.forEach(function(w) { if (w.costUsd > 0) { totalCli.count++; totalCli.totalCost += w.costUsd; } });
    cliUsageData = totalCli;
  }).catch(function(){});
}

// --- Plan Reviews (v3) ---
let hookEventsItems = [];
const HOOK_EVENTS_MAX = 30;

function refreshPlanReviews() {
  fetch(API + '/api/plan-reviews?status=pending').then(function(r){return r.json()}).then(function(reviews) {
    var section = document.getElementById('plan-reviews-section');
    var list = document.getElementById('plan-reviews-list');
    var count = document.getElementById('plan-reviews-count');
    if (!reviews || reviews.length === 0) {
      section.style.display = 'none';
      return;
    }
    section.style.display = '';
    count.textContent = reviews.length;
    list.innerHTML = '';
    reviews.forEach(function(r) {
      var card = document.createElement('div');
      card.className = 'pixel-panel';
      card.style.cssText = 'padding:12px';
      var preview = (r.planText || '').substring(0, 500);
      if (r.planText && r.planText.length > 500) preview += '...';
      card.innerHTML =
        '<div style="display:flex;align-items:center;gap:8px;margin-bottom:8px">' +
          '<span style="padding:2px 8px;border-radius:4px;background:var(--yellow);color:#000;font-size:11px;font-weight:700">REVIEW</span>' +
          (r.agent ? '<span style="font-size:12px;color:var(--accent)">' + esc(r.agent) + '</span>' : '') +
          '<span style="margin-left:auto;font-size:11px;color:var(--muted)">' + esc(r.createdAt ? new Date(r.createdAt).toLocaleTimeString() : '') + '</span>' +
        '</div>' +
        '<pre style="background:#0a0a0f;border:1px solid var(--border);border-radius:6px;padding:10px;font-size:12px;color:var(--text);overflow:auto;max-height:300px;white-space:pre-wrap;margin-bottom:10px">' + esc(preview) + '</pre>' +
        '<div style="display:flex;gap:8px">' +
          '<button class="btn" style="background:var(--green);color:#000;padding:4px 16px;font-size:12px" onclick="reviewPlan(\'' + esc(r.id) + '\',\'approve\')">Approve</button>' +
          '<button class="btn" style="background:var(--red);color:#fff;padding:4px 16px;font-size:12px" onclick="reviewPlan(\'' + esc(r.id) + '\',\'reject\')">Reject</button>' +
        '</div>';
      list.appendChild(card);
    });
  }).catch(function(){});
}

function reviewPlan(id, action) {
  fetch(API + '/api/plan-reviews/' + id + '/' + action, {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({reviewer: 'dashboard'})
  }).then(function(r){return r.json()}).then(function(d) {
    if (d.error) alert('Error: ' + d.error);
    else refreshPlanReviews();
  }).catch(function(err){ alert('Failed: ' + err); });
}

function addHookEvent(data) {
  var hookType = data.hookType || 'event';
  var toolName = data.toolName || '';
  var sessionId = data.sessionId || '';
  var time = new Date().toLocaleTimeString('en-GB', {hour:'2-digit',minute:'2-digit',second:'2-digit'});
  var detail = toolName ? hookType + ': ' + toolName : hookType;
  hookEventsItems.unshift({detail: detail, time: time});
  if (hookEventsItems.length > HOOK_EVENTS_MAX) hookEventsItems.length = HOOK_EVENTS_MAX;
  renderHookEvents();

  // Push event into open worker detail modal in real-time.
  if (workerDetailSessionId && sessionId && sessionId.indexOf(workerDetailSessionId) === 0) {
    var el = document.getElementById('wd-events');
    var emptyMsg = el.querySelector('[style*="No events"]');
    if (emptyMsg) el.innerHTML = '';
    var badge = hookType === 'Stop' ? 'completed' : 'hook';
    var itemDetail = toolName ? esc(hookType) + ': ' + esc(toolName) : esc(hookType);
    el.innerHTML += '<div class="activity-item">' +
      '<span class="activity-type ' + badge + '">' + esc(hookType) + '</span>' +
      '<span class="activity-detail">' + itemDetail + '</span>' +
      '<span class="activity-time">' + esc(time) + '</span>' +
      '</div>';
    el.scrollTop = el.scrollHeight;
    if (hookType !== 'Stop') {
      var tc = document.getElementById('wd-toolcount');
      if (tc) tc.textContent = (parseInt(tc.textContent) || 0) + 1;
    }
    if (hookType === 'Stop') {
      document.getElementById('wd-state').textContent = 'done';
    }
    if (toolName) {
      document.getElementById('wd-lasttool').textContent = toolName;
    }
  }
}

function renderHookEvents() {
  var section = document.getElementById('hook-events-section');
  var list = document.getElementById('hook-events-list');
  var count = document.getElementById('hook-events-count');
  if (hookEventsItems.length === 0) {
    section.style.display = 'none';
    return;
  }
  section.style.display = '';
  count.textContent = hookEventsItems.length;
  list.innerHTML = hookEventsItems.map(function(item) {
    return '<div class="activity-item">' +
      '<span class="activity-type hook">hook</span>' +
      '<span class="activity-detail">' + esc(item.detail) + '</span>' +
      '<span class="activity-time">' + esc(item.time) + '</span>' +
    '</div>';
  }).join('');
}

// --- Worker Detail Modal ---
var workerDetailSessionId = null;

function openWorkerDetail(sessionId, name) {
  workerDetailSessionId = sessionId;
  document.getElementById('wd-title').textContent = name || 'Worker Detail';
  document.getElementById('wd-state').textContent = '...';
  document.getElementById('wd-uptime').textContent = '...';
  document.getElementById('wd-source').textContent = '...';
  document.getElementById('wd-agent').textContent = '...';
  document.getElementById('wd-taskname').textContent = '...';
  document.getElementById('wd-workdir').textContent = '...';
  document.getElementById('wd-toolcount').textContent = '...';
  document.getElementById('wd-lasttool').textContent = '...';
  document.getElementById('wd-events').innerHTML = '';
  document.getElementById('worker-detail-modal').classList.add('open');

  fetch(API + '/api/workers/events/' + encodeURIComponent(sessionId))
    .then(function(r) { return r.json(); })
    .then(function(d) {
      document.getElementById('wd-state').textContent = d.state || '-';
      document.getElementById('wd-uptime').textContent = d.uptime || '-';
      document.getElementById('wd-source').textContent = d.source || 'manual';
      document.getElementById('wd-agent').textContent = d.agent || '-';
      var taskLabel = d.taskName || '-';
      if (d.taskId) taskLabel += ' (' + d.taskId.substring(0, 8) + ')';
      document.getElementById('wd-taskname').textContent = taskLabel;
      document.getElementById('wd-workdir').textContent = d.workdir || '-';
      document.getElementById('wd-toolcount').textContent = (d.toolCount || 0) + '';
      document.getElementById('wd-lasttool').textContent = d.lastTool || '-';
      // Usage data in detail modal.
      var usageEl = document.getElementById('wd-usage');
      if (usageEl) {
        var parts = [];
        if (d.costUsd > 0) parts.push('$' + d.costUsd.toFixed(4));
        if (d.inputTokens > 0) parts.push(Math.round(d.inputTokens/1000) + 'k in / ' + Math.round((d.outputTokens||0)/1000) + 'k out');
        if (d.contextPct > 0) parts.push(d.contextPct + '% context');
        if (d.model) parts.push(d.model);
        usageEl.textContent = parts.length > 0 ? parts.join(' · ') : '-';
      }
      renderWorkerEvents(d.events || []);
    })
    .catch(function() {
      document.getElementById('wd-events').innerHTML = '<div style="color:var(--muted);font-size:12px;padding:8px">Failed to load events</div>';
    });
}

function closeWorkerDetail() {
  workerDetailSessionId = null;
  document.getElementById('worker-detail-modal').classList.remove('open');
}


function renderWorkerEvents(events) {
  var el = document.getElementById('wd-events');
  if (!events || events.length === 0) {
    el.innerHTML = '<div style="color:var(--muted);font-size:12px;padding:8px">No events yet</div>';
    return;
  }
  el.innerHTML = events.map(function(ev) {
    var ts = ev.timestamp ? new Date(ev.timestamp).toLocaleTimeString('en-GB', {hour:'2-digit',minute:'2-digit',second:'2-digit'}) : '';
    var badge = ev.eventType === 'Stop' ? 'completed' : 'hook';
    var detail = ev.toolName ? esc(ev.eventType) + ': ' + esc(ev.toolName) : esc(ev.eventType);
    return '<div class="activity-item">' +
      '<span class="activity-type ' + badge + '">' + esc(ev.eventType || 'event') + '</span>' +
      '<span class="activity-detail">' + detail + '</span>' +
      '<span class="activity-time">' + esc(ts) + '</span>' +
      '</div>';
  }).join('');
  el.scrollTop = el.scrollHeight;
}

