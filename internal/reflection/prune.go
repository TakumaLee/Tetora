package reflection

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tetora/internal/db"
)

// PruneReport summarises the state-machine transitions applied to
// workspace/memory/auto-lessons.md on a single PruneAutoLessons call.
// Kept counts only unchanged lesson entries — not headers, blank lines, or
// entries that were flipped/removed — so it mirrors "entries still pending
// review" rather than "lines untouched in the file".
type PruneReport struct {
	Path            string `json:"path"`
	DryRun          bool   `json:"dryRun"`
	PendingToStale  int    `json:"pendingToStale"`
	StaleRemoved    int    `json:"staleRemoved"`
	RejectedRemoved int    `json:"rejectedRemoved"`
	Kept            int    `json:"kept"`
}

// PruneThresholds controls when each transition fires. Ages are measured from
// reflections.created_at (task_id lookup); untraceable entries use the
// conservative default age defined by Default.
type PruneThresholds struct {
	PendingStaleAfter   time.Duration
	StaleRemoveAfter    time.Duration
	RejectedRemoveAfter time.Duration
	DefaultAge          time.Duration
}

func DefaultPruneThresholds() PruneThresholds {
	return PruneThresholds{
		PendingStaleAfter:   30 * 24 * time.Hour,
		StaleRemoveAfter:    60 * 24 * time.Hour,
		RejectedRemoveAfter: 90 * 24 * time.Hour,
		DefaultAge:          30 * 24 * time.Hour,
	}
}

var pruneTaskIDRe = regexp.MustCompile(`task=([^\s,)]+)`)

// PruneAutoLessons applies the auto-lesson state machine to
// {workspaceDir}/memory/auto-lessons.md:
//
//	[pending]       → [stale]   after PendingStaleAfter (default 30d)
//	[stale]         → removed   after StaleRemoveAfter (default 60d)
//	[rejected]      → removed   after RejectedRemoveAfter (default 90d)
//	[clustered: X]  → kept (waiting for human promotion)
//	[promoted-*]    → kept (historical evidence)
//
// Age is derived from reflections.created_at keyed by task_id. When the
// reflection row is missing (or dbPath is empty), DefaultAge is used — this
// is deliberately conservative so untraceable orphans flip to stale on the
// first run instead of lingering forever.
//
// In dryRun mode the file is not written; the report reflects what would
// change.
func PruneAutoLessons(workspaceDir, dbPath string, now time.Time, thresholds PruneThresholds, dryRun bool) (PruneReport, error) {
	path := filepath.Join(workspaceDir, "memory", "auto-lessons.md")
	rep := PruneReport{Path: path, DryRun: dryRun}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rep, nil
		}
		return rep, fmt.Errorf("PruneAutoLessons: read: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	taskIDs := map[string]struct{}{}
	for _, l := range lines {
		if m := pruneTaskIDRe.FindStringSubmatch(l); len(m) >= 2 {
			taskIDs[m[1]] = struct{}{}
		}
	}
	ages := make(map[string]time.Duration, len(taskIDs))
	for tid := range taskIDs {
		age, ok := lookupReflectionAge(dbPath, tid, now)
		if !ok {
			age = thresholds.DefaultAge
		}
		ages[tid] = age
	}

	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		isEntry := strings.HasPrefix(trimmed, "- [")
		action := classifyPruneLine(line, ages, thresholds)
		switch action {
		case pruneKeep:
			out = append(out, line)
			if isEntry {
				rep.Kept++
			}
		case pruneFlipStale:
			out = append(out, strings.Replace(line, "[pending]", "[stale]", 1))
			rep.PendingToStale++
		case pruneRemoveStale:
			rep.StaleRemoved++
		case pruneRemoveRejected:
			rep.RejectedRemoved++
		}
	}

	if dryRun {
		return rep, nil
	}
	if rep.PendingToStale+rep.StaleRemoved+rep.RejectedRemoved == 0 {
		return rep, nil
	}

	newContent := strings.Join(out, "\n")
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return rep, fmt.Errorf("PruneAutoLessons: write: %w", err)
	}
	return rep, nil
}

type pruneAction int

const (
	pruneKeep pruneAction = iota
	pruneFlipStale
	pruneRemoveStale
	pruneRemoveRejected
)

func classifyPruneLine(line string, ages map[string]time.Duration, th PruneThresholds) pruneAction {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "- [") {
		return pruneKeep
	}

	age := th.DefaultAge
	if m := pruneTaskIDRe.FindStringSubmatch(line); len(m) >= 2 {
		if a, ok := ages[m[1]]; ok {
			age = a
		}
	}

	switch {
	case strings.Contains(line, "[pending]"):
		if age >= th.PendingStaleAfter {
			return pruneFlipStale
		}
	case strings.Contains(line, "[stale]"):
		if age >= th.StaleRemoveAfter {
			return pruneRemoveStale
		}
	case strings.Contains(line, "[rejected]"):
		if age >= th.RejectedRemoveAfter {
			return pruneRemoveRejected
		}
	}
	return pruneKeep
}

func lookupReflectionAge(dbPath, taskID string, now time.Time) (time.Duration, bool) {
	if dbPath == "" || taskID == "" {
		return 0, false
	}
	rows, err := db.QueryArgs(dbPath,
		`SELECT created_at FROM reflections WHERE task_id = ? ORDER BY id ASC LIMIT 1;`,
		taskID)
	if err != nil || len(rows) == 0 {
		return 0, false
	}
	raw, _ := rows[0]["created_at"].(string)
	if raw == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, false
	}
	return now.Sub(t), true
}
