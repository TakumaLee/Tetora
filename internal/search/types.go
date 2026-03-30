package search

import (
	"context"
	"time"
)

// SearchResult represents a single item found by a search provider.
type SearchResult struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"` // e.g., "brave", "tavily", "serpapi"
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	URL         string    `json:"url"`
	Score       float64   `json:"score"`
	Timestamp   time.Time `json:"timestamp"`
	PublishedAt time.Time `json:"published_at,omitempty"`
}

// SearchOptions configures the search behavior.
type SearchOptions struct {
	Limit      int
	MaxAgeDays int
	Language   string
	Region     string
}

// SearchProvider defines the interface for external search engines.
type SearchProvider interface {
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
	Name() string
}

// IntelMetadata represents the database entry for a search result.
type IntelMetadata struct {
	QueryHash string    `json:"query_hash"`
	ResultID  string    `json:"result_id"`
	URL       string    `json:"url"`
	FilePath  string    `json:"file_path"`
	ExpireAt  time.Time `json:"expire_at"`
}
