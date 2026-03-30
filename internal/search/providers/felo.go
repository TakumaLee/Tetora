package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"tetora/internal/search"
	"time"
)

type FeloProvider struct{}

func NewFeloProvider() *FeloProvider {
	return &FeloProvider{}
}

func (p *FeloProvider) Name() string {
	return "felo"
}

func (p *FeloProvider) Search(ctx context.Context, query string, opts search.SearchOptions) ([]search.SearchResult, error) {
	// Felo is a CLI tool, so we use exec
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "felo", query).Output()
	if err != nil {
		return nil, fmt.Errorf("felo search failed: %w", err)
	}

	// Felo might output JSON array or plain text
	var results []search.SearchResult
	var parsed []map[string]string
	if jsonErr := json.Unmarshal(out, &parsed); jsonErr == nil {
		for i, r := range parsed {
			results = append(results, search.SearchResult{
				ID:        fmt.Sprintf("felo-%d-%d", time.Now().Unix(), i),
				Source:    p.Name(),
				Title:     r["title"],
				URL:       r["url"],
				Content:   r["snippet"],
				Score:     0.8, // Slightly lower base score for felo
				Timestamp: time.Now(),
			})
		}
	} else {
		// Fallback: treat whole output as one result
		results = append(results, search.SearchResult{
			ID:        fmt.Sprintf("felo-%d-0", time.Now().Unix()),
			Source:    p.Name(),
			Title:     query,
			Content:   strings.TrimSpace(string(out)),
			Score:     0.7,
			Timestamp: time.Now(),
		})
	}

	return results, nil
}
