// Package provider defines the shared types and interfaces for LLM provider backends.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// --- Provider Interface ---

// Provider abstracts LLM execution backends.
type Provider interface {
	// Name returns the provider name (e.g. "claude", "openai").
	Name() string
	// Execute runs a prompt and returns the result.
	Execute(ctx context.Context, req Request) (*Result, error)
}

// ToolCapableProvider extends Provider with tool execution support.
type ToolCapableProvider interface {
	Provider
	ExecuteWithTools(ctx context.Context, req Request) (*Result, error)
}

// --- Request / Result ---

// Request contains all information needed to execute a task.
type Request struct {
	Prompt       string
	SystemPrompt string
	Model        string
	Workdir      string
	Timeout      time.Duration
	Budget       float64

	// Claude-specific (ignored by API providers).
	PermissionMode string
	MCP            string
	MCPPath        string
	AddDirs        []string
	SessionID      string
	Resume         bool // use --resume to resume existing CLI session
	PersistSession bool // don't add --no-session-persistence (channel sessions)

	// AllowedTools restricts which tools the CLI agent can use (Claude --allowedTools).
	AllowedTools []string

	// AgentName is the Tetora agent name (e.g. "ruri") for worker display.
	AgentName string

	// MaxRSSMB is the RSS hard-limit for the subprocess (MB). 0 = disabled.
	MaxRSSMB int

	// Docker sandbox override (nil=use config default).
	Docker *bool

	// Tools for agentic loop (passed to provider).
	Tools []ToolDef

	// OnEvent callback for streaming events (provider publishes output_chunk, tool_call, etc.).
	OnEvent func(Event) `json:"-"`

	// Optional event channel for SSE streaming (alternative to OnEvent).
	EventCh chan<- Event `json:"-"`

	// Messages for multi-turn tool loop.
	Messages []Message `json:"messages,omitempty"`
}

// Result is the normalized output from any provider.
type Result struct {
	Output     string
	CostUSD    float64
	DurationMs int64
	SessionID  string
	IsError    bool
	Error      string
	Provider   string // name of the provider that actually handled the request
	// Observability metrics.
	TokensIn   int   `json:"tokensIn,omitempty"`   // input tokens consumed
	TokensOut  int   `json:"tokensOut,omitempty"`  // output tokens generated
	ProviderMs int64 `json:"providerMs,omitempty"` // provider-reported latency (vs wall-clock DurationMs)
	// Tool support.
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	StopReason string     `json:"stopReason,omitempty"` // "end_turn", "tool_use"
}

// ErrResult returns a Result signaling an API-level error.
func ErrResult(format string, args ...any) *Result {
	return &Result{IsError: true, Error: fmt.Sprintf(format, args...)}
}

// IsTransientError checks whether an error message indicates a transient failure
// that should count towards the circuit breaker threshold and trigger failover.
func IsTransientError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	transient := []string{
		"timeout", "timed out", "deadline exceeded",
		"connection refused", "connection reset",
		"eof", "broken pipe",
		"http 5", "status 5",
		"temporarily unavailable", "service unavailable",
		"too many requests", "rate limit",
	}
	for _, t := range transient {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// --- Message Types ---

// Message represents a chat message for multi-turn conversations.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []ContentBlock
}

// ContentBlock represents a piece of content (text or tool use/result).
type ContentBlock struct {
	Type      string          `json:"type"` // "text", "tool_use", "tool_result"
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// --- Tool Types ---

// ToolDef defines a tool that can be called by providers.
// Note: Handler is intentionally omitted — providers only need Name/Description/InputSchema.
type ToolDef struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	DeferLoading bool            `json:"defer_loading,omitempty"` // When true, tool is loaded on-demand via search
}

// ToolCall represents a tool invocation request from the provider.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// --- SSE Event ---

// Event represents a Server-Sent Event for task/session streaming.
type Event struct {
	Type          string `json:"type"`
	TaskID        string `json:"taskId,omitempty"`
	SessionID     string `json:"sessionId,omitempty"`
	WorkflowRunID string `json:"workflowRunId,omitempty"`
	Data          any    `json:"data,omitempty"`
	Timestamp     string `json:"timestamp"`
}

// SSE event type constants used by providers.
const (
	EventOutputChunk = "output_chunk"
	EventToolCall    = "tool_call"
	EventToolResult  = "tool_result"
)

// --- Terminal Provider Dependencies ---

// DockerRunner builds Docker-wrapped exec.Cmd for sandboxed execution.
type DockerRunner interface {
	BuildCmd(ctx context.Context, binaryPath, workdir string, args, addDirs []string, mcpPath string) *exec.Cmd
}

// TmuxOps abstracts tmux session operations.
type TmuxOps interface {
	Create(session string, cols, rows int, command, workdir string) error
	Kill(session string)
	Capture(session string) (string, error)
	HasSession(session string) bool
	LoadAndPaste(session, text string) error
	SendText(session, text string) error
	SendKeys(session string, keys ...string) error
	CaptureHistory(session string) (string, error)
}

// TmuxProfile defines CLI-specific tmux behavior (command building, state detection).
type TmuxProfile interface {
	Name() string
	BuildCommand(binaryPath string, req Request) string
	DetectState(capture string) ScreenState
	ApproveKeys() []string
	RejectKeys() []string
}

// WorkerTracker tracks active tmux worker sessions.
type WorkerTracker interface {
	Register(sessionName string, info WorkerInfo)
	Unregister(sessionName string)
	UpdateWorker(sessionName string, state ScreenState, capture string, changed bool)
}

// ScreenState represents the detected state of a tmux terminal session.
type ScreenState int

const (
	ScreenUnknown  ScreenState = iota
	ScreenStarting             // CLI is launching
	ScreenWorking              // CLI is processing
	ScreenWaiting              // CLI is idle, waiting for input
	ScreenApproval             // CLI is asking for permission
	ScreenQuestion             // CLI is asking a question
	ScreenDone                 // CLI has exited
)

// WorkerInfo describes a registered tmux worker.
type WorkerInfo struct {
	TmuxName    string
	TaskID      string
	Agent       string
	Prompt      string
	Workdir     string
	State       ScreenState
	CreatedAt   time.Time
	LastChanged time.Time
}
