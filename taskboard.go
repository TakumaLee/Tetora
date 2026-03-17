package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"


	"tetora/internal/log"
	"tetora/internal/db"
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
	ParentID     string   `json:"parentId"`      // parent task ID (for subtasks)
	DependsOn    []string `json:"dependsOn"`     // task IDs this task depends on
	Type         string   `json:"type"`          // feat/fix/refactor/chore (default: feat)
	Workflow     string   `json:"workflow"`      // workflow name override ("" = use config default, "none" = skip)
	DiscordThread string  `json:"discordThread"` // Discord thread ID
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
	CompletedAt  string   `json:"completedAt"`
	RetryCount   int      `json:"retryCount"`    // number of auto-retries
	CostUSD      float64  `json:"costUsd"`       // cost in USD
	DurationMs   int64    `json:"durationMs"`    // execution duration in ms
	SessionID     string   `json:"sessionId"`     // claude session ID
	WorkflowRunID string   `json:"workflowRunId"` // workflow run ID (links task to workflow execution)
}

type TaskComment struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	Author    string `json:"author"` // agent name or "user"
	Content   string `json:"content"`
	Type      string `json:"type"`   // spec/context/log/system (default: log)
	CreatedAt string `json:"createdAt"`
}



// --- Task Board Engine ---

type TaskBoardEngine struct {
	dbPath   string
	config   TaskBoardConfig
	webhooks []WebhookConfig
	whSem    chan struct{}
}

func newTaskBoardEngine(dbPath string, config TaskBoardConfig, webhooks []WebhookConfig) *TaskBoardEngine {
	return &TaskBoardEngine{
		dbPath:   dbPath,
		config:   config,
		webhooks: webhooks,
		whSem:    make(chan struct{}, 8),
	}
}

// initTaskBoardSchema creates the tasks and task_comments tables if they don't exist.
func (tb *TaskBoardEngine) initTaskBoardSchema() error {
	if err := db.Pragma(tb.dbPath); err != nil {
		return fmt.Errorf("init task board db.Pragma: %w", err)
	}

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
	if err := db.Exec(tb.dbPath, schema); err != nil {
		return fmt.Errorf("init task board schema: %w", err)
	}

	// Migrate: add columns (ignore errors for existing columns).
	migrations := []string{
		"ALTER TABLE tasks ADD COLUMN cost_usd REAL DEFAULT 0;",
		"ALTER TABLE tasks ADD COLUMN duration_ms INTEGER DEFAULT 0;",
		"ALTER TABLE tasks ADD COLUMN session_id TEXT DEFAULT '';",
		"ALTER TABLE tasks ADD COLUMN model TEXT DEFAULT '';",
		"ALTER TABLE tasks ADD COLUMN parent_id TEXT DEFAULT '';",
		"ALTER TABLE tasks ADD COLUMN workflow TEXT DEFAULT '';",
		"ALTER TABLE tasks ADD COLUMN type TEXT DEFAULT 'feat';",
		"ALTER TABLE tasks ADD COLUMN workflow_run_id TEXT DEFAULT '';",
	}
	// task_comments migrations.
	commentMigrations := []string{
		"ALTER TABLE task_comments ADD COLUMN type TEXT DEFAULT 'log';",
	}
	for _, m := range commentMigrations {
		db.Exec(tb.dbPath, m) // ignore duplicate column errors
	}

	// Index for parent-child lookups (ignore error if already exists).
	postMigrations := []string{
		"CREATE INDEX IF NOT EXISTS idx_tasks_parent_id ON tasks(parent_id);",
	}
	for _, m := range migrations {
		db.Exec(tb.dbPath, m) // ignore duplicate column errors
	}
	for _, m := range postMigrations {
		db.Exec(tb.dbPath, m)
	}

	return nil
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

// ListTasks returns tasks filtered by status and assignee (unpaginated, backward-compatible).
func (tb *TaskBoardEngine) ListTasks(status, assignee, project string) ([]TaskBoard, error) {
	result, err := tb.ListTasksPaginated(status, assignee, project, 1, 100)
	if err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

// ListTasksPaginated returns tasks with pagination support.
func (tb *TaskBoardEngine) ListTasksPaginated(status, assignee, project string, page, limit int) (*TaskListResult, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := (page - 1) * limit

	var whereClauses []string
	if status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = '%s'", db.Escape(status)))
	}
	if assignee != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("assignee = '%s'", db.Escape(assignee)))
	}
	if project != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("project = '%s'", db.Escape(project)))
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Get total count.
	countSQL := fmt.Sprintf("SELECT COUNT(*) as total FROM tasks %s", whereClause)
	countRows, err := db.Query(tb.dbPath, countSQL)
	if err != nil {
		return nil, err
	}
	total := 0
	if len(countRows) > 0 {
		if v, ok := countRows[0]["total"]; ok {
			total = toInt(v)
		}
	}

	// Get paginated tasks.
	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, type, workflow, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id
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
		LIMIT %d OFFSET %d
	`, whereClause, limit, offset)

	rows, err := db.Query(tb.dbPath, sql)
	if err != nil {
		return nil, err
	}

	var tasks []TaskBoard
	for _, row := range rows {
		tasks = append(tasks, parseTaskRow(row))
	}

	return &TaskListResult{
		Tasks: tasks,
		Pagination: Pagination{
			Page:    page,
			Limit:   limit,
			Total:   total,
			HasMore: offset+len(tasks) < total,
		},
	}, nil
}

// CreateTask creates a new task.
// Returns an error if a task with the same title already exists in a non-terminal state
// (todo, backlog, doing, review) to prevent duplicate tickets.
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

	// Dedup guard: reject if same title exists in active state.
	dupSQL := fmt.Sprintf(
		`SELECT id, status FROM tasks WHERE title = '%s' AND status IN ('todo', 'backlog', 'doing', 'review')`,
		db.Escape(task.Title),
	)
	dupRows, _ := db.Query(tb.dbPath, dupSQL)
	if len(dupRows) > 0 {
		existingID := fmt.Sprintf("%v", dupRows[0]["id"])
		existingStatus := fmt.Sprintf("%v", dupRows[0]["status"])
		return TaskBoard{}, fmt.Errorf("duplicate task: '%s' already exists (id=%s, status=%s)", task.Title, existingID, existingStatus)
	}

	task.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	task.UpdatedAt = task.CreatedAt

	dependsOnJSON, _ := json.Marshal(task.DependsOn)
	if task.DependsOn == nil {
		dependsOnJSON = []byte("[]")
	}

	if task.Type == "" {
		task.Type = "feat"
	}

	sql := fmt.Sprintf(`
		INSERT INTO tasks (id, project, title, description, status, assignee, priority, model, depends_on, type, workflow, discord_thread_id, created_at, updated_at, retry_count, parent_id)
		VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', 0, '%s')
	`,
		db.Escape(task.ID),
		db.Escape(task.Project),
		db.Escape(task.Title),
		db.Escape(task.Description),
		db.Escape(task.Status),
		db.Escape(task.Assignee),
		db.Escape(task.Priority),
		db.Escape(task.Model),
		db.Escape(string(dependsOnJSON)),
		db.Escape(task.Type),
		db.Escape(task.Workflow),
		db.Escape(task.DiscordThread),
		task.CreatedAt,
		task.UpdatedAt,
		db.Escape(task.ParentID),
	)

	if err := db.Exec(tb.dbPath, sql); err != nil {
		return TaskBoard{}, fmt.Errorf("create task: %w", err)
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
		case "title", "description", "priority", "assignee", "project", "discordThread", "model", "parentId", "workflow", "type", "workflowRunId":
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", toSnakeCase(key), db.Escape(fmt.Sprintf("%v", val))))
		case "dependsOn":
			dependsOnJSON, _ := json.Marshal(val)
			setClauses = append(setClauses, fmt.Sprintf("depends_on = '%s'", db.Escape(string(dependsOnJSON))))
		}
	}

	if len(setClauses) == 0 {
		return TaskBoard{}, fmt.Errorf("no valid update fields")
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_at = '%s'", time.Now().UTC().Format(time.RFC3339)))

	sql := fmt.Sprintf(`UPDATE tasks SET %s WHERE id = '%s'`,
		strings.Join(setClauses, ", "),
		db.Escape(id),
	)

	if err := db.Exec(tb.dbPath, sql); err != nil {
		return TaskBoard{}, fmt.Errorf("update task: %w", err)
	}

	// Fetch and return updated task.
	return tb.GetTask(id)
}

// DeleteTask removes a task and its comments from the DB.
func (tb *TaskBoardEngine) DeleteTask(id string) error {
	sql := fmt.Sprintf(`
		DELETE FROM task_comments WHERE task_id = '%s';
		DELETE FROM tasks WHERE id = '%s';
	`, db.Escape(id), db.Escape(id))
	if err := db.Exec(tb.dbPath, sql); err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

// GetTask retrieves a single task by ID. Normalizes bare numeric IDs to "task-{id}".
func (tb *TaskBoardEngine) GetTask(id string) (TaskBoard, error) {
	id = normalizeTaskID(id)

	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, type, workflow, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id
		FROM tasks WHERE id = '%s'
	`, db.Escape(id))

	rows, err := db.Query(tb.dbPath, sql)
	if err != nil {
		return TaskBoard{}, err
	}
	if len(rows) == 0 {
		return TaskBoard{}, fmt.Errorf("task %q not found", id)
	}

	return parseTaskRow(rows[0]), nil
}

// suggestTasks returns up to 3 recent tasks whose ID shares a prefix with the given ID.
func (tb *TaskBoardEngine) suggestTasks(id string) []TaskBoard {
	// Extract numeric portion and use first 4 digits as prefix for LIKE search.
	numeric := strings.TrimPrefix(id, "task-")
	if len(numeric) < 4 {
		return nil
	}
	prefix := "task-" + numeric[:4]

	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, type, workflow, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id
		FROM tasks WHERE id LIKE '%s%%'
		ORDER BY created_at DESC
		LIMIT 3
	`, db.Escape(prefix))

	rows, err := db.Query(tb.dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}

	var tasks []TaskBoard
	for _, row := range rows {
		tasks = append(tasks, parseTaskRow(row))
	}
	return tasks
}

// MoveTask moves a task to a new status, enforcing dependency checks.
func (tb *TaskBoardEngine) MoveTask(id, newStatus string) (TaskBoard, error) {
	task, err := tb.GetTask(id)
	if err != nil {
		return TaskBoard{}, err
	}

	// Validate status transition.
	validStatuses := []string{"idea", "needs-thought", "backlog", "todo", "doing", "review", "done", "partial-done", "failed"}
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
	`, db.Escape(newStatus), nowISO, completedAt, db.Escape(id))

	if err := db.Exec(tb.dbPath, sql); err != nil {
		return TaskBoard{}, fmt.Errorf("move task: %w", err)
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
	`, db.Escape(assignee), time.Now().UTC().Format(time.RFC3339), db.Escape(id))

	if err := db.Exec(tb.dbPath, sql); err != nil {
		return TaskBoard{}, fmt.Errorf("assign task: %w", err)
	}

	task, err := tb.GetTask(id)
	if err != nil {
		return TaskBoard{}, err
	}

	// Fire webhook event.
	go tb.fireWebhook("task.assigned", task)

	return task, nil
}

// AddComment adds a comment to a task. commentType defaults to "log" if empty.
func (tb *TaskBoardEngine) AddComment(taskID, author, content string, commentType ...string) (TaskComment, error) {
	cType := "log"
	if len(commentType) > 0 && commentType[0] != "" {
		cType = commentType[0]
	}

	comment := TaskComment{
		ID:        generateID("comment"),
		TaskID:    taskID,
		Author:    author,
		Content:   content,
		Type:      cType,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	sql := fmt.Sprintf(`
		INSERT INTO task_comments (id, task_id, author, content, type, created_at)
		VALUES ('%s', '%s', '%s', '%s', '%s', '%s')
	`,
		db.Escape(comment.ID),
		db.Escape(comment.TaskID),
		db.Escape(comment.Author),
		db.Escape(comment.Content),
		db.Escape(comment.Type),
		comment.CreatedAt,
	)

	if err := db.Exec(tb.dbPath, sql); err != nil {
		return TaskComment{}, fmt.Errorf("add comment: %w", err)
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
		SELECT id, task_id, author, content, type, created_at
		FROM task_comments
		WHERE task_id = '%s'
		ORDER BY created_at ASC
		LIMIT 100
	`, db.Escape(taskID))

	rows, err := db.Query(tb.dbPath, sql)
	if err != nil {
		return nil, err
	}

	var comments []TaskComment
	for _, row := range rows {
		cType := fmt.Sprintf("%v", row["type"])
		if cType == "" || cType == "<nil>" {
			cType = "log"
		}
		comments = append(comments, TaskComment{
			ID:        fmt.Sprintf("%v", row["id"]),
			TaskID:    fmt.Sprintf("%v", row["task_id"]),
			Author:    fmt.Sprintf("%v", row["author"]),
			Content:   fmt.Sprintf("%v", row["content"]),
			Type:      cType,
			CreatedAt: fmt.Sprintf("%v", row["created_at"]),
		})
	}
	return comments, nil
}

// AutoRetryFailed moves failed tasks back to "todo" if retry count < maxRetries.
// Uses CAS (Compare-And-Swap) pattern on retry_count to prevent double-increment races.
// Skips tasks flagged as cancelled (not retryable).
func (tb *TaskBoardEngine) AutoRetryFailed() error {
	maxRetries := tb.config.MaxRetriesOrDefault()
	sql := fmt.Sprintf(`
		SELECT id, retry_count FROM tasks WHERE status = 'failed' AND retry_count < %d
	`, maxRetries)

	rows, err := db.Query(tb.dbPath, sql)
	if err != nil {
		return err
	}

	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		currentRetry := int(getFloat64(row, "retry_count"))

		// Skip tasks flagged as cancelled (not retryable).
		comments, _ := tb.GetThread(id)
		cancelled := false
		for _, c := range comments {
			if strings.Contains(c.Content, "[auto-flag] Task was cancelled") {
				cancelled = true
				break
			}
		}
		if cancelled {
			log.Info("auto retry: skipping cancelled task", "id", id)
			continue
		}

		newRetry := currentRetry + 1
		// CAS: only update if retry_count still matches what we read.
		updateSQL := fmt.Sprintf(`
			UPDATE tasks SET status = 'todo', retry_count = %d, updated_at = '%s'
			WHERE id = '%s' AND retry_count = %d
		`, newRetry, time.Now().UTC().Format(time.RFC3339), db.Escape(id), currentRetry)

		if err := db.Exec(tb.dbPath, updateSQL); err != nil {
			log.Warn("auto retry failed task", "id", id, "error", err)
			continue
		}

		// Verify CAS succeeded (sqlite3 CLI doesn't return affected rows).
		verifyRows, _ := db.Query(tb.dbPath, fmt.Sprintf(
			`SELECT retry_count FROM tasks WHERE id = '%s'`, db.Escape(id)))
		if len(verifyRows) > 0 && int(getFloat64(verifyRows[0], "retry_count")) == newRetry {
			log.Info("auto retried failed task", "id", id, "retryCount", newRetry)
		}
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
			select {
			case tb.whSem <- struct{}{}:
				defer func() { <-tb.whSem }()
			default:
				log.Warn("webhook semaphore full, dropping webhook", "url", wh.URL, "event", fullEvent)
				return
			}

			body := map[string]any{
				"event":     fullEvent,
				"data":      payload,
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			}

			bodyJSON, err := json.Marshal(body)
			if err != nil {
				log.Error("webhook body marshal failed", "error", err)
				return
			}

			client := &http.Client{Timeout: 5 * time.Second}
			req, err := http.NewRequest("POST", wh.URL, bytes.NewReader(bodyJSON))
			if err != nil {
				log.Error("webhook request creation failed", "url", wh.URL, "error", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			for k, v := range wh.Headers {
				req.Header.Set(k, v)
			}

			resp, err := client.Do(req)
			if err != nil {
				log.Error("webhook POST failed", "url", wh.URL, "error", err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				log.Warn("webhook POST returned error status", "url", wh.URL, "status", resp.StatusCode)
			}
		}(wh, payload)
	}
}

// parseTaskRow converts a DB row map into a TaskBoard struct.
func parseTaskRow(row map[string]any) TaskBoard {
	dependsOnJSON := fmt.Sprintf("%v", row["depends_on"])
	var dependsOn []string
	if dependsOnJSON != "" && dependsOnJSON != "[]" {
		json.Unmarshal([]byte(dependsOnJSON), &dependsOn)
	}

	parentID := fmt.Sprintf("%v", row["parent_id"])
	if parentID == "<nil>" {
		parentID = ""
	}

	taskType := fmt.Sprintf("%v", row["type"])
	if taskType == "<nil>" || taskType == "" {
		taskType = "feat"
	}

	workflow := fmt.Sprintf("%v", row["workflow"])
	if workflow == "<nil>" {
		workflow = ""
	}

	workflowRunID := fmt.Sprintf("%v", row["workflow_run_id"])
	if workflowRunID == "<nil>" {
		workflowRunID = ""
	}

	return TaskBoard{
		ID:            fmt.Sprintf("%v", row["id"]),
		Project:       fmt.Sprintf("%v", row["project"]),
		Title:         fmt.Sprintf("%v", row["title"]),
		Description:   fmt.Sprintf("%v", row["description"]),
		Status:        fmt.Sprintf("%v", row["status"]),
		Assignee:      fmt.Sprintf("%v", row["assignee"]),
		Priority:      fmt.Sprintf("%v", row["priority"]),
		Model:         fmt.Sprintf("%v", row["model"]),
		ParentID:      parentID,
		DependsOn:     dependsOn,
		Type:          taskType,
		Workflow:      workflow,
		DiscordThread: fmt.Sprintf("%v", row["discord_thread_id"]),
		CreatedAt:     fmt.Sprintf("%v", row["created_at"]),
		UpdatedAt:     fmt.Sprintf("%v", row["updated_at"]),
		CompletedAt:   fmt.Sprintf("%v", row["completed_at"]),
		RetryCount:    int(getFloat64(row, "retry_count")),
		CostUSD:       getFloat64(row, "cost_usd"),
		DurationMs:    int64(getFloat64(row, "duration_ms")),
		SessionID:     fmt.Sprintf("%v", row["session_id"]),
		WorkflowRunID: workflowRunID,
	}
}

// ListChildren returns all tasks with the given parentID.
func (tb *TaskBoardEngine) ListChildren(parentID string) ([]TaskBoard, error) {
	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, type, workflow, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id
		FROM tasks WHERE parent_id = '%s'
		ORDER BY created_at ASC
	`, db.Escape(parentID))

	rows, err := db.Query(tb.dbPath, sql)
	if err != nil {
		return nil, err
	}

	var tasks []TaskBoard
	for _, row := range rows {
		tasks = append(tasks, parseTaskRow(row))
	}
	return tasks, nil
}

// --- Utility Functions ---

// normalizeTaskID adds "task-" prefix if the ID looks like a bare number.
func normalizeTaskID(id string) string {
	if id == "" {
		return id
	}
	if !strings.Contains(id, "-") {
		// Bare number → prepend "task-"
		return "task-" + id
	}
	return id
}

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
	case "parentId":
		return "parent_id"
	case "type":
		return "`type`" // SQLite reserved word — must be quoted
	case "workflowRunId":
		return "workflow_run_id"
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

// hasBlockingDeps returns true if any dependency of the task is not yet done.
func hasBlockingDeps(tb *TaskBoardEngine, t TaskBoard) bool {
	if len(t.DependsOn) == 0 {
		return false
	}
	for _, depID := range t.DependsOn {
		dep, err := tb.GetTask(depID)
		if err != nil || dep.Status != "done" {
			return true
		}
	}
	return false
}

// --- Board View & Project Stats ---

type BoardView struct {
	Columns   map[string][]TaskBoard `json:"columns"`
	Stats     BoardStats             `json:"stats"`
	Projects  []string               `json:"projects"`
	Agents    []string               `json:"agents"`
	Workflows []string               `json:"workflows"`
}

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

// activeStatuses are the kanban statuses shown by default (excluding terminal states).
var activeStatuses = []string{"idea", "needs-thought", "backlog", "todo", "doing", "partial-done", "review"}

// GetBoardView returns all tasks grouped by status column with aggregate stats.
// By default, excludes done/failed statuses unless f.IncludeDone is true.
func (tb *TaskBoardEngine) GetBoardView(f BoardFilter) (*BoardView, error) {
	var whereClauses []string
	for _, pair := range []struct{ col, val string }{
		{"project", f.Project},
		{"assignee", f.Assignee},
		{"priority", f.Priority},
		{"workflow", f.Workflow},
	} {
		if pair.val != "" {
			whereClauses = append(whereClauses, fmt.Sprintf("%s = '%s'", pair.col, db.Escape(pair.val)))
		}
	}

	// By default, only fetch active statuses to reduce payload.
	if !f.IncludeDone {
		placeholders := make([]string, len(activeStatuses))
		for i, s := range activeStatuses {
			placeholders[i] = fmt.Sprintf("'%s'", s)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("status IN (%s)", strings.Join(placeholders, ",")))
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, type, workflow, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id
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

	rows, err := db.Query(tb.dbPath, sql)
	if err != nil {
		return nil, err
	}

	statuses := []string{"idea", "needs-thought", "backlog", "todo", "doing", "partial-done", "review", "done", "failed"}
	columns := make(map[string][]TaskBoard)
	for _, s := range statuses {
		columns[s] = []TaskBoard{}
	}

	byStatus := make(map[string]int)
	projectSet := make(map[string]bool)
	agentSet := make(map[string]bool)
	workflowSet := make(map[string]bool)
	var totalCost float64

	for _, row := range rows {
		t := parseTaskRow(row)

		columns[t.Status] = append(columns[t.Status], t)
		byStatus[t.Status]++
		totalCost += t.CostUSD

		if t.Project != "" {
			projectSet[t.Project] = true
		}
		if t.Assignee != "" {
			agentSet[t.Assignee] = true
		}
		if t.Workflow != "" && t.Workflow != "none" {
			workflowSet[t.Workflow] = true
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
	var workflows []string
	for wf := range workflowSet {
		workflows = append(workflows, wf)
	}

	return &BoardView{
		Columns:   columns,
		Stats:     BoardStats{Total: len(rows), ByStatus: byStatus, TotalCost: totalCost},
		Projects:  projects,
		Agents:    agents,
		Workflows: workflows,
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
	`, db.Escape(projectID))

	rows, err := db.Query(tb.dbPath, sql)
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
		fmt.Println("Usage: tetora task <list|create|show|update|move|assign|comment|thread>")
		fmt.Println("\nCommands:")
		fmt.Println("  list [--status=STATUS] [--assignee=AGENT] [--project=PROJECT]")
		fmt.Println("  create --title=TITLE [--description=DESC] [--priority=PRIORITY] [--assignee=AGENT] [--type=TYPE] [--depends-on=ID]...")
		fmt.Println("  show TASK_ID [--full]")
		fmt.Println("  update TASK_ID [--title=TITLE] [--description=DESC] [--priority=PRIORITY]")
		fmt.Println("  move TASK_ID --status=STATUS")
		fmt.Println("  assign TASK_ID --assignee=AGENT")
		fmt.Println("  comment TASK_ID --author=AUTHOR --content=CONTENT [--type=TYPE]")
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
		var title, description, priority, assignee, taskType string
		var dependsOn []string
		for _, arg := range args {
			if strings.HasPrefix(arg, "--title=") {
				title = strings.TrimPrefix(arg, "--title=")
			} else if strings.HasPrefix(arg, "--description=") {
				description = strings.TrimPrefix(arg, "--description=")
			} else if strings.HasPrefix(arg, "--priority=") {
				priority = strings.TrimPrefix(arg, "--priority=")
			} else if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			} else if strings.HasPrefix(arg, "--type=") {
				taskType = strings.TrimPrefix(arg, "--type=")
			} else if strings.HasPrefix(arg, "--depends-on=") {
				depID := strings.TrimPrefix(arg, "--depends-on=")
				if depID != "" {
					dependsOn = append(dependsOn, depID)
				}
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
			Type:        taskType,
			DependsOn:   dependsOn,
		})
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Created task: %s\n", task.ID)
		fmt.Printf("Title: %s\n", task.Title)
		fmt.Printf("Status: %s\n", task.Status)

	case "show":
		if len(args) < 1 {
			fmt.Println("Usage: tetora task show TASK_ID [--full]")
			os.Exit(1)
		}

		taskID := args[0]
		full := false
		for _, arg := range args[1:] {
			if arg == "--full" {
				full = true
			}
		}

		task, err := tb.GetTask(taskID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			// Suggest similar tasks.
			normalizedID := normalizeTaskID(taskID)
			if candidates := tb.suggestTasks(normalizedID); len(candidates) > 0 {
				fmt.Fprintln(os.Stderr, "Did you mean:")
				for _, c := range candidates {
					fmt.Fprintf(os.Stderr, "  %s  %s  (%s)\n", c.ID, c.Title, c.Status)
				}
			}
			os.Exit(1)
		}

		fmt.Printf("# %s\n\n", task.Title)
		fmt.Printf("- **ID**: %s\n", task.ID)
		fmt.Printf("- **Status**: %s\n", task.Status)
		fmt.Printf("- **Priority**: %s\n", task.Priority)
		fmt.Printf("- **Assignee**: %s\n", task.Assignee)
		fmt.Printf("- **Project**: %s\n", task.Project)
		if task.ParentID != "" {
			fmt.Printf("- **Parent**: %s\n", task.ParentID)
		}
		if len(task.DependsOn) > 0 {
			fmt.Printf("- **Depends On**: %s\n", strings.Join(task.DependsOn, ", "))
		}
		fmt.Printf("- **Created**: %s\n", task.CreatedAt)
		fmt.Printf("- **Updated**: %s\n", task.UpdatedAt)
		if task.CompletedAt != "" {
			fmt.Printf("- **Completed**: %s\n", task.CompletedAt)
		}
		if task.Description != "" {
			fmt.Printf("\n## Description\n\n%s\n", task.Description)
		}

		if full {
			comments, err := tb.GetThread(task.ID)
			if err != nil {
				fmt.Printf("\nError loading comments: %v\n", err)
			} else if len(comments) > 0 {
				fmt.Printf("\n## Comments (%d)\n\n", len(comments))
				for _, c := range comments {
					fmt.Printf("### [%s] %s (type: %s)\n\n%s\n\n", c.CreatedAt, c.Author, c.Type, c.Content)
				}
			}
		}

	case "update":
		if len(args) < 2 {
			fmt.Println("Usage: tetora task update TASK_ID [--title=TITLE] [--description=DESC] [--priority=PRIORITY]")
			os.Exit(1)
		}

		taskID := args[0]
		updates := make(map[string]any)
		var dependsOn []string
		hasDeps := false
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--title=") {
				updates["title"] = strings.TrimPrefix(arg, "--title=")
			} else if strings.HasPrefix(arg, "--description=") {
				updates["description"] = strings.TrimPrefix(arg, "--description=")
			} else if strings.HasPrefix(arg, "--priority=") {
				updates["priority"] = strings.TrimPrefix(arg, "--priority=")
			} else if strings.HasPrefix(arg, "--depends-on=") {
				depID := strings.TrimPrefix(arg, "--depends-on=")
				if depID != "" {
					dependsOn = append(dependsOn, depID)
				}
				hasDeps = true
			}
		}
		if hasDeps {
			updates["dependsOn"] = dependsOn
		}

		if len(updates) == 0 {
			fmt.Println("Error: at least one update field is required")
			os.Exit(1)
		}

		task, err := tb.UpdateTask(taskID, updates)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Updated task %s\n", task.ID)
		fmt.Printf("Title: %s\n", task.Title)
		fmt.Printf("Priority: %s\n", task.Priority)

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
			fmt.Println("Usage: tetora task comment TASK_ID --author=AUTHOR --content=CONTENT [--type=TYPE]")
			os.Exit(1)
		}

		taskID := args[0]
		var author, content, commentType string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--author=") {
				author = strings.TrimPrefix(arg, "--author=")
			} else if strings.HasPrefix(arg, "--content=") {
				content = strings.TrimPrefix(arg, "--content=")
			} else if strings.HasPrefix(arg, "--type=") {
				commentType = strings.TrimPrefix(arg, "--type=")
			}
		}

		if author == "" || content == "" {
			fmt.Println("Error: --author and --content are required")
			os.Exit(1)
		}

		comment, err := tb.AddComment(taskID, author, content, commentType)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Added comment %s (type: %s) to task %s\n", comment.ID, comment.Type, taskID)

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
			fmt.Printf("[%s] %s (type: %s):\n%s\n\n", c.CreatedAt, c.Author, c.Type, c.Content)
		}

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		fmt.Println("Use 'tetora task' to see available commands")
		os.Exit(1)
	}
}
