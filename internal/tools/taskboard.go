package tools

import (
	"encoding/json"

	"tetora/internal/config"
)

// TaskboardDeps holds pre-built handler functions for taskboard tools.
// The root package constructs these closures (which capture root-only types
// like TaskBoardEngine) and passes them in; this package only owns the
// registration logic and JSON schemas.
type TaskboardDeps struct {
	ListHandler      Handler
	GetHandler       Handler
	CreateHandler    Handler
	MoveHandler      Handler
	CommentHandler   Handler
	DecomposeHandler Handler
}

// RegisterTaskboardTools registers the 6 taskboard tools into r.
// It returns early without registering anything if cfg.TaskBoard.Enabled is false.
func RegisterTaskboardTools(r *Registry, cfg *config.Config, enabled func(string) bool, deps TaskboardDeps) {
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
			Handler: deps.ListHandler,
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
			Handler: deps.GetHandler,
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
			Handler: deps.CreateHandler,
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
			Handler: deps.MoveHandler,
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
			Handler: deps.CommentHandler,
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
			Handler: deps.DecomposeHandler,
			Builtin: true,
		})
	}
}
