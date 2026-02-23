package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// --- Tool Types ---

// ToolDef defines a tool that can be called by agents.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Handler     ToolHandler     `json:"-"`
	Builtin     bool            `json:"-"`
	RequireAuth bool            `json:"requireAuth,omitempty"`
}

// ToolCall represents a tool invocation request from the provider.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ToolHandler is a function that executes a tool.
type ToolHandler func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error)

// --- Tool Registry ---

// ToolRegistry manages available tools.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*ToolDef
}

// NewToolRegistry creates a new tool registry with built-in tools.
func NewToolRegistry(cfg *Config) *ToolRegistry {
	r := &ToolRegistry{
		tools: make(map[string]*ToolDef),
	}
	r.registerBuiltins(cfg)
	return r
}

// Register adds a tool to the registry.
func (r *ToolRegistry) Register(tool *ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = tool
}

// Get retrieves a tool by name.
func (r *ToolRegistry) Get(name string) (*ToolDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all registered tools.
func (r *ToolRegistry) List() []*ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ListForProvider serializes tools for API calls (no Handler field).
func (r *ToolRegistry) ListForProvider() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]map[string]any, 0, len(r.tools))
	for _, t := range r.tools {
		var schema map[string]any
		if len(t.InputSchema) > 0 {
			json.Unmarshal(t.InputSchema, &schema)
		}
		result = append(result, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": schema,
		})
	}
	return result
}

// --- Built-in Tools ---

func (r *ToolRegistry) registerBuiltins(cfg *Config) {
	// Check which built-in tools are enabled.
	enabled := func(name string) bool {
		if cfg.Tools.Builtin == nil {
			return true // default: all enabled
		}
		e, ok := cfg.Tools.Builtin[name]
		return !ok || e
	}

	if enabled("exec") {
		r.Register(&ToolDef{
			Name:        "exec",
			Description: "Execute a shell command and return stdout, stderr, and exit code",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "Shell command to execute"},
					"workdir": {"type": "string", "description": "Working directory (optional)"},
					"timeout": {"type": "number", "description": "Timeout in seconds (default 60)"}
				},
				"required": ["command"]
			}`),
			Handler:     toolExec,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("read") {
		r.Register(&ToolDef{
			Name:        "read",
			Description: "Read file contents with optional line range",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to read"},
					"offset": {"type": "number", "description": "Start line (0-indexed, optional)"},
					"limit": {"type": "number", "description": "Number of lines to read (optional)"}
				},
				"required": ["path"]
			}`),
			Handler: toolRead,
			Builtin: true,
		})
	}

	if enabled("write") {
		r.Register(&ToolDef{
			Name:        "write",
			Description: "Write content to a file (creates or overwrites)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to write"},
					"content": {"type": "string", "description": "Content to write"}
				},
				"required": ["path", "content"]
			}`),
			Handler:     toolWrite,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("edit") {
		r.Register(&ToolDef{
			Name:        "edit",
			Description: "Replace text in a file using string substitution",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "File path to edit"},
					"old_string": {"type": "string", "description": "Text to find"},
					"new_string": {"type": "string", "description": "Replacement text"}
				},
				"required": ["path", "old_string", "new_string"]
			}`),
			Handler:     toolEdit,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("web_search") && cfg.Tools.WebSearch.Provider != "" {
		r.Register(&ToolDef{
			Name:        "web_search",
			Description: "Search the web using configured search provider (Brave, Tavily, or SearXNG)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"maxResults": {"type": "number", "description": "Maximum number of results (default 5)"}
				},
				"required": ["query"]
			}`),
			Handler: toolWebSearch,
			Builtin: true,
		})
	}

	if enabled("web_fetch") {
		r.Register(&ToolDef{
			Name:        "web_fetch",
			Description: "Fetch a URL and return plain text content (HTML tags stripped)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL to fetch"},
					"maxLength": {"type": "number", "description": "Maximum length in characters (default 50000)"}
				},
				"required": ["url"]
			}`),
			Handler: toolWebFetch,
			Builtin: true,
		})
	}

	if enabled("memory_search") {
		r.Register(&ToolDef{
			Name:        "memory_search",
			Description: "Search agent memory by query",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"role": {"type": "string", "description": "Filter by role name (optional)"}
				},
				"required": ["query"]
			}`),
			Handler: toolMemorySearch,
			Builtin: true,
		})
	}

	if enabled("memory_get") {
		r.Register(&ToolDef{
			Name:        "memory_get",
			Description: "Get a specific memory value by key",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"key": {"type": "string", "description": "Memory key"},
					"role": {"type": "string", "description": "Role name (optional)"}
				},
				"required": ["key"]
			}`),
			Handler: toolMemoryGet,
			Builtin: true,
		})
	}

	if enabled("knowledge_search") {
		r.Register(&ToolDef{
			Name:        "knowledge_search",
			Description: "Search knowledge base using TF-IDF",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"limit": {"type": "number", "description": "Max results (default 5)"}
				},
				"required": ["query"]
			}`),
			Handler: toolKnowledgeSearch,
			Builtin: true,
		})
	}

	if enabled("session_list") {
		r.Register(&ToolDef{
			Name:        "session_list",
			Description: "List active sessions",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"channel": {"type": "string", "description": "Filter by channel (optional)"}
				}
			}`),
			Handler: toolSessionList,
			Builtin: true,
		})
	}

	if enabled("message") {
		r.Register(&ToolDef{
			Name:        "message",
			Description: "Send a message to a channel (Telegram, Slack, Discord)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"channel": {"type": "string", "description": "Channel type: telegram, slack, discord"},
					"message": {"type": "string", "description": "Message content"}
				},
				"required": ["channel", "message"]
			}`),
			Handler: toolMessage,
			Builtin: true,
		})
	}

	if enabled("cron_list") {
		r.Register(&ToolDef{
			Name:        "cron_list",
			Description: "List scheduled cron jobs",
			InputSchema: json.RawMessage(`{"type": "object"}`),
			Handler:     toolCronList,
			Builtin:     true,
		})
	}

	if enabled("cron_create") {
		r.Register(&ToolDef{
			Name:        "cron_create",
			Description: "Create or update a cron job",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Job name"},
					"schedule": {"type": "string", "description": "Cron schedule or interval (e.g., '@hourly', '*/5m')"},
					"prompt": {"type": "string", "description": "Task prompt"},
					"role": {"type": "string", "description": "Agent role (optional)"}
				},
				"required": ["name", "schedule", "prompt"]
			}`),
			Handler:     toolCronCreate,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("cron_delete") {
		r.Register(&ToolDef{
			Name:        "cron_delete",
			Description: "Delete a cron job by name",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Job name to delete"}
				},
				"required": ["name"]
			}`),
			Handler:     toolCronDelete,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("agent_list") {
		r.Register(&ToolDef{
			Name:        "agent_list",
			Description: "List available agents/roles with their capabilities",
			InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
			Handler:     toolAgentList,
			Builtin:     true,
		})
	}

	if enabled("agent_dispatch") {
		r.Register(&ToolDef{
			Name:        "agent_dispatch",
			Description: "Dispatch a sub-task to another agent and wait for the result",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"role": {"type": "string", "description": "Target agent role name"},
					"prompt": {"type": "string", "description": "Task prompt to send"},
					"timeout": {"type": "number", "description": "Timeout in seconds (default 300)"}
				},
				"required": ["role", "prompt"]
			}`),
			Handler:     toolAgentDispatch,
			Builtin:     true,
		})
	}

	if enabled("agent_message") {
		r.Register(&ToolDef{
			Name:        "agent_message",
			Description: "Send an async message to another agent's session",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"role": {"type": "string", "description": "Target agent role name"},
					"message": {"type": "string", "description": "Message content"},
					"sessionId": {"type": "string", "description": "Target session ID (optional)"}
				},
				"required": ["role", "message"]
			}`),
			Handler:     toolAgentMessage,
			Builtin:     true,
		})
	}

	// --- P13.1: Plugin System --- Code Mode meta-tools.
	if enabled("search_tools") {
		r.Register(&ToolDef{
			Name:        "search_tools",
			Description: "Search available tools by keyword (name or description). Use when there are many tools and you need to find the right one.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Keyword to search for in tool names and descriptions"},
					"limit": {"type": "number", "description": "Maximum results to return (default 10)"}
				},
				"required": ["query"]
			}`),
			Handler: toolSearchTools,
			Builtin: true,
		})
	}

	if enabled("execute_tool") {
		r.Register(&ToolDef{
			Name:        "execute_tool",
			Description: "Execute any registered tool by name with given input. Use with search_tools to discover and run tools dynamically.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Tool name to execute"},
					"input": {"type": "object", "description": "Input parameters for the tool"}
				},
				"required": ["name"]
			}`),
			Handler:     toolExecuteTool,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	// --- P18.4: Self-Improving Skills ---
	if enabled("create_skill") {
		r.Register(&ToolDef{
			Name:        "create_skill",
			Description: "Create a new reusable skill (shell script or Python script) that can be used in future tasks. The skill will need approval before it can execute unless autoApprove is enabled.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Skill name (alphanumeric and hyphens only, max 64 chars)"},
					"description": {"type": "string", "description": "What the skill does"},
					"script": {"type": "string", "description": "The script content (bash or python)"},
					"language": {"type": "string", "enum": ["bash", "python"], "description": "Script language (default: bash)"},
					"matcher": {"type": "object", "properties": {"roles": {"type": "array", "items": {"type": "string"}}, "keywords": {"type": "array", "items": {"type": "string"}}}, "description": "Conditions for auto-injecting this skill"}
				},
				"required": ["name", "description", "script"]
			}`),
			Handler:     createSkillToolHandler,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	// --- P13.4: Image Analysis ---
	if enabled("image_analyze") && cfg.Tools.Vision.Provider != "" {
		r.Register(&ToolDef{
			Name:        "image_analyze",
			Description: "Analyze an image using a Vision API (Anthropic, OpenAI, or Google). Accepts URL or base64 input.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"image": {"type": "string", "description": "Image URL or base64-encoded data (data URI or raw base64)"},
					"prompt": {"type": "string", "description": "What to analyze in the image (default: describe the image)"},
					"detail": {"type": "string", "enum": ["low", "high", "auto"], "description": "Analysis detail level (default: auto)"}
				},
				"required": ["image"]
			}`),
			Handler: toolImageAnalyze,
			Builtin: true,
		})
	}

	// --- P18.2: OAuth 2.0 Framework ---
	if enabled("oauth_status") {
		r.Register(&ToolDef{
			Name:        "oauth_status",
			Description: "List connected OAuth services and their status (scopes, expiry). No secrets are returned.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolOAuthStatus,
			Builtin: true,
		})
	}

	if enabled("oauth_request") {
		r.Register(&ToolDef{
			Name:        "oauth_request",
			Description: "Make an authenticated HTTP request using a connected OAuth service. The token is auto-refreshed if needed.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"service": {"type": "string", "description": "OAuth service name (e.g. google, github)"},
					"method": {"type": "string", "description": "HTTP method (default: GET)"},
					"url": {"type": "string", "description": "Request URL"},
					"body": {"type": "string", "description": "Request body (optional)"}
				},
				"required": ["service", "url"]
			}`),
			Handler:     toolOAuthRequest,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("oauth_authorize") {
		r.Register(&ToolDef{
			Name:        "oauth_authorize",
			Description: "Get the authorization URL for an OAuth service so the user can connect it",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"service": {"type": "string", "description": "OAuth service name (e.g. google, github)"}
				},
				"required": ["service"]
			}`),
			Handler: toolOAuthAuthorize,
			Builtin: true,
		})
	}

	// --- P19.3: Smart Reminders ---
	if enabled("reminder_set") {
		r.Register(&ToolDef{
			Name:        "reminder_set",
			Description: "Set a new reminder with natural language time. Supports Japanese (5分後, 明日3時), English (in 5 min, tomorrow 3pm), Chinese (5分鐘後, 明天下午3點), and absolute times (2025-01-15 14:00, 15:30).",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Reminder text / what to remind about"},
					"time": {"type": "string", "description": "When to fire (natural language: '5分後', 'in 1 hour', 'tomorrow 3pm', '明天下午3點')"},
					"recurring": {"type": "string", "description": "Cron expression for recurring reminders (e.g. '0 9 * * *' for daily 9am). Optional."},
					"channel": {"type": "string", "description": "Source channel (telegram, slack, api, etc.)"},
					"user_id": {"type": "string", "description": "User ID for the reminder owner"}
				},
				"required": ["text", "time"]
			}`),
			Handler: toolReminderSet,
			Builtin: true,
		})
	}

	if enabled("reminder_list") {
		r.Register(&ToolDef{
			Name:        "reminder_list",
			Description: "List active (pending) reminders. Optionally filter by user_id.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"user_id": {"type": "string", "description": "Filter by user ID (optional)"}
				}
			}`),
			Handler: toolReminderList,
			Builtin: true,
		})
	}

	if enabled("reminder_cancel") {
		r.Register(&ToolDef{
			Name:        "reminder_cancel",
			Description: "Cancel an active reminder by ID.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Reminder ID to cancel"},
					"user_id": {"type": "string", "description": "User ID for ownership verification (optional)"}
				},
				"required": ["id"]
			}`),
			Handler: toolReminderCancel,
			Builtin: true,
		})
	}

	// --- P19.4: Notes/Obsidian Integration ---
	if enabled("note_create") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_create",
			Description: "Create a new note in the Obsidian vault. Supports nested paths (e.g. 'daily/2024-01-15'). Auto-appends .md if no extension given.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Note name or path (e.g. 'meeting-notes', 'project/ideas')"},
					"content": {"type": "string", "description": "Note content (markdown)"}
				},
				"required": ["name", "content"]
			}`),
			Handler: toolNoteCreate,
			Builtin: true,
		})
	}

	if enabled("note_read") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_read",
			Description: "Read a note from the Obsidian vault. Returns content, tags, and wikilinks.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Note name or path"}
				},
				"required": ["name"]
			}`),
			Handler: toolNoteRead,
			Builtin: true,
		})
	}

	if enabled("note_append") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_append",
			Description: "Append content to an existing note (creates if not exists).",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Note name or path"},
					"content": {"type": "string", "description": "Content to append"}
				},
				"required": ["name", "content"]
			}`),
			Handler: toolNoteAppend,
			Builtin: true,
		})
	}

	if enabled("note_list") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_list",
			Description: "List notes in the vault. Optionally filter by path prefix.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"prefix": {"type": "string", "description": "Path prefix to filter (e.g. 'daily/', 'project/')"}
				}
			}`),
			Handler: toolNoteList,
			Builtin: true,
		})
	}

	// --- P20.1: Home Assistant ---
	if enabled("ha_list_entities") && cfg.HomeAssistant.Enabled {
		r.Register(&ToolDef{
			Name:        "ha_list_entities",
			Description: "List Home Assistant entities, optionally filtered by domain (light, switch, sensor, climate, etc.)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"domain": {"type": "string", "description": "Entity domain filter (e.g. 'light', 'switch', 'sensor'). Optional — omit to list all."}
				}
			}`),
			Handler: toolHAListEntities,
			Builtin: true,
		})
	}

	if enabled("ha_get_state") && cfg.HomeAssistant.Enabled {
		r.Register(&ToolDef{
			Name:        "ha_get_state",
			Description: "Get the current state and attributes of a single Home Assistant entity",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"entity_id": {"type": "string", "description": "Entity ID (e.g. 'light.living_room', 'sensor.temperature')"}
				},
				"required": ["entity_id"]
			}`),
			Handler: toolHAGetState,
			Builtin: true,
		})
	}

	if enabled("ha_call_service") && cfg.HomeAssistant.Enabled {
		r.Register(&ToolDef{
			Name:        "ha_call_service",
			Description: "Call a Home Assistant service (e.g. turn on a light, set thermostat temperature, lock a door)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"domain": {"type": "string", "description": "Service domain (e.g. 'light', 'switch', 'climate')"},
					"service": {"type": "string", "description": "Service name (e.g. 'turn_on', 'turn_off', 'set_temperature')"},
					"data": {"type": "object", "description": "Service data (e.g. {\"entity_id\": \"light.living_room\", \"brightness\": 128})"}
				},
				"required": ["domain", "service"]
			}`),
			Handler:     toolHACallService,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("ha_set_state") && cfg.HomeAssistant.Enabled {
		r.Register(&ToolDef{
			Name:        "ha_set_state",
			Description: "Directly set the state of a Home Assistant entity (for virtual/custom sensors)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"entity_id": {"type": "string", "description": "Entity ID to update"},
					"state": {"type": "string", "description": "New state value"},
					"attributes": {"type": "object", "description": "Optional attributes to set"}
				},
				"required": ["entity_id", "state"]
			}`),
			Handler:     toolHASetState,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("note_search") && cfg.Notes.Enabled {
		r.Register(&ToolDef{
			Name:        "note_search",
			Description: "Search notes using TF-IDF full-text search. Returns ranked results with snippets.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"max_results": {"type": "number", "description": "Maximum results to return (default 5)"}
				},
				"required": ["query"]
			}`),
			Handler: toolNoteSearch,
			Builtin: true,
		})
	}

	// --- P20.4: Device Actions ---
	registerDeviceTools(r, cfg)

	// --- P20.2: iMessage via BlueBubbles ---
	if enabled("imessage_send") && cfg.IMessage.Enabled {
		r.Register(&ToolDef{
			Name:        "imessage_send",
			Description: "Send an iMessage to a specific chat via BlueBubbles",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"chat_guid": {"type": "string", "description": "Chat GUID (e.g. 'iMessage;-;+1234567890')"},
					"text": {"type": "string", "description": "Message text to send"}
				},
				"required": ["chat_guid", "text"]
			}`),
			Handler: toolIMessageSend,
			Builtin: true,
		})
	}

	if enabled("imessage_search") && cfg.IMessage.Enabled {
		r.Register(&ToolDef{
			Name:        "imessage_search",
			Description: "Search iMessage messages via BlueBubbles",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"limit": {"type": "number", "description": "Maximum results (default 10)"}
				},
				"required": ["query"]
			}`),
			Handler: toolIMessageSearch,
			Builtin: true,
		})
	}

	if enabled("imessage_read") && cfg.IMessage.Enabled {
		r.Register(&ToolDef{
			Name:        "imessage_read",
			Description: "Read recent iMessage messages from a chat via BlueBubbles",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"chat_guid": {"type": "string", "description": "Chat GUID to read messages from"},
					"limit": {"type": "number", "description": "Number of recent messages to retrieve (default 20)"}
				},
				"required": ["chat_guid"]
			}`),
			Handler: toolIMessageRead,
			Builtin: true,
		})
	}

	// --- P19.1: Gmail Integration ---
	if enabled("email_list") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_list",
			Description: "List recent emails from Gmail with optional search query",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Gmail search query (e.g. 'from:alice subject:meeting')"},
					"maxResults": {"type": "number", "description": "Maximum number of results (default 20)"}
				}
			}`),
			Handler: toolEmailList,
			Builtin: true,
		})
	}

	if enabled("email_read") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_read",
			Description: "Read a specific email message by ID, returning full headers and body",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message_id": {"type": "string", "description": "Gmail message ID"}
				},
				"required": ["message_id"]
			}`),
			Handler: toolEmailRead,
			Builtin: true,
		})
	}

	if enabled("email_send") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_send",
			Description: "Send an email via Gmail",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"to": {"type": "string", "description": "Recipient email address"},
					"subject": {"type": "string", "description": "Email subject"},
					"body": {"type": "string", "description": "Email body (plain text)"},
					"cc": {"type": "array", "items": {"type": "string"}, "description": "CC recipients"},
					"bcc": {"type": "array", "items": {"type": "string"}, "description": "BCC recipients"}
				},
				"required": ["to", "subject", "body"]
			}`),
			Handler:     toolEmailSend,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("email_draft") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_draft",
			Description: "Create an email draft in Gmail",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"to": {"type": "string", "description": "Recipient email address"},
					"subject": {"type": "string", "description": "Email subject"},
					"body": {"type": "string", "description": "Email body (plain text)"}
				},
				"required": ["to", "subject"]
			}`),
			Handler: toolEmailDraft,
			Builtin: true,
		})
	}

	if enabled("email_search") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_search",
			Description: "Search emails using advanced Gmail search syntax (from:, to:, subject:, has:attachment, after:, before:, etc.)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Gmail search query using advanced syntax"},
					"maxResults": {"type": "number", "description": "Maximum number of results (default 20)"}
				},
				"required": ["query"]
			}`),
			Handler: toolEmailSearch,
			Builtin: true,
		})
	}

	if enabled("email_label") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_label",
			Description: "Add or remove labels on a Gmail message (e.g. INBOX, UNREAD, STARRED, IMPORTANT, SPAM, TRASH)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message_id": {"type": "string", "description": "Gmail message ID"},
					"add_labels": {"type": "array", "items": {"type": "string"}, "description": "Label IDs to add"},
					"remove_labels": {"type": "array", "items": {"type": "string"}, "description": "Label IDs to remove"}
				},
				"required": ["message_id"]
			}`),
			Handler: toolEmailLabel,
			Builtin: true,
		})
	}

	// --- P19.2: Google Calendar Integration ---
	if enabled("calendar_list") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_list",
			Description: "List upcoming Google Calendar events within a time range",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"timeMin": {"type": "string", "description": "Start of time range (RFC3339, default: now)"},
					"timeMax": {"type": "string", "description": "End of time range (RFC3339, default: 7 days from now)"},
					"maxResults": {"type": "number", "description": "Maximum number of events to return (default 10)"},
					"days": {"type": "number", "description": "Convenience: list events for next N days (default 7)"}
				}
			}`),
			Handler: toolCalendarList,
			Builtin: true,
		})
	}

	if enabled("calendar_create") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_create",
			Description: "Create a Google Calendar event. Accepts structured input or natural language (Japanese/English/Chinese)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"summary": {"type": "string", "description": "Event title"},
					"description": {"type": "string", "description": "Event description"},
					"location": {"type": "string", "description": "Event location"},
					"start": {"type": "string", "description": "Start time (RFC3339 or date YYYY-MM-DD)"},
					"end": {"type": "string", "description": "End time (RFC3339 or date; default: 1 hour after start)"},
					"timeZone": {"type": "string", "description": "Time zone (e.g. Asia/Tokyo)"},
					"attendees": {"type": "array", "items": {"type": "string"}, "description": "Attendee email addresses"},
					"allDay": {"type": "boolean", "description": "Create as all-day event"},
					"text": {"type": "string", "description": "Natural language schedule (e.g. '明日2時の会議', 'meeting tomorrow at 2pm')"}
				}
			}`),
			Handler: toolCalendarCreate,
			Builtin: true,
		})
	}

	if enabled("calendar_update") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_update",
			Description: "Update an existing Google Calendar event",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"eventId": {"type": "string", "description": "Event ID to update"},
					"summary": {"type": "string", "description": "New event title"},
					"description": {"type": "string", "description": "New description"},
					"location": {"type": "string", "description": "New location"},
					"start": {"type": "string", "description": "New start time (RFC3339)"},
					"end": {"type": "string", "description": "New end time (RFC3339)"},
					"timeZone": {"type": "string", "description": "Time zone"},
					"attendees": {"type": "array", "items": {"type": "string"}, "description": "Updated attendee emails"},
					"allDay": {"type": "boolean", "description": "All-day event flag"}
				},
				"required": ["eventId"]
			}`),
			Handler: toolCalendarUpdate,
			Builtin: true,
		})
	}

	if enabled("calendar_delete") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_delete",
			Description: "Delete a Google Calendar event by ID",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"eventId": {"type": "string", "description": "Event ID to delete"}
				},
				"required": ["eventId"]
			}`),
			Handler:     toolCalendarDelete,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("calendar_search") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_search",
			Description: "Search Google Calendar events by text query",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query (full-text)"},
					"timeMin": {"type": "string", "description": "Start of time range (RFC3339, default: 30 days ago)"},
					"timeMax": {"type": "string", "description": "End of time range (RFC3339, default: 90 days from now)"}
				},
				"required": ["query"]
			}`),
			Handler: toolCalendarSearch,
			Builtin: true,
		})
	}

	// --- P20.3: Twitter/X Integration ---
	if enabled("tweet_post") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_post",
			Description: "Post a tweet on Twitter/X. Optionally reply to an existing tweet.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Tweet text content"},
					"reply_to": {"type": "string", "description": "Tweet ID to reply to (optional)"}
				},
				"required": ["text"]
			}`),
			Handler:     toolTweetPost,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("tweet_read_timeline") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_read_timeline",
			Description: "Read the authenticated user's home timeline (reverse chronological)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"max_results": {"type": "number", "description": "Maximum number of tweets to return (default 10, max 100)"}
				}
			}`),
			Handler: toolTweetTimeline,
			Builtin: true,
		})
	}

	if enabled("tweet_search") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_search",
			Description: "Search recent tweets on Twitter/X matching a query",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query (supports Twitter search operators)"},
					"max_results": {"type": "number", "description": "Maximum number of tweets to return (default 10, max 100)"}
				},
				"required": ["query"]
			}`),
			Handler: toolTweetSearch,
			Builtin: true,
		})
	}

	if enabled("tweet_reply") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_reply",
			Description: "Reply to a specific tweet on Twitter/X",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"tweet_id": {"type": "string", "description": "ID of the tweet to reply to"},
					"text": {"type": "string", "description": "Reply text content"}
				},
				"required": ["tweet_id", "text"]
			}`),
			Handler:     toolTweetReply,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("tweet_dm") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_dm",
			Description: "Send a direct message to a Twitter/X user",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"recipient_id": {"type": "string", "description": "Twitter user ID of the recipient"},
					"text": {"type": "string", "description": "Message text"}
				},
				"required": ["recipient_id", "text"]
			}`),
			Handler:     toolTweetDM,
			Builtin:     true,
			RequireAuth: true,
		})
	}
}

// --- Tool Handlers ---

// toolExec executes a shell command.
func toolExec(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Command string  `json:"command"`
		Workdir string  `json:"workdir"`
		Timeout float64 `json:"timeout"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Command == "" {
		return "", fmt.Errorf("command is required")
	}
	if args.Timeout <= 0 {
		args.Timeout = 60
	}

	// Validate workdir is within allowedDirs.
	if args.Workdir != "" {
		if err := validateDirs(cfg, Task{Workdir: args.Workdir}, ""); err != nil {
			return "", fmt.Errorf("workdir not allowed: %w", err)
		}
	}

	timeout := time.Duration(args.Timeout) * time.Second
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", args.Command)
	if args.Workdir != "" {
		cmd.Dir = args.Workdir
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("command failed: %w", err)
		}
	}

	// Limit output size.
	const maxOutput = 100 * 1024 // 100KB
	out := stdout.String()
	errOut := stderr.String()
	if len(out) > maxOutput {
		out = out[:maxOutput] + "\n[truncated]"
	}
	if len(errOut) > maxOutput {
		errOut = errOut[:maxOutput] + "\n[truncated]"
	}

	result := map[string]any{
		"stdout":   out,
		"stderr":   errOut,
		"exitCode": exitCode,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolRead reads file contents.
func toolRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Check file size.
	info, err := os.Stat(args.Path)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	const maxSize = 1024 * 1024 // 1MB
	if info.Size() > maxSize {
		return "", fmt.Errorf("file too large (max 1MB)")
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	if args.Offset > 0 {
		if args.Offset >= len(lines) {
			return "", nil
		}
		lines = lines[args.Offset:]
	}
	if args.Limit > 0 && args.Limit < len(lines) {
		lines = lines[:args.Limit]
	}

	return strings.Join(lines, "\n"), nil
}

// toolWrite writes content to a file.
func toolWrite(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Validate path is within allowedDirs.
	if err := validateDirs(cfg, Task{Workdir: filepath.Dir(args.Path)}, ""); err != nil {
		return "", fmt.Errorf("path not allowed: %w", err)
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(args.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path), nil
}

// toolEdit performs string replacement in a file.
func toolEdit(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" || args.OldString == "" {
		return "", fmt.Errorf("path and old_string are required")
	}

	// Validate path is within allowedDirs.
	if err := validateDirs(cfg, Task{Workdir: filepath.Dir(args.Path)}, ""); err != nil {
		return "", fmt.Errorf("path not allowed: %w", err)
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	if !strings.Contains(content, args.OldString) {
		return "", fmt.Errorf("old_string not found in file")
	}

	// Check for unique match.
	count := strings.Count(content, args.OldString)
	if count > 1 {
		return "", fmt.Errorf("old_string appears %d times (not unique)", count)
	}

	newContent := strings.Replace(content, args.OldString, args.NewString, 1)
	if err := os.WriteFile(args.Path, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("replaced 1 occurrence in %s", args.Path), nil
}

// toolWebFetch fetches a URL and returns plain text.
func toolWebFetch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		URL       string `json:"url"`
		MaxLength int    `json:"maxLength"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if args.MaxLength <= 0 {
		args.MaxLength = 50000 // default 50KB
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", args.URL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, resp.Status)
	}

	// Limit response size.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(args.MaxLength)))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	// Simple HTML tag stripping.
	text := stripHTMLTags(string(body))

	// Truncate to maxLength after stripping tags.
	if len(text) > args.MaxLength {
		text = text[:args.MaxLength]
	}

	return text, nil
}

// stripHTMLTags removes HTML tags from text (naive implementation).
func stripHTMLTags(html string) string {
	var result strings.Builder
	inTag := false
	for _, c := range html {
		if c == '<' {
			inTag = true
		} else if c == '>' {
			inTag = false
		} else if !inTag {
			result.WriteRune(c)
		}
	}
	// Collapse multiple whitespace.
	text := result.String()
	text = strings.Join(strings.Fields(text), " ")
	return text
}

// toolMemorySearch searches agent memory.
func toolMemorySearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	// Build SQL query.
	query := fmt.Sprintf(`SELECT key, value, role FROM agent_memory WHERE value LIKE '%%%s%%'`, escapeSQLite(args.Query))
	if args.Role != "" {
		query += fmt.Sprintf(` AND role = '%s'`, escapeSQLite(args.Role))
	}
	query += ` ORDER BY updated_at DESC LIMIT 10`

	rows, err := queryDB(cfg.HistoryDB, query)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}

	var results []map[string]string
	for _, row := range rows {
		results = append(results, map[string]string{
			"key":   fmt.Sprintf("%v", row["key"]),
			"value": fmt.Sprintf("%v", row["value"]),
			"role":  fmt.Sprintf("%v", row["role"]),
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolMemoryGet retrieves a specific memory value.
func toolMemoryGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Key  string `json:"key"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Key == "" {
		return "", fmt.Errorf("key is required")
	}

	query := fmt.Sprintf(`SELECT value FROM agent_memory WHERE key = '%s'`, escapeSQLite(args.Key))
	if args.Role != "" {
		query += fmt.Sprintf(` AND role = '%s'`, escapeSQLite(args.Role))
	}
	query += ` LIMIT 1`

	rows, err := queryDB(cfg.HistoryDB, query)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("key not found")
	}

	return fmt.Sprintf("%v", rows[0]["value"]), nil
}

// toolKnowledgeSearch searches the knowledge base using existing TF-IDF search.
func toolKnowledgeSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}

	// Use existing knowledge search function if available, otherwise fallback to simple DB query.
	query := fmt.Sprintf(`SELECT filename, snippet FROM knowledge WHERE content LIKE '%%%s%%' ORDER BY indexed_at DESC LIMIT %d`,
		escapeSQLite(args.Query), args.Limit)
	rows, err := queryDB(cfg.HistoryDB, query)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}

	var results []map[string]any
	for _, row := range rows {
		results = append(results, map[string]any{
			"filename": fmt.Sprintf("%v", row["filename"]),
			"snippet":  fmt.Sprintf("%v", row["snippet"]),
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolSessionList lists active sessions.
func toolSessionList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Channel string `json:"channel"`
	}
	json.Unmarshal(input, &args)

	query := `SELECT session_id, channel_type, channel_id, message_count, created_at, updated_at FROM sessions WHERE 1=1`
	if args.Channel != "" {
		query += fmt.Sprintf(` AND channel_type = '%s'`, escapeSQLite(args.Channel))
	}
	query += ` ORDER BY updated_at DESC LIMIT 20`

	rows, err := queryDB(cfg.HistoryDB, query)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}

	var results []map[string]string
	for _, row := range rows {
		results = append(results, map[string]string{
			"session_id":    fmt.Sprintf("%v", row["session_id"]),
			"channel_type":  fmt.Sprintf("%v", row["channel_type"]),
			"channel_id":    fmt.Sprintf("%v", row["channel_id"]),
			"message_count": fmt.Sprintf("%v", row["message_count"]),
			"created_at":    fmt.Sprintf("%v", row["created_at"]),
			"updated_at":    fmt.Sprintf("%v", row["updated_at"]),
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolMessage sends a message to a channel.
func toolMessage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Channel string `json:"channel"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Channel == "" || args.Message == "" {
		return "", fmt.Errorf("channel and message are required")
	}

	switch args.Channel {
	case "telegram":
		if cfg.Telegram.Enabled {
			err := sendTelegramNotify(&cfg.Telegram, args.Message)
			if err != nil {
				return "", fmt.Errorf("send telegram: %w", err)
			}
			return "message sent to telegram", nil
		}
		return "", fmt.Errorf("telegram not enabled")
	case "slack":
		if cfg.Slack.Enabled {
			// Use notification system.
			notifiers := buildNotifiers(cfg)
			for _, n := range notifiers {
				if n.Name() == "slack" {
					n.Send(args.Message)
				}
			}
			return "message sent to slack", nil
		}
		return "", fmt.Errorf("slack not enabled")
	case "discord":
		if cfg.Discord.Enabled {
			// Use notification system.
			notifiers := buildNotifiers(cfg)
			for _, n := range notifiers {
				if n.Name() == "discord" {
					n.Send(args.Message)
				}
			}
			return "message sent to discord", nil
		}
		return "", fmt.Errorf("discord not enabled")
	default:
		return "", fmt.Errorf("unknown channel: %s", args.Channel)
	}
}

// toolCronList lists cron jobs.
func toolCronList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	// Read cron jobs from JobsFile.
	jobs, err := loadCronJobs(cfg.JobsFile)
	if err != nil {
		return "", fmt.Errorf("load jobs: %w", err)
	}

	var results []map[string]any
	for _, j := range jobs {
		results = append(results, map[string]any{
			"id":       j.ID,
			"name":     j.Name,
			"schedule": j.Schedule,
			"enabled":  j.Enabled,
			"role":     j.Role,
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolCronCreate creates or updates a cron job.
func toolCronCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Prompt   string `json:"prompt"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" || args.Schedule == "" || args.Prompt == "" {
		return "", fmt.Errorf("name, schedule, and prompt are required")
	}

	jobs, err := loadCronJobs(cfg.JobsFile)
	if err != nil {
		jobs = []CronJobConfig{}
	}

	// Check if job exists.
	found := false
	for i := range jobs {
		if jobs[i].Name == args.Name {
			jobs[i].Schedule = args.Schedule
			jobs[i].Task.Prompt = args.Prompt
			jobs[i].Role = args.Role
			jobs[i].Enabled = true
			found = true
			break
		}
	}

	if !found {
		newJob := CronJobConfig{
			ID:       newUUID(),
			Name:     args.Name,
			Schedule: args.Schedule,
			Enabled:  true,
			Role:     args.Role,
			Task: CronTaskConfig{
				Prompt: args.Prompt,
			},
		}
		jobs = append(jobs, newJob)
	}

	if err := saveCronJobs(cfg.JobsFile, jobs); err != nil {
		return "", fmt.Errorf("save jobs: %w", err)
	}

	msg := "created"
	if found {
		msg = "updated"
	}
	return fmt.Sprintf("cron job %q %s", args.Name, msg), nil
}

// toolCronDelete deletes a cron job.
func toolCronDelete(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	jobs, err := loadCronJobs(cfg.JobsFile)
	if err != nil {
		return "", fmt.Errorf("load jobs: %w", err)
	}

	found := false
	newJobs := make([]CronJobConfig, 0, len(jobs))
	for _, j := range jobs {
		if j.Name != args.Name {
			newJobs = append(newJobs, j)
		} else {
			found = true
		}
	}

	if !found {
		return "", fmt.Errorf("job %q not found", args.Name)
	}

	if err := saveCronJobs(cfg.JobsFile, newJobs); err != nil {
		return "", fmt.Errorf("save jobs: %w", err)
	}

	return fmt.Sprintf("cron job %q deleted", args.Name), nil
}

// --- Helper functions for cron job management ---

func loadCronJobs(path string) ([]CronJobConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var jobs []CronJobConfig
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func saveCronJobs(path string, jobs []CronJobConfig) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
