// --- Capabilities Tab: Tools, Skills, Templates ---

var capData = { tools: [], skills: [], templates: [] };

async function refreshCapabilities() {
  await Promise.all([loadToolsList(), loadSkillsList(), loadTemplatesList()]);
}

// --- Tools ---

async function loadToolsList() {
  var grid = document.getElementById('cap-tools-grid');
  try {
    var tools = await fetchJSON('/api/tools');
    capData.tools = Array.isArray(tools) ? tools : [];
    renderToolsGrid(capData.tools);
  } catch(e) {
    grid.innerHTML = '<div class="cap-empty">Error loading tools</div>';
  }
}

function renderToolsGrid(tools) {
  var grid = document.getElementById('cap-tools-grid');
  document.getElementById('cap-tools-count').textContent = '(' + tools.length + ')';
  if (tools.length === 0) {
    grid.innerHTML = '<div class="cap-empty">No tools registered</div>';
    return;
  }
  grid.innerHTML = tools.map(function(t) {
    var badges = '';
    if (t.builtin) badges += '<span class="cap-badge cap-badge-builtin">builtin</span>';
    else badges += '<span class="cap-badge cap-badge-custom">custom</span>';
    if (t.requireAuth) badges += '<span class="cap-badge cap-badge-auth">auth</span>';
    return '<div class="cap-card">' +
      '<div class="cap-card-name">' + esc(t.name) + '</div>' +
      '<div class="cap-card-desc">' + esc(t.description || '') + '</div>' +
      '<div class="cap-card-badges">' + badges + '</div>' +
    '</div>';
  }).join('');
}

// --- Skills ---

async function loadSkillsList() {
  var grid = document.getElementById('cap-skills-grid');
  try {
    var data = await fetchJSON('/api/skills/store');
    var skills = data.skills || [];
    var pending = data.pending || [];
    capData.skills = skills.concat(pending);
    renderSkillsGrid(capData.skills);
  } catch(e) {
    // Fallback to basic skills list.
    try {
      var list = await fetchJSON('/api/skills');
      capData.skills = Array.isArray(list) ? list.map(function(s) {
        return { name: s.name, description: s.description, approved: true };
      }) : [];
      renderSkillsGrid(capData.skills);
    } catch(e2) {
      grid.innerHTML = '<div class="cap-empty">Error loading skills</div>';
    }
  }
}

function renderSkillsGrid(skills) {
  var grid = document.getElementById('cap-skills-grid');
  document.getElementById('cap-skills-count').textContent = '(' + skills.length + ')';
  if (skills.length === 0) {
    grid.innerHTML = '<div class="cap-empty">No skills found</div>';
    return;
  }
  grid.innerHTML = skills.map(function(s) {
    var badges = '';
    if (s.approved) badges += '<span class="cap-badge cap-badge-approved">approved</span>';
    else badges += '<span class="cap-badge cap-badge-pending">pending</span>';
    if (s.sandbox) badges += '<span class="cap-badge cap-badge-sandbox">sandbox</span>';
    if (s.usageCount > 0) badges += '<span class="cap-badge">' + s.usageCount + ' uses</span>';

    var actions = '';
    if (!s.approved) {
      actions = '<div class="cap-card-actions">' +
        '<button class="btn btn-sm btn-approve" onclick="event.stopPropagation();approveSkill(\'' + escAttr(s.name) + '\')">Approve</button>' +
        '<button class="btn btn-sm btn-reject" onclick="event.stopPropagation();rejectSkill(\'' + escAttr(s.name) + '\')">Reject</button>' +
      '</div>';
    }

    return '<div class="cap-card">' +
      '<div class="cap-card-name">' + esc(s.name) + '</div>' +
      '<div class="cap-card-desc">' + esc(s.description || '') + '</div>' +
      '<div class="cap-card-badges">' + badges + '</div>' +
      actions +
    '</div>';
  }).join('');
}

async function approveSkill(name) {
  try {
    await fetchJSON('/api/skills/store/' + encodeURIComponent(name) + '/approve', { method: 'POST' });
    toast('Skill approved: ' + name);
    loadSkillsList();
  } catch(e) {
    toast('Approve failed: ' + (e.message || e));
  }
}

async function rejectSkill(name) {
  if (!confirm('Reject skill "' + name + '"?')) return;
  try {
    await fetchJSON('/api/skills/store/' + encodeURIComponent(name) + '/reject', { method: 'POST' });
    toast('Skill rejected: ' + name);
    loadSkillsList();
  } catch(e) {
    toast('Reject failed: ' + (e.message || e));
  }
}

async function deleteCapSkill(name) {
  if (!confirm('Delete skill "' + name + '"?')) return;
  try {
    await fetch('/api/skills/store/' + encodeURIComponent(name), { method: 'DELETE' });
    toast('Skill deleted');
    loadSkillsList();
  } catch(e) {
    toast('Delete failed: ' + (e.message || e));
  }
}

// --- Templates ---

async function loadTemplatesList() {
  var grid = document.getElementById('cap-templates-grid');
  try {
    var data = await fetchJSON('/api/templates');
    capData.templates = data.templates || [];
    renderCapTemplatesGrid(capData.templates);
  } catch(e) {
    grid.innerHTML = '<div class="cap-empty">Error loading templates</div>';
  }
}

function renderCapTemplatesGrid(templates) {
  var grid = document.getElementById('cap-templates-grid');
  document.getElementById('cap-templates-count').textContent = '(' + templates.length + ')';
  if (templates.length === 0) {
    grid.innerHTML = '<div class="cap-empty">No templates available</div>';
    return;
  }
  grid.innerHTML = templates.map(function(t) {
    var badges = '';
    if (t.category) badges += '<span class="cap-badge cap-badge-category">' + esc(t.category) + '</span>';
    badges += '<span class="cap-badge">' + t.stepCount + ' steps</span>';
    if (t.variables && t.variables.length > 0) badges += '<span class="cap-badge">' + t.variables.length + ' vars</span>';

    return '<div class="cap-card cap-card-template">' +
      '<div class="cap-card-name">' + esc(t.name) + '</div>' +
      '<div class="cap-card-desc">' + esc(t.description || '') + '</div>' +
      '<div class="cap-card-badges">' + badges + '</div>' +
      '<div class="cap-card-actions">' +
        '<button class="btn btn-sm" onclick="event.stopPropagation();previewCapTemplate(\'' + escAttr(t.name) + '\')">Preview</button>' +
        '<button class="btn btn-sm btn-primary" onclick="event.stopPropagation();installCapTemplate(\'' + escAttr(t.name) + '\')">Install</button>' +
      '</div>' +
    '</div>';
  }).join('');
}

async function previewCapTemplate(name) {
  try {
    var wf = await fetchJSON('/api/templates/' + encodeURIComponent(name));
    // Switch to workflows tab and open in editor.
    switchTab('operations');
    switchSubTab('operations', 'workflows');
    setTimeout(function() {
      if (typeof openWorkflowEditorWithData === 'function') openWorkflowEditorWithData(wf);
    }, 100);
    toast('Previewing template: ' + name);
  } catch(e) {
    toast('Preview failed: ' + (e.message || e));
  }
}

async function installCapTemplate(name) {
  var newName = prompt('Install as workflow name (leave empty for default):', name.replace(/^tpl-/, ''));
  if (newName === null) return;
  try {
    await fetchJSON('/api/templates/' + encodeURIComponent(name) + '/install', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ newName: newName || '' }),
    });
    toast('Template installed!');
    // Switch to workflows tab.
    switchTab('operations');
    switchSubTab('operations', 'workflows');
    setTimeout(function() {
      loadWorkflowDefs();
      if (newName && typeof openWorkflowEditor === 'function') openWorkflowEditor(newName);
    }, 200);
  } catch(e) {
    toast('Install failed: ' + (e.message || e));
  }
}

// --- Shared ---

function toggleCapSection(section) {
  var body = document.getElementById('cap-' + section + '-body');
  var toggle = document.getElementById('cap-' + section + '-toggle');
  if (!body) return;
  var hidden = body.style.display === 'none';
  body.style.display = hidden ? '' : 'none';
  if (toggle) toggle.innerHTML = hidden ? '&#9660;' : '&#9654;';
}

function filterCapabilities() {
  var q = (document.getElementById('cap-search').value || '').toLowerCase();
  if (!q) {
    renderToolsGrid(capData.tools);
    renderSkillsGrid(capData.skills);
    renderCapTemplatesGrid(capData.templates);
    return;
  }
  renderToolsGrid(capData.tools.filter(function(t) {
    return (t.name || '').toLowerCase().includes(q) || (t.description || '').toLowerCase().includes(q);
  }));
  renderSkillsGrid(capData.skills.filter(function(s) {
    return (s.name || '').toLowerCase().includes(q) || (s.description || '').toLowerCase().includes(q);
  }));
  renderCapTemplatesGrid(capData.templates.filter(function(t) {
    return (t.name || '').toLowerCase().includes(q) || (t.description || '').toLowerCase().includes(q) || (t.category || '').toLowerCase().includes(q);
  }));
}
