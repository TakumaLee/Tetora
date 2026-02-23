package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- P17.3a: Daily Notes ---

type DailyNotesConfig struct {
	Enabled  bool   `json:"enabled"`
	Dir      string `json:"dir,omitempty"`      // default "~/.tetora/notes/"
	Schedule string `json:"schedule,omitempty"` // cron expression, default "0 0 * * *"
}

// dirOrDefault returns the configured notes directory (default ~/.tetora/notes/).
func (c DailyNotesConfig) dirOrDefault(baseDir string) string {
	if c.Dir != "" {
		dir := c.Dir
		if strings.HasPrefix(dir, "~/") {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, dir[2:])
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		return dir
	}
	return filepath.Join(baseDir, "notes")
}

// scheduleOrDefault returns the configured cron schedule (default "0 0 * * *" — daily at midnight).
func (c DailyNotesConfig) scheduleOrDefault() string {
	if c.Schedule != "" {
		return c.Schedule
	}
	return "0 0 * * *"
}

// generateDailyNote creates a markdown summary of the previous day's activity.
// Returns the markdown content and an error if query fails.
func generateDailyNote(cfg *Config, date time.Time) (string, error) {
	if cfg.HistoryDB == "" {
		return "", fmt.Errorf("historyDB not configured")
	}

	// Query tasks from the previous day (midnight to midnight).
	startOfDay := date.Format("2006-01-02 00:00:00")
	endOfDay := date.Add(24 * time.Hour).Format("2006-01-02 00:00:00")

	sql := fmt.Sprintf(`
		SELECT id, name, source, role, status, duration_ms, cost_usd, tokens_in, tokens_out, started_at
		FROM history
		WHERE started_at >= '%s' AND started_at < '%s'
		ORDER BY started_at
	`, escapeSQLite(startOfDay), escapeSQLite(endOfDay))

	rows, err := queryDB(cfg.HistoryDB, sql)
	if err != nil {
		return "", fmt.Errorf("query history: %w", err)
	}

	// Build markdown document.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Daily Summary — %s\n\n", date.Format("2006-01-02")))

	if len(rows) == 0 {
		sb.WriteString("No tasks executed on this day.\n")
		return sb.String(), nil
	}

	// Aggregate stats.
	totalCost := 0.0
	totalTokensIn := 0
	totalTokensOut := 0
	successCount := 0
	errorCount := 0
	roleMap := make(map[string]int)
	sourceMap := make(map[string]int)

	for _, row := range rows {
		status := toString(row["status"])
		costUSD := toFloat(row["cost_usd"])
		tokensIn := toInt(row["tokens_in"])
		tokensOut := toInt(row["tokens_out"])
		role := toString(row["role"])
		source := toString(row["source"])

		totalCost += costUSD
		totalTokensIn += tokensIn
		totalTokensOut += tokensOut

		if status == "success" {
			successCount++
		} else {
			errorCount++
		}

		if role != "" {
			roleMap[role]++
		}
		if source != "" {
			sourceMap[source]++
		}
	}

	// Summary section.
	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("- **Total Tasks**: %d\n", len(rows)))
	sb.WriteString(fmt.Sprintf("- **Success**: %d\n", successCount))
	sb.WriteString(fmt.Sprintf("- **Errors**: %d\n", errorCount))
	sb.WriteString(fmt.Sprintf("- **Total Cost**: $%.4f\n", totalCost))
	sb.WriteString(fmt.Sprintf("- **Total Tokens**: %d in / %d out\n\n", totalTokensIn, totalTokensOut))

	// Role breakdown.
	if len(roleMap) > 0 {
		sb.WriteString("## Tasks by Role\n\n")
		for role, count := range roleMap {
			if role == "" {
				role = "(none)"
			}
			sb.WriteString(fmt.Sprintf("- **%s**: %d\n", role, count))
		}
		sb.WriteString("\n")
	}

	// Source breakdown.
	if len(sourceMap) > 0 {
		sb.WriteString("## Tasks by Source\n\n")
		for source, count := range sourceMap {
			if source == "" {
				source = "(unknown)"
			}
			sb.WriteString(fmt.Sprintf("- **%s**: %d\n", source, count))
		}
		sb.WriteString("\n")
	}

	// Recent tasks (last 10).
	sb.WriteString("## Recent Tasks\n\n")
	maxShow := 10
	if len(rows) < maxShow {
		maxShow = len(rows)
	}
	for i := len(rows) - maxShow; i < len(rows); i++ {
		row := rows[i]
		name := toString(row["name"])
		status := toString(row["status"])
		costUSD := toFloat(row["cost_usd"])
		durationMs := toInt(row["duration_ms"])
		startedAt := toString(row["started_at"])
		role := toString(row["role"])

		statusEmoji := "✅"
		if status != "success" {
			statusEmoji = "❌"
		}

		sb.WriteString(fmt.Sprintf("- %s **%s** (role: %s)\n", statusEmoji, name, role))
		sb.WriteString(fmt.Sprintf("  - Started: %s\n", startedAt))
		sb.WriteString(fmt.Sprintf("  - Duration: %dms, Cost: $%.4f\n", durationMs, costUSD))
	}

	return sb.String(), nil
}

// writeDailyNote writes a daily note to disk at the configured directory.
func writeDailyNote(cfg *Config, date time.Time, content string) error {
	if !cfg.DailyNotes.Enabled {
		return nil
	}

	notesDir := cfg.DailyNotes.dirOrDefault(cfg.baseDir)
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		return fmt.Errorf("mkdir notes: %w", err)
	}

	filename := date.Format("2006-01-02") + ".md"
	filePath := filepath.Join(notesDir, filename)

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write note: %w", err)
	}

	logInfo("daily note written", "date", date.Format("2006-01-02"), "path", filePath)
	return nil
}

// registerDailyNotesJob registers the daily notes cron job with the cron engine.
// This should be called at daemon startup after the cron engine is initialized.
func registerDailyNotesJob(ctx context.Context, cfg *Config, cronEngine *CronEngine) {
	if !cfg.DailyNotes.Enabled {
		return
	}

	schedule := cfg.DailyNotes.scheduleOrDefault()
	expr, err := parseCronExpr(schedule)
	if err != nil {
		logWarn("daily notes schedule invalid", "schedule", schedule, "error", err)
		return
	}

	// Add a synthetic cron job for daily notes.
	job := &cronJob{
		CronJobConfig: CronJobConfig{
			ID:      "daily_notes",
			Name:    "Daily Notes Generator",
			Enabled: true,
			Schedule: schedule,
		},
		expr:    expr,
		loc:     time.Local,
		nextRun: nextRunAfter(expr, time.Local, time.Now()),
	}

	cronEngine.mu.Lock()
	cronEngine.jobs = append(cronEngine.jobs, job)
	cronEngine.mu.Unlock()

	logInfo("daily notes job registered", "schedule", schedule)
}

// runDailyNotesJob is the execution handler for the daily notes cron job.
// It generates and writes a note for the previous day.
func runDailyNotesJob(ctx context.Context, cfg *Config) error {
	// Generate note for yesterday.
	yesterday := time.Now().AddDate(0, 0, -1)
	content, err := generateDailyNote(cfg, yesterday)
	if err != nil {
		return fmt.Errorf("generate note: %w", err)
	}

	if err := writeDailyNote(cfg, yesterday, content); err != nil {
		return fmt.Errorf("write note: %w", err)
	}

	return nil
}

// toString safely converts map[string]any value to string.
func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// toFloat safely converts map[string]any value to float64.
func toFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

// toInt safely converts map[string]any value to int.
func toInt(v any) int {
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
	}
	return 0
}
