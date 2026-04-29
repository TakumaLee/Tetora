package search

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// SearchService coordinates multiple search providers and persistence.
type SearchService struct {
	Registry *Registry
	Store    *IntelStore
}

func NewSearchService(baseDir string) (*SearchService, error) {
	store, err := NewIntelStore(baseDir)
	if err != nil {
		return nil, err
	}
	return &SearchService{
		Registry: NewRegistry(),
		Store:    store,
	}, nil
}

// CompetitiveSearch performs fan-out search across all registered providers.
func (s *SearchService) CompetitiveSearch(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	providers := s.Registry.List()
	if len(providers) == 0 {
		return nil, nil
	}

	var wg sync.WaitGroup
	resultCh := make(chan []SearchResult, len(providers))
	
	// Ensure a strict timeout per provider
	searchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	for _, p := range providers {
		wg.Add(1)
		go func(p SearchProvider) {
			defer wg.Done()
			res, err := p.Search(searchCtx, query, opts)
			if err == nil {
				resultCh <- res
			}
		}(p)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var allResults []SearchResult
	for res := range resultCh {
		allResults = append(allResults, res...)
	}

	// 1. URL Normalization & Deduplication
	deduped := deduplicate(allResults)

	// 2. Sort by Score (or relevance)
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Score > deduped[j].Score
	})

	// 3. Persist to IntelStore
	_ = s.Store.SaveResult(query, deduped, 7*24*time.Hour)

	return deduped, nil
}

func deduplicate(results []SearchResult) []SearchResult {
	seen := make(map[string]bool)
	var final []SearchResult
	for _, r := range results {
		norm := normalizeURL(r.URL)
		if norm == "" {
			final = append(final, r) // Keep results without valid URLs
			continue
		}
		if !seen[norm] {
			seen[norm] = true
			final = append(final, r)
		}
	}
	return final
}

func normalizeURL(u string) string {
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return strings.ToLower(u)
	}
	// Remove common tracking params and fragments
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.ToLower(parsed.String())
}
