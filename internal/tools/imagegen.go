package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
)

// ImageGenBaseURL can be overridden in tests.
var ImageGenBaseURL = "https://api.openai.com"

// ImageGenLimiter tracks daily usage for image generation.
type ImageGenLimiter struct {
	Mu      sync.Mutex `json:"-"`
	Date    string     `json:"-"` // YYYY-MM-DD
	Count   int        `json:"-"`
	CostUSD float64    `json:"-"`
}

// Check returns true if the request is within limits.
func (l *ImageGenLimiter) Check(cfg *config.Config) (bool, string) {
	l.Mu.Lock()
	defer l.Mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if l.Date != today {
		l.Date = today
		l.Count = 0
		l.CostUSD = 0
	}

	limit := cfg.ImageGen.DailyLimit
	if limit <= 0 {
		limit = 10
	}
	if l.Count >= limit {
		return false, fmt.Sprintf("daily limit reached (%d/%d)", l.Count, limit)
	}

	maxCost := cfg.ImageGen.MaxCostDay
	if maxCost <= 0 {
		maxCost = 1.00
	}
	if l.CostUSD >= maxCost {
		return false, fmt.Sprintf("daily cost limit reached ($%.2f/$%.2f)", l.CostUSD, maxCost)
	}

	return true, ""
}

// Record records a successful generation.
func (l *ImageGenLimiter) Record(cost float64) {
	l.Mu.Lock()
	defer l.Mu.Unlock()
	l.Count++
	l.CostUSD += cost
}

// EstimateImageCost returns the estimated cost based on model and quality.
func EstimateImageCost(model, quality, size string) float64 {
	// DALL-E 3 pricing (as of 2024):
	// Standard: 1024x1024=$0.040, 1024x1792=$0.080, 1792x1024=$0.080
	// HD: 1024x1024=$0.080, 1024x1792=$0.120, 1792x1024=$0.120
	if model == "" || model == "dall-e-3" {
		isLarge := size == "1024x1792" || size == "1792x1024"
		if quality == "hd" {
			if isLarge {
				return 0.120
			}
			return 0.080
		}
		if isLarge {
			return 0.080
		}
		return 0.040
	}
	// DALL-E 2 pricing: $0.020 for 1024x1024
	return 0.020
}

// ImageGenDeps holds external dependencies for image generation tool handlers.
type ImageGenDeps struct {
	// GetLimiter returns the ImageGenLimiter for the current request context.
	// Replaces appFromCtx(ctx).ImageGenLimiter in the root package.
	GetLimiter func(ctx context.Context) *ImageGenLimiter
}

// RegisterImageGenTools registers the image_generate and image_generate_status tools.
func RegisterImageGenTools(r *Registry, cfg *config.Config, enabled func(string) bool, deps ImageGenDeps) {
	if !cfg.ImageGen.Enabled {
		return
	}

	if enabled("image_generate") {
		r.Register(&ToolDef{
			Name:        "image_generate",
			Description: "Generate an image using DALL-E (costs $0.04-0.12 per image)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"prompt": {"type": "string", "description": "Image description prompt"},
					"size": {"type": "string", "description": "Image size: 1024x1024 (default), 1024x1792, 1792x1024"},
					"quality": {"type": "string", "description": "Quality: standard (default) or hd"}
				},
				"required": ["prompt"]
			}`),
			Handler: func(ctx context.Context, cfg *config.Config, input json.RawMessage) (string, error) {
				return imageGenerateHandler(ctx, cfg, input, deps)
			},
			Builtin: true,
		})
	}

	if enabled("image_generate_status") {
		r.Register(&ToolDef{
			Name:        "image_generate_status",
			Description: "Check today's image generation usage and remaining quota",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: func(ctx context.Context, cfg *config.Config, input json.RawMessage) (string, error) {
				return imageGenerateStatusHandler(ctx, cfg, deps)
			},
			Builtin: true,
		})
	}
}

// MakeImageGenerateHandler returns a Handler for image generation. Used by tests.
func MakeImageGenerateHandler(deps ImageGenDeps) Handler {
	return func(ctx context.Context, cfg *config.Config, input json.RawMessage) (string, error) {
		return imageGenerateHandler(ctx, cfg, input, deps)
	}
}

// MakeImageGenerateStatusHandler returns a Handler for image gen status. Used by tests.
func MakeImageGenerateStatusHandler(deps ImageGenDeps) Handler {
	return func(ctx context.Context, cfg *config.Config, input json.RawMessage) (string, error) {
		return imageGenerateStatusHandler(ctx, cfg, deps)
	}
}

func imageGenerateHandler(ctx context.Context, cfg *config.Config, input json.RawMessage, deps ImageGenDeps) (string, error) {
	limiter := deps.GetLimiter(ctx)
	var args struct {
		Prompt  string `json:"prompt"`
		Size    string `json:"size"`
		Quality string `json:"quality"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}

	// Check limits.
	if limiter == nil {
		return "", fmt.Errorf("image generation blocked: limiter not initialized")
	}
	ok, reason := limiter.Check(cfg)
	if !ok {
		return "", fmt.Errorf("image generation blocked: %s", reason)
	}

	// Resolve config defaults.
	apiKey := cfg.ImageGen.APIKey
	if apiKey == "" {
		return "", fmt.Errorf("imageGen.apiKey not configured")
	}
	model := cfg.ImageGen.Model
	if model == "" {
		model = "dall-e-3"
	}
	quality := args.Quality
	if quality == "" {
		quality = cfg.ImageGen.Quality
	}
	if quality == "" {
		quality = "standard"
	}
	size := args.Size
	if size == "" {
		size = "1024x1024"
	}

	// Validate size.
	validSizes := map[string]bool{
		"1024x1024": true, "1024x1792": true, "1792x1024": true,
	}
	if !validSizes[size] {
		return "", fmt.Errorf("invalid size %q (valid: 1024x1024, 1024x1792, 1792x1024)", size)
	}

	// Estimate cost.
	cost := EstimateImageCost(model, quality, size)

	// Build request.
	reqBody := map[string]any{
		"model":  model,
		"prompt": args.Prompt,
		"n":      1,
		"size":   size,
	}
	if model == "dall-e-3" {
		reqBody["quality"] = quality
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", ImageGenBaseURL+"/v1/images/generations", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		msg := errResp.Error.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return "", fmt.Errorf("OpenAI API error: %s", msg)
	}

	var result struct {
		Data []struct {
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(result.Data) == 0 {
		return "", fmt.Errorf("no image generated")
	}

	// Record usage.
	limiter.Record(cost)

	// Log to DB for cost tracking.
	logImageGenUsage(cfg, cost, model, quality, size)

	img := result.Data[0]
	output := fmt.Sprintf("Image generated successfully!\nURL: %s\nModel: %s | Quality: %s | Size: %s\nCost: $%.3f", img.URL, model, quality, size, cost)
	if img.RevisedPrompt != "" {
		output += fmt.Sprintf("\nRevised prompt: %s", img.RevisedPrompt)
	}
	return output, nil
}

func imageGenerateStatusHandler(ctx context.Context, cfg *config.Config, deps ImageGenDeps) (string, error) {
	limiter := deps.GetLimiter(ctx)
	if limiter == nil {
		return "", fmt.Errorf("image generation blocked: limiter not initialized")
	}

	limiter.Mu.Lock()
	defer limiter.Mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if limiter.Date != today {
		limiter.Date = today
		limiter.Count = 0
		limiter.CostUSD = 0
	}

	limit := cfg.ImageGen.DailyLimit
	if limit <= 0 {
		limit = 10
	}
	maxCost := cfg.ImageGen.MaxCostDay
	if maxCost <= 0 {
		maxCost = 1.00
	}

	remaining := limit - limiter.Count
	if remaining < 0 {
		remaining = 0
	}
	costRemaining := maxCost - limiter.CostUSD
	if costRemaining < 0 {
		costRemaining = 0
	}

	return fmt.Sprintf("Image Generation Status (today: %s)\nGenerated: %d / %d\nCost: $%.3f / $%.2f\nRemaining: %d images, $%.3f budget",
		today, limiter.Count, limit,
		limiter.CostUSD, maxCost,
		remaining, costRemaining), nil
}

// logImageGenUsage records image generation usage to the database.
func logImageGenUsage(cfg *config.Config, cost float64, model, quality, size string) {
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		return
	}
	// Create table if not exists.
	createSQL := `CREATE TABLE IF NOT EXISTS image_gen_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp TEXT NOT NULL,
		model TEXT NOT NULL,
		quality TEXT NOT NULL,
		size TEXT NOT NULL,
		cost_usd REAL NOT NULL
	)`
	db.Query(dbPath, createSQL)

	insertSQL := fmt.Sprintf(`INSERT INTO image_gen_usage (timestamp, model, quality, size, cost_usd) VALUES ('%s', '%s', '%s', '%s', %f)`,
		time.Now().UTC().Format(time.RFC3339),
		db.Escape(model), db.Escape(quality), db.Escape(size), cost)
	db.Query(dbPath, insertSQL)
}
