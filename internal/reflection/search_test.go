package reflection

import (
	"path/filepath"
	"testing"
	"time"
)

func seedReflections(t *testing.T, dbPath string, refs []*Result) {
	t.Helper()
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	for _, r := range refs {
		if err := Store(dbPath, r); err != nil {
			t.Fatalf("Store: %v", err)
		}
	}
}

func TestSearchReflections_KeywordAndAgentFilter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	now := time.Now().UTC().Format(time.RFC3339)
	seedReflections(t, dbPath, []*Result{
		{TaskID: "t1", Agent: "kokuyou", Score: 1, Feedback: "failed", Improvement: "Must verify worktree before push", CreatedAt: now},
		{TaskID: "t2", Agent: "ruri", Score: 2, Feedback: "ok", Improvement: "Check compact summary before replying", CreatedAt: now},
		{TaskID: "t3", Agent: "kokuyou", Score: 2, Feedback: "slow", Improvement: "Verify worktree isolation", CreatedAt: now},
	})

	// Keyword-only: matches both kokuyou entries mentioning worktree.
	got, err := SearchReflections(dbPath, SearchQuery{Keyword: "worktree"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("keyword search expected 2, got %d", len(got))
	}

	// Keyword + agent: narrows to exactly one.
	got, err = SearchReflections(dbPath, SearchQuery{Keyword: "worktree", Agent: "kokuyou", Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].Agent != "kokuyou" {
		t.Errorf("keyword+agent expected 1 kokuyou hit, got %+v", got)
	}

	// scoreMax=1 keeps only the score=1 row.
	got, err = SearchReflections(dbPath, SearchQuery{Keyword: "worktree", ScoreMax: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].Score != 1 {
		t.Errorf("scoreMax filter failed, got %+v", got)
	}
}

func TestSearchReflections_LimitClamped(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	now := time.Now().UTC().Format(time.RFC3339)
	var refs []*Result
	for i := 0; i < 80; i++ {
		refs = append(refs, &Result{
			TaskID: time.Now().Format("150405.000000") + "-" + string(rune('a'+i%26)),
			Agent:  "kokuyou", Score: 2, Improvement: "same issue", CreatedAt: now,
		})
	}
	seedReflections(t, dbPath, refs)

	got, err := SearchReflections(dbPath, SearchQuery{Keyword: "same issue", Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 50 {
		t.Errorf("limit should clamp to 50, got %d", len(got))
	}
}

func TestGetReflection_FoundAndMissing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	now := time.Now().UTC().Format(time.RFC3339)
	seedReflections(t, dbPath, []*Result{
		{TaskID: "hit-me", Agent: "ruri", Score: 3, Improvement: "nothing", CreatedAt: now},
	})

	r, err := GetReflection(dbPath, "hit-me")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if r == nil || r.TaskID != "hit-me" {
		t.Errorf("expected hit-me, got %+v", r)
	}

	r, err = GetReflection(dbPath, "no-such-task")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if r != nil {
		t.Errorf("expected nil for missing task, got %+v", r)
	}
}
