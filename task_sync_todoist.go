package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- Tool Handler ---

// toolTodoistSync handles the todoist_sync tool.
func toolTodoistSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
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
		if app == nil || app.TaskManager == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		localTasks, _ := app.TaskManager.ListTasks(args.UserID, TaskFilter{})
		pushed := 0
		for _, task := range localTasks {
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
