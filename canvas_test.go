package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestCanvasEngine_NewSession(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled:      true,
			AllowScripts: false,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test Canvas", "<p>Hello World</p>", "800px", "600px")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	if session.ID == "" {
		t.Error("session ID is empty")
	}
	if session.Title != "Test Canvas" {
		t.Errorf("expected title 'Test Canvas', got %q", session.Title)
	}
	if session.Content != "<p>Hello World</p>" {
		t.Errorf("expected content '<p>Hello World</p>', got %q", session.Content)
	}
	if session.Width != "800px" {
		t.Errorf("expected width '800px', got %q", session.Width)
	}
	if session.Height != "600px" {
		t.Errorf("expected height '600px', got %q", session.Height)
	}
	if session.Source != "builtin" {
		t.Errorf("expected source 'builtin', got %q", session.Source)
	}
}

func TestCanvasEngine_UpdateSession(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled:      true,
			AllowScripts: false,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test", "<p>Original</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	err = ce.updateCanvas(session.ID, "<p>Updated</p>")
	if err != nil {
		t.Fatalf("updateCanvas failed: %v", err)
	}

	updated, err := ce.getCanvas(session.ID)
	if err != nil {
		t.Fatalf("getCanvas failed: %v", err)
	}

	if updated.Content != "<p>Updated</p>" {
		t.Errorf("expected content '<p>Updated</p>', got %q", updated.Content)
	}
}

func TestCanvasEngine_CloseSession(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	err = ce.closeCanvas(session.ID)
	if err != nil {
		t.Fatalf("closeCanvas failed: %v", err)
	}

	_, err = ce.getCanvas(session.ID)
	if err == nil {
		t.Error("expected error when getting closed session, got nil")
	}
}

func TestCanvasEngine_ListSessions(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	// Create multiple sessions.
	ce.renderCanvas("Canvas 1", "<p>Test 1</p>", "", "")
	ce.renderCanvas("Canvas 2", "<p>Test 2</p>", "", "")
	ce.renderCanvas("Canvas 3", "<p>Test 3</p>", "", "")

	sessions := ce.listCanvasSessions()
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestCanvasEngine_GetSession(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	retrieved, err := ce.getCanvas(session.ID)
	if err != nil {
		t.Fatalf("getCanvas failed: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("expected ID %q, got %q", session.ID, retrieved.ID)
	}
}

func TestCanvasEngine_NotFound(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	_, err := ce.getCanvas("nonexistent")
	if err == nil {
		t.Error("expected error when getting nonexistent session, got nil")
	}

	err = ce.updateCanvas("nonexistent", "<p>Test</p>")
	if err == nil {
		t.Error("expected error when updating nonexistent session, got nil")
	}

	err = ce.closeCanvas("nonexistent")
	if err == nil {
		t.Error("expected error when closing nonexistent session, got nil")
	}
}

func TestCanvasRender_Tool(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled:      true,
			AllowScripts: false,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	handler := toolCanvasRender(context.Background(), ce)

	input := json.RawMessage(`{
		"title": "Test Canvas",
		"content": "<div>Test Content</div>",
		"width": "1000px",
		"height": "500px"
	}`)

	result, err := handler(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolCanvasRender failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	if res["id"] == "" {
		t.Error("expected id in result")
	}
	if res["title"] != "Test Canvas" {
		t.Errorf("expected title 'Test Canvas', got %v", res["title"])
	}
}

func TestCanvasUpdate_Tool(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	// Create a session first.
	session, err := ce.renderCanvas("Test", "<p>Original</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	handler := toolCanvasUpdate(context.Background(), ce)

	input := json.RawMessage(fmt.Sprintf(`{
		"id": "%s",
		"content": "<p>Updated Content</p>"
	}`, session.ID))

	result, err := handler(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolCanvasUpdate failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	if res["id"] != session.ID {
		t.Errorf("expected id %q, got %v", session.ID, res["id"])
	}

	// Verify the content was updated.
	updated, _ := ce.getCanvas(session.ID)
	if updated.Content != "<p>Updated Content</p>" {
		t.Errorf("expected updated content, got %q", updated.Content)
	}
}

func TestCanvasClose_Tool(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	// Create a session first.
	session, err := ce.renderCanvas("Test", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	handler := toolCanvasClose(context.Background(), ce)

	input := json.RawMessage(fmt.Sprintf(`{
		"id": "%s"
	}`, session.ID))

	result, err := handler(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolCanvasClose failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	if res["id"] != session.ID {
		t.Errorf("expected id %q, got %v", session.ID, res["id"])
	}

	// Verify the session is closed.
	_, err = ce.getCanvas(session.ID)
	if err == nil {
		t.Error("expected error when getting closed session")
	}
}

func TestCanvasConfig_Default(t *testing.T) {
	cfg := &Config{}
	ce := newCanvasEngine(cfg, nil)

	// Test that defaults work.
	session, err := ce.renderCanvas("", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	if session.Title != "Canvas" {
		t.Errorf("expected default title 'Canvas', got %q", session.Title)
	}
	if session.Width != "100%" {
		t.Errorf("expected default width '100%%', got %q", session.Width)
	}
	if session.Height != "400px" {
		t.Errorf("expected default height '400px', got %q", session.Height)
	}
}

func TestStripScriptTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "<p>Hello</p><script>alert('xss')</script><p>World</p>",
			expected: "<p>Hello</p><p>World</p>",
		},
		{
			input:    "<SCRIPT>alert('xss')</SCRIPT>",
			expected: "",
		},
		{
			input:    "<p>No scripts here</p>",
			expected: "<p>No scripts here</p>",
		},
		{
			input:    "<script src='evil.js'></script><div>Content</div>",
			expected: "<div>Content</div>",
		},
	}

	for _, tc := range tests {
		result := stripScriptTags(tc.input)
		if result != tc.expected {
			t.Errorf("stripScriptTags(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}
