package tools

import (
	"encoding/json"
	"fmt"

	"tetora/internal/config"
	"tetora/internal/reflection"
)

// ReflectionDeps holds handler closures for reflection/lesson tools.
// The root package constructs these closures (capturing dbPath, workspaceDir)
// and passes them in; this package owns only the tool registration + schemas.
type ReflectionDeps struct {
	SearchHandler          Handler
	GetHandler             Handler
	LessonHistoryHandler   Handler
	LessonCandidatesHandler Handler
}

// RegisterReflectionTools registers the agent-facing reflection query tools:
//   - reflection_search: keyword/agent/task_id/score/date filter over past reflections
//   - reflection_get:    fetch a single reflection by task_id (for `## Sources` lookups)
//   - lesson_history:    raw lesson_events by lesson_key prefix (traces every occurrence)
//   - lesson_candidates: current promotion-candidate clusters (≥ threshold distinct tasks)
func RegisterReflectionTools(r *Registry, cfg *config.Config, enabled func(string) bool, deps ReflectionDeps) {
	if enabled("reflection_search") {
		r.Register(&ToolDef{
			Name:        "reflection_search",
			Description: "Search past task reflections by keyword, agent, task ID, score upper bound, or date. Useful for answering 'has this happened before?' or tracing a rule back to its source experiences.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"keyword":  {"type": "string", "description": "Substring match against improvement or feedback text"},
					"agent":    {"type": "string", "description": "Filter by agent name (e.g. kokuyou, ruri)"},
					"taskId":   {"type": "string", "description": "Filter by exact task ID"},
					"scoreMax": {"type": "integer", "description": "Score upper bound inclusive, 1-5; omit for no filter"},
					"since":    {"type": "string", "description": "RFC3339 lower bound for created_at"},
					"limit":    {"type": "integer", "description": "Max results, default 10, cap %d"}
				}
			}`, reflection.MaxSearchReflectionsLimit)),
			Handler: deps.SearchHandler,
			Builtin: true,
		})
	}

	if enabled("reflection_get") {
		r.Register(&ToolDef{
			Name:        "reflection_get",
			Description: "Fetch a single reflection by task_id. Use when a rule's `## Sources` section lists task IDs you want to expand.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"taskId": {"type": "string", "description": "Task ID of the reflection to retrieve"}
				},
				"required": ["taskId"]
			}`),
			Handler: deps.GetHandler,
			Builtin: true,
		})
	}

	if enabled("lesson_history") {
		r.Register(&ToolDef{
			Name:        "lesson_history",
			Description: "List raw lesson_events for a lesson_key prefix. Traces every occurrence of a repeated mistake across tasks/agents.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"keyPrefix": {"type": "string", "description": "Lesson key prefix (first 40 bytes of improvement, UTF-8 boundary-safe). Empty returns all events."},
					"limit":     {"type": "integer", "description": "Max results, default 50, cap %d"}
				}
			}`, reflection.MaxLessonHistoryLimit)),
			Handler: deps.LessonHistoryHandler,
			Builtin: true,
		})
	}

	if enabled("lesson_candidates") {
		r.Register(&ToolDef{
			Name:        "lesson_candidates",
			Description: "List auto-lesson clusters whose distinct task_id count meets a promotion threshold. Use to inspect what's queued for rule promotion.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"threshold": {"type": "integer", "description": "Minimum distinct task_ids per cluster, default 3"}
				}
			}`),
			Handler: deps.LessonCandidatesHandler,
			Builtin: true,
		})
	}

}
