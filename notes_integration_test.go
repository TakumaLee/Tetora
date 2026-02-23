package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotesCreateAndRead(t *testing.T) {
	tmp := t.TempDir()
	cfg := &Config{
		Notes: NotesConfig{
			Enabled:    true,
			VaultPath:  tmp,
			DefaultExt: ".md",
		},
	}
	cfg.baseDir = tmp

	svc := &NotesService{
		cfg:        cfg,
		vaultPath:  tmp,
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: tmp,
		},
	}

	// Create a note.
	err := svc.CreateNote("hello", "# Hello World\n\nThis is a test note.")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	// Verify file exists with .md extension.
	p := filepath.Join(tmp, "hello.md")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected file at %s: %v", p, err)
	}

	// Read it back.
	content, err := svc.ReadNote("hello")
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if content != "# Hello World\n\nThis is a test note." {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestNotesCreateWithExtension(t *testing.T) {
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

	// Create with explicit extension.
	err := svc.CreateNote("notes.txt", "plain text")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	p := filepath.Join(tmp, "notes.txt")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected file at %s: %v", p, err)
	}

	// Should NOT have double extension.
	pDouble := filepath.Join(tmp, "notes.txt.md")
	if _, err := os.Stat(pDouble); err == nil {
		t.Fatalf("unexpected double extension file: %s", pDouble)
	}
}

func TestNotesCreateNested(t *testing.T) {
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

	// Create a nested note.
	err := svc.CreateNote("daily/2024-01-15", "# Daily note")
	if err != nil {
		t.Fatalf("CreateNote nested: %v", err)
	}

	p := filepath.Join(tmp, "daily", "2024-01-15.md")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected nested file at %s: %v", p, err)
	}
}

func TestNotesAppend(t *testing.T) {
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

	// Create initial note.
	err := svc.CreateNote("log", "line1\n")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	// Append to it.
	err = svc.AppendNote("log", "line2\n")
	if err != nil {
		t.Fatalf("AppendNote: %v", err)
	}

	content, err := svc.ReadNote("log")
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if content != "line1\nline2\n" {
		t.Fatalf("unexpected content after append: %q", content)
	}
}

func TestNotesAppendCreatesIfNotExists(t *testing.T) {
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

	// Append to non-existent note should create it.
	err := svc.AppendNote("new-note", "created via append\n")
	if err != nil {
		t.Fatalf("AppendNote: %v", err)
	}

	content, err := svc.ReadNote("new-note")
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if content != "created via append\n" {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestNotesListWithPrefix(t *testing.T) {
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

	// Create several notes.
	svc.CreateNote("project/alpha", "Alpha content")
	svc.CreateNote("project/beta", "Beta content")
	svc.CreateNote("personal/diary", "Diary entry")

	// List all.
	all, err := svc.ListNotes("")
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 notes, got %d", len(all))
	}

	// List with prefix filter.
	proj, err := svc.ListNotes("project/")
	if err != nil {
		t.Fatalf("ListNotes with prefix: %v", err)
	}
	if len(proj) != 2 {
		t.Fatalf("expected 2 project notes, got %d", len(proj))
	}
	for _, n := range proj {
		if !strings.HasPrefix(n.Name, "project/") {
			t.Fatalf("unexpected note in project prefix: %s", n.Name)
		}
	}
}

func TestNotesListMetadata(t *testing.T) {
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

	content := "# Test #todo #important\n\nSee [[other-note]] and [[reference|alias]].\n"
	svc.CreateNote("meta-test", content)

	notes, err := svc.ListNotes("")
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}

	n := notes[0]
	if n.Name != "meta-test.md" {
		t.Fatalf("unexpected name: %s", n.Name)
	}
	if n.Size <= 0 {
		t.Fatal("expected positive size")
	}
	if n.ModTime.IsZero() {
		t.Fatal("expected non-zero mod time")
	}

	// Check tags.
	foundTodo := false
	foundImportant := false
	for _, tag := range n.Tags {
		if tag == "todo" {
			foundTodo = true
		}
		if tag == "important" {
			foundImportant = true
		}
	}
	if !foundTodo || !foundImportant {
		t.Fatalf("expected tags [todo, important], got %v", n.Tags)
	}

	// Check links.
	foundOther := false
	foundRef := false
	for _, link := range n.Links {
		if link == "other-note" {
			foundOther = true
		}
		if link == "reference" {
			foundRef = true
		}
	}
	if !foundOther || !foundRef {
		t.Fatalf("expected links [other-note, reference], got %v", n.Links)
	}
}

func TestNotesSearchViaTFIDF(t *testing.T) {
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

	// Create notes with distinct content.
	svc.CreateNote("golang", "Go is a statically typed compiled programming language designed at Google.")
	svc.CreateNote("python", "Python is a high-level general-purpose programming language.")
	svc.CreateNote("cooking", "The best recipe for chocolate cake requires butter and flour.")

	// Wait for async index rebuild.
	time.Sleep(200 * time.Millisecond)

	// Search for "programming language".
	results := svc.SearchNotes("programming language", 5)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for 'programming language', got %d", len(results))
	}

	// Both golang and python should appear, cooking should not (or rank low).
	foundGo := false
	foundPython := false
	for _, r := range results {
		if strings.Contains(r.Filename, "golang") {
			foundGo = true
		}
		if strings.Contains(r.Filename, "python") {
			foundPython = true
		}
	}
	if !foundGo || !foundPython {
		t.Fatalf("expected golang and python in results, got %v", results)
	}

	// Search for "chocolate cake".
	cakeResults := svc.SearchNotes("chocolate cake", 5)
	if len(cakeResults) == 0 {
		t.Fatal("expected results for 'chocolate cake'")
	}
	if !strings.Contains(cakeResults[0].Filename, "cooking") {
		t.Fatalf("expected cooking.md as top result, got %s", cakeResults[0].Filename)
	}
}

func TestExtractWikilinks(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"See [[page1]] and [[page2]].", []string{"page1", "page2"}},
		{"Link with alias: [[real-page|display text]].", []string{"real-page"}},
		{"Nested [[dir/subpage]].", []string{"dir/subpage"}},
		{"Duplicate [[dup]] and [[dup]].", []string{"dup"}},
		{"No links here.", nil},
		{"Empty [[]]", nil}, // empty brackets produce empty match, but regexp won't match empty
	}

	for _, tc := range tests {
		got := extractWikilinks(tc.input)
		if len(got) != len(tc.expected) {
			t.Errorf("extractWikilinks(%q): got %v, want %v", tc.input, got, tc.expected)
			continue
		}
		for i, g := range got {
			if g != tc.expected[i] {
				t.Errorf("extractWikilinks(%q)[%d]: got %q, want %q", tc.input, i, g, tc.expected[i])
			}
		}
	}
}

func TestExtractTags(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"#todo #done", []string{"todo", "done"}},
		{"Start of line #tag1 and middle #tag2.", []string{"tag1", "tag2"}},
		{"Nested #project/subtag works.", []string{"project/subtag"}},
		{"Duplicate #dup and #dup.", []string{"dup"}},
		{"No tags here.", nil},
		{"Email user@host.com is not a tag.", nil},       // @ is not a tag
		{"Number #123 is not a tag.", nil},                // starts with digit
		{"Heading ## is not a tag.", nil},                 // no word after #
	}

	for _, tc := range tests {
		got := extractTags(tc.input)
		if len(got) != len(tc.expected) {
			t.Errorf("extractTags(%q): got %v, want %v", tc.input, got, tc.expected)
			continue
		}
		for i, g := range got {
			if g != tc.expected[i] {
				t.Errorf("extractTags(%q)[%d]: got %q, want %q", tc.input, i, g, tc.expected[i])
			}
		}
	}
}

func TestNotesPathTraversal(t *testing.T) {
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

	// Path traversal should be rejected.
	cases := []string{
		"../escape",
		"foo/../../escape",
		"/etc/passwd",
		".hidden",
	}
	for _, name := range cases {
		if err := svc.CreateNote(name, "malicious"); err == nil {
			t.Errorf("expected error for name %q, got nil", name)
		}
		if _, err := svc.ReadNote(name); err == nil {
			t.Errorf("expected error for ReadNote(%q), got nil", name)
		}
		if err := svc.AppendNote(name, "malicious"); err == nil {
			t.Errorf("expected error for AppendNote(%q), got nil", name)
		}
	}
}

func TestNotesEmptyName(t *testing.T) {
	svc := &NotesService{
		cfg:        &Config{},
		vaultPath:  t.TempDir(),
		defaultExt: ".md",
		idx: &notesIndex{
			docs:      make(map[string]*docEntry),
			idf:       make(map[string]float64),
			vaultPath: t.TempDir(),
		},
	}

	if err := svc.CreateNote("", "content"); err == nil {
		t.Error("expected error for empty name")
	}
	if _, err := svc.ReadNote(""); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestNotesReadNotFound(t *testing.T) {
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

	_, err := svc.ReadNote("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent note")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' in error, got: %v", err)
	}
}

func TestNotesConfigDefaults(t *testing.T) {
	cfg := NotesConfig{}
	if cfg.defaultExtOrMd() != ".md" {
		t.Fatalf("expected .md, got %s", cfg.defaultExtOrMd())
	}

	cfg.DefaultExt = ".txt"
	if cfg.defaultExtOrMd() != ".txt" {
		t.Fatalf("expected .txt, got %s", cfg.defaultExtOrMd())
	}

	cfg2 := NotesConfig{VaultPath: ""}
	resolved := cfg2.vaultPathResolved("/base")
	if resolved != filepath.Join("/base", "vault") {
		t.Fatalf("expected /base/vault, got %s", resolved)
	}

	cfg3 := NotesConfig{VaultPath: "my-vault"}
	resolved3 := cfg3.vaultPathResolved("/base")
	if resolved3 != filepath.Join("/base", "my-vault") {
		t.Fatalf("expected /base/my-vault, got %s", resolved3)
	}
}

func TestNotesIndexRebuild(t *testing.T) {
	tmp := t.TempDir()
	idx := &notesIndex{
		docs:      make(map[string]*docEntry),
		idf:       make(map[string]float64),
		vaultPath: tmp,
	}

	// Write some files.
	os.WriteFile(filepath.Join(tmp, "a.md"), []byte("alpha bravo charlie"), 0o644)
	os.WriteFile(filepath.Join(tmp, "b.md"), []byte("delta echo foxtrot"), 0o644)

	if err := idx.rebuild(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if idx.totalDocs != 2 {
		t.Fatalf("expected 2 docs, got %d", idx.totalDocs)
	}

	// Search should work.
	results := idx.search("alpha bravo", 5)
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].Filename != "a.md" {
		t.Fatalf("expected a.md as top result, got %s", results[0].Filename)
	}
}

func TestValidateNoteName(t *testing.T) {
	valid := []string{"hello", "dir/note", "deep/nested/path", "note.txt", "2024-01-15"}
	for _, name := range valid {
		if err := validateNoteName(name); err != nil {
			t.Errorf("expected valid name %q, got error: %v", name, err)
		}
	}

	invalid := []string{"", "..", "../escape", "/absolute", ".hidden", "foo/../bar"}
	for _, name := range invalid {
		if err := validateNoteName(name); err == nil {
			t.Errorf("expected invalid name %q, got nil", name)
		}
	}
}

func TestLnFunction(t *testing.T) {
	// Test ln() against known values.
	tests := []struct {
		x    float64
		want float64 // approximate
	}{
		{1.0, 0.0},
		{2.718281828, 1.0},
		{10.0, 2.302585},
		{0.5, -0.693147},
	}
	for _, tc := range tests {
		got := ln(tc.x)
		diff := got - tc.want
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.001 {
			t.Errorf("ln(%f) = %f, want ~%f (diff %f)", tc.x, got, tc.want, diff)
		}
	}
}
