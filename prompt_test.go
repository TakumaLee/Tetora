package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndReadPrompt(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}

	if err := writePrompt(cfg, "test-prompt", "# Hello\nThis is a test."); err != nil {
		t.Fatalf("writePrompt: %v", err)
	}

	content, err := readPrompt(cfg, "test-prompt")
	if err != nil {
		t.Fatalf("readPrompt: %v", err)
	}
	if content != "# Hello\nThis is a test." {
		t.Errorf("got %q", content)
	}
}

func TestReadPromptNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}

	_, err := readPrompt(cfg, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent prompt")
	}
}

func TestListPrompts(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}

	// Empty dir.
	prompts, err := listPrompts(cfg)
	if err != nil {
		t.Fatalf("listPrompts empty: %v", err)
	}
	if len(prompts) != 0 {
		t.Errorf("expected 0 prompts, got %d", len(prompts))
	}

	// Add some prompts.
	writePrompt(cfg, "alpha", "Alpha content")
	writePrompt(cfg, "beta", "Beta content that is a bit longer for preview testing")

	prompts, err = listPrompts(cfg)
	if err != nil {
		t.Fatalf("listPrompts: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(prompts))
	}
	// Sorted alphabetically.
	if prompts[0].Name != "alpha" {
		t.Errorf("first prompt = %q, want alpha", prompts[0].Name)
	}
	if prompts[1].Name != "beta" {
		t.Errorf("second prompt = %q, want beta", prompts[1].Name)
	}
}

func TestDeletePrompt(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}

	writePrompt(cfg, "to-delete", "content")

	if err := deletePrompt(cfg, "to-delete"); err != nil {
		t.Fatalf("deletePrompt: %v", err)
	}

	// Should be gone.
	_, err := readPrompt(cfg, "to-delete")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestDeletePromptNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}

	err := deletePrompt(cfg, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent prompt")
	}
}

func TestWritePromptInvalidName(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}

	err := writePrompt(cfg, "bad/name", "content")
	if err == nil {
		t.Error("expected error for invalid name with /")
	}

	err = writePrompt(cfg, "bad name", "content")
	if err == nil {
		t.Error("expected error for name with space")
	}

	err = writePrompt(cfg, "", "content")
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestResolvePromptFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}

	writePrompt(cfg, "my-prompt", "resolved content here")

	// With .md extension.
	content, err := resolvePromptFile(cfg, "my-prompt.md")
	if err != nil {
		t.Fatalf("resolvePromptFile with .md: %v", err)
	}
	if content != "resolved content here" {
		t.Errorf("got %q", content)
	}

	// Without .md extension.
	content, err = resolvePromptFile(cfg, "my-prompt")
	if err != nil {
		t.Fatalf("resolvePromptFile without .md: %v", err)
	}
	if content != "resolved content here" {
		t.Errorf("got %q", content)
	}

	// Empty promptFile.
	content, err = resolvePromptFile(cfg, "")
	if err != nil {
		t.Fatalf("resolvePromptFile empty: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty, got %q", content)
	}
}

func TestListPromptsIgnoresNonMd(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}
	promptDir := filepath.Join(dir, "prompts")
	os.MkdirAll(promptDir, 0o755)

	// Create a .md and a .txt file.
	os.WriteFile(filepath.Join(promptDir, "valid.md"), []byte("valid"), 0o644)
	os.WriteFile(filepath.Join(promptDir, "ignored.txt"), []byte("ignored"), 0o644)

	prompts, _ := listPrompts(cfg)
	if len(prompts) != 1 {
		t.Errorf("expected 1 prompt, got %d", len(prompts))
	}
}
