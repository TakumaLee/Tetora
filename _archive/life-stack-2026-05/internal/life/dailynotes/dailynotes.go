// Package dailynotes provides daily activity summary generation and writing.
package dailynotes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/life/lifedb"
)

// Service generates and writes daily note files.
type Service struct {
	db        lifedb.DB
	historyDB string
	notesDir  string
}

// New creates a new daily notes Service.
// historyDB is the path to the history SQLite database.
// notesDir is the directory where daily note files will be written.
func New(historyDB, notesDir string, db lifedb.DB) *Service {
	return &Service{
		db:        db,
		historyDB: historyDB,
		notesDir:  notesDir,
	}
}

// Generate creates a markdown summary of the given day's activity.
func (s *Service) Generate(date time.Time) (string, error) {
	if s.historyDB == "" {
		return "", fmt.Errorf("historyDB not configured")
	}

	startOfDay := date.Format("2006-01-02 00:00:00")
	endOfDay := date.Add(24 * time.Hour).Format("2006-01-02 00:00:00")

	sql := fmt.Sprintf(`
		SELECT id, name, source, agent, status, duration_ms, cost_usd, tokens_in, tokens_out, started_at
		FROM history
		WHERE started_at >= '%s' AND started_at < '%s'
		ORDER BY started_at
	`, s.db.Escape(startOfDay), s.db.Escape(endOfDay))

	rows, err := s.db.Query(s.historyDB, sql)
	if err != nil {
		return "", fmt.Errorf("query history: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Daily Summary — %s\n\n", date.Format("2006-01-02")))

	if len(rows) == 0 {
		sb.WriteString("No tasks executed on this day.\n")
		return sb.String(), nil
	}

	totalCost := 0.0
	totalTokensIn := 0
	totalTokensOut := 0
	successCount := 0
	errorCount := 0
	roleMap := make(map[string]int)
	sourceMap := make(map[string]int)

	for _, row := range rows {
		status := strVal(row["status"])
		costUSD := floatVal(row["cost_usd"])
		tokensIn := intVal(row["tokens_in"])
		tokensOut := intVal(row["tokens_out"])
		role := strVal(row["agent"])
		source := strVal(row["source"])

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

	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("- **Total Tasks**: %d\n", len(rows)))
	sb.WriteString(fmt.Sprintf("- **Success**: %d\n", successCount))
	sb.WriteString(fmt.Sprintf("- **Errors**: %d\n", errorCount))
	sb.WriteString(fmt.Sprintf("- **Total Cost**: $%.4f\n", totalCost))
	sb.WriteString(fmt.Sprintf("- **Total Tokens**: %d in / %d out\n\n", totalTokensIn, totalTokensOut))

	if len(roleMap) > 0 {
		sb.WriteString("## Tasks by Agent\n\n")
		for role, count := range roleMap {
			if role == "" {
				role = "(none)"
			}
			sb.WriteString(fmt.Sprintf("- **%s**: %d\n", role, count))
		}
		sb.WriteString("\n")
	}

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

	sb.WriteString("## Recent Tasks\n\n")
	maxShow := 10
	if len(rows) < maxShow {
		maxShow = len(rows)
	}
	for i := len(rows) - maxShow; i < len(rows); i++ {
		row := rows[i]
		name := strVal(row["name"])
		status := strVal(row["status"])
		costUSD := floatVal(row["cost_usd"])
		durationMs := intVal(row["duration_ms"])
		startedAt := strVal(row["started_at"])
		role := strVal(row["agent"])

		statusEmoji := "✅"
		if status != "success" {
			statusEmoji = "❌"
		}

		sb.WriteString(fmt.Sprintf("- %s **%s** (agent: %s)\n", statusEmoji, name, role))
		sb.WriteString(fmt.Sprintf("  - Started: %s\n", startedAt))
		sb.WriteString(fmt.Sprintf("  - Duration: %dms, Cost: $%.4f\n", durationMs, costUSD))
	}

	return sb.String(), nil
}

// Write writes a daily note to disk.
func (s *Service) Write(date time.Time, content string) error {
	if err := os.MkdirAll(s.notesDir, 0o755); err != nil {
		return fmt.Errorf("mkdir notes: %w", err)
	}

	filename := date.Format("2006-01-02") + ".md"
	filePath := filepath.Join(s.notesDir, filename)

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write note: %w", err)
	}

	s.db.LogInfo("daily note written", "date", date.Format("2006-01-02"), "path", filePath)
	return nil
}

// RunForYesterday generates and writes a note for the previous day.
func (s *Service) RunForYesterday() error {
	yesterday := time.Now().AddDate(0, 0, -1)
	content, err := s.Generate(yesterday)
	if err != nil {
		return fmt.Errorf("generate note: %w", err)
	}
	return s.Write(yesterday, content)
}

// NotesDir returns the configured notes directory.
func (s *Service) NotesDir() string {
	return s.notesDir
}

// NotesDirFromConfig resolves the notes directory from config values.
// dir is the configured dir (may be relative, "~/..." or absolute).
// baseDir is the application base directory.
func NotesDirFromConfig(dir, baseDir string) string {
	if dir != "" {
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

// --- local helpers ---

func strVal(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func floatVal(v any) float64 {
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

func intVal(v any) int {
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
