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

// --- P23.2: Notion Sync ---

// notionAPIBase is the Notion API base URL (overridable in tests).
var notionAPIBase = "https://api.notion.com/v1"

// notionAPIVersion is the Notion API version header value.
const notionAPIVersion = "2022-06-28"

// NotionSync handles bidirectional sync with Notion databases.
type NotionSync struct {
	cfg    *Config
	dbPath string
}

// notionPage represents a Notion database page (task).
type notionPage struct {
	ID         string `json:"id"`
	Properties struct {
		Name struct {
			Title []struct {
				PlainText string `json:"plain_text"`
			} `json:"title"`
		} `json:"Name"`
		Status struct {
			Select *struct {
				Name string `json:"name"`
			} `json:"select"`
		} `json:"Status"`
		Priority struct {
			Select *struct {
				Name string `json:"name"`
			} `json:"select"`
		} `json:"Priority"`
		DueDate struct {
			Date *struct {
				Start string `json:"start"`
			} `json:"date"`
		} `json:"Due Date"`
	} `json:"properties"`
	CreatedTime string `json:"created_time"`
}

// newNotionSync creates a new NotionSync instance.
func newNotionSync(cfg *Config) *NotionSync {
	return &NotionSync{
		cfg:    cfg,
		dbPath: cfg.HistoryDB,
	}
}

// PullTasks fetches tasks from a Notion database and upserts locally.
func (ns *NotionSync) PullTasks(userID string) (int, error) {
	apiKey := ns.cfg.TaskManager.Notion.APIKey
	if apiKey == "" {
		return 0, fmt.Errorf("notion API key not configured")
	}
	dbID := ns.cfg.TaskManager.Notion.DatabaseID
	if dbID == "" {
		return 0, fmt.Errorf("notion database ID not configured")
	}

	client := &http.Client{Timeout: 30 * time.Second}

	// Query the Notion database.
	body := `{"page_size": 100}`
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/databases/%s/query", notionAPIBase, dbID),
		strings.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("notion: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Notion-Version", notionAPIVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("notion: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("notion API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Results []notionPage `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("notion: decode response: %w", err)
	}

	if globalTaskManager == nil {
		return 0, fmt.Errorf("task manager not initialized")
	}

	pulled := 0
	for _, page := range result.Results {
		title := ""
		if len(page.Properties.Name.Title) > 0 {
			title = page.Properties.Name.Title[0].PlainText
		}
		if title == "" {
			continue
		}

		status := notionStatusToLocal(page)
		priority := notionPriorityToLocal(page)
		dueAt := ""
		if page.Properties.DueDate.Date != nil {
			dueAt = page.Properties.DueDate.Date.Start
		}

		existing, _ := findTaskByExternalID(ns.dbPath, "notion", page.ID)
		if existing != nil {
			updates := map[string]any{
				"title":    title,
				"status":   status,
				"priority": priority,
			}
			if dueAt != "" {
				updates["dueAt"] = dueAt
			}
			globalTaskManager.UpdateTask(existing.ID, updates)
		} else {
			task := UserTask{
				UserID:         userID,
				Title:          title,
				Status:         status,
				Priority:       priority,
				DueAt:          dueAt,
				ExternalID:     page.ID,
				ExternalSource: "notion",
				SourceChannel:  "notion",
			}
			if _, err := globalTaskManager.CreateTask(task); err != nil {
				logWarn("notion sync: create task failed", "notionId", page.ID, "error", err)
				continue
			}
		}
		pulled++
	}

	logInfo("notion pull complete", "pulled", pulled, "userId", userID)
	return pulled, nil
}

// PushTask pushes a local task to Notion.
func (ns *NotionSync) PushTask(task UserTask) error {
	apiKey := ns.cfg.TaskManager.Notion.APIKey
	if apiKey == "" {
		return fmt.Errorf("notion API key not configured")
	}
	dbID := ns.cfg.TaskManager.Notion.DatabaseID
	if dbID == "" {
		return fmt.Errorf("notion database ID not configured")
	}

	// Build Notion page properties.
	props := map[string]any{
		"Name": map[string]any{
			"title": []map[string]any{
				{"text": map[string]any{"content": task.Title}},
			},
		},
	}

	// Add status if available.
	notionStatus := localStatusToNotion(task.Status)
	if notionStatus != "" {
		props["Status"] = map[string]any{
			"select": map[string]any{"name": notionStatus},
		}
	}

	// Add priority.
	notionPriority := localPriorityToNotion(task.Priority)
	if notionPriority != "" {
		props["Priority"] = map[string]any{
			"select": map[string]any{"name": notionPriority},
		}
	}

	// Add due date.
	if task.DueAt != "" {
		dueDate := task.DueAt
		if len(dueDate) > 10 {
			dueDate = dueDate[:10]
		}
		props["Due Date"] = map[string]any{
			"date": map[string]any{"start": dueDate},
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}

	if task.ExternalID != "" && task.ExternalSource == "notion" {
		// Update existing Notion page.
		body := map[string]any{"properties": props}
		bodyJSON, _ := json.Marshal(body)
		req, err := http.NewRequest("PATCH", fmt.Sprintf("%s/pages/%s", notionAPIBase, task.ExternalID),
			strings.NewReader(string(bodyJSON)))
		if err != nil {
			return fmt.Errorf("notion: create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Notion-Version", notionAPIVersion)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("notion: request failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("notion API returned %d: %s", resp.StatusCode, string(respBody))
		}
	} else {
		// Create new Notion page.
		body := map[string]any{
			"parent":     map[string]any{"database_id": dbID},
			"properties": props,
		}
		bodyJSON, _ := json.Marshal(body)
		req, err := http.NewRequest("POST", notionAPIBase+"/pages",
			strings.NewReader(string(bodyJSON)))
		if err != nil {
			return fmt.Errorf("notion: create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Notion-Version", notionAPIVersion)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("notion: request failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("notion API returned %d: %s", resp.StatusCode, string(respBody))
		}

		// Store external ID.
		var created struct {
			ID string `json:"id"`
		}
		if json.NewDecoder(resp.Body).Decode(&created) == nil && created.ID != "" && globalTaskManager != nil {
			globalTaskManager.UpdateTask(task.ID, map[string]any{
				"externalId":     created.ID,
				"externalSource": "notion",
			})
		}
	}

	return nil
}

// SyncAll performs a full bidirectional sync with Notion.
func (ns *NotionSync) SyncAll(userID string) (pulled int, pushed int, err error) {
	pulled, err = ns.PullTasks(userID)
	if err != nil {
		return pulled, 0, fmt.Errorf("notion sync pull: %w", err)
	}

	if globalTaskManager == nil {
		return pulled, 0, nil
	}

	tasks, err := globalTaskManager.ListTasks(userID, TaskFilter{})
	if err != nil {
		return pulled, 0, fmt.Errorf("notion sync list: %w", err)
	}

	for _, task := range tasks {
		if task.ExternalSource == "notion" {
			continue
		}
		if task.ExternalID != "" {
			continue
		}
		if err := ns.PushTask(task); err != nil {
			logWarn("notion sync: push task failed", "taskId", task.ID, "error", err)
			continue
		}
		pushed++
	}

	logInfo("notion sync complete", "pulled", pulled, "pushed", pushed, "userId", userID)
	return pulled, pushed, nil
}

// --- Tool Handler ---

// toolNotionSync handles the notion_sync tool.
func toolNotionSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if !cfg.TaskManager.Notion.Enabled {
		return "", fmt.Errorf("notion sync not enabled")
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

	ns := newNotionSync(cfg)

	switch args.Action {
	case "pull":
		n, err := ns.PullTasks(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Pulled %d tasks from Notion.", n), nil
	case "push":
		if globalTaskManager == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		tasks, _ := globalTaskManager.ListTasks(args.UserID, TaskFilter{})
		pushed := 0
		for _, task := range tasks {
			if task.ExternalSource == "notion" || task.ExternalID != "" {
				continue
			}
			if err := ns.PushTask(task); err != nil {
				continue
			}
			pushed++
		}
		return fmt.Sprintf("Pushed %d tasks to Notion.", pushed), nil
	case "sync", "":
		pulled, pushed, err := ns.SyncAll(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Notion sync complete: pulled %d, pushed %d.", pulled, pushed), nil
	default:
		return "", fmt.Errorf("unknown action %q (use pull, push, or sync)", args.Action)
	}
}

// --- Helpers ---

// notionStatusToLocal maps Notion status select to local status.
func notionStatusToLocal(page notionPage) string {
	if page.Properties.Status.Select == nil {
		return "todo"
	}
	switch strings.ToLower(page.Properties.Status.Select.Name) {
	case "done", "complete", "completed":
		return "done"
	case "in progress", "in_progress", "doing":
		return "in_progress"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "todo"
	}
}

// notionPriorityToLocal maps Notion priority select to local priority.
func notionPriorityToLocal(page notionPage) int {
	if page.Properties.Priority.Select == nil {
		return 2
	}
	switch strings.ToLower(page.Properties.Priority.Select.Name) {
	case "urgent", "critical", "p1":
		return 1
	case "high", "p2":
		return 2
	case "medium", "normal", "p3":
		return 3
	case "low", "p4":
		return 4
	default:
		return 2
	}
}

// localStatusToNotion maps local status to Notion status select name.
func localStatusToNotion(status string) string {
	switch status {
	case "todo":
		return "To Do"
	case "in_progress":
		return "In Progress"
	case "done":
		return "Done"
	case "cancelled":
		return "Cancelled"
	default:
		return ""
	}
}

// localPriorityToNotion maps local priority to Notion priority select name.
func localPriorityToNotion(priority int) string {
	switch priority {
	case 1:
		return "Urgent"
	case 2:
		return "High"
	case 3:
		return "Medium"
	case 4:
		return "Low"
	default:
		return ""
	}
}
