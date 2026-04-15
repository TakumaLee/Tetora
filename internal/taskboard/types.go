package taskboard

// TaskBoard represents a single task on the board.
type TaskBoard struct {
	ID            string   `json:"id"`
	Project       string   `json:"project"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Status        string   `json:"status"`        // backlog/todo/doing/review/done/failed
	Assignee      string   `json:"assignee"`      // agent name
	Priority      string   `json:"priority"`      // low/normal/high/urgent
	Model         string   `json:"model"`         // per-task model override
	ParentID      string   `json:"parentId"`      // parent task ID (for subtasks)
	DependsOn     []string `json:"dependsOn"`     // task IDs this task depends on
	Type          string   `json:"type"`          // feat/fix/refactor/chore
	Workflow      string   `json:"workflow"`      // workflow name override
	DiscordThread string   `json:"discordThread"` // Discord thread ID
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
	CompletedAt   string   `json:"completedAt"`
	RetryCount    int      `json:"retryCount"`    // number of auto-retries
	ExecutionCount int     `json:"executionCount"` // total number of dispatch executions (hard limit guard)
	CostUSD       float64  `json:"costUsd"`       // cost in USD
	DurationMs    int64    `json:"durationMs"`    // execution duration in ms
	SessionID     string   `json:"sessionId"`     // claude session ID
	WorkflowRunID string   `json:"workflowRunId"` // workflow run ID
	Workdirs       []string `json:"workdirs"`       // explicit directories this task operates in (for coord region)
	AllowDangerous bool     `json:"allowDangerous"` // bypass dangerous-ops check when dispatching
}

// TaskComment is a comment on a task.
type TaskComment struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	Author    string `json:"author"` // agent name or "user"
	Content   string `json:"content"`
	Type      string `json:"type"`   // spec/context/log/system (default: log)
	CreatedAt string `json:"createdAt"`
}

// TaskListResult holds a paginated list of tasks.
type TaskListResult struct {
	Tasks      []TaskBoard `json:"tasks"`
	Pagination Pagination  `json:"pagination"`
}

// Pagination holds pagination metadata.
type Pagination struct {
	Page    int  `json:"page"`
	Limit   int  `json:"limit"`
	Total   int  `json:"total"`
	HasMore bool `json:"hasMore"`
}

// BoardView represents all tasks grouped by status with aggregate stats.
type BoardView struct {
	Columns   map[string][]TaskBoard `json:"columns"`
	Stats     BoardStats             `json:"stats"`
	Projects  []string               `json:"projects"`
	Agents    []string               `json:"agents"`
	Workflows []string               `json:"workflows"`
}

// BoardStats contains aggregate statistics for the board view.
type BoardStats struct {
	Total     int            `json:"total"`
	ByStatus  map[string]int `json:"byStatus"`
	TotalCost float64        `json:"totalCost"`
}

// BoardFilter holds optional filters for GetBoardView.
type BoardFilter struct {
	Project     string
	Assignee    string
	Priority    string
	Workflow    string
	IncludeDone bool // if false (default), exclude done/failed statuses from board query
}

// ProjectStats contains task counts and cost for a specific project.
type ProjectStats struct {
	ProjectID  string         `json:"projectId"`
	TaskCounts map[string]int `json:"taskCounts"`
	TotalCost  float64        `json:"totalCost"`
	TotalTasks int            `json:"totalTasks"`
}
