package reflection

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/dispatch"
)

// Result holds the reflection output.
type Result struct {
	TaskID                    string  `json:"taskId"`
	Agent                     string  `json:"agent"`
	Score                     int     `json:"score"`
	Feedback                  string  `json:"feedback"`
	Improvement               string  `json:"improvement"`
	CostUSD                   float64 `json:"costUsd"`
	CreatedAt                 string  `json:"createdAt"`
	EstimatedManualDurationSec int    `json:"estimatedManualDurationSec"`
	AIDurationSec             int     `json:"aiDurationSec"`
}

// Deps holds root-package callbacks needed by performReflection.
// Using a struct avoids import cycles: this package does not import package main.
type Deps struct {
	// Executor runs a single task (wraps root runSingleTask).
	Executor dispatch.TaskExecutor
	// NewID generates a new unique ID.
	NewID func() string
	// FillDefaults populates default values for a task.
	FillDefaults func(cfg *config.Config, t *dispatch.Task)
}

// InitDB creates the reflections table and index.
func InitDB(dbPath string) error {
	// Create table first (so subsequent ALTER TABLE migration has a target).
	if err := db.Exec(dbPath, `CREATE TABLE IF NOT EXISTS reflections (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  agent TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT '',
  score INTEGER NOT NULL DEFAULT 3,
  feedback TEXT DEFAULT '',
  improvement TEXT DEFAULT '',
  cost_usd REAL DEFAULT 0,
  created_at TEXT NOT NULL
);`); err != nil {
		return fmt.Errorf("init reflections table: %w", err)
	}
	// Migration: add agent column if missing (for DBs created before this column existed).
	if err := db.Exec(dbPath, `ALTER TABLE reflections ADD COLUMN agent TEXT NOT NULL DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("init reflections migration: %w", err)
		}
	}
	if err := db.Exec(dbPath, `CREATE INDEX IF NOT EXISTS idx_reflections_agent ON reflections(agent);`); err != nil {
		return fmt.Errorf("init reflections index: %w", err)
	}
	// Migration: add role column for DBs created with legacy schema (role NOT NULL, no DEFAULT).
	// New DBs (created by InitDB above) don't have role, so ADD COLUMN ensures it exists everywhere.
	if err := db.Exec(dbPath, `ALTER TABLE reflections ADD COLUMN role TEXT NOT NULL DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("init reflections migration (role): %w", err)
		}
	}
	// Backfill: sync role from agent for any rows where role is empty.
	if err := db.Exec(dbPath, `UPDATE reflections SET role = agent WHERE role = '' AND agent != '';`); err != nil {
		return fmt.Errorf("init reflections backfill role: %w", err)
	}
	// Migration: add time savings columns.
	for _, m := range []string{
		"ALTER TABLE reflections ADD COLUMN estimated_manual_duration_sec INTEGER DEFAULT 0;",
		"ALTER TABLE reflections ADD COLUMN ai_duration_sec INTEGER DEFAULT 0;",
	} {
		if err := db.Exec(dbPath, m); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf("init reflections migration: %w", err)
			}
		}
	}
	return nil
}

// ShouldReflect determines if a reflection should be performed after task execution.
func ShouldReflect(cfg *config.Config, task dispatch.Task, result dispatch.TaskResult) bool {
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
	if !isFailed && result.CostUSD < cfg.Reflection.MinCostOrDefault() {
		return false
	}
	return true
}

// Perform runs a cheap LLM call to evaluate task output quality.
// The executor in deps is responsible for any semaphore management.
func Perform(ctx context.Context, cfg *config.Config, task dispatch.Task, result dispatch.TaskResult, deps Deps) (*Result, error) {
	// Truncate prompt and output for the reflection prompt.
	promptSnippet := task.Prompt
	if runes := []rune(promptSnippet); len(runes) > 500 {
		promptSnippet = string(runes[:500]) + "..."
	}
	outputSnippet := result.Output
	if runes := []rune(outputSnippet); len(runes) > 1000 {
		outputSnippet = string(runes[:1000]) + "..."
	}

	reflPrompt := fmt.Sprintf(
		`Evaluate this task output quality. Score 1-5 (1=poor, 5=excellent).
Respond ONLY with JSON: {"score":N,"feedback":"brief assessment","improvement":"specific suggestion"}

Task: %s
Agent: %s
Status: %s
Output: %s`,
		promptSnippet, task.Agent, result.Status, outputSnippet)

	budget := BudgetOrDefault(cfg)

	reflTask := dispatch.Task{
		Name:           "reflection-" + task.ID[:8],
		Prompt:         reflPrompt,
		Model:          "haiku",
		Budget:         budget,
		Timeout:        "30s",
		PermissionMode: "plan",
		Agent:          task.Agent,
		Source:         "reflection",
	}
	if deps.NewID != nil {
		reflTask.ID = deps.NewID()
	}
	if deps.FillDefaults != nil {
		deps.FillDefaults(cfg, &reflTask)
	}
	// Override model back to haiku after FillDefaults may have set it.
	reflTask.Model = "haiku"
	reflTask.Budget = budget

	var reflResult dispatch.TaskResult
	if deps.Executor != nil {
		reflResult = deps.Executor.RunTask(ctx, reflTask, task.Agent)
	} else {
		return nil, fmt.Errorf("reflection: no executor provided")
	}

	if reflResult.Status != "success" {
		return nil, fmt.Errorf("reflection failed: %s", reflResult.Error)
	}

	ref, err := ParseOutput(reflResult.Output)
	if err != nil {
		return nil, fmt.Errorf("parse reflection: %w", err)
	}

	ref.TaskID = task.ID
	ref.Agent = task.Agent
	ref.CostUSD = reflResult.CostUSD
	ref.CreatedAt = time.Now().UTC().Format(time.RFC3339)

	return ref, nil
}

// ParseOutput extracts a Result from LLM output.
// Handles raw JSON as well as JSON wrapped in markdown code blocks.
func ParseOutput(output string) (*Result, error) {
	// Try to find JSON object in the output.
	jsonStr := ExtractJSON(output)
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

	return &Result{
		Score:       parsed.Score,
		Feedback:    parsed.Feedback,
		Improvement: parsed.Improvement,
	}, nil
}

// ExtractJSON finds the first JSON object in the string.
// Handles markdown code blocks like ```json {...} ```.
func ExtractJSON(s string) string {
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

// Store persists a reflection result to the database.
func Store(dbPath string, ref *Result) error {
	sql := fmt.Sprintf(
		`INSERT INTO reflections (task_id, role, agent, score, feedback, improvement, cost_usd, created_at, estimated_manual_duration_sec, ai_duration_sec)
		 VALUES ('%s','%s','%s',%d,'%s','%s',%f,'%s',%d,%d)`,
		db.Escape(ref.TaskID),
		db.Escape(ref.Agent), // role == agent intentionally: role is a legacy column kept for schema backward-compat; future divergence (e.g. sub-roles within an agent) can split them
		db.Escape(ref.Agent),
		ref.Score,
		db.Escape(ref.Feedback),
		db.Escape(ref.Improvement),
		ref.CostUSD,
		db.Escape(ref.CreatedAt),
		ref.EstimatedManualDurationSec,
		ref.AIDurationSec,
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("store reflection: %s: %w", string(out), err)
	}

	return nil
}

// Query returns recent reflections, optionally filtered by agent.
func Query(dbPath, agent string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if agent != "" {
		where = fmt.Sprintf("WHERE agent = '%s'", db.Escape(agent))
	}

	sql := fmt.Sprintf(
		`SELECT task_id, agent, score, feedback, improvement, cost_usd, created_at
		 FROM reflections %s ORDER BY created_at DESC LIMIT %d`,
		where, limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, row := range rows {
		results = append(results, Result{
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

// BuildContext formats recent reflections as a text block suitable
// for injection into agent prompts. Returns empty string if no reflections exist.
func BuildContext(dbPath, role string, limit int) string {
	if dbPath == "" || role == "" {
		return ""
	}
	if limit <= 0 {
		limit = 5
	}

	refs, err := Query(dbPath, role, limit)
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

// BudgetOrDefault returns the configured reflection budget or the default of $0.05.
func BudgetOrDefault(cfg *config.Config) float64 {
	if cfg != nil && cfg.Reflection.Budget > 0 {
		return cfg.Reflection.Budget
	}
	return 0.05
}

// EstimateManualDuration returns estimated human time in seconds for a task,
// based on task type and reflection score (1-5).
func EstimateManualDuration(taskType string, score int) int {
	// Clamp score to 1-5.
	if score < 1 {
		score = 1
	}
	if score > 5 {
		score = 5
	}

	// Minutes matrix: [score-1] per type.
	matrix := map[string][5]int{
		"feat":     {30, 60, 120, 240, 480},
		"fix":      {10, 20, 40, 90, 180},
		"refactor": {20, 40, 90, 180, 360},
		"chore":    {5, 10, 20, 40, 90},
	}
	defaultRow := [5]int{15, 30, 60, 120, 240}

	row, ok := matrix[taskType]
	if !ok {
		row = defaultRow
	}
	return row[score-1] * 60 // minutes → seconds
}

// TimeSavingsRow holds aggregated time savings per agent.
type TimeSavingsRow struct {
	Agent     string
	TaskCount int
	ManualSec int
	AISec     int
}

// QueryTimeSavings returns per-agent time savings for a given month (YYYY-MM).
// If month is empty, returns all-time data.
func QueryTimeSavings(dbPath, month string) ([]TimeSavingsRow, error) {
	where := ""
	if month != "" {
		where = fmt.Sprintf("WHERE strftime('%%Y-%%m', created_at) = '%s'", db.Escape(month))
	}
	sql := fmt.Sprintf(
		`SELECT agent, COUNT(*) as task_count,
		        SUM(estimated_manual_duration_sec) as manual_sec,
		        SUM(ai_duration_sec) as ai_sec
		 FROM reflections %s GROUP BY agent ORDER BY manual_sec DESC`, where)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	var results []TimeSavingsRow
	for _, row := range rows {
		results = append(results, TimeSavingsRow{
			Agent:     jsonStr(row["agent"]),
			TaskCount: jsonInt(row["task_count"]),
			ManualSec: jsonInt(row["manual_sec"]),
			AISec:     jsonInt(row["ai_sec"]),
		})
	}
	return results, nil
}

// ExtractAutoLesson appends a lesson entry to {workspaceDir}/rules/auto-lessons.md
// when the reflection score is low (≤ 2) and improvement text is non-empty.
// Duplicate improvements (matched by first 40 chars) are silently skipped.
func ExtractAutoLesson(workspaceDir string, ref *Result) error {
	if ref == nil || ref.Improvement == "" || ref.Score >= 3 {
		return nil
	}

	rulesDir := filepath.Join(workspaceDir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return fmt.Errorf("ExtractAutoLesson: mkdir rules: %w", err)
	}

	autoPath := filepath.Join(rulesDir, "auto-lessons.md")

	// Dedup: check if improvement already present.
	key := ref.Improvement
	if len(key) > 40 {
		key = key[:40]
	}
	if existing, err := os.ReadFile(autoPath); err == nil {
		if strings.Contains(string(existing), key) {
			return nil
		}
	}

	// Build entry.
	entry := fmt.Sprintf("- [pending] (score=%d, task=%s, agent=%s) %s\n",
		ref.Score, ref.TaskID, ref.Agent, ref.Improvement)

	// Create or append.
	f, err := os.OpenFile(autoPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("ExtractAutoLesson: open: %w", err)
	}
	defer f.Close()

	// Write header if file is new (size 0).
	info, _ := f.Stat()
	if info.Size() == 0 {
		if _, err := f.WriteString("# Auto-Lessons\n\n"); err != nil {
			return fmt.Errorf("ExtractAutoLesson: write header: %w", err)
		}
	}

	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("ExtractAutoLesson: write entry: %w", err)
	}
	return nil
}

// --- JSON field helpers (package-local) ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var i int
		fmt.Sscanf(x, "%d", &i)
		return i
	default:
		return 0
	}
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		var f float64
		fmt.Sscanf(x, "%f", &f)
		return f
	default:
		return 0
	}
}
