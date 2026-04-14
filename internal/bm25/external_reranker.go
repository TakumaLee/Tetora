// Package bm25 provides BM25 text ranking and pluggable reranking for tool search.
package bm25

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// ExternalReranker implements Reranker by calling an external HTTP reranking service.
// This supports Cohere Rerank, BGE, or any compatible API.
type ExternalReranker struct {
	Endpoint string
	APIKey   string
	Timeout  time.Duration
	// Model is the model identifier to use (e.g., "rerank-v4.0-pro" for Cohere).
	Model string
	// HTTPClient can be overridden for testing.
	HTTPClient *http.Client
}

// ExternalRerankRequest is the request sent to the external reranker.
type ExternalRerankRequest struct {
	Model   string   `json:"model"`
	Query   string   `json:"query"`
	Docs    []string `json:"documents"`
	TopN    int      `json:"top_n,omitempty"`
}

// ExternalRerankResponse is the response from the external reranker.
type ExternalRerankResponse struct {
	Results []ExternalRerankResult `json:"results"`
}

// ExternalRerankResult is a single reranked document.
type ExternalRerankResult struct {
	Index        int     `json:"index"`
	Document     string  `json:"document"`
	RelevanceScore float64 `json:"relevance_score"`
}

// NewExternalReranker creates an external HTTP-based reranker.
// endpoint: the URL of the reranking service (e.g., "https://api.cohere.ai/v1/rerank")
// apiKey: API key for authentication.
// model: model identifier (e.g., "rerank-english-v3.0").
func NewExternalReranker(endpoint, apiKey, model string) *ExternalReranker {
	return &ExternalReranker{
		Endpoint: endpoint,
		APIKey:   apiKey,
		Model:    model,
		Timeout:  10 * time.Second,
	}
}

// Rerank implements the Reranker interface by calling the external API.
// ctx is used to cancel the HTTP request if the caller's context is done.
func (er *ExternalReranker) Rerank(ctx context.Context, query string, queryTerms []string, bm25Results []Result,
	getDocMeta func(docID string) DocMeta) []RerankResult {

	if len(bm25Results) == 0 || getDocMeta == nil {
		return nil
	}

	// Build documents array: use description + contextual summary as doc text.
	docs := make([]string, len(bm25Results))
	for i, r := range bm25Results {
		meta := getDocMeta(r.ID)
		text := meta.Description
		if meta.ContextualSummary != "" {
			text += " " + meta.ContextualSummary
		}
		docs[i] = meta.Name + " " + text
	}

	reqBody := ExternalRerankRequest{
		Model: er.Model,
		Query: query,
		Docs:  docs,
		TopN:  len(bm25Results),
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		// Fall back to BM25 ordering if serialization fails.
		return fallbackResults(bm25Results)
	}

	client := er.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: er.Timeout}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", er.Endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return fallbackResults(bm25Results)
	}

	req.Header.Set("Content-Type", "application/json")
	if er.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+er.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		// Network error — fall back to BM25.
		return fallbackResults(bm25Results)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// API error — fall back to BM25.
		return fallbackResults(bm25Results)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return fallbackResults(bm25Results)
	}

	var extResp ExternalRerankResponse
	if err := json.Unmarshal(respBody, &extResp); err != nil {
		return fallbackResults(bm25Results)
	}

	// Map external results back to our format.
	out := make([]RerankResult, 0, len(extResp.Results))
	for _, ext := range extResp.Results {
		if ext.Index < 0 || ext.Index >= len(bm25Results) {
			continue
		}
		out = append(out, RerankResult{
			ID:         bm25Results[ext.Index].ID,
			BM25Score:  bm25Results[ext.Index].Score,
			FinalScore: ext.RelevanceScore,
		})
	}

	return out
}

// fallbackResults returns BM25 results as-is with FinalScore = BM25Score.
func fallbackResults(bm25Results []Result) []RerankResult {
	out := make([]RerankResult, len(bm25Results))
	for i, r := range bm25Results {
		out[i] = RerankResult{
			ID:         r.ID,
			BM25Score:  r.Score,
			FinalScore: r.Score,
		}
	}
	return out
}
