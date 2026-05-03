// bm25_inline.go — inlined from archived internal/bm25 package.
// Provides BM25 text ranking and pluggable two-stage reranking for tool search.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Default parameters for BM25.
const (
	bm25DefaultK1 = 1.5
	bm25DefaultB  = 0.75
)

// bm25Document represents a searchable document with an ID and tokenized content.
type bm25Document struct {
	ID    string
	Terms []string
}

// bm25Index holds the precomputed BM25 index for a document collection.
type bm25Index struct {
	k1        float64
	b         float64
	avgDocLen float64
	docCount  int
	idf       map[string]float64
	docTerms  map[string][]string
	docLens   map[string]int
}

// bm25Tokenize splits text into lowercase alphanumeric tokens.
func bm25Tokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var current strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// bm25New creates a BM25 index from the given documents.
func bm25New(docs []bm25Document, k1, b float64) *bm25Index {
	if k1 <= 0 {
		k1 = bm25DefaultK1
	}
	if b < 0 || b > 1 {
		b = bm25DefaultB
	}

	bm := &bm25Index{
		k1:       k1,
		b:        b,
		idf:      make(map[string]float64),
		docTerms: make(map[string][]string),
		docLens:  make(map[string]int),
		docCount: len(docs),
	}

	docFreq := make(map[string]int)
	var totalLen int

	for _, doc := range docs {
		bm.docTerms[doc.ID] = doc.Terms
		docLen := len(doc.Terms)
		bm.docLens[doc.ID] = docLen
		totalLen += docLen

		seen := make(map[string]bool)
		for _, term := range doc.Terms {
			if !seen[term] {
				docFreq[term]++
				seen[term] = true
			}
		}
	}

	if bm.docCount > 0 {
		bm.avgDocLen = float64(totalLen) / float64(bm.docCount)
	}

	for term, df := range docFreq {
		bm.idf[term] = math.Log(1 + (float64(bm.docCount)-float64(df)+0.5)/(float64(df)+0.5))
	}

	return bm
}

// score computes the BM25 score for a single document given query terms.
func (bm *bm25Index) score(docID string, queryTerms []string) float64 {
	if bm.docCount == 0 || bm.avgDocLen == 0 {
		return 0
	}

	terms, ok := bm.docTerms[docID]
	if !ok {
		return 0
	}
	docLen := bm.docLens[docID]

	tf := make(map[string]int)
	for _, t := range terms {
		tf[t]++
	}

	var score float64
	for _, q := range queryTerms {
		idf, ok := bm.idf[q]
		if !ok || idf == 0 {
			continue
		}
		freq := tf[q]
		if freq == 0 {
			continue
		}
		num := float64(freq) * (bm.k1 + 1)
		denom := float64(freq) + bm.k1*(1-bm.b+bm.b*float64(docLen)/bm.avgDocLen)
		score += idf * num / denom
	}

	return score
}

// search ranks all documents by BM25 score and returns the top N results.
func (bm *bm25Index) search(queryTerms []string, topN int) []bm25Result {
	if len(queryTerms) == 0 || bm.docCount == 0 {
		return nil
	}

	results := make([]bm25Result, 0, bm.docCount)
	for docID := range bm.docTerms {
		s := bm.score(docID, queryTerms)
		if s > 0 {
			results = append(results, bm25Result{ID: docID, Score: s})
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })

	if topN > 0 && topN < len(results) {
		results = results[:topN]
	}

	return results
}

// bm25Result holds a single search result with its BM25 score.
type bm25Result struct {
	ID    string
	Score float64
}

// --- Reranking ---

// bm25DocMeta holds per-document metadata used by the reranker.
type bm25DocMeta struct {
	Name              string
	Description       string
	ContextualSummary string
	Keywords          []string
	DocLen            int
	UsageCount        int
}

// bm25RerankResult holds a result with its original BM25 score and final reranked score.
type bm25RerankResult struct {
	ID         string
	BM25Score  float64
	FinalScore float64
}

// bm25Reranker is a pluggable interface for the second-stage reranking step.
type bm25Reranker interface {
	Rerank(ctx context.Context, query string, queryTerms []string, bm25Results []bm25Result,
		getMeta func(docID string) bm25DocMeta) []bm25RerankResult
}

// --- Heuristic Reranker ---

// bm25RerankConfig holds weights for the heuristic reranking stage.
type bm25RerankConfig struct {
	NameMatchWeight     float64
	KeywordBoost        float64
	LengthPenaltyFactor float64
	AvgDocLen           float64
	UsageWeight         float64
}

// bm25DefaultRerankConfig returns sensible defaults.
func bm25DefaultRerankConfig() bm25RerankConfig {
	return bm25RerankConfig{
		NameMatchWeight:     1.5,
		KeywordBoost:        0.5,
		LengthPenaltyFactor: 0.15,
		UsageWeight:         0.3,
	}
}

// bm25HeuristicReranker implements bm25Reranker using name match, keyword priority,
// length penalty, and usage frequency heuristics.
type bm25HeuristicReranker struct {
	cfg bm25RerankConfig
}

// newBM25HeuristicReranker creates a heuristic reranker with the given config.
func newBM25HeuristicReranker(cfg bm25RerankConfig) *bm25HeuristicReranker {
	return &bm25HeuristicReranker{cfg: cfg}
}

// Rerank implements the bm25Reranker interface.
func (hr *bm25HeuristicReranker) Rerank(_ context.Context, query string, queryTerms []string,
	results []bm25Result, getMeta func(docID string) bm25DocMeta) []bm25RerankResult {

	if len(results) == 0 || getMeta == nil {
		out := make([]bm25RerankResult, 0, len(results))
		for _, r := range results {
			out = append(out, bm25RerankResult{ID: r.ID, BM25Score: r.Score, FinalScore: r.Score})
		}
		return out
	}

	out := make([]bm25RerankResult, len(results))
	for i, r := range results {
		meta := getMeta(r.ID)
		multiplier := 1.0

		// Name match bonus.
		if meta.Name != "" {
			nameLower := strings.ToLower(meta.Name)
			for _, qt := range queryTerms {
				if strings.Contains(nameLower, qt) {
					multiplier += hr.cfg.NameMatchWeight
				}
			}
			if strings.Contains(nameLower, strings.ToLower(query)) {
				multiplier += hr.cfg.NameMatchWeight * 0.5
			}
		}

		// Keyword field priority boost.
		if len(meta.Keywords) > 0 {
			keywordSet := make(map[string]bool)
			for _, kw := range meta.Keywords {
				for _, t := range bm25Tokenize(kw) {
					keywordSet[t] = true
				}
			}
			for _, qt := range queryTerms {
				if keywordSet[qt] {
					multiplier += hr.cfg.KeywordBoost
				}
			}
		}

		// Length penalty.
		if hr.cfg.LengthPenaltyFactor > 0 && hr.cfg.AvgDocLen > 0 && meta.DocLen > 0 {
			ratio := float64(meta.DocLen) / hr.cfg.AvgDocLen
			if ratio > 1.5 {
				multiplier -= hr.cfg.LengthPenaltyFactor * (ratio - 1.0)
			}
		}

		// Usage frequency bonus.
		if hr.cfg.UsageWeight > 0 && meta.UsageCount > 0 {
			importance := math.Log(float64(meta.UsageCount) + 1)
			multiplier += hr.cfg.UsageWeight * importance
		}

		if multiplier < 0.1 {
			multiplier = 0.1
		}

		out[i] = bm25RerankResult{
			ID:         r.ID,
			BM25Score:  r.Score,
			FinalScore: r.Score * multiplier,
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].FinalScore > out[j].FinalScore })
	return out
}

// --- External Reranker ---

// bm25ExternalReranker implements bm25Reranker by calling an external HTTP reranking service.
type bm25ExternalReranker struct {
	endpoint   string
	apiKey     string
	model      string
	timeout    time.Duration
	httpClient *http.Client
}

// newBM25ExternalReranker creates an external HTTP-based reranker.
func newBM25ExternalReranker(endpoint, apiKey, model string) *bm25ExternalReranker {
	return &bm25ExternalReranker{
		endpoint: endpoint,
		apiKey:   apiKey,
		model:    model,
		timeout:  10 * time.Second,
	}
}

// Rerank implements the bm25Reranker interface by calling the external API.
func (er *bm25ExternalReranker) Rerank(ctx context.Context, query string, _ []string,
	bm25Results []bm25Result, getMeta func(docID string) bm25DocMeta) []bm25RerankResult {

	if len(bm25Results) == 0 || getMeta == nil {
		return nil
	}

	docs := make([]string, len(bm25Results))
	for i, r := range bm25Results {
		meta := getMeta(r.ID)
		text := meta.Description
		if meta.ContextualSummary != "" {
			text += " " + meta.ContextualSummary
		}
		docs[i] = meta.Name + " " + text
	}

	reqBody := struct {
		Model string   `json:"model"`
		Query string   `json:"query"`
		Docs  []string `json:"documents"`
		TopN  int      `json:"top_n,omitempty"`
	}{
		Model: er.model,
		Query: query,
		Docs:  docs,
		TopN:  len(bm25Results),
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return bm25FallbackResults(bm25Results)
	}

	client := er.httpClient
	if client == nil {
		client = &http.Client{Timeout: er.timeout}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", er.endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return bm25FallbackResults(bm25Results)
	}

	req.Header.Set("Content-Type", "application/json")
	if er.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+er.apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return bm25FallbackResults(bm25Results)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return bm25FallbackResults(bm25Results)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return bm25FallbackResults(bm25Results)
	}

	var extResp struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &extResp); err != nil {
		return bm25FallbackResults(bm25Results)
	}

	out := make([]bm25RerankResult, 0, len(extResp.Results))
	for _, ext := range extResp.Results {
		if ext.Index < 0 || ext.Index >= len(bm25Results) {
			continue
		}
		out = append(out, bm25RerankResult{
			ID:         bm25Results[ext.Index].ID,
			BM25Score:  bm25Results[ext.Index].Score,
			FinalScore: ext.RelevanceScore,
		})
	}

	return out
}

// bm25FallbackResults returns BM25 results as-is with FinalScore = BM25Score.
func bm25FallbackResults(results []bm25Result) []bm25RerankResult {
	out := make([]bm25RerankResult, len(results))
	for i, r := range results {
		out[i] = bm25RerankResult{ID: r.ID, BM25Score: r.Score, FinalScore: r.Score}
	}
	return out
}
