package bm25

import (
	"context"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"search_tools", []string{"search", "tools"}},
		{"Execute a shell command", []string{"execute", "a", "shell", "command"}},
		{"memory_search - Search memory", []string{"memory", "search", "search", "memory"}},
		{"", nil},
		{"123", []string{"123"}},
		{"tool-name_test", []string{"tool", "name", "test"}},
	}

	for _, tc := range tests {
		got := Tokenize(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("Tokenize(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("Tokenize(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestBM25Search(t *testing.T) {
	docs := []Document{
		{ID: "web_search", Terms: Tokenize("Search the web using Google or Bing. Returns top results with snippets.")},
		{ID: "memory_search", Terms: Tokenize("Search your personal memory store for past notes and facts. Returns matching entries.")},
		{ID: "knowledge_search", Terms: Tokenize("Search the knowledge base for indexed files and documents.")},
		{ID: "exec", Terms: Tokenize("Execute a shell command and return stdout, stderr, and exit code.")},
		{ID: "file_read", Terms: Tokenize("Read contents of a file from the filesystem. Supports line ranges.")},
		{ID: "web_fetch", Terms: Tokenize("Fetch content from a URL and extract readable text.")},
		{ID: "agent_dispatch", Terms: Tokenize("Dispatch a task to a specific agent role for execution.")},
		{ID: "task_create", Terms: Tokenize("Create a new task in the task management system with title and description.")},
	}

	bm := New(docs, DefaultK1, DefaultB)

	tests := []struct {
		query    string
		wantTop  string // expected top result ID
		minScore float64
	}{
		{"web search", "web_search", 0.1},
		{"memory", "memory_search", 0.1},
		{"execute command", "exec", 0.1},
		{"read file", "file_read", 0.1},
		{"fetch url", "web_fetch", 0.1},
		{"dispatch agent task", "agent_dispatch", 0.1},
		{"search knowledge", "knowledge_search", 0.1},
	}

	for _, tc := range tests {
		terms := Tokenize(tc.query)
		results := bm.Search(terms, 3)
		if len(results) == 0 {
			t.Errorf("Search(%q): no results", tc.query)
			continue
		}
		if results[0].ID != tc.wantTop {
			t.Errorf("Search(%q): top result = %q, want %q (scores: %+v)", tc.query, results[0].ID, tc.wantTop, results)
		}
		if results[0].Score < tc.minScore {
			t.Errorf("Search(%q): score %f < minimum %f", tc.query, results[0].Score, tc.minScore)
		}
	}
}

func TestBM25NoResults(t *testing.T) {
	docs := []Document{
		{ID: "tool_a", Terms: Tokenize("Search web results")},
		{ID: "tool_b", Terms: Tokenize("Read files from disk")},
	}
	bm := New(docs, DefaultK1, DefaultB)

	results := bm.Search([]string{"nonexistent"}, 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestBM25EmptyIndex(t *testing.T) {
	bm := New(nil, DefaultK1, DefaultB)
	results := bm.Search([]string{"anything"}, 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty index, got %d", len(results))
	}
}

func TestBM25TopNLimit(t *testing.T) {
	docs := []Document{
		{ID: "a", Terms: Tokenize("search web")},
		{ID: "b", Terms: Tokenize("search memory")},
		{ID: "c", Terms: Tokenize("search knowledge")},
		{ID: "d", Terms: Tokenize("search files")},
		{ID: "e", Terms: Tokenize("search documents")},
	}
	bm := New(docs, DefaultK1, DefaultB)

	results := bm.Search([]string{"search"}, 2)
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestBM25ScoreOrdering(t *testing.T) {
	// BM25 favors documents where the query term is proportionally more important.
	// A short doc with the term once can beat a long doc with the term many times
	// due to length normalization. So test with clear frequency difference.
	docs := []Document{
		{ID: "once", Terms: Tokenize("web tool")},
		{ID: "twice", Terms: Tokenize("web search web results")},
	}
	bm := New(docs, DefaultK1, DefaultB)

	results := bm.Search([]string{"web"}, 2)
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// "twice" has higher TF for "web" (2 vs 1) and similar doc length, so it ranks higher.
	if results[0].ID != "twice" {
		t.Errorf("expected 'twice' first, got %q (scores: %+v)", results[0].ID, results)
	}
}

func TestRerankNameMatch(t *testing.T) {
	bm25Results := []Result{
		{ID: "memory_search", Score: 1.0},
		{ID: "knowledge_search", Score: 1.0},
	}

	getMeta := func(docID string) DocMeta {
		if docID == "memory_search" {
			return DocMeta{Name: "memory_search", Keywords: nil, DocLen: 3}
		}
		return DocMeta{Name: "knowledge_search", Keywords: nil, DocLen: 3}
	}

	cfg := DefaultRerankConfig()
	results := Rerank("memory", []string{"memory"}, bm25Results, getMeta, cfg)

	if results[0].ID != "memory_search" {
		t.Errorf("expected memory_search first after reranking, got %q", results[0].ID)
	}
	if results[0].FinalScore <= results[0].BM25Score {
		t.Errorf("memory_search should get name bonus: bm25=%.4f final=%.4f",
			results[0].BM25Score, results[0].FinalScore)
	}
}

func TestRerankKeywordBoost(t *testing.T) {
	bm25Results := []Result{
		{ID: "tool_a", Score: 1.0},
		{ID: "tool_b", Score: 1.0},
	}

	getMeta := func(docID string) DocMeta {
		if docID == "tool_a" {
			return DocMeta{Name: "tool_a", Keywords: []string{"email", "message"}, DocLen: 2}
		}
		return DocMeta{Name: "tool_b", Keywords: nil, DocLen: 2}
	}

	cfg := DefaultRerankConfig()
	results := Rerank("email", []string{"email"}, bm25Results, getMeta, cfg)

	if results[0].ID != "tool_a" {
		t.Errorf("expected tool_a first after reranking (keyword boost), got %q", results[0].ID)
	}
	if results[0].FinalScore <= results[0].BM25Score {
		t.Errorf("tool_a should get keyword boost: bm25=%.4f final=%.4f",
			results[0].BM25Score, results[0].FinalScore)
	}
}

func TestRerankEmptyInput(t *testing.T) {
	results := Rerank("", nil, nil, nil, DefaultRerankConfig())
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil input, got %d", len(results))
	}
}

func TestRerankPreservesAllResults(t *testing.T) {
	bm25Results := []Result{
		{ID: "a", Score: 1.0},
		{ID: "b", Score: 0.8},
		{ID: "c", Score: 0.5},
	}
	getMeta := func(docID string) DocMeta {
		return DocMeta{Name: docID, DocLen: 2}
	}

	results := Rerank("query", []string{"query"}, bm25Results, getMeta, DefaultRerankConfig())
	if len(results) != 3 {
		t.Errorf("expected 3 results preserved, got %d", len(results))
	}
}

func TestHeuristicRerankerImplementsInterface(t *testing.T) {
	// Verify HeuristicReranker satisfies the Reranker interface.
	var _ Reranker = NewHeuristicReranker(DefaultRerankConfig())

	hr := NewHeuristicReranker(DefaultRerankConfig())
	bm25Results := []Result{
		{ID: "search_tools", Score: 1.0},
		{ID: "memory_search", Score: 1.0},
	}
	getMeta := func(docID string) DocMeta {
		return DocMeta{Name: docID, DocLen: 3}
	}

	results := hr.Rerank(context.Background(), "search", []string{"search"}, bm25Results, getMeta)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// search_tools should rank first due to name match bonus.
	if results[0].ID != "search_tools" {
		t.Errorf("expected search_tools first, got %q", results[0].ID)
	}
}
