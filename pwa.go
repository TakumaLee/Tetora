package main

import (
	"net/http"
	"strings"
)

// pwaManifestJSON is the PWA manifest served at /dashboard/manifest.json.
const pwaManifestJSON = `{
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

// pwaServiceWorkerJS is the service worker served at /dashboard/sw.js.
// TETORA_VERSION is replaced at serve-time with the actual version for cache busting.
const pwaServiceWorkerJS = `'use strict';
var CACHE_VERSION = 'tetora-TETORA_VERSION';
var CACHE_API = CACHE_VERSION + '-api';
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
        keys.filter(function(k) { return k !== CACHE_VERSION && k !== CACHE_API; })
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
  if (url.pathname.indexOf('/stream') !== -1) return;
  if (url.pathname === '/dashboard/login') return;

  var isShell = url.pathname === '/dashboard' ||
                url.pathname === '/dashboard/manifest.json' ||
                url.pathname === '/dashboard/icon.svg';

  if (isShell) {
    e.respondWith(
      caches.match(e.request).then(function(cached) {
        if (cached) return cached;
        return fetch(e.request).then(function(resp) {
          if (resp.ok) {
            var clone = resp.clone();
            caches.open(CACHE_VERSION).then(function(c) { c.put(e.request, clone); });
          }
          return resp;
        });
      })
    );
    return;
  }

  e.respondWith(
    fetch(e.request).then(function(resp) {
      if (resp.ok) {
        var clone = resp.clone();
        caches.open(CACHE_API).then(function(c) { c.put(e.request, clone); });
      }
      return resp;
    }).catch(function() {
      return caches.match(e.request);
    })
  );
});
`

// pwaIconSVG is the app icon served at /dashboard/icon.svg.
const pwaIconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">
  <defs>
    <linearGradient id="g" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" style="stop-color:#a78bfa"/>
      <stop offset="100%" style="stop-color:#60a5fa"/>
    </linearGradient>
  </defs>
  <rect width="512" height="512" rx="96" fill="#08080d"/>
  <text x="256" y="340" text-anchor="middle" font-family="system-ui,sans-serif" font-size="280" font-weight="700" fill="url(#g)">T</text>
</svg>`

func handlePWAManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(pwaManifestJSON))
}

func handlePWAServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	sw := strings.Replace(pwaServiceWorkerJS, "TETORA_VERSION", tetoraVersion, 1)
	w.Write([]byte(sw))
}

func handlePWAIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Write([]byte(pwaIconSVG))
}
