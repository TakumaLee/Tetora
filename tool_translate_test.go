package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolTranslateLingva(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify path contains source/target/text.
		if !strings.Contains(r.URL.Path, "/api/v1/en/ja/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"translation": "こんにちは世界",
		})
	}))
	defer srv.Close()

	origURL := lingvaBaseURL
	lingvaBaseURL = srv.URL
	defer func() { lingvaBaseURL = origURL }()

	cfg := &Config{Translate: TranslateConfig{Provider: "lingva"}}
	input, _ := json.Marshal(map[string]any{"text": "Hello world", "from": "en", "to": "ja"})
	result, err := toolTranslate(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "こんにちは世界") {
		t.Errorf("expected translation in result, got: %s", result)
	}
	if !strings.Contains(result, "[en -> ja]") {
		t.Errorf("expected language pair, got: %s", result)
	}
}

func TestToolTranslateLingvaAutoDetect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/v1/auto/en/") {
			t.Errorf("expected auto detect, got path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"translation": "Hello",
		})
	}))
	defer srv.Close()

	origURL := lingvaBaseURL
	lingvaBaseURL = srv.URL
	defer func() { lingvaBaseURL = origURL }()

	cfg := &Config{} // Default provider = lingva.
	input, _ := json.Marshal(map[string]any{"text": "Bonjour", "to": "en"})
	result, err := toolTranslate(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Hello") {
		t.Errorf("expected translated text, got: %s", result)
	}
}

func TestToolTranslateDeepL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		auth := r.Header.Get("Authorization")
		if auth != "DeepL-Auth-Key test-key-123" {
			t.Errorf("unexpected auth header: %s", auth)
		}
		r.ParseForm()
		if r.Form.Get("target_lang") != "JA" {
			t.Errorf("unexpected target_lang: %s", r.Form.Get("target_lang"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"translations": []map[string]any{
				{
					"detected_source_language": "EN",
					"text":                     "こんにちは",
				},
			},
		})
	}))
	defer srv.Close()

	origURL := deeplBaseURL
	deeplBaseURL = srv.URL
	defer func() { deeplBaseURL = origURL }()

	cfg := &Config{Translate: TranslateConfig{Provider: "deepl", APIKey: "test-key-123"}}
	input, _ := json.Marshal(map[string]any{"text": "Hello", "to": "ja"})
	result, err := toolTranslate(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "こんにちは") {
		t.Errorf("expected translation, got: %s", result)
	}
	if !strings.Contains(result, "[en -> ja]") {
		t.Errorf("expected language pair, got: %s", result)
	}
}

func TestToolTranslateDeepLMissingKey(t *testing.T) {
	cfg := &Config{Translate: TranslateConfig{Provider: "deepl"}}
	input, _ := json.Marshal(map[string]any{"text": "Hello", "to": "ja"})
	_, err := toolTranslate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "API key required") {
		t.Errorf("expected API key error, got: %v", err)
	}
}

func TestToolTranslateMissingText(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"to": "en"})
	_, err := toolTranslate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing text")
	}
}

func TestToolTranslateMissingTarget(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"text": "Hello"})
	_, err := toolTranslate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing target")
	}
}

func TestToolTranslateLingvaAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	origURL := lingvaBaseURL
	lingvaBaseURL = srv.URL
	defer func() { lingvaBaseURL = origURL }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"text": "Hello", "to": "ja"})
	_, err := toolTranslate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestToolDetectLanguageHeuristic(t *testing.T) {
	tests := []struct {
		text     string
		expected string
	}{
		{"Hello world", "en"},
		{"こんにちは世界", "ja"},
		{"你好世界", "zh"},
		{"안녕하세요", "ko"},
		{"Привет мир", "ru"},
		{"", "unknown"},
	}

	cfg := &Config{} // No DeepL, use heuristic.
	for _, tt := range tests {
		input, _ := json.Marshal(map[string]any{"text": tt.text})
		result, err := toolDetectLanguage(context.Background(), cfg, input)
		if tt.text == "" {
			if err == nil {
				t.Errorf("expected error for empty text")
			}
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", tt.text, err)
		}
		if !strings.Contains(result, tt.expected) {
			t.Errorf("detectLanguage(%q) = %q, want to contain %q", tt.text, result, tt.expected)
		}
	}
}

func TestToolDetectLanguageDeepL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"translations": []map[string]any{
				{"detected_source_language": "FR"},
			},
		})
	}))
	defer srv.Close()

	origURL := deeplBaseURL
	deeplBaseURL = srv.URL
	defer func() { deeplBaseURL = origURL }()

	cfg := &Config{Translate: TranslateConfig{Provider: "deepl", APIKey: "test-key"}}
	input, _ := json.Marshal(map[string]any{"text": "Bonjour le monde"})
	result, err := toolDetectLanguage(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "fr") {
		t.Errorf("expected 'fr' in result, got: %s", result)
	}
	if !strings.Contains(result, "DeepL") {
		t.Errorf("expected DeepL attribution, got: %s", result)
	}
}

func TestDetectLanguageHeuristicDirect(t *testing.T) {
	// Test the heuristic function directly.
	tests := []struct {
		text string
		lang string
	}{
		{"This is English text.", "en"},
		{"これは日本語のテストです。", "ja"},
		{"카타카나와 히라가나", "ko"},
		{"这是中文测试", "zh"},
		{"Это русский текст", "ru"},
		{"12345", "unknown"},
	}
	for _, tt := range tests {
		got := detectLanguageHeuristic(tt.text)
		if !strings.Contains(got, tt.lang) {
			t.Errorf("detectLanguageHeuristic(%q) = %q, want to contain %q", tt.text, got, tt.lang)
		}
	}
}
