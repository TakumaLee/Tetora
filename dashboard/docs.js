// --- Documentation Viewer ---

var docsState = {
  list: [],
  activeFile: '',
  loaded: false
};

async function refreshDocs() {
  if (docsState.loaded) return;
  try {
    var list = await fetchJSON('/api/docs');
    docsState.list = list || [];
    docsState.loaded = true;
    renderDocsSidebar();
    // Load README by default
    var readme = docsState.list.find(function(d) { return d.file === 'README.md'; });
    if (readme) {
      loadDoc(readme.file, readme.name);
    } else if (docsState.list.length > 0) {
      loadDoc(docsState.list[0].file, docsState.list[0].name);
    }
  } catch(e) {
    var sidebar = document.getElementById('docs-sidebar-list');
    if (sidebar) sidebar.innerHTML = '<div style="color:var(--muted);padding:12px;font-size:12px">Failed to load docs list</div>';
  }
}

function renderDocsSidebar(filter) {
  var sidebar = document.getElementById('docs-sidebar-list');
  if (!sidebar) return;
  var items = docsState.list;
  if (filter) {
    var q = filter.toLowerCase();
    items = items.filter(function(d) {
      return d.name.toLowerCase().indexOf(q) >= 0 ||
             d.description.toLowerCase().indexOf(q) >= 0;
    });
  }
  if (items.length === 0) {
    sidebar.innerHTML = '<div style="color:var(--muted);padding:12px;font-size:12px">No results</div>';
    return;
  }
  sidebar.innerHTML = items.map(function(d) {
    var active = d.file === docsState.activeFile ? ' docs-nav-active' : '';
    return '<button class="docs-nav-item' + active + '" onclick="loadDoc(\'' + escAttr(d.file) + '\',\'' + escAttr(d.name) + '\')">' +
      '<span class="docs-nav-name">' + esc(d.name) + '</span>' +
      '<span class="docs-nav-desc">' + esc(d.description) + '</span>' +
      '</button>';
  }).join('');
}

async function loadDoc(file, name) {
  docsState.activeFile = file;
  renderDocsSidebar(document.getElementById('docs-search') ? document.getElementById('docs-search').value : '');

  var title = document.getElementById('docs-title');
  var content = document.getElementById('docs-rendered');
  if (title) title.textContent = name || file;
  if (content) content.innerHTML = '<div style="color:var(--muted);padding:24px;font-size:13px">Loading...</div>';

  try {
    var resp = await fetch('/api/docs/' + file);
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    var text = await resp.text();
    if (content) {
      content.innerHTML = renderMarkdown(text);
      content.scrollTop = 0;
    }
  } catch(e) {
    if (content) content.innerHTML = '<div style="color:var(--red);padding:24px;font-size:13px">Failed to load: ' + esc(e.message || String(e)) + '</div>';
  }
}

function filterDocsSearch() {
  var q = (document.getElementById('docs-search') || {}).value || '';
  renderDocsSidebar(q);
}
