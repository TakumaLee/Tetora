package tools

import (
	"encoding/json"

	"tetora/internal/config"
)

// CoreDeps holds pre-built handler functions for core tools.
// The root package constructs these closures (which capture root-only types
// like Config, ToolRegistry, App) and passes them in; this package only owns
// the registration logic and JSON schemas.
type CoreDeps struct {
	ExecHandler        Handler
	ReadHandler        Handler
	WriteHandler       Handler
	EditHandler        Handler
	WebSearchHandler   Handler
	WebFetchHandler    Handler
	SessionListHandler Handler
	MessageHandler     Handler
	CronListHandler    Handler
	CronCreateHandler  Handler
	CronDeleteHandler  Handler
	AgentListHandler   Handler
	AgentDispatchHandler Handler
	AgentMessageHandler  Handler
	CompetitiveSearchHandler Handler
	NewsSearchHandler    Handler
	XSearchHandler       Handler
	SearchToolsHandler Handler
	ExecuteToolHandler Handler
	ImageAnalyzeHandler Handler
}

// RegisterCoreTools registers core exec/read/write/edit/web/session/cron/agent/meta tools.
// It mirrors the structure of the original registerCoreTools exactly.
func RegisterCoreTools(r *Registry, cfg *config.Config, enabled func(string) bool, deps CoreDeps) {
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
			Handler:     deps.ExecHandler,
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
			Handler: deps.ReadHandler,
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
			Handler:     deps.WriteHandler,
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
			Handler:     deps.EditHandler,
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
			Handler: deps.WebSearchHandler,
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
			Handler: deps.WebFetchHandler,
			Builtin: true,
		})
	}

	if enabled("competitive_search") {
		r.Register(&ToolDef{
			Name:        "competitive_search",
			Description: "High-quality, multi-source search with deduplication and ranking. Recommended for deep intelligence.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"limit": {"type": "number", "description": "Max results (default 10)"}
				},
				"required": ["query"]
			}`),
			Handler: deps.CompetitiveSearchHandler,
			Builtin: true,
		})
	}

	if enabled("news_search") {
		r.Register(&ToolDef{
			Name:        "news_search",
			Description: "Search recent news articles from global sources. Best for current events and trends.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "News search query"},
					"limit": {"type": "number", "description": "Max results (default 5)"}
				},
				"required": ["query"]
			}`),
			Handler: deps.NewsSearchHandler,
			Builtin: true,
		})
	}

	if enabled("x_search") {
		r.Register(&ToolDef{
			Name:        "x_search",
			Description: "Search X (Twitter) posts for social intelligence and real-time community sentiment.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "X/Twitter search query"},
					"limit": {"type": "number", "description": "Max results (default 10)"}
				},
				"required": ["query"]
			}`),
			Handler: deps.XSearchHandler,
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
			Handler: deps.SessionListHandler,
			Builtin: true,
		})
	}

	if enabled("message") {
		r.Register(&ToolDef{
			Name:        "message",
			Description: "Send a message to a channel (Telegram, Slack, Discord). Use 'discord-<name>' to target a named Discord webhook channel (e.g. 'discord-stock').",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"channel": {"type": "string", "description": "Channel type: telegram, slack, discord. For named Discord webhook channels use discord-<name> (e.g. discord-stock, discord-trading). Named channels must be configured under discord.webhooks in config.json."},
					"message": {"type": "string", "description": "Message content"}
				},
				"required": ["channel", "message"]
			}`),
			Handler: deps.MessageHandler,
			Builtin: true,
		})
	}

	if enabled("cron_list") {
		r.Register(&ToolDef{
			Name:        "cron_list",
			Description: "List scheduled cron jobs",
			InputSchema: json.RawMessage(`{"type": "object"}`),
			Handler:     deps.CronListHandler,
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
					"agent": {"type": "string", "description": "Agent name (optional)"},
					"role": {"type": "string", "description": "Deprecated alias for agent"}
				},
				"required": ["name", "schedule", "prompt"]
			}`),
			Handler:     deps.CronCreateHandler,
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
			Handler:     deps.CronDeleteHandler,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("agent_list") {
		r.Register(&ToolDef{
			Name:        "agent_list",
			Description: "List available agents with their capabilities",
			InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
			Handler:     deps.AgentListHandler,
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
					"agent": {"type": "string", "description": "Target agent name"},
					"role": {"type": "string", "description": "Deprecated alias for agent"},
					"prompt": {"type": "string", "description": "Task prompt to send"},
					"timeout": {"type": "number", "description": "Timeout in seconds (default 300)"}
				},
				"required": ["prompt"]
			}`),
			Handler:     deps.AgentDispatchHandler,
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
					"agent": {"type": "string", "description": "Target agent name"},
					"role": {"type": "string", "description": "Deprecated alias for agent"},
					"message": {"type": "string", "description": "Message content"},
					"sessionId": {"type": "string", "description": "Target session ID (optional)"}
				},
				"required": ["message"]
			}`),
			Handler:     deps.AgentMessageHandler,
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
			Handler: deps.SearchToolsHandler,
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
			Handler:     deps.ExecuteToolHandler,
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
			Handler: deps.ImageAnalyzeHandler,
			Builtin: true,
		})
	}
}
