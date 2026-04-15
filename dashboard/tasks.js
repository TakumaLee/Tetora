// --- Projects (Workspace Tab) ---

var cachedProjects = [];
var cachedBoardData = null;
var cachedBoardSearch = '';
var cachedWorkflowNames = null; // fetched once, reused by task detail + settings
var taskWfSSE = null; // SSE connection for task workflow progress

async function getWorkflowNames() {
  if (cachedWorkflowNames) return cachedWorkflowNames;
  try {
    var list = await fetchJSON('/workflows');
    cachedWorkflowNames = Array.isArray(list) ? list.map(function(wf) { return wf.name || wf; }) : [];
  } catch(e) { cachedWorkflowNames = []; }
  return cachedWorkflowNames;
}

async function refreshProjects() {
  var status = document.getElementById('proj-status-filter').value;
  try {
    var url = '/api/projects';
    if (status) url += '?status=' + encodeURIComponent(status);
    var data = await fetchJSON(url);
    cachedProjects = data.projects || [];
    renderProjects(cachedProjects);
  } catch(e) {
    document.getElementById('projects-grid').innerHTML = '<div style="color:var(--red);padding:20px;text-align:center">Error loading projects</div>';
  }
}

function filterProjects() {
  var q = (document.getElementById('proj-search').value || '').toLowerCase();
  if (!q) { renderProjects(cachedProjects); return; }
  var filtered = cachedProjects.filter(function(p) {
    return (p.name||'').toLowerCase().includes(q) ||
           (p.description||'').toLowerCase().includes(q) ||
           (p.category||'').toLowerCase().includes(q) ||
           (p.tags||'').toLowerCase().includes(q);
  });
  renderProjects(filtered);
}

function renderProjects(projects) {
  var grid = document.getElementById('projects-grid');
  if (!projects || projects.length === 0) {
    grid.innerHTML = '<div style="color:var(--muted);font-size:13px;padding:40px;text-align:center">No projects found. Click "+ Add" to create one.</div>';
    return;
  }
  grid.innerHTML = projects.map(function(p) {
    var tags = (p.tags||'').split(',').filter(Boolean).map(function(t) {
      return '<span class="project-tag">' + esc(t.trim()) + '</span>';
    }).join('');
    var statusClass = p.status === 'archived' ? ' archived' : '';
    var repoLink = p.repoUrl ? '<a href="' + esc(p.repoUrl) + '" target="_blank" style="font-size:11px;color:var(--accent2);text-decoration:none" title="' + esc(p.repoUrl) + '">repo</a>' : '';
    return '<div class="project-card">' +
      '<div class="project-card-header">' +
        '<span class="project-card-name">' + esc(p.name) + '</span>' +
        '<div style="display:flex;align-items:center;gap:6px">' +
          (repoLink ? repoLink + ' ' : '') +
          '<span style="font-size:11px;color:var(--muted)">#' + (p.priority||0) + '</span>' +
        '</div>' +
      '</div>' +
      (p.description ? '<div class="project-card-desc">' + esc(p.description) + '</div>' : '') +
      '<div class="project-card-meta">' +
        (p.category ? '<span class="project-badge project-badge-category">' + esc(p.category) + '</span>' : '') +
        '<span class="project-badge project-badge-status' + statusClass + '">' + esc(p.status||'active') + '</span>' +
      '</div>' +
      (p.workdir ? '<div class="project-card-workdir" title="' + esc(p.workdir) + '">' + esc(p.workdir) + '</div>' : '') +
      (tags ? '<div class="project-card-tags">' + tags + '</div>' : '') +
      '<div class="project-card-actions">' +
        (p.workdir ? '<button class="btn" onclick="dispatchToProject(\'' + esc(p.workdir).replace(/'/g, "\\'") + '\')">Dispatch</button>' : '') +
        '<button class="btn" onclick="openProjectModal(\'' + esc(p.id) + '\')">Edit</button>' +
        '<button class="btn" style="color:var(--red)" onclick="deleteProjectConfirm(\'' + esc(p.id) + '\',\'' + esc(p.name).replace(/'/g, "\\'") + '\')">Del</button>' +
      '</div>' +
    '</div>';
  }).join('');
}

function openProjectModal(editId) {
  var modal = document.getElementById('project-modal');
  document.getElementById('project-form').reset();
  document.getElementById('pjf-mode').value = editId ? 'edit' : 'add';
  document.getElementById('pjf-id').value = editId || '';
  document.getElementById('project-modal-title').textContent = editId ? 'Edit Project' : 'Add Project';
  document.getElementById('pjf-submit').textContent = editId ? 'Save Changes' : 'Add Project';
  document.getElementById('pjf-delete').style.display = editId ? '' : 'none';

  if (editId) {
    var p = cachedProjects.find(function(x) { return x.id === editId; });
    if (p) {
      document.getElementById('pjf-name').value = p.name || '';
      document.getElementById('pjf-desc').value = p.description || '';
      document.getElementById('pjf-workdir').value = p.workdir || '';
      document.getElementById('pjf-repo').value = p.repoUrl || '';
      document.getElementById('pjf-category').value = p.category || '';
      document.getElementById('pjf-tags').value = p.tags || '';
      document.getElementById('pjf-status').value = p.status || 'active';
      document.getElementById('pjf-priority').value = p.priority || 0;
    }
  }
  modal.classList.add('open');
}

function closeProjectModal() {
  document.getElementById('project-modal').classList.remove('open');
}

async function submitProject(e) {
  e.preventDefault();
  var mode = document.getElementById('pjf-mode').value;
  var id = document.getElementById('pjf-id').value;
  var body = {
    name: document.getElementById('pjf-name').value.trim(),
    description: document.getElementById('pjf-desc').value.trim(),
    workdir: document.getElementById('pjf-workdir').value.trim(),
    repoUrl: document.getElementById('pjf-repo').value.trim(),
    category: document.getElementById('pjf-category').value.trim(),
    tags: document.getElementById('pjf-tags').value.trim(),
    status: document.getElementById('pjf-status').value,
    priority: parseInt(document.getElementById('pjf-priority').value) || 0,
  };

  try {
    if (mode === 'edit') {
      await fetchJSON('/api/projects/' + id, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      toast('Project updated');
    } else {
      await fetchJSON('/api/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      toast('Project created');
    }
    closeProjectModal();
    refreshBoard();
    refreshProjects();
  } catch(e) {
    toast('Error: ' + e.message);
  }
  return false;
}

async function deleteFromProjectModal() {
  var id = document.getElementById('pjf-id').value;
  var name = document.getElementById('pjf-name').value;
  if (!id) return;
  if (!confirm('Delete project "' + name + '"?')) return;
  try {
    await fetchJSON('/api/projects/' + id, { method: 'DELETE' });
    toast('Project deleted');
    closeProjectModal();
    refreshBoard();
    refreshProjects();
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

async function deleteProjectConfirm(id, name) {
  if (!confirm('Delete project "' + name + '"?')) return;
  try {
    await fetchJSON('/api/projects/' + id, { method: 'DELETE' });
    toast('Project deleted');
    refreshBoard();
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

// --- Kanban Board ---

async function refreshBoard() {
  // Refresh projects for stats bar + filter dropdowns.
  var projStatus = document.getElementById('proj-status-filter').value;
  try {
    var projUrl = '/api/projects';
    if (projStatus) projUrl += '?status=' + encodeURIComponent(projStatus);
    var projData = await fetchJSON(projUrl);
    cachedProjects = projData.projects || [];
  } catch(e) { cachedProjects = []; }

  // Fetch board data.
  var project = document.getElementById('kb-filter-project').value;
  var assignee = document.getElementById('kb-filter-assignee').value;
  var priority = document.getElementById('kb-filter-priority').value;
  var workflow = document.getElementById('kb-filter-workflow').value;
  var url = '/api/tasks/board?';
  if (project) url += 'project=' + encodeURIComponent(project) + '&';
  if (assignee) url += 'assignee=' + encodeURIComponent(assignee) + '&';
  if (priority) url += 'priority=' + encodeURIComponent(priority) + '&';
  if (workflow) url += 'workflow=' + encodeURIComponent(workflow) + '&';

  try {
    cachedBoardData = await fetchJSON(url);
  } catch(e) {
    // Task board might not be enabled — show empty state.
    cachedBoardData = { columns: {idea:[],backlog:[],'needs-thought':[],todo:[],doing:[],'partial-done':[],review:[],done:[],failed:[]}, stats: {total:0,byStatus:{},totalCost:0}, projects: [], agents: [] };
  }

  // Cache all tasks for search
  var allTasks = [];
  if (cachedBoardData && cachedBoardData.columns) {
    ['idea','backlog','needs-thought','todo','doing','partial-done','review','done','failed'].forEach(function(s) {
      (cachedBoardData.columns[s] || []).forEach(function(t) { allTasks.push(t); });
    });
  }
  window._kanbanTasks = allTasks;

  renderProjectStatsBar();
  populateBoardFilters();
  renderBoard();
}

function renderProjectStatsBar() {
  var bar = document.getElementById('kanban-stats-bar');
  var activeProject = document.getElementById('kb-filter-project').value;

  if (!cachedProjects || cachedProjects.length === 0) {
    bar.innerHTML = '<div style="color:var(--muted);font-size:12px;padding:10px">No projects. Click "+ Project" to create one.</div>';
    return;
  }

  // Count tasks per project from board data.
  var projectTaskCounts = {};
  var projectCosts = {};
  if (cachedBoardData && cachedBoardData.columns) {
    var statuses = ['idea','backlog','needs-thought','todo','doing','review','done','failed'];
    statuses.forEach(function(s) {
      (cachedBoardData.columns[s] || []).forEach(function(t) {
        var proj = t.project || 'default';
        projectTaskCounts[proj] = (projectTaskCounts[proj] || 0) + 1;
        projectCosts[proj] = (projectCosts[proj] || 0) + (t.costUsd || 0);
      });
    });
  }

  bar.innerHTML = cachedProjects.map(function(p) {
    var count = projectTaskCounts[p.id] || projectTaskCounts[p.name] || 0;
    var cost = projectCosts[p.id] || projectCosts[p.name] || 0;
    var isActive = activeProject === p.id || activeProject === p.name;
    return '<div class="kanban-stat-chip' + (isActive ? ' active' : '') + '" onclick="filterByProject(\'' + esc(p.name) + '\')">' +
      '<button class="kanban-stat-chip-edit" onclick="event.stopPropagation();openProjectModal(\'' + esc(p.id) + '\')" title="Edit project">&#9998;</button>' +
      '<span class="kanban-stat-chip-name">' + esc(p.name) + '</span>' +
      '<span class="kanban-stat-chip-info">' + count + ' task' + (count !== 1 ? 's' : '') + '</span>' +
      (cost > 0 ? '<span class="kanban-stat-chip-cost">$' + cost.toFixed(2) + '</span>' : '') +
    '</div>';
  }).join('') +
  '<div class="kanban-stat-chip" onclick="openProjectModal()" style="border-style:dashed;justify-content:center;align-items:center;min-width:80px">' +
    '<span style="color:var(--muted);font-size:18px">+</span>' +
  '</div>';
}

function filterByProject(name) {
  var sel = document.getElementById('kb-filter-project');
  if (sel.value === name) {
    sel.value = '';
  } else {
    sel.value = name;
  }
  refreshBoard();
}

function populateBoardFilters() {
  // Populate project filter.
  var projSel = document.getElementById('kb-filter-project');
  var currentProj = projSel.value;
  projSel.innerHTML = '<option value="">All Projects</option>';
  var projects = (cachedBoardData && cachedBoardData.projects) || [];
  cachedProjects.forEach(function(p) {
    var opt = document.createElement('option');
    opt.value = p.name;
    opt.textContent = p.name;
    projSel.appendChild(opt);
  });
  // Also add board-only projects not in cached list.
  projects.forEach(function(pn) {
    if (!cachedProjects.find(function(p) { return p.name === pn || p.id === pn; })) {
      var opt = document.createElement('option');
      opt.value = pn;
      opt.textContent = pn;
      projSel.appendChild(opt);
    }
  });
  projSel.value = currentProj;

  // Populate assignee filter.
  var agentSel = document.getElementById('kb-filter-assignee');
  var currentAgent = agentSel.value;
  agentSel.innerHTML = '<option value="">All Agents</option>';
  var agents = (cachedBoardData && cachedBoardData.agents) || [];
  agents.forEach(function(a) {
    var n = agentName(a);
    if (!n) return;
    var opt = document.createElement('option');
    opt.value = n;
    opt.textContent = n;
    agentSel.appendChild(opt);
  });
  agentSel.value = currentAgent;

  // Populate workflow filter.
  var wfSel = document.getElementById('kb-filter-workflow');
  var currentWf = wfSel.value;
  wfSel.innerHTML = '<option value="">All Workflows</option>';
  var workflows = (cachedBoardData && cachedBoardData.workflows) || [];
  workflows.sort();
  workflows.forEach(function(wf) {
    var opt = document.createElement('option');
    opt.value = wf;
    opt.textContent = wf;
    wfSel.appendChild(opt);
  });
  wfSel.value = currentWf;
}

function renderBoard() {
  var board = document.getElementById('kanban-board');
  var search = (document.getElementById('kb-search').value || '').toLowerCase();
  var statuses = ['idea','backlog','needs-thought','todo','doing','partial-done','review','done','failed'];
  var labels = {idea:'Idea','needs-thought':'Needs Thought',backlog:'Backlog',todo:'Todo',doing:'Doing','partial-done':'Partial',review:'Review',done:'Done',failed:'Failed'};

  if (!cachedBoardData) {
    board.innerHTML = '<div style="color:var(--muted);font-size:13px;padding:40px;text-align:center;width:100%">Loading board...</div>';
    return;
  }

  var totalCost = cachedBoardData.stats ? cachedBoardData.stats.totalCost : 0;
  document.getElementById('kb-total-cost').textContent = totalCost > 0 ? 'Total: $' + totalCost.toFixed(2) : '';

  board.innerHTML = statuses.map(function(status) {
    var tasks = (cachedBoardData.columns[status] || []);
    if (search) {
      tasks = tasks.filter(function(t) {
        return (t.title||'').toLowerCase().includes(search) ||
               (t.project||'').toLowerCase().includes(search) ||
               (t.assignee||'').toLowerCase().includes(search) ||
               (t.workflow||'').toLowerCase().includes(search) ||
               (t.description||'').toLowerCase().includes(search);
      });
    }
    var cards = tasks.map(function(t) {
      var badges = '';
      if (t.type && t.type !== 'feat') badges += '<span class="kanban-badge" style="background:var(--surface);color:var(--muted)">' + esc(t.type) + '</span>';
      if (t.project && t.project !== 'default') badges += '<span class="kanban-badge kanban-badge-project">' + esc(t.project) + '</span>';
      if (t.assignee) badges += '<span class="kanban-badge kanban-badge-assignee">@' + esc(t.assignee) + '</span>';
      if (t.priority === 'urgent') badges += '<span class="kanban-badge kanban-badge-priority-urgent">urgent</span>';
      else if (t.priority === 'high') badges += '<span class="kanban-badge kanban-badge-priority-high">high</span>';
      if (t.costUsd > 0) badges += '<span class="kanban-badge kanban-badge-cost">$' + t.costUsd.toFixed(2) + '</span>';
      if (t.model) badges += '<span class="kanban-badge kanban-badge-model">' + esc(t.model) + '</span>';
      if (t.workflow && t.workflow !== 'none') badges += '<span class="kanban-badge kanban-badge-workflow">' + esc(t.workflow) + '</span>';
      var shortId = esc(t.id.replace('task-', '').slice(-8));
      return '<div class="kanban-card" draggable="true" data-task-id="' + esc(t.id) + '" onclick="openTaskDetail(\'' + esc(t.id) + '\')" ondragstart="onCardDragStart(event)" ondragend="onCardDragEnd(event)">' +
        '<div class="kanban-card-title">' + esc(t.title) + '</div>' +
        (badges ? '<div class="kanban-card-badges">' + badges + '</div>' : '') +
        '<div class="kanban-card-id" title="' + esc(t.id) + '">#' + shortId + '</div>' +
      '</div>';
    }).join('');
    return '<div class="kanban-column" data-status="' + status + '" ondragover="onColumnDragOver(event)" ondragleave="onColumnDragLeave(event)" ondrop="onColumnDrop(event)">' +
      '<div class="kanban-column-header">' +
        '<span>' + labels[status] + '</span>' +
        '<span class="count">' + tasks.length + '</span>' +
      '</div>' +
      '<div class="kanban-column-body">' +
        (cards || '<div style="color:var(--muted);font-size:11px;text-align:center;padding:20px">No tasks</div>') +
      '</div>' +
    '</div>';
  }).join('');
}

function onCardDragStart(e) {
  var card = e.target.closest('.kanban-card');
  if (!card) return;
  e.dataTransfer.setData('text/plain', card.dataset.taskId);
  e.dataTransfer.effectAllowed = 'move';
  requestAnimationFrame(function() { card.classList.add('dragging'); });
}

function onCardDragEnd(e) {
  var card = e.target.closest('.kanban-card');
  if (card) card.classList.remove('dragging');
  document.querySelectorAll('.kanban-column.drag-over').forEach(function(col) {
    col.classList.remove('drag-over');
  });
}

function onColumnDragOver(e) {
  e.preventDefault();
  e.dataTransfer.dropEffect = 'move';
  var col = e.target.closest('.kanban-column');
  if (col) col.classList.add('drag-over');
}

function onColumnDragLeave(e) {
  var col = e.target.closest('.kanban-column');
  if (col && !col.contains(e.relatedTarget)) col.classList.remove('drag-over');
}

async function onColumnDrop(e) {
  e.preventDefault();
  var col = e.target.closest('.kanban-column');
  if (col) col.classList.remove('drag-over');
  var taskId = e.dataTransfer.getData('text/plain');
  var status = col ? col.dataset.status : '';
  if (!taskId || !status) return;
  try {
    var res = await fetch(API + '/api/tasks/' + taskId + '/move', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status: status })
    });
    if (!res.ok) {
      var err = await res.json().catch(function() { return { error: 'Move failed' }; });
      toast(err.error || 'Move failed');
      return;
    }
    refreshBoard();
  } catch(err) {
    toast('Error: ' + err.message);
  }
}

function filterBoard() {
  renderBoard();
}

// --- Task Detail Modal ---

async function openTaskDetail(taskId) {
  history.pushState({taskId: taskId}, '', '#task/' + taskId);
  await _showTaskDetail(taskId);
}

// Pure display function — no history manipulation.
// Use this from popstate and initial hash loading.
async function _showTaskDetail(taskId) {
  document.getElementById('td-id').value = taskId;
  var idDisplay = document.getElementById('td-id-display');
  if (idDisplay) idDisplay.textContent = taskId;
  try {
    var task = await fetchJSON('/api/tasks/' + taskId);
    document.getElementById('task-detail-title').textContent = task.title || 'Task Detail';
    document.getElementById('td-status').value = task.status || 'backlog';
    document.getElementById('td-priority').value = task.priority || 'normal';
    document.getElementById('td-title-input').value = task.title || '';
    document.getElementById('td-desc-input').value = task.description || '';

    // Populate assignee dropdown.
    var assigneeSel = document.getElementById('td-assignee');
    assigneeSel.innerHTML = '<option value="">Unassigned</option>';
    var agents = (cachedBoardData && cachedBoardData.agents) || [];
    // Also fetch roles.
    try {
      var roles = await fetchJSON('/roles');
      if (Array.isArray(roles)) {
        roles.forEach(function(r) {
          var name = typeof r === 'string' ? r : r.name;
          if (name && agents.indexOf(name) < 0) agents.push(name);
        });
      }
    } catch(e) {}
    agents.forEach(function(a) {
      var n = agentName(a);
      if (!n) return;
      var opt = document.createElement('option');
      opt.value = n; opt.textContent = n;
      assigneeSel.appendChild(opt);
    });
    assigneeSel.value = agentName(task.assignee) || '';

    // Populate project dropdown.
    var projSel = document.getElementById('td-project');
    projSel.innerHTML = '<option value="default">default</option>';
    cachedProjects.forEach(function(p) {
      var opt = document.createElement('option');
      opt.value = p.name; opt.textContent = p.name;
      projSel.appendChild(opt);
    });
    projSel.value = task.project || 'default';

    // Type.
    document.getElementById('td-type').value = task.type || 'feat';

    // Model.
    document.getElementById('td-model').value = task.model || '';
    updateModelTier('td');

    // Workflow override.
    var wfSel = document.getElementById('td-workflow');
    if (wfSel) {
      var names = await getWorkflowNames();
      wfSel.innerHTML = '<option value="">Default</option><option value="none">None (direct dispatch)</option>';
      names.forEach(function(name) {
        var opt = document.createElement('option');
        opt.value = name; opt.textContent = name;
        wfSel.appendChild(opt);
      });
      wfSel.value = task.workflow || '';
    }

    // Cost & duration display.
    var costEl = document.getElementById('td-cost-display');
    var durEl = document.getElementById('td-duration-display');
    var datesEl = document.getElementById('td-dates-display');
    costEl.textContent = task.costUsd > 0 ? 'Cost: $' + task.costUsd.toFixed(4) : '';
    durEl.textContent = task.durationMs > 0 ? 'Duration: ' + formatDuration(task.durationMs) : '';
    datesEl.textContent = 'Created: ' + (task.createdAt || '').substring(0, 10);
    if (task.completedAt) datesEl.textContent += ' | Done: ' + task.completedAt.substring(0, 10);

    // Show/hide cancel button based on status.
    var cancelBtn = document.getElementById('td-cancel-btn');
    if (cancelBtn) cancelBtn.style.display = task.status === 'doing' ? '' : 'none';

    // Load workflow step progress.
    loadTaskWfProgress(task);

    // Load subtask tree.
    loadSubtaskTree(taskId);

    // Load comments.
    loadTaskComments(taskId);

    // Load diff review panel for review/done tasks.
    if (task.status === 'review' || task.status === 'done') {
      loadDiffReview(taskId);
    } else {
      document.getElementById('td-diff-review').style.display = 'none';
    }

    document.getElementById('task-detail-modal').classList.add('open');
  } catch(e) {
    toast('Error loading task: ' + e.message);
  }
}

function closeTaskDetail() {
  if (taskWfSSE) { taskWfSSE.close(); taskWfSSE = null; }
  document.getElementById('task-detail-modal').classList.remove('open');
  if (location.hash.startsWith('#task/')) {
    history.pushState(null, '', location.pathname + location.search);
  }
}

async function deleteTask() {
  var id = document.getElementById('td-id').value;
  if (!id) return;
  if (!confirm('刪除這個 Task？此操作無法復原。')) return;
  try {
    await fetch('/api/tasks/' + id, { method: 'DELETE' });
    closeTaskDetail();
    refreshBoard();
    toast('Task 已刪除');
  } catch(e) {
    toast('刪除失敗: ' + e.message);
  }
}

async function loadSubtaskTree(taskId) {
  var section = document.getElementById('td-subtask-section');
  var treeEl = document.getElementById('td-subtask-tree');
  try {
    var data = await fetchJSON('/api/tasks/' + taskId + '/subtasks');
    if (!data.children || data.children.length === 0) {
      section.style.display = 'none';
      return;
    }
    section.style.display = '';
    treeEl.innerHTML = renderSubtaskNodes(data.children, 0);
  } catch(e) {
    section.style.display = 'none';
  }
}

function renderSubtaskNodes(nodes, depth) {
  return nodes.map(function(node) {
    var t = node.task;
    var statusColors = {backlog:'var(--muted)',todo:'var(--accent2)',doing:'var(--accent)',review:'var(--warn)',done:'var(--green)',failed:'var(--red)'};
    var statusColor = statusColors[t.status] || 'var(--muted)';
    var badge = '<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:' + statusColor + ';margin-right:5px"></span>';
    var assignee = t.assignee ? '<span style="color:var(--muted);font-size:11px;margin-left:6px">@' + esc(t.assignee) + '</span>' : '';
    var status = '<span style="color:var(--muted);font-size:11px;margin-left:6px">' + esc(t.status) + '</span>';
    var title = '<span style="font-size:12px;cursor:pointer" onclick="openTaskDetail(\'' + esc(t.id) + '\')">' + esc(t.title) + '</span>';
    var content = badge + title + status + assignee;
    var childrenHtml = '';
    if (node.children && node.children.length > 0) {
      childrenHtml = '<div style="padding-left:16px">' + renderSubtaskNodes(node.children, depth + 1) + '</div>';
      var summary = '<summary style="padding:3px 0;list-style:none;cursor:pointer"><span style="margin-right:4px;font-size:10px;color:var(--muted)">▶</span>' + content + '</summary>';
      return '<details open style="padding-left:' + (depth > 0 ? '0' : '0') + 'px">' + summary + childrenHtml + '</details>';
    }
    if (node.truncated) {
      childrenHtml = '<div style="padding-left:16px;color:var(--muted);font-size:11px;padding:2px 0">…さらに子タスクあり</div>';
      var summary2 = '<summary style="padding:3px 0;list-style:none;cursor:pointer"><span style="margin-right:4px;font-size:10px;color:var(--muted)">▶</span>' + content + '</summary>';
      return '<details style="padding-left:0">' + summary2 + childrenHtml + '</details>';
    }
    return '<div style="padding:3px 0;padding-left:12px">' + content + '</div>';
  }).join('');
}

async function loadTaskComments(taskId) {
  var thread = document.getElementById('td-comment-thread');
  try {
    var data = await fetchJSON('/api/tasks/' + taskId + '/thread');
    var comments = data.comments || [];
    if (comments.length === 0) {
      thread.innerHTML = '<div style="color:var(--muted);font-size:12px;padding:10px;text-align:center">No comments yet</div>';
      return;
    }
    thread.innerHTML = comments.map(function(c) {
      return '<div class="task-comment">' +
        '<span class="task-comment-author">' + esc(c.author) + '</span>' +
        '<span class="task-comment-time">' + (c.createdAt || '').substring(0, 16).replace('T', ' ') + '</span>' +
        '<div class="task-comment-content">' + esc(c.content) + '</div>' +
      '</div>';
    }).join('');
    thread.scrollTop = thread.scrollHeight;
  } catch(e) {
    thread.innerHTML = '<div style="color:var(--red);font-size:12px;padding:10px">Error loading comments</div>';
  }
}

async function updateTaskField(field) {
  var taskId = document.getElementById('td-id').value;
  if (!taskId) return;

  if (field === 'status') {
    var newStatus = document.getElementById('td-status').value;
    // Toggle cancel button visibility.
    var cb = document.getElementById('td-cancel-btn');
    if (cb) cb.style.display = newStatus === 'doing' ? '' : 'none';
    try {
      await fetchJSON('/api/tasks/' + taskId + '/move', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ status: newStatus }),
      });
      toast('Status updated');
      refreshBoard();
    } catch(e) { toast('Error: ' + e.message); }
    return;
  }

  var updates = {};
  if (field === 'title') updates.title = document.getElementById('td-title-input').value;
  else if (field === 'description') updates.description = document.getElementById('td-desc-input').value;
  else if (field === 'priority') updates.priority = document.getElementById('td-priority').value;
  else if (field === 'assignee') updates.assignee = document.getElementById('td-assignee').value;
  else if (field === 'project') updates.project = document.getElementById('td-project').value;
  else if (field === 'model') updates.model = document.getElementById('td-model').value;
  else if (field === 'workflow') updates.workflow = document.getElementById('td-workflow').value;
  else if (field === 'type') updates.type = document.getElementById('td-type').value;

  try {
    await fetchJSON('/api/tasks/' + taskId, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updates),
    });
    toast(field.charAt(0).toUpperCase() + field.slice(1) + ' updated');
    if (field === 'title') {
      document.getElementById('task-detail-title').textContent = updates.title;
    }
    refreshBoard();
  } catch(e) { toast('Error: ' + e.message); }
}

function updateModelTier(prefix) {
  var model = document.getElementById(prefix + '-model').value;
  var tier = document.getElementById(prefix + '-model-tier');
  if (!model) { tier.textContent = ''; tier.className = 'model-tier'; return; }
  var map = { haiku: ['FAST', 'model-tier-fast'], sonnet: ['BALANCED', 'model-tier-balanced'], opus: ['DEEP', 'model-tier-deep'] };
  var info = map[model] || ['', ''];
  tier.textContent = info[0];
  tier.className = 'model-tier ' + info[1];
}

async function addTaskComment() {
  var taskId = document.getElementById('td-id').value;
  var input = document.getElementById('td-comment-input');
  var content = input.value.trim();
  if (!taskId || !content) return;

  try {
    await fetchJSON('/api/tasks/' + taskId + '/comment', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ author: 'user', content: content }),
    });
    input.value = '';
    loadTaskComments(taskId);
  } catch(e) { toast('Error: ' + e.message); }
}

// --- Diff Review Panel ---

var diffReviewState = {
  taskId: null,
  files: [],       // parsed file list from diff
  rawDiff: '',     // full diff text
  activeFile: null, // currently selected file
  reviewComments: [], // inline comments added
};

async function loadDiffReview(taskId) {
  var panel = document.getElementById('td-diff-review');
  diffReviewState.taskId = taskId;
  diffReviewState.reviewComments = [];

  try {
    var data = await fetchJSON('/api/tasks/' + taskId + '/diff');
    var diff = data.diff || '';

    if (!diff) {
      panel.style.display = 'none';
      return;
    }

    diffReviewState.rawDiff = diff;
    diffReviewState.files = parseDiffFiles(diff);

    panel.style.display = 'block';
    renderDiffFileTree();

    // Show first file by default.
    if (diffReviewState.files.length > 0) {
      selectDiffFile(0);
    } else {
      document.getElementById('td-diff-viewer').innerHTML = '<div class="diff-no-changes">No file changes to display</div>';
    }

    // Load existing review comments.
    loadReviewComments(taskId);

    // Show/hide review actions based on task status.
    var status = document.getElementById('td-status').value;
    document.getElementById('td-review-actions').style.display =
      (status === 'review' || status === 'done') ? 'flex' : 'none';
  } catch(e) {
    panel.style.display = 'none';
  }
}

function parseDiffFiles(diff) {
  var files = [];
  var chunks = diff.split(/^diff --git /m);

  for (var i = 1; i < chunks.length; i++) {
    var chunk = chunks[i];
    var lines = chunk.split('\n');

    // Parse file path from "a/path b/path"
    var headerMatch = lines[0].match(/a\/(.+?)\s+b\/(.+)/);
    if (!headerMatch) continue;

    var filePath = headerMatch[2];
    var status = 'modified';

    // Check for new/deleted file
    if (chunk.indexOf('new file mode') >= 0) status = 'added';
    else if (chunk.indexOf('deleted file mode') >= 0) status = 'deleted';

    // Count additions/deletions
    var additions = 0, deletions = 0;
    var diffLines = chunk.split('\n');
    for (var j = 0; j < diffLines.length; j++) {
      if (diffLines[j].charAt(0) === '+' && diffLines[j].charAt(1) !== '+') additions++;
      else if (diffLines[j].charAt(0) === '-' && diffLines[j].charAt(1) !== '-') deletions++;
    }

    files.push({
      path: filePath,
      status: status,
      additions: additions,
      deletions: deletions,
      rawChunk: 'diff --git ' + chunk,
    });
  }

  return files;
}

function renderDiffFileTree() {
  var tree = document.getElementById('td-diff-file-tree');
  tree.innerHTML = diffReviewState.files.map(function(f, idx) {
    var statusChar = f.status === 'added' ? 'A' : f.status === 'deleted' ? 'D' : 'M';
    var statusCls = f.status;
    return '<div class="diff-file-item' + (idx === 0 ? ' active' : '') + '" onclick="selectDiffFile(' + idx + ')">' +
      '<span class="file-status ' + statusCls + '">' + statusChar + '</span>' +
      '<span>' + esc(f.path.split('/').pop()) + '</span>' +
      '<span style="color:var(--muted);font-size:10px;margin-left:4px">' + esc(f.path.substring(0, f.path.lastIndexOf('/'))) + '</span>' +
      '<span class="file-stats"><span class="add">+' + f.additions + '</span> <span class="del">-' + f.deletions + '</span></span>' +
    '</div>';
  }).join('');
}

function selectDiffFile(idx) {
  diffReviewState.activeFile = idx;

  // Update file tree selection.
  var items = document.querySelectorAll('.diff-file-item');
  items.forEach(function(el, i) {
    el.classList.toggle('active', i === idx);
  });

  var file = diffReviewState.files[idx];
  if (!file) return;

  renderDiffContent(file);
}

function renderDiffContent(file) {
  var viewer = document.getElementById('td-diff-viewer');
  var lines = file.rawChunk.split('\n');
  var html = '';
  var oldLineNum = 0;
  var newLineNum = 0;
  var inHunk = false;

  for (var i = 0; i < lines.length; i++) {
    var line = lines[i];

    // Hunk header
    var hunkMatch = line.match(/^@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@(.*)/);
    if (hunkMatch) {
      oldLineNum = parseInt(hunkMatch[1]);
      newLineNum = parseInt(hunkMatch[2]);
      inHunk = true;
      html += '<div class="diff-hunk-header">' + esc(line) + '</div>';
      continue;
    }

    if (!inHunk) continue;

    var cls = '';
    var displayLineNum = '';

    if (line.charAt(0) === '+') {
      cls = 'added';
      displayLineNum = newLineNum;
      newLineNum++;
    } else if (line.charAt(0) === '-') {
      cls = 'removed';
      displayLineNum = oldLineNum;
      oldLineNum++;
    } else if (line.charAt(0) === '\\') {
      continue; // "No newline at end of file"
    } else {
      displayLineNum = newLineNum;
      oldLineNum++;
      newLineNum++;
    }

    var fileForComment = file.path;
    var lineForComment = displayLineNum;

    html += '<div class="diff-line ' + cls + '" data-file="' + esc(fileForComment) + '" data-line="' + lineForComment + '">' +
      '<button class="diff-line-comment-btn" onclick="openInlineCommentForm(this, \'' + esc(fileForComment) + '\', ' + lineForComment + ')" title="Add comment">+</button>' +
      '<div class="diff-line-num">' + displayLineNum + '</div>' +
      '<div class="diff-line-content">' + esc(line.substring(1)) + '</div>' +
    '</div>';

    // Show any existing inline comments for this line.
    var lineComments = diffReviewState.reviewComments.filter(function(c) {
      return c.file === fileForComment && c.line === lineForComment;
    });
    lineComments.forEach(function(c) {
      html += '<div class="diff-inline-comment"><span class="comment-author">' + esc(c.author || 'user') + '</span>' + esc(c.comment) + '</div>';
    });
  }

  if (!html) {
    html = '<div class="diff-no-changes">Binary file or no displayable changes</div>';
  }

  viewer.innerHTML = html;
}

function openInlineCommentForm(btn, file, line) {
  // Remove any existing open forms.
  var existing = document.querySelector('.diff-inline-comment-form');
  if (existing) existing.remove();

  var form = document.createElement('div');
  form.className = 'diff-inline-comment-form';
  form.innerHTML = '<div style="font-size:11px;color:var(--muted);margin-bottom:4px">' + esc(file) + ':' + line + '</div>' +
    '<textarea id="inline-comment-text" placeholder="Add review comment..."></textarea>' +
    '<div class="form-actions">' +
    '<button class="btn" onclick="this.closest(\'.diff-inline-comment-form\').remove()" style="font-size:11px;padding:4px 10px">Cancel</button>' +
    '<button class="btn btn-primary" onclick="submitInlineComment(\'' + esc(file) + '\', ' + line + ')" style="font-size:11px;padding:4px 10px">Comment</button>' +
    '</div>';

  // Insert after the clicked line.
  var lineEl = btn.closest('.diff-line');
  lineEl.parentNode.insertBefore(form, lineEl.nextSibling);
  form.querySelector('textarea').focus();
}

async function submitInlineComment(file, line) {
  var text = document.getElementById('inline-comment-text');
  if (!text) return;
  var comment = text.value.trim();
  if (!comment) return;

  var taskId = diffReviewState.taskId;
  try {
    await fetchJSON('/api/tasks/' + taskId + '/review-comment', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ file: file, line: line, comment: comment, author: 'user' }),
    });

    // Add to local state.
    diffReviewState.reviewComments.push({ file: file, line: line, comment: comment, author: 'user' });

    // Remove form and re-render.
    var form = document.querySelector('.diff-inline-comment-form');
    if (form) form.remove();

    // Re-render current file to show the comment.
    if (diffReviewState.activeFile !== null) {
      renderDiffContent(diffReviewState.files[diffReviewState.activeFile]);
    }

    updateReviewCount();
    toast('Comment added');
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

async function loadReviewComments(taskId) {
  try {
    var data = await fetchJSON('/api/tasks/' + taskId + '/thread');
    var comments = data.comments || [];
    diffReviewState.reviewComments = [];

    comments.forEach(function(c) {
      if (c.Type === 'review' || c.type === 'review') {
        try {
          var parsed = JSON.parse(c.Content || c.content);
          parsed.author = c.Author || c.author;
          parsed.id = c.ID || c.id;
          diffReviewState.reviewComments.push(parsed);
        } catch(e) {}
      }
    });

    updateReviewCount();
    renderReviewCommentsList();
  } catch(e) {}
}

function updateReviewCount() {
  var count = diffReviewState.reviewComments.length;
  var el = document.getElementById('td-review-count');
  el.textContent = count > 0 ? count : '';
}

function renderReviewCommentsList() {
  var list = document.getElementById('td-review-comments-list');
  var comments = diffReviewState.reviewComments;

  if (comments.length === 0) {
    list.innerHTML = '<div style="color:var(--muted);font-size:12px;padding:16px;text-align:center">No review comments yet. Click the + button on diff lines to add comments.</div>';
    return;
  }

  list.innerHTML = comments.map(function(c) {
    return '<div class="diff-inline-comment" style="margin-bottom:6px">' +
      '<div style="display:flex;justify-content:space-between;align-items:center">' +
      '<span class="comment-author">' + esc(c.author || 'user') + '</span>' +
      '<span style="font-size:10px;color:var(--muted);font-family:var(--font-mono)">' + esc(c.file || '') + ':' + (c.line || '') + '</span>' +
      '</div>' +
      '<div style="margin-top:4px">' + esc(c.comment) + '</div>' +
    '</div>';
  }).join('');
}

function switchDiffTab(tab) {
  var tabs = document.querySelectorAll('.diff-review-tab');
  tabs.forEach(function(t) { t.classList.remove('active'); });

  if (tab === 'changes') {
    tabs[0].classList.add('active');
    document.getElementById('td-diff-changes-tab').style.display = 'block';
    document.getElementById('td-diff-comments-tab').style.display = 'none';
  } else {
    tabs[1].classList.add('active');
    document.getElementById('td-diff-changes-tab').style.display = 'none';
    document.getElementById('td-diff-comments-tab').style.display = 'block';
  }
}

async function submitReviewFeedback(action) {
  var taskId = diffReviewState.taskId;
  var summary = document.getElementById('td-review-summary').value.trim();

  if (action === 'approve' && !confirm('Approve this task and mark as done?')) return;
  if (action === 'request-changes' && !summary && diffReviewState.reviewComments.length === 0) {
    toast('Please add review comments or a summary before requesting changes.');
    return;
  }

  try {
    var result = await fetchJSON('/api/tasks/' + taskId + '/review-feedback', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: action, summary: summary, author: 'user' }),
    });

    if (action === 'approve') {
      document.getElementById('td-status').value = 'done';
      toast('Task approved and marked as done');
    } else {
      toast('Changes requested (' + (result.commentCount || 0) + ' comments compiled)');
    }

    refreshBoard();
    loadTaskComments(taskId);
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

// --- Task Create Modal ---

async function openTaskCreateModal() {
  document.getElementById('task-create-form').reset();
  // Populate dropdowns.
  var projSel = document.getElementById('tcf-project');
  projSel.innerHTML = '<option value="default">default</option>';
  cachedProjects.forEach(function(p) {
    var opt = document.createElement('option');
    opt.value = p.name; opt.textContent = p.name;
    projSel.appendChild(opt);
  });

  var agentSel = document.getElementById('tcf-assignee');
  agentSel.innerHTML = '<option value="">Unassigned</option>';
  try {
    var roles = await fetchJSON('/roles');
    if (Array.isArray(roles)) {
      roles.forEach(function(r) {
        var name = typeof r === 'string' ? r : r.name;
        if (!name) return;
        var opt = document.createElement('option');
        opt.value = name; opt.textContent = name;
        agentSel.appendChild(opt);
      });
    }
  } catch(e) {}

  document.getElementById('task-create-modal').classList.add('open');
}

function closeTaskCreate() {
  document.getElementById('task-create-modal').classList.remove('open');
}

async function submitNewTask(e) {
  e.preventDefault();
  var body = {
    title: document.getElementById('tcf-title').value.trim(),
    description: document.getElementById('tcf-desc').value.trim(),
    project: document.getElementById('tcf-project').value,
    assignee: document.getElementById('tcf-assignee').value,
    priority: document.getElementById('tcf-priority').value,
    status: document.getElementById('tcf-status').value,
    model: document.getElementById('tcf-model').value,
    type: document.getElementById('tcf-type').value,
  };
  if (!body.title) { toast('Title is required'); return false; }
  try {
    await fetchJSON('/api/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    toast('Task created');
    closeTaskCreate();
    refreshBoard();
  } catch(e) { toast('Error: ' + e.message); }
  return false;
}

async function scanWorkspaceProjects() {
  try {
    var data = await fetchJSON('/api/projects/scan-workspace');
    var entries = data.entries || [];
    if (entries.length === 0) {
      toast('No projects found in workspace');
      return;
    }
    // Show selection dialog.
    var existing = cachedProjects.map(function(p) { return p.name.toLowerCase(); });
    var newEntries = entries.filter(function(e) { return existing.indexOf(e.name.toLowerCase()) < 0; });
    if (newEntries.length === 0) {
      toast('All workspace projects already imported');
      return;
    }
    var msg = 'Import ' + newEntries.length + ' new project(s)?\n\n' + newEntries.map(function(e) { return '- ' + e.name + (e.category ? ' [' + e.category + ']' : ''); }).join('\n');
    if (!confirm(msg)) return;
    var imported = 0;
    for (var i = 0; i < newEntries.length; i++) {
      var e = newEntries[i];
      try {
        await fetchJSON('/api/projects', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: e.name,
            description: e.description,
            category: e.category,
            workdir: e.workdir || '',
            status: 'active',
          }),
        });
        imported++;
      } catch(err) {
        console.warn('Failed to import ' + e.name + ': ' + err.message);
      }
    }
    toast('Imported ' + imported + ' project(s)');
    refreshProjects();
  } catch(e) {
    toast('Error: ' + e.message);
  }
}

function dispatchToProject(workdir) {
  openDispatchModal();
  document.getElementById('df-workdir').value = workdir;
}

// --- Directory Browser ---

var dirBrowserTarget = '';  // ID of the input field to fill
var dirBrowserPath = '';    // Current browsing path

function openDirBrowser(targetInputId) {
  dirBrowserTarget = targetInputId;
  var current = document.getElementById(targetInputId).value.trim();
  var startPath = current || '~';
  document.getElementById('dir-browser-modal').classList.add('open');
  loadDirBrowser(startPath);
}

function closeDirBrowser() {
  document.getElementById('dir-browser-modal').classList.remove('open');
}

function selectDirBrowser() {
  if (dirBrowserTarget && dirBrowserPath) {
    document.getElementById(dirBrowserTarget).value = dirBrowserPath;
  }
  closeDirBrowser();
}

function dirBrowserUp() {
  if (!dirBrowserPath) return;
  // Go to parent by removing last path component.
  var parts = dirBrowserPath.split('/');
  if (parts.length > 2) {
    parts.pop();
    loadDirBrowser(parts.join('/'));
  }
}

async function loadDirBrowser(path) {
  var list = document.getElementById('dir-browser-list');
  var pathEl = document.getElementById('dir-browser-path');
  list.innerHTML = '<div style="color:var(--muted);padding:20px;text-align:center">Loading...</div>';
  pathEl.textContent = path;

  try {
    var data = await fetchJSON('/api/dirs?path=' + encodeURIComponent(path));
    dirBrowserPath = data.path;
    pathEl.textContent = data.path;
    pathEl.title = data.path;

    var dirs = data.dirs || [];
    if (dirs.length === 0) {
      list.innerHTML = '<div style="color:var(--muted);padding:20px;text-align:center;font-size:12px">No subdirectories</div>';
      return;
    }
    list.innerHTML = dirs.map(function(d) {
      return '<div class="dir-item" onclick="loadDirBrowser(\'' + d.path.replace(/'/g, "\\'") + '\')">' +
        '<span class="dir-item-icon">&#128193;</span>' +
        '<span class="dir-item-name">' + esc(d.name) + '</span>' +
      '</div>';
    }).join('');
  } catch(e) {
    list.innerHTML = '<div style="color:var(--red);padding:20px;text-align:center;font-size:12px">Error: ' + esc(e.message) + '</div>';
  }
}

// --- Batch Add Projects ---

var batchBrowsePath = '';
var batchDirs = [];
var batchSelected = new Set();

function openBatchAddBrowser() {
  batchSelected = new Set();
  document.getElementById('batch-add-modal').classList.add('open');
  loadBatchBrowser('~/.tetora/workspace');
}

function closeBatchAdd() {
  document.getElementById('batch-add-modal').classList.remove('open');
}

function batchBrowseUp() {
  if (!batchBrowsePath) return;
  var parts = batchBrowsePath.split('/');
  if (parts.length > 2) {
    parts.pop();
    loadBatchBrowser(parts.join('/'));
  }
}

async function loadBatchBrowser(path) {
  var list = document.getElementById('batch-dir-list');
  var pathEl = document.getElementById('batch-browse-path');
  list.innerHTML = '<div style="color:var(--muted);padding:20px;text-align:center">Loading...</div>';
  pathEl.textContent = path;
  batchSelected = new Set();
  document.getElementById('batch-select-all').checked = false;
  updateBatchCount();

  try {
    var data = await fetchJSON('/api/dirs?path=' + encodeURIComponent(path));
    batchBrowsePath = data.path;
    batchDirs = data.dirs || [];
    pathEl.textContent = data.path;
    pathEl.title = data.path;

    if (batchDirs.length === 0) {
      list.innerHTML = '<div style="color:var(--muted);padding:20px;text-align:center;font-size:12px">No subdirectories</div>';
      return;
    }
    // Filter out already-added projects.
    var existingPaths = cachedProjects.map(function(p) { return p.workdir; });

    list.innerHTML = batchDirs.map(function(d) {
      var exists = existingPaths.indexOf(d.path) >= 0;
      return '<div class="dir-item' + (exists ? '' : '') + '" style="' + (exists ? 'opacity:0.4' : '') + '">' +
        '<input type="checkbox" class="dir-item-check" data-path="' + esc(d.path) + '" data-name="' + esc(d.name) + '"' +
          (exists ? ' disabled title="Already added"' : '') +
          ' onchange="batchToggleItem(this)">' +
        '<span class="dir-item-icon">&#128193;</span>' +
        '<span class="dir-item-name">' + esc(d.name) + '</span>' +
        (exists ? '<span style="font-size:10px;color:var(--green)">added</span>' : '') +
        '<button class="btn" onclick="event.stopPropagation();loadBatchBrowser(\'' + d.path.replace(/'/g, "\\'") + '\')" style="padding:2px 8px;font-size:10px;margin-left:4px">open</button>' +
      '</div>';
    }).join('');
  } catch(e) {
    list.innerHTML = '<div style="color:var(--red);padding:20px;text-align:center;font-size:12px">Error: ' + esc(e.message) + '</div>';
  }
}

function batchToggleItem(el) {
  var path = el.dataset.path;
  if (el.checked) {
    batchSelected.add(path);
  } else {
    batchSelected.delete(path);
  }
  updateBatchCount();
}

function batchToggleAll() {
  var all = document.getElementById('batch-select-all').checked;
  document.querySelectorAll('#batch-dir-list .dir-item-check:not(:disabled)').forEach(function(el) {
    el.checked = all;
    if (all) batchSelected.add(el.dataset.path);
    else batchSelected.delete(el.dataset.path);
  });
  updateBatchCount();
}

function updateBatchCount() {
  document.getElementById('batch-count').textContent = batchSelected.size + ' selected';
  document.getElementById('batch-add-btn').textContent = 'Add ' + (batchSelected.size || '') + ' Selected';
}

async function submitBatchAdd() {
  if (batchSelected.size === 0) { toast('No folders selected'); return; }
  var items = [];
  document.querySelectorAll('#batch-dir-list .dir-item-check:checked').forEach(function(el) {
    items.push({ name: el.dataset.name, path: el.dataset.path });
  });
  var added = 0;
  for (var i = 0; i < items.length; i++) {
    try {
      await fetchJSON('/api/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: items[i].name,
          workdir: items[i].path,
          status: 'active',
        }),
      });
      added++;
    } catch(e) {
      console.warn('Failed to add ' + items[i].name + ': ' + e.message);
    }
  }
  toast('Added ' + added + ' project(s)');
  closeBatchAdd();
  cachedProjects = [];
  refreshProjects();
}

// --- Workflow Step Progress in Task Detail ---

async function loadTaskWfProgress(task) {
  var container = document.getElementById('td-wf-progress');
  if (taskWfSSE) { taskWfSSE.close(); taskWfSSE = null; }

  var runId = task.workflowRunId;

  // If no runId yet but task is doing with a workflow, try to find running run by taskId variable.
  if (!runId && task.status === 'doing' && task.workflow && task.workflow !== 'none') {
    runId = await findRunningWfRunForTask(task.id);
  }

  if (!runId) {
    container.style.display = 'none';
    return;
  }

  container.style.display = '';
  container.dataset.runId = runId;

  try {
    var data = await fetchJSON('/workflow-runs/' + runId);
    var run = data.run || data;
    renderTaskWfProgress(run);

    if (run.status === 'running' || run.status === 'waiting') {
      subscribeTaskWfSSE(runId);
    }
    // Load inline human gate cards for this run.
    renderInlineTaskGates(runId);
  } catch(e) {
    container.style.display = 'none';
  }
}

async function findRunningWfRunForTask(taskId) {
  try {
    var runs = await fetchJSON('/workflow-runs');
    if (!Array.isArray(runs)) return null;
    for (var i = 0; i < runs.length; i++) {
      var r = runs[i];
      if (r.variables && r.variables._taskId === taskId) return r.id;
      if (r.variables && r.variables.taskId === taskId) return r.id;
    }
  } catch(e) {}
  return null;
}

function renderTaskWfProgress(run) {
  var statusEl = document.getElementById('td-wf-run-status');
  var stepsEl = document.getElementById('td-wf-steps');

  var statusCls = run.status === 'success' ? 'badge-ok' : (run.status === 'error' || run.status === 'timeout') ? 'badge-err' : 'badge-warn';
  statusEl.textContent = run.status;
  statusEl.className = 'badge ' + statusCls;

  // Show/hide workflow cancel button.
  var wfCancelBtn = document.getElementById('td-wf-cancel-btn');
  if (wfCancelBtn) wfCancelBtn.style.display = run.status === 'running' ? '' : 'none';

  var stepResults = run.stepResults || {};
  var steps = Object.values(stepResults);
  steps.sort(function(a, b) { return (a.startedAt || '').localeCompare(b.startedAt || ''); });

  stepsEl.innerHTML = steps.map(function(s) {
    var icon = '&#9679;';
    var cls = 'td-wf-step';
    if (s.status === 'success') { icon = '&#10003;'; cls += ' step-success'; }
    else if (s.status === 'error') { icon = '&#10007;'; cls += ' step-error'; }
    else if (s.status === 'running') { icon = '&#9654;'; cls += ' step-running'; }
    else if (s.status === 'skipped') { icon = '&#8212;'; cls += ' step-skipped'; }
    else { cls += ' step-pending'; }

    var dur = '';
    if (s.durationMs > 0) dur = ' <span class="td-wf-step-dur">' + formatDuration(s.durationMs) + '</span>';

    return '<div class="' + cls + '" data-step-id="' + esc(s.stepId) + '">' +
      '<span class="td-wf-step-icon">' + icon + '</span>' +
      '<span class="td-wf-step-name">' + esc(s.stepId) + '</span>' +
      dur +
    '</div>';
  }).join('');

  // Resume action for failed/cancelled/timeout workflow runs.
  var actionsEl = document.getElementById('td-wf-actions');
  if (!actionsEl) {
    actionsEl = document.createElement('div');
    actionsEl.id = 'td-wf-actions';
    actionsEl.style.cssText = 'margin-top:8px';
    stepsEl.parentElement.appendChild(actionsEl);
  }
  if (run.status === 'error' || run.status === 'cancelled' || run.status === 'timeout') {
    var completed = steps.filter(function(s) { return s.status === 'success' || s.status === 'skipped'; }).length;
    actionsEl.innerHTML =
      '<div style="font-size:11px;color:var(--muted);margin-bottom:6px">' +
        completed + '/' + steps.length + ' steps completed — workflow can be resumed from checkpoint' +
      '</div>' +
      '<button class="btn" style="font-size:11px;background:var(--accent);color:#fff" ' +
        'onclick="resumeTaskWorkflow()">Resume from Checkpoint</button>' +
      ' <button class="btn" style="font-size:11px" ' +
        'onclick="retryTaskWorkflowFresh()">Clear &amp; Re-dispatch</button>';
  } else if (run.status === 'resumed') {
    actionsEl.innerHTML =
      '<div style="font-size:11px;color:var(--muted)">This run was resumed as a new run.</div>';
  } else {
    actionsEl.innerHTML = '';
  }
}

function subscribeTaskWfSSE(runId) {
  if (taskWfSSE) { taskWfSSE.close(); }
  var url = '/dispatch/workflow:' + runId + '/stream';
  taskWfSSE = new EventSource(url);
  taskWfSSE.onmessage = function(e) {
    try {
      var ev = JSON.parse(e.data);
      if (ev.type === 'step_started' && ev.data) {
        updateTaskWfStep(ev.data.stepId, 'running');
      }
      if (ev.type === 'step_completed' && ev.data) {
        updateTaskWfStep(ev.data.stepId, ev.data.status, ev.data.durationMs);
      }
      if (ev.type === 'human_gate_waiting' && ev.data) {
        addInlineHgCard(ev.data);
      }
      if (ev.type === 'human_gate_responded' && ev.data) {
        removeInlineHgCard(ev.data.hgKey);
      }
      if (ev.type === 'workflow_completed') {
        if (taskWfSSE) { taskWfSSE.close(); taskWfSSE = null; }
        // Hide cancel buttons immediately.
        var wfCb = document.getElementById('td-wf-cancel-btn');
        if (wfCb) wfCb.style.display = 'none';
        var tdCb = document.getElementById('td-cancel-btn');
        if (tdCb) tdCb.style.display = 'none';
        // Refresh to get final state.
        var taskId = document.getElementById('td-id').value;
        if (taskId) {
          setTimeout(function() { openTaskDetail(taskId); }, 500);
        }
      }
    } catch(err) {}
  };
  taskWfSSE.onerror = function() {
    if (taskWfSSE) { taskWfSSE.close(); taskWfSSE = null; }
  };
}

function updateTaskWfStep(stepId, status, durationMs) {
  var el = document.querySelector('.td-wf-step[data-step-id="' + stepId + '"]');
  if (!el) return;

  el.className = 'td-wf-step';
  var iconEl = el.querySelector('.td-wf-step-icon');
  if (status === 'success') { el.classList.add('step-success'); if (iconEl) iconEl.innerHTML = '&#10003;'; }
  else if (status === 'error') { el.classList.add('step-error'); if (iconEl) iconEl.innerHTML = '&#10007;'; }
  else if (status === 'running') { el.classList.add('step-running'); if (iconEl) iconEl.innerHTML = '&#9654;'; }
  else if (status === 'skipped') { el.classList.add('step-skipped'); if (iconEl) iconEl.innerHTML = '&#8212;'; }
  else { el.classList.add('step-pending'); }

  if (durationMs > 0) {
    var durEl = el.querySelector('.td-wf-step-dur');
    if (!durEl) {
      durEl = document.createElement('span');
      durEl.className = 'td-wf-step-dur';
      el.appendChild(durEl);
    }
    durEl.textContent = formatDuration(durationMs);
  }
}

function openWfRunFromTask() {
  var container = document.getElementById('td-wf-progress');
  var runId = container ? container.dataset.runId : '';
  if (runId && typeof openWfRun === 'function') {
    closeTaskDetail();
    // Switch to workflows tab and open the run.
    var wfTab = document.querySelector('[data-tab="workflows"]');
    if (wfTab) wfTab.click();
    setTimeout(function() { openWfRun(runId); }, 200);
  }
  return false;
}

// Resume the failed workflow run for the current task from checkpoint.
async function resumeTaskWorkflow() {
  var container = document.getElementById('td-wf-progress');
  var runId = container ? container.dataset.runId : '';
  if (!runId) { toast('No workflow run to resume'); return; }
  if (!confirm('Resume workflow from checkpoint? Completed steps will be skipped.')) return;
  try {
    await fetchJSON('/workflow-runs/' + encodeURIComponent(runId) + '/resume', { method: 'POST' });
    toast('Workflow resume started');
    // Reload task detail after a delay to pick up the new run.
    var taskId = document.getElementById('td-id').value;
    setTimeout(function() {
      if (taskId) openTaskDetail(taskId);
    }, 2000);
  } catch(e) {
    toast('Resume failed: ' + (e.message || e));
  }
}

// Restart the task's workflow from scratch (clear workflowRunId, set to todo).
async function retryTaskWorkflowFresh() {
  var taskId = document.getElementById('td-id').value;
  if (!taskId) return;
  if (!confirm('Clear workflow progress and re-dispatch? The task will return to todo and start a fresh workflow run.')) return;
  try {
    await fetchJSON('/tasks/' + encodeURIComponent(taskId), {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status: 'todo', workflowRunId: '' }),
    });
    toast('Task reset to todo — will be re-dispatched');
    setTimeout(function() { openTaskDetail(taskId); refreshBoard(); }, 500);
  } catch(e) {
    toast('Reset failed: ' + (e.message || e));
  }
}

// ============================================================
// Human Gates — "Waiting for You" panel
// ============================================================

var hgPollTimer = null;

function startHumanGatePolling() {
  refreshHumanGates();
  if (hgPollTimer) clearInterval(hgPollTimer);
  hgPollTimer = setInterval(refreshHumanGates, 30000);
}

async function refreshHumanGates() {
  try {
    var gates = await fetchJSON('/api/human-gates?status=waiting');
    var list = Array.isArray(gates) ? gates : [];
    renderHumanGatesPanel(list, 'human-gates-panel', 'human-gates-list', 'hg-panel-badge');
    renderHumanGatesPanel(list, 'human-gates-panel-tasks', 'human-gates-list-tasks', 'hg-panel-badge-tasks');
    updateHumanGatesBadge(list.length);
  } catch(e) {}
}

function renderHumanGatesPanel(gates, panelId, listId, badgeId) {
  var panel = document.getElementById(panelId);
  var list = document.getElementById(listId);
  var badge = document.getElementById(badgeId);
  if (!panel || !list) return;
  if (badge) badge.textContent = gates.length;
  if (gates.length === 0) {
    panel.style.display = 'none';
    return;
  }
  var sorted = gates.slice().sort(function(a, b) {
    return (a.createdAt || '').localeCompare(b.createdAt || '');
  });
  panel.style.display = '';
  list.innerHTML = sorted.map(function(g) { return buildHgCardHtml(g, 'hg-dash-card-'); }).join('');
}

function buildHgCardHtml(hg, cardPrefix) {
  var key = hg.key || '';
  var subtype = hg.subtype || 'approval';
  var prompt = hg.prompt || '';
  var workflowName = hg.workflowName || '';
  var stepId = hg.stepId || '';
  var createdAt = hg.createdAt || '';
  var prefix = cardPrefix || 'hg-dash-card-';

  var icons = { approval: '&#x1F510;', action: '&#x2705;', input: '&#x270F;&#xFE0F;' };
  var icon = icons[subtype] || '&#x23F3;';
  var waitTime = hgWaitTime(createdAt);
  var inputId = prefix + escAttr(key) + '-input';

  var actionsHtml = '';
  if (subtype === 'approval') {
    actionsHtml =
      '<div class="hg-comment-row"><textarea class="hg-comment" id="' + inputId + '" placeholder="Comment (optional)..." rows="2"></textarea></div>' +
      '<div class="hg-btn-row">' +
      '<button class="btn btn-primary hg-btn-approve" onclick="hgApprove(\'' + escAttr(key) + '\',\'' + escAttr(prefix) + '\')">Approve</button>' +
      ' <button class="btn hg-btn-reject" onclick="hgReject(\'' + escAttr(key) + '\',\'' + escAttr(prefix) + '\')">Reject</button>' +
      '</div>';
  } else if (subtype === 'action') {
    actionsHtml =
      '<div class="hg-comment-row"><textarea class="hg-comment" id="' + inputId + '" placeholder="Note (optional)..." rows="2"></textarea></div>' +
      '<div class="hg-btn-row">' +
      '<button class="btn btn-primary hg-btn-done" onclick="hgComplete(\'' + escAttr(key) + '\',\'' + escAttr(prefix) + '\')">Mark Done</button>' +
      '</div>';
  } else {
    actionsHtml =
      '<div class="hg-comment-row"><input class="hg-input" id="' + inputId + '" type="text" placeholder="Your response..."></div>' +
      '<div class="hg-btn-row">' +
      '<button class="btn btn-primary hg-btn-submit" onclick="hgSubmit(\'' + escAttr(key) + '\',\'' + escAttr(prefix) + '\')">Submit</button>' +
      '</div>';
  }

  return '<div class="hg-card" id="' + escAttr(prefix) + escAttr(key) + '">' +
    '<div class="hg-card-header">' +
    '<span class="hg-icon">' + icon + '</span>' +
    '<div class="hg-meta">' +
    '<strong class="hg-wf-name">' + esc(workflowName || stepId) + '</strong>' +
    (stepId && workflowName ? '<span class="hg-step-id">' + esc(stepId) + '</span>' : '') +
    '</div>' +
    '<span class="badge badge-warn hg-subtype-badge">' + esc(subtype) + '</span>' +
    (waitTime ? '<span class="hg-wait-time">' + esc(waitTime) + '</span>' : '') +
    '</div>' +
    (prompt ? '<p class="hg-prompt">' + esc(prompt) + '</p>' : '') +
    '<div class="hg-actions">' + actionsHtml + '</div>' +
    '</div>';
}

function hgWaitTime(createdAt) {
  if (!createdAt) return '';
  var created = new Date(createdAt);
  if (isNaN(created.getTime())) return '';
  var diffMs = Date.now() - created.getTime();
  if (diffMs < 60000) return Math.floor(diffMs / 1000) + 's ago';
  if (diffMs < 3600000) return Math.floor(diffMs / 60000) + 'm ago';
  if (diffMs < 86400000) return Math.floor(diffMs / 3600000) + 'h ago';
  return Math.floor(diffMs / 86400000) + 'd ago';
}

function updateHumanGatesBadge(count) {
  var navBadge = document.getElementById('hg-nav-badge');
  if (navBadge) {
    navBadge.textContent = count;
    navBadge.style.display = count > 0 ? '' : 'none';
  }
}

function hgGetInputVal(key, prefix) {
  var inputId = (prefix || 'hg-dash-card-') + key + '-input';
  var el = document.getElementById(inputId);
  return el ? el.value : '';
}

function hgApprove(key, prefix)  { hgRespond(key, 'approve',  hgGetInputVal(key, prefix), prefix); }
function hgReject(key, prefix)   { hgRespond(key, 'reject',   hgGetInputVal(key, prefix), prefix); }
function hgComplete(key, prefix) { hgRespond(key, 'complete', hgGetInputVal(key, prefix), prefix); }
function hgSubmit(key, prefix) {
  var val = hgGetInputVal(key, prefix);
  if (!val.trim()) { toast('Please enter a response'); return; }
  hgRespond(key, 'submit', val, prefix);
}

async function hgRespond(key, action, response, prefix) {
  var cardId = (prefix || 'hg-dash-card-') + key;
  var card = document.getElementById(cardId);
  if (card) {
    card.querySelectorAll('button').forEach(function(b) { b.disabled = true; });
    card.style.opacity = '0.5';
  }
  try {
    await fetchJSON('/api/human-gates/' + encodeURIComponent(key) + '/respond', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: action, response: response, respondedBy: 'takuma' }),
    });
    if (card) card.remove();
    refreshHumanGates();
    removeInlineHgCard(key);
    toast('Gate responded: ' + action);
  } catch(e) {
    toast('Error: ' + (e.message || e));
    if (card) {
      card.querySelectorAll('button').forEach(function(b) { b.disabled = false; });
      card.style.opacity = '';
    }
  }
}

// --- Inline gate cards in task detail modal ---

async function renderInlineTaskGates(runId) {
  var container = document.getElementById('td-hg-inline');
  if (!container) return;
  try {
    var [waitingGates, rejectedGates] = await Promise.all([
      fetchJSON('/api/human-gates?status=waiting'),
      fetchJSON('/api/human-gates?status=rejected')
    ]);
    var allGates = (Array.isArray(waitingGates) ? waitingGates : []).concat(Array.isArray(rejectedGates) ? rejectedGates : []);
    var runGates = allGates.filter(function(g) { return g.runId === runId; });
    if (runGates.length === 0) {
      container.style.display = 'none';
      return;
    }
    container.style.display = '';
    container.innerHTML =
      '<div style="font-size:11px;color:#fbbf24;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;margin-bottom:6px">&#9203; Human Gate</div>' +
      runGates.map(buildInlineHgCardHtml).join('');
  } catch(e) {
    container.style.display = 'none';
  }
}

function buildInlineHgCardHtml(hg) {
  var key = hg.key || '';
  var subtype = hg.subtype || 'approval';
  var prompt = hg.prompt || '';
  var stepId = hg.stepId || '';
  var status = hg.status || 'waiting';
  var inputId = 'hg-inline-' + escAttr(key) + '-input';

  var actionsHtml = '';
  if (status === 'rejected') {
    actionsHtml =
      '<span class="badge badge-err" style="font-size:9px;margin-right:6px">rejected</span>' +
      '<button class="gate-retry-btn" data-key="' + escAttr(key) + '" style="font-size:11px;padding:2px 8px;border:1px solid #fbbf24;background:transparent;color:#fbbf24;border-radius:4px;cursor:pointer">Retry</button>';
  } else if (subtype === 'approval') {
    actionsHtml =
      '<textarea class="hg-comment" id="' + inputId + '" placeholder="Comment (optional)..." rows="1" style="font-size:11px;margin-bottom:4px;width:100%;box-sizing:border-box"></textarea>' +
      '<button class="btn btn-primary" style="font-size:11px;padding:2px 8px" onclick="hgInlineApprove(\'' + escAttr(key) + '\')">Approve</button>' +
      ' <button class="btn" style="font-size:11px;padding:2px 8px;border-color:var(--red);color:var(--red)" onclick="hgInlineReject(\'' + escAttr(key) + '\')">Reject</button>';
  } else if (subtype === 'action') {
    actionsHtml =
      '<textarea class="hg-comment" id="' + inputId + '" placeholder="Note (optional)..." rows="1" style="font-size:11px;margin-bottom:4px;width:100%;box-sizing:border-box"></textarea>' +
      '<button class="btn btn-primary" style="font-size:11px;padding:2px 8px" onclick="hgInlineComplete(\'' + escAttr(key) + '\')">Mark Done</button>';
  } else {
    actionsHtml =
      '<input type="text" id="' + inputId + '" placeholder="Your response..." style="font-size:11px;margin-bottom:4px;width:100%;box-sizing:border-box">' +
      '<button class="btn btn-primary" style="font-size:11px;padding:2px 8px" onclick="hgInlineSubmit(\'' + escAttr(key) + '\')">Submit</button>';
  }

  var badgeCls = status === 'rejected' ? 'badge-err' : 'badge-warn';
  return '<div class="hg-inline-card" id="hg-inline-card-' + escAttr(key) + '">' +
    '<div style="display:flex;align-items:center;gap:6px;margin-bottom:4px">' +
    '<span style="font-size:11px;font-weight:600">' + esc(stepId) + '</span>' +
    '<span class="badge ' + badgeCls + '" style="font-size:9px">' + esc(subtype) + '</span>' +
    '</div>' +
    (prompt ? '<div style="font-size:12px;margin-bottom:6px;color:var(--text)">' + esc(prompt) + '</div>' : '') +
    actionsHtml +
    '</div>';
}

function addInlineHgCard(data) {
  var container = document.getElementById('td-hg-inline');
  if (!container) return;
  var key = data.hgKey || data.key || '';
  if (!key || document.getElementById('hg-inline-card-' + key)) return;
  container.style.display = '';
  if (!container.querySelector('.hg-inline-card')) {
    container.innerHTML =
      '<div style="font-size:11px;color:#fbbf24;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;margin-bottom:6px">&#9203; Human Gate</div>';
  }
  var div = document.createElement('div');
  div.innerHTML = buildInlineHgCardHtml({
    key: key, hgKey: key,
    stepId: data.stepId, subtype: data.subtype, prompt: data.prompt,
  });
  container.appendChild(div.firstChild);
}

function removeInlineHgCard(key) {
  var card = document.getElementById('hg-inline-card-' + key);
  if (card) card.remove();
  var container = document.getElementById('td-hg-inline');
  if (container && !container.querySelector('.hg-inline-card')) {
    container.style.display = 'none';
  }
}

function hgInlineGetVal(key) {
  var el = document.getElementById('hg-inline-' + key + '-input');
  return el ? el.value : '';
}

function hgInlineApprove(key)  { hgInlineRespond(key, 'approve',  hgInlineGetVal(key)); }
function hgInlineReject(key)   { hgInlineRespond(key, 'reject',   hgInlineGetVal(key)); }
function hgInlineComplete(key) { hgInlineRespond(key, 'complete', hgInlineGetVal(key)); }
function hgInlineSubmit(key) {
  var val = hgInlineGetVal(key);
  if (!val.trim()) { toast('Please enter a response'); return; }
  hgInlineRespond(key, 'submit', val);
}

async function hgInlineRespond(key, action, response) {
  var card = document.getElementById('hg-inline-card-' + key);
  if (card) {
    card.querySelectorAll('button').forEach(function(b) { b.disabled = true; });
    card.style.opacity = '0.5';
  }
  try {
    await fetchJSON('/api/human-gates/' + encodeURIComponent(key) + '/respond', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: action, response: response, respondedBy: 'takuma' }),
    });
    removeInlineHgCard(key);
    refreshHumanGates();
    toast('Gate responded: ' + action);
  } catch(e) {
    toast('Error: ' + (e.message || e));
    if (card) {
      card.querySelectorAll('button').forEach(function(b) { b.disabled = false; });
      card.style.opacity = '';
    }
  }
}
// ============================================================
// End Human Gates
// ============================================================

// --- Task URL hash routing ---
// Opens task detail when URL contains #task/{id}, supports browser back/forward.
window.addEventListener('popstate', function(e) {
  if (e.state && e.state.taskId) {
    _showTaskDetail(e.state.taskId); // pure display — no pushState
  } else {
    document.getElementById('task-detail-modal').classList.remove('open');
  }
});

(function initTaskHash() {
  var hash = location.hash;
  if (hash && hash.startsWith('#task/')) {
    var taskId = hash.slice(6);
    if (taskId) _showTaskDetail(taskId); // URL already set, just show
  }
})();

