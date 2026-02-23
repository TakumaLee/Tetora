// Tetora PWA Application
const API_BASE = localStorage.getItem('tetora_api') || window.location.origin;
const API_TOKEN = localStorage.getItem('tetora_token') || '';

const $ = (sel) => document.querySelector(sel);
const statusDot = $('#statusDot');
const promptInput = $('#promptInput');
const dispatchBtn = $('#dispatchBtn');
const quickActionsEl = $('#quickActions');
const historyEl = $('#history');

const quickActions = [
  { label: 'Status', prompt: '/status' },
  { label: 'Tasks', prompt: '/tasks list' },
  { label: 'Notes', prompt: '/notes today' },
  { label: 'Agents', prompt: '/agents status' },
];

async function api(method, path, body) {
  const headers = { 'Content-Type': 'application/json' };
  if (API_TOKEN) headers['Authorization'] = `Bearer ${API_TOKEN}`;
  try {
    const resp = await fetch(`${API_BASE}${path}`, {
      method, headers, body: body ? JSON.stringify(body) : undefined,
    });
    return await resp.json();
  } catch (e) {
    return { error: e.message };
  }
}

async function checkStatus() {
  const result = await api('GET', '/api/status');
  statusDot.classList.toggle('online', !result.error);
}

async function dispatch(prompt) {
  if (!prompt.trim()) return;
  dispatchBtn.disabled = true;
  dispatchBtn.textContent = 'Sending...';
  const result = await api('POST', '/api/dispatch', { prompt });
  dispatchBtn.disabled = false;
  dispatchBtn.textContent = 'Send';
  addHistory(prompt, result);
  promptInput.value = '';
  return result;
}

function addHistory(prompt, result) {
  const items = JSON.parse(localStorage.getItem('tetora_history') || '[]');
  items.unshift({ prompt, result: result.error || result.taskId || 'OK', time: new Date().toISOString() });
  if (items.length > 50) items.length = 50;
  localStorage.setItem('tetora_history', JSON.stringify(items));
  renderHistory(items);
}

function renderHistory(items) {
  if (!items) items = JSON.parse(localStorage.getItem('tetora_history') || '[]');
  historyEl.innerHTML = items.map((item) => `
    <div class="history-item">
      <div class="time">${new Date(item.time).toLocaleString()}</div>
      <div class="prompt">${escapeHtml(item.prompt)}</div>
      <div class="result">${escapeHtml(String(item.result))}</div>
    </div>
  `).join('');
}

function escapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

function renderQuickActions() {
  quickActionsEl.innerHTML = quickActions.map((a) => `
    <div class="quick-action" data-prompt="${escapeHtml(a.prompt)}">${escapeHtml(a.label)}</div>
  `).join('');
  quickActionsEl.addEventListener('click', (e) => {
    const el = e.target.closest('.quick-action');
    if (el) dispatch(el.dataset.prompt);
  });
}

async function registerPush() {
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;
  try {
    const reg = await navigator.serviceWorker.register('/sw.js');
    const sub = await reg.pushManager.getSubscription();
    if (!sub) {
      const vapidKey = await api('GET', '/api/push/vapid-key');
      if (vapidKey.publicKey) {
        const newSub = await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: vapidKey.publicKey,
        });
        await api('POST', '/api/push/subscribe', newSub.toJSON());
      }
    }
  } catch (e) {
    console.warn('Push registration failed:', e);
  }
}

function handleShare() {
  const params = new URLSearchParams(window.location.search);
  const text = params.get('text') || params.get('title') || params.get('url');
  if (text && window.location.pathname === '/share') {
    promptInput.value = text;
    dispatch(text);
    history.replaceState(null, '', '/');
  }
}

function init() {
  renderQuickActions();
  renderHistory();
  checkStatus();
  setInterval(checkStatus, 30000);
  handleShare();
  registerPush();
  dispatchBtn.addEventListener('click', () => dispatch(promptInput.value));
  promptInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); dispatch(promptInput.value); }
  });
}

init();
