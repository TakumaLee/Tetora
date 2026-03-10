// --- Workflow Visual Editor ---
// Phase 1: Read-only DAG from workflow JSON definition
// Phase 2: Edit nodes (add/delete/properties), connect, save
// Phase 3: Drag position, auto-layout, zoom/pan (space+drag)

var wfEd = {
  workflow: null,       // { name, description, steps, variables }
  positions: {},        // { stepId: {x, y} }
  selected: null,       // selected step ID
  dirty: false,
  drag: null,           // { nodeId, startMX, startMY, origX, origY }
  connStart: null,      // { nodeId } port drag start
  connMX: 0, connMY: 0, // current mouse position for temp connector
  zoom: 1,
  panX: 0, panY: 0,
  panDrag: null,        // { startMX, startMY, origPanX, origPanY }
  spaceDown: false,
  NODE_W: 180, NODE_H: 64,
  skills: [],           // cached from /api/skills
  tools: [],            // cached from /api/tools
};

// ---- Workflow Definitions List ----

async function loadWorkflowDefs() {
  var el = document.getElementById('wf-defs-list');
  if (!el) return;
  el.innerHTML = '<span style="color:var(--muted);font-size:13px">Loading...</span>';
  try {
    var workflows = await fetchJSON('/workflows').catch(() => []);
    if (!workflows || workflows.length === 0) {
      el.innerHTML = '<span style="color:var(--muted);font-size:13px">No workflows found. Create one below.</span>';
      return;
    }
    var html = '';
    workflows.forEach(function(wf) {
      var steps = (wf.steps || []).length;
      var safeName = escAttr(wf.name);
      html += '<div class="wfed-def-row" onclick="openWorkflowEditor(\'' + safeName + '\')">' +
        '<div class="wfed-def-name">' + esc(wf.name) + '</div>' +
        '<div class="wfed-def-meta">' + steps + ' step' + (steps !== 1 ? 's' : '') +
          (wf.description ? ' &middot; ' + esc(wf.description.substring(0, 60)) : '') + '</div>' +
        '<div class="wfed-def-actions" onclick="event.stopPropagation()">' +
          '<button class="btn" style="font-size:11px;padding:2px 8px" onclick="openWorkflowEditor(\'' + safeName + '\')">Edit</button>' +
          '<button class="btn" style="font-size:11px;padding:2px 8px" onclick="runWorkflowByName(\'' + safeName + '\')">Run</button>' +
          '<button class="btn btn-danger" style="font-size:11px;padding:2px 8px" onclick="deleteWorkflowDef(\'' + safeName + '\')">Del</button>' +
        '</div>' +
      '</div>';
    });
    el.innerHTML = html;
  } catch(e) {
    el.innerHTML = '<span style="color:#f87171;font-size:13px">Failed to load workflows</span>';
  }
}

async function deleteWorkflowDef(name) {
  if (!confirm('Delete workflow "' + name + '"?')) return;
  try {
    await fetchAPI('/workflows/' + encodeURIComponent(name), { method: 'DELETE' });
    toast('Workflow deleted');
    loadWorkflowDefs();
    if (wfEd.workflow && wfEd.workflow.name === name) closeWorkflowEditor();
  } catch(e) {
    toast('Delete failed: ' + e.message);
  }
}

async function runWorkflowByName(name) {
  showRunModal({ name: name, variables: {} });
}

function newWorkflowDef() {
  var name = prompt('Workflow name (no spaces, e.g. my-workflow):');
  if (!name || !name.trim()) return;
  name = name.trim().replace(/\s+/g, '-');
  openWorkflowEditorWithData({
    name: name,
    description: '',
    steps: [],
    variables: {},
  });
}

// ---- Open Editor ----

async function openWorkflowEditor(name) {
  try {
    var wf = await fetchJSON('/workflows/' + encodeURIComponent(name));
    openWorkflowEditorWithData(wf);
  } catch(e) {
    toast('Failed to open "' + name + '": ' + e.message);
    console.error('openWorkflowEditor error', name, e);
  }
}

function openWorkflowEditorWithData(wf) {
  if (!wf || typeof wf !== 'object') { toast('Invalid workflow data'); return; }
  wfEd.workflow = JSON.parse(JSON.stringify(wf)); // deep clone
  wfEd.workflow.steps = wfEd.workflow.steps || [];
  wfEd.selected = null;
  wfEd.dirty = false;
  wfEd.zoom = 1;
  wfEd.panX = 0;
  wfEd.panY = 0;
  wfEd.drag = null;
  wfEd.connStart = null;
  wfEd.panDrag = null;

  // Auto-layout positions
  wfEd.positions = autoLayoutPositions(wfEd.workflow.steps);

  var panel = document.getElementById('wf-editor-panel');
  if (panel) panel.style.display = '';

  document.getElementById('wf-editor-title').textContent = wf.name;
  renderEditorCanvas();
  renderPropertyPanel(null);
  updateDirtyIndicator();

  // Register keyboard listeners for pan
  wedRegisterKeyListeners();

  // Prevent default middle-click scroll on canvas wrap (once)
  var canvasWrap = document.getElementById('wf-editor-canvas-wrap');
  if (canvasWrap && !canvasWrap._wedMidClick) {
    canvasWrap._wedMidClick = true;
    canvasWrap.addEventListener('mousedown', function(e) {
      if (e.button === 1) e.preventDefault();
    });
  }

  // Pre-load skills and tools for dropdowns
  wfLoadSkills();
  wfLoadTools();

  panel.scrollIntoView({ behavior: 'smooth', block: 'start' });
}

function closeWorkflowEditor() {
  if (wfEd.dirty && !confirm('Unsaved changes. Close anyway?')) return;
  var panel = document.getElementById('wf-editor-panel');
  if (panel) panel.style.display = 'none';
  wfEd.workflow = null;
  wfEd.selected = null;
  wfEd.dirty = false;
  wedUnregisterKeyListeners();
}

// ---- Keyboard Listeners (space = pan mode) ----

var _wedKeyDown = null;
var _wedKeyUp = null;

function wedRegisterKeyListeners() {
  wedUnregisterKeyListeners();
  _wedKeyDown = function(e) {
    if (e.code === 'Space' && !e.target.matches('input,textarea,select')) {
      e.preventDefault();
      wfEd.spaceDown = true;
      var wrap = document.getElementById('wf-editor-canvas-wrap');
      if (wrap) wrap.style.cursor = 'grab';
    }
  };
  _wedKeyUp = function(e) {
    if (e.code === 'Space') {
      wfEd.spaceDown = false;
      wfEd.panDrag = null;
      var wrap = document.getElementById('wf-editor-canvas-wrap');
      if (wrap) wrap.style.cursor = '';
    }
  };
  document.addEventListener('keydown', _wedKeyDown);
  document.addEventListener('keyup', _wedKeyUp);
}

function wedUnregisterKeyListeners() {
  if (_wedKeyDown) { document.removeEventListener('keydown', _wedKeyDown); _wedKeyDown = null; }
  if (_wedKeyUp) { document.removeEventListener('keyup', _wedKeyUp); _wedKeyUp = null; }
  wfEd.spaceDown = false;
}

// ---- Auto-Layout ----

function autoLayoutPositions(steps) {
  if (!steps || steps.length === 0) return {};
  var NW = wfEd.NODE_W, NH = wfEd.NODE_H;
  var GAP_X = 80, GAP_Y = 36, PAD = 40;

  // Build dep map
  var depMap = {};
  steps.forEach(function(s) { depMap[s.id] = s.dependsOn || []; });

  // Compute layers via longest path
  var layers = {}, visited = {};
  function getLayer(id) {
    if (id in layers) return layers[id];
    if (visited[id]) { layers[id] = 0; return 0; }
    visited[id] = true;
    var deps = depMap[id] || [];
    var max = -1;
    deps.forEach(function(d) { if (d in depMap) max = Math.max(max, getLayer(d)); });
    layers[id] = max + 1;
    return layers[id];
  }
  steps.forEach(function(s) { getLayer(s.id); });

  // Group by layer
  var byLayer = {};
  steps.forEach(function(s) {
    var l = layers[s.id] || 0;
    if (!byLayer[l]) byLayer[l] = [];
    byLayer[l].push(s.id);
  });

  var positions = {};
  Object.keys(byLayer).forEach(function(l) {
    var col = parseInt(l, 10);
    byLayer[l].forEach(function(id, row) {
      positions[id] = {
        x: PAD + col * (NW + GAP_X),
        y: PAD + row * (NH + GAP_Y)
      };
    });
  });

  return positions;
}

// ---- Compute Canvas Bounds ----

function computeCanvasBounds(steps) {
  var NW = wfEd.NODE_W, NH = wfEd.NODE_H;
  var maxX = 500, maxY = 260;
  steps.forEach(function(s) {
    var p = wfEd.positions[s.id];
    if (p) {
      maxX = Math.max(maxX, p.x + NW + 80);
      maxY = Math.max(maxY, p.y + NH + 80);
    }
  });
  return { w: maxX, h: maxY };
}

// ---- Apply Viewport Transform ----

function applyViewTransform() {
  var g = document.getElementById('wed-viewport');
  if (g) {
    g.setAttribute('transform', 'translate(' + wfEd.panX + ',' + wfEd.panY + ') scale(' + wfEd.zoom + ')');
  }
}

// ---- Render Canvas ----

function renderEditorCanvas() {
  var svg = document.getElementById('wf-editor-svg');
  if (!svg || !wfEd.workflow) return;

  var steps = wfEd.workflow.steps || [];
  var NW = wfEd.NODE_W, NH = wfEd.NODE_H;
  var bounds = computeCanvasBounds(steps);

  // SVG fills the wrap, content is inside #wed-viewport (pan+zoom transform)
  svg.setAttribute('width', '100%');
  svg.setAttribute('height', '100%');
  svg.style.minWidth = bounds.w + 'px';
  svg.style.minHeight = bounds.h + 'px';

  var html = '<defs>';
  html += '<marker id="wed-arrow" viewBox="0 0 10 7" refX="9" refY="3.5" markerWidth="7" markerHeight="5" orient="auto-start-reverse"><polygon points="0 0,10 3.5,0 7" fill="#6b7280"/></marker>';
  html += '<marker id="wed-arrow-sel" viewBox="0 0 10 7" refX="9" refY="3.5" markerWidth="7" markerHeight="5" orient="auto-start-reverse"><polygon points="0 0,10 3.5,0 7" fill="#a78bfa"/></marker>';
  html += '</defs>';
  html += '<rect id="wed-bg" x="0" y="0" width="100%" height="100%" fill="transparent"/>';
  html += '<g id="wed-viewport" transform="translate(' + wfEd.panX + ',' + wfEd.panY + ') scale(' + wfEd.zoom + ')">';

  // Draw edges (dependsOn)
  steps.forEach(function(s) {
    var to = wfEd.positions[s.id];
    if (!to) return;
    (s.dependsOn || []).forEach(function(depId) {
      var from = wfEd.positions[depId];
      if (!from) return;
      var x1 = from.x + NW, y1 = from.y + NH / 2;
      var x2 = to.x, y2 = to.y + NH / 2;
      var cx = (x1 + x2) / 2;
      var isHandoff = s.handoffFrom === depId;
      var cls = isHandoff ? 'wed-edge handoff' : 'wed-edge';
      var marker = isHandoff ? 'url(#wed-arrow-sel)' : 'url(#wed-arrow)';
      html += '<path class="' + cls + '" d="M' + x1 + ' ' + y1 +
        ' C' + cx + ' ' + y1 + ' ' + cx + ' ' + y2 + ' ' + x2 + ' ' + y2 +
        '" marker-end="' + marker + '"/>';
    });
  });

  // Temp connector line (while dragging from port)
  if (wfEd.connStart) {
    var fp = wfEd.positions[wfEd.connStart];
    if (fp) {
      html += '<line id="wed-conn-temp" class="wed-conn-temp" x1="' + (fp.x + NW) + '" y1="' + (fp.y + NH/2) + '" x2="' + wfEd.connMX + '" y2="' + wfEd.connMY + '"/>';
    }
  }

  // Draw nodes
  steps.forEach(function(s) {
    var p = wfEd.positions[s.id];
    if (!p) return;
    var isSel = wfEd.selected === s.id;
    var typeClass = 'wed-node-' + (s.type || 'dispatch');
    var selClass = isSel ? ' selected' : '';

    html += '<g class="wed-node ' + typeClass + selClass + '" id="wed-n-' + escAttr(s.id) + '"' +
      ' data-id="' + escAttr(s.id) + '">';

    if (s.type === 'condition') {
      // Diamond shape
      var cx = p.x + NW/2, cy = p.y + NH/2;
      var hw = NW/2 - 4, hh = NH/2 - 4;
      html += '<polygon class="wed-node-shape" points="' +
        cx + ',' + (cy - hh) + ' ' +
        (cx + hw) + ',' + cy + ' ' +
        cx + ',' + (cy + hh) + ' ' +
        (cx - hw) + ',' + cy + '"/>';
    } else if (s.type === 'parallel') {
      // Double border rect
      html += '<rect class="wed-node-shape wed-parallel-outer" x="' + (p.x + 3) + '" y="' + (p.y + 3) + '" width="' + (NW - 6) + '" height="' + (NH - 6) + '" rx="8"/>';
      html += '<rect class="wed-node-shape wed-parallel-inner" x="' + (p.x + 7) + '" y="' + (p.y + 7) + '" width="' + (NW - 14) + '" height="' + (NH - 14) + '" rx="6"/>';
    } else if (s.type === 'external') {
      // Dashed border rect for external steps
      html += '<rect class="wed-node-shape wed-external" x="' + p.x + '" y="' + p.y + '" width="' + NW + '" height="' + NH + '" rx="8" stroke-dasharray="6 3"/>';
    } else {
      html += '<rect class="wed-node-shape" x="' + p.x + '" y="' + p.y + '" width="' + NW + '" height="' + NH + '" rx="8"/>';
    }

    // Type badge
    var typeLabel = s.type && s.type !== 'dispatch' ? s.type : '';
    var nodeLabel = s.id.length > 18 ? s.id.substring(0, 16) + '..' : s.id;
    var roleLabel = s.agent || s.skill || '';
    if (roleLabel.length > 16) roleLabel = roleLabel.substring(0, 14) + '..';

    var textY = p.y + (roleLabel || typeLabel ? 20 : 30);
    html += '<text class="wed-node-label" x="' + (p.x + NW/2) + '" y="' + textY + '" text-anchor="middle">' + esc(nodeLabel) + '</text>';
    if (roleLabel) {
      html += '<text class="wed-node-sublabel" x="' + (p.x + NW/2) + '" y="' + (p.y + 36) + '" text-anchor="middle">' + esc(roleLabel) + '</text>';
    }
    if (typeLabel) {
      html += '<text class="wed-node-type" x="' + (p.x + 10) + '" y="' + (p.y + 13) + '">' + esc(typeLabel) + '</text>';
    }

    // Output port (right side)
    html += '<circle class="wed-port wed-port-out" cx="' + (p.x + NW) + '" cy="' + (p.y + NH/2) + '" r="5" data-id="' + escAttr(s.id) + '"/>';
    // Input port (left side, visual only)
    html += '<circle class="wed-port wed-port-in" cx="' + p.x + '" cy="' + (p.y + NH/2) + '" r="5" data-id="' + escAttr(s.id) + '"/>';

    html += '</g>';
  });

  html += '</g>'; // end #wed-viewport

  svg.innerHTML = html;

  // Attach drag events to nodes
  steps.forEach(function(s) {
    var g = document.getElementById('wed-n-' + s.id);
    if (g) {
      g.addEventListener('mousedown', wedNodeMousedown);
    }
  });

  // Attach port drag events
  svg.querySelectorAll('.wed-port-out').forEach(function(port) {
    port.addEventListener('mousedown', wedPortMousedown);
  });

  // Canvas-level mouse events for pan/deselect
  svg.addEventListener('mousedown', wedSVGMousedown);
  svg.addEventListener('mousemove', wedSVGMousemove);
  svg.addEventListener('mouseup', wedSVGMouseup);
  svg.addEventListener('wheel', wedSVGWheel, { passive: false });

  // Keep JSON panel in sync if visible
  syncJsonPanelIfVisible();
}

// ---- Drag: Nodes ----

function wedNodeMousedown(e) {
  if (e.target.classList.contains('wed-port')) return; // handled by port handler
  if (e.button === 1) return; // middle click → let SVG handle pan
  if (wfEd.spaceDown) return; // pan mode takes priority
  e.preventDefault();
  e.stopPropagation();
  var g = e.currentTarget;
  var id = g.getAttribute('data-id');

  // Select node
  wfEd.selected = id;
  renderPropertyPanel(id);

  // Start drag
  var svg = document.getElementById('wf-editor-svg');
  var pt = svgViewportPoint(svg, e.clientX, e.clientY);
  var pos = wfEd.positions[id] || { x: 0, y: 0 };
  wfEd.drag = { nodeId: id, startMX: pt.x, startMY: pt.y, origX: pos.x, origY: pos.y };

  // Highlight selected
  svg.querySelectorAll('.wed-node').forEach(function(n) { n.classList.remove('selected'); });
  g.classList.add('selected');
}

// ---- Drag: Ports (connections) ----

function wedPortMousedown(e) {
  e.preventDefault();
  e.stopPropagation();
  var id = e.target.getAttribute('data-id');
  wfEd.connStart = id;
  var svg = document.getElementById('wf-editor-svg');
  var pt = svgViewportPoint(svg, e.clientX, e.clientY);
  wfEd.connMX = pt.x;
  wfEd.connMY = pt.y;
}

// ---- SVG Mouse Events ----

function wedSVGMousedown(e) {
  // Pan mode: space+drag or middle button
  if (wfEd.spaceDown || e.button === 1) {
    e.preventDefault();
    var wrap = document.getElementById('wf-editor-canvas-wrap');
    if (wrap) wrap.style.cursor = 'grabbing';
    wfEd.panDrag = {
      startMX: e.clientX,
      startMY: e.clientY,
      origPanX: wfEd.panX,
      origPanY: wfEd.panY,
    };
    return;
  }

  if (e.target === e.currentTarget || e.target.tagName === 'svg' || e.target.id === 'wed-bg') {
    // Click on empty canvas → deselect
    if (!wfEd.drag && !wfEd.connStart) {
      wfEd.selected = null;
      renderPropertyPanel(null);
      var svg = document.getElementById('wf-editor-svg');
      svg.querySelectorAll('.wed-node').forEach(function(n) { n.classList.remove('selected'); });
    }
  }
}

function wedSVGMousemove(e) {
  var svg = document.getElementById('wf-editor-svg');
  if (!svg) return;

  // Pan drag
  if (wfEd.panDrag) {
    e.preventDefault();
    var dx = e.clientX - wfEd.panDrag.startMX;
    var dy = e.clientY - wfEd.panDrag.startMY;
    wfEd.panX = wfEd.panDrag.origPanX + dx;
    wfEd.panY = wfEd.panDrag.origPanY + dy;
    applyViewTransform();
    return;
  }

  if (wfEd.drag) {
    e.preventDefault();
    var pt = svgViewportPoint(svg, e.clientX, e.clientY);
    var dx = pt.x - wfEd.drag.startMX;
    var dy = pt.y - wfEd.drag.startMY;
    var nx = Math.max(0, wfEd.drag.origX + dx);
    var ny = Math.max(0, wfEd.drag.origY + dy);
    wfEd.positions[wfEd.drag.nodeId] = { x: nx, y: ny };
    renderEditorCanvas();
    // Re-select after re-render
    var g = document.getElementById('wed-n-' + wfEd.drag.nodeId);
    if (g) g.classList.add('selected');
    return;
  }

  if (wfEd.connStart) {
    e.preventDefault();
    var pt = svgViewportPoint(svg, e.clientX, e.clientY);
    wfEd.connMX = pt.x;
    wfEd.connMY = pt.y;

    // Update temp line
    var line = document.getElementById('wed-conn-temp');
    if (line) {
      line.setAttribute('x2', pt.x);
      line.setAttribute('y2', pt.y);
    } else {
      renderEditorCanvas();
    }
    return;
  }
}

function wedSVGMouseup(e) {
  // End pan drag
  if (wfEd.panDrag) {
    wfEd.panDrag = null;
    var wrap = document.getElementById('wf-editor-canvas-wrap');
    if (wrap) wrap.style.cursor = wfEd.spaceDown ? 'grab' : '';
    return;
  }

  if (wfEd.drag) {
    wfEd.drag = null;
    wfEd.dirty = true;
    updateDirtyIndicator();
    return;
  }

  if (wfEd.connStart) {
    var svg = document.getElementById('wf-editor-svg');
    // Find if mouse is over a node (in viewport space)
    var pt = svgViewportPoint(svg, e.clientX, e.clientY);
    var targetId = findNodeAtPoint(pt.x, pt.y);
    if (targetId && targetId !== wfEd.connStart) {
      // Add edge: targetId.dependsOn += connStart
      var step = (wfEd.workflow.steps || []).find(function(s) { return s.id === targetId; });
      if (step) {
        if (!step.dependsOn) step.dependsOn = [];
        if (!step.dependsOn.includes(wfEd.connStart)) {
          step.dependsOn.push(wfEd.connStart);
          wfEd.dirty = true;
          updateDirtyIndicator();
          toast('Connected: ' + wfEd.connStart + ' → ' + targetId);
        }
      }
    }
    wfEd.connStart = null;
    renderEditorCanvas();
    return;
  }
}

function wedSVGWheel(e) {
  e.preventDefault();
  var delta = e.deltaY > 0 ? 0.9 : 1.1;
  var newZoom = Math.max(0.2, Math.min(4, wfEd.zoom * delta));

  // Zoom toward mouse cursor: adjust pan so the point under cursor stays fixed
  var svg = document.getElementById('wf-editor-svg');
  if (svg) {
    var rect = svg.getBoundingClientRect();
    var mx = e.clientX - rect.left;
    var my = e.clientY - rect.top;
    // Point in viewport space before zoom
    var vx = (mx - wfEd.panX) / wfEd.zoom;
    var vy = (my - wfEd.panY) / wfEd.zoom;
    // After zoom, adjust pan so vx/vy stays under mouse
    wfEd.panX = mx - vx * newZoom;
    wfEd.panY = my - vy * newZoom;
  }

  wfEd.zoom = newZoom;
  applyViewTransform();
}

// ---- Coordinate helpers ----

// Returns point in viewport (content) space, accounting for pan+zoom
function svgViewportPoint(svg, clientX, clientY) {
  var rect = svg.getBoundingClientRect();
  var mx = clientX - rect.left;
  var my = clientY - rect.top;
  return {
    x: (mx - wfEd.panX) / wfEd.zoom,
    y: (my - wfEd.panY) / wfEd.zoom,
  };
}

function findNodeAtPoint(x, y) {
  var NW = wfEd.NODE_W, NH = wfEd.NODE_H;
  var steps = wfEd.workflow.steps || [];
  for (var i = 0; i < steps.length; i++) {
    var p = wfEd.positions[steps[i].id];
    if (!p) continue;
    if (x >= p.x && x <= p.x + NW && y >= p.y && y <= p.y + NH) {
      return steps[i].id;
    }
  }
  return null;
}

// ---- Property Panel ----

function renderPropertyPanel(stepId) {
  var panel = document.getElementById('wf-prop-panel');
  if (!panel) return;

  if (!stepId || !wfEd.workflow) {
    panel.innerHTML = '<div style="color:var(--muted);font-size:13px;padding:16px">Click a node to edit its properties.</div>';
    return;
  }

  var step = (wfEd.workflow.steps || []).find(function(s) { return s.id === stepId; });
  if (!step) {
    panel.innerHTML = '<div style="color:var(--muted);font-size:13px;padding:16px">Step not found.</div>';
    return;
  }

  var fields = [
    { key: 'id', label: 'ID', type: 'text', required: true },
    { key: 'type', label: 'Type', type: 'select', options: ['dispatch','skill','condition','parallel','tool_call','delay','notify','external'] },
    { key: 'agent', label: 'Agent', type: 'text', placeholder: 'e.g. kokuyou' },
    { key: 'prompt', label: 'Prompt', type: 'textarea' },
    { key: 'skill', label: 'Skill', type: 'skill-select' },
    { key: 'model', label: 'Model', type: 'text', placeholder: 'claude-sonnet-4-6' },
    { key: 'timeout', label: 'Timeout', type: 'text', placeholder: '30m' },
    { key: 'budget', label: 'Budget ($)', type: 'number' },
    { key: 'dependsOn', label: 'Depends On', type: 'depends' },
    { key: 'if', label: 'Condition (if)', type: 'text' },
    { key: 'then', label: 'Then (step ID)', type: 'text' },
    { key: 'else', label: 'Else (step ID)', type: 'text' },
    { key: 'handoffFrom', label: 'Handoff From', type: 'text' },
    { key: 'toolName', label: 'Tool Name', type: 'tool-select' },
    { key: 'delay', label: 'Delay', type: 'delay' },
    { key: 'notifyMsg', label: 'Notify Message', type: 'text' },
    { key: 'externalUrl', label: 'External URL', type: 'text', placeholder: 'https://api.example.com/endpoint' },
    { key: 'externalContentType', label: 'Content Type', type: 'select', options: ['application/json', 'application/xml', 'text/xml', 'application/x-www-form-urlencoded', 'text/plain'] },
    { key: 'externalBody', label: 'Body (JSON KV)', type: 'json-map', placeholder: '{"key": "value"}' },
    { key: 'externalRawBody', label: 'Raw Body', type: 'textarea', placeholder: 'XML or raw body (mutually exclusive with Body KV)' },
    { key: 'callbackKey', label: 'Callback Key', type: 'text', placeholder: 'my-service-{{runId}}' },
    { key: 'callbackTimeout', label: 'Callback Timeout', type: 'text', placeholder: '5m (max 30d)' },
    { key: 'callbackMode', label: 'Callback Mode', type: 'select', options: ['', 'single', 'streaming'] },
    { key: 'callbackAuth', label: 'Callback Auth', type: 'select', options: ['', 'bearer', 'open', 'signature'] },
    { key: 'callbackAccumulate', label: 'Accumulate Results', type: 'checkbox' },
    { key: 'onTimeout', label: 'On Timeout', type: 'select', options: ['', 'stop', 'skip'] },
    { key: 'retryMax', label: 'Max Retries', type: 'number' },
    { key: 'onError', label: 'On Error', type: 'select', options: ['', 'stop', 'skip', 'retry'] },
  ];

  var visibleFields = fields;
  var stype = step.type || 'dispatch';
  if (stype === 'dispatch') {
    visibleFields = fields.filter(function(f) {
      return ['id','type','agent','prompt','model','timeout','budget','dependsOn','handoffFrom','retryMax','onError'].includes(f.key);
    });
  } else if (stype === 'skill') {
    visibleFields = fields.filter(function(f) {
      return ['id','type','skill','dependsOn','timeout','retryMax','onError'].includes(f.key);
    });
  } else if (stype === 'condition') {
    visibleFields = fields.filter(function(f) {
      return ['id','type','if','then','else','dependsOn'].includes(f.key);
    });
  } else if (stype === 'tool_call') {
    visibleFields = fields.filter(function(f) {
      return ['id','type','toolName','dependsOn','retryMax','onError'].includes(f.key);
    });
  } else if (stype === 'delay') {
    visibleFields = fields.filter(function(f) {
      return ['id','type','delay','dependsOn'].includes(f.key);
    });
  } else if (stype === 'notify') {
    visibleFields = fields.filter(function(f) {
      return ['id','type','notifyMsg','dependsOn'].includes(f.key);
    });
  } else if (stype === 'external') {
    visibleFields = fields.filter(function(f) {
      return ['id','type','externalUrl','externalContentType','externalBody','externalRawBody',
              'callbackKey','callbackTimeout','callbackMode','callbackAuth','callbackAccumulate',
              'onTimeout','dependsOn','retryMax','onError'].includes(f.key);
    });
  }

  var html = '<div style="padding:12px">';
  html += '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px">';
  html += '<strong style="color:var(--accent);font-size:14px">' + esc(step.id) + '</strong>';
  html += '<button class="btn btn-danger" style="font-size:11px;padding:2px 8px" onclick="deleteStepById(' + JSON.stringify(stepId) + ')">Delete</button>';
  html += '</div>';

  visibleFields.forEach(function(f) {
    html += '<div class="wfed-prop-row">';
    html += '<label class="wfed-prop-label">' + esc(f.label) + '</label>';
    var val = step[f.key];

    if (f.type === 'select') {
      html += '<select class="wfed-prop-input" onchange="updateStepProp(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this.value)">';
      f.options.forEach(function(opt) {
        var sel = (val === opt || (!val && opt === '')) ? ' selected' : '';
        html += '<option value="' + escAttr(opt) + '"' + sel + '>' + esc(opt || '(default)') + '</option>';
      });
      html += '</select>';
    } else if (f.type === 'textarea') {
      html += '<textarea class="wfed-prop-input wfed-prop-textarea" rows="3" onblur="updateStepProp(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this.value)"' +
        (f.placeholder ? ' placeholder="' + escAttr(f.placeholder) + '"' : '') + '>' +
        esc(val || '') + '</textarea>';
    } else if (f.type === 'checkbox') {
      var checked = !!val;
      html += '<input type="checkbox" class="wfed-prop-checkbox"' + (checked ? ' checked' : '') +
        ' onchange="updateStepProp(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this.checked)">';
    } else if (f.type === 'json-map') {
      var mapStr = '';
      if (val && typeof val === 'object') {
        try { mapStr = JSON.stringify(val, null, 2); } catch(e) { mapStr = '{}'; }
      }
      html += '<textarea class="wfed-prop-input wfed-prop-textarea" rows="3" onblur="updateStepPropJSON(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this.value)"' +
        (f.placeholder ? ' placeholder="' + escAttr(f.placeholder) + '"' : '') + '>' +
        esc(mapStr) + '</textarea>';
    } else if (f.type === 'depends') {
      var otherSteps = (wfEd.workflow.steps || []).filter(function(s) { return s.id !== stepId; });
      var deps = Array.isArray(val) ? val : [];
      if (otherSteps.length === 0) {
        html += '<span style="color:var(--muted);font-size:11px">No other steps yet.</span>';
      } else {
        html += '<div class="wfed-depends-list">';
        otherSteps.forEach(function(s) {
          var isChk = deps.includes(s.id);
          html += '<label class="wfed-dep-item">' +
            '<input type="checkbox"' + (isChk ? ' checked' : '') +
            ' onchange="toggleDependsOn(' + JSON.stringify(stepId) + ',' + JSON.stringify(s.id) + ',this.checked)"> ' +
            esc(s.id) + ' <span style="color:var(--muted);font-size:10px">(' +
            esc(s.type || 'dispatch') + (s.agent ? ', ' + esc(s.agent) : '') + ')</span>' +
            '</label>';
        });
        html += '</div>';
      }
    } else if (f.type === 'skill-select') {
      html += '<select class="wfed-prop-input" onchange="updateStepProp(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this.value)">';
      html += '<option value="">-- select skill --</option>';
      wfEd.skills.forEach(function(sk) {
        var sel = val === sk.name ? ' selected' : '';
        var label = sk.name + (sk.description ? ' \u2014 ' + sk.description.substring(0, 40) : '');
        html += '<option value="' + escAttr(sk.name) + '"' + sel + '>' + esc(label) + '</option>';
      });
      html += '</select>';
    } else if (f.type === 'tool-select') {
      html += '<select class="wfed-prop-input" onchange="updateStepProp(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this.value)">';
      html += '<option value="">-- select tool --</option>';
      wfEd.tools.forEach(function(t) {
        var sel = val === t.name ? ' selected' : '';
        var label = t.name + (t.description ? ' \u2014 ' + t.description.substring(0, 40) : '');
        html += '<option value="' + escAttr(t.name) + '"' + sel + '>' + esc(label) + '</option>';
      });
      html += '</select>';
    } else if (f.type === 'delay') {
      html += '<input class="wfed-prop-input" type="text" value="' + escAttr(String(val || '')) + '"' +
        ' placeholder="30s / 5m / 1h"' +
        ' oninput="wfDelayValidate(this)"' +
        ' onblur="wfDelayBlur(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this)"/>';
      html += '<div class="wfed-delay-hint">Format: 30s / 5m / 1h</div>';
    } else if (f.type === 'tags') {
      var tagsVal = Array.isArray(val) ? val.join(', ') : (val || '');
      html += '<input class="wfed-prop-input" type="text" value="' + escAttr(tagsVal) + '"' +
        ' placeholder="comma-separated IDs"' +
        ' onblur="updateStepPropTags(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this.value)"/>';
    } else if (f.type === 'number') {
      html += '<input class="wfed-prop-input" type="number" value="' + escAttr(String(val || '')) + '"' +
        (f.placeholder ? ' placeholder="' + escAttr(f.placeholder) + '"' : '') +
        ' onblur="updateStepPropNum(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this.value)"/>';
    } else {
      html += '<input class="wfed-prop-input" type="text" value="' + escAttr(String(val || '')) + '"' +
        (f.placeholder ? ' placeholder="' + escAttr(f.placeholder) + '"' : '') +
        ' onblur="updateStepProp(' + JSON.stringify(stepId) + ',' + JSON.stringify(f.key) + ',this.value)"/>';
    }
    html += '</div>';
  });

  html += '</div>';
  panel.innerHTML = html;
}

function updateStepProp(stepId, key, value) {
  var step = (wfEd.workflow.steps || []).find(function(s) { return s.id === stepId; });
  if (!step) return;
  if (value === '') {
    delete step[key];
  } else {
    step[key] = value;
  }
  wfEd.dirty = true;
  updateDirtyIndicator();

  // If ID changed, update positions map and re-render
  if (key === 'id' && value && value !== stepId) {
    wfEd.positions[value] = wfEd.positions[stepId];
    delete wfEd.positions[stepId];
    if (wfEd.selected === stepId) wfEd.selected = value;
    // Update dependsOn references
    (wfEd.workflow.steps || []).forEach(function(s) {
      if (s.dependsOn) {
        s.dependsOn = s.dependsOn.map(function(d) { return d === stepId ? value : d; });
      }
    });
  }

  renderEditorCanvas();
  renderPropertyPanel(wfEd.selected);
}

function updateStepPropJSON(stepId, key, value) {
  if (!wfEd.workflow) return;
  var step = (wfEd.workflow.steps || []).find(function(s) { return s.id === stepId; });
  if (!step) return;
  if (!value || value.trim() === '' || value.trim() === '{}') {
    delete step[key];
  } else {
    try {
      step[key] = JSON.parse(value);
    } catch(e) {
      toast('Invalid JSON: ' + e.message);
      return;
    }
  }
  wfEd.dirty = true;
  updateDirtyIndicator();
}

function updateStepPropTags(stepId, key, value) {
  var step = (wfEd.workflow.steps || []).find(function(s) { return s.id === stepId; });
  if (!step) return;
  var tags = value.split(',').map(function(t) { return t.trim(); }).filter(Boolean);
  if (tags.length === 0) {
    delete step[key];
  } else {
    step[key] = tags;
  }
  wfEd.dirty = true;
  updateDirtyIndicator();
  renderEditorCanvas();
}

function updateStepPropNum(stepId, key, value) {
  var step = (wfEd.workflow.steps || []).find(function(s) { return s.id === stepId; });
  if (!step) return;
  var num = parseFloat(value);
  if (isNaN(num) || value === '') {
    delete step[key];
  } else {
    step[key] = num;
  }
  wfEd.dirty = true;
  updateDirtyIndicator();
}

// ---- Add / Delete Steps ----

function addStepToWorkflow(type) {
  if (!wfEd.workflow) return;
  if (!wfEd.workflow.steps) wfEd.workflow.steps = [];

  var baseId = type + '-' + (wfEd.workflow.steps.length + 1);
  var newStep = { id: baseId, type: type };

  // Default fields by type
  if (type === 'dispatch') {
    newStep.agent = '';
    newStep.prompt = '';
    delete newStep.type; // dispatch is default, type field is optional
  } else if (type === 'skill') {
    newStep.skill = '';
  } else if (type === 'condition') {
    newStep.if = '';
    newStep.then = '';
    newStep.else = '';
  } else if (type === 'parallel') {
    newStep.parallel = [];
  } else if (type === 'external') {
    newStep.externalUrl = '';
    newStep.callbackKey = '';
    newStep.callbackTimeout = '5m';
  }

  // Position near last node or at center of current view
  var steps = wfEd.workflow.steps;
  var NW = wfEd.NODE_W, NH = wfEd.NODE_H;
  var lastPos = { x: 40, y: 40 };
  if (steps.length > 0) {
    var lastId = steps[steps.length - 1].id;
    var lp = wfEd.positions[lastId];
    if (lp) lastPos = { x: lp.x + NW + 80, y: lp.y };
  }
  wfEd.positions[newStep.id] = lastPos;

  wfEd.workflow.steps.push(newStep);
  wfEd.dirty = true;
  wfEd.selected = newStep.id;
  updateDirtyIndicator();
  renderEditorCanvas();
  renderPropertyPanel(newStep.id);

  // Highlight new node
  setTimeout(function() {
    var g = document.getElementById('wed-n-' + newStep.id);
    if (g) g.classList.add('selected');
  }, 50);
}

function deleteStepById(stepId) {
  if (!wfEd.workflow) return;
  if (!confirm('Delete step "' + stepId + '"?')) return;

  wfEd.workflow.steps = (wfEd.workflow.steps || []).filter(function(s) { return s.id !== stepId; });

  // Remove from dependsOn in all other steps
  (wfEd.workflow.steps || []).forEach(function(s) {
    if (s.dependsOn) {
      s.dependsOn = s.dependsOn.filter(function(d) { return d !== stepId; });
      if (s.dependsOn.length === 0) delete s.dependsOn;
    }
  });

  delete wfEd.positions[stepId];
  if (wfEd.selected === stepId) {
    wfEd.selected = null;
    renderPropertyPanel(null);
  }
  wfEd.dirty = true;
  updateDirtyIndicator();
  renderEditorCanvas();
}

function disconnectEdge(fromId, toId) {
  var step = (wfEd.workflow.steps || []).find(function(s) { return s.id === toId; });
  if (!step || !step.dependsOn) return;
  step.dependsOn = step.dependsOn.filter(function(d) { return d !== fromId; });
  if (step.dependsOn.length === 0) delete step.dependsOn;
  wfEd.dirty = true;
  updateDirtyIndicator();
  renderEditorCanvas();
}

// ---- Auto-Layout Button ----

function wedAutoLayout() {
  if (!wfEd.workflow) return;
  wfEd.positions = autoLayoutPositions(wfEd.workflow.steps || []);
  wfEd.panX = 0;
  wfEd.panY = 0;
  wfEd.zoom = 1;
  wfEd.dirty = true;
  updateDirtyIndicator();
  renderEditorCanvas();
}

// ---- Save ----

async function saveWorkflowEditorData() {
  if (!wfEd.workflow) return;

  // Clean up empty fields
  var wf = JSON.parse(JSON.stringify(wfEd.workflow));
  wf.steps = (wf.steps || []).map(function(s) {
    var clean = {};
    Object.keys(s).forEach(function(k) {
      var v = s[k];
      if (v === null || v === undefined || v === '') return;
      if (Array.isArray(v) && v.length === 0) return;
      clean[k] = v;
    });
    return clean;
  });

  try {
    var resp = await fetch(API + '/workflows', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(wf),
    });
    if (!resp.ok) {
      var err = await resp.json().catch(function() { return {}; });
      var msg = err.errors ? err.errors.join(', ') : (err.error || 'Save failed');
      toast('Save failed: ' + msg);
      return;
    }
    wfEd.dirty = false;
    updateDirtyIndicator();
    toast('Workflow saved: ' + wf.name);
    loadWorkflowDefs();
  } catch(e) {
    toast('Save error: ' + e.message);
  }
}

// ---- Workflow Metadata ----

function editWorkflowMeta() {
  if (!wfEd.workflow) return;
  var name = prompt('Workflow name:', wfEd.workflow.name);
  if (!name || !name.trim()) return;
  var desc = prompt('Description (optional):', wfEd.workflow.description || '');
  wfEd.workflow.name = name.trim();
  wfEd.workflow.description = desc || '';
  document.getElementById('wf-editor-title').textContent = name.trim();
  wfEd.dirty = true;
  updateDirtyIndicator();
}

// ---- Dirty Indicator ----

function updateDirtyIndicator() {
  var el = document.getElementById('wf-dirty-badge');
  if (el) {
    el.textContent = wfEd.dirty ? '● Unsaved' : '✓ Saved';
    el.style.color = wfEd.dirty ? '#fbbf24' : '#34d399';
  }
  var saveBtn = document.getElementById('wf-save-btn');
  if (saveBtn) saveBtn.style.opacity = wfEd.dirty ? '1' : '0.5';
}

// ---- Zoom Buttons ----

function wedZoomIn()  { wfEd.zoom = Math.min(4, wfEd.zoom * 1.2); applyViewTransform(); }
function wedZoomOut() { wfEd.zoom = Math.max(0.2, wfEd.zoom / 1.2); applyViewTransform(); }
function wedZoomReset() { wfEd.zoom = 1; wfEd.panX = 0; wfEd.panY = 0; applyViewTransform(); }

// ---- JSON Editor (toggle) ----

function toggleWfJsonEditor() {
  var panel = document.getElementById('wf-json-editor-panel');
  if (!panel) return;
  if (panel.style.display === 'none' || !panel.style.display) {
    if (!wfEd.workflow) return;
    var ta = document.getElementById('wf-json-textarea');
    if (ta) ta.value = JSON.stringify(wfEd.workflow, null, 2);
    panel.style.display = '';
  } else {
    panel.style.display = 'none';
  }
}

function applyWfJsonEdit() {
  var ta = document.getElementById('wf-json-textarea');
  if (!ta) return;
  try {
    var wf = JSON.parse(ta.value);
    wfEd.workflow = wf;
    wfEd.positions = autoLayoutPositions(wf.steps || []);
    wfEd.selected = null;
    wfEd.panX = 0;
    wfEd.panY = 0;
    wfEd.zoom = 1;
    wfEd.dirty = true;
    updateDirtyIndicator();
    renderEditorCanvas();
    renderPropertyPanel(null);
    document.getElementById('wf-json-editor-panel').style.display = 'none';
    toast('JSON applied');
  } catch(e) {
    toast('Invalid JSON: ' + e.message);
  }
}

// ---- Dropdown Helper ----

function toggleAddStepMenu() {
  var menu = document.getElementById('wf-add-step-menu');
  if (!menu) return;
  menu.style.display = menu.style.display === 'none' || !menu.style.display ? '' : 'none';
  if (menu.style.display !== 'none') {
    // Close when clicking outside
    setTimeout(function() {
      document.addEventListener('click', function handler(e) {
        var wrap = document.getElementById('wf-add-step-wrap');
        if (wrap && !wrap.contains(e.target)) {
          menu.style.display = 'none';
          document.removeEventListener('click', handler);
        }
      });
    }, 10);
  }
}

// ---- DependsOn checkbox toggle ----

function toggleDependsOn(stepId, depId, isChecked) {
  var step = (wfEd.workflow.steps || []).find(function(s) { return s.id === stepId; });
  if (!step) return;
  if (!step.dependsOn) step.dependsOn = [];
  if (isChecked) {
    if (!step.dependsOn.includes(depId)) step.dependsOn.push(depId);
  } else {
    step.dependsOn = step.dependsOn.filter(function(d) { return d !== depId; });
  }
  if (step.dependsOn.length === 0) delete step.dependsOn;
  wfEd.dirty = true;
  updateDirtyIndicator();
  renderEditorCanvas();
}

// ---- Delay field validation ----

function wfDelayValidate(input) {
  var v = input.value.trim();
  var valid = v === '' || /^\d+[smh]$/.test(v);
  input.style.borderColor = valid ? '' : '#f87171';
  var hint = input.nextElementSibling;
  if (hint && hint.classList.contains('wfed-delay-hint')) {
    hint.style.display = valid ? 'none' : '';
  }
}

function wfDelayBlur(stepId, key, input) {
  var v = input.value.trim();
  if (v && !/^\d+[smh]$/.test(v)) {
    input.style.borderColor = '#f87171';
    return; // don't save invalid value
  }
  input.style.borderColor = '';
  updateStepProp(stepId, key, v);
}

// ---- Skills / Tools loader ----

async function wfLoadSkills() {
  try {
    var skills = await fetchJSON('/api/skills');
    wfEd.skills = Array.isArray(skills) ? skills : [];
  } catch(e) {
    wfEd.skills = [];
  }
}

async function wfLoadTools() {
  try {
    var tools = await fetchJSON('/api/tools');
    wfEd.tools = Array.isArray(tools) ? tools : [];
  } catch(e) {
    wfEd.tools = [];
  }
}

// ---- JSON panel sync ----

function syncJsonPanelIfVisible() {
  var panel = document.getElementById('wf-json-editor-panel');
  if (!panel || panel.style.display === 'none') return;
  var ta = document.getElementById('wf-json-textarea');
  if (ta && wfEd.workflow) ta.value = JSON.stringify(wfEd.workflow, null, 2);
}

// ---- Fullscreen toggle ----

function wfEdToggleFullscreen() {
  var panel = document.getElementById('wf-editor-panel');
  if (!panel) return;
  panel.classList.toggle('wfed-fullscreen');
  var btn = document.getElementById('wf-fullscreen-btn');
  if (btn) btn.title = panel.classList.contains('wfed-fullscreen') ? '⛶ 退出全螢幕' : '⛶ 全螢幕';
}

// ---- Run confirmation modal ----

function showRunModal(wf) {
  if (!wf) return;
  if (typeof wf === 'string') wf = { name: wf, variables: {} };
  if (!wf.name) return;
  var vars = wf.variables || {};
  var modal = document.getElementById('wf-run-modal');
  var body = document.getElementById('wf-run-modal-body');
  if (!modal || !body) return;
  var varKeys = Object.keys(vars);
  var html = '<p style="color:var(--muted);margin:0 0 12px;font-size:13px">Workflow: <strong style="color:var(--text)">' + esc(wf.name) + '</strong></p>';
  if (varKeys.length > 0) {
    html += '<div style="font-size:11px;color:var(--muted);margin-bottom:8px">Variable overrides (leave blank to use defaults):</div>';
    varKeys.forEach(function(k) {
      html += '<div style="margin-bottom:6px">' +
        '<label style="font-size:11px;color:var(--muted);display:block">' + esc(k) +
        ' <span style="opacity:0.6">(default: ' + esc(String(vars[k] || '')) + ')</span></label>' +
        '<input id="wf-run-var-' + escAttr(k) + '" class="wfed-prop-input" type="text" placeholder="' +
        escAttr(String(vars[k] || '')) + '" style="width:100%;box-sizing:border-box"/>' +
        '</div>';
    });
  } else {
    html += '<p style="color:var(--muted);font-size:12px;margin:0">No variables defined for this workflow.</p>';
  }
  body.innerHTML = html;
  modal._wfName = wf.name;
  modal._varKeys = varKeys;
  modal.style.display = 'flex';
}

function closeRunModal() {
  var modal = document.getElementById('wf-run-modal');
  if (modal) modal.style.display = 'none';
}

async function confirmRunWorkflow() {
  var modal = document.getElementById('wf-run-modal');
  if (!modal) return;
  var name = modal._wfName;
  var varKeys = modal._varKeys || [];
  var variables = {};
  varKeys.forEach(function(k) {
    var input = document.getElementById('wf-run-var-' + k);
    if (input && input.value.trim()) variables[k] = input.value.trim();
  });
  closeRunModal();
  try {
    await fetchJSON('/workflows/' + encodeURIComponent(name) + '/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ variables: variables }),
    });
    toast('Workflow started: ' + name);
    setTimeout(refreshWorkflowRuns, 800);
  } catch(e) {
    toast('Run failed: ' + e.message);
  }
}
