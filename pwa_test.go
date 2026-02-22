package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Manifest tests
// ---------------------------------------------------------------------------

func TestPWAManifest_ValidJSON(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(pwaManifestJSON), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	required := []string{"name", "short_name", "start_url", "display", "icons", "theme_color", "background_color"}
	for _, key := range required {
		if _, ok := m[key]; !ok {
			t.Errorf("manifest missing required field %q", key)
		}
	}
}

func TestPWAManifest_DisplayStandalone(t *testing.T) {
	var m map[string]any
	json.Unmarshal([]byte(pwaManifestJSON), &m)
	if m["display"] != "standalone" {
		t.Errorf("expected display=standalone, got %v", m["display"])
	}
}

func TestPWAManifest_StartURL(t *testing.T) {
	var m map[string]any
	json.Unmarshal([]byte(pwaManifestJSON), &m)
	if m["start_url"] != "/dashboard" {
		t.Errorf("expected start_url=/dashboard, got %v", m["start_url"])
	}
}

func TestPWAManifest_ThemeColor(t *testing.T) {
	var m map[string]any
	json.Unmarshal([]byte(pwaManifestJSON), &m)
	if m["theme_color"] != "#a78bfa" {
		t.Errorf("expected theme_color=#a78bfa, got %v", m["theme_color"])
	}
}

// ---------------------------------------------------------------------------
// Icon tests
// ---------------------------------------------------------------------------

func TestPWAIcon_ValidSVG(t *testing.T) {
	if !strings.Contains(pwaIconSVG, "<svg") {
		t.Error("icon does not contain <svg tag")
	}
	if !strings.Contains(pwaIconSVG, "</svg>") {
		t.Error("icon does not contain closing </svg> tag")
	}
	if !strings.Contains(pwaIconSVG, "xmlns") {
		t.Error("icon missing xmlns attribute")
	}
}

func TestPWAIcon_BrandColors(t *testing.T) {
	if !strings.Contains(pwaIconSVG, "#a78bfa") {
		t.Error("icon missing accent color #a78bfa")
	}
	if !strings.Contains(pwaIconSVG, "#60a5fa") {
		t.Error("icon missing accent2 color #60a5fa")
	}
	if !strings.Contains(pwaIconSVG, "#08080d") {
		t.Error("icon missing background color #08080d")
	}
}

// ---------------------------------------------------------------------------
// Service Worker tests
// ---------------------------------------------------------------------------

func TestPWAServiceWorker_EventListeners(t *testing.T) {
	for _, event := range []string{"install", "activate", "fetch"} {
		if !strings.Contains(pwaServiceWorkerJS, "'"+event+"'") {
			t.Errorf("service worker missing %q event listener", event)
		}
	}
}

func TestPWAServiceWorker_SkipsPOST(t *testing.T) {
	if !strings.Contains(pwaServiceWorkerJS, "e.request.method !== 'GET'") {
		t.Error("service worker does not skip non-GET requests")
	}
}

func TestPWAServiceWorker_SkipsSSE(t *testing.T) {
	if !strings.Contains(pwaServiceWorkerJS, "/stream") {
		t.Error("service worker does not skip SSE stream paths")
	}
}

// ---------------------------------------------------------------------------
// Handler tests
// ---------------------------------------------------------------------------

func TestHandlePWAManifest_ContentType(t *testing.T) {
	req := httptest.NewRequest("GET", "/dashboard/manifest.json", nil)
	rr := httptest.NewRecorder()
	handlePWAManifest(rr, req)
	if ct := rr.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("Content-Type = %q, want application/manifest+json", ct)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Body.Len() == 0 {
		t.Error("response body is empty")
	}
}

func TestHandlePWAServiceWorker_Headers(t *testing.T) {
	req := httptest.NewRequest("GET", "/dashboard/sw.js", nil)
	rr := httptest.NewRecorder()
	handlePWAServiceWorker(rr, req)
	if ct := rr.Header().Get("Content-Type"); ct != "application/javascript" {
		t.Errorf("Content-Type = %q, want application/javascript", ct)
	}
	if swa := rr.Header().Get("Service-Worker-Allowed"); swa != "/" {
		t.Errorf("Service-Worker-Allowed = %q, want /", swa)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	// Version replacement should have happened.
	body := rr.Body.String()
	if strings.Contains(body, "TETORA_VERSION") {
		t.Error("service worker still contains TETORA_VERSION placeholder")
	}
	if !strings.Contains(body, tetoraVersion) {
		t.Errorf("service worker does not contain actual version %q", tetoraVersion)
	}
}

func TestHandlePWAIcon_ContentType(t *testing.T) {
	req := httptest.NewRequest("GET", "/dashboard/icon.svg", nil)
	rr := httptest.NewRecorder()
	handlePWAIcon(rr, req)
	if ct := rr.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Dashboard HTML integration tests
// ---------------------------------------------------------------------------

func TestDashboardHTML_ManifestLink(t *testing.T) {
	html := string(dashboardHTML)
	if !strings.Contains(html, `rel="manifest"`) {
		t.Error("dashboard.html missing manifest link")
	}
	if !strings.Contains(html, `/dashboard/manifest.json`) {
		t.Error("dashboard.html manifest link has wrong href")
	}
}

func TestDashboardHTML_SWRegistration(t *testing.T) {
	html := string(dashboardHTML)
	if !strings.Contains(html, "serviceWorker") {
		t.Error("dashboard.html missing service worker registration")
	}
	if !strings.Contains(html, "/dashboard/sw.js") {
		t.Error("dashboard.html SW registration has wrong path")
	}
}

func TestDashboardHTML_ThemeColor(t *testing.T) {
	html := string(dashboardHTML)
	if !strings.Contains(html, `name="theme-color"`) {
		t.Error("dashboard.html missing theme-color meta tag")
	}
}

func TestDashboardHTML_InstallButton(t *testing.T) {
	html := string(dashboardHTML)
	if !strings.Contains(html, "pwa-install-btn") {
		t.Error("dashboard.html missing PWA install button")
	}
	if !strings.Contains(html, "pwaInstall") {
		t.Error("dashboard.html missing pwaInstall function")
	}
}

// ---------------------------------------------------------------------------
// Auth middleware bypass test
// ---------------------------------------------------------------------------

func TestDashboardAuthMiddleware_AllowsPWAAssets(t *testing.T) {
	cfg := &Config{
		DashboardAuth: DashboardAuthConfig{
			Enabled:  true,
			Password: "secret",
		},
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := dashboardAuthMiddleware(cfg, inner)

	paths := []string{"/dashboard/manifest.json", "/dashboard/sw.js", "/dashboard/icon.svg"}
	for _, p := range paths {
		req := httptest.NewRequest("GET", p, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("path %s returned %d with auth enabled, expected 200 (bypass)", p, rr.Code)
		}
	}
}
