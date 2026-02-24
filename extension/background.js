// Tetora Browser Relay — Background Service Worker
let ws = null;
let reconnectDelay = 1000;
let enabled = false;
let token = '';
let port = 18792;

// Load settings.
chrome.storage.local.get(['enabled', 'token', 'port'], (data) => {
  enabled = data.enabled || false;
  token = data.token || '';
  port = data.port || 18792;
  if (enabled) connect();
});

function connect() {
  if (ws && ws.readyState === WebSocket.OPEN) return;
  const url = `ws://127.0.0.1:${port}/relay/ws${token ? '?token=' + token : ''}`;
  ws = new WebSocket(url);

  ws.onopen = () => {
    console.log('[Tetora] Connected to relay');
    reconnectDelay = 1000;
    chrome.action.setBadgeText({ text: 'ON' });
    chrome.action.setBadgeBackgroundColor({ color: '#4CAF50' });
  };

  ws.onmessage = async (event) => {
    try {
      const req = JSON.parse(event.data);
      const result = await handleCommand(req);
      ws.send(JSON.stringify({ id: req.id, result }));
    } catch (err) {
      try {
        const req = JSON.parse(event.data);
        ws.send(JSON.stringify({ id: req.id, error: err.message }));
      } catch (_) {
        console.error('[Tetora] Failed to parse message:', err);
      }
    }
  };

  ws.onclose = () => {
    console.log('[Tetora] Disconnected');
    chrome.action.setBadgeText({ text: 'OFF' });
    chrome.action.setBadgeBackgroundColor({ color: '#F44336' });
    ws = null;
    if (enabled) {
      setTimeout(connect, reconnectDelay);
      reconnectDelay = Math.min(reconnectDelay * 2, 30000);
    }
  };

  ws.onerror = () => { ws.close(); };
}

async function handleCommand(req) {
  const params = req.params || {};
  switch (req.action) {
    case 'navigate': {
      const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
      await chrome.tabs.update(tab.id, { url: params.url });
      // Wait for page load.
      await new Promise(resolve => {
        chrome.tabs.onUpdated.addListener(function listener(tabId, info) {
          if (tabId === tab.id && info.status === 'complete') {
            chrome.tabs.onUpdated.removeListener(listener);
            resolve();
          }
        });
      });
      return 'navigated to ' + params.url;
    }
    case 'content': {
      const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
      const results = await chrome.scripting.executeScript({
        target: { tabId: tab.id },
        func: () => document.body.innerText
      });
      return results[0]?.result || '';
    }
    case 'click': {
      const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
      const results = await chrome.scripting.executeScript({
        target: { tabId: tab.id },
        func: (sel) => {
          const el = document.querySelector(sel);
          if (!el) throw new Error('element not found: ' + sel);
          el.click();
          return 'clicked ' + sel;
        },
        args: [params.selector]
      });
      return results[0]?.result || 'clicked';
    }
    case 'type': {
      const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
      const results = await chrome.scripting.executeScript({
        target: { tabId: tab.id },
        func: (sel, text) => {
          const el = document.querySelector(sel);
          if (!el) throw new Error('element not found: ' + sel);
          el.focus();
          el.value = text;
          el.dispatchEvent(new Event('input', { bubbles: true }));
          el.dispatchEvent(new Event('change', { bubbles: true }));
          return 'typed into ' + sel;
        },
        args: [params.selector, params.text]
      });
      return results[0]?.result || 'typed';
    }
    case 'screenshot': {
      const dataUrl = await chrome.tabs.captureVisibleTab(null, { format: 'png' });
      return dataUrl;
    }
    case 'eval': {
      const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
      const results = await chrome.scripting.executeScript({
        target: { tabId: tab.id },
        func: (code) => {
          return eval(code);
        },
        args: [params.code]
      });
      return JSON.stringify(results[0]?.result);
    }
    default:
      throw new Error('unknown action: ' + req.action);
  }
}

// Listen for enable/disable from popup.
chrome.runtime.onMessage.addListener((msg) => {
  if (msg.type === 'toggle') {
    enabled = msg.enabled;
    token = msg.token || token;
    port = msg.port || port;
    chrome.storage.local.set({ enabled, token, port });
    if (enabled) {
      connect();
    } else if (ws) {
      ws.close();
      ws = null;
    }
  }
  if (msg.type === 'getStatus') {
    chrome.runtime.sendMessage({
      type: 'status',
      connected: ws && ws.readyState === WebSocket.OPEN,
      enabled
    });
  }
});
