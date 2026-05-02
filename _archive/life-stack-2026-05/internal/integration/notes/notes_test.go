package notes

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testService creates a Service backed by a temporary directory.
func testService(t *testing.T) *Service {
	t.Helper()
	tmp := t.TempDir()
	cfg := Config{
		Enabled:    true,
		VaultPath:  tmp,
		DefaultExt: ".md",
	}
	return New(cfg, tmp, false, nil, nil, nil, nil)
}

// --- CRUD ---

func TestCreateAndRead(t *testing.T) {
	svc := testService(t)

	const name = "hello"
	const content = "Hello, world!"

	if err := svc.CreateNote(name, content); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	got, err := svc.ReadNote(name)
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if got != content {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestCreateWithExtension(t *testing.T) {
	svc := testService(t)

	// Name already has .md extension — must not double-append.
	if err := svc.CreateNote("explicit.md", "data"); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := svc.ReadNote("explicit.md"); err != nil {
		t.Fatalf("ReadNote: %v", err)
	}

	// A different extension must be preserved as-is.
	if err := svc.CreateNote("note.txt", "text data"); err != nil {
		t.Fatalf("CreateNote txt: %v", err)
	}
	if _, err := svc.ReadNote("note.txt"); err != nil {
		t.Fatalf("ReadNote txt: %v", err)
	}
}

func TestCreateNested(t *testing.T) {
	svc := testService(t)

	if err := svc.CreateNote("projects/tetora/roadmap", "# Roadmap"); err != nil {
		t.Fatalf("CreateNote nested: %v", err)
	}
	got, err := svc.ReadNote("projects/tetora/roadmap")
	if err != nil {
		t.Fatalf("ReadNote nested: %v", err)
	}
	if got != "# Roadmap" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestAppend(t *testing.T) {
	svc := testService(t)

	if err := svc.CreateNote("journal", "line1\n"); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := svc.AppendNote("journal", "line2\n"); err != nil {
		t.Fatalf("AppendNote: %v", err)
	}

	got, err := svc.ReadNote("journal")
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line2") {
		t.Errorf("expected both lines; got %q", got)
	}
}

func TestAppendCreatesIfNotExists(t *testing.T) {
	svc := testService(t)

	if err := svc.AppendNote("new-note", "auto-created"); err != nil {
		t.Fatalf("AppendNote on non-existent: %v", err)
	}
	got, err := svc.ReadNote("new-note")
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if got != "auto-created" {
		t.Errorf("unexpected content: %q", got)
	}
}

// --- List ---

func TestListWithPrefix(t *testing.T) {
	svc := testService(t)

	notes := []string{"work/task1", "work/task2", "personal/diary"}
	for _, n := range notes {
		if err := svc.CreateNote(n, "content"); err != nil {
			t.Fatalf("CreateNote %q: %v", n, err)
		}
	}

	listed, err := svc.ListNotes("work")
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(listed) != 2 {
		t.Errorf("expected 2 notes with prefix 'work', got %d", len(listed))
	}
	for _, ni := range listed {
		if !strings.HasPrefix(ni.Name, "work") {
			t.Errorf("unexpected note outside prefix: %q", ni.Name)
		}
	}
}

func TestListMetadata(t *testing.T) {
	svc := testService(t)

	content := "# My Note\n\n#golang #testing\n\nSee also [[other-note]] and [[guide|Guide]]."
	if err := svc.CreateNote("meta", content); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	listed, err := svc.ListNotes("")
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 note, got %d", len(listed))
	}

	ni := listed[0]

	// Tags
	wantTags := map[string]bool{"golang": true, "testing": true}
	for _, tag := range ni.Tags {
		if !wantTags[tag] {
			t.Errorf("unexpected tag %q", tag)
		}
		delete(wantTags, tag)
	}
	if len(wantTags) > 0 {
		t.Errorf("missing tags: %v", wantTags)
	}

	// Links
	wantLinks := map[string]bool{"other-note": true, "guide": true}
	for _, link := range ni.Links {
		if !wantLinks[link] {
			t.Errorf("unexpected link %q", link)
		}
		delete(wantLinks, link)
	}
	if len(wantLinks) > 0 {
		t.Errorf("missing links: %v", wantLinks)
	}
}

// --- Search ---

func TestSearchViaTFIDF(t *testing.T) {
	svc := testService(t)

	docs := map[string]string{
		"golang":  "Golang concurrency channels goroutines",
		"python":  "Python scripting dynamic typing lambda",
		"systems": "Golang systems programming memory safety",
	}
	for name, content := range docs {
		if err := svc.CreateNote(name, content); err != nil {
			t.Fatalf("CreateNote %q: %v", name, err)
		}
	}

	// Rebuild index synchronously so search results are deterministic.
	svc.rebuildIndex()

	results := svc.SearchNotes("Golang concurrency", 5)
	if len(results) == 0 {
		t.Fatal("expected search results, got none")
	}
	// The "golang" note should score highest — it has both terms.
	if results[0].Filename != "golang.md" {
		t.Errorf("expected 'golang.md' as top result, got %q", results[0].Filename)
	}
	// Score must be positive.
	if results[0].Score <= 0 {
		t.Errorf("expected positive score, got %f", results[0].Score)
	}
	// LineStart must be 1-based.
	if results[0].LineStart < 1 {
		t.Errorf("LineStart should be >= 1, got %d", results[0].LineStart)
	}
}

// --- Extract helpers ---

func TestExtractWikilinks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "simple",
			content: "[[note-a]] and [[note-b]]",
			want:    []string{"note-a", "note-b"},
		},
		{
			name:    "with alias",
			content: "[[real-page|display text]]",
			want:    []string{"real-page"},
		},
		{
			name:    "dedup",
			content: "[[foo]] and [[foo]] again",
			want:    []string{"foo"},
		},
		{
			name:    "none",
			content: "no links here",
			want:    nil,
		},
		{
			name:    "mixed",
			content: "See [[alpha|Alpha Guide]] and [[beta]].",
			want:    []string{"alpha", "beta"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractWikilinks(tc.content)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i, g := range got {
				if g != tc.want[i] {
					t.Errorf("[%d] got %q, want %q", i, g, tc.want[i])
				}
			}
		})
	}
}

func TestExtractTags(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "basic",
			content: "#go #testing",
			want:    []string{"go", "testing"},
		},
		{
			name:    "inline",
			content: "This is #important content with #todo items.",
			want:    []string{"important", "todo"},
		},
		{
			name:    "dedup",
			content: "#go #go #go",
			want:    []string{"go"},
		},
		{
			name:    "hierarchical",
			content: "#project/tetora is great",
			want:    []string{"project/tetora"},
		},
		{
			name:    "none",
			content: "plain text no tags",
			want:    nil,
		},
		{
			name:    "must start with letter",
			content: "#1invalid",
			want:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractTags(tc.content)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i, g := range got {
				if g != tc.want[i] {
					t.Errorf("[%d] got %q, want %q", i, g, tc.want[i])
				}
			}
		})
	}
}

// --- Security ---

func TestPathTraversal(t *testing.T) {
	svc := testService(t)

	cases := []string{
		"../escape",
		"foo/../../etc/passwd",
		"a/b/../../../etc",
	}
	for _, c := range cases {
		if err := svc.CreateNote(c, "bad"); err == nil {
			t.Errorf("expected error for traversal %q, got nil", c)
		}
		if _, err := svc.ReadNote(c); err == nil {
			t.Errorf("expected error for traversal read %q, got nil", c)
		}
	}
}

func TestEmptyName(t *testing.T) {
	svc := testService(t)

	if err := svc.CreateNote("", "data"); err == nil {
		t.Error("expected error for empty name on CreateNote")
	}
	if _, err := svc.ReadNote(""); err == nil {
		t.Error("expected error for empty name on ReadNote")
	}
	if err := svc.AppendNote("", "data"); err == nil {
		t.Error("expected error for empty name on AppendNote")
	}
}

func TestReadNotFound(t *testing.T) {
	svc := testService(t)

	_, err := svc.ReadNote("nonexistent-note")
	if err == nil {
		t.Fatal("expected error for missing note, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

// --- Config ---

func TestConfigDefaults(t *testing.T) {
	t.Run("DefaultExtOrMd empty", func(t *testing.T) {
		c := Config{}
		if c.DefaultExtOrMd() != ".md" {
			t.Errorf("expected .md, got %q", c.DefaultExtOrMd())
		}
	})
	t.Run("DefaultExtOrMd set", func(t *testing.T) {
		c := Config{DefaultExt: ".org"}
		if c.DefaultExtOrMd() != ".org" {
			t.Errorf("expected .org, got %q", c.DefaultExtOrMd())
		}
	})
	t.Run("VaultPathResolved empty uses baseDir/vault", func(t *testing.T) {
		c := Config{}
		got := c.VaultPathResolved("/base")
		if got != "/base/vault" {
			t.Errorf("got %q, want /base/vault", got)
		}
	})
	t.Run("VaultPathResolved absolute unchanged", func(t *testing.T) {
		c := Config{VaultPath: "/absolute/path"}
		got := c.VaultPathResolved("/base")
		if got != "/absolute/path" {
			t.Errorf("got %q, want /absolute/path", got)
		}
	})
	t.Run("VaultPathResolved relative joined to baseDir", func(t *testing.T) {
		c := Config{VaultPath: "myvault"}
		got := c.VaultPathResolved("/base")
		if got != "/base/myvault" {
			t.Errorf("got %q, want /base/myvault", got)
		}
	})
	t.Run("VaultPathResolved tilde expansion", func(t *testing.T) {
		home, _ := os.UserHomeDir()
		c := Config{VaultPath: "~/notes"}
		got := c.VaultPathResolved("/base")
		want := filepath.Join(home, "notes")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// --- Index ---

func TestIndexRebuild(t *testing.T) {
	svc := testService(t)

	notes := []struct{ name, content string }{
		{"alpha", "alpha beta gamma"},
		{"beta", "beta delta epsilon"},
		{"gamma", "gamma zeta eta"},
	}
	for _, n := range notes {
		if err := svc.CreateNote(n.name, n.content); err != nil {
			t.Fatalf("CreateNote %q: %v", n.name, err)
		}
	}

	svc.rebuildIndex()

	if svc.idx.totalDocs != 3 {
		t.Errorf("expected 3 indexed docs, got %d", svc.idx.totalDocs)
	}
	if len(svc.idx.idf) == 0 {
		t.Error("expected non-empty IDF table after rebuild")
	}
}

// --- ValidateNoteName ---

func TestValidateNoteName(t *testing.T) {
	valid := []string{
		"simple",
		"with-dashes",
		"nested/path",
		"deep/nested/note",
		"note.md",
		"note.txt",
	}
	for _, v := range valid {
		if err := ValidateNoteName(v); err != nil {
			t.Errorf("ValidateNoteName(%q) unexpected error: %v", v, err)
		}
	}

	invalid := []string{
		"",
		"../bad",
		"foo/../bar",
		"/absolute",
		".hidden",
		"dir/.hidden",
	}
	for _, inv := range invalid {
		if err := ValidateNoteName(inv); err == nil {
			t.Errorf("ValidateNoteName(%q) expected error, got nil", inv)
		}
	}
}

// --- Ln ---

func TestLn(t *testing.T) {
	cases := []struct {
		x    float64
		want float64
	}{
		{1.0, 0.0},
		{math.E, 1.0},
		{2.0, math.Log(2.0)},
		{10.0, math.Log(10.0)},
		{0.5, math.Log(0.5)},
		{100.0, math.Log(100.0)},
	}

	const epsilon = 1e-9
	for _, tc := range cases {
		got := Ln(tc.x)
		diff := got - tc.want
		if diff < 0 {
			diff = -diff
		}
		if diff > epsilon {
			t.Errorf("Ln(%v) = %v, want %v (diff %e)", tc.x, got, tc.want, diff)
		}
	}

	// Edge cases.
	if Ln(0) != 0 {
		t.Errorf("Ln(0) should return 0, got %v", Ln(0))
	}
	if Ln(-1) != 0 {
		t.Errorf("Ln(-1) should return 0, got %v", Ln(-1))
	}
}
