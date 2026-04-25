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

  const raw = document.getElementById('rf-url').value;
  const urls = raw.split('\n').map(s => s.trim()).filter(Boolean);
  if (!urls.length) return false;

  const agent = document.getElementById('rf-agent').value.trim();
  const model = document.getElementById('rf-model').value.trim();
  const postComment = document.getElementById('rf-post-comment').checked;

  document.getElementById('rf-actions').style.display = 'none';
  document.getElementById('rf-loading').style.display = '';

  const results = [];
  let totalCost = 0;

  for (let i = 0; i < urls.length; i++) {
    const prUrl = normalizeReviewURL(urls[i]);
    document.getElementById('rf-progress').textContent =
      urls.length > 1 ? `(${i + 1}/${urls.length}) Reviewing ${prUrl}…` : `Reviewing…`;

    const startTime = Date.now();
    if (reviewTimer) clearInterval(reviewTimer);
    reviewTimer = setInterval(() => {
      const elapsed = Math.floor((Date.now() - startTime) / 1000);
      const m = Math.floor(elapsed / 60);
      const s = elapsed % 60;
      document.getElementById('rf-elapsed').textContent = m > 0 ? `${m}m ${s}s` : `${s}s`;
    }, 1000);

    try {
      const payload = { pr_url: prUrl, post_comment: postComment };
      if (agent) payload.agent = agent;
      if (model) payload.model = model;

      const result = await fetchJSON('/review', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });

      clearInterval(reviewTimer); reviewTimer = null;
      totalCost += result.costUsd || 0;
      const secs = Math.round((result.durationMs || 0) / 1000);
      if (result.status === 'ok') {
        const commentTag = result.commented
          ? ' [commented]'
          : result.comment_error ? ` [comment failed: ${result.comment_error}]` : '';
        results.push(`✓ ${prUrl} ($${(result.costUsd||0).toFixed(4)}, ${secs}s)${commentTag}\n${result.output || ''}`);
      } else {
        results.push(`✗ ${prUrl}: ${result.error || 'unknown error'}`);
      }
    } catch (err) {
      if (reviewTimer) { clearInterval(reviewTimer); reviewTimer = null; }
      const msg = (err && err.message) ? err.message : String(err);
      if (msg.includes('409') || msg.includes('dispatch already running')) {
        results.push(`✗ ${prUrl}: another dispatch is running — aborted`);
        break;
      }
      results.push(`✗ ${prUrl}: ${msg}`);
    }
  }

  closeReviewModal();
  const summary = urls.length > 1 ? `${urls.length} reviews done (total $${totalCost.toFixed(4)})\n\n` : '';
  alert(summary + results.join('\n\n---\n\n'));
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
