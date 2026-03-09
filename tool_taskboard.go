package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// registerTaskboardTools registers taskboard CRUD + decompose tools for agents.
func registerTaskboardTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	if !cfg.TaskBoard.Enabled {
		return
	}

	if enabled("taskboard_list") {
		r.Register(&ToolDef{
			Name:        "taskboard_list",
			Description: "List taskboard tickets filtered by status, assignee, project, or parentId",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"status": {"type": "string", "description": "Filter by status: backlog/todo/doing/review/done/failed"},
					"assignee": {"type": "string", "description": "Filter by agent name"},
					"project": {"type": "string", "description": "Filter by project name"},
					"parentId": {"type": "string", "description": "Filter by parent task ID (show children only)"}
				}
			}`),
			Handler: toolTaskboardList(cfg),
			Builtin: true,
		})
	}

	if enabled("taskboard_get") {
		r.Register(&ToolDef{
			Name:        "taskboard_get",
			Description: "Get a single taskboard ticket with its comments thread",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Task ID"}
				},
				"required": ["id"]
			}`),
			Handler: toolTaskboardGet(cfg),
			Builtin: true,
		})
	}

	if enabled("taskboard_create") {
		r.Register(&ToolDef{
			Name:        "taskboard_create",
			Description: "Create a new taskboard ticket",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Task title (required)"},
					"description": {"type": "string", "description": "Task description"},
					"assignee": {"type": "string", "description": "Agent name to assign"},
					"priority": {"type": "string", "description": "Priority: low/normal/high/urgent"},
					"project": {"type": "string", "description": "Project name"},
					"parentId": {"type": "string", "description": "Parent task ID (for subtasks)"},
					"model": {"type": "string", "description": "LLM model override (e.g. sonnet, haiku, opus)"},
					"dependsOn": {"type": "array", "items": {"type": "string"}, "description": "Task IDs this task depends on"},
					"type": {"type": "string", "description": "Task type for branch naming: feat/fix/refactor/chore (default: feat)"}
				},
				"required": ["title"]
			}`),
			Handler: toolTaskboardCreate(cfg),
			Builtin: true,
		})
	}

	if enabled("taskboard_move") {
		r.Register(&ToolDef{
			Name:        "taskboard_move",
			Description: "Move a taskboard ticket to a new status",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Task ID"},
					"status": {"type": "string", "description": "Target status: backlog/todo/doing/review/done/failed"}
				},
				"required": ["id", "status"]
			}`),
			Handler: toolTaskboardMove(cfg),
			Builtin: true,
		})
	}

	if enabled("taskboard_comment") {
		r.Register(&ToolDef{
			Name:        "taskboard_comment",
			Description: "Add a comment to a taskboard ticket",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"taskId": {"type": "string", "description": "Task ID to comment on"},
					"content": {"type": "string", "description": "Comment content"},
					"author": {"type": "string", "description": "Comment author (defaults to calling agent)"},
					"type": {"type": "string", "description": "Comment type: spec/context/log/system (default: log)"}
				},
				"required": ["taskId", "content"]
			}`),
			Handler: toolTaskboardComment(cfg),
			Builtin: true,
		})
	}

	if enabled("taskboard_decompose") {
		r.Register(&ToolDef{
			Name:        "taskboard_decompose",
			Description: "Batch-create subtasks under a parent task. Idempotent: skips subtasks with matching title+parentId that already exist.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"parentId": {"type": "string", "description": "Parent task ID"},
					"subtasks": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"title": {"type": "string", "description": "Subtask title"},
								"description": {"type": "string", "description": "Subtask description"},
								"assignee": {"type": "string", "description": "Agent name"},
								"priority": {"type": "string", "description": "Priority: low/normal/high/urgent"},
								"model": {"type": "string", "description": "LLM model override"},
								"dependsOn": {"type": "array", "items": {"type": "string"}, "description": "Dependency task IDs"}
							},
							"required": ["title"]
						},
						"description": "List of subtasks to create"
					}
				},
				"required": ["parentId", "subtasks"]
			}`),
			Handler: toolTaskboardDecompose(cfg),
			Builtin: true,
		})
	}
}

// --- Handler Factories ---

func toolTaskboardList(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			Status   string `json:"status"`
			Assignee string `json:"assignee"`
			Project  string `json:"project"`
			ParentID string `json:"parentId"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		// If parentId is specified, use ListChildren.
		if args.ParentID != "" {
			children, err := tb.ListChildren(args.ParentID)
			if err != nil {
				return "", err
			}
			out, _ := json.MarshalIndent(children, "", "  ")
			return string(out), nil
		}

		tasks, err := tb.ListTasks(args.Status, args.Assignee, args.Project)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(tasks, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardGet(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" {
			return "", fmt.Errorf("id is required")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		task, err := tb.GetTask(args.ID)
		if err != nil {
			return "", err
		}
		comments, err := tb.GetThread(args.ID)
		if err != nil {
			return "", err
		}

		result := map[string]any{
			"task":     task,
			"comments": comments,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardCreate(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Assignee    string   `json:"assignee"`
			Priority    string   `json:"priority"`
			Project     string   `json:"project"`
			ParentID    string   `json:"parentId"`
			Model       string   `json:"model"`
			DependsOn   []string `json:"dependsOn"`
			Workflow    string   `json:"workflow"`
			Type        string   `json:"type"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.Title == "" {
			return "", fmt.Errorf("title is required")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		task, err := tb.CreateTask(TaskBoard{
			Title:       args.Title,
			Description: args.Description,
			Assignee:    args.Assignee,
			Priority:    args.Priority,
			Project:     args.Project,
			ParentID:    args.ParentID,
			Model:       args.Model,
			DependsOn:   args.DependsOn,
			Workflow:    args.Workflow,
			Type:        args.Type,
		})
		if err != nil {
			return "", err
		}

		out, _ := json.MarshalIndent(task, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardMove(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" || args.Status == "" {
			return "", fmt.Errorf("id and status are required")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		task, err := tb.MoveTask(args.ID, args.Status)
		if err != nil {
			return "", err
		}

		out, _ := json.MarshalIndent(task, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardComment(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			TaskID  string `json:"taskId"`
			Content string `json:"content"`
			Author  string `json:"author"`
			Type    string `json:"type"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.TaskID == "" || args.Content == "" {
			return "", fmt.Errorf("taskId and content are required")
		}
		if args.Author == "" {
			args.Author = "agent"
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		comment, err := tb.AddComment(args.TaskID, args.Author, args.Content, args.Type)
		if err != nil {
			return "", err
		}

		out, _ := json.MarshalIndent(comment, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardDecompose(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			ParentID string `json:"parentId"`
			Subtasks []struct {
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Assignee    string   `json:"assignee"`
				Priority    string   `json:"priority"`
				Model       string   `json:"model"`
				Type        string   `json:"type"`
				DependsOn   []string `json:"dependsOn"`
			} `json:"subtasks"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ParentID == "" {
			return "", fmt.Errorf("parentId is required")
		}
		if len(args.Subtasks) == 0 {
			return "", fmt.Errorf("subtasks array is required and must not be empty")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		// Verify parent exists.
		parent, err := tb.GetTask(args.ParentID)
		if err != nil {
			return "", fmt.Errorf("parent task not found: %w", err)
		}

		// Fetch existing children for idempotency check.
		existing, err := tb.ListChildren(args.ParentID)
		if err != nil {
			return "", fmt.Errorf("failed to list existing children: %w", err)
		}
		existingTitles := make(map[string]bool, len(existing))
		for _, e := range existing {
			existingTitles[e.Title] = true
		}

		var created, skipped int
		var subtaskIDs []string

		for _, sub := range args.Subtasks {
			if sub.Title == "" {
				continue
			}

			// Idempotency: skip if same title already exists under this parent.
			if existingTitles[sub.Title] {
				skipped++
				continue
			}

			priority := sub.Priority
			if priority == "" {
				priority = parent.Priority
			}

			subType := sub.Type
			if subType == "" {
				subType = parent.Type
			}

			task, err := tb.CreateTask(TaskBoard{
				Title:       sub.Title,
				Description: sub.Description,
				Assignee:    sub.Assignee,
				Priority:    priority,
				Project:     parent.Project,
				ParentID:    args.ParentID,
				Model:       sub.Model,
				Type:        subType,
				DependsOn:   sub.DependsOn,
			})
			if err != nil {
				logWarn("taskboard_decompose: create subtask failed", "parent", args.ParentID, "title", sub.Title, "error", err)
				continue
			}

			created++
			subtaskIDs = append(subtaskIDs, task.ID)
			existingTitles[sub.Title] = true
		}

		// Move parent to "doing" (waiting for children) if it was backlog/todo.
		if created > 0 && (parent.Status == "backlog" || parent.Status == "todo") {
			if _, err := tb.MoveTask(args.ParentID, "doing"); err != nil {
				logWarn("taskboard_decompose: failed to move parent to doing", "parentId", args.ParentID, "error", err)
			}
		}

		// Add decomposition comment to parent.
		if created > 0 {
			comment := fmt.Sprintf("[decompose] Created %d subtasks (skipped %d existing): %s",
				created, skipped, strings.Join(subtaskIDs, ", "))
			if _, err := tb.AddComment(args.ParentID, "system", comment); err != nil {
				logWarn("taskboard_decompose: add comment failed", "parentId", args.ParentID, "error", err)
			}
		}

		result := map[string]any{
			"created":    created,
			"skipped":    skipped,
			"subtaskIds": subtaskIDs,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil
	}
}
