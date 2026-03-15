package pwa

import (
	"net/http"
	"strings"
)

// ManifestJSON is the PWA manifest served at /dashboard/manifest.json.
const ManifestJSON = `{
  "name": "Tetora",
  "short_name": "Tetora",
  "description": "AI Agent Orchestrator",
  "start_url": "/dashboard",
  "scope": "/",
  "display": "standalone",
  "background_color": "#08080d",
  "theme_color": "#a78bfa",
  "icons": [
    {
      "src": "/dashboard/icon.svg",
      "sizes": "any",
      "type": "image/svg+xml",
      "purpose": "any maskable"
    }
  ]
}`

// ServiceWorkerJS is the service worker served at /dashboard/sw.js.
// TETORA_VERSION is replaced at serve-time with the actual version for cache busting.
const ServiceWorkerJS = `'use strict';
var CACHE_VERSION = 'tetora-TETORA_VERSION';
var APP_SHELL = [
  '/dashboard',
  '/dashboard/manifest.json',
  '/dashboard/icon.svg'
];

self.addEventListener('install', function(e) {
  e.waitUntil(
    caches.open(CACHE_VERSION).then(function(cache) {
      return cache.addAll(APP_SHELL);
    }).then(function() {
      return self.skipWaiting();
    })
  );
});

self.addEventListener('activate', function(e) {
  e.waitUntil(
    caches.keys().then(function(keys) {
      return Promise.all(
        keys.filter(function(k) { return k !== CACHE_VERSION; })
            .map(function(k) { return caches.delete(k); })
      );
    }).then(function() {
      return self.clients.claim();
    })
  );
});

self.addEventListener('fetch', function(e) {
  if (e.request.method !== 'GET') return;

  var url = new URL(e.request.url);

  // Explicitly skip SSE streams.
  if (url.pathname.indexOf('/stream') === 0) return;

  // Only cache app shell assets. Let all API requests pass through
  // to the browser's native fetch so Referer and cookies are preserved
  // (SW-initiated fetches can strip Referer, causing auth failures).
  var isShell = url.pathname === '/dashboard' ||
                url.pathname === '/dashboard/manifest.json' ||
                url.pathname === '/dashboard/icon.svg' ||
                url.pathname === '/dashboard/office-bg.webp' ||
                url.pathname.indexOf('/dashboard/sprites/') === 0;

  if (!isShell) return;

  e.respondWith(
    fetch(e.request).then(function(resp) {
      if (resp.ok) {
        var clone = resp.clone();
        caches.open(CACHE_VERSION).then(function(c) { c.put(e.request, clone); });
      }
      return resp;
    }).catch(function() {
      return caches.match(e.request);
    })
  );
});
`

// IconSVG is the app icon served at /dashboard/icon.svg.
const IconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">
  <defs>
    <linearGradient id="g" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" style="stop-color:#a78bfa"/>
      <stop offset="100%" style="stop-color:#60a5fa"/>
    </linearGradient>
  </defs>
  <rect width="512" height="512" rx="96" fill="#08080d"/>
  <text x="256" y="340" text-anchor="middle" font-family="system-ui,sans-serif" font-size="280" font-weight="700" fill="url(#g)">T</text>
</svg>`

// HandleManifest serves the PWA manifest JSON.
func HandleManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(ManifestJSON))
}

// HandleServiceWorker returns an http.HandlerFunc that serves the service worker JS
// with TETORA_VERSION replaced by the provided version string.
func HandleServiceWorker(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
		sw := strings.Replace(ServiceWorkerJS, "TETORA_VERSION", version, 1)
		w.Write([]byte(sw))
	}
}

// HandleIcon serves the app icon SVG.
func HandleIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Write([]byte(IconSVG))
}
