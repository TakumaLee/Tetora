package taskboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SessionLockFile is the filename written inside an active worktree to signal
// that a Claude session is currently running there. Both the worktree manager
// and the task dispatcher reference this constant to avoid hardcoding the string
// in multiple places.
const SessionLockFile = ".tetora-active"

// GenerateID generates a unique ID with the given prefix.
func GenerateID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// NormalizeTaskID adds "task-" prefix if the ID looks like a bare number.
func NormalizeTaskID(id string) string {
	if id == "" {
		return id
	}
	if !strings.Contains(id, "-") {
		return "task-" + id
	}
	return id
}

// parseTaskRow converts a DB row map into a TaskBoard struct.
func parseTaskRow(row map[string]any) TaskBoard {
	dependsOnJSON := fmt.Sprintf("%v", row["depends_on"])
	var dependsOn []string
	if dependsOnJSON != "" && dependsOnJSON != "[]" {
		json.Unmarshal([]byte(dependsOnJSON), &dependsOn)
	}

	parentID := fmt.Sprintf("%v", row["parent_id"])
	if parentID == "<nil>" {
		parentID = ""
	}

	taskType := fmt.Sprintf("%v", row["type"])
	if taskType == "<nil>" || taskType == "" {
		taskType = "feat"
	}

	workflow := fmt.Sprintf("%v", row["workflow"])
	if workflow == "<nil>" {
		workflow = ""
	}

	workflowRunID := fmt.Sprintf("%v", row["workflow_run_id"])
	if workflowRunID == "<nil>" {
		workflowRunID = ""
	}

	workdirsJSON := fmt.Sprintf("%v", row["workdirs"])
	var workdirs []string
	if workdirsJSON != "" && workdirsJSON != "<nil>" && workdirsJSON != "[]" {
		json.Unmarshal([]byte(workdirsJSON), &workdirs)
	}

	return TaskBoard{
		ID:            fmt.Sprintf("%v", row["id"]),
		Project:       fmt.Sprintf("%v", row["project"]),
		Title:         fmt.Sprintf("%v", row["title"]),
		Description:   fmt.Sprintf("%v", row["description"]),
		Status:        fmt.Sprintf("%v", row["status"]),
		Assignee:      fmt.Sprintf("%v", row["assignee"]),
		Priority:      fmt.Sprintf("%v", row["priority"]),
		Model:         fmt.Sprintf("%v", row["model"]),
		ParentID:      parentID,
		DependsOn:     dependsOn,
		Type:          taskType,
		Workflow:      workflow,
		DiscordThread: fmt.Sprintf("%v", row["discord_thread_id"]),
		CreatedAt:     fmt.Sprintf("%v", row["created_at"]),
		UpdatedAt:     fmt.Sprintf("%v", row["updated_at"]),
		CompletedAt:   fmt.Sprintf("%v", row["completed_at"]),
		RetryCount:     int(getFloat64(row, "retry_count")),
		ExecutionCount: int(getFloat64(row, "execution_count")),
		CostUSD:       getFloat64(row, "cost_usd"),
		DurationMs:    int64(getFloat64(row, "duration_ms")),
		SessionID:     fmt.Sprintf("%v", row["session_id"]),
		WorkflowRunID:  workflowRunID,
		Workdirs:       workdirs,
		AllowDangerous: getFloat64(row, "allow_dangerous") != 0,
	}
}

func getFloat64(row map[string]any, key string) float64 {
	if val, ok := row[key]; ok {
		if f, ok := val.(float64); ok {
			return f
		}
	}
	return 0
}

func toInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

// jsonUnmarshalStringSlice parses a JSON array of strings. Returns nil on error.
func jsonUnmarshalStringSlice(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var result []string
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil
	}
	return result
}

func toSnakeCase(s string) string {
	switch s {
	case "discordThread":
		return "discord_thread_id"
	case "dependsOn":
		return "depends_on"
	case "parentId":
		return "parent_id"
	case "type":
		return "`type`" // SQLite reserved word — must be quoted
	case "workflowRunId":
		return "workflow_run_id"
	case "executionCount":
		return "execution_count"
	default:
		return s
	}
}
