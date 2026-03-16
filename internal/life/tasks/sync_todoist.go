package tasks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TodoistAPIBase is the Todoist REST API v2 base URL (overridable in tests).
var TodoistAPIBase = "https://api.todoist.com/rest/v2"

// TodoistConfig holds Todoist sync configuration.
type TodoistConfig struct {
	APIKey string
}

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
	svc    *Service
	config TodoistConfig
}

// NewTodoistSync creates a new TodoistSync instance.
func NewTodoistSync(svc *Service, cfg TodoistConfig) *TodoistSync {
	return &TodoistSync{
		svc:    svc,
		config: cfg,
	}
}

// PullTasks fetches tasks from Todoist and upserts them locally.
func (ts *TodoistSync) PullTasks(userID string) (int, error) {
	if ts.config.APIKey == "" {
		return 0, fmt.Errorf("todoist API key not configured")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", TodoistAPIBase+"/tasks", nil)
	if err != nil {
		return 0, fmt.Errorf("todoist: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.config.APIKey)

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

	if ts.svc == nil {
		return 0, fmt.Errorf("task manager not initialized")
	}

	pulled := 0
	for _, tt := range tasks {
		existing, _ := ts.svc.FindByExternalID("todoist", tt.ID)
		if existing != nil {
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
			ts.svc.UpdateTask(existing.ID, updates)
		} else {
			dueAt := ""
			if tt.Due != nil {
				dueAt = tt.Due.Date
			}
			task := UserTask{
				UserID:         userID,
				Title:          tt.Content,
				Description:    tt.Description,
				Priority:       TodoistPriorityToLocal(tt.Priority),
				DueAt:          dueAt,
				ExternalID:     tt.ID,
				ExternalSource: "todoist",
				SourceChannel:  "todoist",
			}
			if tt.IsCompleted {
				task.Status = "done"
			}
			_, err := ts.svc.CreateTask(task)
			if err != nil {
				if ts.svc.db.LogWarn != nil {
					ts.svc.db.LogWarn("todoist sync: create task failed", "todoistId", tt.ID, "error", err)
				}
				continue
			}
		}
		pulled++
	}

	if ts.svc.db.LogInfo != nil {
		ts.svc.db.LogInfo("todoist pull complete", "pulled", pulled, "userId", userID)
	}
	return pulled, nil
}

// PushTask pushes a local task to Todoist.
func (ts *TodoistSync) PushTask(task UserTask) error {
	if ts.config.APIKey == "" {
		return fmt.Errorf("todoist API key not configured")
	}

	body := map[string]any{
		"content":     task.Title,
		"description": task.Description,
		"priority":    LocalPriorityToTodoist(task.Priority),
	}
	if task.DueAt != "" {
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
		method = "POST"
		url = fmt.Sprintf("%s/tasks/%s", TodoistAPIBase, task.ExternalID)
	} else {
		method = "POST"
		url = TodoistAPIBase + "/tasks"
	}

	req, err := http.NewRequest(method, url, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return fmt.Errorf("todoist: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.config.APIKey)
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

	if task.ExternalID == "" {
		var created TodoistTask
		if json.NewDecoder(resp.Body).Decode(&created) == nil && created.ID != "" {
			ts.svc.UpdateTask(task.ID, map[string]any{
				"externalId":     created.ID,
				"externalSource": "todoist",
			})
		}
	}

	return nil
}

// SyncAll performs a full bidirectional sync.
func (ts *TodoistSync) SyncAll(userID string) (pulled int, pushed int, err error) {
	pulled, err = ts.PullTasks(userID)
	if err != nil {
		return pulled, 0, fmt.Errorf("todoist sync pull: %w", err)
	}

	if ts.svc == nil {
		return pulled, 0, nil
	}

	tasks, err := ts.svc.ListTasks(userID, TaskFilter{})
	if err != nil {
		return pulled, 0, fmt.Errorf("todoist sync list: %w", err)
	}

	for _, task := range tasks {
		if task.ExternalSource == "todoist" {
			continue
		}
		if task.ExternalID != "" {
			continue
		}
		if err := ts.PushTask(task); err != nil {
			if ts.svc.db.LogWarn != nil {
				ts.svc.db.LogWarn("todoist sync: push task failed", "taskId", task.ID, "error", err)
			}
			continue
		}
		pushed++
	}

	if ts.svc.db.LogInfo != nil {
		ts.svc.db.LogInfo("todoist sync complete", "pulled", pulled, "pushed", pushed, "userId", userID)
	}
	return pulled, pushed, nil
}

// --- Priority Conversion ---

// TodoistPriorityToLocal converts Todoist priority (4=urgent, 1=normal)
// to local priority (1=urgent, 4=low).
func TodoistPriorityToLocal(tp int) int {
	switch tp {
	case 4:
		return 1
	case 3:
		return 2
	case 2:
		return 3
	default:
		return 4
	}
}

// LocalPriorityToTodoist converts local priority (1=urgent, 4=low)
// to Todoist priority (4=urgent, 1=normal).
func LocalPriorityToTodoist(lp int) int {
	switch lp {
	case 1:
		return 4
	case 2:
		return 3
	case 3:
		return 2
	default:
		return 1
	}
}
