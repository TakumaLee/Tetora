// --- Review PR/MR Modal ---

let reviewTimer = null;

function openReviewModal() {
  const modal = document.getElementById('review-modal');
  document.getElementById('review-form').reset();
  document.getElementById('review-form').style.display = '';
  document.getElementById('rf-loading').style.display = 'none';
  document.getElementById('rf-actions').style.display = 'flex';

  // Populate agent dropdown from cached roles (kokuyou preferred default).
  const agentSel = document.getElementById('rf-agent');
  const opts = ['<option value="">— default —</option>'];
  if (typeof cachedRoles !== 'undefined' && Array.isArray(cachedRoles)) {
    cachedRoles.forEach(r => {
      opts.push(`<option value="${esc(r.name)}">${esc(r.name)}</option>`);
    });
  }
  agentSel.innerHTML = opts.join('');

  modal.classList.add('open');
}

function closeReviewModal() {
  document.getElementById('review-modal').classList.remove('open');
  if (reviewTimer) { clearInterval(reviewTimer); reviewTimer = null; }
}

async function submitReview(e) {
  e.preventDefault();

  const url = document.getElementById('rf-url').value.trim();
  if (!url) return false;

  const agent = document.getElementById('rf-agent').value.trim();
  const model = document.getElementById('rf-model').value.trim();
  const postComment = document.getElementById('rf-post-comment').checked;
  const payload = { pr_url: normalizeReviewURL(url), post_comment: postComment };
  if (agent) payload.agent = agent;
  if (model) payload.model = model;

  document.getElementById('rf-actions').style.display = 'none';
  document.getElementById('rf-loading').style.display = '';
  const startTime = Date.now();
  reviewTimer = setInterval(() => {
    const elapsed = Math.floor((Date.now() - startTime) / 1000);
    const m = Math.floor(elapsed / 60);
    const s = elapsed % 60;
    document.getElementById('rf-elapsed').textContent = m > 0 ? `${m}m ${s}s` : `${s}s`;
  }, 1000);

  try {
    const result = await fetchJSON('/review', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });

    if (reviewTimer) { clearInterval(reviewTimer); reviewTimer = null; }
    closeReviewModal();

    const cost = (result.costUsd || 0).toFixed(4);
    const secs = Math.round((result.durationMs || 0) / 1000);
    if (result.status === 'ok') {
      alert(`Review complete ($${cost}, ${secs}s)\n\n${result.output || '(no output)'}`);
    } else {
      alert(`Review failed: ${result.error || 'unknown error'}`);
    }
  } catch (err) {
    if (reviewTimer) { clearInterval(reviewTimer); reviewTimer = null; }
    closeReviewModal();
    const msg = (err && err.message) ? err.message : String(err);
    if (msg.includes('409') || msg.includes('dispatch already running')) {
      alert('Another dispatch is already running. Please retry after it finishes.');
    } else {
      alert('Review failed: ' + msg);
    }
  }
  return false;
}

// Accept github shorthand (owner/repo#NUM) → full URL.
function normalizeReviewURL(s) {
  s = (s || '').trim();
  if (/^https?:\/\//i.test(s)) return s;
  const m = s.match(/^([\w.-]+\/[\w.-]+)#(\d+)$/);
  if (m) return `https://github.com/${m[1]}/pull/${m[2]}`;
  return s;
}
