package canvas

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// --- Interfaces ---

// MCPHost is the interface canvas uses to interact with the MCP host.
// The root package satisfies this with its concrete *MCPHost.
type MCPHost interface {
	// GetServer returns the server with the given name, or nil if not found.
	GetServer(name string) MCPServer
}

// MCPServer is a marker interface for an MCP server instance.
// canvas only needs to test for nil (server existence); no methods are called.
type MCPServer interface{}

// ToolRegistry is the interface canvas uses to register tools.
// The root package satisfies this with its concrete *ToolRegistry.
type ToolRegistry interface {
	RegisterCanvas(name, description string, schema json.RawMessage, handler Handler)
}

// Handler is the canvas-internal tool handler signature.
// It deliberately omits the cfg parameter because canvas carries its own
// CanvasConfig and does not need the root *Config at call time.
type Handler func(ctx context.Context, input json.RawMessage) (string, error)

// --- Config ---

// CanvasConfig configures the canvas engine.
type CanvasConfig struct {
	Enabled         bool   `json:"enabled,omitempty"`
	MaxIframeHeight string `json:"maxIframeHeight,omitempty"`
	AllowScripts    bool   `json:"allowScripts,omitempty"`
	CSP             string `json:"csp,omitempty"`
}

// --- Types ---

// Engine provides 3 layers: MCP Apps Host, Built-in Canvas Tools, Interactive Canvas.
type Engine struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	cfg      CanvasConfig
	mcpHost  MCPHost
}

// Session represents a single canvas instance.
type Session struct {
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

// Message represents a message between canvas iframe and agent.
type Message struct {
	SessionID string          `json:"sessionId"`
	Message   json.RawMessage `json:"message"`
}

// --- Constructor ---

// New creates a new canvas Engine.
// mcpHost may be nil if MCP integration is not needed.
func New(cfg CanvasConfig, mcpHost MCPHost) *Engine {
	return &Engine{
		sessions: make(map[string]*Session),
		cfg:      cfg,
		mcpHost:  mcpHost,
	}
}

// --- L1: MCP Apps Host ---

// DiscoverMCPCanvas checks if an MCP server provides ui:// resources.
// When a tool response contains _meta.ui/resourceUri, fetch the HTML from MCP.
func (ce *Engine) DiscoverMCPCanvas(mcpServerName, resourceURI string) (*Session, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if ce.mcpHost == nil {
		return nil, fmt.Errorf("mcp host not available")
	}

	// Check if MCP server exists.
	server := ce.mcpHost.GetServer(mcpServerName)
	if server == nil {
		return nil, fmt.Errorf("mcp server %q not found", mcpServerName)
	}

	// Fetch resource from MCP server.
	// This is a simplified implementation. In a real scenario, you'd send a
	// JSON-RPC request to the MCP server to fetch the resource content.
	// For now, we'll return a placeholder.
	content := fmt.Sprintf("<p>Canvas from MCP server: %s</p><p>Resource: %s</p>", mcpServerName, resourceURI)

	session := &Session{
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
	slog.Info("canvas.mcp.created", "id", session.ID, "server", mcpServerName, "uri", resourceURI)

	return session, nil
}

// --- L2: Built-in Canvas Tools ---

// Render creates a new canvas session from agent-generated HTML.
func (ce *Engine) Render(title, content, width, height string) (*Session, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// Apply defaults.
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
	if !ce.cfg.AllowScripts {
		// Simple script tag removal (naive implementation).
		// In production, use a proper HTML sanitizer.
		content = StripScriptTags(content)
	}

	session := &Session{
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
	slog.Info("canvas.render", "id", session.ID, "title", title)

	return session, nil
}

// Update updates an existing canvas session's content.
func (ce *Engine) Update(id, content string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	session, ok := ce.sessions[id]
	if !ok {
		return fmt.Errorf("canvas session %q not found", id)
	}

	// Apply sanitization.
	if !ce.cfg.AllowScripts {
		content = StripScriptTags(content)
	}

	session.Content = content
	session.UpdatedAt = time.Now()

	slog.Info("canvas.update", "id", id)
	return nil
}

// Close closes a canvas session.
func (ce *Engine) Close(id string) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if _, ok := ce.sessions[id]; !ok {
		return fmt.Errorf("canvas session %q not found", id)
	}

	delete(ce.sessions, id)
	slog.Info("canvas.close", "id", id)
	return nil
}

// Get retrieves a canvas session by ID.
func (ce *Engine) Get(id string) (*Session, error) {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	session, ok := ce.sessions[id]
	if !ok {
		return nil, fmt.Errorf("canvas session %q not found", id)
	}

	return session, nil
}

// List returns all active canvas sessions.
func (ce *Engine) List() []*Session {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	sessions := make([]*Session, 0, len(ce.sessions))
	for _, s := range ce.sessions {
		sessions = append(sessions, s)
	}

	return sessions
}

// --- L3: Interactive Canvas ---

// HandleMessage handles messages from canvas iframe to agent.
// This allows interactive canvas to send user input back to the agent session.
func (ce *Engine) HandleMessage(sessionID string, message json.RawMessage) error {
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
	slog.Info("canvas.message", "sessionId", sessionID, "source", session.Source, "message", string(message))

	return nil
}

// --- Tool Handlers ---

// HandlerRender returns a Handler for the canvas_render tool.
func HandlerRender(ce *Engine) Handler {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
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

		session, err := ce.Render(args.Title, args.Content, args.Width, args.Height)
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

// HandlerUpdate returns a Handler for the canvas_update tool.
func HandlerUpdate(ce *Engine) Handler {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
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

		if err := ce.Update(args.ID, args.Content); err != nil {
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

// HandlerClose returns a Handler for the canvas_close tool.
func HandlerClose(ce *Engine) Handler {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" {
			return "", fmt.Errorf("id is required")
		}

		if err := ce.Close(args.ID); err != nil {
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

// RegisterTools registers canvas tools with the provided ToolRegistry.
// The enabledFn callback lets the caller check per-tool enable state.
func RegisterTools(registry ToolRegistry, ce *Engine, enabledFn func(name string) bool) {
	if enabledFn("canvas_render") {
		registry.RegisterCanvas(
			"canvas_render",
			"Render HTML/SVG content in dashboard canvas panel",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Canvas title"},
					"content": {"type": "string", "description": "HTML/SVG content to render"},
					"width": {"type": "string", "description": "Canvas width (e.g., '100%', '800px'). Default: 100%"},
					"height": {"type": "string", "description": "Canvas height (e.g., '400px', '600px'). Default: 400px"}
				},
				"required": ["content"]
			}`),
			HandlerRender(ce),
		)
	}

	if enabledFn("canvas_update") {
		registry.RegisterCanvas(
			"canvas_update",
			"Update existing canvas content",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Canvas session ID"},
					"content": {"type": "string", "description": "New HTML/SVG content"}
				},
				"required": ["id", "content"]
			}`),
			HandlerUpdate(ce),
		)
	}

	if enabledFn("canvas_close") {
		registry.RegisterCanvas(
			"canvas_close",
			"Close a canvas panel",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Canvas session ID to close"}
				},
				"required": ["id"]
			}`),
			HandlerClose(ce),
		)
	}
}

// --- Helper Functions ---

// StripScriptTags removes <script> tags from HTML (naive implementation).
// In production, use a proper HTML sanitizer like bluemonday.
func StripScriptTags(html string) string {
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
	sLower := asciiToLower(s)
	substrLower := asciiToLower(substr)
	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		if sLower[i:i+len(substrLower)] == substrLower {
			return i
		}
	}
	return -1
}

// asciiToLower converts a string to lowercase (ASCII only).
func asciiToLower(s string) string {
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

// newUUID generates a random UUID v4.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
