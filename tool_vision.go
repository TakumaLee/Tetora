package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// --- P13.4: Image Analysis ---

// visionInput is the input schema for the image_analyze tool.
type visionInput struct {
	Image  string `json:"image"`            // URL or base64 data
	Prompt string `json:"prompt"`           // analysis prompt
	Detail string `json:"detail,omitempty"` // "low", "high", "auto"
}

// visionProvider abstracts different Vision API backends.
type visionProvider interface {
	// Name returns the provider identifier.
	Name() string
	// Analyze sends an image to the vision API and returns a text description.
	Analyze(ctx context.Context, cfg *VisionConfig, imageData []byte, mediaType, prompt, detail string) (string, error)
}

// detectMediaType detects image format from first bytes.
// Supported formats: JPEG, PNG, GIF, WebP.
func detectMediaType(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	// JPEG
	if data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	// PNG
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4e && data[3] == 0x47 {
		return "image/png"
	}
	// GIF
	if string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a" {
		return "image/gif"
	}
	// WebP (RIFF....WEBP)
	if string(data[:4]) == "RIFF" && len(data) >= 12 && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

// isBase64Image checks if the input string is base64-encoded image data.
// Supports both raw base64 and data URI format (data:image/...;base64,...).
func isBase64Image(s string) bool {
	if strings.HasPrefix(s, "data:image/") {
		return true
	}
	// Check if it looks like raw base64 (no URL-like prefix).
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	// Try to detect if it's base64 by checking for valid base64 chars.
	if len(s) > 100 {
		sample := s[:100]
		for _, c := range sample {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
				return false
			}
		}
		return true
	}
	return false
}

// parseBase64Image extracts raw bytes and media type from a base64 string.
func parseBase64Image(s string) ([]byte, string, error) {
	var b64Data string
	var mediaType string

	if strings.HasPrefix(s, "data:") {
		// Parse data URI: data:image/jpeg;base64,/9j/4AAQ...
		idx := strings.Index(s, ",")
		if idx < 0 {
			return nil, "", fmt.Errorf("invalid data URI format")
		}
		header := s[:idx]
		b64Data = s[idx+1:]
		// Extract media type from header: data:image/jpeg;base64
		header = strings.TrimPrefix(header, "data:")
		semiIdx := strings.Index(header, ";")
		if semiIdx > 0 {
			mediaType = header[:semiIdx]
		} else {
			mediaType = header
		}
	} else {
		b64Data = s
	}

	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		// Try URL-safe base64.
		data, err = base64.URLEncoding.DecodeString(b64Data)
		if err != nil {
			return nil, "", fmt.Errorf("invalid base64 data: %w", err)
		}
	}

	// If media type not from data URI, detect from bytes.
	if mediaType == "" {
		mediaType = detectMediaType(data)
		if mediaType == "" {
			return nil, "", fmt.Errorf("unsupported image format")
		}
	}

	return data, mediaType, nil
}

// fetchImage downloads an image from a URL and returns the bytes.
func fetchImage(ctx context.Context, url string, maxSize int) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetch image: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	// Read with size limit.
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxSize)+1))
	if err != nil {
		return nil, "", fmt.Errorf("read image: %w", err)
	}
	if len(data) > maxSize {
		return nil, "", fmt.Errorf("image too large: %d bytes exceeds limit of %d bytes", len(data), maxSize)
	}

	// Detect media type from response header or bytes.
	mediaType := ""
	ct := resp.Header.Get("Content-Type")
	if ct != "" {
		// Extract media type from content-type header (e.g. "image/jpeg; charset=utf-8").
		parts := strings.SplitN(ct, ";", 2)
		mt := strings.TrimSpace(parts[0])
		switch mt {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
			mediaType = mt
		}
	}
	if mediaType == "" {
		mediaType = detectMediaType(data)
	}
	if mediaType == "" {
		return nil, "", fmt.Errorf("unsupported image format from URL")
	}

	return data, mediaType, nil
}

// defaultMaxImageSize is 5MB.
const defaultMaxImageSize = 5 * 1024 * 1024

// toolImageAnalyze is the handler for the image_analyze tool.
func toolImageAnalyze(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args visionInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Image == "" {
		return "", fmt.Errorf("image is required")
	}
	if args.Prompt == "" {
		args.Prompt = "Describe what you see in this image."
	}
	if args.Detail == "" {
		args.Detail = "auto"
	}

	// Validate detail parameter.
	switch args.Detail {
	case "low", "high", "auto":
		// ok
	default:
		return "", fmt.Errorf("invalid detail level %q: must be low, high, or auto", args.Detail)
	}

	// Determine provider early to fail fast if not configured.
	provider := resolveVisionProvider(cfg)
	if provider == nil {
		return "", fmt.Errorf("vision not configured (set tools.vision.provider in config)")
	}

	// Determine max image size.
	maxSize := cfg.Tools.Vision.MaxImageSize
	if maxSize <= 0 {
		maxSize = defaultMaxImageSize
	}

	// Get image data.
	var imageData []byte
	var mediaType string
	var err error

	if isBase64Image(args.Image) {
		imageData, mediaType, err = parseBase64Image(args.Image)
		if err != nil {
			return "", fmt.Errorf("parse base64 image: %w", err)
		}
		if len(imageData) > maxSize {
			return "", fmt.Errorf("image too large: %d bytes exceeds limit of %d bytes", len(imageData), maxSize)
		}
	} else {
		// Treat as URL.
		imageData, mediaType, err = fetchImage(ctx, args.Image, maxSize)
		if err != nil {
			return "", err
		}
	}

	// Validate media type is supported.
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		// ok
	default:
		return "", fmt.Errorf("unsupported image format: %s (supported: jpeg, png, gif, webp)", mediaType)
	}

	// Call the vision API.
	result, err := provider.Analyze(ctx, &cfg.Tools.Vision, imageData, mediaType, args.Prompt, args.Detail)
	if err != nil {
		return "", fmt.Errorf("vision analysis failed: %w", err)
	}

	return result, nil
}

// resolveVisionProvider returns the appropriate vision provider based on config.
func resolveVisionProvider(cfg *Config) visionProvider {
	switch cfg.Tools.Vision.Provider {
	case "anthropic":
		return &anthropicVision{}
	case "openai":
		return &openaiVision{}
	case "google":
		return &googleVision{}
	default:
		return nil
	}
}

// --- Anthropic Vision Provider ---

type anthropicVision struct{}

func (a *anthropicVision) Name() string { return "anthropic" }

func (a *anthropicVision) Analyze(ctx context.Context, cfg *VisionConfig, imageData []byte, mediaType, prompt, detail string) (string, error) {
	if cfg.APIKey == "" {
		return "", fmt.Errorf("anthropic vision requires apiKey in tools.vision")
	}

	model := cfg.Model
	if model == "" {
		model = "claude-sonnet-4-5-20250929"
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	// Build Anthropic Messages API request.
	b64Data := base64.StdEncoding.EncodeToString(imageData)
	reqBody := map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": mediaType,
							"data":       b64Data,
						},
					},
					{
						"type": "text",
						"text": prompt,
					},
				},
			},
		},
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(reqJSON)))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic api error: %d %s", resp.StatusCode, string(body))
	}

	// Parse response.
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	// Collect text content.
	var texts []string
	for _, c := range result.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	if len(texts) == 0 {
		return "", fmt.Errorf("empty response from anthropic vision")
	}

	return strings.Join(texts, "\n"), nil
}

// --- OpenAI Vision Provider ---

type openaiVision struct{}

func (o *openaiVision) Name() string { return "openai" }

func (o *openaiVision) Analyze(ctx context.Context, cfg *VisionConfig, imageData []byte, mediaType, prompt, detail string) (string, error) {
	if cfg.APIKey == "" {
		return "", fmt.Errorf("openai vision requires apiKey in tools.vision")
	}

	model := cfg.Model
	if model == "" {
		model = "gpt-4o"
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	// Build data URI for OpenAI.
	b64Data := base64.StdEncoding.EncodeToString(imageData)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mediaType, b64Data)

	// Build OpenAI Chat Completions request with image_url content part.
	reqBody := map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": prompt,
					},
					{
						"type": "image_url",
						"image_url": map[string]any{
							"url":    dataURI,
							"detail": detail,
						},
					},
				},
			},
		},
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := baseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(reqJSON)))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai api error: %d %s", resp.StatusCode, string(body))
	}

	// Parse response.
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty response from openai vision")
	}

	return result.Choices[0].Message.Content, nil
}

// --- Google Vision Provider ---

type googleVision struct{}

func (g *googleVision) Name() string { return "google" }

func (g *googleVision) Analyze(ctx context.Context, cfg *VisionConfig, imageData []byte, mediaType, prompt, detail string) (string, error) {
	if cfg.APIKey == "" {
		return "", fmt.Errorf("google vision requires apiKey in tools.vision")
	}

	model := cfg.Model
	if model == "" {
		model = "gemini-2.0-flash"
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}

	// Build Gemini generateContent request.
	b64Data := base64.StdEncoding.EncodeToString(imageData)
	reqBody := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]any{
					{
						"inlineData": map[string]any{
							"mimeType": mediaType,
							"data":     b64Data,
						},
					},
					{
						"text": prompt,
					},
				},
			},
		},
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/models/%s:generateContent?key=%s", baseURL, model, cfg.APIKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(reqJSON)))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google api error: %d %s", resp.StatusCode, string(body))
	}

	// Parse Gemini response.
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(result.Candidates) == 0 {
		return "", fmt.Errorf("empty response from google vision")
	}

	var texts []string
	for _, part := range result.Candidates[0].Content.Parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	if len(texts) == 0 {
		return "", fmt.Errorf("empty text in google vision response")
	}

	return strings.Join(texts, "\n"), nil
}
