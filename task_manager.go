package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// globalTaskManager is the singleton task manager service.
var globalTaskManager *TaskManagerService

// --- Tool Handlers ---

// toolTaskCreate handles the task_create tool.
func toolTaskCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TaskManager == nil {
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

	created, err := app.TaskManager.CreateTask(task)
	if err != nil {
		return "", err
	}

	// Auto-decompose if requested.
	if args.Decompose && len(args.Subtasks) > 0 {
		subs, err := app.TaskManager.DecomposeTask(created.ID, args.Subtasks)
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
	app := appFromCtx(ctx)
	if app == nil || app.TaskManager == nil {
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

	tasks, err := app.TaskManager.ListTasks(args.UserID, filters)
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
		subs, _ := app.TaskManager.GetSubtasks(t.ID)
		results = append(results, taskWithSubs{UserTask: t, SubtaskCount: len(subs)})
	}

	out, _ := json.MarshalIndent(results, "", "  ")
	return string(out), nil
}

// toolTaskComplete handles the task_complete tool.
func toolTaskComplete(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TaskManager == nil {
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

	if err := app.TaskManager.CompleteTask(args.TaskID); err != nil {
		return "", err
	}

	task, _ := app.TaskManager.GetTask(args.TaskID)
	if task != nil {
		out, _ := json.MarshalIndent(task, "", "  ")
		return fmt.Sprintf("Task completed.\n%s", string(out)), nil
	}
	return "Task completed.", nil
}

// toolTaskReview handles the task_review tool.
func toolTaskReview(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TaskManager == nil {
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

	review, err := app.TaskManager.GenerateReview(args.UserID, args.Period)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(review, "", "  ")
	return string(out), nil
}
