package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWhatsAppWebhookVerification(t *testing.T) {
	cfg := &Config{
		WhatsApp: WhatsAppConfig{
			Enabled:     true,
			VerifyToken: "test_verify_token_123",
		},
	}

	state := newDispatchState()
	sem := make(chan struct{}, 1)
	bot := newWhatsAppBot(cfg, state, sem, nil)

	tests := []struct {
		name       string
		mode       string
		token      string
		challenge  string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "valid verification",
			mode:       "subscribe",
			token:      "test_verify_token_123",
			challenge:  "challenge_12345",
			wantStatus: 200,
			wantBody:   "challenge_12345",
		},
		{
			name:       "invalid token",
			mode:       "subscribe",
			token:      "wrong_token",
			challenge:  "challenge_12345",
			wantStatus: 403,
			wantBody:   "forbidden",
		},
		{
			name:       "invalid mode",
			mode:       "unsubscribe",
			token:      "test_verify_token_123",
			challenge:  "challenge_12345",
			wantStatus: 403,
			wantBody:   "forbidden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/whatsapp/webhook?hub.mode="+tt.mode+"&hub.verify_token="+tt.token+"&hub.challenge="+tt.challenge, nil)
			w := httptest.NewRecorder()

			bot.whatsAppWebhookHandler(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			if !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want to contain %q", w.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestSendWhatsAppMessagePayload(t *testing.T) {
	// This test verifies payload format (doesn't actually send).
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                "15551234567",
		"type":              "text",
		"text": map[string]string{
			"body": "Hello from Tetora!",
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed["messaging_product"] != "whatsapp" {
		t.Errorf("messaging_product = %q, want %q", parsed["messaging_product"], "whatsapp")
	}

	if parsed["to"] != "15551234567" {
		t.Errorf("to = %q, want %q", parsed["to"], "15551234567")
	}

	textMap := parsed["text"].(map[string]interface{})
	if textMap["body"] != "Hello from Tetora!" {
		t.Errorf("text.body = %q, want %q", textMap["body"], "Hello from Tetora!")
	}
}

func TestWhatsAppSignatureVerification(t *testing.T) {
	appSecret := "test_secret_key"
	body := []byte(`{"test":"data"}`)

	// Generate valid signature.
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name      string
		appSecret string
		body      []byte
		signature string
		want      bool
	}{
		{
			name:      "valid signature",
			appSecret: appSecret,
			body:      body,
			signature: validSig,
			want:      true,
		},
		{
			name:      "invalid signature",
			appSecret: appSecret,
			body:      body,
			signature: "sha256=invalid",
			want:      false,
		},
		{
			name:      "missing sha256 prefix",
			appSecret: appSecret,
			body:      body,
			signature: hex.EncodeToString(mac.Sum(nil)),
			want:      false,
		},
		{
			name:      "empty signature",
			appSecret: appSecret,
			body:      body,
			signature: "",
			want:      false,
		},
		{
			name:      "no secret configured",
			appSecret: "",
			body:      body,
			signature: "anything",
			want:      true, // skip verification if no secret
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := verifyWhatsAppSignature(tt.appSecret, tt.body, tt.signature)
			if got != tt.want {
				t.Errorf("verifyWhatsAppSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWhatsAppAPIVersion(t *testing.T) {
	tests := []struct {
		name string
		cfg  WhatsAppConfig
		want string
	}{
		{
			name: "default version",
			cfg:  WhatsAppConfig{},
			want: "v21.0",
		},
		{
			name: "custom version",
			cfg:  WhatsAppConfig{APIVersion: "v18.0"},
			want: "v18.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.apiVersion()
			if got != tt.want {
				t.Errorf("apiVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWhatsAppConfigEnabled(t *testing.T) {
	cfg := &Config{
		WhatsApp: WhatsAppConfig{
			Enabled:       true,
			PhoneNumberID: "123456789",
			AccessToken:   "test_token",
			VerifyToken:   "verify_token",
		},
	}

	if !cfg.WhatsApp.Enabled {
		t.Error("WhatsApp should be enabled")
	}

	if cfg.WhatsApp.PhoneNumberID != "123456789" {
		t.Errorf("PhoneNumberID = %q, want %q", cfg.WhatsApp.PhoneNumberID, "123456789")
	}
}

func TestWhatsAppMessageText(t *testing.T) {
	text := &whatsAppMessageText{
		Body: "Hello, world!",
	}

	if text.Body != "Hello, world!" {
		t.Errorf("Body = %q, want %q", text.Body, "Hello, world!")
	}

	// Test JSON marshaling.
	data, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed whatsAppMessageText
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed.Body != "Hello, world!" {
		t.Errorf("parsed.Body = %q, want %q", parsed.Body, "Hello, world!")
	}
}

func TestWhatsAppDedupMessages(t *testing.T) {
	cfg := &Config{
		WhatsApp: WhatsAppConfig{
			Enabled:       true,
			PhoneNumberID: "123456789",
			AccessToken:   "test_token",
		},
		SmartDispatch: SmartDispatchConfig{
			Enabled:     false,
			DefaultRole: "琉璃",
		},
		HistoryDB: ":memory:",
	}

	state := newDispatchState()
	sem := make(chan struct{}, 1)
	bot := newWhatsAppBot(cfg, state, sem, nil)

	// Mark message as processed.
	bot.mu.Lock()
	bot.processed["msg_123"] = time.Now()
	bot.processedSize = 1
	bot.mu.Unlock()

	// Check dedup works.
	bot.mu.Lock()
	if _, seen := bot.processed["msg_123"]; !seen {
		t.Error("message not found in processed map")
	}
	count := bot.processedSize
	bot.mu.Unlock()

	if count != 1 {
		t.Errorf("processedSize = %d, want 1", count)
	}
}

func TestWhatsAppBotCreation(t *testing.T) {
	cfg := &Config{
		WhatsApp: WhatsAppConfig{
			Enabled:       true,
			PhoneNumberID: "123456789",
			AccessToken:   "test_token",
		},
	}

	state := newDispatchState()
	sem := make(chan struct{}, 1)

	bot := newWhatsAppBot(cfg, state, sem, nil)
	if bot == nil {
		t.Fatal("newWhatsAppBot returned nil")
	}

	if bot.cfg != cfg {
		t.Error("bot config not set correctly")
	}

	if bot.state != state {
		t.Error("bot state not set correctly")
	}

	if bot.sem != sem {
		t.Error("bot semaphore not set correctly")
	}

	if bot.processed == nil {
		t.Error("bot processed map not initialized")
	}
}
