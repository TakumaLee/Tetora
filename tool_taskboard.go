package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tetora/internal/log"
)

// Registration moved to internal/tools/taskboard.go.
// Handler factories below are passed via TaskboardDeps in wire_tools.go.

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
			// Suggest similar tasks on not-found.
			normalizedID := normalizeTaskID(args.ID)
			if candidates := tb.suggestTasks(normalizedID); len(candidates) > 0 {
				lines := []string{err.Error(), "Did you mean:"}
				for _, c := range candidates {
					lines = append(lines, fmt.Sprintf("  %s  %s  (%s)", c.ID, c.Title, c.Status))
				}
				return "", fmt.Errorf("%s", strings.Join(lines, "\n"))
			}
			return "", err
		}
		// Use normalized ID (from task) for thread lookup.
		comments, err := tb.GetThread(task.ID)
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
				log.Warn("taskboard_decompose: create subtask failed", "parent", args.ParentID, "title", sub.Title, "error", err)
				continue
			}

			created++
			subtaskIDs = append(subtaskIDs, task.ID)
			existingTitles[sub.Title] = true
		}

		// Move parent to "todo" (ready, waiting for children) if it was in backlog.
		if created > 0 && parent.Status == "backlog" {
			if _, err := tb.MoveTask(args.ParentID, "todo"); err != nil {
				log.Warn("taskboard_decompose: failed to move parent to todo", "parentId", args.ParentID, "error", err)
			}
		}

		// Add decomposition comment to parent.
		if created > 0 {
			comment := fmt.Sprintf("[decompose] Created %d subtasks (skipped %d existing): %s",
				created, skipped, strings.Join(subtaskIDs, ", "))
			if _, err := tb.AddComment(args.ParentID, "system", comment); err != nil {
				log.Warn("taskboard_decompose: add comment failed", "parentId", args.ParentID, "error", err)
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
