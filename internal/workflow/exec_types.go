package workflow

// WorkflowRun tracks a single execution of a workflow.
type WorkflowRun struct {
	ID           string                    `json:"id"`
	WorkflowName string                    `json:"workflowName"`
	Status       string                    `json:"status"` // "running", "success", "error", "cancelled", "timeout", "resumed", "waiting"
	StartedAt    string                    `json:"startedAt"`
	FinishedAt   string                    `json:"finishedAt,omitempty"`
	DurationMs   int64                     `json:"durationMs,omitempty"`
	TotalCost    float64                   `json:"totalCostUsd,omitempty"`
	Variables    map[string]string         `json:"variables,omitempty"`
	StepResults  map[string]*StepRunResult `json:"stepResults"`
	Error        string                    `json:"error,omitempty"`
	ResumedFrom  string                    `json:"resumedFrom,omitempty"`
}

// StepRunResult tracks the execution of one step.
type StepRunResult struct {
	StepID     string  `json:"stepId"`
	Status     string  `json:"status"` // "pending", "running", "success", "error", "skipped", "timeout", "waiting_human"
	Output     string  `json:"output,omitempty"`
	Error      string  `json:"error,omitempty"`
	StartedAt  string  `json:"startedAt,omitempty"`
	FinishedAt string  `json:"finishedAt,omitempty"`
	DurationMs int64   `json:"durationMs,omitempty"`
	CostUSD    float64 `json:"costUsd,omitempty"`
	TaskID     string  `json:"taskId,omitempty"`
	SessionID  string  `json:"sessionId,omitempty"`
	Retries    int     `json:"retries,omitempty"`
}

// WorkflowRunMode controls how a workflow is executed.
type WorkflowRunMode string

const (
	// WorkflowModeLive is the default mode: full execution with provider calls and history recording.
	WorkflowModeLive WorkflowRunMode = "live"
	// WorkflowModeDryRun skips provider calls and estimates costs instead.
	WorkflowModeDryRun WorkflowRunMode = "dry-run"
	// WorkflowModeShadow executes normally but marks runs so they skip task-level history recording.
	WorkflowModeShadow WorkflowRunMode = "shadow"
)

// stepDoneMsg carries a completed step result from a goroutine back to the DAG scheduler.
type stepDoneMsg struct {
	id     string
	result *StepRunResult
}
