package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- P18.2: OAuth 2.0 Framework Tests ---

// TestEncryptDecryptOAuthToken tests round-trip encryption.
func TestEncryptDecryptOAuthToken(t *testing.T) {
	key := "test-encryption-key-12345"

	// Round-trip.
	original := "my-secret-access-token-abc123"
	encrypted, err := encryptOAuthToken(original, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if encrypted == original {
		t.Fatal("encrypted should differ from original")
	}

	decrypted, err := decryptOAuthToken(encrypted, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != original {
		t.Fatalf("decrypted %q != original %q", decrypted, original)
	}

	// Wrong key should fail.
	_, err = decryptOAuthToken(encrypted, "wrong-key")
	if err == nil {
		t.Fatal("should fail with wrong key")
	}

	// Empty input should return empty.
	enc, err := encryptOAuthToken("", key)
	if err != nil || enc != "" {
		t.Fatalf("empty input: enc=%q err=%v", enc, err)
	}
	dec, err := decryptOAuthToken("", key)
	if err != nil || dec != "" {
		t.Fatalf("empty decrypt: dec=%q err=%v", dec, err)
	}

	// No key = plaintext pass-through.
	enc, err = encryptOAuthToken("hello", "")
	if err != nil || enc != "hello" {
		t.Fatalf("no key encrypt: enc=%q err=%v", enc, err)
	}
	dec, err = decryptOAuthToken("hello", "")
	if err != nil || dec != "hello" {
		t.Fatalf("no key decrypt: dec=%q err=%v", dec, err)
	}

	// Two encryptions of same plaintext should differ (random nonce).
	enc1, _ := encryptOAuthToken(original, key)
	enc2, _ := encryptOAuthToken(original, key)
	if enc1 == enc2 {
		t.Fatal("two encryptions should differ (random nonce)")
	}
}

// TestOAuthState tests CSRF state generation and expiry.
func TestOAuthState(t *testing.T) {
	cfg := &Config{ListenAddr: ":8080"}
	mgr := newOAuthManager(cfg)

	// Generate state.
	state, err := generateState()
	if err != nil {
		t.Fatalf("generateState: %v", err)
	}
	if len(state) != 32 { // 16 bytes = 32 hex chars
		t.Fatalf("state length: %d", len(state))
	}

	// Two states should differ.
	state2, _ := generateState()
	if state == state2 {
		t.Fatal("two states should differ")
	}

	// Store and validate.
	mgr.mu.Lock()
	mgr.states[state] = oauthState{service: "google", createdAt: time.Now()}
	mgr.mu.Unlock()

	mgr.mu.Lock()
	st, ok := mgr.states[state]
	mgr.mu.Unlock()
	if !ok || st.service != "google" {
		t.Fatal("state should be stored")
	}

	// Expired state cleanup.
	mgr.mu.Lock()
	mgr.states["old"] = oauthState{service: "old", createdAt: time.Now().Add(-15 * time.Minute)}
	mgr.cleanExpiredStates()
	_, hasOld := mgr.states["old"]
	_, hasNew := mgr.states[state]
	mgr.mu.Unlock()

	if hasOld {
		t.Fatal("expired state should be cleaned")
	}
	if !hasNew {
		t.Fatal("valid state should remain")
	}
}

// TestTokenStorage tests store/load/delete/list with a temp DB.
func TestTokenStorage(t *testing.T) {
	// Check sqlite3 is available.
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Init table.
	if err := initOAuthTable(dbPath); err != nil {
		t.Fatalf("initOAuthTable: %v", err)
	}

	encKey := "test-key"

	// Store token.
	token := OAuthToken{
		ServiceName:  "github",
		AccessToken:  "ghp_xxxxxxxxxxxx",
		RefreshToken: "ghr_xxxxxxxxxxxx",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339),
		Scopes:       "repo user",
	}
	if err := storeOAuthToken(dbPath, token, encKey); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Load token.
	loaded, err := loadOAuthToken(dbPath, "github", encKey)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded token is nil")
	}
	if loaded.AccessToken != token.AccessToken {
		t.Fatalf("access_token mismatch: %q vs %q", loaded.AccessToken, token.AccessToken)
	}
	if loaded.RefreshToken != token.RefreshToken {
		t.Fatalf("refresh_token mismatch")
	}
	if loaded.Scopes != "repo user" {
		t.Fatalf("scopes mismatch: %q", loaded.Scopes)
	}

	// List statuses.
	statuses, err := listOAuthTokenStatuses(dbPath, encKey)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if !statuses[0].Connected {
		t.Fatal("should be connected")
	}
	if statuses[0].ServiceName != "github" {
		t.Fatalf("service name: %q", statuses[0].ServiceName)
	}

	// Load non-existent.
	none, err := loadOAuthToken(dbPath, "nonexistent", encKey)
	if err != nil {
		t.Fatalf("load nonexistent: %v", err)
	}
	if none != nil {
		t.Fatal("should be nil for non-existent")
	}

	// Delete token.
	if err := deleteOAuthToken(dbPath, "github"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	deleted, _ := loadOAuthToken(dbPath, "github", encKey)
	if deleted != nil {
		t.Fatal("should be nil after delete")
	}

	// List after delete.
	statuses, _ = listOAuthTokenStatuses(dbPath, encKey)
	if len(statuses) != 0 {
		t.Fatalf("expected 0 statuses after delete, got %d", len(statuses))
	}
}

// TestTokenRefresh tests token refresh with a mock server.
func TestTokenRefresh(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initOAuthTable(dbPath); err != nil {
		t.Fatalf("initOAuthTable: %v", err)
	}

	// Mock token server.
	newAccessToken := "new-access-token-xyz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  newAccessToken,
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "new-refresh-token",
		})
	}))
	defer srv.Close()

	encKey := "test-key"

	// Store an expired token.
	token := OAuthToken{
		ServiceName:  "testservice",
		AccessToken:  "old-expired-token",
		RefreshToken: "valid-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
		Scopes:       "read",
	}
	if err := storeOAuthToken(dbPath, token, encKey); err != nil {
		t.Fatalf("store: %v", err)
	}

	cfg := &Config{
		HistoryDB:  dbPath,
		ListenAddr: ":8080",
		OAuth: OAuthConfig{
			EncryptionKey: encKey,
			Services: map[string]OAuthServiceConfig{
				"testservice": {
					ClientID:     "test-client-id",
					ClientSecret: "test-client-secret",
					AuthURL:      srv.URL + "/auth",
					TokenURL:     srv.URL + "/token",
				},
			},
		},
	}

	mgr := newOAuthManager(cfg)
	refreshed, err := mgr.refreshTokenIfNeeded("testservice")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.AccessToken != newAccessToken {
		t.Fatalf("expected %q, got %q", newAccessToken, refreshed.AccessToken)
	}

	// Verify token was stored.
	stored, _ := loadOAuthToken(dbPath, "testservice", encKey)
	if stored.AccessToken != newAccessToken {
		t.Fatalf("stored token mismatch: %q", stored.AccessToken)
	}
}

// TestOAuthTemplates verifies built-in templates have required fields.
func TestOAuthTemplates(t *testing.T) {
	for name, tmpl := range oauthTemplates {
		if tmpl.AuthURL == "" {
			t.Errorf("template %q missing AuthURL", name)
		}
		if tmpl.TokenURL == "" {
			t.Errorf("template %q missing TokenURL", name)
		}
	}

	// Verify known templates exist.
	for _, name := range []string{"google", "github", "twitter"} {
		if _, ok := oauthTemplates[name]; !ok {
			t.Errorf("missing template: %s", name)
		}
	}
}

// TestOAuthManagerRequest tests authenticated requests with mock.
func TestOAuthManagerRequest(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initOAuthTable(dbPath); err != nil {
		t.Fatalf("initOAuthTable: %v", err)
	}

	// Mock API server that verifies Authorization header.
	var receivedAuth string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":"ok"}`))
	}))
	defer apiSrv.Close()

	encKey := "test-key"
	accessToken := "test-bearer-token-123"

	// Store a valid (non-expired) token.
	token := OAuthToken{
		ServiceName: "mockapi",
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339),
	}
	if err := storeOAuthToken(dbPath, token, encKey); err != nil {
		t.Fatalf("store: %v", err)
	}

	cfg := &Config{
		HistoryDB:  dbPath,
		ListenAddr: ":8080",
		OAuth: OAuthConfig{
			EncryptionKey: encKey,
			Services: map[string]OAuthServiceConfig{
				"mockapi": {
					ClientID: "id",
					AuthURL:  "http://example.com/auth",
					TokenURL: "http://example.com/token",
				},
			},
		},
	}

	mgr := newOAuthManager(cfg)
	resp, err := mgr.Request(context.Background(), "mockapi", "GET", apiSrv.URL+"/test", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	expectedAuth := "Bearer " + accessToken
	if receivedAuth != expectedAuth {
		t.Fatalf("auth header: %q, expected %q", receivedAuth, expectedAuth)
	}
}

// TestHandleCallback tests OAuth callback with mock exchange.
func TestHandleCallback(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initOAuthTable(dbPath); err != nil {
		t.Fatalf("initOAuthTable: %v", err)
	}

	// Mock token exchange server.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "callback-access-token",
			"refresh_token": "callback-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    7200,
			"scope":         "read write",
		})
	}))
	defer tokenSrv.Close()

	encKey := "test-key"
	cfg := &Config{
		HistoryDB:  dbPath,
		ListenAddr: ":8080",
		OAuth: OAuthConfig{
			EncryptionKey: encKey,
			RedirectBase:  "http://localhost:8080",
			Services: map[string]OAuthServiceConfig{
				"testcb": {
					ClientID:     "client-id",
					ClientSecret: "client-secret",
					AuthURL:      tokenSrv.URL + "/auth",
					TokenURL:     tokenSrv.URL + "/token",
					Scopes:       []string{"read", "write"},
				},
			},
		},
	}

	mgr := newOAuthManager(cfg)

	// First, generate a valid state.
	stateToken, _ := generateState()
	mgr.mu.Lock()
	mgr.states[stateToken] = oauthState{service: "testcb", createdAt: time.Now()}
	mgr.mu.Unlock()

	// Simulate callback request.
	req := httptest.NewRequest("GET",
		fmt.Sprintf("/api/oauth/testcb/callback?code=auth-code-123&state=%s", stateToken),
		nil)
	w := httptest.NewRecorder()

	mgr.handleCallback(w, req, "testcb")

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body := w.Body.String()
		t.Fatalf("callback status: %d, body: %s", resp.StatusCode, body)
	}

	// Verify token was stored.
	stored, err := loadOAuthToken(dbPath, "testcb", encKey)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if stored == nil {
		t.Fatal("stored token is nil")
	}
	if stored.AccessToken != "callback-access-token" {
		t.Fatalf("access_token: %q", stored.AccessToken)
	}
	if stored.RefreshToken != "callback-refresh-token" {
		t.Fatalf("refresh_token: %q", stored.RefreshToken)
	}
	if !strings.Contains(stored.Scopes, "read") {
		t.Fatalf("scopes: %q", stored.Scopes)
	}

	// Callback with invalid state should fail.
	req2 := httptest.NewRequest("GET",
		"/api/oauth/testcb/callback?code=auth-code-123&state=invalid-state", nil)
	w2 := httptest.NewRecorder()
	mgr.handleCallback(w2, req2, "testcb")
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("invalid state should return 400, got %d", w2.Code)
	}

	// Callback without state should fail.
	req3 := httptest.NewRequest("GET",
		"/api/oauth/testcb/callback?code=auth-code-123", nil)
	w3 := httptest.NewRecorder()
	mgr.handleCallback(w3, req3, "testcb")
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("missing state should return 400, got %d", w3.Code)
	}
}

// TestResolveServiceConfig tests template merging.
func TestResolveServiceConfig(t *testing.T) {
	cfg := &Config{
		ListenAddr: ":8080",
		OAuth: OAuthConfig{
			Services: map[string]OAuthServiceConfig{
				"google": {
					ClientID:     "my-client-id",
					ClientSecret: "my-secret",
					Scopes:       []string{"email", "profile"},
				},
				"custom": {
					ClientID:     "custom-id",
					ClientSecret: "custom-secret",
					AuthURL:      "https://custom.example.com/auth",
					TokenURL:     "https://custom.example.com/token",
				},
			},
		},
	}

	mgr := newOAuthManager(cfg)

	// Google should merge template + user config.
	google, err := mgr.resolveServiceConfig("google")
	if err != nil {
		t.Fatalf("resolve google: %v", err)
	}
	if google.ClientID != "my-client-id" {
		t.Fatalf("clientId: %q", google.ClientID)
	}
	if google.AuthURL != "https://accounts.google.com/o/oauth2/v2/auth" {
		t.Fatalf("authUrl should come from template: %q", google.AuthURL)
	}
	if google.ExtraParams["access_type"] != "offline" {
		t.Fatal("extra params should come from template")
	}

	// Custom service should use user config.
	custom, err := mgr.resolveServiceConfig("custom")
	if err != nil {
		t.Fatalf("resolve custom: %v", err)
	}
	if custom.AuthURL != "https://custom.example.com/auth" {
		t.Fatalf("authUrl: %q", custom.AuthURL)
	}

	// Unknown service should fail.
	_, err = mgr.resolveServiceConfig("unknown")
	if err == nil {
		t.Fatal("should fail for unknown service")
	}
}

// TestToolOAuthStatus tests the tool handler.
func TestToolOAuthStatus(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initOAuthTable(dbPath); err != nil {
		t.Fatalf("initOAuthTable: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		OAuth:     OAuthConfig{EncryptionKey: "test"},
	}

	// No tokens — should return helpful message.
	result, err := toolOAuthStatus(context.Background(), cfg, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolOAuthStatus: %v", err)
	}
	if !strings.Contains(result, "No OAuth") {
		t.Fatalf("expected no-services message, got: %s", result)
	}

	// Store a token, then check.
	storeOAuthToken(dbPath, OAuthToken{
		ServiceName: "github",
		AccessToken: "test",
		Scopes:      "repo",
	}, "test")

	result, err = toolOAuthStatus(context.Background(), cfg, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolOAuthStatus: %v", err)
	}
	if !strings.Contains(result, "github") {
		t.Fatalf("expected github in result: %s", result)
	}
}

// Note: TestMain is defined in another test file in this package.
// Logger initialization is handled there.
