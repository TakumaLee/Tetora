// --- Tetora Store ---

var storeData = { items: [], categories: [], filtered: [] };
var storeActiveCategory = '';

async function refreshStore() {
  try {
    var data = await fetchJSON('/api/store/browse');
    storeData.items = data.items || [];
    storeData.categories = data.categories || [];
    storeData.filtered = storeData.items;
    storeActiveCategory = '';
    renderStoreCategories();
    renderStoreFeatured();
    renderStoreGrid(storeData.filtered);
  } catch(e) {
    document.getElementById('store-grid').innerHTML =
      '<div class="cap-empty">Error loading store: ' + esc(e.message || '') + '</div>';
  }
}

function renderStoreCategories() {
  var el = document.getElementById('store-categories');
  var cats = storeData.categories || [];
  var html = '<button class="store-cat-btn' + (storeActiveCategory === '' ? ' active' : '') +
    '" onclick="filterStoreCategory(\'\')">All (' + storeData.items.length + ')</button>';
  cats.forEach(function(c) {
    html += '<button class="store-cat-btn' + (storeActiveCategory === c.name ? ' active' : '') +
      '" onclick="filterStoreCategory(\'' + escAttr(c.name) + '\')">' +
      esc(c.name) + ' (' + c.count + ')</button>';
  });
  el.innerHTML = html;
}

function renderStoreFeatured() {
  var el = document.getElementById('store-featured');
  // Show top 4 items with most steps (as "featured") — placeholder for real featured logic.
  var featured = storeData.items.slice().sort(function(a, b) { return b.stepCount - a.stepCount; }).slice(0, 4);
  if (featured.length === 0) { el.innerHTML = ''; return; }

  el.innerHTML = '<div style="font-size:13px;font-weight:600;color:var(--text);margin-bottom:8px">Featured</div>' +
    '<div class="store-featured-grid">' +
    featured.map(function(item) { return renderStoreCard(item, true); }).join('') +
    '</div>';
}

function renderStoreGrid(items) {
  var el = document.getElementById('store-grid');
  if (items.length === 0) {
    el.innerHTML = '<div class="cap-empty">No templates found</div>';
    return;
  }
  el.innerHTML = items.map(function(item) { return renderStoreCard(item, false); }).join('');
}

function renderStoreCard(item, featured) {
  var badges = '';
  if (item.category) badges += '<span class="cap-badge cap-badge-category">' + esc(item.category) + '</span>';
  badges += '<span class="cap-badge">' + item.stepCount + ' steps</span>';
  if (item.source === 'installed') badges += '<span class="cap-badge cap-badge-approved">installed</span>';
  else if (item.source === 'builtin') badges += '<span class="cap-badge cap-badge-builtin">builtin</span>';
  if (item.installed && item.source !== 'installed') badges += '<span class="cap-badge cap-badge-approved">installed</span>';

  var tags = '';
  if (item.tags && item.tags.length > 0) {
    tags = '<div class="store-card-tags">' +
      item.tags.slice(0, 4).map(function(t) { return '<span class="store-tag">' + esc(t) + '</span>'; }).join('') +
    '</div>';
  }

  var actions = '';
  if (item.source === 'builtin' && !item.installed) {
    actions = '<button class="btn btn-sm btn-primary" onclick="event.stopPropagation();installCapTemplate(\'' + escAttr(item.name) + '\')">Install</button>';
  } else if (item.source === 'builtin' && item.installed) {
    actions = '<button class="btn btn-sm" onclick="event.stopPropagation();previewCapTemplate(\'' + escAttr(item.name) + '\')">Preview</button>';
  } else if (item.source === 'installed') {
    actions = '<button class="btn btn-sm" onclick="event.stopPropagation();editCapWorkflow(\'' + escAttr(item.name) + '\')">Edit</button>' +
              '<button class="btn btn-sm" onclick="event.stopPropagation();exportWorkflow(\'' + escAttr(item.name) + '\')">Export</button>';
  }

  var cls = 'store-card' + (featured ? ' store-card-featured' : '');
  return '<div class="' + cls + '">' +
    '<div class="cap-card-name">' + esc(item.name) + '</div>' +
    '<div class="cap-card-desc">' + esc(item.description || '') + '</div>' +
    '<div class="cap-card-badges">' + badges + '</div>' +
    tags +
    '<div class="cap-card-actions">' + actions + '</div>' +
  '</div>';
}

function filterStoreCategory(cat) {
  storeActiveCategory = cat;
  renderStoreCategories();
  applyStoreFilters();
}

function filterStore() {
  applyStoreFilters();
}

function applyStoreFilters() {
  var q = (document.getElementById('store-search').value || '').toLowerCase();
  var cat = storeActiveCategory;

  var filtered = storeData.items.filter(function(item) {
    if (cat && item.category !== cat) return false;
    if (q) {
      return (item.name || '').toLowerCase().includes(q) ||
             (item.description || '').toLowerCase().includes(q) ||
             (item.tags || []).some(function(t) { return t.toLowerCase().includes(q); });
    }
    return true;
  });

  storeData.filtered = filtered;
  renderStoreGrid(filtered);
}
