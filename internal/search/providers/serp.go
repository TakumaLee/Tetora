package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"tetora/internal/search"
	"time"
)

type SerpAPIProvider struct {
	APIKey string
	Client *http.Client
}

func NewSerpAPIProvider(apiKey string) *SerpAPIProvider {
	return &SerpAPIProvider{
		APIKey: apiKey,
		Client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *SerpAPIProvider) Name() string {
	return "serpapi"
}

func (p *SerpAPIProvider) Search(ctx context.Context, query string, opts search.SearchOptions) ([]search.SearchResult, error) {
	if p.APIKey == "" {
		return nil, fmt.Errorf("serpapi requires an API key")
	}

	engine := "google"
	searchQuery := query

	switch opts.Type {
	case "news":
		engine = "google_news"
	case "social":
		engine = "google"
		searchQuery = "site:x.com " + query
	default:
		engine = "google"
	}
	
	u := fmt.Sprintf("https://serpapi.com/search?engine=%s&q=%s&num=%d&api_key=%s",
		engine, url.QueryEscape(searchQuery), opts.Limit, p.APIKey)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("serpapi error: %d %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var results []search.SearchResult

	if engine == "google_news" {
		var data struct {
			NewsResults []struct {
				Stories []struct {
					Title   string `json:"title"`
					Link    string `json:"link"`
					Date    string `json:"date"`
					Source  struct {
						Name string `json:"name"`
					} `json:"source"`
				} `json:"stories"`
			} `json:"news_results"`
		}
		if err := json.Unmarshal(body, &data); err == nil {
			for _, topic := range data.NewsResults {
				for _, r := range topic.Stories {
					results = append(results, search.SearchResult{
						ID:        fmt.Sprintf("serp-news-%d", time.Now().UnixNano()),
						Source:    fmt.Sprintf("%s (News: %s)", p.Name(), r.Source.Name),
						Title:     r.Title,
						URL:       r.Link,
						Content:   r.Date,
						Score:     0.9,
						Timestamp: time.Now(),
					})
					if len(results) >= opts.Limit {
						return results, nil
					}
				}
			}
		}
	} else {
		var data struct {
			OrganicResults []struct {
				Title   string `json:"title"`
				Link    string `json:"link"`
				Snippet string `json:"snippet"`
			} `json:"organic_results"`
		}
		if err := json.Unmarshal(body, &data); err == nil {
			for i, r := range data.OrganicResults {
				results = append(results, search.SearchResult{
					ID:        fmt.Sprintf("serp-%d-%d", time.Now().Unix(), i),
					Source:    p.Name(),
					Title:     r.Title,
					URL:       r.Link,
					Content:   r.Snippet,
					Score:     1.0 - (float64(i) * 0.05),
					Timestamp: time.Now(),
				})
			}
		}
	}

	return results, nil
}
