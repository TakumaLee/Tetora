package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newImageGenTestServer(t *testing.T, authKey string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(405)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+authKey {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "unauthorized"},
			})
			return
		}

		// Decode request body to verify.
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"url":             "https://example.com/generated-image.png",
					"revised_prompt":  "a fluffy orange cat sitting on a windowsill",
				},
			},
		})
	}))
}

func TestToolImageGenerate(t *testing.T) {
	server := newImageGenTestServer(t, "test-key")
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	// Reset limiter.
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 1.00,
			Quality:    "standard",
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "a cat"})
	result, err := toolImageGenerate(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "https://example.com/generated-image.png") {
		t.Errorf("expected URL in result, got: %s", result)
	}
	if !strings.Contains(result, "dall-e-3") {
		t.Errorf("expected model in result, got: %s", result)
	}
	if !strings.Contains(result, "revised_prompt") || !strings.Contains(result, "fluffy orange cat") {
		// The revised prompt should appear in output.
		if !strings.Contains(result, "Revised prompt:") {
			t.Errorf("expected revised prompt in result, got: %s", result)
		}
	}
	if !strings.Contains(result, "$0.040") {
		t.Errorf("expected cost in result, got: %s", result)
	}
}

func TestToolImageGenerateCustomSize(t *testing.T) {
	server := newImageGenTestServer(t, "test-key")
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 5.00,
			Quality:    "hd",
		},
	}

	input, _ := json.Marshal(map[string]any{
		"prompt": "a landscape",
		"size":   "1792x1024",
	})
	result, err := toolImageGenerate(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "1792x1024") {
		t.Errorf("expected size in result, got: %s", result)
	}
	if !strings.Contains(result, "$0.120") {
		t.Errorf("expected HD large cost $0.120, got: %s", result)
	}
}

func TestToolImageGenerateInvalidSize(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]any{
		"prompt": "test",
		"size":   "512x512",
	})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for invalid size")
	}
	if !strings.Contains(err.Error(), "invalid size") {
		t.Errorf("expected invalid size error, got: %v", err)
	}
}

func TestToolImageGenerateEmptyPrompt(t *testing.T) {
	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled: true,
			APIKey:  "test-key",
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": ""})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
	if !strings.Contains(err.Error(), "prompt is required") {
		t.Errorf("expected prompt required error, got: %v", err)
	}
}

func TestToolImageGenerateNoAPIKey(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "apiKey not configured") {
		t.Errorf("expected apiKey error, got: %v", err)
	}
}

func TestToolImageGenerateDailyLimit(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 2,
			MaxCostDay: 10.00,
		},
	}

	// Simulate 2 previous generations.
	globalImageGenLimiter.date = timeNowFormatDate()
	globalImageGenLimiter.count = 2
	globalImageGenLimiter.costUSD = 0.08

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for daily limit")
	}
	if !strings.Contains(err.Error(), "daily limit reached") {
		t.Errorf("expected daily limit error, got: %v", err)
	}
}

func TestToolImageGenerateCostLimit(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 100,
			MaxCostDay: 0.05,
		},
	}

	// Simulate cost already exceeded.
	globalImageGenLimiter.date = timeNowFormatDate()
	globalImageGenLimiter.count = 1
	globalImageGenLimiter.costUSD = 0.06

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for cost limit")
	}
	if !strings.Contains(err.Error(), "daily cost limit reached") {
		t.Errorf("expected cost limit error, got: %v", err)
	}
}

func TestToolImageGenerateDailyLimitReset(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 2,
			MaxCostDay: 10.00,
		},
	}

	// Set a past date - should reset on check.
	globalImageGenLimiter.date = "2020-01-01"
	globalImageGenLimiter.count = 100
	globalImageGenLimiter.costUSD = 999.00

	ok, _ := globalImageGenLimiter.check(cfg)
	if !ok {
		t.Fatal("expected limit to reset for new day")
	}
	if globalImageGenLimiter.count != 0 {
		t.Errorf("expected count reset to 0, got %d", globalImageGenLimiter.count)
	}
	if globalImageGenLimiter.costUSD != 0 {
		t.Errorf("expected cost reset to 0, got %f", globalImageGenLimiter.costUSD)
	}
}

func TestToolImageGenerateStatus(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	// Set some usage.
	globalImageGenLimiter.date = timeNowFormatDate()
	globalImageGenLimiter.count = 3
	globalImageGenLimiter.costUSD = 0.160

	result, err := toolImageGenerateStatus(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Generated: 3 / 10") {
		t.Errorf("expected generation count, got: %s", result)
	}
	if !strings.Contains(result, "$0.160") {
		t.Errorf("expected cost in result, got: %s", result)
	}
	if !strings.Contains(result, "Remaining: 7 images") {
		t.Errorf("expected remaining count, got: %s", result)
	}
}

func TestToolImageGenerateStatusEmpty(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			DailyLimit: 5,
			MaxCostDay: 2.00,
		},
	}

	result, err := toolImageGenerateStatus(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Generated: 0 / 5") {
		t.Errorf("expected zero usage, got: %s", result)
	}
	if !strings.Contains(result, "Remaining: 5 images") {
		t.Errorf("expected full remaining, got: %s", result)
	}
}

func TestToolImageGenerateStatusDefaultLimits(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	// No limits configured - should use defaults.
	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled: true,
		},
	}

	result, err := toolImageGenerateStatus(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default limit is 10, default max cost is 1.00.
	if !strings.Contains(result, "/ 10") {
		t.Errorf("expected default limit 10, got: %s", result)
	}
	if !strings.Contains(result, "$1.00") {
		t.Errorf("expected default max cost $1.00, got: %s", result)
	}
}

func TestToolImageGenerateAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "content policy violation",
			},
		})
	}))
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for API error")
	}
	if !strings.Contains(err.Error(), "content policy violation") {
		t.Errorf("expected API error message, got: %v", err)
	}
}

func TestToolImageGenerateAuthError(t *testing.T) {
	server := newImageGenTestServer(t, "correct-key")
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "wrong-key",
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for auth failure")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected unauthorized error, got: %v", err)
	}
}

func TestToolImageGenerateEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{},
		})
	}))
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
	if !strings.Contains(err.Error(), "no image generated") {
		t.Errorf("expected no image error, got: %v", err)
	}
}

func TestToolImageGenerateRecordUsage(t *testing.T) {
	server := newImageGenTestServer(t, "test-key")
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 1.00,
			Quality:    "standard",
		},
	}

	// Generate 3 images.
	for i := 0; i < 3; i++ {
		input, _ := json.Marshal(map[string]string{"prompt": "test"})
		_, err := toolImageGenerate(context.Background(), cfg, input)
		if err != nil {
			t.Fatalf("generation %d: unexpected error: %v", i+1, err)
		}
	}

	globalImageGenLimiter.mu.Lock()
	if globalImageGenLimiter.count != 3 {
		t.Errorf("expected count=3, got %d", globalImageGenLimiter.count)
	}
	expectedCost := 0.040 * 3
	if globalImageGenLimiter.costUSD < expectedCost-0.001 || globalImageGenLimiter.costUSD > expectedCost+0.001 {
		t.Errorf("expected cost ~$%.3f, got $%.3f", expectedCost, globalImageGenLimiter.costUSD)
	}
	globalImageGenLimiter.mu.Unlock()
}

func TestToolImageGenerateQualityOverride(t *testing.T) {
	// Track what quality was sent to the API.
	var receivedQuality string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		if q, ok := reqBody["quality"].(string); ok {
			receivedQuality = q
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"url": "https://example.com/img.png", "revised_prompt": ""},
			},
		})
	}))
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 5.00,
			Quality:    "standard", // Default config quality.
		},
	}

	// Override quality to hd via input args.
	input, _ := json.Marshal(map[string]any{
		"prompt":  "test",
		"quality": "hd",
	})
	result, err := toolImageGenerate(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedQuality != "hd" {
		t.Errorf("expected quality=hd sent to API, got %q", receivedQuality)
	}
	if !strings.Contains(result, "$0.080") {
		t.Errorf("expected HD cost $0.080, got: %s", result)
	}
}

func TestEstimateImageCost(t *testing.T) {
	tests := []struct {
		model, quality, size string
		want                 float64
	}{
		{"dall-e-3", "standard", "1024x1024", 0.040},
		{"dall-e-3", "hd", "1024x1024", 0.080},
		{"dall-e-3", "standard", "1024x1792", 0.080},
		{"dall-e-3", "standard", "1792x1024", 0.080},
		{"dall-e-3", "hd", "1024x1792", 0.120},
		{"dall-e-3", "hd", "1792x1024", 0.120},
		{"dall-e-2", "standard", "1024x1024", 0.020},
		{"dall-e-2", "hd", "1024x1024", 0.020},
		{"", "standard", "1024x1024", 0.040},   // empty model defaults to dall-e-3 pricing
		{"", "hd", "1024x1792", 0.120},          // empty model defaults to dall-e-3 pricing
	}
	for _, tt := range tests {
		got := estimateImageCost(tt.model, tt.quality, tt.size)
		if got != tt.want {
			t.Errorf("estimateImageCost(%q, %q, %q) = %f, want %f",
				tt.model, tt.quality, tt.size, got, tt.want)
		}
	}
}

func TestImageGenLimiterCheck(t *testing.T) {
	cfg := &Config{
		ImageGen: ImageGenConfig{
			DailyLimit: 5,
			MaxCostDay: 0.50,
		},
	}

	l := &imageGenLimiter{}

	// Fresh limiter should pass.
	ok, reason := l.check(cfg)
	if !ok {
		t.Fatalf("expected ok, got blocked: %s", reason)
	}

	// At limit should block.
	l.date = timeNowFormatDate()
	l.count = 5
	ok, reason = l.check(cfg)
	if ok {
		t.Fatal("expected blocked at daily limit")
	}
	if !strings.Contains(reason, "daily limit reached") {
		t.Errorf("unexpected reason: %s", reason)
	}

	// Cost limit should block.
	l.count = 1
	l.costUSD = 0.55
	ok, reason = l.check(cfg)
	if ok {
		t.Fatal("expected blocked at cost limit")
	}
	if !strings.Contains(reason, "daily cost limit reached") {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestImageGenLimiterRecord(t *testing.T) {
	l := &imageGenLimiter{}
	l.record(0.040)
	l.record(0.080)

	if l.count != 2 {
		t.Errorf("expected count=2, got %d", l.count)
	}
	if l.costUSD < 0.119 || l.costUSD > 0.121 {
		t.Errorf("expected cost ~$0.120, got $%.3f", l.costUSD)
	}
}

// timeNowFormatDate returns today's date string matching the limiter format.
func timeNowFormatDate() string {
	return timeNowFormat("2006-01-02")
}

func timeNowFormat(layout string) string {
	return time.Now().Format(layout)
}
