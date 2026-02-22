package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- P13.4: Image Analysis Tests ---

// makePNGBytes returns minimal valid PNG file bytes.
func makePNGBytes() []byte {
	// Minimal 1x1 white PNG (smallest valid PNG).
	pngData := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, // 8-bit RGB
		0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, 0x54, // IDAT chunk
		0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00,
		0x00, 0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc, 0x33,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, // IEND chunk
		0xae, 0x42, 0x60, 0x82,
	}
	return pngData
}

// makeJPEGBytes returns minimal valid JPEG file bytes.
func makeJPEGBytes() []byte {
	// Minimal JPEG (SOI + APP0 + minimal content + EOI).
	return []byte{
		0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46,
		0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01,
		0x00, 0x01, 0x00, 0x00, 0xff, 0xd9,
	}
}

func TestVisionDetectMediaType(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"PNG", makePNGBytes(), "image/png"},
		{"JPEG", makeJPEGBytes(), "image/jpeg"},
		{"GIF87a", append([]byte("GIF87a"), make([]byte, 10)...), "image/gif"},
		{"GIF89a", append([]byte("GIF89a"), make([]byte, 10)...), "image/gif"},
		{"WebP", append(append([]byte("RIFF"), make([]byte, 4)...), []byte("WEBP")...), "image/webp"},
		{"unknown", []byte("not an image at all!"), ""},
		{"too short", []byte("abc"), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectMediaType(tt.data)
			if got != tt.want {
				t.Errorf("detectMediaType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVisionIsBase64Image(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"data URI", "data:image/png;base64,iVBOR...", true},
		{"HTTP URL", "https://example.com/image.png", false},
		{"plain base64", strings.Repeat("ABCD", 30), true},
		{"short string", "abc", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBase64Image(tt.input)
			if got != tt.want {
				t.Errorf("isBase64Image(%q...) = %v, want %v", tt.input[:min(len(tt.input), 20)], got, tt.want)
			}
		})
	}
}

func TestVisionParseBase64Image(t *testing.T) {
	// Test with data URI.
	pngData := makePNGBytes()
	b64 := base64.StdEncoding.EncodeToString(pngData)
	dataURI := "data:image/png;base64," + b64

	data, mediaType, err := parseBase64Image(dataURI)
	if err != nil {
		t.Fatalf("parseBase64Image(data URI) error: %v", err)
	}
	if mediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png", mediaType)
	}
	if len(data) != len(pngData) {
		t.Errorf("data length = %d, want %d", len(data), len(pngData))
	}

	// Test with raw base64 (auto-detect from bytes).
	data2, mediaType2, err := parseBase64Image(b64)
	if err != nil {
		t.Fatalf("parseBase64Image(raw base64) error: %v", err)
	}
	if mediaType2 != "image/png" {
		t.Errorf("mediaType = %q, want image/png", mediaType2)
	}
	if len(data2) != len(pngData) {
		t.Errorf("data length = %d, want %d", len(data2), len(pngData))
	}

	// Test invalid base64.
	_, _, err = parseBase64Image("data:image/png;base64,!!!invalid!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}

	// Test invalid data URI format.
	_, _, err = parseBase64Image("data:nope")
	if err == nil {
		t.Error("expected error for invalid data URI")
	}
}

func TestVisionFetchImage(t *testing.T) {
	pngData := makePNGBytes()

	// Mock HTTP server serving a PNG image.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngData)
	}))
	defer srv.Close()

	ctx := context.Background()
	data, mediaType, err := fetchImage(ctx, srv.URL+"/image.png", defaultMaxImageSize)
	if err != nil {
		t.Fatalf("fetchImage error: %v", err)
	}
	if mediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png", mediaType)
	}
	if len(data) != len(pngData) {
		t.Errorf("data length = %d, want %d", len(data), len(pngData))
	}
}

func TestVisionFetchImageOversize(t *testing.T) {
	// Mock HTTP server serving a large image.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		// Write more than maxSize bytes.
		w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	ctx := context.Background()
	_, _, err := fetchImage(ctx, srv.URL+"/large.jpg", 100) // 100 byte limit
	if err == nil {
		t.Error("expected error for oversize image")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %q, want contains 'too large'", err.Error())
	}
}

func TestVisionFetchImageNetworkError(t *testing.T) {
	ctx := context.Background()
	_, _, err := fetchImage(ctx, "http://127.0.0.1:1/nonexistent", defaultMaxImageSize)
	if err == nil {
		t.Error("expected error for network failure")
	}
}

func TestVisionFetchImageUnsupportedFormat(t *testing.T) {
	// Mock HTTP server serving non-image data.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("not an image at all, just plain text content here"))
	}))
	defer srv.Close()

	ctx := context.Background()
	_, _, err := fetchImage(ctx, srv.URL+"/text.txt", defaultMaxImageSize)
	if err == nil {
		t.Error("expected error for unsupported format")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error = %q, want contains 'unsupported'", err.Error())
	}
}

func TestVisionAnthropicProvider(t *testing.T) {
	// Mock Anthropic API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request format.
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key = %q, want test-key", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("anthropic-version = %q, want 2023-06-01", r.Header.Get("anthropic-version"))
		}

		// Validate request body structure.
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		messages, ok := reqBody["messages"].([]any)
		if !ok || len(messages) == 0 {
			t.Error("expected messages array in request")
		}
		msg := messages[0].(map[string]any)
		content := msg["content"].([]any)
		if len(content) != 2 {
			t.Errorf("expected 2 content blocks, got %d", len(content))
		}
		// First block: image.
		imgBlock := content[0].(map[string]any)
		if imgBlock["type"] != "image" {
			t.Errorf("first block type = %q, want image", imgBlock["type"])
		}
		source := imgBlock["source"].(map[string]any)
		if source["type"] != "base64" {
			t.Errorf("source type = %q, want base64", source["type"])
		}

		// Return mock response.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "A 1x1 white pixel image."},
			},
		})
	}))
	defer srv.Close()

	provider := &anthropicVision{}
	cfg := &VisionConfig{
		APIKey:  "test-key",
		Model:   "claude-sonnet-4-5-20250929",
		BaseURL: srv.URL,
	}
	ctx := context.Background()

	result, err := provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "Describe this", "auto")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if !strings.Contains(result, "1x1") {
		t.Errorf("result = %q, want contains '1x1'", result)
	}
}

func TestVisionOpenAIProvider(t *testing.T) {
	// Mock OpenAI API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("expected Bearer authorization header")
		}

		// Validate request body structure.
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		messages := reqBody["messages"].([]any)
		msg := messages[0].(map[string]any)
		content := msg["content"].([]any)
		if len(content) != 2 {
			t.Errorf("expected 2 content blocks, got %d", len(content))
		}
		// Second block: image_url.
		imgBlock := content[1].(map[string]any)
		if imgBlock["type"] != "image_url" {
			t.Errorf("second block type = %q, want image_url", imgBlock["type"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "A small white pixel."}},
			},
		})
	}))
	defer srv.Close()

	provider := &openaiVision{}
	cfg := &VisionConfig{
		APIKey:  "test-key",
		Model:   "gpt-4o",
		BaseURL: srv.URL,
	}
	ctx := context.Background()

	result, err := provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "Describe this", "high")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if !strings.Contains(result, "pixel") {
		t.Errorf("result = %q, want contains 'pixel'", result)
	}
}

func TestVisionGoogleProvider(t *testing.T) {
	// Mock Google Gemini API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "generateContent") {
			t.Errorf("path = %q, want contains 'generateContent'", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("key = %q, want test-key", r.URL.Query().Get("key"))
		}

		// Validate request body structure.
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		contents := reqBody["contents"].([]any)
		content := contents[0].(map[string]any)
		parts := content["parts"].([]any)
		if len(parts) != 2 {
			t.Errorf("expected 2 parts, got %d", len(parts))
		}
		// First part: inlineData.
		imgPart := parts[0].(map[string]any)
		if _, ok := imgPart["inlineData"]; !ok {
			t.Error("expected inlineData in first part")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]any{
						{"text": "A white pixel image."},
					},
				}},
			},
		})
	}))
	defer srv.Close()

	provider := &googleVision{}
	cfg := &VisionConfig{
		APIKey:  "test-key",
		Model:   "gemini-2.0-flash",
		BaseURL: srv.URL,
	}
	ctx := context.Background()

	result, err := provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "Describe this", "auto")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if !strings.Contains(result, "pixel") {
		t.Errorf("result = %q, want contains 'pixel'", result)
	}
}

func TestVisionToolHandler(t *testing.T) {
	// Mock Anthropic API for full handler test.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "This is a test image."},
			},
		})
	}))
	defer srv.Close()

	// Create an image server for URL-based fetch.
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(makePNGBytes())
	}))
	defer imgSrv.Close()

	cfg := &Config{
		Tools: ToolConfig{
			Vision: VisionConfig{
				Provider: "anthropic",
				APIKey:   "test-key",
				BaseURL:  srv.URL,
			},
		},
	}
	ctx := context.Background()

	// Test with URL input.
	input := json.RawMessage(fmt.Sprintf(`{"image": "%s/photo.png", "prompt": "Describe this image"}`, imgSrv.URL))
	result, err := toolImageAnalyze(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolImageAnalyze error: %v", err)
	}
	if !strings.Contains(result, "test image") {
		t.Errorf("result = %q, want contains 'test image'", result)
	}

	// Test with base64 input.
	b64 := base64.StdEncoding.EncodeToString(makePNGBytes())
	input = json.RawMessage(fmt.Sprintf(`{"image": "data:image/png;base64,%s", "prompt": "Describe"}`, b64))
	result, err = toolImageAnalyze(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolImageAnalyze base64 error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result for base64 input")
	}
}

func TestVisionToolHandlerErrors(t *testing.T) {
	ctx := context.Background()

	// Test missing image.
	cfg := &Config{
		Tools: ToolConfig{
			Vision: VisionConfig{Provider: "anthropic", APIKey: "test"},
		},
	}
	_, err := toolImageAnalyze(ctx, cfg, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "image is required") {
		t.Errorf("expected 'image is required' error, got %v", err)
	}

	// Test invalid detail.
	_, err = toolImageAnalyze(ctx, cfg, json.RawMessage(`{"image": "https://x.com/a.png", "detail": "super"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid detail") {
		t.Errorf("expected 'invalid detail' error, got %v", err)
	}

	// Test unconfigured provider.
	cfg2 := &Config{Tools: ToolConfig{}}
	_, err = toolImageAnalyze(ctx, cfg2, json.RawMessage(`{"image": "https://x.com/a.png"}`))
	if err == nil || !strings.Contains(err.Error(), "vision not configured") {
		t.Errorf("expected 'vision not configured' error, got %v", err)
	}
}

func TestVisionOversizeImageRejection(t *testing.T) {
	ctx := context.Background()

	// Create config with very small max size.
	cfg := &Config{
		Tools: ToolConfig{
			Vision: VisionConfig{
				Provider:     "anthropic",
				APIKey:       "test-key",
				MaxImageSize: 10, // 10 bytes limit
			},
		},
	}

	// Test with base64 image that exceeds limit.
	pngData := makePNGBytes() // > 10 bytes
	b64 := base64.StdEncoding.EncodeToString(pngData)
	input := json.RawMessage(fmt.Sprintf(`{"image": "data:image/png;base64,%s"}`, b64))

	_, err := toolImageAnalyze(ctx, cfg, input)
	if err == nil {
		t.Error("expected error for oversize image")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %q, want contains 'too large'", err.Error())
	}
}

func TestVisionConfigValidation(t *testing.T) {
	// Test default max image size.
	cfg := &VisionConfig{}
	if cfg.MaxImageSize != 0 {
		t.Errorf("default MaxImageSize = %d, want 0 (to be resolved to defaultMaxImageSize)", cfg.MaxImageSize)
	}

	// Test provider resolution.
	testCases := []struct {
		provider string
		wantNil  bool
	}{
		{"anthropic", false},
		{"openai", false},
		{"google", false},
		{"", true},
		{"unknown", true},
	}

	for _, tc := range testCases {
		cfg := &Config{Tools: ToolConfig{Vision: VisionConfig{Provider: tc.provider}}}
		p := resolveVisionProvider(cfg)
		if tc.wantNil && p != nil {
			t.Errorf("resolveVisionProvider(%q) = %v, want nil", tc.provider, p)
		}
		if !tc.wantNil && p == nil {
			t.Errorf("resolveVisionProvider(%q) = nil, want non-nil", tc.provider)
		}
	}
}

func TestVisionProviderAPIError(t *testing.T) {
	// Mock server that returns an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	providers := []struct {
		name     string
		provider visionProvider
	}{
		{"anthropic", &anthropicVision{}},
		{"openai", &openaiVision{}},
		{"google", &googleVision{}},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			cfg := &VisionConfig{
				APIKey:  "test-key",
				BaseURL: srv.URL,
			}
			_, err := p.provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "test", "auto")
			if err == nil {
				t.Error("expected error for API error response")
			}
			if !strings.Contains(err.Error(), "500") {
				t.Errorf("error = %q, want contains '500'", err.Error())
			}
		})
	}
}

func TestVisionProviderEmptyResponse(t *testing.T) {
	ctx := context.Background()

	// Anthropic: empty content array.
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"content": []any{}})
	}))
	defer srv1.Close()
	p1 := &anthropicVision{}
	_, err := p1.Analyze(ctx, &VisionConfig{APIKey: "k", BaseURL: srv1.URL}, makePNGBytes(), "image/png", "test", "auto")
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Errorf("anthropic: expected 'empty response' error, got %v", err)
	}

	// OpenAI: empty choices.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer srv2.Close()
	p2 := &openaiVision{}
	_, err = p2.Analyze(ctx, &VisionConfig{APIKey: "k", BaseURL: srv2.URL}, makePNGBytes(), "image/png", "test", "auto")
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Errorf("openai: expected 'empty response' error, got %v", err)
	}

	// Google: empty candidates.
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{}})
	}))
	defer srv3.Close()
	p3 := &googleVision{}
	_, err = p3.Analyze(ctx, &VisionConfig{APIKey: "k", BaseURL: srv3.URL}, makePNGBytes(), "image/png", "test", "auto")
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Errorf("google: expected 'empty response' error, got %v", err)
	}
}

func TestVisionProviderMissingAPIKey(t *testing.T) {
	ctx := context.Background()
	providers := []struct {
		name     string
		provider visionProvider
	}{
		{"anthropic", &anthropicVision{}},
		{"openai", &openaiVision{}},
		{"google", &googleVision{}},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			cfg := &VisionConfig{APIKey: ""}
			_, err := p.provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "test", "auto")
			if err == nil || !strings.Contains(err.Error(), "requires apiKey") {
				t.Errorf("expected 'requires apiKey' error, got %v", err)
			}
		})
	}
}

