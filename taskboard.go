package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// --- Task Board Types ---

type TaskBoard struct {
	ID           string   `json:"id"`
	Project      string   `json:"project"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Status       string   `json:"status"`        // backlog/todo/doing/review/done/failed
	Assignee     string   `json:"assignee"`      // agent name
	Priority     string   `json:"priority"`      // low/normal/high/urgent
	Model        string   `json:"model"`         // per-task model override (e.g. "sonnet", "haiku", "opus")
	DependsOn    []string `json:"dependsOn"`     // task IDs this task depends on
	DiscordThread string  `json:"discordThread"` // Discord thread ID
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
	CompletedAt  string   `json:"completedAt"`
	RetryCount   int      `json:"retryCount"`    // number of auto-retries
	CostUSD      float64  `json:"costUsd"`       // cost in USD
	DurationMs   int64    `json:"durationMs"`    // execution duration in ms
	SessionID    string   `json:"sessionId"`     // claude session ID
}

type TaskComment struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	Author    string `json:"author"` // agent name or "user"
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
}

type TaskBoardDispatchConfig struct {
	Enabled            bool    `json:"enabled"`
	Interval           string  `json:"interval,omitempty"`           // default "5m"
	DefaultModel       string  `json:"defaultModel,omitempty"`       // override model for auto-dispatched tasks
	MaxBudget          float64 `json:"maxBudget,omitempty"`          // max cost per task in USD (default: no limit)
	DefaultAgent       string  `json:"defaultAgent,omitempty"`       // fallback agent for unassigned todo tasks
	BacklogAgent       string  `json:"backlogAgent,omitempty"`       // agent for backlog triage (default: "ruri")
	ReviewAgent        string  `json:"reviewAgent,omitempty"`        // agent for review verification (default: "ruri")
	StuckThreshold     string  `json:"stuckThreshold,omitempty"`     // max time a task can be in "doing" before reset (default: "2h")
	MaxConcurrentTasks int     `json:"maxConcurrentTasks,omitempty"` // max tasks dispatched per scan cycle (0 = unlimited)
}

type TaskBoardConfig struct {
	Enabled       bool                    `json:"enabled"`
	MaxRetries    int                     `json:"maxRetries,omitempty"`    // default 3
	RequireReview bool                    `json:"requireReview,omitempty"` // quality gate
	AutoDispatch  TaskBoardDispatchConfig `json:"autoDispatch,omitempty"`
}

func (c TaskBoardConfig) maxRetriesOrDefault() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return 3
}

// --- Task Board Engine ---

type TaskBoardEngine struct {
	dbPath string
	config TaskBoardConfig
	webhooks []WebhookConfig
}

func newTaskBoardEngine(dbPath string, config TaskBoardConfig, webhooks []WebhookConfig) *TaskBoardEngine {
	return &TaskBoardEngine{
		dbPath: dbPath,
		config: config,
		webhooks: webhooks,
	}
}

// initTaskBoardSchema creates the tasks and task_comments tables if they don't exist.
func (tb *TaskBoardEngine) initTaskBoardSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		project TEXT DEFAULT 'default',
		title TEXT NOT NULL,
		description TEXT DEFAULT '',
		status TEXT DEFAULT 'backlog',
		assignee TEXT DEFAULT '',
		priority TEXT DEFAULT 'normal',
		depends_on TEXT DEFAULT '[]',
		discord_thread_id TEXT DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		completed_at TEXT DEFAULT '',
		retry_count INTEGER DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS task_comments (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		author TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
	CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks(assignee);
	CREATE INDEX IF NOT EXISTS idx_task_comments_task_id ON task_comments(task_id);
	`
	cmd := exec.Command("sqlite3", tb.dbPath, schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init task board schema: %s: %w", string(out), err)
	}

	// Migrate: add cost/duration/session columns (ignore errors for existing columns).
	migrations := []string{
		"ALTER TABLE tasks ADD COLUMN cost_usd REAL DEFAULT 0;",
		"ALTER TABLE tasks ADD COLUMN duration_ms INTEGER DEFAULT 0;",
		"ALTER TABLE tasks ADD COLUMN session_id TEXT DEFAULT '';",
		"ALTER TABLE tasks ADD COLUMN model TEXT DEFAULT '';",
	}
	for _, m := range migrations {
		exec.Command("sqlite3", tb.dbPath, m).CombinedOutput() // ignore duplicate column errors
	}

	return nil
}

// ListTasks returns tasks filtered by status and assignee.
func (tb *TaskBoardEngine) ListTasks(status, assignee, project string) ([]TaskBoard, error) {
	var whereClauses []string
	if status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = '%s'", escapeSQLite(status)))
	}
	if assignee != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("assignee = '%s'", escapeSQLite(assignee)))
	}
	if project != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("project = '%s'", escapeSQLite(project)))
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model
		FROM tasks %s
		ORDER BY
			CASE priority
				WHEN 'urgent' THEN 1
				WHEN 'high' THEN 2
				WHEN 'normal' THEN 3
				WHEN 'low' THEN 4
				ELSE 5
			END,
			created_at DESC
		LIMIT 100
	`, whereClause)

	rows, err := queryDB(tb.dbPath, sql)
	if err != nil {
		return nil, err
	}

	var tasks []TaskBoard
	for _, row := range rows {
		dependsOnJSON := fmt.Sprintf("%v", row["depends_on"])
		var dependsOn []string
		if dependsOnJSON != "" && dependsOnJSON != "[]" {
			json.Unmarshal([]byte(dependsOnJSON), &dependsOn)
		}

		tasks = append(tasks, TaskBoard{
			ID:           fmt.Sprintf("%v", row["id"]),
			Project:      fmt.Sprintf("%v", row["project"]),
			Title:        fmt.Sprintf("%v", row["title"]),
			Description:  fmt.Sprintf("%v", row["description"]),
			Status:       fmt.Sprintf("%v", row["status"]),
			Assignee:     fmt.Sprintf("%v", row["assignee"]),
			Priority:     fmt.Sprintf("%v", row["priority"]),
			Model:        fmt.Sprintf("%v", row["model"]),
			DependsOn:    dependsOn,
			DiscordThread: fmt.Sprintf("%v", row["discord_thread_id"]),
			CreatedAt:    fmt.Sprintf("%v", row["created_at"]),
			UpdatedAt:    fmt.Sprintf("%v", row["updated_at"]),
			CompletedAt:  fmt.Sprintf("%v", row["completed_at"]),
			RetryCount:   int(getFloat64(row, "retry_count")),
			CostUSD:      getFloat64(row, "cost_usd"),
			DurationMs:   int64(getFloat64(row, "duration_ms")),
			SessionID:    fmt.Sprintf("%v", row["session_id"]),
		})
	}
	return tasks, nil
}

// CreateTask creates a new task.
func (tb *TaskBoardEngine) CreateTask(task TaskBoard) (TaskBoard, error) {
	if task.ID == "" {
		task.ID = generateID("task")
	}
	if task.Status == "" {
		task.Status = "backlog"
	}
	if task.Priority == "" {
		task.Priority = "normal"
	}
	if task.Project == "" {
		task.Project = "default"
	}
	task.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	task.UpdatedAt = task.CreatedAt

	dependsOnJSON, _ := json.Marshal(task.DependsOn)
	if task.DependsOn == nil {
		dependsOnJSON = []byte("[]")
	}

	sql := fmt.Sprintf(`
		INSERT INTO tasks (id, project, title, description, status, assignee, priority, model, depends_on, discord_thread_id, created_at, updated_at, retry_count)
		VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', 0)
	`,
		escapeSQLite(task.ID),
		escapeSQLite(task.Project),
		escapeSQLite(task.Title),
		escapeSQLite(task.Description),
		escapeSQLite(task.Status),
		escapeSQLite(task.Assignee),
		escapeSQLite(task.Priority),
		escapeSQLite(task.Model),
		escapeSQLite(string(dependsOnJSON)),
		escapeSQLite(task.DiscordThread),
		task.CreatedAt,
		task.UpdatedAt,
	)

	cmd := exec.Command("sqlite3", tb.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return TaskBoard{}, fmt.Errorf("create task: %s: %w", string(out), err)
	}

	// Fire webhook event.
	go tb.fireWebhook("task.created", task)

	return task, nil
}

// UpdateTask updates task fields.
func (tb *TaskBoardEngine) UpdateTask(id string, updates map[string]any) (TaskBoard, error) {
	// Build SET clause from updates map.
	var setClauses []string
	for key, val := range updates {
		switch key {
		case "title", "description", "priority", "assignee", "project", "discordThread", "model":
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", toSnakeCase(key), escapeSQLite(fmt.Sprintf("%v", val))))
		case "dependsOn":
			dependsOnJSON, _ := json.Marshal(val)
			setClauses = append(setClauses, fmt.Sprintf("depends_on = '%s'", escapeSQLite(string(dependsOnJSON))))
		}
	}

	if len(setClauses) == 0 {
		return TaskBoard{}, fmt.Errorf("no valid update fields")
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_at = '%s'", time.Now().UTC().Format(time.RFC3339)))

	sql := fmt.Sprintf(`UPDATE tasks SET %s WHERE id = '%s'`,
		strings.Join(setClauses, ", "),
		escapeSQLite(id),
	)

	cmd := exec.Command("sqlite3", tb.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return TaskBoard{}, fmt.Errorf("update task: %s: %w", string(out), err)
	}

	// Fetch and return updated task.
	return tb.GetTask(id)
}

// DeleteTask removes a task and its comments from the DB.
func (tb *TaskBoardEngine) DeleteTask(id string) error {
	sql := fmt.Sprintf(`
		DELETE FROM task_comments WHERE task_id = '%s';
		DELETE FROM tasks WHERE id = '%s';
	`, escapeSQLite(id), escapeSQLite(id))
	cmd := exec.Command("sqlite3", tb.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete task: %s: %w", string(out), err)
	}
	return nil
}

// GetTask retrieves a single task by ID.
func (tb *TaskBoardEngine) GetTask(id string) (TaskBoard, error) {
	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model
		FROM tasks WHERE id = '%s'
	`, escapeSQLite(id))

	rows, err := queryDB(tb.dbPath, sql)
	if err != nil {
		return TaskBoard{}, err
	}
	if len(rows) == 0 {
		return TaskBoard{}, fmt.Errorf("task not found")
	}

	row := rows[0]
	dependsOnJSON := fmt.Sprintf("%v", row["depends_on"])
	var dependsOn []string
	if dependsOnJSON != "" && dependsOnJSON != "[]" {
		json.Unmarshal([]byte(dependsOnJSON), &dependsOn)
	}

	return TaskBoard{
		ID:           fmt.Sprintf("%v", row["id"]),
		Project:      fmt.Sprintf("%v", row["project"]),
		Title:        fmt.Sprintf("%v", row["title"]),
		Description:  fmt.Sprintf("%v", row["description"]),
		Status:       fmt.Sprintf("%v", row["status"]),
		Assignee:     fmt.Sprintf("%v", row["assignee"]),
		Priority:     fmt.Sprintf("%v", row["priority"]),
		Model:        fmt.Sprintf("%v", row["model"]),
		DependsOn:    dependsOn,
		DiscordThread: fmt.Sprintf("%v", row["discord_thread_id"]),
		CreatedAt:    fmt.Sprintf("%v", row["created_at"]),
		UpdatedAt:    fmt.Sprintf("%v", row["updated_at"]),
		CompletedAt:  fmt.Sprintf("%v", row["completed_at"]),
		RetryCount:   int(getFloat64(row, "retry_count")),
		CostUSD:      getFloat64(row, "cost_usd"),
		DurationMs:   int64(getFloat64(row, "duration_ms")),
		SessionID:    fmt.Sprintf("%v", row["session_id"]),
	}, nil
}

// MoveTask moves a task to a new status, enforcing dependency checks.
func (tb *TaskBoardEngine) MoveTask(id, newStatus string) (TaskBoard, error) {
	task, err := tb.GetTask(id)
	if err != nil {
		return TaskBoard{}, err
	}

	// Validate status transition.
	validStatuses := []string{"backlog", "todo", "doing", "review", "done", "failed"}
	isValid := false
	for _, s := range validStatuses {
		if s == newStatus {
			isValid = true
			break
		}
	}
	if !isValid {
		return TaskBoard{}, fmt.Errorf("invalid status: %s", newStatus)
	}

	// Dependency check: can't move to "doing" if dependencies aren't done.
	if newStatus == "doing" && len(task.DependsOn) > 0 {
		for _, depID := range task.DependsOn {
			dep, err := tb.GetTask(depID)
			if err != nil {
				return TaskBoard{}, fmt.Errorf("dependency %s not found", depID)
			}
			if dep.Status != "done" {
				return TaskBoard{}, fmt.Errorf("dependency %s (status: %s) must be done before starting this task", depID, dep.Status)
			}
		}
	}

	// Quality gate: if requireReview is enabled, tasks must pass "review" before "done".
	if tb.config.RequireReview && newStatus == "done" && task.Status != "review" {
		return TaskBoard{}, fmt.Errorf("task must pass review before completion")
	}

	// Update status.
	nowISO := time.Now().UTC().Format(time.RFC3339)
	completedAt := ""
	if newStatus == "done" {
		completedAt = nowISO
	}

	sql := fmt.Sprintf(`
		UPDATE tasks SET status = '%s', updated_at = '%s', completed_at = '%s' WHERE id = '%s'
	`, escapeSQLite(newStatus), nowISO, completedAt, escapeSQLite(id))

	cmd := exec.Command("sqlite3", tb.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return TaskBoard{}, fmt.Errorf("move task: %s: %w", string(out), err)
	}

	task.Status = newStatus
	task.UpdatedAt = nowISO
	task.CompletedAt = completedAt

	// Fire webhook event.
	go tb.fireWebhook("task.moved", task)

	return task, nil
}

// AssignTask assigns a task to an agent.
func (tb *TaskBoardEngine) AssignTask(id, assignee string) (TaskBoard, error) {
	sql := fmt.Sprintf(`
		UPDATE tasks SET assignee = '%s', updated_at = '%s' WHERE id = '%s'
	`, escapeSQLite(assignee), time.Now().UTC().Format(time.RFC3339), escapeSQLite(id))

	cmd := exec.Command("sqlite3", tb.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return TaskBoard{}, fmt.Errorf("assign task: %s: %w", string(out), err)
	}

	task, err := tb.GetTask(id)
	if err != nil {
		return TaskBoard{}, err
	}

	// Fire webhook event.
	go tb.fireWebhook("task.assigned", task)

	return task, nil
}

// AddComment adds a comment to a task.
func (tb *TaskBoardEngine) AddComment(taskID, author, content string) (TaskComment, error) {
	comment := TaskComment{
		ID:        generateID("comment"),
		TaskID:    taskID,
		Author:    author,
		Content:   content,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	sql := fmt.Sprintf(`
		INSERT INTO task_comments (id, task_id, author, content, created_at)
		VALUES ('%s', '%s', '%s', '%s', '%s')
	`,
		escapeSQLite(comment.ID),
		escapeSQLite(comment.TaskID),
		escapeSQLite(comment.Author),
		escapeSQLite(comment.Content),
		comment.CreatedAt,
	)

	cmd := exec.Command("sqlite3", tb.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return TaskComment{}, fmt.Errorf("add comment: %s: %w", string(out), err)
	}

	// Fire webhook event.
	go tb.fireWebhook("comment.added", map[string]any{
		"taskId": taskID,
		"comment": comment,
	})

	return comment, nil
}

// GetThread returns all comments for a task.
func (tb *TaskBoardEngine) GetThread(taskID string) ([]TaskComment, error) {
	sql := fmt.Sprintf(`
		SELECT id, task_id, author, content, created_at
		FROM task_comments
		WHERE task_id = '%s'
		ORDER BY created_at ASC
		LIMIT 100
	`, escapeSQLite(taskID))

	rows, err := queryDB(tb.dbPath, sql)
	if err != nil {
		return nil, err
	}

	var comments []TaskComment
	for _, row := range rows {
		comments = append(comments, TaskComment{
			ID:        fmt.Sprintf("%v", row["id"]),
			TaskID:    fmt.Sprintf("%v", row["task_id"]),
			Author:    fmt.Sprintf("%v", row["author"]),
			Content:   fmt.Sprintf("%v", row["content"]),
			CreatedAt: fmt.Sprintf("%v", row["created_at"]),
		})
	}
	return comments, nil
}

// AutoRetryFailed moves failed tasks back to "todo" if retry count < maxRetries.
func (tb *TaskBoardEngine) AutoRetryFailed() error {
	maxRetries := tb.config.maxRetriesOrDefault()
	sql := fmt.Sprintf(`
		SELECT id, retry_count FROM tasks WHERE status = 'failed' AND retry_count < %d
	`, maxRetries)

	rows, err := queryDB(tb.dbPath, sql)
	if err != nil {
		return err
	}

	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		retryCount := int(getFloat64(row, "retry_count")) + 1

		updateSQL := fmt.Sprintf(`
			UPDATE tasks SET status = 'todo', retry_count = %d, updated_at = '%s' WHERE id = '%s'
		`, retryCount, time.Now().UTC().Format(time.RFC3339), escapeSQLite(id))

		cmd := exec.Command("sqlite3", tb.dbPath, updateSQL)
		if out, err := cmd.CombinedOutput(); err != nil {
			logWarn("auto retry failed task", "id", id, "error", string(out))
			continue
		}

		logInfo("auto retried failed task", "id", id, "retryCount", retryCount)
	}

	return nil
}

// fireWebhook sends a webhook notification for task board events.
func (tb *TaskBoardEngine) fireWebhook(event string, payload any) {
	fullEvent := "taskboard." + event
	for _, wh := range tb.webhooks {
		// Check if webhook listens to this event.
		if !webhookMatchesEvent(wh, fullEvent) {
			continue
		}

		go func(wh WebhookConfig, payload any) {
			body := map[string]any{
				"event":     fullEvent,
				"data":      payload,
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			}

			bodyJSON, err := json.Marshal(body)
			if err != nil {
				logError("webhook body marshal failed", "error", err)
				return
			}

			client := &http.Client{Timeout: 5 * time.Second}
			req, err := http.NewRequest("POST", wh.URL, bytes.NewReader(bodyJSON))
			if err != nil {
				logError("webhook request creation failed", "url", wh.URL, "error", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			for k, v := range wh.Headers {
				req.Header.Set(k, v)
			}

			resp, err := client.Do(req)
			if err != nil {
				logError("webhook POST failed", "url", wh.URL, "error", err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				logWarn("webhook POST returned error status", "url", wh.URL, "status", resp.StatusCode)
			}
		}(wh, payload)
	}
}

// --- Utility Functions ---

func generateID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func toSnakeCase(s string) string {
	// Simple camelCase to snake_case converter for common task fields.
	switch s {
	case "discordThread":
		return "discord_thread_id"
	case "dependsOn":
		return "depends_on"
	default:
		return s
	}
}

func getFloat64(row map[string]any, key string) float64 {
	if val, ok := row[key]; ok {
		if f, ok := val.(float64); ok {
			return f
		}
	}
	return 0
}

// --- Board View & Project Stats ---

type BoardView struct {
	Columns  map[string][]TaskBoard `json:"columns"`
	Stats    BoardStats             `json:"stats"`
	Projects []string               `json:"projects"`
	Agents   []string               `json:"agents"`
}

type BoardStats struct {
	Total     int            `json:"total"`
	ByStatus  map[string]int `json:"byStatus"`
	TotalCost float64        `json:"totalCost"`
}

// GetBoardView returns all tasks grouped by status column with aggregate stats.
func (tb *TaskBoardEngine) GetBoardView(project, assignee, priority string) (*BoardView, error) {
	var whereClauses []string
	if project != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("project = '%s'", escapeSQLite(project)))
	}
	if assignee != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("assignee = '%s'", escapeSQLite(assignee)))
	}
	if priority != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("priority = '%s'", escapeSQLite(priority)))
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model
		FROM tasks %s
		ORDER BY
			CASE priority
				WHEN 'urgent' THEN 1
				WHEN 'high' THEN 2
				WHEN 'normal' THEN 3
				WHEN 'low' THEN 4
				ELSE 5
			END,
			created_at DESC
		LIMIT 500
	`, whereClause)

	rows, err := queryDB(tb.dbPath, sql)
	if err != nil {
		return nil, err
	}

	statuses := []string{"backlog", "todo", "doing", "review", "done", "failed"}
	columns := make(map[string][]TaskBoard)
	for _, s := range statuses {
		columns[s] = []TaskBoard{}
	}

	byStatus := make(map[string]int)
	projectSet := make(map[string]bool)
	agentSet := make(map[string]bool)
	var totalCost float64

	for _, row := range rows {
		dependsOnJSON := fmt.Sprintf("%v", row["depends_on"])
		var dependsOn []string
		if dependsOnJSON != "" && dependsOnJSON != "[]" {
			json.Unmarshal([]byte(dependsOnJSON), &dependsOn)
		}

		t := TaskBoard{
			ID:            fmt.Sprintf("%v", row["id"]),
			Project:       fmt.Sprintf("%v", row["project"]),
			Title:         fmt.Sprintf("%v", row["title"]),
			Description:   fmt.Sprintf("%v", row["description"]),
			Status:        fmt.Sprintf("%v", row["status"]),
			Assignee:      fmt.Sprintf("%v", row["assignee"]),
			Priority:      fmt.Sprintf("%v", row["priority"]),
			Model:         fmt.Sprintf("%v", row["model"]),
			DependsOn:     dependsOn,
			DiscordThread: fmt.Sprintf("%v", row["discord_thread_id"]),
			CreatedAt:     fmt.Sprintf("%v", row["created_at"]),
			UpdatedAt:     fmt.Sprintf("%v", row["updated_at"]),
			CompletedAt:   fmt.Sprintf("%v", row["completed_at"]),
			RetryCount:    int(getFloat64(row, "retry_count")),
			CostUSD:       getFloat64(row, "cost_usd"),
			DurationMs:    int64(getFloat64(row, "duration_ms")),
			SessionID:     fmt.Sprintf("%v", row["session_id"]),
		}

		columns[t.Status] = append(columns[t.Status], t)
		byStatus[t.Status]++
		totalCost += t.CostUSD

		if t.Project != "" {
			projectSet[t.Project] = true
		}
		if t.Assignee != "" {
			agentSet[t.Assignee] = true
		}
	}

	var projects []string
	for p := range projectSet {
		projects = append(projects, p)
	}
	var agents []string
	for a := range agentSet {
		agents = append(agents, a)
	}

	return &BoardView{
		Columns:  columns,
		Stats:    BoardStats{Total: len(rows), ByStatus: byStatus, TotalCost: totalCost},
		Projects: projects,
		Agents:   agents,
	}, nil
}

// --- Project Stats ---

type ProjectStats struct {
	ProjectID  string         `json:"projectId"`
	TaskCounts map[string]int `json:"taskCounts"`
	TotalCost  float64        `json:"totalCost"`
	TotalTasks int            `json:"totalTasks"`
}

// GetProjectStats returns task counts and cost for a specific project.
func (tb *TaskBoardEngine) GetProjectStats(projectID string) (*ProjectStats, error) {
	sql := fmt.Sprintf(`
		SELECT status, COUNT(*) as cnt, COALESCE(SUM(cost_usd),0) as cost
		FROM tasks
		WHERE project = '%s'
		GROUP BY status
	`, escapeSQLite(projectID))

	rows, err := queryDB(tb.dbPath, sql)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	var totalCost float64
	var totalTasks int
	for _, row := range rows {
		status := fmt.Sprintf("%v", row["status"])
		cnt := int(getFloat64(row, "cnt"))
		cost := getFloat64(row, "cost")
		counts[status] = cnt
		totalTasks += cnt
		totalCost += cost
	}

	return &ProjectStats{
		ProjectID:  projectID,
		TaskCounts: counts,
		TotalCost:  totalCost,
		TotalTasks: totalTasks,
	}, nil
}

// --- CLI Commands ---

func cmdTask(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora task <list|create|move|assign|comment|thread>")
		fmt.Println("\nCommands:")
		fmt.Println("  list [--status=STATUS] [--assignee=AGENT] [--project=PROJECT]")
		fmt.Println("  create --title=TITLE [--description=DESC] [--priority=PRIORITY] [--assignee=AGENT]")
		fmt.Println("  move TASK_ID --status=STATUS")
		fmt.Println("  assign TASK_ID --assignee=AGENT")
		fmt.Println("  comment TASK_ID --author=AUTHOR --content=CONTENT")
		fmt.Println("  thread TASK_ID")
		os.Exit(0)
	}

	cfg := loadConfig("")
	if !cfg.TaskBoard.Enabled {
		fmt.Println("Error: task board not enabled in config")
		os.Exit(1)
	}

	tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
	if err := tb.initTaskBoardSchema(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	cmd := args[0]
	args = args[1:]

	switch cmd {
	case "list":
		var status, assignee, project string
		for _, arg := range args {
			if strings.HasPrefix(arg, "--status=") {
				status = strings.TrimPrefix(arg, "--status=")
			} else if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			} else if strings.HasPrefix(arg, "--project=") {
				project = strings.TrimPrefix(arg, "--project=")
			}
		}

		tasks, err := tb.ListTasks(status, assignee, project)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if len(tasks) == 0 {
			fmt.Println("No tasks found")
			return
		}

		fmt.Printf("Found %d tasks:\n\n", len(tasks))
		for _, t := range tasks {
			fmt.Printf("ID: %s\n", t.ID)
			fmt.Printf("Title: %s\n", t.Title)
			fmt.Printf("Status: %s | Priority: %s | Assignee: %s\n", t.Status, t.Priority, t.Assignee)
			if t.Description != "" {
				fmt.Printf("Description: %s\n", t.Description)
			}
			fmt.Printf("Created: %s | Updated: %s\n", t.CreatedAt, t.UpdatedAt)
			fmt.Println()
		}

	case "create":
		var title, description, priority, assignee string
		for _, arg := range args {
			if strings.HasPrefix(arg, "--title=") {
				title = strings.TrimPrefix(arg, "--title=")
			} else if strings.HasPrefix(arg, "--description=") {
				description = strings.TrimPrefix(arg, "--description=")
			} else if strings.HasPrefix(arg, "--priority=") {
				priority = strings.TrimPrefix(arg, "--priority=")
			} else if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			}
		}

		if title == "" {
			fmt.Println("Error: --title is required")
			os.Exit(1)
		}

		task, err := tb.CreateTask(TaskBoard{
			Title:       title,
			Description: description,
			Priority:    priority,
			Assignee:    assignee,
		})
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Created task: %s\n", task.ID)
		fmt.Printf("Title: %s\n", task.Title)
		fmt.Printf("Status: %s\n", task.Status)

	case "move":
		if len(args) < 2 {
			fmt.Println("Usage: tetora task move TASK_ID --status=STATUS")
			os.Exit(1)
		}

		taskID := args[0]
		var newStatus string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--status=") {
				newStatus = strings.TrimPrefix(arg, "--status=")
			}
		}

		if newStatus == "" {
			fmt.Println("Error: --status is required")
			os.Exit(1)
		}

		task, err := tb.MoveTask(taskID, newStatus)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Moved task %s to %s\n", task.ID, task.Status)

	case "assign":
		if len(args) < 2 {
			fmt.Println("Usage: tetora task assign TASK_ID --assignee=AGENT")
			os.Exit(1)
		}

		taskID := args[0]
		var assignee string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			}
		}

		if assignee == "" {
			fmt.Println("Error: --assignee is required")
			os.Exit(1)
		}

		task, err := tb.AssignTask(taskID, assignee)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Assigned task %s to %s\n", task.ID, task.Assignee)

	case "comment":
		if len(args) < 3 {
			fmt.Println("Usage: tetora task comment TASK_ID --author=AUTHOR --content=CONTENT")
			os.Exit(1)
		}

		taskID := args[0]
		var author, content string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--author=") {
				author = strings.TrimPrefix(arg, "--author=")
			} else if strings.HasPrefix(arg, "--content=") {
				content = strings.TrimPrefix(arg, "--content=")
			}
		}

		if author == "" || content == "" {
			fmt.Println("Error: --author and --content are required")
			os.Exit(1)
		}

		comment, err := tb.AddComment(taskID, author, content)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Added comment %s to task %s\n", comment.ID, taskID)

	case "thread":
		if len(args) < 1 {
			fmt.Println("Usage: tetora task thread TASK_ID")
			os.Exit(1)
		}

		taskID := args[0]
		comments, err := tb.GetThread(taskID)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if len(comments) == 0 {
			fmt.Println("No comments found")
			return
		}

		fmt.Printf("Thread for task %s (%d comments):\n\n", taskID, len(comments))
		for _, c := range comments {
			fmt.Printf("[%s] %s:\n%s\n\n", c.CreatedAt, c.Author, c.Content)
		}

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		fmt.Println("Use 'tetora task' to see available commands")
		os.Exit(1)
	}
}
