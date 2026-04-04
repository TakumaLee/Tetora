// Package workflow provides workflow definitions, execution, and trigger types
// shared across the Tetora application.
package workflow

// --- Workflow Types ---

// Workflow defines a multi-step orchestration pipeline.
type Workflow struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Steps       []WorkflowStep    `json:"steps"`
	Variables   map[string]string `json:"variables,omitempty"` // input variables with defaults
	Timeout     string            `json:"timeout,omitempty"`   // overall workflow timeout, e.g. "30m"
	OnSuccess   string            `json:"onSuccess,omitempty"` // notification template
	OnFailure   string            `json:"onFailure,omitempty"` // notification template

	// Git worktree isolation (opt-in). When enabled, creates an isolated worktree
	// for the entire workflow run. All steps share the same branch.
	GitWorktree bool   `json:"gitWorktree,omitempty"` // enable worktree isolation
	Branch      string `json:"branch,omitempty"`      // explicit branch name (auto: "wf/{name}")
	Workdir     string `json:"workdir,omitempty"`     // repo directory (falls back to cfg.DefaultWorkdir)
}

// WorkflowStep is a single step in a workflow.
type WorkflowStep struct {
	ID        string   `json:"id"`
	Type      string   `json:"type,omitempty"`      // "dispatch" (default), "skill", "condition", "parallel"
	Agent     string   `json:"agent,omitempty"`     // agent role for dispatch steps
	Prompt    string   `json:"prompt,omitempty"`    // for dispatch steps
	Skill     string   `json:"skill,omitempty"`     // skill name for skill steps
	SkillArgs []string `json:"skillArgs,omitempty"` // skill arguments
	DependsOn []string `json:"dependsOn,omitempty"` // step IDs that must complete first

	// Dispatch options.
	Model          string  `json:"model,omitempty"`
	Provider       string  `json:"provider,omitempty"`
	Timeout        string  `json:"timeout,omitempty"` // per-step timeout
	Budget         float64 `json:"budget,omitempty"`
	PermissionMode string  `json:"permissionMode,omitempty"`

	// Condition step fields.
	If   string `json:"if,omitempty"`   // condition expression
	Then string `json:"then,omitempty"` // step ID to jump to on true
	Else string `json:"else,omitempty"` // step ID to jump to on false

	// Handoff step fields.
	HandoffFrom string `json:"handoffFrom,omitempty"` // source step ID whose output becomes context

	// Parallel step fields.
	Parallel []WorkflowStep `json:"parallel,omitempty"` // sub-steps to run in parallel

	// Failure handling.
	RetryMax      int    `json:"retryMax,omitempty"`      // max retries on failure
	RetryDelay    string `json:"retryDelay,omitempty"`    // delay between retries
	OnError       string `json:"onError,omitempty"`       // "stop" (default), "skip", "retry"
	AllowDangerous bool  `json:"allowDangerous,omitempty"` // bypass dangerous-ops check for this step

	// New step types: tool_call, delay, notify, external.
	ToolName  string            `json:"toolName,omitempty"`  // for type="tool_call"
	ToolInput map[string]string `json:"toolInput,omitempty"` // tool input params (supports {{var}} expansion)
	Delay     string            `json:"delay,omitempty"`     // for type="delay" (e.g. "30s", "5m")
	NotifyMsg string            `json:"notifyMsg,omitempty"` // for type="notify"
	NotifyTo  string            `json:"notifyTo,omitempty"`  // notification channel hint

	// External step fields (type="external").
	ExternalURL         string            `json:"externalUrl,omitempty"`         // POST target URL
	ExternalHeaders     map[string]string `json:"externalHeaders,omitempty"`     // custom headers (supports template vars)
	ExternalBody        map[string]string `json:"externalBody,omitempty"`        // request body KV (supports template vars)
	ExternalRawBody     string            `json:"externalRawBody,omitempty"`     // raw body (XML / custom, mutually exclusive with ExternalBody)
	ExternalContentType string            `json:"externalContentType,omitempty"` // default: application/json
	CallbackKey         string            `json:"callbackKey,omitempty"`         // callback matching key (supports template vars)
	CallbackTimeout     string            `json:"callbackTimeout,omitempty"`     // wait timeout (default 1h, max 30d)
	CallbackMode        string            `json:"callbackMode,omitempty"`        // "single" (default) or "streaming"
	CallbackAccumulate  bool              `json:"callbackAccumulate,omitempty"`  // streaming: true=accumulate all results as JSON array
	CallbackAuth        string            `json:"callbackAuth,omitempty"`        // "bearer" (default), "open", "signature"
	CallbackContentType string            `json:"callbackContentType,omitempty"` // callback content type, default application/json
	CallbackResponseMap *ResponseMapping  `json:"callbackResponseMap,omitempty"` // extract status/data from webhook body
	OnTimeout           string            `json:"onTimeout,omitempty"`           // timeout behavior: stop / skip

	// Human gate step (type="human").
	HumanSubtype   string   `json:"humanSubtype,omitempty"`   // "approval", "action", "input"
	HumanPrompt    string   `json:"humanPrompt,omitempty"`    // message shown to human (supports {{var}})
	HumanAssignee  string   `json:"humanAssignee,omitempty"`  // assigned person (supports {{var}})
	HumanTimeout   string   `json:"humanTimeout,omitempty"`   // duration string, e.g. "24h" (empty = 72h default)
	HumanOnTimeout string   `json:"humanOnTimeout,omitempty"` // "stop" (default), "skip", "approve"
	HumanInputKey  string   `json:"humanInputKey,omitempty"`  // subtype=input: response written to step output under this key
	HumanOptions   []string          `json:"humanOptions,omitempty"`   // reserved: custom option labels for approval
	HumanContext    map[string]string `json:"humanContext,omitempty"`   // key-value context shown in gate card
}

// ResponseMapping extracts structured data from arbitrary webhook bodies.
type ResponseMapping struct {
	StatusPath string `json:"statusPath,omitempty"` // JSONPath-like: "data.object.status"
	DataPath   string `json:"dataPath,omitempty"`   // JSONPath-like: "data.object"
	DonePath   string `json:"donePath,omitempty"`   // streaming: field path to check for completion
	DoneValue  string `json:"doneValue,omitempty"`  // streaming: DonePath value that indicates done
}

// WorkflowContext holds runtime state for a workflow execution.
type WorkflowContext struct {
	Input map[string]string             // workflow input variables
	Steps map[string]*WorkflowStepResult // completed step results
	Env   map[string]string             // environment snapshot
}

// WorkflowStepResult stores the output of a completed step.
type WorkflowStepResult struct {
	Output string `json:"output"`
	Status string `json:"status"` // "success", "error", "skipped", "timeout"
	Error  string `json:"error,omitempty"`
}

// TemplateSummary holds summary info for a workflow template.
type TemplateSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	StepCount   int      `json:"stepCount"`
	Variables   []string `json:"variables"`
	Category    string   `json:"category"`
}
