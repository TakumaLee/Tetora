// Package tasks implements personal task management with projects,
// subtasks, reviews, and external sync (Notion, Todoist).
package tasks

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"tetora/internal/life/lifedb"
)

// --- Types ---

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

// Config holds task manager configuration.
type Config struct {
	DefaultProject string
}

// UUIDFn generates a new UUID string.
type UUIDFn func() string

// --- Service ---

// Service provides task management operations.
type Service struct {
	dbPath         string
	defaultProject string
	db             lifedb.DB
	uuidFn         UUIDFn
}

// New creates a new task management Service.
func New(dbPath string, cfg Config, db lifedb.DB, uuidFn UUIDFn) *Service {
	dp := cfg.DefaultProject
	if dp == "" {
		dp = "inbox"
	}
	return &Service{
		dbPath:         dbPath,
		defaultProject: dp,
		db:             db,
		uuidFn:         uuidFn,
	}
}

// DBPath returns the database path used by this service.
func (svc *Service) DBPath() string {
	return svc.dbPath
}

// --- DB Initialization ---

// InitDB creates the user_tasks and task_projects tables.
func InitDB(dbPath string) error {
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

// --- Task CRUD ---

// CreateTask creates a new task and returns it with generated ID and timestamps.
func (svc *Service) CreateTask(task UserTask) (*UserTask, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if task.ID == "" {
		task.ID = svc.uuidFn()
	}
	if task.Status == "" {
		task.Status = "todo"
	}
	if task.Priority < 1 || task.Priority > 4 {
		task.Priority = 2
	}
	if task.Project == "" {
		task.Project = svc.defaultProject
	}
	if task.Tags == nil {
		task.Tags = []string{}
	}
	task.CreatedAt = now
	task.UpdatedAt = now

	tagsJSON, _ := json.Marshal(task.Tags)

	sql := fmt.Sprintf(`INSERT INTO user_tasks (id, user_id, title, description, project, status, priority, due_at, parent_id, tags, source_channel, external_id, external_source, sort_order, created_at, updated_at, completed_at)
VALUES ('%s','%s','%s','%s','%s','%s',%d,'%s','%s','%s','%s','%s','%s',%d,'%s','%s','%s');`,
		svc.db.Escape(task.ID),
		svc.db.Escape(task.UserID),
		svc.db.Escape(task.Title),
		svc.db.Escape(task.Description),
		svc.db.Escape(task.Project),
		svc.db.Escape(task.Status),
		task.Priority,
		svc.db.Escape(task.DueAt),
		svc.db.Escape(task.ParentID),
		svc.db.Escape(string(tagsJSON)),
		svc.db.Escape(task.SourceChannel),
		svc.db.Escape(task.ExternalID),
		svc.db.Escape(task.ExternalSource),
		task.SortOrder,
		svc.db.Escape(task.CreatedAt),
		svc.db.Escape(task.UpdatedAt),
		svc.db.Escape(task.CompletedAt),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create task: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return &task, nil
}

// GetTask retrieves a single task by ID.
func (svc *Service) GetTask(taskID string) (*UserTask, error) {
	sql := fmt.Sprintf(`SELECT * FROM user_tasks WHERE id = '%s';`, svc.db.Escape(taskID))
	rows, err := svc.db.Query(svc.dbPath, sql)
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
func (svc *Service) UpdateTask(taskID string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}

	var setClauses []string
	for key, val := range updates {
		col := TaskFieldToColumn(key)
		if col == "" {
			continue
		}
		switch v := val.(type) {
		case string:
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, svc.db.Escape(v)))
		case float64:
			setClauses = append(setClauses, fmt.Sprintf("%s = %d", col, int(v)))
		case int:
			setClauses = append(setClauses, fmt.Sprintf("%s = %d", col, v))
		case []string:
			j, _ := json.Marshal(v)
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, svc.db.Escape(string(j))))
		case []any:
			j, _ := json.Marshal(v)
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, svc.db.Escape(string(j))))
		default:
			setClauses = append(setClauses, fmt.Sprintf("%s = '%s'", col, svc.db.Escape(fmt.Sprintf("%v", v))))
		}
	}
	if len(setClauses) == 0 {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	setClauses = append(setClauses, fmt.Sprintf("updated_at = '%s'", svc.db.Escape(now)))

	sql := fmt.Sprintf(`UPDATE user_tasks SET %s WHERE id = '%s';`,
		strings.Join(setClauses, ", "), svc.db.Escape(taskID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("update task: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// CompleteTask marks a task and all its incomplete subtasks as done.
func (svc *Service) CompleteTask(taskID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(`UPDATE user_tasks SET status = 'done', completed_at = '%s', updated_at = '%s' WHERE id = '%s';`,
		svc.db.Escape(now), svc.db.Escape(now), svc.db.Escape(taskID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("complete task: %s: %w", strings.TrimSpace(string(out)), err)
	}

	if err := svc.completeSubtasks(taskID, now); err != nil {
		return fmt.Errorf("complete subtasks: %w", err)
	}
	return nil
}

func (svc *Service) completeSubtasks(parentID, now string) error {
	sql := fmt.Sprintf(`SELECT id FROM user_tasks WHERE parent_id = '%s' AND status != 'done' AND status != 'cancelled';`,
		svc.db.Escape(parentID))
	rows, err := svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return err
	}
	for _, row := range rows {
		childID := jsonStr(row["id"])
		if childID == "" {
			continue
		}
		upd := fmt.Sprintf(`UPDATE user_tasks SET status = 'done', completed_at = '%s', updated_at = '%s' WHERE id = '%s';`,
			svc.db.Escape(now), svc.db.Escape(now), svc.db.Escape(childID))
		cmd := exec.Command("sqlite3", svc.dbPath, upd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("complete subtask %s: %s: %w", childID, strings.TrimSpace(string(out)), err)
		}
		if err := svc.completeSubtasks(childID, now); err != nil {
			return err
		}
	}
	return nil
}

// DeleteTask removes a task by ID.
func (svc *Service) DeleteTask(taskID string) error {
	sql := fmt.Sprintf(`DELETE FROM user_tasks WHERE id = '%s';`, svc.db.Escape(taskID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete task: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// --- Listing/Filtering ---

// ListTasks returns tasks for a user matching the given filters.
func (svc *Service) ListTasks(userID string, filters TaskFilter) ([]UserTask, error) {
	conditions := []string{fmt.Sprintf("user_id = '%s'", svc.db.Escape(userID))}

	if filters.Status != "" {
		conditions = append(conditions, fmt.Sprintf("status = '%s'", svc.db.Escape(filters.Status)))
	}
	if filters.Project != "" {
		conditions = append(conditions, fmt.Sprintf("project = '%s'", svc.db.Escape(filters.Project)))
	}
	if filters.Priority > 0 {
		conditions = append(conditions, fmt.Sprintf("priority = %d", filters.Priority))
	}
	if filters.DueDate != "" {
		conditions = append(conditions, fmt.Sprintf("due_at != '' AND due_at <= '%s'", svc.db.Escape(filters.DueDate)))
	}
	if filters.Tag != "" {
		conditions = append(conditions, fmt.Sprintf("tags LIKE '%%%s%%'", svc.db.Escape(filters.Tag)))
	}

	limit := filters.Limit
	if limit <= 0 {
		limit = 50
	}

	sql := fmt.Sprintf(`SELECT * FROM user_tasks WHERE %s AND parent_id = '' ORDER BY priority ASC, sort_order ASC, created_at DESC LIMIT %d;`,
		strings.Join(conditions, " AND "), limit)
	rows, err := svc.db.Query(svc.dbPath, sql)
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
func (svc *Service) GetSubtasks(parentID string) ([]UserTask, error) {
	sql := fmt.Sprintf(`SELECT * FROM user_tasks WHERE parent_id = '%s' ORDER BY sort_order ASC, created_at ASC;`,
		svc.db.Escape(parentID))
	rows, err := svc.db.Query(svc.dbPath, sql)
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
func (svc *Service) CreateProject(userID, name, description string) (*TaskProject, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	id := svc.uuidFn()

	sql := fmt.Sprintf(`INSERT INTO task_projects (id, user_id, name, description, created_at)
VALUES ('%s','%s','%s','%s','%s');`,
		svc.db.Escape(id),
		svc.db.Escape(userID),
		svc.db.Escape(name),
		svc.db.Escape(description),
		svc.db.Escape(now),
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
func (svc *Service) ListProjects(userID string) ([]TaskProject, error) {
	sql := fmt.Sprintf(`SELECT * FROM task_projects WHERE user_id = '%s' ORDER BY name ASC;`,
		svc.db.Escape(userID))
	rows, err := svc.db.Query(svc.dbPath, sql)
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
func (svc *Service) GenerateReview(userID, period string) (*TaskReview, error) {
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
		svc.db.Escape(userID), svc.db.Escape(since))
	rows, err := svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review completed: %w", err)
	}
	if len(rows) > 0 {
		review.Completed = jsonInt(rows[0]["cnt"])
	}

	// Count added tasks in period.
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND created_at >= '%s';`,
		svc.db.Escape(userID), svc.db.Escape(since))
	rows, err = svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review added: %w", err)
	}
	if len(rows) > 0 {
		review.Added = jsonInt(rows[0]["cnt"])
	}

	// Count overdue tasks.
	now := time.Now().UTC().Format(time.RFC3339)
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND due_at != '' AND due_at < '%s' AND status NOT IN ('done','cancelled');`,
		svc.db.Escape(userID), svc.db.Escape(now))
	rows, err = svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review overdue: %w", err)
	}
	if len(rows) > 0 {
		review.Overdue = jsonInt(rows[0]["cnt"])
	}

	// Count in_progress tasks.
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND status = 'in_progress';`,
		svc.db.Escape(userID))
	rows, err = svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review in_progress: %w", err)
	}
	if len(rows) > 0 {
		review.InProgress = jsonInt(rows[0]["cnt"])
	}

	// Count pending (todo) tasks.
	sql = fmt.Sprintf(`SELECT COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND status = 'todo';`,
		svc.db.Escape(userID))
	rows, err = svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("review pending: %w", err)
	}
	if len(rows) > 0 {
		review.Pending = jsonInt(rows[0]["cnt"])
	}

	// Top 3 projects by task count.
	sql = fmt.Sprintf(`SELECT project, COUNT(*) as cnt FROM user_tasks WHERE user_id = '%s' AND status NOT IN ('cancelled') GROUP BY project ORDER BY cnt DESC LIMIT 3;`,
		svc.db.Escape(userID))
	rows, err = svc.db.Query(svc.dbPath, sql)
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
		svc.db.Escape(userID), svc.db.Escape(since))
	rows, err = svc.db.Query(svc.dbPath, sql)
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
func (svc *Service) DecomposeTask(taskID string, subtitles []string) ([]UserTask, error) {
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

// --- External ID Lookup ---

// FindByExternalID looks up a task by external source and ID.
func (svc *Service) FindByExternalID(source, externalID string) (*UserTask, error) {
	sql := fmt.Sprintf(`SELECT * FROM user_tasks WHERE external_source = '%s' AND external_id = '%s' LIMIT 1;`,
		svc.db.Escape(source), svc.db.Escape(externalID))
	rows, err := svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	task := taskFromRow(rows[0])
	return &task, nil
}

// --- Helpers ---

// TaskFieldToColumn maps JSON field names to DB column names.
func TaskFieldToColumn(field string) string {
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

// TaskFromRow converts a queryDB row to a UserTask.
func TaskFromRow(row map[string]any) UserTask {
	return taskFromRow(row)
}

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

// --- JSON helpers (package-private, same as root) ---

func jsonStr(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

func jsonInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case string:
		var n int
		fmt.Sscanf(val, "%d", &n)
		return n
	default:
		return 0
	}
}
