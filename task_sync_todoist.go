package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// --- P23.2: Todoist Sync ---

// todoistAPIBase is the Todoist REST API v2 base URL (overridable in tests).
var todoistAPIBase = "https://api.todoist.com/rest/v2"

// TodoistTask represents a task from the Todoist API.
type TodoistTask struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	Description string `json:"description"`
	ProjectID   string `json:"project_id"`
	Priority    int    `json:"priority"` // 1=normal, 4=urgent (Todoist uses inverted scale)
	Due         *struct {
		Date string `json:"date"`
	} `json:"due,omitempty"`
	IsCompleted bool   `json:"is_completed"`
	CreatedAt   string `json:"created_at"`
}

// TodoistSync handles bidirectional sync with Todoist.
type TodoistSync struct {
	cfg    *Config
	dbPath string
}

// newTodoistSync creates a new TodoistSync instance.
func newTodoistSync(cfg *Config) *TodoistSync {
	return &TodoistSync{
		cfg:    cfg,
		dbPath: cfg.HistoryDB,
	}
}

// PullTasks fetches tasks from Todoist and upserts them locally.
func (ts *TodoistSync) PullTasks(userID string) (int, error) {
	apiKey := ts.cfg.TaskManager.Todoist.APIKey
	if apiKey == "" {
		return 0, fmt.Errorf("todoist API key not configured")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", todoistAPIBase+"/tasks", nil)
	if err != nil {
		return 0, fmt.Errorf("todoist: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("todoist: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("todoist API returned %d: %s", resp.StatusCode, string(body))
	}

	var tasks []TodoistTask
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return 0, fmt.Errorf("todoist: decode response: %w", err)
	}

	if globalTaskManager == nil {
		return 0, fmt.Errorf("task manager not initialized")
	}

	pulled := 0
	for _, tt := range tasks {
		// Check if already synced.
		existing, _ := findTaskByExternalID(ts.dbPath, "todoist", tt.ID)
		if existing != nil {
			// Update existing task.
			updates := map[string]any{
				"title":       tt.Content,
				"description": tt.Description,
			}
			if tt.Due != nil && tt.Due.Date != "" {
				updates["dueAt"] = tt.Due.Date
			}
			if tt.IsCompleted {
				updates["status"] = "done"
			}
			globalTaskManager.UpdateTask(existing.ID, updates)
		} else {
			// Create new task.
			dueAt := ""
			if tt.Due != nil {
				dueAt = tt.Due.Date
			}
			task := UserTask{
				UserID:         userID,
				Title:          tt.Content,
				Description:    tt.Description,
				Priority:       todoistPriorityToLocal(tt.Priority),
				DueAt:          dueAt,
				ExternalID:     tt.ID,
				ExternalSource: "todoist",
				SourceChannel:  "todoist",
			}
			if tt.IsCompleted {
				task.Status = "done"
			}
			_, err := globalTaskManager.CreateTask(task)
			if err != nil {
				logWarn("todoist sync: create task failed", "todoistId", tt.ID, "error", err)
				continue
			}
		}
		pulled++
	}

	logInfo("todoist pull complete", "pulled", pulled, "userId", userID)
	return pulled, nil
}

// PushTask pushes a local task to Todoist.
func (ts *TodoistSync) PushTask(task UserTask) error {
	apiKey := ts.cfg.TaskManager.Todoist.APIKey
	if apiKey == "" {
		return fmt.Errorf("todoist API key not configured")
	}

	body := map[string]any{
		"content":     task.Title,
		"description": task.Description,
		"priority":    localPriorityToTodoist(task.Priority),
	}
	if task.DueAt != "" {
		// Todoist expects date in YYYY-MM-DD format.
		dueDate := task.DueAt
		if len(dueDate) > 10 {
			dueDate = dueDate[:10]
		}
		body["due_date"] = dueDate
	}

	bodyJSON, _ := json.Marshal(body)
	client := &http.Client{Timeout: 30 * time.Second}

	var method, url string
	if task.ExternalID != "" && task.ExternalSource == "todoist" {
		// Update existing Todoist task.
		method = "POST"
		url = fmt.Sprintf("%s/tasks/%s", todoistAPIBase, task.ExternalID)
	} else {
		// Create new Todoist task.
		method = "POST"
		url = todoistAPIBase + "/tasks"
	}

	req, err := http.NewRequest(method, url, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return fmt.Errorf("todoist: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("todoist: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("todoist API returned %d: %s", resp.StatusCode, string(respBody))
	}

	// If we created a new task, store the external ID.
	if task.ExternalID == "" {
		var created TodoistTask
		if json.NewDecoder(resp.Body).Decode(&created) == nil && created.ID != "" {
			globalTaskManager.UpdateTask(task.ID, map[string]any{
				"externalId":     created.ID,
				"externalSource": "todoist",
			})
		}
	}

	return nil
}

// SyncAll performs a full bidirectional sync.
func (ts *TodoistSync) SyncAll(userID string) (pulled int, pushed int, err error) {
	// Pull first.
	pulled, err = ts.PullTasks(userID)
	if err != nil {
		return pulled, 0, fmt.Errorf("todoist sync pull: %w", err)
	}

	// Push local tasks that are not yet synced to Todoist.
	if globalTaskManager == nil {
		return pulled, 0, nil
	}

	tasks, err := globalTaskManager.ListTasks(userID, TaskFilter{})
	if err != nil {
		return pulled, 0, fmt.Errorf("todoist sync list: %w", err)
	}

	for _, task := range tasks {
		if task.ExternalSource == "todoist" {
			continue // Already synced from Todoist.
		}
		if task.ExternalID != "" {
			continue // Already has an external link.
		}
		if err := ts.PushTask(task); err != nil {
			logWarn("todoist sync: push task failed", "taskId", task.ID, "error", err)
			continue
		}
		pushed++
	}

	logInfo("todoist sync complete", "pulled", pulled, "pushed", pushed, "userId", userID)
	return pulled, pushed, nil
}

// --- Tool Handler ---

// toolTodoistSync handles the todoist_sync tool.
func toolTodoistSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.TaskManager.Todoist.Enabled {
		return "", fmt.Errorf("todoist sync not enabled")
	}
	var args struct {
		Action string `json:"action"` // pull, push, sync
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	ts := newTodoistSync(cfg)

	switch args.Action {
	case "pull":
		n, err := ts.PullTasks(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Pulled %d tasks from Todoist.", n), nil
	case "push":
		if globalTaskManager == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		tasks, _ := globalTaskManager.ListTasks(args.UserID, TaskFilter{})
		pushed := 0
		for _, task := range tasks {
			if task.ExternalSource == "todoist" || task.ExternalID != "" {
				continue
			}
			if err := ts.PushTask(task); err != nil {
				continue
			}
			pushed++
		}
		return fmt.Sprintf("Pushed %d tasks to Todoist.", pushed), nil
	case "sync", "":
		pulled, pushed, err := ts.SyncAll(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Todoist sync complete: pulled %d, pushed %d.", pulled, pushed), nil
	default:
		return "", fmt.Errorf("unknown action %q (use pull, push, or sync)", args.Action)
	}
}

// --- Helpers ---

// findTaskByExternalID looks up a task by external source and ID.
func findTaskByExternalID(dbPath, source, externalID string) (*UserTask, error) {
	sql := fmt.Sprintf(`SELECT * FROM user_tasks WHERE external_source = '%s' AND external_id = '%s' LIMIT 1;`,
		escapeSQLite(source), escapeSQLite(externalID))
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	task := taskFromRow(rows[0])
	return &task, nil
}

// todoistPriorityToLocal converts Todoist priority (4=urgent, 1=normal)
// to local priority (1=urgent, 4=low).
func todoistPriorityToLocal(tp int) int {
	switch tp {
	case 4:
		return 1 // urgent
	case 3:
		return 2 // high
	case 2:
		return 3 // medium
	default:
		return 4 // low/normal
	}
}

// localPriorityToTodoist converts local priority (1=urgent, 4=low)
// to Todoist priority (4=urgent, 1=normal).
func localPriorityToTodoist(lp int) int {
	switch lp {
	case 1:
		return 4 // urgent
	case 2:
		return 3 // high
	case 3:
		return 2 // medium
	default:
		return 1 // normal
	}
}
