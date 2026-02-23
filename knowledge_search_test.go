package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"Hello World", []string{"hello", "world"}},
		{"foo-bar_baz", []string{"foo", "bar", "baz"}},
		{"Go 1.25 release", []string{"go", "1", "25", "release"}},
		{"", nil},
		{"   ", nil},
		{"one", []string{"one"}},
	}
	for _, tc := range tests {
		got := tokenize(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("tokenize(%q) len=%d, want %d; got=%v", tc.input, len(got), len(tc.want), got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("tokenize(%q)[%d]=%q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestTokenizeCJK(t *testing.T) {
	// Single CJK character produces a unigram only.
	got := tokenize("A")
	// "A" is not CJK; test actual CJK.
	got = tokenize("\u4e16")
	if len(got) != 1 || got[0] != "\u4e16" {
		t.Errorf("single CJK: got=%v, want [世]", got)
	}
}

func TestTokenizeCJKBigram(t *testing.T) {
	// "知識検索" should produce unigrams and bigrams.
	input := "知識検索"
	got := tokenize(input)

	// Expected: 知, 知識, 識, 識検, 検, 検索, 索
	expectedContains := []string{"知", "知識", "識", "識検", "検", "検索", "索"}
	gotSet := make(map[string]bool)
	for _, tok := range got {
		gotSet[tok] = true
	}
	for _, want := range expectedContains {
		if !gotSet[want] {
			t.Errorf("tokenize(%q) missing %q; got=%v", input, want, got)
		}
	}
}

func TestTokenizeMixed(t *testing.T) {
	// Mixed Latin and CJK.
	input := "Go言語のガイド"
	got := tokenize(input)

	gotSet := make(map[string]bool)
	for _, tok := range got {
		gotSet[tok] = true
	}

	// Should contain "go" (Latin, lowercased).
	if !gotSet["go"] {
		t.Errorf("tokenize(%q) missing 'go'; got=%v", input, got)
	}
	// Should contain CJK unigrams.
	if !gotSet["言"] {
		t.Errorf("tokenize(%q) missing '言'; got=%v", input, got)
	}
	// Should contain CJK bigram.
	if !gotSet["言語"] {
		t.Errorf("tokenize(%q) missing '言語'; got=%v", input, got)
	}
}

func TestIsCJK(t *testing.T) {
	tests := []struct {
		r    rune
		want bool
	}{
		{'A', false},
		{'z', false},
		{'1', false},
		{' ', false},
		{'\u4e16', true},  // 世 (CJK Unified)
		{'\u9fff', true},  // last CJK Unified
		{'\u4e00', true},  // first CJK Unified
		{'\u3042', true},  // あ (Hiragana)
		{'\u30a2', true},  // ア (Katakana)
		{'\u309f', true},  // last Hiragana
		{'\u30ff', true},  // last Katakana
		{'\u3040', true},  // first Hiragana
		{'\u30a0', true},  // first Katakana
		{'\u303f', false}, // just below Hiragana range
		{'\u3100', false}, // above Katakana range
	}
	for _, tc := range tests {
		got := isCJK(tc.r)
		if got != tc.want {
			t.Errorf("isCJK(%U) = %v, want %v", tc.r, got, tc.want)
		}
	}
}

func TestBuildKnowledgeIndex(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go-guide.md"), []byte("Go is a fast programming language.\nIt supports concurrency."), 0o644)
	os.WriteFile(filepath.Join(dir, "python.md"), []byte("Python is a versatile language.\nUsed for data science."), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	if idx.totalDocs != 2 {
		t.Errorf("totalDocs = %d, want 2", idx.totalDocs)
	}
	if _, ok := idx.docs["go-guide.md"]; !ok {
		t.Error("missing go-guide.md in index")
	}
	if _, ok := idx.docs["python.md"]; !ok {
		t.Error("missing python.md in index")
	}
	// IDF should have entries.
	if len(idx.idf) == 0 {
		t.Error("IDF map is empty")
	}
}

func TestBuildKnowledgeIndexEmpty(t *testing.T) {
	dir := t.TempDir()
	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex on empty dir: %v", err)
	}
	if idx.totalDocs != 0 {
		t.Errorf("totalDocs = %d, want 0", idx.totalDocs)
	}
}

func TestBuildKnowledgeIndexNonExistent(t *testing.T) {
	idx, err := buildKnowledgeIndex("/nonexistent/knowledge/path")
	if err != nil {
		t.Fatalf("expected no error for nonexistent dir, got: %v", err)
	}
	if idx.totalDocs != 0 {
		t.Errorf("totalDocs = %d, want 0", idx.totalDocs)
	}
}

func TestBuildKnowledgeIndexSkipsHidden(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(dir, "visible.md"), []byte("content"), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}
	if idx.totalDocs != 1 {
		t.Errorf("totalDocs = %d, want 1", idx.totalDocs)
	}
	if _, ok := idx.docs[".hidden"]; ok {
		t.Error("hidden file should not be indexed")
	}
}

func TestBuildKnowledgeIndexSkipsDirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}
	if idx.totalDocs != 1 {
		t.Errorf("totalDocs = %d, want 1", idx.totalDocs)
	}
}

func TestSearchKnowledge(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go-guide.md"), []byte("Go is a fast programming language.\nIt supports concurrency and goroutines."), 0o644)
	os.WriteFile(filepath.Join(dir, "python.md"), []byte("Python is a versatile scripting language.\nUsed for data science and machine learning."), 0o644)
	os.WriteFile(filepath.Join(dir, "rust.md"), []byte("Rust is a systems programming language.\nFocused on safety and performance."), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	results := idx.search("programming language", 10)
	if len(results) == 0 {
		t.Fatal("search returned no results for 'programming language'")
	}

	// All three docs mention "language"; go-guide and rust mention "programming".
	// Check that results are ranked (score descending).
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted by score: [%d]=%f > [%d]=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}

	// The top result should be one of the docs that contain "programming".
	top := results[0]
	if top.Filename != "go-guide.md" && top.Filename != "rust.md" {
		t.Errorf("top result = %q, expected go-guide.md or rust.md", top.Filename)
	}

	// Snippet should not be empty.
	if top.Snippet == "" {
		t.Error("top result snippet is empty")
	}

	// Score should be positive.
	if top.Score <= 0 {
		t.Errorf("top result score = %f, expected > 0", top.Score)
	}

	// LineStart should be 1-based.
	if top.LineStart < 1 {
		t.Errorf("top result lineStart = %d, expected >= 1", top.LineStart)
	}
}

func TestSearchKnowledgeCJK(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "jp-guide.md"), []byte("Go言語の基本的なガイドです。\n並行処理をサポートします。"), 0o644)
	os.WriteFile(filepath.Join(dir, "en-guide.md"), []byte("Go is a fast language.\nSupports concurrency."), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	results := idx.search("言語", 10)
	if len(results) == 0 {
		t.Fatal("search returned no results for CJK query '言語'")
	}

	// The Japanese file should be ranked first.
	if results[0].Filename != "jp-guide.md" {
		t.Errorf("top result = %q, want jp-guide.md", results[0].Filename)
	}
}

func TestSearchNoResults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "doc.md"), []byte("Hello world"), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	results := idx.search("zyxwvut", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results for non-matching query, got %d", len(results))
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "doc.md"), []byte("Hello world"), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	results := idx.search("", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestSearchEmptyIndex(t *testing.T) {
	dir := t.TempDir()
	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	results := idx.search("hello", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results on empty index, got %d", len(results))
	}
}

func TestSearchLimit(t *testing.T) {
	dir := t.TempDir()
	// Create many files that all contain "common".
	for i := 0; i < 20; i++ {
		name := filepath.Join(dir, strings.Replace("doc-NN.md", "NN", strings.Repeat("a", i+1), 1))
		os.WriteFile(name, []byte("This is a common document about common things."), 0o644)
	}

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	results := idx.search("common", 5)
	if len(results) > 5 {
		t.Errorf("expected at most 5 results, got %d", len(results))
	}
}

func TestSearchLimitZero(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello world"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("hello there"), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	// maxResults=0 means no limit.
	results := idx.search("hello", 0)
	if len(results) != 2 {
		t.Errorf("expected 2 results with maxResults=0, got %d", len(results))
	}
}

func TestBuildSnippet(t *testing.T) {
	lines := []string{
		"line zero",
		"line one",
		"line two - match here",
		"line three",
		"line four",
	}

	// Match at line 2 with 1 context line.
	snippet := buildSnippet(lines, 2, 1)
	if !strings.Contains(snippet, "line two - match here") {
		t.Errorf("snippet does not contain match line: %q", snippet)
	}
	if !strings.Contains(snippet, "line one") {
		t.Errorf("snippet does not contain context before: %q", snippet)
	}
	if !strings.Contains(snippet, "line three") {
		t.Errorf("snippet does not contain context after: %q", snippet)
	}
	// Should NOT contain line zero or line four (only 1 context line).
	if strings.Contains(snippet, "line zero") {
		t.Errorf("snippet should not contain line zero: %q", snippet)
	}
}

func TestBuildSnippetEdgeStart(t *testing.T) {
	lines := []string{"first line", "second line", "third line"}
	snippet := buildSnippet(lines, 0, 1)
	if !strings.Contains(snippet, "first line") {
		t.Errorf("snippet does not contain first line: %q", snippet)
	}
	if !strings.Contains(snippet, "second line") {
		t.Errorf("snippet does not contain second line: %q", snippet)
	}
}

func TestBuildSnippetEdgeEnd(t *testing.T) {
	lines := []string{"first line", "second line", "third line"}
	snippet := buildSnippet(lines, 2, 1)
	if !strings.Contains(snippet, "second line") {
		t.Errorf("snippet does not contain second line: %q", snippet)
	}
	if !strings.Contains(snippet, "third line") {
		t.Errorf("snippet does not contain third line: %q", snippet)
	}
}

func TestBuildSnippetEmpty(t *testing.T) {
	snippet := buildSnippet(nil, 0, 1)
	if snippet != "" {
		t.Errorf("expected empty snippet for nil lines, got %q", snippet)
	}
}

func TestBuildSnippetTruncation(t *testing.T) {
	// Create lines that produce a snippet longer than 200 chars.
	lines := []string{
		strings.Repeat("a", 100),
		strings.Repeat("b", 100),
		strings.Repeat("c", 100),
	}
	snippet := buildSnippet(lines, 1, 1)
	if len(snippet) > 203 { // 200 + "..."
		t.Errorf("snippet too long: %d chars", len(snippet))
	}
	if !strings.HasSuffix(snippet, "...") {
		t.Errorf("truncated snippet should end with '...': %q", snippet)
	}
}

func TestRebuild(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "initial.md"), []byte("initial content about Go"), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	if idx.totalDocs != 1 {
		t.Fatalf("totalDocs = %d, want 1", idx.totalDocs)
	}

	// Search for initial content.
	results := idx.search("initial", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'initial', got %d", len(results))
	}

	// Add a new file and rebuild.
	os.WriteFile(filepath.Join(dir, "added.md"), []byte("added content about Rust"), 0o644)
	if err := idx.rebuild(dir); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if idx.totalDocs != 2 {
		t.Errorf("after rebuild totalDocs = %d, want 2", idx.totalDocs)
	}

	// Search should find the new file.
	results = idx.search("rust", 10)
	if len(results) == 0 {
		t.Error("expected results for 'rust' after rebuild")
	}
	found := false
	for _, r := range results {
		if r.Filename == "added.md" {
			found = true
			break
		}
	}
	if !found {
		t.Error("added.md not found in search results after rebuild")
	}

	// Remove the initial file and rebuild.
	os.Remove(filepath.Join(dir, "initial.md"))
	if err := idx.rebuild(dir); err != nil {
		t.Fatalf("rebuild after remove: %v", err)
	}

	if idx.totalDocs != 1 {
		t.Errorf("after second rebuild totalDocs = %d, want 1", idx.totalDocs)
	}

	results = idx.search("initial", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results for 'initial' after file removed, got %d", len(results))
	}
}

func TestSearchScoreRanking(t *testing.T) {
	dir := t.TempDir()
	// doc1 mentions "terraform" many times.
	os.WriteFile(filepath.Join(dir, "terraform.md"),
		[]byte("Terraform is infrastructure as code.\nTerraform plans. Terraform applies. Terraform state."), 0o644)
	// doc2 mentions "terraform" once.
	os.WriteFile(filepath.Join(dir, "tools.md"),
		[]byte("Various tools: ansible, chef, puppet, terraform, pulumi."), 0o644)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		t.Fatalf("buildKnowledgeIndex: %v", err)
	}

	results := idx.search("terraform", 10)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// terraform.md should rank higher due to higher TF.
	if results[0].Filename != "terraform.md" {
		t.Errorf("top result = %q, want terraform.md", results[0].Filename)
	}
}

func TestFindBestMatchLine(t *testing.T) {
	lines := []string{
		"Introduction to the guide",
		"Go programming basics",
		"Advanced Go concurrency patterns",
		"Summary and conclusion",
	}
	queryTokens := tokenize("go concurrency")
	best := findBestMatchLine(lines, queryTokens)
	// Line 2 has both "go" and "concurrency".
	if best != 2 {
		t.Errorf("findBestMatchLine = %d, want 2", best)
	}
}
