package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// --- Web Search Tool ---

// toolWebSearch performs web search using the configured search provider.
func toolWebSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"maxResults"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.MaxResults <= 0 {
		args.MaxResults = cfg.Tools.WebSearch.MaxResults
		if args.MaxResults <= 0 {
			args.MaxResults = 5
		}
	}

	// Check if search provider is configured.
	if cfg.Tools.WebSearch.Provider == "" {
		return "", fmt.Errorf("web search not configured (set tools.webSearch.provider in config)")
	}

	// Route to configured provider.
	switch cfg.Tools.WebSearch.Provider {
	case "brave":
		return searchBrave(ctx, cfg, args.Query, args.MaxResults)
	case "tavily":
		return searchTavily(ctx, cfg, args.Query, args.MaxResults)
	case "searxng":
		return searchSearXNG(ctx, cfg, args.Query, args.MaxResults)
	default:
		return "", fmt.Errorf("unknown search provider: %s", cfg.Tools.WebSearch.Provider)
	}
}

// searchBrave calls the Brave Search API.
func searchBrave(ctx context.Context, cfg *Config, query string, maxResults int) (string, error) {
	if cfg.Tools.WebSearch.APIKey == "" {
		return "", fmt.Errorf("brave search requires apiKey in tools.webSearch")
	}

	// Build request.
	url := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		urlEncode(query), maxResults)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", cfg.Tools.WebSearch.APIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("brave api error: %d %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	// Parse Brave API response.
	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &braveResp); err != nil {
		return "", fmt.Errorf("parse brave response: %w", err)
	}

	// Convert to standard result format.
	var results []map[string]string
	for _, r := range braveResp.Web.Results {
		results = append(results, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Description,
		})
	}

	out, _ := json.Marshal(results)
	return string(out), nil
}

// searchTavily calls the Tavily Search API.
func searchTavily(ctx context.Context, cfg *Config, query string, maxResults int) (string, error) {
	if cfg.Tools.WebSearch.APIKey == "" {
		return "", fmt.Errorf("tavily search requires apiKey in tools.webSearch")
	}

	// Build request.
	reqBody := map[string]any{
		"query":              query,
		"max_results":        maxResults,
		"search_depth":       "basic",
		"include_answer":     false,
		"include_raw_content": false,
	}
	reqJSON, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", strings.NewReader(string(reqJSON)))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cfg.Tools.WebSearch.APIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("tavily api error: %d %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	// Parse Tavily API response.
	var tavilyResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &tavilyResp); err != nil {
		return "", fmt.Errorf("parse tavily response: %w", err)
	}

	// Convert to standard result format.
	var results []map[string]string
	for _, r := range tavilyResp.Results {
		results = append(results, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Content,
		})
	}

	out, _ := json.Marshal(results)
	return string(out), nil
}

// searchSearXNG calls a self-hosted SearXNG instance.
func searchSearXNG(ctx context.Context, cfg *Config, query string, maxResults int) (string, error) {
	baseURL := cfg.Tools.WebSearch.BaseURL
	if baseURL == "" {
		return "", fmt.Errorf("searxng requires baseURL in tools.webSearch")
	}

	// Build request.
	url := fmt.Sprintf("%s/search?q=%s&format=json&pageno=1", baseURL, urlEncode(query))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("searxng api error: %d %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	// Parse SearXNG response.
	var searxResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &searxResp); err != nil {
		return "", fmt.Errorf("parse searxng response: %w", err)
	}

	// Limit results.
	if len(searxResp.Results) > maxResults {
		searxResp.Results = searxResp.Results[:maxResults]
	}

	// Convert to standard result format.
	var results []map[string]string
	for _, r := range searxResp.Results {
		results = append(results, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Content,
		})
	}

	out, _ := json.Marshal(results)
	return string(out), nil
}

// --- Helper Functions ---

// urlEncode encodes a string for use in URL query parameters.
func urlEncode(s string) string {
	// Simple URL encoding for query parameters.
	s = strings.ReplaceAll(s, " ", "+")
	s = strings.ReplaceAll(s, "&", "%26")
	s = strings.ReplaceAll(s, "=", "%3D")
	s = strings.ReplaceAll(s, "?", "%3F")
	s = strings.ReplaceAll(s, "#", "%23")
	return s
}
