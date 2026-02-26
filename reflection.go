package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// --- Reflection Config ---

// ReflectionConfig controls post-execution reflection behavior.
type ReflectionConfig struct {
	Enabled       bool    `json:"enabled"`
	TriggerOnFail bool    `json:"triggerOnFail,omitempty"` // reflect on failed tasks too (default false)
	MinCost       float64 `json:"minCost,omitempty"`       // minimum task cost to trigger reflection
	Budget        float64 `json:"budget,omitempty"`        // budget for reflection LLM call (default 0.05)
}

// --- Reflection Result ---

// ReflectionResult holds the reflection output.
type ReflectionResult struct {
	TaskID      string  `json:"taskId"`
	Agent        string  `json:"agent"`
	Score       int     `json:"score"`
	Feedback    string  `json:"feedback"`
	Improvement string  `json:"improvement"`
	CostUSD     float64 `json:"costUsd"`
	CreatedAt   string  `json:"createdAt"`
}

// --- DB Init ---

// initReflectionDB creates the reflections table and index.
func initReflectionDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS reflections (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  score INTEGER NOT NULL DEFAULT 3,
  feedback TEXT DEFAULT '',
  improvement TEXT DEFAULT '',
  cost_usd REAL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_reflections_agent ON reflections(agent);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init reflections: %s: %w", string(out), err)
	}
	return nil
}

// --- MinCost Default ---

// minCostOrDefault returns the configured MinCost, defaulting to $0.03.
func (c ReflectionConfig) minCostOrDefault() float64 {
	if c.MinCost > 0 {
		return c.MinCost
	}
	return 0.03
}

// --- Should Reflect ---

// shouldReflect determines if a reflection should be performed after task execution.
func shouldReflect(cfg *Config, task Task, result TaskResult) bool {
	if cfg == nil || !cfg.Reflection.Enabled {
		return false
	}
	// Skip if agent is empty — reflection needs an agent context.
	if task.Agent == "" {
		return false
	}
	// Skip failed/timeout tasks unless TriggerOnFail is set.
	isFailed := result.Status == "error" || result.Status == "timeout"
	if isFailed && !cfg.Reflection.TriggerOnFail {
		return false
	}
	// Skip if cost is below MinCost threshold (default $0.03).
	// Bypass cost check for failed tasks when TriggerOnFail is enabled —
	// failed tasks often have zero cost but still benefit from reflection.
	if !isFailed && result.CostUSD < cfg.Reflection.minCostOrDefault() {
		return false
	}
	return true
}

// --- Perform Reflection ---

// performReflection runs a cheap LLM call to evaluate task output quality.
func performReflection(ctx context.Context, cfg *Config, task Task, result TaskResult, sem ...chan struct{}) (*ReflectionResult, error) {
	// Use provided sem or create a temporary one.
	var taskSem chan struct{}
	if len(sem) > 0 && sem[0] != nil {
		taskSem = sem[0]
	} else {
		taskSem = make(chan struct{}, 1)
	}
	// Truncate prompt and output for the reflection prompt.
	promptSnippet := task.Prompt
	if len(promptSnippet) > 500 {
		promptSnippet = promptSnippet[:500] + "..."
	}
	outputSnippet := result.Output
	if len(outputSnippet) > 1000 {
		outputSnippet = outputSnippet[:1000] + "..."
	}

	reflPrompt := fmt.Sprintf(
		`Evaluate this task output quality. Score 1-5 (1=poor, 5=excellent).
Respond ONLY with JSON: {"score":N,"feedback":"brief assessment","improvement":"specific suggestion"}

Task: %s
Agent: %s
Status: %s
Output: %s`,
		promptSnippet, task.Agent, result.Status, outputSnippet)

	budget := reflectionBudgetOrDefault(cfg)

	reflTask := Task{
		ID:             newUUID(),
		Name:           "reflection-" + task.ID[:8],
		Prompt:         reflPrompt,
		Model:          "haiku",
		Budget:         budget,
		Timeout:        "30s",
		PermissionMode: "plan",
		Agent:           task.Agent,
		Source:         "reflection",
	}
	fillDefaults(cfg, &reflTask)
	// Override model back to haiku after fillDefaults may have set it.
	reflTask.Model = "haiku"
	reflTask.Budget = budget

	reflResult := runSingleTask(ctx, cfg, reflTask, taskSem, "")

	if reflResult.Status != "success" {
		return nil, fmt.Errorf("reflection failed: %s", reflResult.Error)
	}

	ref, err := parseReflectionOutput(reflResult.Output)
	if err != nil {
		return nil, fmt.Errorf("parse reflection: %w", err)
	}

	ref.TaskID = task.ID
	ref.Agent = task.Agent
	ref.CostUSD = reflResult.CostUSD
	ref.CreatedAt = time.Now().UTC().Format(time.RFC3339)

	return ref, nil
}

// --- Parse Reflection Output ---

// parseReflectionOutput extracts a ReflectionResult from LLM output.
// Handles raw JSON as well as JSON wrapped in markdown code blocks.
func parseReflectionOutput(output string) (*ReflectionResult, error) {
	// Try to find JSON object in the output.
	jsonStr := extractJSON(output)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in reflection output")
	}

	var parsed struct {
		Score       int    `json:"score"`
		Feedback    string `json:"feedback"`
		Improvement string `json:"improvement"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON in reflection: %w", err)
	}

	// Validate score range.
	if parsed.Score < 1 || parsed.Score > 5 {
		return nil, fmt.Errorf("score %d out of range 1-5", parsed.Score)
	}

	return &ReflectionResult{
		Score:       parsed.Score,
		Feedback:    parsed.Feedback,
		Improvement: parsed.Improvement,
	}, nil
}

// extractJSON finds the first JSON object in the string.
// Handles markdown code blocks like ```json {...} ```.
func extractJSON(s string) string {
	// Strip markdown code fences if present.
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (```json or just ```).
		idx := strings.Index(s, "\n")
		if idx >= 0 {
			s = s[idx+1:]
		}
		// Remove closing fence.
		if last := strings.LastIndex(s, "```"); last >= 0 {
			s = s[:last]
		}
		s = strings.TrimSpace(s)
	}

	// Find first { and last matching }.
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	// Find the matching closing brace.
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// --- Store Reflection ---

// storeReflection persists a reflection result to the database.
func storeReflection(dbPath string, ref *ReflectionResult) error {
	sql := fmt.Sprintf(
		`INSERT INTO reflections (task_id, agent, score, feedback, improvement, cost_usd, created_at)
		 VALUES ('%s','%s',%d,'%s','%s',%f,'%s')`,
		escapeSQLite(ref.TaskID),
		escapeSQLite(ref.Agent),
		ref.Score,
		escapeSQLite(ref.Feedback),
		escapeSQLite(ref.Improvement),
		ref.CostUSD,
		escapeSQLite(ref.CreatedAt),
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("store reflection: %s: %w", string(out), err)
	}

	return nil
}

// --- Query Reflections ---

// queryReflections returns recent reflections, optionally filtered by agent.
func queryReflections(dbPath, agent string, limit int) ([]ReflectionResult, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if agent != "" {
		where = fmt.Sprintf("WHERE agent = '%s'", escapeSQLite(agent))
	}

	sql := fmt.Sprintf(
		`SELECT task_id, agent, score, feedback, improvement, cost_usd, created_at
		 FROM reflections %s ORDER BY created_at DESC LIMIT %d`,
		where, limit)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var results []ReflectionResult
	for _, row := range rows {
		results = append(results, ReflectionResult{
			TaskID:      jsonStr(row["task_id"]),
			Agent:       jsonStr(row["agent"]),
			Score:       jsonInt(row["score"]),
			Feedback:    jsonStr(row["feedback"]),
			Improvement: jsonStr(row["improvement"]),
			CostUSD:     jsonFloat(row["cost_usd"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return results, nil
}

// --- Build Reflection Context ---

// buildReflectionContext formats recent reflections as a text block suitable
// for injection into agent prompts. Returns empty string if no reflections exist.
func buildReflectionContext(dbPath, role string, limit int) string {
	if dbPath == "" || role == "" {
		return ""
	}
	if limit <= 0 {
		limit = 5
	}

	refs, err := queryReflections(dbPath, role, limit)
	if err != nil || len(refs) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Recent self-assessments for agent %s:\n", role))
	for _, ref := range refs {
		b.WriteString(fmt.Sprintf("- Score: %d/5 - %s\n", ref.Score, ref.Improvement))
	}
	return b.String()
}

// --- Budget Helper ---

// reflectionBudgetOrDefault returns the configured reflection budget or the default of $0.05.
func reflectionBudgetOrDefault(cfg *Config) float64 {
	if cfg != nil && cfg.Reflection.Budget > 0 {
		return cfg.Reflection.Budget
	}
	return 0.05
}
