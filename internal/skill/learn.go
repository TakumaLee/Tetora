package skill

import (
	"fmt"
	"strings"
	"time"

	"tetora/internal/db"
)

// --- P18.4: Self-Improving Skills — Learning ---

// InitSkillUsageTable creates the skill_usage table if it doesn't exist,
// and migrates it to include observability columns.
func InitSkillUsageTable(dbPath string) error {
	sql := `CREATE TABLE IF NOT EXISTS skill_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		skill_name TEXT NOT NULL,
		event_type TEXT NOT NULL,
		task_prompt TEXT DEFAULT '',
		role TEXT DEFAULT '',
		created_at TEXT NOT NULL
	)`
	if _, err := db.Query(dbPath, sql); err != nil {
		return err
	}
	// Migration: add observability columns (idempotent — silently ignores if already exist).
	// NOTE: agent→role migration must run first so downstream migrations can depend on role.
	migrations := []string{
		// Migrate legacy "agent" column → "role" (must be first)
		`ALTER TABLE skill_usage ADD COLUMN role TEXT DEFAULT ''`,
		`UPDATE skill_usage SET role = agent WHERE role = '' AND agent != ''`,
		`ALTER TABLE skill_usage ADD COLUMN status TEXT DEFAULT ''`,
		`ALTER TABLE skill_usage ADD COLUMN duration_ms INTEGER DEFAULT 0`,
		`ALTER TABLE skill_usage ADD COLUMN source TEXT DEFAULT ''`,
		`ALTER TABLE skill_usage ADD COLUMN session_id TEXT DEFAULT ''`,
		`ALTER TABLE skill_usage ADD COLUMN error_msg TEXT DEFAULT ''`,
	}
	for _, m := range migrations {
		_, _ = db.Query(dbPath, m) // ignore "duplicate column" errors
	}
	return nil
}

// SkillEventOpts holds optional fields for an extended skill usage event.
type SkillEventOpts struct {
	Status     string // success, fail, skipped
	DurationMs int    // execution time in milliseconds
	Source     string // dispatch, claude-code, cli
	SessionID  string // link to job_runs
	ErrorMsg   string // failure reason
}

// RecordSkillEvent inserts a skill usage event into the database (basic form).
func RecordSkillEvent(dbPath, skillName, eventType, taskPrompt, role string) {
	RecordSkillEventEx(dbPath, skillName, eventType, taskPrompt, role, SkillEventOpts{})
}

// RecordSkillEventEx inserts a skill usage event with extended observability fields.
func RecordSkillEventEx(dbPath, skillName, eventType, taskPrompt, role string, opts SkillEventOpts) {
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO skill_usage (skill_name, event_type, task_prompt, role, created_at, status, duration_ms, source, session_id, error_msg)
		 VALUES ('%s', '%s', '%s', '%s', '%s', '%s', %d, '%s', '%s', '%s')`,
		db.Escape(skillName),
		db.Escape(eventType),
		db.Escape(taskPrompt),
		db.Escape(role),
		now,
		db.Escape(opts.Status),
		opts.DurationMs,
		db.Escape(opts.Source),
		db.Escape(opts.SessionID),
		db.Escape(opts.ErrorMsg),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		logWarn("record skill event failed", "skill", skillName, "event", eventType, "error", err)
	}
}

// QuerySkillStats returns per-skill aggregated usage statistics.
func QuerySkillStats(dbPath string, skillName string, days int) ([]map[string]any, error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)

	var where string
	if skillName != "" {
		where = fmt.Sprintf("AND skill_name = '%s'", db.Escape(skillName))
	}

	sql := fmt.Sprintf(`
		SELECT
			skill_name,
			COUNT(CASE WHEN event_type = 'injected' THEN 1 END) AS injected,
			COUNT(CASE WHEN event_type = 'invoked' THEN 1 END) AS invoked,
			COUNT(CASE WHEN event_type = 'invoked' AND status = 'success' THEN 1 END) AS success,
			COUNT(CASE WHEN event_type = 'invoked' AND status = 'fail' THEN 1 END) AS fail,
			MAX(created_at) AS last_used
		FROM skill_usage
		WHERE created_at >= '%s' %s
		GROUP BY skill_name
		ORDER BY invoked DESC, injected DESC
	`, cutoff, where)
	return db.Query(dbPath, sql)
}

// QuerySkillHistory returns recent events for a specific skill.
func QuerySkillHistory(dbPath, skillName string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}
	sql := fmt.Sprintf(
		`SELECT event_type, status, source, duration_ms, error_msg, role, created_at
		 FROM skill_usage
		 WHERE skill_name = '%s'
		 ORDER BY created_at DESC LIMIT %d`,
		db.Escape(skillName), limit)
	return db.Query(dbPath, sql)
}

// SuggestSkillsForPrompt finds previously created skills whose creation prompt
// overlaps with the current prompt. Returns skill names sorted by relevance.
func SuggestSkillsForPrompt(dbPath, prompt string, limit int) []string {
	if prompt == "" || limit <= 0 {
		return nil
	}

	// Get all "created" events with their prompts.
	sql := `SELECT DISTINCT skill_name, task_prompt FROM skill_usage WHERE event_type = 'created' AND task_prompt != ''`
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}

	// Simple word overlap scoring.
	promptWords := SkillTokenize(prompt)
	if len(promptWords) == 0 {
		return nil
	}

	type scored struct {
		name  string
		score float64
	}
	var candidates []scored

	for _, row := range rows {
		name := fmt.Sprintf("%v", row["skill_name"])
		taskPrompt := fmt.Sprintf("%v", row["task_prompt"])

		taskWords := SkillTokenize(taskPrompt)
		if len(taskWords) == 0 {
			continue
		}

		overlap := wordOverlap(promptWords, taskWords)
		if overlap > 0 {
			// Normalize by the smaller set size.
			minLen := len(promptWords)
			if len(taskWords) < minLen {
				minLen = len(taskWords)
			}
			score := float64(overlap) / float64(minLen)
			if score >= 0.15 { // minimum 15% overlap threshold
				candidates = append(candidates, scored{name, score})
			}
		}
	}

	// Sort by score descending (simple insertion sort, small N).
	for i := 1; i < len(candidates); i++ {
		j := i
		for j > 0 && candidates[j].score > candidates[j-1].score {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
			j--
		}
	}

	// Deduplicate and limit.
	seen := make(map[string]bool)
	var result []string
	for _, c := range candidates {
		if seen[c.name] {
			continue
		}
		seen[c.name] = true
		result = append(result, c.name)
		if len(result) >= limit {
			break
		}
	}
	return result
}

// AutoInjectLearnedSkills returns file-based skills that match the current task
// via both ShouldInjectSkill and historical prompt matching.
func AutoInjectLearnedSkills(cfg *AppConfig, task TaskContext) []SkillConfig {
	fileSkills := LoadFileSkills(cfg)
	if len(fileSkills) == 0 {
		return nil
	}

	// First, collect skills that match via injection rules.
	// Learned skills use ShouldInjectLearnedSkill (adds recency-based window).
	var matched []SkillConfig
	matchedNames := make(map[string]bool)
	for _, s := range fileSkills {
		var inject bool
		if s.Learned {
			inject = ShouldInjectLearnedSkill(s, task)
		} else {
			inject = ShouldInjectSkill(s, task)
		}
		if inject {
			matched = append(matched, s)
			matchedNames[s.Name] = true
		}
	}

	// Then, check historical prompt overlap for additional suggestions.
	if cfg.HistoryDB != "" {
		suggested := SuggestSkillsForPrompt(cfg.HistoryDB, task.Prompt, 5)
		for _, name := range suggested {
			if matchedNames[name] {
				continue
			}
			// Find the skill config for this name.
			for _, s := range fileSkills {
				if s.Name == name {
					matched = append(matched, s)
					matchedNames[name] = true
					break
				}
			}
		}
	}

	return matched
}

// SkillTokenize splits text into lowercase words for skill prompt comparison.
func SkillTokenize(text string) []string {
	text = strings.ToLower(text)
	words := strings.Fields(text)
	// Filter out very short words (noise).
	var result []string
	for _, w := range words {
		// Strip common punctuation.
		w = strings.Trim(w, ".,;:!?\"'()[]{}#@$%^&*")
		if len(w) >= 3 {
			result = append(result, w)
		}
	}
	return result
}

// RecordSkillCompletion records completed/failed events for skills
// that were injected for this task (matching by agent + recent timing).
func RecordSkillCompletion(dbPath string, task TaskContext, result TaskCompletion, role, startedAt, finishedAt string) {
	if dbPath == "" {
		return
	}

	// Find skills injected for this task. Try session_id match first,
	// fall back to role + recent time window (injection records often
	// lack session_id).
	var rows []map[string]any
	var err error
	if task.SessionID != "" {
		sql := fmt.Sprintf(
			`SELECT DISTINCT skill_name FROM skill_usage
			 WHERE event_type = 'injected' AND role = '%s'
			 AND session_id = '%s'`,
			db.Escape(role),
			db.Escape(task.SessionID),
		)
		rows, err = db.Query(dbPath, sql)
	}
	// Fallback: match by role + recent injections (last 4 hours).
	if len(rows) == 0 {
		cutoff := time.Now().UTC().Add(-4 * time.Hour).Format(time.RFC3339)
		sql := fmt.Sprintf(
			`SELECT DISTINCT skill_name FROM skill_usage
			 WHERE event_type = 'injected' AND role = '%s'
			 AND created_at > '%s'
			 AND skill_name NOT IN (
			   SELECT skill_name FROM skill_usage
			   WHERE event_type IN ('completed','failed') AND role = '%s'
			   AND created_at > '%s'
			 )`,
			db.Escape(role), cutoff, db.Escape(role), cutoff,
		)
		rows, err = db.Query(dbPath, sql)
	}
	if err != nil || len(rows) == 0 {
		return
	}

	// Calculate duration.
	var durationMs int
	if startedAt != "" && finishedAt != "" {
		start, e1 := time.Parse(time.RFC3339, startedAt)
		end, e2 := time.Parse(time.RFC3339, finishedAt)
		if e1 == nil && e2 == nil {
			durationMs = int(end.Sub(start).Milliseconds())
		}
	}

	status := "success"
	eventType := "completed"
	errMsg := ""
	if result.Status != "success" {
		status = "fail"
		eventType = "failed"
		errMsg = db.Truncate(result.Error, 200)
	}

	for _, row := range rows {
		skillName := fmt.Sprintf("%v", row["skill_name"])
		RecordSkillEventEx(dbPath, skillName, eventType, "", role, SkillEventOpts{
			Status:     status,
			DurationMs: durationMs,
			Source:     "dispatch",
			SessionID:  task.SessionID,
			ErrorMsg:   errMsg,
		})
	}
}

// wordOverlap counts how many words from a appear in b.
func wordOverlap(a, b []string) int {
	set := make(map[string]bool, len(b))
	for _, w := range b {
		set[w] = true
	}
	count := 0
	for _, w := range a {
		if set[w] {
			count++
		}
	}
	return count
}
