package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// buildReviewDigest assembles a structured markdown digest of recent activity
// for LLM analysis. days controls the lookback window (capped at 90).
func buildReviewDigest(cfg *Config, days int) string {
	if cfg == nil || cfg.HistoryDB == "" {
		return "(Review digest unavailable: no history database configured)"
	}

	// Clamp days to [1, 90].
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}

	cutoff := time.Now().AddDate(0, 0, -days).Format(time.RFC3339)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Activity Digest (last %d days)\n\n", days))

	// Section 1: Recent Conversations.
	b.WriteString("## Recent Conversations\n\n")
	msgs := queryReviewMessages(cfg.HistoryDB, cutoff, 200)
	if len(msgs) == 0 {
		b.WriteString("(none in this period)\n\n")
	} else {
		for _, m := range msgs {
			content := m.Content
			if len(content) > 300 {
				content = content[:300] + "..."
			}
			content = strings.ReplaceAll(content, "\n", " ")
			date := m.CreatedAt
			if len(date) > 10 {
				date = date[:10]
			}
			b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", date, m.Role, content))
		}
		b.WriteString("\n")
	}

	// Section 2: Recent Reflections.
	b.WriteString("## Recent Reflections\n\n")
	refs := queryReviewReflections(cfg.HistoryDB, cutoff, 50)
	if len(refs) == 0 {
		b.WriteString("(none in this period)\n\n")
	} else {
		for _, r := range refs {
			b.WriteString(fmt.Sprintf("- Score: %d/5 | Role: %s | %s | Improve: %s\n",
				r.Score, r.Role, r.Feedback, r.Improvement))
		}
		b.WriteString("\n")
	}

	// Section 3: Recent Job Runs (failures detailed, successes counted).
	b.WriteString("## Recent Job Runs\n\n")
	runs := queryReviewJobRuns(cfg.HistoryDB, cutoff, 50)
	if len(runs) == 0 {
		b.WriteString("(none in this period)\n\n")
	} else {
		successCount := 0
		var failures []JobRun
		for _, r := range runs {
			if r.Status == "success" {
				successCount++
			} else {
				failures = append(failures, r)
			}
		}
		b.WriteString(fmt.Sprintf("Successful runs: %d\n", successCount))
		if len(failures) == 0 {
			b.WriteString("Failed runs: 0\n\n")
		} else {
			b.WriteString(fmt.Sprintf("Failed runs: %d\n", len(failures)))
			for _, f := range failures {
				errMsg := f.Error
				if len(errMsg) > 200 {
					errMsg = errMsg[:200] + "..."
				}
				b.WriteString(fmt.Sprintf("- [%s] %s (%s): %s — %s\n",
					f.StartedAt[:10], f.Name, f.JobID, f.Status, errMsg))
			}
			b.WriteString("\n")
		}
	}

	// Section 4: Existing Skills.
	b.WriteString("## Existing Skills\n\n")
	fileMetas := loadAllFileSkillMetas(cfg)
	configSkills := cfg.Skills
	if len(fileMetas) == 0 && len(configSkills) == 0 {
		b.WriteString("(none)\n\n")
	} else {
		for _, m := range fileMetas {
			b.WriteString(fmt.Sprintf("- %s: %s\n", m.Name, m.Description))
		}
		for _, s := range configSkills {
			b.WriteString(fmt.Sprintf("- %s (config): %s\n", s.Name, s.Description))
		}
		b.WriteString("\n")
	}

	// Section 5: Memory Status (with priority + last access).
	b.WriteString("## Memory Status\n\n")
	memories, err := listMemory(cfg, "")
	accessLog := loadMemoryAccessLog(cfg)
	now := time.Now()
	if err != nil || len(memories) == 0 {
		b.WriteString("(none)\n\n")
	} else {
		for _, m := range memories {
			lastAccess := "never"
			staleWarning := ""
			if ts, ok := accessLog[m.Key]; ok {
				lastAccess = ts
				if len(lastAccess) > 10 {
					lastAccess = lastAccess[:10]
				}
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					ageDays := int(now.Sub(t).Hours() / 24)
					if ageDays > 30 && m.Priority != "P0" {
						staleWarning = fmt.Sprintf(" — stale (%d days)", ageDays)
					}
				}
			}
			b.WriteString(fmt.Sprintf("- %s (%s) — last accessed: %s%s\n", m.Key, m.Priority, lastAccess, staleWarning))
		}
		b.WriteString("\n")
	}

	// Archived memory count.
	archiveDir := filepath.Join(cfg.WorkspaceDir, "memory", "archive")
	if entries, err := os.ReadDir(archiveDir); err == nil {
		archiveCount := 0
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				archiveCount++
			}
		}
		if archiveCount > 0 {
			b.WriteString(fmt.Sprintf("Archived entries: %d (in memory/archive/)\n\n", archiveCount))
		}
	}

	return b.String()
}

// --- Review Query Helpers ---

// queryReviewMessages returns recent session messages (user/assistant only).
func queryReviewMessages(dbPath, cutoff string, limit int) []SessionMessage {
	sql := fmt.Sprintf(
		`SELECT id, session_id, role, content, cost_usd, tokens_in, tokens_out, model, task_id, created_at
		 FROM session_messages
		 WHERE created_at >= '%s' AND role IN ('user','assistant')
		 ORDER BY created_at DESC LIMIT %d`,
		escapeSQLite(cutoff), limit)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		logWarn("review: query messages failed", "error", err)
		return nil
	}

	var msgs []SessionMessage
	for _, row := range rows {
		msgs = append(msgs, sessionMessageFromRow(row))
	}
	return msgs
}

// queryReviewReflections returns recent reflections.
func queryReviewReflections(dbPath, cutoff string, limit int) []ReflectionResult {
	sql := fmt.Sprintf(
		`SELECT task_id, role, score, feedback, improvement, cost_usd, created_at
		 FROM reflections
		 WHERE created_at >= '%s'
		 ORDER BY created_at DESC LIMIT %d`,
		escapeSQLite(cutoff), limit)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		logWarn("review: query reflections failed", "error", err)
		return nil
	}

	var results []ReflectionResult
	for _, row := range rows {
		results = append(results, ReflectionResult{
			TaskID:      jsonStr(row["task_id"]),
			Role:        jsonStr(row["role"]),
			Score:       jsonInt(row["score"]),
			Feedback:    jsonStr(row["feedback"]),
			Improvement: jsonStr(row["improvement"]),
			CostUSD:     jsonFloat(row["cost_usd"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return results
}

// queryReviewJobRuns returns recent job runs.
func queryReviewJobRuns(dbPath, cutoff string, limit int) []JobRun {
	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(role,'') as role
		 FROM job_runs
		 WHERE started_at >= '%s'
		 ORDER BY started_at DESC LIMIT %d`,
		escapeSQLite(cutoff), limit)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		logWarn("review: query job runs failed", "error", err)
		return nil
	}

	var runs []JobRun
	for _, row := range rows {
		runs = append(runs, jobRunFromRow(row))
	}
	return runs
}
