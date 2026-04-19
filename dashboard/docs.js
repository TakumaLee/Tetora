// --- Documentation Viewer ---

var docsLangNames = {
  'en':'English','zh-TW':'繁體中文','ja':'日本語','ko':'한국어',
  'id':'Bahasa Indonesia','th':'ภาษาไทย','fil':'Filipino',
  'es':'Español','fr':'Français','de':'Deutsch'
};

var docsState = {
  list: [],
  activeFile: '',
  activeName: '',
  lang: detectDocsLang(),
  loaded: false
};

function detectDocsLang() {
  var stored = localStorage.getItem('tetora-docs-lang');
  if (stored) return stored;
  var nav = (navigator.language || '').replace('_', '-');
  var supported = ['zh-TW', 'ja', 'ko', 'id', 'th', 'fil', 'es', 'fr', 'de'];
  for (var i = 0; i < supported.length; i++) {
    if (nav === supported[i]) return supported[i];
  }
  var prefix = nav.split('-')[0];
  for (var i = 0; i < supported.length; i++) {
    if (supported[i].split('-')[0] === prefix) return supported[i];
  }
  return 'en';
}

async function refreshDocs() {
  if (docsState.loaded) return;
  try {
    var list = await fetchJSON('/api/docs');
    docsState.list = list || [];
    docsState.loaded = true;
    if (docsState.list.length === 0) {
      showDocsUnderConstruction();
      return;
    }
    renderDocsSidebar();
    // Load README by default
    var readme = docsState.list.find(function(d) { return d.file === 'README.md'; });
    if (readme) {
      loadDoc(readme.file, readme.name);
    } else if (docsState.list.length > 0) {
      loadDoc(docsState.list[0].file, docsState.list[0].name);
    }
  } catch(e) {
    showDocsUnderConstruction();
  }
}

function showDocsUnderConstruction() {
  var sidebar = document.getElementById('docs-sidebar-list');
  if (sidebar) sidebar.innerHTML = '<div style="color:var(--muted);padding:16px;font-size:12px;text-align:center">Coming soon</div>';
  var content = document.getElementById('docs-rendered');
  if (content) content.innerHTML =
    '<div style="display:flex;flex-direction:column;align-items:center;justify-content:center;height:60vh;color:var(--muted);text-align:center">' +
      '<div style="font-size:40px;margin-bottom:16px">🚧</div>' +
      '<div style="font-size:18px;font-weight:600;margin-bottom:8px">Documentation Under Construction</div>' +
      '<div style="font-size:13px;max-width:360px">Documentation is being prepared. Check back later for guides, API references, and workflow examples.</div>' +
    '</div>';
  var title = document.getElementById('docs-title');
  if (title) title.textContent = 'Documentation';
}

function renderDocsSidebar(filter) {
  var sidebar = document.getElementById('docs-sidebar-list');
  if (!sidebar) return;
  var items = docsState.list;
  if (filter) {
    var q = filter.toLowerCase();
    items = items.filter(function(d) {
      return (d.name || '').toLowerCase().indexOf(q) >= 0 ||
             (d.description || '').toLowerCase().indexOf(q) >= 0;
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

function renderDocsLangSelect() {
  var sel = document.getElementById('docs-lang-select');
  if (!sel) return;
  var active = docsState.list.find(function(d) { return d.file === docsState.activeFile; });
  var langs = (active && active.langs) || [];
  if (langs.length === 0) {
    sel.style.display = 'none';
    return;
  }
  sel.style.display = '';
  var currentLang = docsState.lang;
  var options = '<option value="en"' + (currentLang === 'en' ? ' selected' : '') + '>English</option>';
  for (var i = 0; i < langs.length; i++) {
    var selected = currentLang === langs[i] ? ' selected' : '';
    options += '<option value="' + langs[i] + '"' + selected + '>' + (docsLangNames[langs[i]] || langs[i]) + '</option>';
  }
  sel.innerHTML = options;
  // If current lang not available for this doc, show English selected
  if (currentLang !== 'en' && langs.indexOf(currentLang) < 0) {
    sel.value = 'en';
  }
}

function changeDocsLang(lang) {
  docsState.lang = lang;
  localStorage.setItem('tetora-docs-lang', lang);
  if (docsState.activeFile) {
    loadDoc(docsState.activeFile, docsState.activeName);
  }
}

async function loadDoc(file, name) {
  docsState.activeFile = file;
  docsState.activeName = name;
  renderDocsSidebar(document.getElementById('docs-search') ? document.getElementById('docs-search').value : '');

  var title = document.getElementById('docs-title');
  var content = document.getElementById('docs-rendered');
  if (title) title.textContent = name || file;
  if (content) content.innerHTML = '<div style="color:var(--muted);padding:24px;font-size:13px">Loading...</div>';

  renderDocsLangSelect();

  try {
    var url = '/api/docs/' + file;
    if (docsState.lang && docsState.lang !== 'en') {
      url += '?lang=' + encodeURIComponent(docsState.lang);
    }
    var resp = await fetch(url);
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
