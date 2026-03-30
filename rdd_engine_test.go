package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureStateFile(t *testing.T) {
	tmpDir := t.TempDir()

	err := EnsureStateFile(tmpDir, "STATE.md", "Test Objective")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "STATE.md"))
	if err != nil {
		t.Fatalf("failed to read created state file: %v", err)
	}

	strContent := string(content)
	if !strings.Contains(strContent, "Test Objective") {
		t.Errorf("state file should contain objective, got:\n%s", strContent)
	}
	if !strings.Contains(strContent, "PENDING: Initial project setup") {
		t.Errorf("state file should contain pending status, got:\n%s", strContent)
	}

	// Test with existing file (should not overwrite)
	err = os.WriteFile(filepath.Join(tmpDir, "STATE.md"), []byte("Existing Content"), 0o644)
	if err != nil {
		t.Fatalf("failed to write existing file: %v", err)
	}

	err = EnsureStateFile(tmpDir, "STATE.md", "New Objective")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content2, _ := os.ReadFile(filepath.Join(tmpDir, "STATE.md"))
	if string(content2) != "Existing Content" {
		t.Errorf("EnsureStateFile should not overwrite existing file")
	}
}

func TestBuildResumeContext(t *testing.T) {
	tmpDir := t.TempDir()

	// Missing state file
	_, err := BuildResumeContext(tmpDir, "STATE.md")
	if err == nil {
		t.Errorf("expected error when state file is missing")
	}

	// Create state file
	os.WriteFile(filepath.Join(tmpDir, "STATE.md"), []byte("State Content"), 0o644)

	ctx, err := BuildResumeContext(tmpDir, "STATE.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(ctx, "State Content") {
		t.Errorf("context should contain state content")
	}

	// Add requirements file
	os.WriteFile(filepath.Join(tmpDir, "REQUIREMENTS.md"), []byte("Req Content"), 0o644)
	ctx2, err := BuildResumeContext(tmpDir, "STATE.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(ctx2, "Req Content") {
		t.Errorf("context should contain requirements content")
	}
}
