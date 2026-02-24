package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestToolNoteDedup(t *testing.T) {
	tmp := t.TempDir()

	// Set up a mock global notes service.
	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Create files: two duplicates and one unique.
	os.WriteFile(filepath.Join(tmp, "a.md"), []byte("hello world"), 0o644)
	os.WriteFile(filepath.Join(tmp, "b.md"), []byte("hello world"), 0o644)
	os.WriteFile(filepath.Join(tmp, "c.md"), []byte("unique content"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	// Test: scan without auto_delete.
	input, _ := json.Marshal(map[string]any{"auto_delete": false})
	out, err := toolNoteDedup(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolNoteDedup: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	totalFiles := int(result["total_files"].(float64))
	if totalFiles != 3 {
		t.Errorf("expected 3 total_files, got %d", totalFiles)
	}

	dupGroups := int(result["duplicate_groups"].(float64))
	if dupGroups != 1 {
		t.Errorf("expected 1 duplicate_groups, got %d", dupGroups)
	}

	// Verify files still exist (no deletion).
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if _, err := os.Stat(filepath.Join(tmp, name)); err != nil {
			t.Errorf("expected %s to still exist", name)
		}
	}
}

func TestToolNoteDedupAutoDelete(t *testing.T) {
	tmp := t.TempDir()

	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Three files with same content.
	os.WriteFile(filepath.Join(tmp, "x.md"), []byte("dup content"), 0o644)
	os.WriteFile(filepath.Join(tmp, "y.md"), []byte("dup content"), 0o644)
	os.WriteFile(filepath.Join(tmp, "z.md"), []byte("dup content"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{"auto_delete": true})
	out, err := toolNoteDedup(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolNoteDedup: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	deleted := int(result["deleted"].(float64))
	if deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", deleted)
	}

	// Verify only one file remains.
	remaining := 0
	for _, name := range []string{"x.md", "y.md", "z.md"} {
		if _, err := os.Stat(filepath.Join(tmp, name)); err == nil {
			remaining++
		}
	}
	if remaining != 1 {
		t.Errorf("expected 1 remaining file, got %d", remaining)
	}
}

func TestToolNoteDedupPrefix(t *testing.T) {
	tmp := t.TempDir()

	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Create subdirectory with duplicates.
	os.MkdirAll(filepath.Join(tmp, "sub"), 0o755)
	os.WriteFile(filepath.Join(tmp, "sub", "a.md"), []byte("same"), 0o644)
	os.WriteFile(filepath.Join(tmp, "sub", "b.md"), []byte("same"), 0o644)
	// Outside prefix - should not be scanned.
	os.WriteFile(filepath.Join(tmp, "outside.md"), []byte("same"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{"prefix": "sub"})
	out, err := toolNoteDedup(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolNoteDedup: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	totalFiles := int(result["total_files"].(float64))
	if totalFiles != 2 {
		t.Errorf("expected 2 total_files (prefix filter), got %d", totalFiles)
	}
}

func TestToolSourceAudit(t *testing.T) {
	tmp := t.TempDir()

	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Create some actual notes.
	os.WriteFile(filepath.Join(tmp, "note1.md"), []byte("content1"), 0o644)
	os.WriteFile(filepath.Join(tmp, "note2.md"), []byte("content2"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	// Expected: note1, note2, note3 (note3 is missing).
	input, _ := json.Marshal(map[string]any{
		"expected": []string{"note1.md", "note2.md", "note3.md"},
	})
	out, err := toolSourceAudit(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolSourceAudit: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	expectedCount := int(result["expected_count"].(float64))
	if expectedCount != 3 {
		t.Errorf("expected_count: want 3, got %d", expectedCount)
	}

	actualCount := int(result["actual_count"].(float64))
	if actualCount != 2 {
		t.Errorf("actual_count: want 2, got %d", actualCount)
	}

	missingCount := int(result["missing_count"].(float64))
	if missingCount != 1 {
		t.Errorf("missing_count: want 1, got %d", missingCount)
	}

	// Check missing contains note3.md.
	missingList, ok := result["missing"].([]any)
	if !ok || len(missingList) != 1 {
		t.Fatalf("expected 1 missing entry, got %v", result["missing"])
	}
	if missingList[0].(string) != "note3.md" {
		t.Errorf("expected missing note3.md, got %s", missingList[0])
	}
}

func TestToolSourceAuditExtra(t *testing.T) {
	tmp := t.TempDir()

	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Actual has an extra file not in expected list.
	os.WriteFile(filepath.Join(tmp, "note1.md"), []byte("content1"), 0o644)
	os.WriteFile(filepath.Join(tmp, "extra.md"), []byte("extra content"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"expected": []string{"note1.md"},
	})
	out, err := toolSourceAudit(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolSourceAudit: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	extraCount := int(result["extra_count"].(float64))
	if extraCount != 1 {
		t.Errorf("extra_count: want 1, got %d", extraCount)
	}

	extraList, ok := result["extra"].([]any)
	if !ok || len(extraList) != 1 {
		t.Fatalf("expected 1 extra entry, got %v", result["extra"])
	}
	if extraList[0].(string) != "extra.md" {
		t.Errorf("expected extra.md, got %s", extraList[0])
	}
}

func TestContentHashSHA256(t *testing.T) {
	h1 := contentHashSHA256("hello world")
	h2 := contentHashSHA256("hello world")
	h3 := contentHashSHA256("different content")

	if h1 != h2 {
		t.Errorf("same content should produce same hash: %s != %s", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("different content should produce different hash")
	}
	// First 16 bytes = 32 hex chars.
	if len(h1) != 32 {
		t.Errorf("expected 32 hex chars, got %d", len(h1))
	}
}
