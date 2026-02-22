package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// --- Canvas Engine ---

// CanvasEngine provides 3 layers: MCP Apps Host, Built-in Canvas Tools, Interactive Canvas.
type CanvasEngine struct {
	sessions map[string]*CanvasSession
	mu       sync.RWMutex
	cfg      *Config
	mcpHost  *MCPHost
}

// CanvasSession represents a single canvas instance.
type CanvasSession struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"` // HTML
	Width     string    `json:"width"`
	Height    string    `json:"height"`
	Source    string    `json:"source"`  // "builtin", "mcp"
	MCPServer string    `json:"mcpServer,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CanvasMessage represents a message between canvas iframe and agent.
type CanvasMessage struct {
	SessionID string          `json:"sessionId"`
	Message   json.RawMessage `json:"message"`
}

// CanvasConfig configures the canvas engine.
type CanvasConfig struct {
	Enabled         bool   `json:"enabled,omitempty"`
	MaxIframeHeight string `json:"maxIframeHeight,omitempty"`
	AllowScripts    bool   `json:"allowScripts,omitempty"`
	CSP             string `json:"csp,omitempty"`
}

// newCanvasEngine creates a new canvas engine.
func newCanvasEngine(cfg *Config, mcpHost *MCPHost) *CanvasEngine {
	return &CanvasEngine{
		sessions: make(map[string]*CanvasSession),
		cfg:      cfg,
		mcpHost:  mcpHost,
	}
}

// --- L1: MCP Apps Host ---

// discoverMCPCanvas checks if an MCP server provides ui:// resources.
// When a tool response contains _meta.ui/resourceUri, fetch the HTML from MCP.
func (ce *CanvasEngine) discoverMCPCanvas(mcpServerName, resourceURI string) (*CanvasSession, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if ce.mcpHost == nil {
		return nil, fmt.Errorf("mcp host not available")
	}

	// Check if MCP server exists.
	server := ce.mcpHost.getServer(mcpServerName)
	if server == nil {
		return nil, fmt.Errorf("mcp server %q not found", mcpServerName)
	}

	// Fetch resource from MCP server.
	// This is a simplified implementation. In a real scenario, you'd send a
	// JSON-RPC request to the MCP server to fetch the resource content.
	// For now, we'll return a placeholder.
	content := fmt.Sprintf("<p>Canvas from MCP server: %s</p><p>Resource: %s</p>", mcpServerName, resourceURI)

	session := &CanvasSession{
		ID:        newUUID(),
		Title:     fmt.Sprintf("MCP: %s", mcpServerName),
		Content:   content,
		Width:     "100%",
		Height:    "400px",
		Source:    "mcp",
		MCPServer: mcpServerName,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	ce.sessions[session.ID] = session
	logInfo("canvas.mcp.created", "id", session.ID, "server", mcpServerName, "uri", resourceURI)

	return session, nil
}

// --- L2: Built-in Canvas Tools ---

// renderCanvas creates a new canvas session from agent-generated HTML.
func (ce *CanvasEngine) renderCanvas(title, content, width, height string) (*CanvasSession, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// Validate inputs.
	if title == "" {
		title = "Canvas"
	}
	if width == "" {
		width = "100%"
	}
	if height == "" {
		height = "400px"
	}

	// Apply CSP and sanitization if configured.
	if !ce.cfg.Canvas.AllowScripts {
		// Simple script tag removal (naive implementation).
		// In production, use a proper HTML sanitizer.
		content = stripScriptTags(content)
	}

	session := &CanvasSession{
		ID:        newUUID(),
		Title:     title,
		Content:   content,
		Width:     width,
		Height:    height,
		Source:    "builtin",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	ce.sessions[session.ID] = session
	logInfo("canvas.render", "id", session.ID, "title", title)

	return session, nil
}

// updateCanvas updates an existing canvas session's content.
func (ce *CanvasEngine) updateCanvas(id, content string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	session, ok := ce.sessions[id]
	if !ok {
		return fmt.Errorf("canvas session %q not found", id)
	}

	// Apply sanitization.
	if !ce.cfg.Canvas.AllowScripts {
		content = stripScriptTags(content)
	}

	session.Content = content
	session.UpdatedAt = time.Now()

	logInfo("canvas.update", "id", id)
	return nil
}

// closeCanvas closes a canvas session.
func (ce *CanvasEngine) closeCanvas(id string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if _, ok := ce.sessions[id]; !ok {
		return fmt.Errorf("canvas session %q not found", id)
	}

	delete(ce.sessions, id)
	logInfo("canvas.close", "id", id)
	return nil
}

// getCanvas retrieves a canvas session by ID.
func (ce *CanvasEngine) getCanvas(id string) (*CanvasSession, error) {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	session, ok := ce.sessions[id]
	if !ok {
		return nil, fmt.Errorf("canvas session %q not found", id)
	}

	return session, nil
}

// listCanvasSessions returns all active canvas sessions.
func (ce *CanvasEngine) listCanvasSessions() []*CanvasSession {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	sessions := make([]*CanvasSession, 0, len(ce.sessions))
	for _, s := range ce.sessions {
		sessions = append(sessions, s)
	}

	return sessions
}

// --- L3: Interactive Canvas ---

// handleCanvasMessage handles messages from canvas iframe to agent.
// This allows interactive canvas to send user input back to the agent session.
func (ce *CanvasEngine) handleCanvasMessage(sessionID string, message json.RawMessage) error {
	ce.mu.RLock()
	session, ok := ce.sessions[sessionID]
	ce.mu.RUnlock()

	if !ok {
		return fmt.Errorf("canvas session %q not found", sessionID)
	}

	// Log the message for now. In a full implementation, this would:
	// 1. Look up the agent session associated with this canvas
	// 2. Inject the message into that session's context
	// 3. Trigger the agent to process the message
	logInfo("canvas.message", "sessionId", sessionID, "source", session.Source, "message", string(message))

	return nil
}

// --- Helper Functions ---

// stripScriptTags removes <script> tags from HTML (naive implementation).
// In production, use a proper HTML sanitizer like bluemonday.
func stripScriptTags(html string) string {
	// Simple implementation: remove everything between <script> and </script>
	result := html
	for {
		start := findIgnoreCase(result, "<script")
		if start == -1 {
			break
		}
		end := findIgnoreCase(result[start:], "</script>")
		if end == -1 {
			// Unclosed script tag, remove to end.
			result = result[:start]
			break
		}
		end += start + len("</script>")
		result = result[:start] + result[end:]
	}
	return result
}

// findIgnoreCase finds the first occurrence of substr in s (case-insensitive).
// Returns -1 if not found.
func findIgnoreCase(s, substr string) int {
	sLower := toLower(s)
	substrLower := toLower(substr)
	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		if sLower[i:i+len(substrLower)] == substrLower {
			return i
		}
	}
	return -1
}

// toLower converts a string to lowercase (ASCII only, simplified).
func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c = c + ('a' - 'A')
		}
		result[i] = c
	}
	return string(result)
}

// --- Tool Handlers (registered in tool.go or http.go) ---

// toolCanvasRender is the handler for canvas_render tool.
func toolCanvasRender(ctx context.Context, ce *CanvasEngine) ToolHandler {
	return func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
		var args struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			Width   string `json:"width"`
			Height  string `json:"height"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.Content == "" {
			return "", fmt.Errorf("content is required")
		}

		session, err := ce.renderCanvas(args.Title, args.Content, args.Width, args.Height)
		if err != nil {
			return "", err
		}

		result := map[string]any{
			"id":      session.ID,
			"title":   session.Title,
			"message": "Canvas rendered successfully",
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}
}

// toolCanvasUpdate is the handler for canvas_update tool.
func toolCanvasUpdate(ctx context.Context, ce *CanvasEngine) ToolHandler {
	return func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" || args.Content == "" {
			return "", fmt.Errorf("id and content are required")
		}

		if err := ce.updateCanvas(args.ID, args.Content); err != nil {
			return "", err
		}

		result := map[string]any{
			"id":      args.ID,
			"message": "Canvas updated successfully",
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}
}

// toolCanvasClose is the handler for canvas_close tool.
func toolCanvasClose(ctx context.Context, ce *CanvasEngine) ToolHandler {
	return func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" {
			return "", fmt.Errorf("id is required")
		}

		if err := ce.closeCanvas(args.ID); err != nil {
			return "", err
		}

		result := map[string]any{
			"id":      args.ID,
			"message": "Canvas closed successfully",
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}
}

// registerCanvasTools registers canvas tools with the tool registry.
// This is called from http.go after creating the canvasEngine.
func registerCanvasTools(registry *ToolRegistry, ce *CanvasEngine, cfg *Config) {
	// Check which canvas tools are enabled.
	enabled := func(name string) bool {
		if cfg.Tools.Builtin == nil {
			return true // default: all enabled
		}
		e, ok := cfg.Tools.Builtin[name]
		return !ok || e
	}

	if enabled("canvas_render") {
		registry.Register(&ToolDef{
			Name:        "canvas_render",
			Description: "Render HTML/SVG content in dashboard canvas panel",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Canvas title"},
					"content": {"type": "string", "description": "HTML/SVG content to render"},
					"width": {"type": "string", "description": "Canvas width (e.g., '100%', '800px'). Default: 100%"},
					"height": {"type": "string", "description": "Canvas height (e.g., '400px', '600px'). Default: 400px"}
				},
				"required": ["content"]
			}`),
			Handler: toolCanvasRender(context.Background(), ce),
			Builtin: true,
		})
	}

	if enabled("canvas_update") {
		registry.Register(&ToolDef{
			Name:        "canvas_update",
			Description: "Update existing canvas content",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Canvas session ID"},
					"content": {"type": "string", "description": "New HTML/SVG content"}
				},
				"required": ["id", "content"]
			}`),
			Handler: toolCanvasUpdate(context.Background(), ce),
			Builtin: true,
		})
	}

	if enabled("canvas_close") {
		registry.Register(&ToolDef{
			Name:        "canvas_close",
			Description: "Close a canvas panel",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Canvas session ID to close"}
				},
				"required": ["id"]
			}`),
			Handler: toolCanvasClose(context.Background(), ce),
			Builtin: true,
		})
	}
}
