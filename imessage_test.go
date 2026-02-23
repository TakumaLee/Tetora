package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- P20.2: iMessage via BlueBubbles Tests ---

// TestIMessageBotNewBot tests constructor.
func TestIMessageBotNewBot(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234",
			Password:  "test-pass",
			AllowedChats: []string{"iMessage;-;+1234567890"},
		},
	}
	state := newDispatchState()
	sem := make(chan struct{}, 1)

	bot := newIMessageBot(cfg, state, sem)
	if bot == nil {
		t.Fatal("expected non-nil bot")
	}
	if bot.serverURL != "http://localhost:1234" {
		t.Errorf("serverURL = %q, want %q", bot.serverURL, "http://localhost:1234")
	}
	if bot.password != "test-pass" {
		t.Errorf("password = %q, want %q", bot.password, "test-pass")
	}
	if bot.client == nil {
		t.Error("expected non-nil client")
	}
	if bot.dedup == nil {
		t.Error("expected non-nil dedup map")
	}
}

// TestIMessageBotNewBotTrailingSlash tests trailing slash is stripped.
func TestIMessageBotNewBotTrailingSlash(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234/",
			Password:  "pw",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)
	if bot.serverURL != "http://localhost:1234" {
		t.Errorf("serverURL = %q, want no trailing slash", bot.serverURL)
	}
}

// TestIMessageWebhookHandler tests the webhook endpoint.
func TestIMessageWebhookHandler(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234",
			Password:  "test",
		},
	}
	state := newDispatchState()
	state.broker = newSSEBroker()
	sem := make(chan struct{}, 1)
	bot := newIMessageBot(cfg, state, sem)

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/imessage/webhook", nil)
		w := httptest.NewRecorder()
		bot.webhookHandler(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/imessage/webhook", strings.NewReader("not json"))
		w := httptest.NewRecorder()
		bot.webhookHandler(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("non-message event", func(t *testing.T) {
		payload := `{"type": "chat-read-status-changed", "data": {}}`
		req := httptest.NewRequest("POST", "/api/imessage/webhook", strings.NewReader(payload))
		w := httptest.NewRecorder()
		bot.webhookHandler(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("valid new-message event returns 200", func(t *testing.T) {
		msg := BlueBubblesMessage{
			GUID:     "msg-001",
			ChatGUID: "iMessage;-;+1234567890",
			Text:     "Hello",
			IsFromMe: true, // from self, should be skipped in handleMessage
		}
		msgJSON, _ := json.Marshal(msg)
		payload := BlueBubblesWebhookPayload{
			Type: "new-message",
			Data: json.RawMessage(msgJSON),
		}
		payloadJSON, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/api/imessage/webhook", strings.NewReader(string(payloadJSON)))
		w := httptest.NewRecorder()
		bot.webhookHandler(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

// TestIMessageHandleMessageDedup tests dedup logic.
func TestIMessageHandleMessageDedup(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:      true,
			ServerURL:    "http://localhost:1234",
			Password:     "test",
			AllowedChats: []string{}, // empty = allow all
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	// Add a GUID to dedup.
	bot.mu.Lock()
	bot.dedup["msg-dup"] = time.Now()
	bot.mu.Unlock()

	// handleMessage should skip duplicate (no panic, no crash).
	bot.handleMessage(BlueBubblesMessage{
		GUID:     "msg-dup",
		ChatGUID: "chat-1",
		Text:     "duplicate",
		IsFromMe: false,
	})

	// The dedup entry should still exist.
	bot.mu.Lock()
	_, exists := bot.dedup["msg-dup"]
	bot.mu.Unlock()
	if !exists {
		t.Error("dedup entry should still exist")
	}
}

// TestIMessageHandleMessageIsFromMe tests that own messages are skipped.
func TestIMessageHandleMessageIsFromMe(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234",
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	// handleMessage should skip isFromMe (no panic, no dispatch).
	bot.handleMessage(BlueBubblesMessage{
		GUID:     "msg-self",
		ChatGUID: "chat-1",
		Text:     "my own message",
		IsFromMe: true,
	})

	// Should not appear in dedup since it's skipped before dedup check.
	bot.mu.Lock()
	_, exists := bot.dedup["msg-self"]
	bot.mu.Unlock()
	if exists {
		t.Error("isFromMe message should not be added to dedup")
	}
}

// TestIMessageHandleMessageEmpty tests that empty messages are skipped.
func TestIMessageHandleMessageEmpty(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234",
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	bot.handleMessage(BlueBubblesMessage{
		GUID:     "msg-empty",
		ChatGUID: "chat-1",
		Text:     "   ",
		IsFromMe: false,
	})

	bot.mu.Lock()
	_, exists := bot.dedup["msg-empty"]
	bot.mu.Unlock()
	if exists {
		t.Error("empty message should not be added to dedup")
	}
}

// TestIMessageHandleMessageAllowedChats tests allowed chat filtering.
func TestIMessageHandleMessageAllowedChats(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:      true,
			ServerURL:    "http://localhost:1234",
			Password:     "test",
			AllowedChats: []string{"chat-allowed"},
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	// Message from non-allowed chat should be skipped after dedup.
	bot.handleMessage(BlueBubblesMessage{
		GUID:     "msg-blocked",
		ChatGUID: "chat-other",
		Text:     "hello from unknown",
		IsFromMe: false,
	})

	// The dedup entry should exist (message was processed up to allowedChats check).
	bot.mu.Lock()
	_, exists := bot.dedup["msg-blocked"]
	bot.mu.Unlock()
	if !exists {
		t.Error("dedup entry should exist even for blocked chats")
	}
}

// TestIMessageSendMessage tests sendMessage request format.
func TestIMessageSendMessage(t *testing.T) {
	var receivedBody map[string]string
	var receivedPassword string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPassword = r.URL.Query().Get("password")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": 200}`))
	}))
	defer ts.Close()

	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: ts.URL,
			Password:  "test-pw",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	err := bot.sendMessage("iMessage;-;+1234567890", "Hello World")
	if err != nil {
		t.Fatalf("sendMessage failed: %v", err)
	}

	if receivedPassword != "test-pw" {
		t.Errorf("password = %q, want %q", receivedPassword, "test-pw")
	}
	if receivedBody["chatGuid"] != "iMessage;-;+1234567890" {
		t.Errorf("chatGuid = %q, want %q", receivedBody["chatGuid"], "iMessage;-;+1234567890")
	}
	if receivedBody["message"] != "Hello World" {
		t.Errorf("message = %q, want %q", receivedBody["message"], "Hello World")
	}
}

// TestIMessageSendMessageEmpty tests empty params.
func TestIMessageSendMessageEmpty(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234",
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	if err := bot.sendMessage("", "text"); err == nil {
		t.Error("expected error for empty chatGUID")
	}
	if err := bot.sendMessage("chat", ""); err == nil {
		t.Error("expected error for empty text")
	}
}

// TestIMessageSendMessageHTTPError tests HTTP error handling.
func TestIMessageSendMessageHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "server error"}`))
	}))
	defer ts.Close()

	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: ts.URL,
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	err := bot.sendMessage("chat-1", "hello")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain status code, got: %s", err.Error())
	}
}

// TestIMessageSearchMessages tests search response parsing.
func TestIMessageSearchMessages(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("password") != "test-pw" {
			t.Errorf("password = %q, want %q", r.URL.Query().Get("password"), "test-pw")
		}
		if r.URL.Query().Get("query") != "hello" {
			t.Errorf("query = %q, want %q", r.URL.Query().Get("query"), "hello")
		}
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"guid":        "msg-001",
					"chatGuid":    "chat-1",
					"text":        "hello world",
					"handle":      map[string]string{"address": "+1234567890"},
					"dateCreated": 1700000000000,
					"isFromMe":    false,
				},
				{
					"guid":        "msg-002",
					"chatGuid":    "chat-1",
					"text":        "hello again",
					"handle":      map[string]string{"address": "+0987654321"},
					"dateCreated": 1700000001000,
					"isFromMe":    true,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: ts.URL,
			Password:  "test-pw",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	msgs, err := bot.searchMessages("hello", 10)
	if err != nil {
		t.Fatalf("searchMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].GUID != "msg-001" {
		t.Errorf("first message GUID = %q, want %q", msgs[0].GUID, "msg-001")
	}
	if msgs[0].Handle != "+1234567890" {
		t.Errorf("first message handle = %q, want %q", msgs[0].Handle, "+1234567890")
	}
	if msgs[1].IsFromMe != true {
		t.Error("second message should be isFromMe=true")
	}
}

// TestIMessageSearchMessagesEmpty tests empty query error.
func TestIMessageSearchMessagesEmpty(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234",
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	_, err := bot.searchMessages("", 10)
	if err == nil {
		t.Error("expected error for empty query")
	}
}

// TestIMessageReadRecentMessages tests read response parsing.
func TestIMessageReadRecentMessages(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check URL path contains chat GUID.
		if !strings.Contains(r.URL.Path, "chat-1") {
			t.Errorf("URL path should contain chat GUID, got: %s", r.URL.Path)
		}
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"guid":        "msg-recent-1",
					"chatGuid":    "chat-1",
					"text":        "recent message",
					"handle":      map[string]string{"address": "+111"},
					"dateCreated": 1700000000000,
					"isFromMe":    false,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: ts.URL,
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	msgs, err := bot.readRecentMessages("chat-1", 5)
	if err != nil {
		t.Fatalf("readRecentMessages failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Text != "recent message" {
		t.Errorf("text = %q, want %q", msgs[0].Text, "recent message")
	}
}

// TestIMessageReadRecentMessagesEmptyChatGUID tests empty chat GUID error.
func TestIMessageReadRecentMessagesEmptyChatGUID(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234",
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	_, err := bot.readRecentMessages("", 5)
	if err == nil {
		t.Error("expected error for empty chatGUID")
	}
}

// TestIMessageSendTapback tests tapback request.
func TestIMessageSendTapback(t *testing.T) {
	var receivedBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "msg-001") {
			t.Errorf("URL path should contain message GUID, got: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: ts.URL,
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	err := bot.sendTapback("chat-1", "msg-001", 2000)
	if err != nil {
		t.Fatalf("sendTapback failed: %v", err)
	}
	if receivedBody["chatGuid"] != "chat-1" {
		t.Errorf("chatGuid = %v, want %q", receivedBody["chatGuid"], "chat-1")
	}
	tapback, ok := receivedBody["tapback"].(float64) // JSON numbers are float64
	if !ok || int(tapback) != 2000 {
		t.Errorf("tapback = %v, want 2000", receivedBody["tapback"])
	}
}

// TestIMessageConfigWebhookPathOrDefault tests webhook path helper.
func TestIMessageConfigWebhookPathOrDefault(t *testing.T) {
	tests := []struct {
		name string
		cfg  IMessageConfig
		want string
	}{
		{"default", IMessageConfig{}, "/api/imessage/webhook"},
		{"custom", IMessageConfig{WebhookPath: "/custom/hook"}, "/custom/hook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.webhookPathOrDefault()
			if got != tt.want {
				t.Errorf("webhookPathOrDefault() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIMessageNotifierInterface tests that IMessageBot satisfies Notifier.
func TestIMessageNotifierInterface(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:      true,
			ServerURL:    "http://localhost:1234",
			Password:     "test",
			AllowedChats: []string{"chat-1"},
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	// Test Name().
	var n Notifier = bot
	if n.Name() != "imessage" {
		t.Errorf("Name() = %q, want %q", n.Name(), "imessage")
	}

	// Test Send() with no allowed chats.
	bot2 := newIMessageBot(&Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234",
			Password:  "test",
		},
	}, nil, nil)
	err := bot2.Send("test")
	if err == nil {
		t.Error("expected error when no allowed chats configured")
	}

	// Test Send() with empty text.
	err = bot.Send("")
	if err != nil {
		t.Errorf("Send empty text should return nil, got: %v", err)
	}
}

// TestIMessagePresenceSetterInterface tests that IMessageBot satisfies PresenceSetter.
func TestIMessagePresenceSetterInterface(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: "http://localhost:1234",
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	var ps PresenceSetter = bot
	if ps.PresenceName() != "imessage" {
		t.Errorf("PresenceName() = %q, want %q", ps.PresenceName(), "imessage")
	}

	// SetTyping should be a no-op.
	err := ps.SetTyping(context.Background(), "chat-1")
	if err != nil {
		t.Errorf("SetTyping should return nil, got: %v", err)
	}
}

// TestIMessageToolHandlerSend tests the imessage_send tool.
func TestIMessageToolHandlerSend(t *testing.T) {
	// Set up a mock server.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": 200}`))
	}))
	defer ts.Close()

	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: ts.URL,
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)
	globalIMessageBot = bot
	defer func() { globalIMessageBot = nil }()

	input, _ := json.Marshal(map[string]string{
		"chat_guid": "chat-1",
		"text":      "hello tool",
	})
	result, err := toolIMessageSend(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolIMessageSend failed: %v", err)
	}
	if !strings.Contains(result, "chat-1") {
		t.Errorf("result should contain chat GUID, got: %s", result)
	}
}

// TestIMessageToolHandlerSendMissing tests missing params.
func TestIMessageToolHandlerSendMissing(t *testing.T) {
	globalIMessageBot = nil
	input, _ := json.Marshal(map[string]string{"chat_guid": "chat-1", "text": "hi"})
	_, err := toolIMessageSend(context.Background(), nil, input)
	if err == nil {
		t.Error("expected error when bot not initialized")
	}
}

// TestIMessageToolHandlerSearch tests the imessage_search tool.
func TestIMessageToolHandlerSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"guid": "msg-1", "chatGuid": "c1", "text": "found", "handle": map[string]string{"address": "+1"}, "dateCreated": 1700000000000, "isFromMe": false},
			},
		})
	}))
	defer ts.Close()

	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: ts.URL,
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)
	globalIMessageBot = bot
	defer func() { globalIMessageBot = nil }()

	input, _ := json.Marshal(map[string]interface{}{"query": "found"})
	result, err := toolIMessageSearch(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolIMessageSearch failed: %v", err)
	}
	if !strings.Contains(result, "msg-1") {
		t.Errorf("result should contain message GUID, got: %s", result)
	}
}

// TestIMessageToolHandlerRead tests the imessage_read tool.
func TestIMessageToolHandlerRead(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"guid": "msg-r1", "chatGuid": "chat-1", "text": "recent", "handle": map[string]string{"address": "+2"}, "dateCreated": 1700000000000, "isFromMe": false},
			},
		})
	}))
	defer ts.Close()

	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:   true,
			ServerURL: ts.URL,
			Password:  "test",
		},
	}
	bot := newIMessageBot(cfg, nil, nil)
	globalIMessageBot = bot
	defer func() { globalIMessageBot = nil }()

	input, _ := json.Marshal(map[string]interface{}{"chat_guid": "chat-1", "limit": 5})
	result, err := toolIMessageRead(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolIMessageRead failed: %v", err)
	}
	if !strings.Contains(result, "msg-r1") {
		t.Errorf("result should contain message GUID, got: %s", result)
	}
}

// TestIMessageDedupCleanup tests that old dedup entries are cleaned up.
func TestIMessageDedupCleanup(t *testing.T) {
	cfg := &Config{
		IMessage: IMessageConfig{
			Enabled:      true,
			ServerURL:    "http://localhost:1234",
			Password:     "test",
			AllowedChats: []string{"chat-allowed-only"}, // restrict to prevent dispatch
		},
	}
	bot := newIMessageBot(cfg, nil, nil)

	// Pre-populate dedup with old entries.
	bot.mu.Lock()
	oldTime := time.Now().Add(-10 * time.Minute) // older than 5 min cutoff
	for i := 0; i < 5; i++ {
		bot.dedup[fmt.Sprintf("old-msg-%d", i)] = oldTime
	}
	bot.mu.Unlock()

	// Process a new message (which triggers dedup cleanup).
	// This message will be added to dedup then stopped by allowedChats filter.
	bot.handleMessage(BlueBubblesMessage{
		GUID:     "new-msg",
		ChatGUID: "chat-not-allowed",
		Text:     "trigger cleanup",
		IsFromMe: false,
	})

	// Old entries should be cleaned up.
	bot.mu.Lock()
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("old-msg-%d", i)
		if _, exists := bot.dedup[key]; exists {
			t.Errorf("old dedup entry %q should have been cleaned up", key)
		}
	}
	// New message should be in dedup (it gets added before allowedChats check).
	if _, exists := bot.dedup["new-msg"]; !exists {
		t.Error("new message should be in dedup")
	}
	bot.mu.Unlock()
}
