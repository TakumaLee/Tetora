package taskboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/log"
	"tetora/internal/webhook"
)

// Engine handles the task board database operations.
type Engine struct {
	dbPath   string
	config   config.TaskBoardConfig
	webhooks []config.WebhookConfig
	whSem    chan struct{}
}

// NewEngine creates a new task board engine.
func NewEngine(dbPath string, cfg config.TaskBoardConfig, webhooks []config.WebhookConfig) *Engine {
	return &Engine{
		dbPath:   dbPath,
		config:   cfg,
		webhooks: webhooks,
		whSem:    make(chan struct{}, 8),
	}
}

// DBPath returns the database path used by this engine.
func (tb *Engine) DBPath() string { return tb.dbPath }

// Config returns the taskboard config.
func (tb *Engine) Config() config.TaskBoardConfig { return tb.config }

// InitSchema creates the tasks and task_comments tables if they don't exist.
func (tb *Engine) InitSchema() error {
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
		"ALTER TABLE tasks ADD COLUMN workdirs TEXT DEFAULT '[]';",
		"ALTER TABLE tasks ADD COLUMN execution_count INTEGER DEFAULT 0;",
		"ALTER TABLE tasks ADD COLUMN allow_dangerous INTEGER DEFAULT 0;",
	}
	commentMigrations := []string{
		"ALTER TABLE task_comments ADD COLUMN type TEXT DEFAULT 'log';",
	}
	for _, m := range commentMigrations {
		db.Exec(tb.dbPath, m) // ignore duplicate column errors
	}

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

// ListTasks returns tasks filtered by status and assignee (unpaginated, backward-compatible).
func (tb *Engine) ListTasks(status, assignee, project string) ([]TaskBoard, error) {
	result, err := tb.ListTasksPaginated(status, assignee, project, 1, 100)
	if err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

// ListTasksPaginated returns tasks with pagination support.
func (tb *Engine) ListTasksPaginated(status, assignee, project string, page, limit int) (*TaskListResult, error) {
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
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id, workdirs, execution_count, allow_dangerous
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
func (tb *Engine) CreateTask(task TaskBoard) (TaskBoard, error) {
	if task.ID == "" {
		task.ID = GenerateID("task")
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

	workdirsJSON, _ := json.Marshal(task.Workdirs)
	if task.Workdirs == nil {
		workdirsJSON = []byte("[]")
	}

	if task.Type == "" {
		task.Type = "feat"
	}

	allowDangerousInt := 0
	if task.AllowDangerous {
		allowDangerousInt = 1
	}
	sql := fmt.Sprintf(`
		INSERT INTO tasks (id, project, title, description, status, assignee, priority, model, depends_on, type, workflow, discord_thread_id, created_at, updated_at, retry_count, parent_id, workdirs, allow_dangerous)
		VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', 0, '%s', '%s', %d)
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
		db.Escape(string(workdirsJSON)),
		allowDangerousInt,
	)

	if err := db.Exec(tb.dbPath, sql); err != nil {
		return TaskBoard{}, fmt.Errorf("create task: %w", err)
	}

	// Fire webhook event.
	go tb.fireWebhook("task.created", task)

	return task, nil
}

// UpdateTask updates task fields.
func (tb *Engine) UpdateTask(id string, updates map[string]any) (TaskBoard, error) {
	id = NormalizeTaskID(id)
	var setClauses []string
	for key, val := range updates {
		switch key {
		case "title", "description", "priority", "assignee", "project", "discordThread", "model", "parentId", "workflow", "type", "workflowRunId":
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", toSnakeCase(key), db.Escape(fmt.Sprintf("%v", val))))
		case "dependsOn":
			dependsOnJSON, _ := json.Marshal(val)
			setClauses = append(setClauses, fmt.Sprintf("depends_on = '%s'", db.Escape(string(dependsOnJSON))))
		case "allowDangerous":
			v := 0
			switch b := val.(type) {
			case bool:
				if b {
					v = 1
				}
			case float64:
				if b != 0 {
					v = 1
				}
			}
			setClauses = append(setClauses, fmt.Sprintf("allow_dangerous = %d", v))
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

	return tb.GetTask(id)
}

// DeleteTask removes a task and its comments from the DB.
func (tb *Engine) DeleteTask(id string) error {
	id = NormalizeTaskID(id)
	sql := fmt.Sprintf(`
		DELETE FROM task_comments WHERE task_id = '%s';
		DELETE FROM tasks WHERE id = '%s';
	`, db.Escape(id), db.Escape(id))
	if err := db.Exec(tb.dbPath, sql); err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

// GetTask retrieves a single task by ID.
func (tb *Engine) GetTask(id string) (TaskBoard, error) {
	id = NormalizeTaskID(id)

	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, type, workflow, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id, workdirs, execution_count, allow_dangerous
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

// SuggestTasks returns up to 3 recent tasks whose ID shares a prefix with the given ID.
func (tb *Engine) SuggestTasks(id string) []TaskBoard {
	numeric := strings.TrimPrefix(id, "task-")
	if len(numeric) < 4 {
		return nil
	}
	prefix := "task-" + numeric[:4]

	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, type, workflow, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id, workdirs, execution_count, allow_dangerous
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
func (tb *Engine) MoveTask(id, newStatus string) (TaskBoard, error) {
	id = NormalizeTaskID(id)
	task, err := tb.GetTask(id)
	if err != nil {
		return TaskBoard{}, err
	}

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

	// Quality gate.
	if tb.config.RequireReview && newStatus == "done" && task.Status != "review" {
		return TaskBoard{}, fmt.Errorf("task must pass review before completion")
	}

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

	go tb.fireWebhook("task.moved", task)

	return task, nil
}

// AssignTask assigns a task to an agent.
func (tb *Engine) AssignTask(id, assignee string) (TaskBoard, error) {
	id = NormalizeTaskID(id)
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

	go tb.fireWebhook("task.assigned", task)

	return task, nil
}

// AddComment adds a comment to a task.
func (tb *Engine) AddComment(taskID, author, content string, commentType ...string) (TaskComment, error) {
	taskID = NormalizeTaskID(taskID)
	cType := "log"
	if len(commentType) > 0 && commentType[0] != "" {
		cType = commentType[0]
	}

	comment := TaskComment{
		ID:        GenerateID("comment"),
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

	go tb.fireWebhook("comment.added", map[string]any{
		"taskId":  taskID,
		"comment": comment,
	})

	return comment, nil
}

// GetThread returns all comments for a task.
func (tb *Engine) GetThread(taskID string) ([]TaskComment, error) {
	taskID = NormalizeTaskID(taskID)
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
func (tb *Engine) AutoRetryFailed() error {
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
		updateSQL := fmt.Sprintf(`
			UPDATE tasks SET status = 'todo', retry_count = %d, updated_at = '%s'
			WHERE id = '%s' AND retry_count = %d
		`, newRetry, time.Now().UTC().Format(time.RFC3339), db.Escape(id), currentRetry)

		if err := db.Exec(tb.dbPath, updateSQL); err != nil {
			log.Warn("auto retry failed task", "id", id, "error", err)
			continue
		}

		verifyRows, _ := db.Query(tb.dbPath, fmt.Sprintf(
			`SELECT retry_count FROM tasks WHERE id = '%s'`, db.Escape(id)))
		if len(verifyRows) > 0 && int(getFloat64(verifyRows[0], "retry_count")) == newRetry {
			log.Info("auto retried failed task", "id", id, "retryCount", newRetry)
		}
	}

	return nil
}

// ListChildren returns all tasks with the given parentID.
func (tb *Engine) ListChildren(parentID string) ([]TaskBoard, error) {
	sql := fmt.Sprintf(`
		SELECT id, project, title, description, status, assignee, priority,
		       depends_on, type, workflow, discord_thread_id, created_at, updated_at, completed_at, retry_count,
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id, workdirs, execution_count, allow_dangerous
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

// GetBoardView returns all tasks grouped by status column with aggregate stats.
func (tb *Engine) GetBoardView(f BoardFilter) (*BoardView, error) {
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

	activeStatuses := []string{"idea", "needs-thought", "backlog", "todo", "doing", "partial-done", "review"}
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
		       cost_usd, duration_ms, session_id, model, parent_id, workflow_run_id, workdirs, execution_count, allow_dangerous
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

// GetProjectStats returns task counts and cost for a specific project.
func (tb *Engine) GetProjectStats(projectID string) (*ProjectStats, error) {
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

// HasBlockingDeps returns true if any dependency of the task is not yet done.
func HasBlockingDeps(tb *Engine, t TaskBoard) bool {
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

// fireWebhook sends a webhook notification for task board events.
func (tb *Engine) fireWebhook(event string, payload any) {
	fullEvent := "taskboard." + event
	for _, wh := range tb.webhooks {
		if !webhook.MatchesEvent(webhook.Config{URL: wh.URL, Events: wh.Events, Headers: wh.Headers}, fullEvent) {
			continue
		}

		go func(wh config.WebhookConfig, payload any) {
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

// FireWebhook is an exported wrapper to allow the dispatcher to fire webhooks.
func (tb *Engine) FireWebhook(event string, payload any) {
	tb.fireWebhook(event, payload)
}
