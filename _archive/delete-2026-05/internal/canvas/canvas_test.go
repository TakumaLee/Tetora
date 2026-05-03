package canvas

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// --- Engine Tests ---

func TestEngine_Render(t *testing.T) {
	cfg := CanvasConfig{
		Enabled:      true,
		AllowScripts: false,
	}
	ce := New(cfg, nil)

	session, err := ce.Render("Test Canvas", "<p>Hello World</p>", "800px", "600px")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
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

func TestEngine_Update(t *testing.T) {
	cfg := CanvasConfig{
		Enabled:      true,
		AllowScripts: false,
	}
	ce := New(cfg, nil)

	session, err := ce.Render("Test", "<p>Original</p>", "", "")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	err = ce.Update(session.ID, "<p>Updated</p>")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	updated, err := ce.Get(session.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if updated.Content != "<p>Updated</p>" {
		t.Errorf("expected content '<p>Updated</p>', got %q", updated.Content)
	}
}

func TestEngine_Close(t *testing.T) {
	cfg := CanvasConfig{Enabled: true}
	ce := New(cfg, nil)

	session, err := ce.Render("Test", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	err = ce.Close(session.ID)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err = ce.Get(session.ID)
	if err == nil {
		t.Error("expected error when getting closed session, got nil")
	}
}

func TestEngine_List(t *testing.T) {
	cfg := CanvasConfig{Enabled: true}
	ce := New(cfg, nil)

	ce.Render("Canvas 1", "<p>Test 1</p>", "", "")
	ce.Render("Canvas 2", "<p>Test 2</p>", "", "")
	ce.Render("Canvas 3", "<p>Test 3</p>", "", "")

	sessions := ce.List()
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestEngine_Get(t *testing.T) {
	cfg := CanvasConfig{Enabled: true}
	ce := New(cfg, nil)

	session, err := ce.Render("Test", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	retrieved, err := ce.Get(session.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("expected ID %q, got %q", session.ID, retrieved.ID)
	}
}

func TestEngine_NotFound(t *testing.T) {
	cfg := CanvasConfig{Enabled: true}
	ce := New(cfg, nil)

	_, err := ce.Get("nonexistent")
	if err == nil {
		t.Error("expected error when getting nonexistent session, got nil")
	}

	err = ce.Update("nonexistent", "<p>Test</p>")
	if err == nil {
		t.Error("expected error when updating nonexistent session, got nil")
	}

	err = ce.Close("nonexistent")
	if err == nil {
		t.Error("expected error when closing nonexistent session, got nil")
	}
}

func TestEngine_Defaults(t *testing.T) {
	cfg := CanvasConfig{}
	ce := New(cfg, nil)

	session, err := ce.Render("", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
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

// --- Tool Handler Tests ---

func TestHandlerRender(t *testing.T) {
	cfg := CanvasConfig{
		Enabled:      true,
		AllowScripts: false,
	}
	ce := New(cfg, nil)

	handler := HandlerRender(ce)

	input := json.RawMessage(`{
		"title": "Test Canvas",
		"content": "<div>Test Content</div>",
		"width": "1000px",
		"height": "500px"
	}`)

	result, err := handler(context.Background(), input)
	if err != nil {
		t.Fatalf("HandlerRender failed: %v", err)
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

func TestHandlerUpdate(t *testing.T) {
	cfg := CanvasConfig{Enabled: true}
	ce := New(cfg, nil)

	session, err := ce.Render("Test", "<p>Original</p>", "", "")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	handler := HandlerUpdate(ce)

	input := json.RawMessage(fmt.Sprintf(`{
		"id": "%s",
		"content": "<p>Updated Content</p>"
	}`, session.ID))

	result, err := handler(context.Background(), input)
	if err != nil {
		t.Fatalf("HandlerUpdate failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	if res["id"] != session.ID {
		t.Errorf("expected id %q, got %v", session.ID, res["id"])
	}

	updated, _ := ce.Get(session.ID)
	if updated.Content != "<p>Updated Content</p>" {
		t.Errorf("expected updated content, got %q", updated.Content)
	}
}

func TestHandlerClose(t *testing.T) {
	cfg := CanvasConfig{Enabled: true}
	ce := New(cfg, nil)

	session, err := ce.Render("Test", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	handler := HandlerClose(ce)

	input := json.RawMessage(fmt.Sprintf(`{
		"id": "%s"
	}`, session.ID))

	result, err := handler(context.Background(), input)
	if err != nil {
		t.Fatalf("HandlerClose failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	if res["id"] != session.ID {
		t.Errorf("expected id %q, got %v", session.ID, res["id"])
	}

	_, err = ce.Get(session.ID)
	if err == nil {
		t.Error("expected error when getting closed session")
	}
}

// --- StripScriptTags Tests ---

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
		result := StripScriptTags(tc.input)
		if result != tc.expected {
			t.Errorf("StripScriptTags(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

// --- MCP Host Interface Test ---

// stubMCPHost implements MCPHost for testing.
type stubMCPHost struct {
	servers map[string]MCPServer
}

// stubMCPServer implements MCPServer for testing.
type stubMCPServer struct{}

func (h *stubMCPHost) GetServer(name string) MCPServer {
	return h.servers[name]
}

func TestEngine_DiscoverMCPCanvas_NoHost(t *testing.T) {
	ce := New(CanvasConfig{}, nil)
	_, err := ce.DiscoverMCPCanvas("server", "ui://test")
	if err == nil {
		t.Error("expected error when mcp host is nil")
	}
}

func TestEngine_DiscoverMCPCanvas_ServerNotFound(t *testing.T) {
	host := &stubMCPHost{servers: map[string]MCPServer{}}
	ce := New(CanvasConfig{}, host)
	_, err := ce.DiscoverMCPCanvas("unknown", "ui://test")
	if err == nil {
		t.Error("expected error when server not found")
	}
}

func TestEngine_DiscoverMCPCanvas_Success(t *testing.T) {
	host := &stubMCPHost{servers: map[string]MCPServer{
		"my-server": &stubMCPServer{},
	}}
	ce := New(CanvasConfig{}, host)

	session, err := ce.DiscoverMCPCanvas("my-server", "ui://dashboard")
	if err != nil {
		t.Fatalf("DiscoverMCPCanvas failed: %v", err)
	}

	if session.Source != "mcp" {
		t.Errorf("expected source 'mcp', got %q", session.Source)
	}
	if session.MCPServer != "my-server" {
		t.Errorf("expected MCPServer 'my-server', got %q", session.MCPServer)
	}
}
