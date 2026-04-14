package benchmark

import (
	"context"
	"testing"

	"tetora/internal/bm25"
	"tetora/internal/tools"
)

func buildTestRegistry(t *testing.T) *tools.Registry {
	t.Helper()
	r := tools.NewRegistry()
	r.Register(&tools.ToolDef{Name: "web_search", Description: "Search the web using Google or Bing. Returns top results."})
	r.Register(&tools.ToolDef{Name: "memory_search", Description: "Search your personal memory store for past notes and facts."})
	r.Register(&tools.ToolDef{Name: "memory_get", Description: "Get a specific memory by ID."})
	r.Register(&tools.ToolDef{Name: "knowledge_search", Description: "Search the knowledge base for indexed files and documents."})
	r.Register(&tools.ToolDef{Name: "exec", Description: "Execute a shell command and return stdout, stderr, and exit code."})
	r.Register(&tools.ToolDef{Name: "file_read", Description: "Read contents of a file from the filesystem."})
	r.Register(&tools.ToolDef{Name: "file_write", Description: "Write content to a file."})
	r.Register(&tools.ToolDef{Name: "agent_dispatch", Description: "Dispatch a task to a specific agent role."})
	r.Register(&tools.ToolDef{Name: "search_tools", Description: "Search available tools by keyword using BM25 ranking."})
	r.Register(&tools.ToolDef{Name: "execute_tool", Description: "Execute any registered tool by name."})
	r.Register(&tools.ToolDef{Name: "task_create", Description: "Create a new task in the task management system."})
	return r
}

func TestBenchmarkSuite(t *testing.T) {
	r := buildTestRegistry(t)
	suite := NewSuite()
	results := suite.Evaluate(r)

	if len(results) != len(suite.Cases) {
		t.Fatalf("expected %d results, got %d", len(suite.Cases), len(results))
	}

	summary := Summary(results)
	if summary["total_queries"] != float64(len(suite.Cases)) {
		t.Errorf("expected %d total queries, got %.0f", len(suite.Cases), summary["total_queries"])
	}

	// Check that some queries find relevant tools (basic sanity).
	if summary["avg_recall_at_3"] <= 0 {
		t.Error("avg_recall_at_3 should be > 0")
	}
	if summary["avg_mrr"] <= 0 {
		t.Error("avg_mrr should be > 0")
	}

	t.Logf("\n%s", Report(results))
}

func TestRerankerComparison(t *testing.T) {
	r := buildTestRegistry(t)
	suite := NewSuite()

	// Create two rerankers with different configs.
	heuristic := bm25.NewHeuristicReranker(bm25.DefaultRerankConfig())

	// A "pass-through" reranker that doesn't change scores (identity reranker).
	identity := &identityReranker{}

	report := CompareRerankers(r, suite, "heuristic", heuristic, "identity", identity)
	t.Logf("\n%s", report)

	// Heuristic should generally perform as well or better than identity.
	summary := Summary(suite.Evaluate(r))
	if summary["avg_mrr"] <= 0 {
		t.Error("avg_mrr should be > 0 after comparison")
	}
}

// identityReranker returns results unchanged (BM25 score = final score).
type identityReranker struct{}

func (ir *identityReranker) Rerank(_ context.Context, query string, queryTerms []string, bm25Results []bm25.Result,
	getMeta func(docID string) bm25.DocMeta) []bm25.RerankResult {
	out := make([]bm25.RerankResult, len(bm25Results))
	for i, r := range bm25Results {
		out[i] = bm25.RerankResult{
			ID:         r.ID,
			BM25Score:  r.Score,
			FinalScore: r.Score,
		}
	}
	return out
}

func TestMetrics(t *testing.T) {
	// Test recall
	r := recallAtK([]string{"a", "b", "c"}, []string{"a", "d"}, 3)
	if r != 0.5 {
		t.Errorf("recallAtK = %.2f, want 0.5", r)
	}

	// Test MRR: first relevant at position 2 → MRR = 1/2
	m := mrr([]string{"x", "a", "b"}, []string{"a"})
	if m != 0.5 {
		t.Errorf("mrr = %.2f, want 0.5", m)
	}

	// Test MRR: no relevant found → MRR = 0
	m = mrr([]string{"x", "y", "z"}, []string{"a"})
	if m != 0 {
		t.Errorf("mrr = %.2f, want 0", m)
	}

	// Test NDCG
	n := ndcgAt5([]string{"a", "b", "x"}, []string{"a", "b"}, nil)
	if n <= 0 || n > 1 {
		t.Errorf("ndcgAt5 = %.4f, want in (0, 1]", n)
	}
}
