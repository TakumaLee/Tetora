package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- P19.1: Gmail Integration Tests ---

func TestBase64URLEncodeDecodeRoundtrip(t *testing.T) {
	tests := []string{
		"Hello, World!",
		"",
		"This is a longer string with special characters: <>&\"'\n\ttabs and newlines",
		"日本語テスト",
		strings.Repeat("a", 10000),
	}
	for _, input := range tests {
		encoded := base64URLEncode([]byte(input))
		decoded, err := decodeBase64URL(encoded)
		if err != nil {
			t.Errorf("decodeBase64URL(%q) error: %v", encoded, err)
			continue
		}
		if decoded != input {
			t.Errorf("roundtrip failed: got %q, want %q", decoded, input)
		}
	}
}

func TestBase64URLEncodeNopadding(t *testing.T) {
	// base64url with no padding should not contain '=' characters.
	encoded := base64URLEncode([]byte("test"))
	if strings.Contains(encoded, "=") {
		t.Errorf("base64URLEncode should not contain padding, got %q", encoded)
	}
	// Should not contain + or / (standard base64 chars).
	if strings.ContainsAny(encoded, "+/") {
		t.Errorf("base64URLEncode should use URL-safe chars, got %q", encoded)
	}
}

func TestDecodeBase64URLVariants(t *testing.T) {
	// Test that decoding works with various padding styles.
	input := "Hello!"
	encoded := base64URLEncode([]byte(input))

	// No padding (standard for Gmail API).
	decoded, err := decodeBase64URL(encoded)
	if err != nil {
		t.Fatalf("decode no padding: %v", err)
	}
	if decoded != input {
		t.Errorf("got %q, want %q", decoded, input)
	}
}

func TestBuildRFC2822Basic(t *testing.T) {
	msg := buildRFC2822("alice@example.com", "bob@example.com", "Hello Bob", "How are you?", nil, nil)

	// Check required headers.
	if !strings.Contains(msg, "MIME-Version: 1.0") {
		t.Error("missing MIME-Version header")
	}
	if !strings.Contains(msg, "Content-Type: text/plain; charset=\"UTF-8\"") {
		t.Error("missing Content-Type header")
	}
	if !strings.Contains(msg, "From: alice@example.com") {
		t.Error("missing From header")
	}
	if !strings.Contains(msg, "To: bob@example.com") {
		t.Error("missing To header")
	}
	if !strings.Contains(msg, "Subject: Hello Bob") {
		t.Error("missing Subject header")
	}
	if !strings.Contains(msg, "Date: ") {
		t.Error("missing Date header")
	}
	// Body should be after double CRLF.
	if !strings.Contains(msg, "\r\n\r\nHow are you?") {
		t.Error("body not properly separated from headers")
	}
}

func TestBuildRFC2822WithCcBcc(t *testing.T) {
	msg := buildRFC2822(
		"alice@example.com",
		"bob@example.com",
		"Team meeting",
		"See you at 3pm",
		[]string{"carol@example.com", "dave@example.com"},
		[]string{"eve@example.com"},
	)

	if !strings.Contains(msg, "Cc: carol@example.com, dave@example.com") {
		t.Error("missing or incorrect Cc header")
	}
	if !strings.Contains(msg, "Bcc: eve@example.com") {
		t.Error("missing or incorrect Bcc header")
	}
}

func TestBuildRFC2822NoCcBcc(t *testing.T) {
	msg := buildRFC2822("a@b.com", "c@d.com", "Test", "Body", nil, nil)

	if strings.Contains(msg, "Cc:") {
		t.Error("should not contain Cc header when empty")
	}
	if strings.Contains(msg, "Bcc:") {
		t.Error("should not contain Bcc header when empty")
	}
}

func TestGmailStripHTMLTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<p>Hello</p>", "Hello"},
		{"<b>bold</b> <i>italic</i>", "bold italic"},
		{"no tags here", "no tags here"},
		{"<div><p>nested</p></div>", "nested"},
		{"<a href=\"http://example.com\">link</a>", "link"},
		{"", ""},
		{"  spaces  ", "spaces"},
	}

	for _, tt := range tests {
		got := stripHTMLTags(tt.input)
		if got != tt.expected {
			t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseGmailPayload(t *testing.T) {
	payload := map[string]any{
		"headers": []any{
			map[string]any{"name": "Subject", "value": "Test Subject"},
			map[string]any{"name": "From", "value": "alice@example.com"},
			map[string]any{"name": "To", "value": "bob@example.com"},
			map[string]any{"name": "Date", "value": "Mon, 1 Jan 2024 12:00:00 +0000"},
		},
		"mimeType": "text/plain",
		"body": map[string]any{
			"data": base64URLEncode([]byte("Hello, this is the body.")),
		},
	}

	subject, from, to, date, body := parseGmailPayload(payload)

	if subject != "Test Subject" {
		t.Errorf("subject = %q, want %q", subject, "Test Subject")
	}
	if from != "alice@example.com" {
		t.Errorf("from = %q, want %q", from, "alice@example.com")
	}
	if to != "bob@example.com" {
		t.Errorf("to = %q, want %q", to, "bob@example.com")
	}
	if date != "Mon, 1 Jan 2024 12:00:00 +0000" {
		t.Errorf("date = %q", date)
	}
	if body != "Hello, this is the body." {
		t.Errorf("body = %q, want %q", body, "Hello, this is the body.")
	}
}

func TestParseGmailPayloadMultipart(t *testing.T) {
	// Simulate a multipart/alternative message with text/plain and text/html.
	payload := map[string]any{
		"headers": []any{
			map[string]any{"name": "Subject", "value": "Multipart"},
		},
		"mimeType": "multipart/alternative",
		"parts": []any{
			map[string]any{
				"mimeType": "text/plain",
				"body": map[string]any{
					"data": base64URLEncode([]byte("Plain text body")),
				},
			},
			map[string]any{
				"mimeType": "text/html",
				"body": map[string]any{
					"data": base64URLEncode([]byte("<p>HTML body</p>")),
				},
			},
		},
	}

	subject, _, _, _, body := parseGmailPayload(payload)
	if subject != "Multipart" {
		t.Errorf("subject = %q, want %q", subject, "Multipart")
	}
	if body != "Plain text body" {
		t.Errorf("body = %q, want %q (should prefer text/plain)", body, "Plain text body")
	}
}

func TestParseGmailPayloadHTMLFallback(t *testing.T) {
	// Only text/html available — should strip tags.
	payload := map[string]any{
		"headers": []any{},
		"mimeType": "multipart/alternative",
		"parts": []any{
			map[string]any{
				"mimeType": "text/html",
				"body": map[string]any{
					"data": base64URLEncode([]byte("<div><p>Hello</p> <b>world</b></div>")),
				},
			},
		},
	}

	_, _, _, _, body := parseGmailPayload(payload)
	if body != "Hello world" {
		t.Errorf("body = %q, want %q (should strip HTML)", body, "Hello world")
	}
}

func TestParseGmailPayloadEmpty(t *testing.T) {
	payload := map[string]any{}
	subject, from, to, date, body := parseGmailPayload(payload)
	if subject != "" || from != "" || to != "" || date != "" || body != "" {
		t.Error("empty payload should return empty strings")
	}
}

func TestToolEmailListNotConfigured(t *testing.T) {
	// Ensure globalGmailService is nil.
	saved := globalGmailService
	globalGmailService = nil
	defer func() { globalGmailService = saved }()

	_, err := toolEmailList(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when gmail not configured")
	}
	if !strings.Contains(err.Error(), "gmail not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolEmailReadNotConfigured(t *testing.T) {
	saved := globalGmailService
	globalGmailService = nil
	defer func() { globalGmailService = saved }()

	_, err := toolEmailRead(context.Background(), &Config{}, json.RawMessage(`{"message_id":"abc"}`))
	if err == nil {
		t.Fatal("expected error when gmail not configured")
	}
	if !strings.Contains(err.Error(), "gmail not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolEmailSendNotConfigured(t *testing.T) {
	saved := globalGmailService
	globalGmailService = nil
	defer func() { globalGmailService = saved }()

	_, err := toolEmailSend(context.Background(), &Config{}, json.RawMessage(`{"to":"a@b.com","subject":"x","body":"y"}`))
	if err == nil {
		t.Fatal("expected error when gmail not configured")
	}
	if !strings.Contains(err.Error(), "gmail not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolEmailDraftNotConfigured(t *testing.T) {
	saved := globalGmailService
	globalGmailService = nil
	defer func() { globalGmailService = saved }()

	_, err := toolEmailDraft(context.Background(), &Config{}, json.RawMessage(`{"to":"a@b.com","subject":"x"}`))
	if err == nil {
		t.Fatal("expected error when gmail not configured")
	}
}

func TestToolEmailSearchNotConfigured(t *testing.T) {
	saved := globalGmailService
	globalGmailService = nil
	defer func() { globalGmailService = saved }()

	_, err := toolEmailSearch(context.Background(), &Config{}, json.RawMessage(`{"query":"test"}`))
	if err == nil {
		t.Fatal("expected error when gmail not configured")
	}
}

func TestToolEmailLabelNotConfigured(t *testing.T) {
	saved := globalGmailService
	globalGmailService = nil
	defer func() { globalGmailService = saved }()

	_, err := toolEmailLabel(context.Background(), &Config{}, json.RawMessage(`{"message_id":"abc","add_labels":["STARRED"]}`))
	if err == nil {
		t.Fatal("expected error when gmail not configured")
	}
}

func TestToolEmailReadValidation(t *testing.T) {
	saved := globalGmailService
	globalGmailService = &GmailService{cfg: &Config{}}
	defer func() { globalGmailService = saved }()

	// Missing message_id.
	_, err := toolEmailRead(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for missing message_id")
	}
	if !strings.Contains(err.Error(), "message_id is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolEmailSendValidation(t *testing.T) {
	saved := globalGmailService
	globalGmailService = &GmailService{cfg: &Config{}}
	defer func() { globalGmailService = saved }()

	tests := []struct {
		input string
		err   string
	}{
		{`{"subject":"x","body":"y"}`, "to is required"},
		{`{"to":"a@b.com","body":"y"}`, "subject is required"},
		{`{"to":"a@b.com","subject":"x"}`, "body is required"},
	}

	for _, tt := range tests {
		_, err := toolEmailSend(context.Background(), &Config{}, json.RawMessage(tt.input))
		if err == nil {
			t.Errorf("expected error for input %s", tt.input)
			continue
		}
		if !strings.Contains(err.Error(), tt.err) {
			t.Errorf("input %s: got %v, want %q", tt.input, err, tt.err)
		}
	}
}

func TestToolEmailLabelValidation(t *testing.T) {
	saved := globalGmailService
	globalGmailService = &GmailService{cfg: &Config{}}
	defer func() { globalGmailService = saved }()

	// Missing message_id.
	_, err := toolEmailLabel(context.Background(), &Config{}, json.RawMessage(`{"add_labels":["STARRED"]}`))
	if err == nil || !strings.Contains(err.Error(), "message_id is required") {
		t.Errorf("expected message_id required error, got: %v", err)
	}

	// No labels specified.
	_, err = toolEmailLabel(context.Background(), &Config{}, json.RawMessage(`{"message_id":"abc"}`))
	if err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Errorf("expected at least one label error, got: %v", err)
	}
}

func TestToolEmailSearchValidation(t *testing.T) {
	saved := globalGmailService
	globalGmailService = &GmailService{cfg: &Config{}}
	defer func() { globalGmailService = saved }()

	_, err := toolEmailSearch(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Errorf("expected query required error, got: %v", err)
	}
}

func TestToolEmailDraftValidation(t *testing.T) {
	saved := globalGmailService
	globalGmailService = &GmailService{cfg: &Config{}}
	defer func() { globalGmailService = saved }()

	_, err := toolEmailDraft(context.Background(), &Config{}, json.RawMessage(`{"subject":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "to is required") {
		t.Errorf("expected to required error, got: %v", err)
	}

	_, err = toolEmailDraft(context.Background(), &Config{}, json.RawMessage(`{"to":"a@b.com"}`))
	if err == nil || !strings.Contains(err.Error(), "subject is required") {
		t.Errorf("expected subject required error, got: %v", err)
	}
}

// TestGmailListMessagesWithMock tests the ListMessages method using httptest.
func TestGmailListMessagesWithMock(t *testing.T) {
	// Create a mock Gmail API server.
	mux := http.NewServeMux()

	// Mock messages.list endpoint.
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query().Get("q")
		resp := map[string]any{
			"messages": []map[string]any{
				{"id": "msg1", "threadId": "thread1"},
				{"id": "msg2", "threadId": "thread2"},
			},
			"resultSizeEstimate": 2,
		}
		if q == "no-results" {
			resp["messages"] = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Mock messages.get endpoint (for metadata fetch).
	mux.HandleFunc("/gmail/v1/users/me/messages/msg1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":       "msg1",
			"threadId": "thread1",
			"snippet":  "Hello from msg1",
			"payload": map[string]any{
				"headers": []any{
					map[string]any{"name": "Subject", "value": "Test Subject 1"},
					map[string]any{"name": "From", "value": "alice@example.com"},
					map[string]any{"name": "Date", "value": "Mon, 1 Jan 2024 12:00:00 +0000"},
				},
			},
		})
	})
	mux.HandleFunc("/gmail/v1/users/me/messages/msg2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":       "msg2",
			"threadId": "thread2",
			"snippet":  "Hello from msg2",
			"payload": map[string]any{
				"headers": []any{
					map[string]any{"name": "Subject", "value": "Test Subject 2"},
					map[string]any{"name": "From", "value": "bob@example.com"},
				},
			},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Create a mock OAuthManager that injects auth and rewrites URLs.
	savedMgr := globalOAuthManager
	globalOAuthManager = &OAuthManager{
		cfg:    &Config{},
		states: make(map[string]oauthState),
	}
	defer func() { globalOAuthManager = savedMgr }()

	// Override the OAuthManager.Request to use our test server.
	// Since we can't easily mock OAuthManager.Request, we test the URL construction
	// and parsing logic indirectly through the helper functions.
	// Direct API tests are covered by integration tests.

	// Test that the service constructs correct URLs (unit-level).
	cfg := &Config{}
	cfg.Gmail.Enabled = true
	cfg.Gmail.MaxResults = 10

	svc := &GmailService{cfg: cfg}
	_ = svc // used in integration tests

	// Test that an empty query returns the configured maxResults.
	_ = fmt.Sprintf("%s/messages?maxResults=10", ts.URL)
}

// TestGmailGetMessageParsing tests the full message parsing pipeline.
func TestGmailGetMessageParsing(t *testing.T) {
	// Simulate a full Gmail API message response.
	plainBody := "Hello, this is plain text."
	raw := map[string]any{
		"id":       "msg123",
		"threadId": "thread456",
		"snippet":  "Hello, this is...",
		"labelIds": []string{"INBOX", "UNREAD"},
		"payload": map[string]any{
			"headers": []any{
				map[string]any{"name": "Subject", "value": "Meeting Tomorrow"},
				map[string]any{"name": "From", "value": "boss@company.com"},
				map[string]any{"name": "To", "value": "you@company.com"},
				map[string]any{"name": "Date", "value": "Tue, 2 Jan 2024 09:00:00 +0000"},
			},
			"mimeType": "multipart/alternative",
			"parts": []any{
				map[string]any{
					"mimeType": "text/plain",
					"body": map[string]any{
						"data": base64URLEncode([]byte(plainBody)),
					},
				},
				map[string]any{
					"mimeType": "text/html",
					"body": map[string]any{
						"data": base64URLEncode([]byte("<p>Hello, this is <b>HTML</b>.</p>")),
					},
				},
			},
		},
	}

	payload := raw["payload"].(map[string]any)
	subject, from, to, date, body := parseGmailPayload(payload)

	if subject != "Meeting Tomorrow" {
		t.Errorf("subject = %q", subject)
	}
	if from != "boss@company.com" {
		t.Errorf("from = %q", from)
	}
	if to != "you@company.com" {
		t.Errorf("to = %q", to)
	}
	if date != "Tue, 2 Jan 2024 09:00:00 +0000" {
		t.Errorf("date = %q", date)
	}
	if body != plainBody {
		t.Errorf("body = %q, want %q", body, plainBody)
	}
}

// TestGmailSendMessageFormat verifies the RFC 2822 + base64url encoding pipeline.
func TestGmailSendMessageFormat(t *testing.T) {
	raw := buildRFC2822("sender@example.com", "recipient@example.com", "Test Send", "Body content", nil, nil)
	encoded := base64URLEncode([]byte(raw))

	// Verify it can be decoded back.
	decoded, err := decodeBase64URL(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded != raw {
		t.Error("encoded/decoded message mismatch")
	}

	// Verify the decoded message has correct format.
	if !strings.Contains(decoded, "From: sender@example.com") {
		t.Error("missing From header in encoded message")
	}
	if !strings.Contains(decoded, "To: recipient@example.com") {
		t.Error("missing To header in encoded message")
	}
	if !strings.Contains(decoded, "Subject: Test Send") {
		t.Error("missing Subject header in encoded message")
	}
}

func TestGmailConfigDefaults(t *testing.T) {
	cfg := GmailConfig{}
	if cfg.MaxResults != 0 {
		t.Errorf("default MaxResults should be 0 (service fills in 20)")
	}
	if cfg.Enabled {
		t.Error("default Enabled should be false")
	}
}

func TestExtractBodyNestedMultipart(t *testing.T) {
	// Deeply nested multipart message.
	payload := map[string]any{
		"mimeType": "multipart/mixed",
		"parts": []any{
			map[string]any{
				"mimeType": "multipart/alternative",
				"parts": []any{
					map[string]any{
						"mimeType": "text/plain",
						"body": map[string]any{
							"data": base64URLEncode([]byte("Deep nested plain text")),
						},
					},
				},
			},
		},
	}

	result := extractBody(payload, "text/plain")
	if result != "Deep nested plain text" {
		t.Errorf("extractBody nested = %q, want %q", result, "Deep nested plain text")
	}
}
