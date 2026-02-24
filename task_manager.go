package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// --- P23.2: Task Management ---

// UserTask represents a personal task for a user.
type UserTask struct {
	ID             string   `json:"id"`
	UserID         string   `json:"userId"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Project        string   `json:"project"`
	Status         string   `json:"status"`   // todo, in_progress, done, cancelled
	Priority       int      `json:"priority"` // 1-4 (1=urgent, 4=low)
	DueAt          string   `json:"dueAt"`
	ParentID       string   `json:"parentId"` // for subtasks
	Tags           []string `json:"tags"`
	SourceChannel  string   `json:"sourceChannel"`
	ExternalID     string   `json:"externalId"`
	ExternalSource string   `json:"externalSource"`
	SortOrder      int      `json:"sortOrder"`
	CreatedAt      string   `json:"createdAt"`
	UpdatedAt      string   `json:"updatedAt"`
	CompletedAt    string   `json:"completedAt"`
}

// TaskProject represents a user-defined project for grouping tasks.
type TaskProject struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt"`
}

// TaskReview is a summary of task activity for a given period.
type TaskReview struct {
	Period      string     `json:"period"` // "daily","weekly"
	Completed   int        `json:"completed"`
	Added       int        `json:"added"`
	Overdue     int        `json:"overdue"`
	InProgress  int        `json:"inProgress"`
	Pending     int        `json:"pending"`
	TopProjects []string   `json:"topProjects"`
	Tasks       []UserTask `json:"tasks,omitempty"`
}

// TaskFilter controls listing and filtering of tasks.
type TaskFilter struct {
	Status   string // filter by status
	Project  string // filter by project
	Priority int    // filter by priority (0 = any)
	DueDate  string // filter by due date (before)
	Tag      string // filter by tag
	Limit    int    // max results
}

// TaskManagerService provides task management operations.
type TaskManagerService struct {
	cfg    *Config
	dbPath string
}

// globalTaskManager is the singleton task manager service.
var globalTaskManager *TaskManagerService

// --- DB Initialization ---

// initTaskManagerDB creates the user_tasks and task_projects tables.
func initTaskManagerDB(dbPath string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS user_tasks (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    project TEXT DEFAULT 'inbox',
    status TEXT DEFAULT 'todo',
    priority INTEGER DEFAULT 2,
    due_at TEXT DEFAULT '',
    parent_id TEXT DEFAULT '',
    tags TEXT DEFAULT '[]',
    source_channel TEXT DEFAULT '',
    external_id TEXT DEFAULT '',
    external_source TEXT DEFAULT '',
    sort_order INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tasks_user ON user_tasks(user_id, status);
CREATE INDEX IF NOT EXISTS idx_tasks_project ON user_tasks(user_id, project, status);
CREATE INDEX IF NOT EXISTS idx_tasks_parent ON user_tasks(parent_id);
CREATE INDEX IF NOT EXISTS idx_tasks_due ON user_tasks(user_id, due_at);

CREATE TABLE IF NOT EXISTS task_projects (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_proj_name ON task_projects(user_id, name);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init task_manager tables: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// newTaskManagerService creates a new TaskManagerService.
func newTaskManagerService(cfg *Config) *TaskManagerService {
	return &TaskManagerService{
		cfg:    cfg,
		dbPath: cfg.HistoryDB,
	}
}

// --- Task CRUD ---

// CreateTask creates a new task and returns it with generated ID and timestamps.
func (svc *TaskManagerService) CreateTask(task UserTask) (*UserTask, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if task.ID == "" {
		task.ID = newUUID()
	}
	if task.Status == "" {
		task.Status = "todo"
	}
	if task.Priority < 1 || task.Priority > 4 {
		task.Priority = 2
	}
	if task.Project == "" {
		task.Project = svc.cfg.TaskManager.defaultProjectOrInbox()
	}
	if task.Tags == nil {
		task.Tags = []string{}
	}
	task.CreatedAt = now
	task.UpdatedAt = now

	tagsJSON, _ := json.Marshal(task.Tags)

	sql := fmt.Sprintf(`INSERT INTO user_tasks (id, user_id, title, description, project, status, priority, due_at, parent_id, tags, source_channel, external_id, external_source, sort_order, created_at, updated_at, completed_at)
VALUES ('%s','%s','%s','%s','%s','%s',%d,'%s','%s','%s','%s','%s','%s',%d,'%s','%s','%s');`,
		escapeSQLite(task.ID),
		escapeSQLite(task.UserID),
		escapeSQLite(task.Title),
		escapeSQLite(task.Description),
		escapeSQLite(task.Project),
		escapeSQLite(task.Status),
		task.Priority,
		escapeSQLite(task.DueAt),
		escapeSQLite(task.ParentID),
		escapeSQLite(string(tagsJSON)),
		escapeSQLite(task.SourceChannel),
		escapeSQLite(task.ExternalID),
		escapeSQLite(task.ExternalSource),
		task.SortOrder,
		escapeSQLite(task.CreatedAt),
		escapeSQLite(task.UpdatedAt),
		escapeSQLite(task.CompletedAt),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create task: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return &task, nil
}

// GetTask retrieves a single task by ID.
func (svc *TaskManagerService) GetTask(taskID string) (*UserTask, error) {
	sql := fmt.Sprintf(`SELECT * FROM user_tasks WHERE id = '%s';`, escapeSQLite(taskID))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	task := taskFromRow(rows[0])
	return &task, nil
}

// UpdateTask updates specific fields of a task.
func (svc *TaskManagerService) UpdateTask(taskID string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}

	var setClauses []string
	for key, val := range updates {
		col := taskFieldToColumn(key)
		if col == "" {
			continue
		}
		switch v := val.(type) {
		case string:
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, escapeSQLite(v)))
		case float64:
			setClauses = append(setClauses, fmt.Sprintf("%s = %d", col, int(v)))
		case int:
			setClauses = append(setClauses, fmt.Sprintf("%s = %d", col, v))
		case []string:
			j, _ := json.Marshal(v)
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, escapeSQLite(string(j))))
		case []any:
			j, _ := json.Marshal(v)
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, escapeSQLite(string(j))))
		default:
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, escapeSQLite(fmt.Sprintf("%v", v))))
		}
	}
	if len(setClauses) == 0 {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	setClauses = append(setClauses, fmt.Sprintf("updated_at = '%s'", escapeSQLite(now)))

	sql := fmt.Sprintf(`UPDATE user_tasks SET %s WHERE id = '%s';`,
		strings.Join(setClauses, ", "), escapeSQLite(taskID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("update task: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// CompleteTask marks a task and all its incomplete subtasks as done.
func (svc *TaskManagerService) CompleteTask(taskID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Complete the task itself.
	sql := fmt.Sprintf(`UPDATE user_tasks SET status = 'done', completed_at = '%s', updated_at = '%s' WHERE id = '%s';`,
		escapeSQLite(now), escapeSQLite(now), escapeSQLite(taskID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("complete task: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Complete all incomplete subtasks recursively.
	if err := svc.completeSubtasks(taskID, now); err != nil {
		return fmt.Errorf("complete subtasks: %w", err)
	}
	return nil
}

// completeSubtasks recursively completes all subtasks of a parent.
func (svc *TaskManagerService) completeSubtasks(parentID, now string) error {
	// Find all incomplete subtasks.
	sql := fmt.Sprintf(`SELECT id FROM user_tasks WHERE parent_id = '%s' AND status != 'done' AND status != 'cancelled';`,
		escapeSQLite(parentID))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return err
	}
	for _, row := range rows {
		childID := jsonStr(row["id"])
		if childID == "" {
			continue
		}
		// Complete this child.
		upd := fmt.Sprintf(`UPDATE user_tasks SET status = 'done', completed_at = '%s', updated_at = '%s' WHERE id = '%s';`,
			escapeSQLite(now), escapeSQLite(now), escapeSQLite(childID))
		cmd := exec.Command("sqlite3", svc.dbPath, upd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("complete subtask %s: %s: %w", childID, strings.TrimSpace(string(out)), err)
		}
		// Recurse into children of this child.
		if err := svc.completeSubtasks(childID, now); err != nil {
			return err
		}
	}
	return nil
}

// DeleteTask removes a task by ID.
func (svc *TaskManagerService) DeleteTask(taskID string) error {
	sql := fmt.Sprintf(`DELETE FROM user_tasks WHERE id = '%s';`, escapeSQLite(taskID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete task: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// --- Listing/Filtering ---

// ListTasks returns tasks for a user matching the given filters.
func (svc *TaskManagerService) ListTasks(userID string, filters TaskFilter) ([]UserTask, error) {
	conditions := []string{fmt.Sprintf("user_id = '%s'", escapeSQLite(userID))}

	if filters.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = '%s'", escapeSQLite(filters.Status)))
	}
	if filters.Project != "" {
		conditions = append(conditions, fmt.Sprintf("project = '%s'", escapeSQLite(filters.Project)))
	}
	if filters.Priority > 0 {
		conditions = append(conditions, fmt.Sprintf("priority = %d", filters.Priority))
	}
	if filters.DueDate != "" {
		conditions = append(conditions, fmt.Sprintf("due_at != '' AND due_at <= '%s'", escapeSQLite(filters.DueDate)))
	}
	if filters.Tag != "" {
		// Tags stored as JSON array; use LIKE for substring matching.
		conditions = append(conditions, fmt.Sprintf("tags LIKE '%%%s%%'", escapeSQLite(filters.Tag)))
	}

	limit := filters.Limit
	if limit <= 0 {
		limit = 50
	}

	sql := fmt.Sprintf(`SELECT * FROM user_tasks WHERE %s AND parent_id = '' ORDER BY priority ASC, sort_order ASC, created_at DESC LIMIT %d;`,
		strings.Join(conditions, " AND "), limit)
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	tasks := make([]UserTask, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, taskFromRow(row))
	}
	return tasks, nil
}

// GetSubtasks returns all subtasks of a parent task.
func (svc *TaskManagerService) GetSubtasks(parentID string) ([]UserTask, error) {
	sql := fmt.Sprintf(`SELECT * FROM user_tasks WHERE parent_id = '%s' ORDER BY sort_order ASC, created_at ASC;`,
		escapeSQLite(parentID))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get subtasks: %w", err)
	}

	tasks := make([]UserTask, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, taskFromRow(row))
	}
	return tasks, nil
}

// --- Projects ---

// CreateProject creates a new task project.
func (svc *TaskManagerService) CreateProject(userID, name, description string) (*TaskProject, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	id := newUUID()

	sql := fmt.Sprintf(`INSERT INTO task_projects (id, user_id, name, description, created_at)
VALUES ('%s','%s','%s','%s','%s');`,
		escapeSQLite(id),
		escapeSQLite(userID),
		escapeSQLite(name),
		escapeSQLite(description),
		escapeSQLite(now),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create project: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return &TaskProject{
		ID:          id,
		UserID:      userID,
		Name:        name,
		Description: description,
		CreatedAt:   now,
	}, nil
}

// ListProjects returns all projects for a user.
func (svc *TaskManagerService) ListProjects(userID string) ([]TaskProject, error) {
	sql := fmt.Sprintf(`SELECT * FROM task_projects WHERE user_id = '%s' ORDER BY name ASC;`,
		escapeSQLite(userID))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	projects := make([]TaskProject, 0, len(rows))
	for _, row := range rows {
		projects = append(projects, TaskProject{
			ID:          jsonStr(row["id"]),
			UserID:      jsonStr(row["user_id"]),
			Name:        jsonStr(row["name"]),
			Description: jsonStr(row["description"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return projects, nil
}

// --- Review ---

// GenerateReview generates a task activity summary for the given period.
func (svc *TaskManagerService) GenerateReview(userID, period string) (*TaskReview, error) {
	var since string
	switch period {
	case "weekly":
		since = time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	default:
		period = "daily"
		since = time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	}

	review := &TaskReview{Period: period}

	// Count completed tasks in period.
	sql := fmt.Sprintf(`SELECT COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND status = 'done' AND completed_at >= '%s';`,
		escapeSQLite(userID), escapeSQLite(since))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review completed: %w", err)
	}
	if len(rows) > 0 {
		review.Completed = jsonInt(rows[0]["cnt"])
	}

	// Count added tasks in period.
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND created_at >= '%s';`,
		escapeSQLite(userID), escapeSQLite(since))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review added: %w", err)
	}
	if len(rows) > 0 {
		review.Added = jsonInt(rows[0]["cnt"])
	}

	// Count overdue tasks (due_at < now AND status not done/cancelled).
	now := time.Now().UTC().Format(time.RFC3339)
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND due_at != '' AND due_at < '%s' AND status NOT IN ('done','cancelled');`,
		escapeSQLite(userID), escapeSQLite(now))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review overdue: %w", err)
	}
	if len(rows) > 0 {
		review.Overdue = jsonInt(rows[0]["cnt"])
	}

	// Count in_progress tasks.
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND status = 'in_progress';`,
		escapeSQLite(userID))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review in_progress: %w", err)
	}
	if len(rows) > 0 {
		review.InProgress = jsonInt(rows[0]["cnt"])
	}

	// Count pending (todo) tasks.
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND status = 'todo';`,
		escapeSQLite(userID))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review pending: %w", err)
	}
	if len(rows) > 0 {
		review.Pending = jsonInt(rows[0]["cnt"])
	}

	// Top 3 projects by task count.
	sql = fmt.Sprintf(`SELECT project, COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND status NOT IN ('cancelled') GROUP BY project ORDER BY cnt DESC LIMIT 3;`,
		escapeSQLite(userID))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review projects: %w", err)
	}
	for _, row := range rows {
		p := jsonStr(row["project"])
		if p != "" {
			review.TopProjects = append(review.TopProjects, p)
		}
	}
	if review.TopProjects == nil {
		review.TopProjects = []string{}
	}

	// Include recently completed tasks in the period.
	sql = fmt.Sprintf(`SELECT * FROM user_tasks WHERE user_id = '%s' AND status = 'done' AND completed_at >= '%s' ORDER BY completed_at DESC LIMIT 10;`,
		escapeSQLite(userID), escapeSQLite(since))
	rows, err = queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review tasks: %w", err)
	}
	for _, row := range rows {
		review.Tasks = append(review.Tasks, taskFromRow(row))
	}

	return review, nil
}

// --- NL Task Decomposition ---

// DecomposeTask splits a complex task into subtasks.
func (svc *TaskManagerService) DecomposeTask(taskID string, subtitles []string) ([]UserTask, error) {
	parent, err := svc.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("decompose: parent: %w", err)
	}

	subtasks := make([]UserTask, 0, len(subtitles))
	for i, title := range subtitles {
		sub := UserTask{
			UserID:        parent.UserID,
			Title:         title,
			Project:       parent.Project,
			Status:        "todo",
			Priority:      parent.Priority,
			ParentID:      parent.ID,
			Tags:          parent.Tags,
			SourceChannel: parent.SourceChannel,
			SortOrder:     i + 1,
		}
		created, err := svc.CreateTask(sub)
		if err != nil {
			return nil, fmt.Errorf("decompose: create subtask %d: %w", i, err)
		}
		subtasks = append(subtasks, *created)
	}

	// Mark parent as in_progress if it was todo.
	if parent.Status == "todo" {
		svc.UpdateTask(taskID, map[string]any{"status": "in_progress"})
	}

	return subtasks, nil
}

// --- Tool Handlers ---

// toolTaskCreate handles the task_create tool.
func toolTaskCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalTaskManager == nil {
		return "", fmt.Errorf("task manager not initialized (enable taskManager in config)")
	}
	var args struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Project     string   `json:"project"`
		Priority    int      `json:"priority"`
		DueAt       string   `json:"dueAt"`
		Tags        []string `json:"tags"`
		UserID      string   `json:"userId"`
		Decompose   bool     `json:"decompose"`
		Subtasks    []string `json:"subtasks"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Title == "" {
		return "", fmt.Errorf("title is required")
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	task := UserTask{
		UserID:      args.UserID,
		Title:       args.Title,
		Description: args.Description,
		Project:     args.Project,
		Priority:    args.Priority,
		DueAt:       args.DueAt,
		Tags:        args.Tags,
	}

	created, err := globalTaskManager.CreateTask(task)
	if err != nil {
		return "", err
	}

	// Auto-decompose if requested.
	if args.Decompose && len(args.Subtasks) > 0 {
		subs, err := globalTaskManager.DecomposeTask(created.ID, args.Subtasks)
		if err != nil {
			return "", fmt.Errorf("task created but decomposition failed: %w", err)
		}
		result := map[string]any{
			"task":     created,
			"subtasks": subs,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil
	}

	out, _ := json.MarshalIndent(created, "", "  ")
	return string(out), nil
}

// toolTaskList handles the task_list tool.
func toolTaskList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalTaskManager == nil {
		return "", fmt.Errorf("task manager not initialized (enable taskManager in config)")
	}
	var args struct {
		Status   string `json:"status"`
		Project  string `json:"project"`
		Priority int    `json:"priority"`
		DueDate  string `json:"dueDate"`
		Tag      string `json:"tag"`
		Limit    int    `json:"limit"`
		UserID   string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	filters := TaskFilter{
		Status:   args.Status,
		Project:  args.Project,
		Priority: args.Priority,
		DueDate:  args.DueDate,
		Tag:      args.Tag,
		Limit:    args.Limit,
	}

	tasks, err := globalTaskManager.ListTasks(args.UserID, filters)
	if err != nil {
		return "", err
	}

	// Include subtask counts.
	type taskWithSubs struct {
		UserTask
		SubtaskCount int `json:"subtaskCount"`
	}
	results := make([]taskWithSubs, 0, len(tasks))
	for _, t := range tasks {
		subs, _ := globalTaskManager.GetSubtasks(t.ID)
		results = append(results, taskWithSubs{UserTask: t, SubtaskCount: len(subs)})
	}

	out, _ := json.MarshalIndent(results, "", "  ")
	return string(out), nil
}

// toolTaskComplete handles the task_complete tool.
func toolTaskComplete(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalTaskManager == nil {
		return "", fmt.Errorf("task manager not initialized (enable taskManager in config)")
	}
	var args struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.TaskID == "" {
		return "", fmt.Errorf("taskId is required")
	}

	if err := globalTaskManager.CompleteTask(args.TaskID); err != nil {
		return "", err
	}

	task, _ := globalTaskManager.GetTask(args.TaskID)
	if task != nil {
		out, _ := json.MarshalIndent(task, "", "  ")
		return fmt.Sprintf("Task completed.\n%s", string(out)), nil
	}
	return "Task completed.", nil
}

// toolTaskReview handles the task_review tool.
func toolTaskReview(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalTaskManager == nil {
		return "", fmt.Errorf("task manager not initialized (enable taskManager in config)")
	}
	var args struct {
		Period string `json:"period"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}
	if args.Period == "" {
		args.Period = "daily"
	}

	review, err := globalTaskManager.GenerateReview(args.UserID, args.Period)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(review, "", "  ")
	return string(out), nil
}

// --- Helpers ---

// taskFromRow converts a queryDB row to a UserTask.
func taskFromRow(row map[string]any) UserTask {
	t := UserTask{
		ID:             jsonStr(row["id"]),
		UserID:         jsonStr(row["user_id"]),
		Title:          jsonStr(row["title"]),
		Description:    jsonStr(row["description"]),
		Project:        jsonStr(row["project"]),
		Status:         jsonStr(row["status"]),
		Priority:       jsonInt(row["priority"]),
		DueAt:          jsonStr(row["due_at"]),
		ParentID:       jsonStr(row["parent_id"]),
		SourceChannel:  jsonStr(row["source_channel"]),
		ExternalID:     jsonStr(row["external_id"]),
		ExternalSource: jsonStr(row["external_source"]),
		SortOrder:      jsonInt(row["sort_order"]),
		CreatedAt:      jsonStr(row["created_at"]),
		UpdatedAt:      jsonStr(row["updated_at"]),
		CompletedAt:    jsonStr(row["completed_at"]),
	}

	// Parse tags from JSON string.
	tagsStr := jsonStr(row["tags"])
	if tagsStr != "" {
		var tags []string
		if json.Unmarshal([]byte(tagsStr), &tags) == nil {
			t.Tags = tags
		}
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	return t
}

// taskFieldToColumn maps JSON field names to DB column names.
func taskFieldToColumn(field string) string {
	switch field {
	case "title":
		return "title"
	case "description":
		return "description"
	case "project":
		return "project"
	case "status":
		return "status"
	case "priority":
		return "priority"
	case "dueAt":
		return "due_at"
	case "parentId":
		return "parent_id"
	case "tags":
		return "tags"
	case "sourceChannel":
		return "source_channel"
	case "externalId":
		return "external_id"
	case "externalSource":
		return "external_source"
	case "sortOrder":
		return "sort_order"
	default:
		return ""
	}
}
