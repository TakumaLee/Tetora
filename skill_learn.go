package main

import (
	"fmt"
	"strings"
	"time"
)

// --- P18.4: Self-Improving Skills — Learning ---

// initSkillUsageTable creates the skill_usage table if it doesn't exist.
func initSkillUsageTable(dbPath string) error {
	sql := `CREATE TABLE IF NOT EXISTS skill_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		skill_name TEXT NOT NULL,
		event_type TEXT NOT NULL,
		task_prompt TEXT DEFAULT '',
		agent TEXT DEFAULT '',
		created_at TEXT NOT NULL
	)`
	_, err := queryDB(dbPath, sql)
	return err
}

// recordSkillEvent inserts a skill usage event into the database.
func recordSkillEvent(dbPath, skillName, eventType, taskPrompt, role string) {
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO skill_usage (skill_name, event_type, task_prompt, agent, created_at) VALUES ('%s', '%s', '%s', '%s', '%s')`,
		escapeSQLite(skillName),
		escapeSQLite(eventType),
		escapeSQLite(taskPrompt),
		escapeSQLite(role),
		now,
	)
	if _, err := queryDB(dbPath, sql); err != nil {
		logWarn("record skill event failed", "skill", skillName, "event", eventType, "error", err)
	}
}

// suggestSkillsForPrompt finds previously created skills whose creation prompt
// overlaps with the current prompt. Returns skill names sorted by relevance.
func suggestSkillsForPrompt(dbPath, prompt string, limit int) []string {
	if prompt == "" || limit <= 0 {
		return nil
	}

	// Get all "created" events with their prompts.
	sql := `SELECT DISTINCT skill_name, task_prompt FROM skill_usage WHERE event_type = 'created' AND task_prompt != ''`
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil
	}

	// Simple word overlap scoring.
	promptWords := skillTokenize(prompt)
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

		taskWords := skillTokenize(taskPrompt)
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

// autoInjectLearnedSkills returns file-based skills that match the current task
// via both shouldInjectSkill and historical prompt matching.
func autoInjectLearnedSkills(cfg *Config, task Task) []SkillConfig {
	fileSkills := loadFileSkills(cfg)
	if len(fileSkills) == 0 {
		return nil
	}

	// First, collect skills that match via normal injection rules.
	var matched []SkillConfig
	matchedNames := make(map[string]bool)
	for _, s := range fileSkills {
		if shouldInjectSkill(s, task) {
			matched = append(matched, s)
			matchedNames[s.Name] = true
		}
	}

	// Then, check historical prompt overlap for additional suggestions.
	if cfg.HistoryDB != "" {
		suggested := suggestSkillsForPrompt(cfg.HistoryDB, task.Prompt, 5)
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

// skillTokenize splits text into lowercase words for skill prompt comparison.
func skillTokenize(text string) []string {
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
