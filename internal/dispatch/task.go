package dispatch

import "time"

// Task represents a single unit of work to be dispatched.
type Task struct {
	// Public JSON fields.
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Prompt         string   `json:"prompt"`
	Workdir        string   `json:"workdir"`
	Model          string   `json:"model"`
	Provider       string   `json:"provider,omitempty"`
	Docker         *bool    `json:"docker,omitempty"` // per-task Docker sandbox override
	Timeout        string   `json:"timeout"`
	Budget         float64  `json:"budget"`
	PermissionMode string   `json:"permissionMode"`
	MCP            string   `json:"mcp"`
	AddDirs        []string `json:"addDirs"`
	SystemPrompt   string   `json:"systemPrompt"`
	SessionID      string   `json:"sessionId"`
	Resume         bool     `json:"resume,omitempty"`         // use --continue (resume existing CLI session)
	PersistSession bool     `json:"persistSession,omitempty"` // don't add --no-session-persistence
	Agent          string   `json:"agent,omitempty"`          // role name for smart dispatch
	ReviewLoop     bool     `json:"reviewLoop,omitempty"`     // enable Dev↔QA retry loop for this task
	Source         string   `json:"source,omitempty"`         // "dispatch", "cron", "ask", "route:*"
	TraceID        string   `json:"traceId,omitempty"`        // trace ID for request correlation
	Depth          int      `json:"depth,omitempty"`          // nesting depth (0 = top-level)
	ParentID       string   `json:"parentId,omitempty"`       // parent task ID
	AllowDangerous bool     `json:"allowDangerous,omitempty"` // skip dangerous ops check
	AllowedTools   []string `json:"allowedTools,omitempty"`   // CLI --allowedTools (skill-derived + explicit)

	// Runtime fields (not serialized).
	ChannelNotifier ChannelNotifier    `json:"-"` // messaging channel notifier
	ApprovalGate    ApprovalGate       `json:"-"` // pre-execution approval gate
	SSEBroker       SSEBrokerPublisher `json:"-"` // streaming event broker
	OnStart         func()             `json:"-"` // called after semaphore acquired, before execution
	WorkflowRunID   string             `json:"-"` // workflow run ID for SSE forwarding
	ClientID        string             `json:"-"` // multi-tenant client ID
}

// CompletionStatus represents the agent's self-assessed completion quality.
type CompletionStatus string

const (
	StatusDone             CompletionStatus = "DONE"              // task fully completed, no concerns
	StatusDoneWithConcerns CompletionStatus = "DONE_WITH_CONCERNS" // completed but agent has reservations
	StatusBlocked          CompletionStatus = "BLOCKED"            // cannot proceed without external input
	StatusNeedsContext     CompletionStatus = "NEEDS_CONTEXT"      // missing information to complete
)

// TaskResult holds the outcome of a completed task.
type TaskResult struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	ExitCode   int     `json:"exitCode"`
	Output     string  `json:"output"`
	Error      string  `json:"error,omitempty"`
	DurationMs int64   `json:"durationMs"`
	CostUSD    float64 `json:"costUsd"`
	Model      string  `json:"model"`
	SessionID  string  `json:"sessionId"`
	OutputFile string  `json:"outputFile,omitempty"`
	// Observability metrics.
	TokensIn   int    `json:"tokensIn,omitempty"`
	TokensOut  int    `json:"tokensOut,omitempty"`
	ProviderMs int64  `json:"providerMs,omitempty"`
	TraceID    string `json:"traceId,omitempty"`
	Provider   string `json:"provider,omitempty"`
	TrustLevel   string `json:"trustLevel,omitempty"`
	Agent        string `json:"agent,omitempty"`
	SlotWarning  string `json:"slotWarning,omitempty"`
	// Completion status fields (agent self-assessment).
	CompletionStat CompletionStatus `json:"completionStatus,omitempty"` // agent's self-assessed completion quality
	Concerns       string           `json:"concerns,omitempty"`         // DONE_WITH_CONCERNS reason
	BlockedReason  string           `json:"blockedReason,omitempty"`    // BLOCKED / NEEDS_CONTEXT reason
	// Dev↔QA loop fields (populated when ReviewLoop is enabled).
	QAApproved *bool  `json:"qaApproved,omitempty"` // nil=no review, true=passed, false=failed
	QAComment  string `json:"qaComment,omitempty"`  // reviewer feedback
	Attempts   int    `json:"attempts,omitempty"`   // total execution attempts (0=no loop)
}

// DispatchResult holds the aggregate outcome of a multi-task dispatch.
type DispatchResult struct {
	StartedAt  time.Time    `json:"startedAt"`
	FinishedAt time.Time    `json:"finishedAt"`
	DurationMs int64        `json:"durationMs"`
	TotalCost  float64      `json:"totalCostUsd"`
	Tasks      []TaskResult `json:"tasks"`
	Summary    string       `json:"summary"`
}
