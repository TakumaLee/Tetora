package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Signature Verification Tests ---

func TestVerifyWebhookSignature_NoSecret(t *testing.T) {
	r := httptest.NewRequest("POST", "/hooks/test", nil)
	if !verifyWebhookSignature(r, []byte("body"), "") {
		t.Error("expected true when no secret configured")
	}
}

func TestVerifyWebhookSignature_GitHub(t *testing.T) {
	secret := "mysecret"
	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	r := httptest.NewRequest("POST", "/hooks/test", nil)
	r.Header.Set("X-Hub-Signature-256", sig)

	if !verifyWebhookSignature(r, body, secret) {
		t.Error("expected true for valid GitHub signature")
	}

	// Wrong signature.
	r2 := httptest.NewRequest("POST", "/hooks/test", nil)
	r2.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	if verifyWebhookSignature(r2, body, secret) {
		t.Error("expected false for invalid GitHub signature")
	}
}

func TestVerifyWebhookSignature_GitLab(t *testing.T) {
	secret := "gitlab-token"
	r := httptest.NewRequest("POST", "/hooks/test", nil)
	r.Header.Set("X-Gitlab-Token", secret)

	if !verifyWebhookSignature(r, []byte("body"), secret) {
		t.Error("expected true for valid GitLab token")
	}

	r2 := httptest.NewRequest("POST", "/hooks/test", nil)
	r2.Header.Set("X-Gitlab-Token", "wrong")
	if verifyWebhookSignature(r2, []byte("body"), secret) {
		t.Error("expected false for wrong GitLab token")
	}
}

func TestVerifyWebhookSignature_Generic(t *testing.T) {
	secret := "genericsecret"
	body := []byte(`{"data":"test"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	r := httptest.NewRequest("POST", "/hooks/test", nil)
	r.Header.Set("X-Webhook-Signature", sig)

	if !verifyWebhookSignature(r, body, secret) {
		t.Error("expected true for valid generic signature")
	}
}

func TestVerifyWebhookSignature_SecretButNoHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/hooks/test", nil)
	if verifyWebhookSignature(r, []byte("body"), "secret") {
		t.Error("expected false when secret is set but no signature header")
	}
}

// --- HMAC-SHA256 Tests ---

func TestVerifyHMACSHA256(t *testing.T) {
	secret := "test-secret"
	body := []byte("hello world")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	if !verifyHMACSHA256(body, secret, sig) {
		t.Error("expected true for valid HMAC")
	}
	if verifyHMACSHA256(body, secret, "badhex") {
		t.Error("expected false for invalid hex")
	}
	if verifyHMACSHA256(body, "wrong-secret", sig) {
		t.Error("expected false for wrong secret")
	}
}

// --- Template Expansion Tests ---

func TestExpandPayloadTemplate_Simple(t *testing.T) {
	payload := map[string]any{
		"action": "opened",
		"title":  "Fix bug",
		"count":  float64(42),
	}

	result := expandPayloadTemplate("Action: {{payload.action}}, Title: {{payload.title}}", payload)
	if result != "Action: opened, Title: Fix bug" {
		t.Errorf("got %q", result)
	}
}

func TestExpandPayloadTemplate_Nested(t *testing.T) {
	payload := map[string]any{
		"pull_request": map[string]any{
			"title":    "Add feature",
			"html_url": "https://github.com/repo/pull/1",
		},
	}

	result := expandPayloadTemplate("PR: {{payload.pull_request.title}} - {{payload.pull_request.html_url}}", payload)
	expected := "PR: Add feature - https://github.com/repo/pull/1"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExpandPayloadTemplate_Missing(t *testing.T) {
	result := expandPayloadTemplate("{{payload.nonexistent}}", map[string]any{})
	if result != "{{payload.nonexistent}}" {
		t.Errorf("expected original placeholder for missing key, got %q", result)
	}
}

func TestExpandPayloadTemplate_Types(t *testing.T) {
	payload := map[string]any{
		"count":   float64(42),
		"rate":    float64(3.14),
		"active":  true,
		"tags":    []any{"a", "b"},
	}

	tests := []struct {
		template string
		expected string
	}{
		{"{{payload.count}}", "42"},
		{"{{payload.rate}}", "3.14"},
		{"{{payload.active}}", "true"},
		{"{{payload.tags}}", `["a","b"]`},
	}

	for _, tt := range tests {
		result := expandPayloadTemplate(tt.template, payload)
		if result != tt.expected {
			t.Errorf("expandPayloadTemplate(%q) = %q, want %q", tt.template, result, tt.expected)
		}
	}
}

// --- Nested Value Tests ---

func TestGetNestedValue(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep",
			},
		},
		"top": "level",
	}

	if v := getNestedValue(m, "top"); v != "level" {
		t.Errorf("got %v, want 'level'", v)
	}
	if v := getNestedValue(m, "a.b.c"); v != "deep" {
		t.Errorf("got %v, want 'deep'", v)
	}
	if v := getNestedValue(m, "a.b.missing"); v != nil {
		t.Errorf("got %v, want nil", v)
	}
	if v := getNestedValue(m, "nonexistent"); v != nil {
		t.Errorf("got %v, want nil", v)
	}
}

// --- Filter Evaluation Tests ---

func TestEvaluateFilter_Empty(t *testing.T) {
	if !evaluateFilter("", map[string]any{}) {
		t.Error("empty filter should accept all")
	}
}

func TestEvaluateFilter_Equal(t *testing.T) {
	payload := map[string]any{"action": "opened"}

	if !evaluateFilter("payload.action == 'opened'", payload) {
		t.Error("expected true for matching ==")
	}
	if evaluateFilter("payload.action == 'closed'", payload) {
		t.Error("expected false for non-matching ==")
	}
}

func TestEvaluateFilter_NotEqual(t *testing.T) {
	payload := map[string]any{"action": "opened"}

	if !evaluateFilter("payload.action != 'closed'", payload) {
		t.Error("expected true for non-matching !=")
	}
	if evaluateFilter("payload.action != 'opened'", payload) {
		t.Error("expected false for matching !=")
	}
}

func TestEvaluateFilter_Truthy(t *testing.T) {
	tests := []struct {
		payload  map[string]any
		filter   string
		expected bool
	}{
		{map[string]any{"active": true}, "payload.active", true},
		{map[string]any{"active": false}, "payload.active", false},
		{map[string]any{"name": "test"}, "payload.name", true},
		{map[string]any{"name": ""}, "payload.name", false},
		{map[string]any{"count": float64(5)}, "payload.count", true},
		{map[string]any{"count": float64(0)}, "payload.count", false},
		{map[string]any{}, "payload.missing", false},
	}

	for _, tt := range tests {
		result := evaluateFilter(tt.filter, tt.payload)
		if result != tt.expected {
			t.Errorf("evaluateFilter(%q) = %v, want %v", tt.filter, result, tt.expected)
		}
	}
}

func TestEvaluateFilter_NestedKey(t *testing.T) {
	payload := map[string]any{
		"pull_request": map[string]any{
			"state": "open",
		},
	}
	if !evaluateFilter("payload.pull_request.state == 'open'", payload) {
		t.Error("expected true for nested key equality")
	}
}

func TestEvaluateFilter_DoubleQuotes(t *testing.T) {
	payload := map[string]any{"action": "opened"}
	if !evaluateFilter(`payload.action == "opened"`, payload) {
		t.Error("expected true with double quotes")
	}
}

// --- isTruthy Tests ---

func TestIsTruthy(t *testing.T) {
	tests := []struct {
		val      any
		expected bool
	}{
		{nil, false},
		{true, true},
		{false, false},
		{"hello", true},
		{"", false},
		{float64(1), true},
		{float64(0), false},
		{map[string]any{}, true},     // non-nil non-basic type = true
		{[]any{"a"}, true},
	}
	for _, tt := range tests {
		if isTruthy(tt.val) != tt.expected {
			t.Errorf("isTruthy(%v) = %v, want %v", tt.val, !tt.expected, tt.expected)
		}
	}
}

// --- IncomingWebhookConfig Tests ---

func TestIncomingWebhookConfig_IsEnabled(t *testing.T) {
	// Default (nil) → enabled.
	c := IncomingWebhookConfig{}
	if !c.isEnabled() {
		t.Error("expected enabled by default")
	}

	// Explicitly enabled.
	tr := true
	c.Enabled = &tr
	if !c.isEnabled() {
		t.Error("expected enabled when set to true")
	}

	// Explicitly disabled.
	f := false
	c.Enabled = &f
	if c.isEnabled() {
		t.Error("expected disabled when set to false")
	}
}

// --- Handler Integration Tests ---

// testWebhookConfig creates a Config with a minimal provider registry for webhook tests.
func testWebhookConfig(webhooks map[string]IncomingWebhookConfig) *Config {
	cfg := &Config{
		IncomingWebhooks: webhooks,
		registry:         newProviderRegistry(),
		baseDir:          "/tmp/tetora-test",
	}
	return cfg
}

func TestHandleIncomingWebhook_NotFound(t *testing.T) {
	cfg := testWebhookConfig(nil)
	r := httptest.NewRequest("POST", "/hooks/missing", strings.NewReader(`{}`))
	result := handleIncomingWebhook(context.Background(), cfg, "missing", r, nil, nil, nil)
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "not found") {
		t.Errorf("expected 'not found' in message, got %q", result.Message)
	}
}

func TestHandleIncomingWebhook_Disabled(t *testing.T) {
	f := false
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜", Enabled: &f},
	})
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(`{}`))
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, nil, nil)
	if result.Status != "disabled" {
		t.Errorf("expected disabled status, got %q", result.Status)
	}
}

func TestHandleIncomingWebhook_SignatureFail(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜", Secret: "mysecret"},
	})
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(`{"test":true}`))
	// No signature header.
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, nil, nil)
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "signature") {
		t.Errorf("expected signature error, got %q", result.Message)
	}
}

func TestHandleIncomingWebhook_BadJSON(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜"},
	})
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(`not json`))
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, nil, nil)
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "parse payload") {
		t.Errorf("expected parse error, got %q", result.Message)
	}
}

func TestHandleIncomingWebhook_Filtered(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"gh": {Agent: "黒曜", Filter: "payload.action == 'opened'"},
	})
	body := `{"action":"closed"}`
	r := httptest.NewRequest("POST", "/hooks/gh", strings.NewReader(body))
	result := handleIncomingWebhook(context.Background(), cfg, "gh", r, nil, nil, nil)
	if result.Status != "filtered" {
		t.Errorf("expected filtered status, got %q", result.Status)
	}
}

func TestHandleIncomingWebhook_Accepted(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜", Template: "Process: {{payload.action}}"},
	})
	body := `{"action":"opened"}`
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(body))
	sem := make(chan struct{}, 5)
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted status, got %q", result.Status)
	}
	if result.Agent != "黒曜" {
		t.Errorf("expected role 黒曜, got %q", result.Agent)
	}
	if result.TaskID == "" {
		t.Error("expected non-empty taskID")
	}
}

func TestHandleIncomingWebhook_WithValidSignature(t *testing.T) {
	secret := "webhook-secret"
	payload := `{"action":"opened","title":"Test PR"}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"gh": {Agent: "黒曜", Secret: secret, Template: "Review: {{payload.title}}"},
	})

	r := httptest.NewRequest("POST", "/hooks/gh", strings.NewReader(payload))
	r.Header.Set("X-Hub-Signature-256", sig)
	sem := make(chan struct{}, 5)

	result := handleIncomingWebhook(context.Background(), cfg, "gh", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted, got %q: %s", result.Status, result.Message)
	}
}

func TestHandleIncomingWebhook_DefaultPrompt(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜"}, // no template
	})
	body := `{"key":"value"}`
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(body))
	sem := make(chan struct{}, 5)
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted, got %q", result.Status)
	}
}

// --- HTTP Endpoint Tests ---

func TestIncomingWebhookHTTPEndpoint(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜", Template: "Test: {{payload.msg}}"},
	})
	sem := make(chan struct{}, 5)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/hooks/")
		ctx := r.Context()
		result := handleIncomingWebhook(ctx, cfg, name, r, nil, sem, nil)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	body := `{"msg":"hello"}`
	req := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var result IncomingWebhookResult
	json.NewDecoder(rr.Body).Decode(&result)
	if result.Status != "accepted" {
		t.Errorf("expected accepted, got %q", result.Status)
	}
}

func TestIncomingWebhookHTTPEndpoint_NotFound(t *testing.T) {
	cfg := testWebhookConfig(nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/hooks/")
		result := handleIncomingWebhook(r.Context(), cfg, name, r, nil, nil, nil)
		w.Header().Set("Content-Type", "application/json")
		if result.Status == "error" {
			w.WriteHeader(http.StatusBadRequest)
		}
		json.NewEncoder(w).Encode(result)
	})

	req := httptest.NewRequest("POST", "/hooks/nonexistent", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// --- Webhook List API Test ---

func TestWebhookListEndpoint(t *testing.T) {
	cfg := &Config{
		IncomingWebhooks: map[string]IncomingWebhookConfig{
			"gh-pr":  {Agent: "黒曜", Secret: "s", Filter: "payload.action == 'opened'"},
			"sentry": {Agent: "黒曜", Template: "Alert: {{payload.title}}"},
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		type webhookInfo struct {
			Name      string `json:"name"`
			Agent     string `json:"agent"`
			Enabled   bool   `json:"enabled"`
			HasSecret bool   `json:"hasSecret"`
		}
		var list []webhookInfo
		for name, wh := range cfg.IncomingWebhooks {
			list = append(list, webhookInfo{
				Name:      name,
				Agent:      wh.Agent,
				Enabled:   wh.isEnabled(),
				HasSecret: wh.Secret != "",
			})
		}
		json.NewEncoder(w).Encode(list)
	})

	req := httptest.NewRequest("GET", "/webhooks/incoming", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var list []map[string]any
	json.NewDecoder(rr.Body).Decode(&list)
	if len(list) != 2 {
		t.Errorf("expected 2 webhooks, got %d", len(list))
	}
}

// --- Workflow Trigger Tests ---

func TestTriggerWebhookWorkflow_NotFound(t *testing.T) {
	cfg := &Config{
		baseDir: t.TempDir(),
	}
	whCfg := IncomingWebhookConfig{
		Agent:     "黒曜",
		Workflow: "nonexistent",
	}
	result := triggerWebhookWorkflow(context.Background(), cfg, "test", whCfg,
		map[string]any{}, "prompt", nil, nil, nil)
	if result.Status != "error" {
		t.Errorf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "nonexistent") {
		t.Errorf("expected workflow name in error, got %q", result.Message)
	}
}

// --- Body Size Limit Test ---

func TestHandleIncomingWebhook_LargeBody(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜"},
	})
	// Create a valid JSON that's large but under 1MB.
	largePayload := `{"data":"` + strings.Repeat("x", 500000) + `"}`
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(largePayload))
	sem := make(chan struct{}, 5)
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted for large payload, got %q: %s", result.Status, result.Message)
	}
}

// --- Filter + Template Combo Test ---

func TestHandleIncomingWebhook_FilterPassAndTemplateExpand(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"gh": {
			Agent:     "黒曜",
			Filter:   "payload.action == 'opened'",
			Template: "Review PR: {{payload.pull_request.title}} ({{payload.pull_request.html_url}})",
		},
	})

	payload := map[string]any{
		"action": "opened",
		"pull_request": map[string]any{
			"title":    "Add feature X",
			"html_url": "https://github.com/repo/pull/42",
		},
	}
	body, _ := json.Marshal(payload)

	r := httptest.NewRequest("POST", "/hooks/gh", bytes.NewReader(body))
	sem := make(chan struct{}, 5)
	result := handleIncomingWebhook(context.Background(), cfg, "gh", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted, got %q: %s", result.Status, result.Message)
	}
	if result.Agent != "黒曜" {
		t.Errorf("expected role 黒曜, got %q", result.Agent)
	}
}
