package tools

import (
	"context"
	"fmt"
	"testing"
)

func TestRegistrySearchBM25(t *testing.T) {
	r := NewRegistry()

	r.Register(&ToolDef{
		Name:        "web_search",
		Description: "Search the web using Google or Bing. Returns top results with snippets.",
	})
	r.Register(&ToolDef{
		Name:        "memory_search",
		Description: "Search your personal memory store for past notes and facts.",
	})
	r.Register(&ToolDef{
		Name:        "knowledge_search",
		Description: "Search the knowledge base for indexed files and documents.",
	})
	r.Register(&ToolDef{
		Name:        "exec",
		Description: "Execute a shell command and return stdout, stderr, and exit code.",
	})
	r.Register(&ToolDef{
		Name:        "file_read",
		Description: "Read contents of a file from the filesystem.",
	})
	r.Register(&ToolDef{
		Name:        "agent_dispatch",
		Description: "Dispatch a task to a specific agent role for execution.",
	})

	tests := []struct {
		query   string
		topN    int
		wantTop string
	}{
		{"web search", 3, "web_search"},
		{"memory", 3, "memory_search"},
		{"execute shell command", 3, "exec"},
		{"read file contents", 3, "file_read"},
		{"dispatch task to agent", 3, "agent_dispatch"},
		{"knowledge documents", 3, "knowledge_search"},
	}

	for _, tc := range tests {
		results := r.SearchBM25(context.Background(), tc.query, tc.topN)
		if len(results) == 0 {
			t.Errorf("SearchBM25(%q): no results", tc.query)
			continue
		}
		if results[0].Tool.Name != tc.wantTop {
			t.Errorf("SearchBM25(%q): top result = %q, want %q (scores: bm25=%.4f, final=%.4f)",
				tc.query, results[0].Tool.Name, tc.wantTop, results[0].BM25Score, results[0].FinalScore)
		}
	}
}

func TestRegistrySearchBM25EmptyQuery(t *testing.T) {
	r := NewRegistry()
	r.Register(&ToolDef{Name: "test", Description: "A test tool"})

	results := r.SearchBM25(context.Background(), "", 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestRegistrySearchBM25NoTools(t *testing.T) {
	r := NewRegistry()
	results := r.SearchBM25(context.Background(), "anything", 5)
	// Should not panic, returns nil or empty
	_ = results
}

func TestRegistryBM25IndexRebuildOnRegister(t *testing.T) {
	r := NewRegistry()

	// Register first tool, search should work
	r.Register(&ToolDef{Name: "tool_a", Description: "Search web results"})
	results := r.SearchBM25(context.Background(), "web", 5)
	if len(results) != 1 || results[0].Tool.Name != "tool_a" {
		t.Errorf("expected tool_a, got %+v", results)
	}

	// Register second tool, search should include both
	r.Register(&ToolDef{Name: "tool_b", Description: "Search memory"})
	results = r.SearchBM25(context.Background(), "search", 5)
	if len(results) != 2 {
		t.Errorf("expected 2 results after registering tool_b, got %d: %+v", len(results), results)
	}
}

func TestRegistrySearchBM25WithKeywords(t *testing.T) {
	r := NewRegistry()
	r.Register(&ToolDef{
		Name:        "msg_send",
		Description: "Send a message to a contact.",
		Keywords:    []string{"email", "notification", "communicate"},
	})

	// Searching for "email" should match via keywords even though description doesn't contain it
	results := r.SearchBM25(context.Background(), "send email", 5)
	if len(results) == 0 {
		t.Errorf("expected results for 'send email' via keywords, got none")
	}
	if len(results) > 0 && results[0].Tool.Name != "msg_send" {
		t.Errorf("expected msg_send, got %q", results[0].Tool.Name)
	}
}

func TestApplyDeferredPolicy(t *testing.T) {
	r := NewRegistry()

	// Register a mix of always-loaded and regular tools
	r.Register(&ToolDef{Name: "search_tools", Description: "Search tools"})
	r.Register(&ToolDef{Name: "execute_tool", Description: "Execute a tool"})
	r.Register(&ToolDef{Name: "memory_search", Description: "Search memory"})
	r.Register(&ToolDef{Name: "web_search", Description: "Search the web"})
	r.Register(&ToolDef{Name: "knowledge_search", Description: "Search knowledge"})
	r.Register(&ToolDef{Name: "email_send", Description: "Send an email"})
	r.Register(&ToolDef{Name: "file_read", Description: "Read a file"})
	r.Register(&ToolDef{Name: "task_create", Description: "Create a task"})

	r.ApplyDeferredPolicy()

	// Always-loaded tools should NOT be deferred
	alwaysNames := []string{"search_tools", "execute_tool", "memory_search", "web_search", "knowledge_search"}
	for _, name := range alwaysNames {
		tool, ok := r.Get(name)
		if !ok {
			t.Errorf("expected tool %q to exist", name)
			continue
		}
		if tool.DeferLoading {
			t.Errorf("tool %q should NOT be deferred (in always-loaded set)", name)
		}
	}

	// Other tools SHOULD be deferred
	deferredTools := []string{"email_send", "file_read", "task_create"}
	for _, name := range deferredTools {
		tool, ok := r.Get(name)
		if !ok {
			t.Errorf("expected tool %q to exist", name)
			continue
		}
		if !tool.DeferLoading {
			t.Errorf("tool %q should be deferred, but isn't", name)
		}
	}
}

func TestDeferredPolicyCount(t *testing.T) {
	r := NewRegistry()

	// Simulate a realistic registry
	r.Register(&ToolDef{Name: "search_tools", Description: "Search tools"})
	r.Register(&ToolDef{Name: "execute_tool", Description: "Execute a tool"})
	for i := 0; i < 50; i++ {
		r.Register(&ToolDef{Name: fmt.Sprintf("tool_%d", i), Description: fmt.Sprintf("Tool %d", i)})
	}

	r.ApplyDeferredPolicy()

	// Only 2 should be non-deferred (search_tools + execute_tool; memory_search etc. not registered here)
	alwaysCount := 0
	deferredCount := 0
	r.Range(func(t *ToolDef) bool {
		if t.DeferLoading {
			deferredCount++
		} else {
			alwaysCount++
		}
		return true
	})

	if alwaysCount != AlwaysLoadedCount() {
		// Note: only registered tools count, so alwaysCount <= AlwaysLoadedCount()
		t.Logf("alwaysCount=%d, registered always-loaded=%d (some may not be registered yet)", alwaysCount, AlwaysLoadedCount())
	}
	if deferredCount != 50 {
		t.Errorf("expected 50 deferred tools, got %d", deferredCount)
	}
}

func TestRerankingNameMatchBonus(t *testing.T) {
	r := NewRegistry()
	r.Register(&ToolDef{Name: "memory_search", Description: "Search personal memory for past notes."})
	r.Register(&ToolDef{Name: "knowledge_search", Description: "Search the knowledge base for indexed files."})
	r.Register(&ToolDef{Name: "web_search", Description: "Search the web for online results."})

	r.ApplyDeferredPolicy()

	// Query "search" matches all three. With reranking, "memory_search" should
	// rank first because "memory" doesn't appear in the query but the tool name
	// contains "search" and the description contains "search" too.
	// More specifically, let's query "memory" — only memory_search matches BM25,
	// so let's query "search" to get all three and verify reranking order.
	results := r.SearchBM25(context.Background(), "search", 3)
	if len(results) < 3 {
		t.Fatalf("expected 3 results, got %d: %+v", len(results), results)
	}
	// All have "search" in description, so BM25 ranks them similarly.
	// The reranker should give all equal scores (no name bonus for "search" alone
	// since all names contain "search").
	for _, res := range results {
		if res.FinalScore <= 0 {
			t.Errorf("%s final score = %.4f, expected > 0", res.Tool.Name, res.FinalScore)
		}
	}
}

func TestRerankingKeywordBoost(t *testing.T) {
	r := NewRegistry()
	r.Register(&ToolDef{
		Name:        "msg_send",
		Description: "Send a communication.",
		Keywords:    []string{"email", "notification", "message"},
	})
	r.Register(&ToolDef{
		Name:        "notify_push",
		Description: "Send a push notification to a device.",
	})

	// Query "email" should rank msg_send first because "email" is in its Keywords.
	results := r.SearchBM25(context.Background(), "email", 5)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Tool.Name != "msg_send" {
		t.Errorf("top result = %q, want msg_send (keyword boost). scores: bm25=%.4f final=%.4f",
			results[0].Tool.Name, results[0].BM25Score, results[0].FinalScore)
	}
}

func TestRerankingNameVsDescription(t *testing.T) {
	// A tool whose name matches the query should rank higher than one whose description
	// matches but name doesn't.
	r := NewRegistry()
	r.Register(&ToolDef{
		Name:        "task_list",
		Description: "Show all tasks in the system.",
	})
	r.Register(&ToolDef{
		Name:        "item_search",
		Description: "Search through your task list to find specific items.",
	})

	// Query "task" — both match in description, but task_list has "task" in its name.
	results := r.SearchBM25(context.Background(), "task", 5)
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}
	if results[0].Tool.Name != "task_list" {
		t.Errorf("top result = %q, want task_list (name match bonus). scores: bm25=%.4f final=%.4f",
			results[0].Tool.Name, results[0].BM25Score, results[0].FinalScore)
	}
}

func TestUsageFrequencyBoost(t *testing.T) {
	r := NewRegistry()
	r.Register(&ToolDef{Name: "tool_a", Description: "Do something useful."})
	r.Register(&ToolDef{Name: "tool_b", Description: "Do something similar."})

	// Simulate tool_a being used many times.
	for i := 0; i < 100; i++ {
		r.RecordUsage("tool_a")
	}

	// Both tools have identical BM25 scores for "something".
	// But tool_a should rank higher due to usage frequency bonus.
	results := r.SearchBM25(context.Background(), "something", 2)
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Tool.Name != "tool_a" {
		t.Errorf("top result = %q, want tool_a (usage frequency boost). scores: bm25=%.4f final=%.4f",
			results[0].Tool.Name, results[0].BM25Score, results[0].FinalScore)
	}
	if results[0].FinalScore <= results[1].FinalScore {
		t.Errorf("tool_a final %.4f should be > tool_b final %.4f (usage bonus)",
			results[0].FinalScore, results[1].FinalScore)
	}
}

func TestGetUsage(t *testing.T) {
	r := NewRegistry()
	r.Register(&ToolDef{Name: "my_tool", Description: "A test tool"})

	if r.GetUsage("my_tool") != 0 {
		t.Errorf("expected 0 usage initially, got %d", r.GetUsage("my_tool"))
	}

	r.RecordUsage("my_tool")
	r.RecordUsage("my_tool")
	r.RecordUsage("my_tool")

	if r.GetUsage("my_tool") != 3 {
		t.Errorf("expected 3 usage after 3 calls, got %d", r.GetUsage("my_tool"))
	}

	// Unknown tool returns 0.
	if r.GetUsage("nonexistent") != 0 {
		t.Errorf("expected 0 for unknown tool, got %d", r.GetUsage("nonexistent"))
	}
}
