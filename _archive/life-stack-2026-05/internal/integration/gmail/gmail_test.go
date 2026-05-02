package gmail

import (
	"strings"
	"testing"
)

func TestBase64URLEncodeDecodeRoundtrip(t *testing.T) {
	tests := []string{
		"Hello, World!",
		"",
		"This is a longer string with special characters: <>&\"'\n\ttabs and newlines",
		"日本語テスト",
		strings.Repeat("a", 10000),
	}
	for _, input := range tests {
		encoded := Base64URLEncode([]byte(input))
		decoded, err := DecodeBase64URL(encoded)
		if err != nil {
			t.Errorf("DecodeBase64URL(%q) error: %v", encoded, err)
			continue
		}
		if decoded != input {
			t.Errorf("roundtrip failed: got %q, want %q", decoded, input)
		}
	}
}

func TestBase64URLEncodeNopadding(t *testing.T) {
	encoded := Base64URLEncode([]byte("test"))
	if strings.Contains(encoded, "=") {
		t.Errorf("Base64URLEncode should not contain padding, got %q", encoded)
	}
	if strings.ContainsAny(encoded, "+/") {
		t.Errorf("Base64URLEncode should use URL-safe chars, got %q", encoded)
	}
}

func TestDecodeBase64URLVariants(t *testing.T) {
	input := "Hello!"
	encoded := Base64URLEncode([]byte(input))
	decoded, err := DecodeBase64URL(encoded)
	if err != nil {
		t.Fatalf("decode no padding: %v", err)
	}
	if decoded != input {
		t.Errorf("got %q, want %q", decoded, input)
	}
}

func TestBuildRFC2822Basic(t *testing.T) {
	msg := BuildRFC2822("alice@example.com", "bob@example.com", "Hello Bob", "How are you?", nil, nil)
	if !strings.Contains(msg, "MIME-Version: 1.0") {
		t.Error("missing MIME-Version header")
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
	if !strings.Contains(msg, "\r\n\r\nHow are you?") {
		t.Error("body not properly separated from headers")
	}
}

func TestBuildRFC2822WithCcBcc(t *testing.T) {
	msg := BuildRFC2822(
		"alice@example.com", "bob@example.com", "Team meeting", "See you at 3pm",
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
	msg := BuildRFC2822("a@b.com", "c@d.com", "Test", "Body", nil, nil)
	if strings.Contains(msg, "Cc:") {
		t.Error("should not contain Cc header when empty")
	}
	if strings.Contains(msg, "Bcc:") {
		t.Error("should not contain Bcc header when empty")
	}
}

func TestStripHTMLTags(t *testing.T) {
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
	}
	for _, tt := range tests {
		got := StripHTMLTags(tt.input)
		if got != tt.expected {
			t.Errorf("StripHTMLTags(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParsePayload(t *testing.T) {
	payload := map[string]any{
		"headers": []any{
			map[string]any{"name": "Subject", "value": "Test Subject"},
			map[string]any{"name": "From", "value": "alice@example.com"},
			map[string]any{"name": "To", "value": "bob@example.com"},
			map[string]any{"name": "Date", "value": "Mon, 1 Jan 2024 12:00:00 +0000"},
		},
		"mimeType": "text/plain",
		"body": map[string]any{
			"data": Base64URLEncode([]byte("Hello, this is the body.")),
		},
	}

	subject, from, to, date, body := ParsePayload(payload)
	if subject != "Test Subject" {
		t.Errorf("subject = %q, want %q", subject, "Test Subject")
	}
	if from != "alice@example.com" {
		t.Errorf("from = %q", from)
	}
	if to != "bob@example.com" {
		t.Errorf("to = %q", to)
	}
	if date != "Mon, 1 Jan 2024 12:00:00 +0000" {
		t.Errorf("date = %q", date)
	}
	if body != "Hello, this is the body." {
		t.Errorf("body = %q", body)
	}
}

func TestParsePayloadMultipart(t *testing.T) {
	payload := map[string]any{
		"headers": []any{
			map[string]any{"name": "Subject", "value": "Multipart"},
		},
		"mimeType": "multipart/alternative",
		"parts": []any{
			map[string]any{
				"mimeType": "text/plain",
				"body": map[string]any{
					"data": Base64URLEncode([]byte("Plain text body")),
				},
			},
			map[string]any{
				"mimeType": "text/html",
				"body": map[string]any{
					"data": Base64URLEncode([]byte("<p>HTML body</p>")),
				},
			},
		},
	}

	subject, _, _, _, body := ParsePayload(payload)
	if subject != "Multipart" {
		t.Errorf("subject = %q", subject)
	}
	if body != "Plain text body" {
		t.Errorf("body = %q, want plain text", body)
	}
}

func TestParsePayloadHTMLFallback(t *testing.T) {
	payload := map[string]any{
		"headers":  []any{},
		"mimeType": "multipart/alternative",
		"parts": []any{
			map[string]any{
				"mimeType": "text/html",
				"body": map[string]any{
					"data": Base64URLEncode([]byte("<div><p>Hello</p> <b>world</b></div>")),
				},
			},
		},
	}

	_, _, _, _, body := ParsePayload(payload)
	if body != "Hello world" {
		t.Errorf("body = %q, want stripped HTML", body)
	}
}

func TestParsePayloadEmpty(t *testing.T) {
	payload := map[string]any{}
	subject, from, to, date, body := ParsePayload(payload)
	if subject != "" || from != "" || to != "" || date != "" || body != "" {
		t.Error("empty payload should return empty strings")
	}
}

func TestExtractBodyNestedMultipart(t *testing.T) {
	payload := map[string]any{
		"mimeType": "multipart/mixed",
		"parts": []any{
			map[string]any{
				"mimeType": "multipart/alternative",
				"parts": []any{
					map[string]any{
						"mimeType": "text/plain",
						"body": map[string]any{
							"data": Base64URLEncode([]byte("Deep nested plain text")),
						},
					},
				},
			},
		},
	}

	result := ExtractBody(payload, "text/plain")
	if result != "Deep nested plain text" {
		t.Errorf("ExtractBody nested = %q", result)
	}
}

func TestGmailSendMessageFormat(t *testing.T) {
	raw := BuildRFC2822("sender@example.com", "recipient@example.com", "Test Send", "Body content", nil, nil)
	encoded := Base64URLEncode([]byte(raw))
	decoded, err := DecodeBase64URL(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded != raw {
		t.Error("encoded/decoded message mismatch")
	}
	if !strings.Contains(decoded, "From: sender@example.com") {
		t.Error("missing From header")
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	if cfg.MaxResults != 0 {
		t.Errorf("default MaxResults should be 0")
	}
	if cfg.Enabled {
		t.Error("default Enabled should be false")
	}
}
