// --- Team Builder ---

var _teamBuilderLoaded = false;

function refreshTeamBuilder() {
  var container = document.getElementById('team-builder-content');
  if (!container) return;
  if (!_teamBuilderLoaded) {
    renderTeamBuilderView();
    _teamBuilderLoaded = true;
  }
  loadTeamList();
}

function renderTeamBuilderView() {
  var container = document.getElementById('team-builder-content');
  container.innerHTML = [
    '<div class="section">',
    '  <div class="section-header">',
    '    <span class="section-title">Team Builder</span>',
    '    <div style="display:flex;gap:8px;align-items:center">',
    '      <button class="btn btn-primary" onclick="openTeamGenerateModal()">+ Generate Team</button>',
    '      <button class="btn" onclick="loadTeamList()">Refresh</button>',
    '    </div>',
    '  </div>',
    '  <p style="color:var(--text-secondary);font-size:13px;margin-bottom:16px">',
    '    Create and manage AI agent teams. Generate a team from a description or use a built-in template.',
    '  </p>',
    '  <div id="team-list"></div>',
    '</div>',
    '<div id="team-generate-modal" class="modal-backdrop" style="display:none" onclick="if(event.target===this)closeTeamGenerateModal()">',
    '  <div class="modal-box" style="max-width:520px">',
    '    <div class="modal-header">',
    '      <span>Generate New Team</span>',
    '      <button class="modal-close" onclick="closeTeamGenerateModal()">&times;</button>',
    '    </div>',
    '    <form id="team-generate-form" onsubmit="submitTeamGenerate(event)">',
    '      <div class="form-group">',
    '        <label>Description</label>',
    '        <textarea id="tg-description" rows="4" placeholder="Describe the team you want, e.g.: A data engineering team for ETL pipelines, data quality, and analytics" required></textarea>',
    '      </div>',
    '      <div class="form-group">',
    '        <label>Team Size (optional)</label>',
    '        <input type="number" id="tg-size" min="2" max="10" placeholder="Auto">',
    '      </div>',
    '      <div class="form-group">',
    '        <label>Base Template (optional)</label>',
    '        <select id="tg-template">',
    '          <option value="">None</option>',
    '          <option value="software-dev">Software Dev</option>',
    '          <option value="content-creation">Content Creation</option>',
    '          <option value="customer-support">Customer Support</option>',
    '        </select>',
    '      </div>',
    '      <div id="tg-status" style="display:none;padding:12px;background:var(--bg-tertiary);border-radius:8px;margin-bottom:12px;font-size:13px"></div>',
    '      <div style="display:flex;gap:8px;justify-content:flex-end">',
    '        <button type="button" class="btn" onclick="closeTeamGenerateModal()">Cancel</button>',
    '        <button type="submit" class="btn btn-primary" id="tg-submit">Generate</button>',
    '      </div>',
    '    </form>',
    '  </div>',
    '</div>',
    '<div id="team-detail-modal" class="modal-backdrop" style="display:none" onclick="if(event.target===this)closeTeamDetailModal()">',
    '  <div class="modal-box" style="max-width:700px;max-height:80vh;overflow-y:auto">',
    '    <div class="modal-header">',
    '      <span id="td-title">Team Details</span>',
    '      <button class="modal-close" onclick="closeTeamDetailModal()">&times;</button>',
    '    </div>',
    '    <div id="td-body"></div>',
    '    <div id="td-actions" style="display:flex;gap:8px;justify-content:flex-end;margin-top:16px"></div>',
    '  </div>',
    '</div>'
  ].join('\n');
}

async function loadTeamList() {
  var list = document.getElementById('team-list');
  if (!list) return;
  list.innerHTML = '<div style="color:var(--text-secondary);padding:20px;text-align:center">Loading teams...</div>';

  try {
    var teams = await fetchJSON('/api/teams');
    if (!Array.isArray(teams) || teams.length === 0) {
      list.innerHTML = '<div style="color:var(--text-secondary);padding:20px;text-align:center">No teams yet. Click "Generate Team" to create one.</div>';
      return;
    }

    var html = '<div class="agents-grid">';
    teams.forEach(function(t) {
      var badge = t.builtin
        ? '<span style="background:var(--accent);color:#fff;padding:2px 6px;border-radius:4px;font-size:10px;margin-left:6px">BUILTIN</span>'
        : '';
      html += '<div class="agent-card" style="cursor:pointer" onclick="openTeamDetail(\'' + esc(t.name) + '\')">';
      html += '<div style="display:flex;justify-content:space-between;align-items:center">';
      html += '<span style="font-weight:600">' + esc(t.name) + badge + '</span>';
      html += '<span style="color:var(--text-secondary);font-size:12px">' + t.agentCount + ' agents</span>';
      html += '</div>';
      html += '<div style="color:var(--text-secondary);font-size:13px;margin-top:4px">' + esc(t.description) + '</div>';
      html += '</div>';
    });
    html += '</div>';
    list.innerHTML = html;
  } catch (e) {
    list.innerHTML = '<div style="color:var(--danger);padding:20px">Error loading teams: ' + esc(e.message) + '</div>';
  }
}

function openTeamGenerateModal() {
  document.getElementById('team-generate-form').reset();
  document.getElementById('tg-status').style.display = 'none';
  document.getElementById('tg-submit').disabled = false;
  document.getElementById('team-generate-modal').style.display = 'flex';
}

function closeTeamGenerateModal() {
  document.getElementById('team-generate-modal').style.display = 'none';
}

async function submitTeamGenerate(e) {
  e.preventDefault();
  var desc = document.getElementById('tg-description').value.trim();
  if (!desc) return;

  var size = parseInt(document.getElementById('tg-size').value) || 0;
  var template = document.getElementById('tg-template').value;
  var btn = document.getElementById('tg-submit');
  var status = document.getElementById('tg-status');

  btn.disabled = true;
  btn.textContent = 'Generating...';
  status.style.display = 'block';
  status.textContent = 'Generating team... this may take a minute.';

  try {
    var payload = { description: desc };
    if (size > 0) payload.size = size;
    if (template) payload.template = template;

    var resp = await fetch(API + '/api/teams/generate', {
      method: 'POST',
      headers: authHeaders({'Content-Type': 'application/json'}),
      body: JSON.stringify(payload)
    });
    if (!resp.ok) {
      var err = await resp.json();
      throw new Error(err.error || resp.statusText);
    }
    var team = await resp.json();

    status.textContent = 'Team generated! Saving...';

    // Save the team.
    var resp2 = await fetch(API + '/api/teams', {
      method: 'POST',
      headers: authHeaders({'Content-Type': 'application/json'}),
      body: JSON.stringify(team)
    });
    if (!resp2.ok) {
      var err2 = await resp2.json();
      throw new Error(err2.error || resp2.statusText);
    }

    closeTeamGenerateModal();
    toast('Team "' + team.name + '" created with ' + team.agents.length + ' agents');
    loadTeamList();
  } catch (err) {
    status.style.display = 'block';
    status.innerHTML = '<span style="color:var(--danger)">Error: ' + esc(err.message) + '</span>';
  } finally {
    btn.disabled = false;
    btn.textContent = 'Generate';
  }
}

async function openTeamDetail(name) {
  var title = document.getElementById('td-title');
  var body = document.getElementById('td-body');
  var actions = document.getElementById('td-actions');
  title.textContent = 'Loading...';
  body.innerHTML = '';
  actions.innerHTML = '';
  document.getElementById('team-detail-modal').style.display = 'flex';

  try {
    var team = await fetchJSON('/api/teams/' + encodeURIComponent(name));
    title.textContent = team.name + (team.builtin ? ' (builtin)' : '');

    var html = '<p style="color:var(--text-secondary);margin-bottom:16px">' + esc(team.description) + '</p>';
    html += '<div class="agents-grid">';
    (team.agents || []).forEach(function(a) {
      html += '<div class="agent-card">';
      html += '<div style="display:flex;justify-content:space-between;align-items:center">';
      html += '<span style="font-weight:600">' + esc(a.displayName || a.key) + '</span>';
      html += '<span style="background:var(--bg-tertiary);padding:2px 8px;border-radius:4px;font-size:11px">' + esc(a.model) + '</span>';
      html += '</div>';
      html += '<div style="color:var(--text-secondary);font-size:13px;margin-top:4px">' + esc(a.description) + '</div>';
      if (a.keywords && a.keywords.length > 0) {
        var kw = a.keywords.slice(0, 8);
        html += '<div style="margin-top:6px;display:flex;flex-wrap:wrap;gap:4px">';
        kw.forEach(function(k) {
          html += '<span style="background:var(--bg-tertiary);padding:1px 6px;border-radius:3px;font-size:10px">' + esc(k) + '</span>';
        });
        if (a.keywords.length > 8) html += '<span style="font-size:10px;color:var(--text-secondary)">+' + (a.keywords.length - 8) + '</span>';
        html += '</div>';
      }
      html += '</div>';
    });
    html += '</div>';
    body.innerHTML = html;

    // Actions.
    var actHtml = '';
    actHtml += '<button class="btn btn-primary" onclick="applyTeam(\'' + esc(name) + '\',false)">Apply to Config</button>';
    actHtml += '<button class="btn" onclick="applyTeam(\'' + esc(name) + '\',true)">Force Apply</button>';
    if (!team.builtin) {
      actHtml += '<button class="btn" style="color:var(--danger)" onclick="deleteTeam(\'' + esc(name) + '\')">Delete</button>';
    }
    actions.innerHTML = actHtml;
  } catch (err) {
    body.innerHTML = '<div style="color:var(--danger)">Error: ' + esc(err.message) + '</div>';
  }
}

function closeTeamDetailModal() {
  document.getElementById('team-detail-modal').style.display = 'none';
}

async function applyTeam(name, force) {
  var label = force ? 'Force applying' : 'Applying';
  if (!confirm(label + ' team "' + name + '" will add agents to your config. Continue?')) return;

  try {
    var resp = await fetch(API + '/api/teams/' + encodeURIComponent(name) + '/apply', {
      method: 'POST',
      headers: authHeaders({'Content-Type': 'application/json'}),
      body: JSON.stringify({ force: force })
    });
    if (!resp.ok) {
      var err = await resp.json();
      throw new Error(err.error || resp.statusText);
    }
    toast('Team "' + name + '" applied. Config reloaded.');
    closeTeamDetailModal();
  } catch (err) {
    toast('Error: ' + err.message);
  }
}

async function deleteTeam(name) {
  if (!confirm('Delete team "' + name + '"? This cannot be undone.')) return;
  try {
    var resp = await fetch(API + '/api/teams/' + encodeURIComponent(name), {
      method: 'DELETE',
      headers: authHeaders()
    });
    if (!resp.ok) {
      var err = await resp.json();
      throw new Error(err.error || resp.statusText);
    }
    toast('Team "' + name + '" deleted.');
    closeTeamDetailModal();
    loadTeamList();
  } catch (err) {
    toast('Error: ' + err.message);
  }
}
