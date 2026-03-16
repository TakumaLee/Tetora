package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- Tool Handler ---

// toolNotionSync handles the notion_sync tool.
func toolNotionSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
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
		if app == nil || app.TaskManager == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		localTasks, _ := app.TaskManager.ListTasks(args.UserID, TaskFilter{})
		pushed := 0
		for _, task := range localTasks {
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
