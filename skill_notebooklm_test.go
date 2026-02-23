package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// --- P21.7: NotebookLM Skill Tests ---

func TestNotebookLMImport_NoRelay(t *testing.T) {
	old := globalBrowserRelay
	globalBrowserRelay = nil
	defer func() { globalBrowserRelay = old }()

	input, _ := json.Marshal(map[string]any{
		"notebook_url": "https://notebooklm.google.com/notebook/abc",
		"urls":         []string{"https://example.com"},
	})
	_, err := toolNotebookLMImport(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when relay is nil")
	}
	if !strings.Contains(err.Error(), "browser extension not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMListSources_NoRelay(t *testing.T) {
	old := globalBrowserRelay
	globalBrowserRelay = nil
	defer func() { globalBrowserRelay = old }()

	_, err := toolNotebookLMListSources(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when relay is nil")
	}
	if !strings.Contains(err.Error(), "browser extension not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMQuery_NoRelay(t *testing.T) {
	old := globalBrowserRelay
	globalBrowserRelay = nil
	defer func() { globalBrowserRelay = old }()

	input, _ := json.Marshal(map[string]any{
		"question": "What is the summary?",
	})
	_, err := toolNotebookLMQuery(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when relay is nil")
	}
	if !strings.Contains(err.Error(), "browser extension not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMQuery_EmptyQuestion(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"question": "",
	})
	_, err := toolNotebookLMQuery(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error for empty question")
	}
	if !strings.Contains(err.Error(), "question is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMDeleteSource_NoArgs(t *testing.T) {
	input, _ := json.Marshal(map[string]any{})
	_, err := toolNotebookLMDeleteSource(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when neither source_name nor source_id provided")
	}
	if !strings.Contains(err.Error(), "source_name or source_id is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMImport_NoURLs(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"notebook_url": "https://notebooklm.google.com/notebook/abc",
		"urls":         []string{},
	})
	_, err := toolNotebookLMImport(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error for empty urls")
	}
	if !strings.Contains(err.Error(), "urls is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMImport_NoNotebookURL(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"notebook_url": "",
		"urls":         []string{"https://example.com"},
	})
	_, err := toolNotebookLMImport(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error for empty notebook_url")
	}
	if !strings.Contains(err.Error(), "notebook_url is required") {
		t.Errorf("unexpected error: %v", err)
	}
}
