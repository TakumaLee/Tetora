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
	if err := InitLessonEventsDB(dbPath); err != nil {
		return err
	}
	return nil
}

// InitLessonEventsDB creates the lesson_events table used by the promotion
// pipeline. Each row records a single auto-lesson trigger keyed by
// lesson_key (improvement[:40]) so that promotion logic can count distinct
// task occurrences without relying on the text-based dedup in auto-lessons.md.
func InitLessonEventsDB(dbPath string) error {
	if dbPath == "" {
		return nil
	}
	if err := db.Exec(dbPath, `CREATE TABLE IF NOT EXISTS lesson_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  lesson_key TEXT NOT NULL,
  task_id TEXT NOT NULL,
  agent TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  improvement TEXT NOT NULL DEFAULT '',
  score INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);`); err != nil {
		return fmt.Errorf("init lesson_events table: %w", err)
	}
	if err := db.Exec(dbPath, `CREATE INDEX IF NOT EXISTS idx_lesson_events_key ON lesson_events(lesson_key);`); err != nil {
		return fmt.Errorf("init lesson_events index: %w", err)
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

// SearchQuery filters reflections for interactive agent lookup.
// Empty fields are ignored. Keyword matches improvement OR feedback.
type SearchQuery struct {
	Keyword  string
	Agent    string
	TaskID   string
	ScoreMax int // inclusive upper bound; 0 means no filter
	Since    string // RFC3339 lower bound; empty = no filter
	Limit    int
}

// SearchReflections returns reflections matching q, newest first.
// Intended as the backing query for the agent-callable `reflection_search`
// tool. Uses parameterised escape helpers for all user-supplied strings.
func SearchReflections(dbPath string, q SearchQuery) ([]Result, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("SearchReflections: dbPath required")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	var clauses []string
	if q.Keyword != "" {
		kw := db.Escape(q.Keyword)
		clauses = append(clauses, fmt.Sprintf("(improvement LIKE '%%%s%%' OR feedback LIKE '%%%s%%')", kw, kw))
	}
	if q.Agent != "" {
		clauses = append(clauses, fmt.Sprintf("agent = '%s'", db.Escape(q.Agent)))
	}
	if q.TaskID != "" {
		clauses = append(clauses, fmt.Sprintf("task_id = '%s'", db.Escape(q.TaskID)))
	}
	if q.ScoreMax > 0 {
		clauses = append(clauses, fmt.Sprintf("score <= %d", q.ScoreMax))
	}
	if q.Since != "" {
		clauses = append(clauses, fmt.Sprintf("created_at >= '%s'", db.Escape(q.Since)))
	}
	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}

	sql := fmt.Sprintf(
		`SELECT task_id, agent, score, feedback, improvement, cost_usd, created_at
		 FROM reflections %s ORDER BY created_at DESC LIMIT %d`,
		where, limit)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(rows))
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

// GetReflection fetches a single reflection by task_id (the most useful handle
// agents have when following a `## Sources` link from a rule).
func GetReflection(dbPath, taskID string) (*Result, error) {
	if dbPath == "" || taskID == "" {
		return nil, fmt.Errorf("GetReflection: dbPath and taskID required")
	}
	sql := fmt.Sprintf(
		`SELECT task_id, agent, score, feedback, improvement, cost_usd, created_at
		 FROM reflections WHERE task_id = '%s' LIMIT 1`,
		db.Escape(taskID))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	r := rows[0]
	return &Result{
		TaskID:      jsonStr(r["task_id"]),
		Agent:       jsonStr(r["agent"]),
		Score:       jsonInt(r["score"]),
		Feedback:    jsonStr(r["feedback"]),
		Improvement: jsonStr(r["improvement"]),
		CostUSD:     jsonFloat(r["cost_usd"]),
		CreatedAt:   jsonStr(r["created_at"]),
	}, nil
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

// ExtractAutoLesson appends a lesson entry to {workspaceDir}/memory/auto-lessons.md
// when the reflection score is low (≤ 2) and improvement text is non-empty.
// Duplicate improvements (matched by first 40 chars) are silently skipped in
// the markdown file, but every trigger is still recorded in lesson_events when
// dbPath is non-empty — that's the authoritative source for promotion counts.
//
// Lives under memory/ (not rules/) because entries are a pending-promotion queue,
// not governance. Rules/ is for validated cross-agent rules; unpromoted raw
// lessons would bloat it past the workspace injection cap.
func ExtractAutoLesson(workspaceDir, dbPath string, ref *Result) error {
	if ref == nil || ref.Improvement == "" || ref.Score >= 3 {
		return nil
	}

	key := lessonKey(ref.Improvement)

	// Record the event first — even if markdown write skips due to dedup,
	// we still want the occurrence counted for promotion.
	if dbPath != "" {
		if err := recordLessonEvent(dbPath, key, ref); err != nil {
			return fmt.Errorf("ExtractAutoLesson: record event: %w", err)
		}
	}

	memoryDir := filepath.Join(workspaceDir, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		return fmt.Errorf("ExtractAutoLesson: mkdir memory: %w", err)
	}

	autoPath := filepath.Join(memoryDir, "auto-lessons.md")

	// Dedup: check if improvement already present in markdown.
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

// lessonKey returns the dedup/promotion key for an improvement: first 40
// characters (byte-wise, matching legacy behaviour so existing markdown entries
// stay compatible).
func lessonKey(improvement string) string {
	if len(improvement) > 40 {
		return improvement[:40]
	}
	return improvement
}

func recordLessonEvent(dbPath, key string, ref *Result) error {
	// Ensure table exists — InitLessonEventsDB is idempotent.
	if err := InitLessonEventsDB(dbPath); err != nil {
		return err
	}
	createdAt := ref.CreatedAt
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}
	return db.ExecArgs(dbPath,
		`INSERT INTO lesson_events (lesson_key, task_id, agent, session_id, improvement, score, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key, ref.TaskID, ref.Agent, "", ref.Improvement, ref.Score, createdAt)
}

// PromotionCandidate describes a lesson_key that has fired enough times to be
// considered for promotion from auto-lessons.md into rules/.
type PromotionCandidate struct {
	LessonKey    string   `json:"lessonKey"`
	Occurrences  int      `json:"occurrences"`
	Agents       []string `json:"agents"`
	TaskIDs      []string `json:"taskIds"`
	Improvements []string `json:"improvements"`
	FirstSeen    string   `json:"firstSeen"`
	LastSeen     string   `json:"lastSeen"`
}

// LessonEvent is a single historical trigger recorded in lesson_events.
type LessonEvent struct {
	LessonKey   string `json:"lessonKey"`
	TaskID      string `json:"taskId"`
	Agent       string `json:"agent"`
	SessionID   string `json:"sessionId"`
	Improvement string `json:"improvement"`
	Score       int    `json:"score"`
	CreatedAt   string `json:"createdAt"`
}

// PromotionResult summarises a PromoteLessons run.
type PromotionResult struct {
	Candidates  []PromotionCandidate `json:"candidates"`
	ReportPath  string               `json:"reportPath,omitempty"`
	PromotedIDs []string             `json:"promotedIds"`
	AutoLessons string               `json:"autoLessonsPath,omitempty"`
}

// StaleRuleResult describes a rules/*.md file that has not been modified in
// the configured staleDays window.
type StaleRuleResult struct {
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
	LastModified string `json:"lastModified"`
	AgeDays      int    `json:"ageDays"`
}

// ScanPromotionCandidates inspects lesson_events and returns lesson_key groups
// whose distinct occurrences reach the given threshold. Dedup inside a single
// task_id is applied (a task repeatedly re-triggering the same lesson only
// counts once) to prevent a noisy task from inflating the count.
func ScanPromotionCandidates(dbPath string, threshold int) ([]PromotionCandidate, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("ScanPromotionCandidates: dbPath required")
	}
	if threshold < 1 {
		threshold = 3
	}
	if err := InitLessonEventsDB(dbPath); err != nil {
		return nil, err
	}

	sql := fmt.Sprintf(
		`SELECT lesson_key,
		        COUNT(DISTINCT task_id) AS occurrences,
		        MIN(created_at) AS first_seen,
		        MAX(created_at) AS last_seen
		 FROM lesson_events
		 GROUP BY lesson_key
		 HAVING COUNT(DISTINCT task_id) >= %d
		 ORDER BY occurrences DESC, last_seen DESC`, threshold)

	groupRows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("scan groups: %w", err)
	}

	candidates := make([]PromotionCandidate, 0, len(groupRows))
	for _, row := range groupRows {
		cand := PromotionCandidate{
			LessonKey:   db.Str(row["lesson_key"]),
			Occurrences: db.Int(row["occurrences"]),
			FirstSeen:   db.Str(row["first_seen"]),
			LastSeen:    db.Str(row["last_seen"]),
		}
		// Pull the detail rows for this lesson_key.
		detailRows, err := db.QueryArgs(dbPath,
			`SELECT task_id, agent, improvement
			 FROM lesson_events WHERE lesson_key = ? ORDER BY created_at`,
			cand.LessonKey)
		if err != nil {
			return nil, fmt.Errorf("scan details: %w", err)
		}
		seenTask := make(map[string]struct{})
		seenAgent := make(map[string]struct{})
		seenImprovement := make(map[string]struct{})
		for _, dr := range detailRows {
			tid := db.Str(dr["task_id"])
			ag := db.Str(dr["agent"])
			im := db.Str(dr["improvement"])
			if _, ok := seenTask[tid]; !ok && tid != "" {
				cand.TaskIDs = append(cand.TaskIDs, tid)
				seenTask[tid] = struct{}{}
			}
			if _, ok := seenAgent[ag]; !ok && ag != "" {
				cand.Agents = append(cand.Agents, ag)
				seenAgent[ag] = struct{}{}
			}
			if _, ok := seenImprovement[im]; !ok && im != "" {
				cand.Improvements = append(cand.Improvements, im)
				seenImprovement[im] = struct{}{}
			}
		}
		candidates = append(candidates, cand)
	}
	return candidates, nil
}

// PromoteLessons runs ScanPromotionCandidates and, when autoWrite is true,
// materialises a report file under {workspaceDir}/rules/auto-promoted-YYYYMMDD.md
// and flips the corresponding entries in memory/auto-lessons.md from
// [pending] → [promoted-YYYYMMDD]. When autoWrite is false, no files are
// modified and only the candidate list is returned (dry-run).
func PromoteLessons(workspaceDir, dbPath string, threshold int, autoWrite bool) (*PromotionResult, error) {
	candidates, err := ScanPromotionCandidates(dbPath, threshold)
	if err != nil {
		return nil, err
	}

	result := &PromotionResult{Candidates: candidates}
	result.AutoLessons = filepath.Join(workspaceDir, "memory", "auto-lessons.md")

	if !autoWrite || len(candidates) == 0 {
		return result, nil
	}

	stamp := time.Now().UTC().Format("20060102")
	rulesDir := filepath.Join(workspaceDir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return nil, fmt.Errorf("PromoteLessons: mkdir rules: %w", err)
	}
	reportPath := filepath.Join(rulesDir, fmt.Sprintf("auto-promoted-%s.md", stamp))
	result.ReportPath = reportPath

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Auto-Promoted Rules (%s)\n\n", time.Now().UTC().Format("2006-01-02")))
	b.WriteString("> Candidates surfaced from `lesson_events` (≥ threshold distinct tasks).\n")
	b.WriteString("> Promoted entries require human review before merging into topical rule files.\n\n")
	for _, c := range candidates {
		b.WriteString(fmt.Sprintf("## Pattern: %s\n", escapeMarkdown(c.LessonKey)))
		b.WriteString(fmt.Sprintf("- Occurrences: %d (distinct tasks)\n", c.Occurrences))
		b.WriteString(fmt.Sprintf("- Agents: %s\n", strings.Join(c.Agents, ", ")))
		b.WriteString(fmt.Sprintf("- Task IDs: %s\n", strings.Join(c.TaskIDs, ", ")))
		b.WriteString(fmt.Sprintf("- First seen: %s\n", c.FirstSeen))
		b.WriteString(fmt.Sprintf("- Last seen:  %s\n", c.LastSeen))
		b.WriteString("- Improvements:\n")
		for _, im := range c.Improvements {
			b.WriteString(fmt.Sprintf("  - %s\n", im))
		}
		b.WriteString("\n")
		result.PromotedIDs = append(result.PromotedIDs, c.LessonKey)
	}
	if err := os.WriteFile(reportPath, []byte(b.String()), 0o644); err != nil {
		return nil, fmt.Errorf("PromoteLessons: write report: %w", err)
	}

	// Flip [pending] → [promoted-YYYYMMDD] for each candidate in auto-lessons.md.
	// We operate on byte content to avoid restructuring the markdown.
	if content, err := os.ReadFile(result.AutoLessons); err == nil {
		updated := string(content)
		marker := fmt.Sprintf("[promoted-%s]", stamp)
		for _, c := range candidates {
			// Walk each line looking for a "[pending]" line containing the lesson_key.
			lines := strings.Split(updated, "\n")
			for i, line := range lines {
				if !strings.Contains(line, "[pending]") {
					continue
				}
				if !strings.Contains(line, c.LessonKey) {
					continue
				}
				lines[i] = strings.Replace(line, "[pending]", marker, 1)
			}
			updated = strings.Join(lines, "\n")
		}
		if updated != string(content) {
			if err := os.WriteFile(result.AutoLessons, []byte(updated), 0o644); err != nil {
				return nil, fmt.Errorf("PromoteLessons: rewrite auto-lessons.md: %w", err)
			}
		}
	}
	return result, nil
}

// QueryLessonHistory returns all lesson_events whose lesson_key starts with
// the given prefix. Empty prefix returns the entire history.
func QueryLessonHistory(dbPath, lessonKeyPrefix string, limit int) ([]LessonEvent, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("QueryLessonHistory: dbPath required")
	}
	if limit <= 0 {
		limit = 100
	}
	if err := InitLessonEventsDB(dbPath); err != nil {
		return nil, err
	}

	var rows []map[string]any
	var err error
	if lessonKeyPrefix == "" {
		rows, err = db.QueryArgs(dbPath,
			`SELECT lesson_key, task_id, agent, session_id, improvement, score, created_at
			 FROM lesson_events ORDER BY created_at DESC LIMIT `+fmt.Sprintf("%d", limit))
	} else {
		pattern := db.Escape(lessonKeyPrefix) + "%"
		rows, err = db.Query(dbPath, fmt.Sprintf(
			`SELECT lesson_key, task_id, agent, session_id, improvement, score, created_at
			 FROM lesson_events WHERE lesson_key LIKE '%s'
			 ORDER BY created_at DESC LIMIT %d`, pattern, limit))
	}
	if err != nil {
		return nil, err
	}

	events := make([]LessonEvent, 0, len(rows))
	for _, r := range rows {
		events = append(events, LessonEvent{
			LessonKey:   db.Str(r["lesson_key"]),
			TaskID:      db.Str(r["task_id"]),
			Agent:       db.Str(r["agent"]),
			SessionID:   db.Str(r["session_id"]),
			Improvement: db.Str(r["improvement"]),
			Score:       db.Int(r["score"]),
			CreatedAt:   db.Str(r["created_at"]),
		})
	}
	return events, nil
}

// AuditStaleRules scans {workspaceDir}/rules/*.md (excluding INDEX.md and
// auto-promoted-*.md) and returns any file whose mtime is older than
// staleDays. Caller decides whether to prune or flag the results.
func AuditStaleRules(workspaceDir string, staleDays int) ([]StaleRuleResult, error) {
	if staleDays <= 0 {
		staleDays = 90
	}
	rulesDir := filepath.Join(workspaceDir, "rules")
	info, err := os.Stat(rulesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("AuditStaleRules: stat: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("AuditStaleRules: rules path is not a directory: %s", rulesDir)
	}

	cutoff := time.Now().Add(-time.Duration(staleDays) * 24 * time.Hour)
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return nil, fmt.Errorf("AuditStaleRules: read: %w", err)
	}

	var out []StaleRuleResult
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if name == "INDEX.md" || strings.HasPrefix(name, "auto-promoted-") {
			continue
		}
		full := filepath.Join(rulesDir, name)
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().After(cutoff) {
			continue
		}
		age := int(time.Since(fi.ModTime()).Hours() / 24)
		out = append(out, StaleRuleResult{
			Path:         full,
			RelativePath: filepath.Join("rules", name),
			LastModified: fi.ModTime().UTC().Format(time.RFC3339),
			AgeDays:      age,
		})
	}
	return out, nil
}

func escapeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
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
