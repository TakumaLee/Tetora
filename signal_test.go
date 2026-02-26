package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- P15.4: Signal Channel Tests ---

func TestSignalWebhookParsing(t *testing.T) {
	cfg := &Config{
		Signal: SignalConfig{
			Enabled:     true,
			APIBaseURL:  "http://localhost:8080",
			PhoneNumber: "+1234567890",
		},
		HistoryDB:    ":memory:",
		MaxConcurrent: 1,
		ClaudePath:   "claude",
		DefaultModel: "claude-opus-4",
		Agents:        map[string]AgentConfig{},
		Providers:    map[string]ProviderConfig{},
		baseDir:      "/tmp/tetora-test",
	}
	cfg.registry = initProviders(cfg)
	state := newDispatchState()
	sem := make(chan struct{}, 1)
	bot := newSignalBot(cfg, state, sem)

	// Sample webhook payload.
	payload := signalReceivePayload{
		Envelope: signalEnvelope{
			Source:     "+0987654321",
			SourceName: "Test User",
			SourceUUID: "test-uuid",
			Timestamp:  time.Now().UnixMilli(),
			DataMessage: &signalDataMessage{
				Timestamp: time.Now().UnixMilli(),
				Message:   "Hello from Signal",
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/signal/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Wait for async processing.
	time.Sleep(100 * time.Millisecond)

	// Check dedup tracking.
	bot.mu.Lock()
	// Check if the message was tracked (processedSize > 0).
	if bot.processedSize == 0 {
		t.Error("expected message to be tracked in dedup map")
	}
	bot.mu.Unlock()
}

func TestSignalGroupMessageHandling(t *testing.T) {
	cfg := &Config{
		Signal: SignalConfig{
			Enabled:     true,
			APIBaseURL:  "http://localhost:8080",
			PhoneNumber: "+1234567890",
		},
		HistoryDB:    ":memory:",
		MaxConcurrent: 1,
		ClaudePath:   "claude",
		DefaultModel: "claude-opus-4",
		Agents:        map[string]AgentConfig{},
		Providers:    map[string]ProviderConfig{},
		baseDir:      "/tmp/tetora-test",
	}
	cfg.registry = initProviders(cfg)
	state := newDispatchState()
	sem := make(chan struct{}, 1)
	bot := newSignalBot(cfg, state, sem)

	// Sample group message.
	payload := signalReceivePayload{
		Envelope: signalEnvelope{
			Source:     "+0987654321",
			SourceName: "Group Member",
			Timestamp:  time.Now().UnixMilli(),
			DataMessage: &signalDataMessage{
				Timestamp: time.Now().UnixMilli(),
				Message:   "Group message test",
				GroupInfo: &signalGroupInfo{
					GroupID: "group-123",
					Type:    "DELIVER",
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/signal/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Wait for async processing.
	time.Sleep(100 * time.Millisecond)

	// Verify group message was tracked.
	bot.mu.Lock()
	if bot.processedSize == 0 {
		t.Error("expected group message to be tracked")
	}
	bot.mu.Unlock()
}

func TestSignalMessageDedup(t *testing.T) {
	cfg := &Config{
		Signal: SignalConfig{
			Enabled: true,
		},
		HistoryDB:    ":memory:",
		MaxConcurrent: 1,
		ClaudePath:   "claude",
		DefaultModel: "claude-opus-4",
		Agents:        map[string]AgentConfig{},
		Providers:    map[string]ProviderConfig{},
		baseDir:      "/tmp/tetora-test",
	}
	cfg.registry = initProviders(cfg)
	state := newDispatchState()
	sem := make(chan struct{}, 1)
	bot := newSignalBot(cfg, state, sem)

	envelope := signalEnvelope{
		Source:    "+0987654321",
		Timestamp: time.Now().UnixMilli(),
		DataMessage: &signalDataMessage{
			Timestamp: time.Now().UnixMilli(),
			Message:   "Duplicate test",
		},
	}

	// Process once.
	bot.processEnvelope(envelope)
	time.Sleep(50 * time.Millisecond)

	bot.mu.Lock()
	firstSize := bot.processedSize
	bot.mu.Unlock()

	// Process again (should be deduped).
	bot.processEnvelope(envelope)
	time.Sleep(50 * time.Millisecond)

	bot.mu.Lock()
	secondSize := bot.processedSize
	bot.mu.Unlock()

	if firstSize != secondSize {
		t.Errorf("expected dedup to prevent duplicate processing, got first=%d second=%d", firstSize, secondSize)
	}
}

func TestSignalSendMessage(t *testing.T) {
	// Mock HTTP server.
	var capturedRequest *http.Request
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRequest = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"timestamp": 123456789}`))
	}))
	defer server.Close()

	cfg := &Config{
		Signal: SignalConfig{
			Enabled:     true,
			APIBaseURL:  server.URL,
			PhoneNumber: "+1234567890",
		},
	}
	bot := newSignalBot(cfg, nil, nil)

	err := bot.SendMessage("+0987654321", "Test message")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	// Verify request.
	if capturedRequest.Method != "POST" {
		t.Errorf("expected POST, got %s", capturedRequest.Method)
	}

	if !strings.Contains(capturedRequest.URL.Path, "/v2/send") {
		t.Errorf("expected /v2/send path, got %s", capturedRequest.URL.Path)
	}

	// Verify payload.
	var payload signalSendRequest
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}

	if payload.Number != "+0987654321" {
		t.Errorf("expected number +0987654321, got %s", payload.Number)
	}

	if payload.Message != "Test message" {
		t.Errorf("expected message 'Test message', got %s", payload.Message)
	}
}

func TestSignalSendGroupMessage(t *testing.T) {
	// Mock HTTP server.
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"timestamp": 123456789}`))
	}))
	defer server.Close()

	cfg := &Config{
		Signal: SignalConfig{
			Enabled:    true,
			APIBaseURL: server.URL,
		},
	}
	bot := newSignalBot(cfg, nil, nil)

	err := bot.SendGroupMessage("group-abc123", "Test group message")
	if err != nil {
		t.Fatalf("SendGroupMessage failed: %v", err)
	}

	// Verify payload.
	var payload signalSendRequest
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}

	if payload.GroupID != "group-abc123" {
		t.Errorf("expected groupID group-abc123, got %s", payload.GroupID)
	}

	if payload.Message != "Test group message" {
		t.Errorf("expected message 'Test group message', got %s", payload.Message)
	}
}

func TestSignalNotifier(t *testing.T) {
	// Mock HTTP server.
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &SignalNotifier{
		Config: SignalConfig{
			APIBaseURL: server.URL,
		},
		Recipient: "+1234567890",
		IsGroup:   false,
	}

	err := notifier.Send("Notification test")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify payload.
	var payload signalSendRequest
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}

	if payload.Number != "+1234567890" {
		t.Errorf("expected number +1234567890, got %s", payload.Number)
	}

	if payload.Message != "Notification test" {
		t.Errorf("expected message 'Notification test', got %s", payload.Message)
	}
}

func TestSignalNotifierGroupMode(t *testing.T) {
	// Mock HTTP server.
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &SignalNotifier{
		Config: SignalConfig{
			APIBaseURL: server.URL,
		},
		Recipient: "group-xyz",
		IsGroup:   true,
	}

	err := notifier.Send("Group notification")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify payload.
	var payload signalSendRequest
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}

	if payload.GroupID != "group-xyz" {
		t.Errorf("expected groupID group-xyz, got %s", payload.GroupID)
	}

	if payload.Message != "Group notification" {
		t.Errorf("expected message 'Group notification', got %s", payload.Message)
	}
}

func TestSignalEmptyMessageIgnored(t *testing.T) {
	cfg := &Config{
		Signal: SignalConfig{
			Enabled: true,
		},
		HistoryDB: ":memory:",
	}
	state := newDispatchState()
	sem := make(chan struct{}, 1)
	bot := newSignalBot(cfg, state, sem)

	envelope := signalEnvelope{
		Source:    "+0987654321",
		Timestamp: time.Now().UnixMilli(),
		DataMessage: &signalDataMessage{
			Timestamp: time.Now().UnixMilli(),
			Message:   "",
		},
	}

	bot.processEnvelope(envelope)
	time.Sleep(50 * time.Millisecond)

	bot.mu.Lock()
	if bot.processedSize != 0 {
		t.Error("expected empty message to be ignored")
	}
	bot.mu.Unlock()
}
