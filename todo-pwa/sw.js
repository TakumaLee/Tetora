const CACHE_NAME = 'wu-todo-v1';
const ASSETS = [
  './',
  './index.html',
  './style.css',
  './app.js',
  './manifest.json'
];

// 安裝時快取關鍵檔案
self.addEventListener('install', (e) => {
  e.waitUntil(
    caches.open(CACHE_NAME).then((cache) => {
      console.log('快取我大吳軍令資產');
      return cache.addAll(ASSETS);
    })
  );
});

// 攔截請求，優先從快取讀取
self.addEventListener('fetch', (e) => {
  e.respondWith(
    caches.match(e.request).then((response) => {
      return response || fetch(e.request);
    })
  );
});
