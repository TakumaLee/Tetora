// Package benchmark provides tool search evaluation with synthetic queries.
// Based on arXiv:2604.01733: domain-specific evaluation is essential —
// general embedding leaderboards don't predict tool retrieval performance.
package benchmark

import (
	"context"
	"fmt"
	"math"
	"strings"

	"tetora/internal/bm25"
	"tetora/internal/tools"
)

// QueryCase is a single benchmark query with known relevant tools.
type QueryCase struct {
	Query         string   // The search query
	RelevantTools []string // Tools that should appear in top results (ground truth)
}

// BenchmarkResult holds evaluation metrics for a single query or aggregate.
type BenchmarkResult struct {
	Query      string
	RecallAt3  float64 // Fraction of relevant tools found in top 3
	RecallAt5  float64 // Fraction of relevant tools found in top 5
	MRR        float64 // Mean Reciprocal Rank
	NDCGAt5    float64 // Normalized Discounted Cumulative Gain at 5
	AvgBM25    float64 // Average BM25 score of top results
	AvgFinal   float64 // Average final reranked score of top results
}

// Suite holds a collection of benchmark queries and evaluation logic.
type Suite struct {
	Cases []QueryCase
}

// NewSuite creates a benchmark suite with default synthetic queries
// that cover common tool search scenarios.
func NewSuite() *Suite {
	return &Suite{
		Cases: []QueryCase{
			{
				Query:         "search web online",
				RelevantTools: []string{"web_search"},
			},
			{
				Query:         "search memory personal notes",
				RelevantTools: []string{"memory_search", "memory_get"},
			},
			{
				Query:         "execute shell command bash",
				RelevantTools: []string{"exec"},
			},
			{
				Query:         "read file contents from disk",
				RelevantTools: []string{"file_read"},
			},
			{
				Query:         "write save file create",
				RelevantTools: []string{"file_write"},
			},
			{
				Query:         "dispatch task to agent role",
				RelevantTools: []string{"agent_dispatch"},
			},
			{
				Query:         "search knowledge base documents",
				RelevantTools: []string{"knowledge_search"},
			},
			{
				Query:         "search find available tools",
				RelevantTools: []string{"search_tools"},
			},
			{
				Query:         "run execute any tool",
				RelevantTools: []string{"execute_tool"},
			},
			{
				Query:         "create new task todo",
				RelevantTools: []string{"task_create"},
			},
		},
	}
}

// AddCase adds a custom benchmark query to the suite.
func (s *Suite) AddCase(q QueryCase) {
	s.Cases = append(s.Cases, q)
}

// Evaluate runs the benchmark against the given tool registry and returns per-query results.
func (s *Suite) Evaluate(registry *tools.Registry) []BenchmarkResult {
	results := make([]BenchmarkResult, 0, len(s.Cases))

	for _, tc := range s.Cases {
		searchResults := registry.SearchBM25(context.Background(), tc.Query, 10)
		topNames := make([]string, 0, len(searchResults))
		topBM25 := make([]float64, 0, len(searchResults))
		topFinal := make([]float64, 0, len(searchResults))
		for _, r := range searchResults {
			topNames = append(topNames, r.Tool.Name)
			topBM25 = append(topBM25, r.BM25Score)
			topFinal = append(topFinal, r.FinalScore)
		}

		res := BenchmarkResult{
			Query:    tc.Query,
			RecallAt3: recallAtK(topNames, tc.RelevantTools, 3),
			RecallAt5: recallAtK(topNames, tc.RelevantTools, 5),
			MRR:       mrr(topNames, tc.RelevantTools),
			NDCGAt5:   ndcgAt5(topNames, tc.RelevantTools, topFinal),
		}

		if len(topBM25) > 0 {
			for _, s := range topBM25 {
				res.AvgBM25 += s
			}
			res.AvgBM25 /= float64(len(topBM25))
		}
		if len(topFinal) > 0 {
			for _, s := range topFinal {
				res.AvgFinal += s
			}
			res.AvgFinal /= float64(len(topFinal))
		}

		results = append(results, res)
	}

	return results
}

// Summary returns aggregate metrics across all queries.
func Summary(results []BenchmarkResult) map[string]float64 {
	n := float64(len(results))
	if n == 0 {
		return map[string]float64{}
	}

	var sumR3, sumR5, sumMRR, sumNDCG float64
	for _, r := range results {
		sumR3 += r.RecallAt3
		sumR5 += r.RecallAt5
		sumMRR += r.MRR
		sumNDCG += r.NDCGAt5
	}

	return map[string]float64{
		"avg_recall_at_3": sumR3 / n,
		"avg_recall_at_5": sumR5 / n,
		"avg_mrr":         sumMRR / n,
		"avg_ndcg_at_5":   sumNDCG / n,
		"total_queries":   n,
	}
}

// Report formats the benchmark results as a human-readable string.
func Report(results []BenchmarkResult) string {
	var sb strings.Builder
	sb.WriteString("=== Tool Search Benchmark Report ===\n\n")

	for _, r := range results {
		sb.WriteString(fmt.Sprintf("Query: %q\n", r.Query))
		sb.WriteString(fmt.Sprintf("  Recall@3:  %.2f\n", r.RecallAt3))
		sb.WriteString(fmt.Sprintf("  Recall@5:  %.2f\n", r.RecallAt5))
		sb.WriteString(fmt.Sprintf("  MRR:       %.2f\n", r.MRR))
		sb.WriteString(fmt.Sprintf("  NDCG@5:    %.2f\n", r.NDCGAt5))
		sb.WriteString("\n")
	}

	agg := Summary(results)
	sb.WriteString("=== Aggregate Metrics ===\n")
	sb.WriteString(fmt.Sprintf("  Avg Recall@3: %.2f\n", agg["avg_recall_at_3"]))
	sb.WriteString(fmt.Sprintf("  Avg Recall@5: %.2f\n", agg["avg_recall_at_5"]))
	sb.WriteString(fmt.Sprintf("  Avg MRR:      %.2f\n", agg["avg_mrr"]))
	sb.WriteString(fmt.Sprintf("  Avg NDCG@5:   %.2f\n", agg["avg_ndcg_at_5"]))
	sb.WriteString(fmt.Sprintf("  Total Queries: %.0f\n", agg["total_queries"]))

	return sb.String()
}

// recallAtK computes the fraction of relevant tools found in top-K results.
func recallAtK(topK []string, relevant []string, k int) float64 {
	if len(relevant) == 0 {
		return 1.0
	}
	if len(topK) > k {
		topK = topK[:k]
	}

	found := 0
	for _, rel := range relevant {
		for _, actual := range topK {
			if actual == rel {
				found++
				break
			}
		}
	}
	return float64(found) / float64(len(relevant))
}

// mrr computes Mean Reciprocal Rank: 1/rank of the first relevant result.
func mrr(results []string, relevant []string) float64 {
	relSet := make(map[string]bool)
	for _, r := range relevant {
		relSet[r] = true
	}

	for i, r := range results {
		if relSet[r] {
			return 1.0 / float64(i+1)
		}
	}
	return 0.0
}

// ndcgAt5 computes Normalized Discounted Cumulative Gain at K=5.
func ndcgAt5(results []string, relevant []string, scores []float64) float64 {
	relSet := make(map[string]bool)
	for _, r := range relevant {
		relSet[r] = true
	}

	k := 5
	if len(results) < k {
		k = len(results)
	}

	// DCG
	dcg := 0.0
	for i := 0; i < k; i++ {
		rel := 0.0
		if relSet[results[i]] {
			rel = 1.0
		}
		dcg += rel / math.Log2(float64(i+1)+1)
	}

	// Ideal DCG (all relevant tools at top positions)
	idealRel := len(relevant)
	if idealRel > k {
		idealRel = k
	}
	idcg := 0.0
	for i := 0; i < idealRel; i++ {
		idcg += 1.0 / math.Log2(float64(i+1)+1)
	}

	if idcg == 0 {
		return 0.0
	}
	return dcg / idcg
}

// CompareRerankers runs the benchmark with two different rerankers and compares.
//
// NOT goroutine-safe: it temporarily mutates registry's reranker between
// evaluations and restores the default at the end. Must not be called
// concurrently with any operation that reads or modifies the registry's
// reranker (i.e. call only from a dedicated benchmark goroutine or test).
func CompareRerankers(registry *tools.Registry, suite *Suite, nameA string, rerankerA bm25.Reranker, nameB string, rerankerB bm25.Reranker) string {
	// Run with reranker A
	registry.SetReranker(rerankerA)
	resultsA := suite.Evaluate(registry)
	summaryA := Summary(resultsA)

	// Run with reranker B
	registry.SetReranker(rerankerB)
	resultsB := suite.Evaluate(registry)
	summaryB := Summary(resultsB)

	// Restore default
	registry.SetReranker(bm25.NewHeuristicReranker(bm25.DefaultRerankConfig()))

	var sb strings.Builder
	sb.WriteString("=== Reranker Comparison ===\n\n")
	sb.WriteString(fmt.Sprintf("  %-20s %-10s %-10s %-10s\n", "Metric", nameA, nameB, "Delta"))
	sb.WriteString("  " + strings.Repeat("-", 52) + "\n")

	metrics := []string{"avg_recall_at_3", "avg_recall_at_5", "avg_mrr", "avg_ndcg_at_5"}
	for _, m := range metrics {
		a := summaryA[m]
		b := summaryB[m]
		delta := b - a
		sb.WriteString(fmt.Sprintf("  %-20s %-10.4f %-10.4f %+.4f\n", m, a, b, delta))
	}

	// Determine winner
	winsA, winsB := 0, 0
	for i := range suite.Cases {
		if resultsA[i].MRR > resultsB[i].MRR {
			winsA++
		} else if resultsB[i].MRR > resultsA[i].MRR {
			winsB++
		}
	}
	sb.WriteString(fmt.Sprintf("\n  MRR wins: %s=%d, %s=%d\n", nameA, winsA, nameB, winsB))

	// Best reranker
	if summaryB["avg_mrr"] > summaryA["avg_mrr"] {
		sb.WriteString(fmt.Sprintf("\n  Winner: %s (avg MRR: %.4f vs %.4f)\n", nameB, summaryB["avg_mrr"], summaryA["avg_mrr"]))
	} else if summaryA["avg_mrr"] > summaryB["avg_mrr"] {
		sb.WriteString(fmt.Sprintf("\n  Winner: %s (avg MRR: %.4f vs %.4f)\n", nameA, summaryA["avg_mrr"], summaryB["avg_mrr"]))
	} else {
		sb.WriteString("\n  Tie — both rerankers perform equally.\n")
	}

	return sb.String()
}
