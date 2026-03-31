package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// --- isTaskBoardEnabled ---

func TestIsTaskBoardEnabled(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"enabled true", `{"taskBoard":{"enabled":true}}`, true},
		{"enabled false", `{"taskBoard":{"enabled":false}}`, false},
		{"missing taskBoard key", `{"other":1}`, false},
		{"taskBoard not an object", `{"taskBoard":"yes"}`, false},
		{"enabled key missing", `{"taskBoard":{}}`, false},
		{"enabled not a bool", `{"taskBoard":{"enabled":1}}`, false},
		{"empty json", `{}`, false},
		{"invalid json", `{not json}`, false},
		{"empty input", ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isTaskBoardEnabled([]byte(c.input))
			if got != c.want {
				t.Errorf("isTaskBoardEnabled(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

// --- detectPhases ---

func TestDetectPhases(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "explicit Phase N headers",
			content: "# Project\n\n## Phase 1\n\n## Phase 2\n\n## Phase 3\n",
			want:    []string{"1", "2", "3"},
		},
		{
			name:    "Phase letter headers",
			content: "## Phase A\n## Phase B\n## Phase C\n",
			want:    []string{"A", "B", "C"},
		},
		{
			name:    "PHASE uppercase",
			content: "## PHASE 1\n## PHASE 2\n",
			want:    []string{"1", "2"},
		},
		{
			name:    "version-style headers",
			content: "## v1.0\n## v2.3.1\n",
			want:    []string{"v1.0", "v2.3.1"},
		},
		{
			name:    "generic readme headers not matched",
			content: "## Overview\n## Installation\n## Usage\n## Contributing\n## License\n## API\n## Requirements\n",
			want:    nil,
		},
		{
			name:    "mixed: phases and generic headers",
			content: "## Overview\n## Phase 1\n## Installation\n## Phase 2\n## License\n",
			want:    []string{"1", "2"},
		},
		{
			name:    "no headers at all",
			content: "Just some plain text\nNo headers here\n",
			want:    nil,
		},
		{
			name:    "empty file",
			content: "",
			want:    nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "roadmap-*.md")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(c.content); err != nil {
				t.Fatal(err)
			}
			f.Close()

			got := detectPhases(f.Name())
			if len(got) != len(c.want) {
				t.Errorf("detectPhases() = %v, want %v", got, c.want)
				return
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("detectPhases()[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestDetectPhases_fileNotFound(t *testing.T) {
	got := detectPhases("/nonexistent/path/roadmap.md")
	if len(got) != 0 {
		t.Errorf("expected nil/empty for missing file, got %v", got)
	}
}

// --- findRoadmapFiles ---

func TestFindRoadmapFiles(t *testing.T) {
	dir := t.TempDir()

	// Create some files
	files := []string{"README.md", "ROADMAP.md", "CLAUDE.md"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("# content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Create tasks/ subdir with roadmap
	tasksDir := filepath.Join(dir, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "roadmap-v3.md"), []byte("# Roadmap"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := findRoadmapFiles(dir)

	// Check all expected files are found
	expected := map[string]bool{
		"README.md":            false,
		"ROADMAP.md":           false,
		"CLAUDE.md":            false,
		"tasks/roadmap-v3.md":  false,
	}
	for _, f := range found {
		expected[f] = true
	}
	for name, hit := range expected {
		if !hit {
			t.Errorf("expected %q in findRoadmapFiles result, not found. Got: %v", name, found)
		}
	}
}

func TestFindRoadmapFiles_emptyDir(t *testing.T) {
	dir := t.TempDir()
	found := findRoadmapFiles(dir)
	if len(found) != 0 {
		t.Errorf("expected empty result for empty dir, got %v", found)
	}
}

func TestFindRoadmapFiles_nonexistentDir(t *testing.T) {
	found := findRoadmapFiles("/nonexistent/dir/that/doesnt/exist")
	if len(found) != 0 {
		t.Errorf("expected empty result for nonexistent dir, got %v", found)
	}
}
