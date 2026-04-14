// Package bm25 provides BM25 (Best Matching 25) text ranking for search.
// BM25 is a bag-of-words retrieval function that ranks documents based on
// query terms appearing in each document, incorporating term frequency
// saturation and document length normalization.
package bm25

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Default parameters for BM25.
// k1 controls term frequency saturation (typical range: 1.2–2.0).
// b controls document length normalization (typical range: 0.5–1.0).
const (
	DefaultK1 = 1.5
	DefaultB  = 0.75
)

// Document represents a searchable document with an ID and tokenized content.
type Document struct {
	ID    string
	Terms []string
}

// BM25 holds the precomputed BM25 index for a document collection.
type BM25 struct {
	k1        float64
	b         float64
	avgDocLen float64
	docCount  int
	idf       map[string]float64       // term -> IDF value
	docTerms  map[string][]string       // doc ID -> terms
	docLens   map[string]int            // doc ID -> term count
}

// Tokenize splits text into lowercase alphabetic tokens.
// Non-alphabetic characters act as separators.
func Tokenize(text string) []string {
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

// New creates a BM25 index from the given documents.
func New(docs []Document, k1, b float64) *BM25 {
	if k1 <= 0 {
		k1 = DefaultK1
	}
	if b < 0 || b > 1 {
		b = DefaultB
	}

	bm := &BM25{
		k1:       k1,
		b:        b,
		idf:      make(map[string]float64),
		docTerms: make(map[string][]string),
		docLens:  make(map[string]int),
		docCount: len(docs),
	}

	// Build term frequency per document and document frequency for IDF.
	docFreq := make(map[string]int) // term -> number of docs containing it
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

	// Precompute IDF for all terms.
	for term, df := range docFreq {
		// Standard IDF: log((N - df + 0.5) / (df + 0.5) + 1)
		// This is the Robertson/Sparck Jones variant used in Lucene/Elasticsearch.
		bm.idf[term] = math.Log(1 + (float64(bm.docCount)-float64(df)+0.5)/(float64(df)+0.5))
	}

	return bm
}

// Score computes the BM25 score for a single document given query terms.
func (bm *BM25) Score(docID string, queryTerms []string) float64 {
	if bm.docCount == 0 || bm.avgDocLen == 0 {
		return 0
	}

	terms, ok := bm.docTerms[docID]
	if !ok {
		return 0
	}
	docLen := bm.docLens[docID]

	// Compute term frequencies in this document.
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

		// BM25 term score:
		// IDF(q) * (tf * (k1 + 1)) / (tf + k1 * (1 - b + b * |D|/avgdl))
		num := float64(freq) * (bm.k1 + 1)
		denom := float64(freq) + bm.k1*(1-bm.b+bm.b*float64(docLen)/bm.avgDocLen)
		score += idf * num / denom
	}

	return score
}

// Search ranks all documents by BM25 score and returns the top N results
// sorted by descending score.
func (bm *BM25) Search(queryTerms []string, topN int) []Result {
	if len(queryTerms) == 0 || bm.docCount == 0 {
		return nil
	}

	results := make([]Result, 0, bm.docCount)
	for docID := range bm.docTerms {
		s := bm.Score(docID, queryTerms)
		if s > 0 {
			results = append(results, Result{
				ID:    docID,
				Score: s,
			})
		}
	}

	// Sort by score descending.
	sortResults(results)

	if topN > 0 && topN < len(results) {
		results = results[:topN]
	}

	return results
}

// Result holds a single search result with its BM25 score.
type Result struct {
	ID    string
	Score float64
}

// sortResults sorts results by score in descending order.
func sortResults(results []Result) {
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
}

// --- Two-Stage Reranking (based on arXiv:2604.01733 findings) ---

// Reranker is a pluggable interface for the second-stage reranking step.
// Implementations can be heuristic-based, neural (Cohere, BGE), or hybrid.
type Reranker interface {
	// Rerank re-scores the given BM25 results and returns them sorted by final score.
	// query is the original query string, queryTerms are tokenized query terms.
	// bm25Results are the initial BM25-ranked results.
	// getMeta provides per-document metadata.
	// ctx is used for cancellation (e.g. external HTTP rerankers respect it).
	Rerank(ctx context.Context, query string, queryTerms []string, bm25Results []Result,
		getMeta func(docID string) DocMeta) []RerankResult
}

// --- Heuristic Reranker (default implementation) ---

// RerankConfig holds weights for the heuristic reranking stage.
// The final score = BM25Score * (1 + nameBonus + keywordBoost + lengthPenalty + usageBonus).
type RerankConfig struct {
	// NameMatchWeight: bonus multiplier when query terms appear in the tool name.
	NameMatchWeight float64
	// KeywordBoost: extra weight for matches in the Keywords field vs Description.
	KeywordBoost float64
	// LengthPenaltyFactor: penalize overly long descriptions (0 = no penalty).
	LengthPenaltyFactor float64
	// AvgDocLen: average description length for normalization (0 = skip penalty).
	AvgDocLen float64
	// UsageWeight: maximum bonus from usage frequency (0 = no usage bonus).
	UsageWeight float64
}

// DefaultRerankConfig returns sensible defaults based on empirical tuning.
func DefaultRerankConfig() RerankConfig {
	return RerankConfig{
		NameMatchWeight:     1.5,  // Name match gives 50%+ boost
		KeywordBoost:        0.5,  // Keyword field match gives 25%+ boost
		LengthPenaltyFactor: 0.15, // Mild penalty for very long descriptions
		UsageWeight:         0.3,  // Frequently used tools get up to 30% bonus
	}
}

// RerankResult holds a result with its original BM25 score and reranked final score.
type RerankResult struct {
	ID         string
	BM25Score  float64
	FinalScore float64
}

// HeuristicReranker implements Reranker using name match, keyword priority,
// length penalty, and usage frequency heuristics.
type HeuristicReranker struct {
	Cfg RerankConfig
}

// NewHeuristicReranker creates a heuristic reranker with the given config.
func NewHeuristicReranker(cfg RerankConfig) *HeuristicReranker {
	return &HeuristicReranker{Cfg: cfg}
}

// Rerank implements the Reranker interface.
func (hr *HeuristicReranker) Rerank(_ context.Context, query string, queryTerms []string, bm25Results []Result,
	getDocMeta func(docID string) DocMeta) []RerankResult {
	return rerankImpl(query, queryTerms, bm25Results, getDocMeta, hr.Cfg)
}

// Rerank is a package-level convenience function using the given config.
func Rerank(query string, queryTerms []string, bm25Results []Result,
	getDocMeta func(docID string) DocMeta, cfg RerankConfig) []RerankResult {
	return rerankImpl(query, queryTerms, bm25Results, getDocMeta, cfg)
}

// rerankImpl is the shared implementation.
func rerankImpl(query string, queryTerms []string, bm25Results []Result,
	getDocMeta func(docID string) DocMeta, cfg RerankConfig) []RerankResult {

	if len(bm25Results) == 0 || getDocMeta == nil {
		out := make([]RerankResult, 0, len(bm25Results))
		for _, r := range bm25Results {
			out = append(out, RerankResult{ID: r.ID, BM25Score: r.Score, FinalScore: r.Score})
		}
		return out
	}

	results := make([]RerankResult, len(bm25Results))
	for i, r := range bm25Results {
		meta := getDocMeta(r.ID)
		multiplier := 1.0

		// 1. Name exact match bonus (from paper: lexical precision dominates in entity domains).
		if meta.Name != "" {
			nameLower := strings.ToLower(meta.Name)
			for _, qt := range queryTerms {
				if strings.Contains(nameLower, qt) {
					multiplier += cfg.NameMatchWeight
				}
			}
			// If the entire query appears in the name, extra bonus.
			if strings.Contains(nameLower, strings.ToLower(query)) {
				multiplier += cfg.NameMatchWeight * 0.5
			}
		}

		// 2. Keyword field priority boost.
		// Terms matching the Keywords field are more relevant than Description matches.
		if len(meta.Keywords) > 0 {
			keywordSet := make(map[string]bool)
			for _, kw := range meta.Keywords {
				for _, t := range Tokenize(kw) {
					keywordSet[t] = true
				}
			}
			for _, qt := range queryTerms {
				if keywordSet[qt] {
					multiplier += cfg.KeywordBoost
				}
			}
		}

		// 3. Length penalty: long descriptions dilute relevance for precise queries.
		if cfg.LengthPenaltyFactor > 0 && cfg.AvgDocLen > 0 && meta.DocLen > 0 {
			ratio := float64(meta.DocLen) / cfg.AvgDocLen
			if ratio > 1.5 {
				multiplier -= cfg.LengthPenaltyFactor * (ratio - 1.0)
			}
		}

		// 4. Usage frequency bonus (inspired by term recency in WOSP 2020).
		// Frequently used tools get a diminishing-returns bonus: log(count+1).
		if cfg.UsageWeight > 0 && meta.UsageCount > 0 {
			importance := math.Log(float64(meta.UsageCount) + 1)
			multiplier += cfg.UsageWeight * importance
		}

		if multiplier < 0.1 {
			multiplier = 0.1 // Floor
		}

		finalScore := r.Score * multiplier
		results[i] = RerankResult{
			ID:         r.ID,
			BM25Score:  r.Score,
			FinalScore: finalScore,
		}
	}

	// Sort by final score.
	sortRerankResults(results)
	return results
}

// DocMeta holds per-document metadata used by the reranker.
type DocMeta struct {
	Name              string   // Tool name (for exact match bonus)
	Description       string   // Tool description (for external reranker docs)
	ContextualSummary string   // AI-generated contextual summary
	Keywords          []string // Extra keywords (for priority boost)
	DocLen            int      // Tokenized description length (for length penalty)
	UsageCount        int      // How many times this tool has been called (for popularity boost)
}

func sortRerankResults(results []RerankResult) {
	sort.Slice(results, func(i, j int) bool { return results[i].FinalScore > results[j].FinalScore })
}
