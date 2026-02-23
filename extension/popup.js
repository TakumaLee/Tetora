const statusEl = document.getElementById('status');
const portEl = document.getElementById('port');
const tokenEl = document.getElementById('token');
const toggleBtn = document.getElementById('toggle');

let currentEnabled = false;

chrome.storage.local.get(['enabled', 'token', 'port'], (data) => {
  currentEnabled = data.enabled || false;
  tokenEl.value = data.token || '';
  portEl.value = data.port || 18792;
  updateUI();
});

function updateUI() {
  if (currentEnabled) {
    statusEl.textContent = 'Connected';
    statusEl.className = 'status connected';
    toggleBtn.textContent = 'Disconnect';
    toggleBtn.className = 'btn-off';
  } else {
    statusEl.textContent = 'Disconnected';
    statusEl.className = 'status disconnected';
    toggleBtn.textContent = 'Connect';
    toggleBtn.className = 'btn-on';
  }
}

toggleBtn.addEventListener('click', () => {
  currentEnabled = !currentEnabled;
  chrome.runtime.sendMessage({
    type: 'toggle',
    enabled: currentEnabled,
    token: tokenEl.value,
    port: parseInt(portEl.value) || 18792
  });
  chrome.storage.local.set({
    enabled: currentEnabled,
    token: tokenEl.value,
    port: parseInt(portEl.value) || 18792
  });
  updateUI();
});
