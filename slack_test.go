package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- stripSlackMentions ---

func TestStripSlackMentions(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "hello world"},
		{"<@U12345> hello", "hello"},
		{"<@U12345|user> hello", "hello"},
		{"<@U12345> <@U67890> hello", "hello"},
		{"hello <@U12345> world", "hello  world"},
		{"<@U12345>", ""},
		{"", ""},
		{"no mentions here", "no mentions here"},
		{"<@UABC123> run tests <@UDEF456>", "run tests"},
	}

	for _, tt := range tests {
		got := stripSlackMentions(tt.input)
		if got != tt.expected {
			t.Errorf("stripSlackMentions(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- threadTS ---

func TestThreadTS(t *testing.T) {
	// When ThreadTS is set, use it.
	e1 := slackEvent{TS: "123.456", ThreadTS: "100.200"}
	if got := threadTS(e1); got != "100.200" {
		t.Errorf("threadTS with ThreadTS = %q, want %q", got, "100.200")
	}

	// When ThreadTS is empty, use TS.
	e2 := slackEvent{TS: "123.456", ThreadTS: ""}
	if got := threadTS(e2); got != "123.456" {
		t.Errorf("threadTS without ThreadTS = %q, want %q", got, "123.456")
	}
}

// --- verifySlackSignature ---

func TestVerifySlackSignature(t *testing.T) {
	secret := "test-signing-secret"
	body := []byte(`{"type":"event_callback"}`)
	ts := fmt.Sprintf("%d", time.Now().Unix())

	// Compute valid signature.
	baseStr := fmt.Sprintf("v0:%s:%s", ts, string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(baseStr))
	validSig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	// Valid signature.
	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(string(body)))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", validSig)
	if !verifySlackSignature(req, body, secret) {
		t.Error("expected valid signature to pass")
	}

	// Invalid signature.
	req2 := httptest.NewRequest("POST", "/slack/events", strings.NewReader(string(body)))
	req2.Header.Set("X-Slack-Request-Timestamp", ts)
	req2.Header.Set("X-Slack-Signature", "v0=deadbeef")
	if verifySlackSignature(req2, body, secret) {
		t.Error("expected invalid signature to fail")
	}

	// Missing headers.
	req3 := httptest.NewRequest("POST", "/slack/events", strings.NewReader(string(body)))
	if verifySlackSignature(req3, body, secret) {
		t.Error("expected missing headers to fail")
	}

	// Expired timestamp (>5 min old).
	oldTS := fmt.Sprintf("%d", time.Now().Unix()-400)
	baseStr2 := fmt.Sprintf("v0:%s:%s", oldTS, string(body))
	mac2 := hmac.New(sha256.New, []byte(secret))
	mac2.Write([]byte(baseStr2))
	oldSig := "v0=" + hex.EncodeToString(mac2.Sum(nil))

	req4 := httptest.NewRequest("POST", "/slack/events", strings.NewReader(string(body)))
	req4.Header.Set("X-Slack-Request-Timestamp", oldTS)
	req4.Header.Set("X-Slack-Signature", oldSig)
	if verifySlackSignature(req4, body, secret) {
		t.Error("expected expired timestamp to fail")
	}
}

// --- slackEventHandler URL verification ---

func TestSlackEventHandler_URLVerification(t *testing.T) {
	cfg := &Config{
		Slack: SlackBotConfig{
			Enabled:       true,
			BotToken:      "xoxb-test",
			SigningSecret:  "",
		},
	}
	state := newDispatchState()
	sem := make(chan struct{}, 3)
	sb := newSlackBot(cfg, state, sem, nil, nil)

	payload := `{"type":"url_verification","challenge":"test-challenge-123"}`
	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(payload))
	w := httptest.NewRecorder()

	sb.slackEventHandler(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["challenge"] != "test-challenge-123" {
		t.Errorf("challenge = %q, want %q", resp["challenge"], "test-challenge-123")
	}
}

// --- slackEventHandler method check ---

func TestSlackEventHandler_MethodNotAllowed(t *testing.T) {
	cfg := &Config{
		Slack: SlackBotConfig{Enabled: true, BotToken: "xoxb-test"},
	}
	state := newDispatchState()
	sem := make(chan struct{}, 3)
	sb := newSlackBot(cfg, state, sem, nil, nil)

	req := httptest.NewRequest("GET", "/slack/events", nil)
	w := httptest.NewRecorder()

	sb.slackEventHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// --- slackEventHandler signature verification ---

func TestSlackEventHandler_InvalidSignature(t *testing.T) {
	cfg := &Config{
		Slack: SlackBotConfig{
			Enabled:       true,
			BotToken:      "xoxb-test",
			SigningSecret:  "my-secret",
		},
	}
	state := newDispatchState()
	sem := make(chan struct{}, 3)
	sb := newSlackBot(cfg, state, sem, nil, nil)

	payload := `{"type":"url_verification","challenge":"test"}`
	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(payload))
	req.Header.Set("X-Slack-Request-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("X-Slack-Signature", "v0=invalid")
	w := httptest.NewRecorder()

	sb.slackEventHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- isDuplicate ---

func TestSlackBotDeduplicate(t *testing.T) {
	cfg := &Config{
		Slack: SlackBotConfig{Enabled: true, BotToken: "xoxb-test"},
	}
	state := newDispatchState()
	sem := make(chan struct{}, 3)
	sb := newSlackBot(cfg, state, sem, nil, nil)

	// First call: not duplicate.
	if sb.isDuplicate("event-001") {
		t.Error("first call should not be duplicate")
	}
	// Second call: duplicate.
	if !sb.isDuplicate("event-001") {
		t.Error("second call should be duplicate")
	}
	// Different event: not duplicate.
	if sb.isDuplicate("event-002") {
		t.Error("different event should not be duplicate")
	}
}

// --- SlackBotConfig JSON ---

func TestSlackBotConfigJSON(t *testing.T) {
	raw := `{
		"enabled": true,
		"botToken": "$SLACK_BOT_TOKEN",
		"signingSecret": "$SLACK_SIGNING_SECRET",
		"defaultChannel": "C12345"
	}`

	var cfg SlackBotConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !cfg.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.BotToken != "$SLACK_BOT_TOKEN" {
		t.Errorf("botToken = %q, want %q", cfg.BotToken, "$SLACK_BOT_TOKEN")
	}
	if cfg.SigningSecret != "$SLACK_SIGNING_SECRET" {
		t.Errorf("signingSecret = %q", cfg.SigningSecret)
	}
	if cfg.DefaultChannel != "C12345" {
		t.Errorf("defaultChannel = %q", cfg.DefaultChannel)
	}
}

// --- Event callback with bot message (should be ignored) ---

func TestSlackEventHandler_IgnoresBotMessages(t *testing.T) {
	cfg := &Config{
		Slack: SlackBotConfig{Enabled: true, BotToken: "xoxb-test"},
	}
	state := newDispatchState()
	sem := make(chan struct{}, 3)
	sb := newSlackBot(cfg, state, sem, nil, nil)

	event := slackEvent{
		Type:  "message",
		Text:  "hello from bot",
		BotID: "B12345",
	}
	eventJSON, _ := json.Marshal(event)

	payload := fmt.Sprintf(`{"type":"event_callback","event_id":"ev1","event":%s}`, string(eventJSON))
	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(payload))
	w := httptest.NewRecorder()

	sb.slackEventHandler(w, req)

	// Should acknowledge immediately.
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
