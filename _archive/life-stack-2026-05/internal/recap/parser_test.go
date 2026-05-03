package recap

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleLines = `{"type":"user","message":{"role":"user","content":"hi"},"uuid":"u1","sessionId":"s1","cwd":"/repo","timestamp":"2026-04-17T00:00:00Z"}
{"parentUuid":"u1","type":"system","subtype":"away_summary","content":"Goal: something happened while away.","timestamp":"2026-04-17T00:05:00Z","uuid":"u2","sessionId":"s1","cwd":"/repo","gitBranch":"feat/x"}
{"type":"assistant","message":{"role":"assistant","content":"ok"},"uuid":"u3","sessionId":"s1","cwd":"/repo","timestamp":"2026-04-17T00:06:00Z"}
{"type":"system","subtype":"other_event","content":"should-skip","uuid":"u4","sessionId":"s1","cwd":"/repo"}
{"type":"system","subtype":"away_summary","content":"","uuid":"u5","sessionId":"s1","cwd":"/repo"}
{"type":"system","subtype":"away_summary","content":"second away","timestamp":"2026-04-17T01:00:00Z","uuid":"u6","sessionId":"s1","cwd":"/repo","gitBranch":"feat/x"}
`

func writeTempJSONL(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return path
}

func TestReadAwaySummariesFrom_ExtractsOnlyAwaySummaries(t *testing.T) {
	path := writeTempJSONL(t, sampleLines)
	recs, offset, err := ReadAwaySummariesFrom(path, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 records (u2 + u6), got %d", len(recs))
	}
	if recs[0].UUID != "u2" || recs[0].Content == "" {
		t.Errorf("first record mismatched: %+v", recs[0])
	}
	if recs[1].UUID != "u6" || recs[1].GitBranch != "feat/x" {
		t.Errorf("second record mismatched: %+v", recs[1])
	}
	if offset <= 0 {
		t.Errorf("offset should advance to EOF, got %d", offset)
	}
}

func TestReadAwaySummariesFrom_SkipsEmptyContentAndMissingUUID(t *testing.T) {
	// u5 has empty content → should be skipped.
	path := writeTempJSONL(t, sampleLines)
	recs, _, err := ReadAwaySummariesFrom(path, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, r := range recs {
		if r.UUID == "u5" {
			t.Errorf("u5 with empty content should have been skipped")
		}
	}
}

func TestReadAwaySummariesFrom_ResumesFromOffset(t *testing.T) {
	path := writeTempJSONL(t, sampleLines)
	// First pass: read everything.
	_, offset, err := ReadAwaySummariesFrom(path, 0)
	if err != nil {
		t.Fatalf("read pass 1: %v", err)
	}
	// Append a new away_summary and a noise line.
	extra := `{"type":"system","subtype":"away_summary","content":"third away","uuid":"u7","sessionId":"s1","cwd":"/repo","timestamp":"2026-04-17T02:00:00Z"}
`
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString(extra); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	// Second pass: should only see the new one.
	recs, newOffset, err := ReadAwaySummariesFrom(path, offset)
	if err != nil {
		t.Fatalf("read pass 2: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 new record, got %d", len(recs))
	}
	if recs[0].UUID != "u7" {
		t.Errorf("expected u7, got %s", recs[0].UUID)
	}
	if newOffset <= offset {
		t.Errorf("offset should advance past previous EOF")
	}
}

func TestReadAwaySummariesFrom_TruncatedFileResetsToZero(t *testing.T) {
	path := writeTempJSONL(t, sampleLines)
	_, offset, err := ReadAwaySummariesFrom(path, 0)
	if err != nil {
		t.Fatalf("read pass 1: %v", err)
	}
	// Replace file with a shorter version (simulates rotation).
	shorter := `{"type":"system","subtype":"away_summary","content":"fresh","uuid":"u_new","sessionId":"s2","cwd":"/other"}
`
	if err := os.WriteFile(path, []byte(shorter), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	recs, _, err := ReadAwaySummariesFrom(path, offset)
	if err != nil {
		t.Fatalf("read pass 2: %v", err)
	}
	if len(recs) != 1 || recs[0].UUID != "u_new" {
		t.Fatalf("expected u_new after truncation, got %+v", recs)
	}
}
