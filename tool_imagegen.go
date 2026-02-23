package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// imageGenBaseURL can be overridden in tests.
var imageGenBaseURL = "https://api.openai.com"

// imageGenLimiter tracks daily usage for image generation.
type imageGenLimiter struct {
	mu      sync.Mutex
	date    string // YYYY-MM-DD
	count   int
	costUSD float64
}

var globalImageGenLimiter = &imageGenLimiter{}

// check returns true if the request is within limits.
func (l *imageGenLimiter) check(cfg *Config) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if l.date != today {
		l.date = today
		l.count = 0
		l.costUSD = 0
	}

	limit := cfg.ImageGen.DailyLimit
	if limit <= 0 {
		limit = 10
	}
	if l.count >= limit {
		return false, fmt.Sprintf("daily limit reached (%d/%d)", l.count, limit)
	}

	maxCost := cfg.ImageGen.MaxCostDay
	if maxCost <= 0 {
		maxCost = 1.00
	}
	if l.costUSD >= maxCost {
		return false, fmt.Sprintf("daily cost limit reached ($%.2f/$%.2f)", l.costUSD, maxCost)
	}

	return true, ""
}

// record records a successful generation.
func (l *imageGenLimiter) record(cost float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.count++
	l.costUSD += cost
}

// estimateImageCost returns the estimated cost based on model and quality.
func estimateImageCost(model, quality, size string) float64 {
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

func toolImageGenerate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
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
	ok, reason := globalImageGenLimiter.check(cfg)
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
	cost := estimateImageCost(model, quality, size)

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
	req, err := http.NewRequestWithContext(ctx, "POST", imageGenBaseURL+"/v1/images/generations", strings.NewReader(string(bodyBytes)))
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
	globalImageGenLimiter.record(cost)

	// Log to DB for cost tracking.
	logImageGenUsage(cfg, cost, model, quality, size)

	img := result.Data[0]
	output := fmt.Sprintf("Image generated successfully!\nURL: %s\nModel: %s | Quality: %s | Size: %s\nCost: $%.3f", img.URL, model, quality, size, cost)
	if img.RevisedPrompt != "" {
		output += fmt.Sprintf("\nRevised prompt: %s", img.RevisedPrompt)
	}
	return output, nil
}

func toolImageGenerateStatus(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	globalImageGenLimiter.mu.Lock()
	defer globalImageGenLimiter.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if globalImageGenLimiter.date != today {
		globalImageGenLimiter.date = today
		globalImageGenLimiter.count = 0
		globalImageGenLimiter.costUSD = 0
	}

	limit := cfg.ImageGen.DailyLimit
	if limit <= 0 {
		limit = 10
	}
	maxCost := cfg.ImageGen.MaxCostDay
	if maxCost <= 0 {
		maxCost = 1.00
	}

	remaining := limit - globalImageGenLimiter.count
	if remaining < 0 {
		remaining = 0
	}
	costRemaining := maxCost - globalImageGenLimiter.costUSD
	if costRemaining < 0 {
		costRemaining = 0
	}

	return fmt.Sprintf("Image Generation Status (today: %s)\nGenerated: %d / %d\nCost: $%.3f / $%.2f\nRemaining: %d images, $%.3f budget",
		today, globalImageGenLimiter.count, limit,
		globalImageGenLimiter.costUSD, maxCost,
		remaining, costRemaining), nil
}

// logImageGenUsage records image generation usage to the database.
func logImageGenUsage(cfg *Config, cost float64, model, quality, size string) {
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
	queryDB(dbPath, createSQL)

	insertSQL := fmt.Sprintf(`INSERT INTO image_gen_usage (timestamp, model, quality, size, cost_usd) VALUES ('%s', '%s', '%s', '%s', %f)`,
		time.Now().UTC().Format(time.RFC3339),
		escapeSQLite(model), escapeSQLite(quality), escapeSQLite(size), cost)
	queryDB(dbPath, insertSQL)
}
