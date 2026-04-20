package autoupdate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
)

var rePolymarketDate = regexp.MustCompile(`polymarket-health-(\d{4}-\d{2}-\d{2})\.md$`)

func updatePolymarket(ctx context.Context, cfg *config.Config, front json.RawMessage) (map[string]any, error) {
	pattern := filepath.Join(cfg.BaseDir, "workspace/memory/daily/polymarket-health-*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob polymarket health files: %w", err)
	}

	if len(matches) == 0 {
		log.Warn("polymarket autoupdate: no health files found", "pattern", pattern)
		return nil, nil
	}

	// Find latest file by parsed date.
	var latestPath string
	var latestDate time.Time
	for _, m := range matches {
		sub := rePolymarketDate.FindStringSubmatch(filepath.Base(m))
		if sub == nil {
			continue
		}
		t, err := time.Parse("2006-01-02", sub[1])
		if err != nil {
			continue
		}
		if t.After(latestDate) {
			latestDate = t
			latestPath = m
		}
	}

	if latestPath == "" {
		log.Warn("polymarket autoupdate: could not parse date from any file")
		return nil, nil
	}

	// Read up to 4KB.
	f, err := os.Open(latestPath)
	if err != nil {
		return nil, fmt.Errorf("open polymarket health file: %w", err)
	}
	defer f.Close()

	summary := extractPolymarketSummary(f)
	if len(summary) > 200 {
		summary = summary[:200]
	}

	// Staleness.
	daysOld := int(time.Since(latestDate).Hours() / 24)
	var status string
	switch {
	case daysOld > 3:
		status = "red"
	case daysOld > 1:
		status = "yellow"
	}

	updates := map[string]any{
		"summary":       "[auto] " + summary,
		"last_updated":  time.Now().UTC().Format(time.RFC3339),
		"last_intel_at": latestDate.Format(time.RFC3339),
	}
	if status != "" {
		updates["status"] = status
	}
	return updates, nil
}

// extractPolymarketSummary scans the reader for the first ## heading and
// returns the first non-empty non-heading line after it.
// Fallback: first non-empty line after the # heading.
func extractPolymarketSummary(f *os.File) string {
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	content := string(buf[:n])

	scanner := bufio.NewScanner(strings.NewReader(content))

	var afterH1 bool
	var h2Summary string
	var h1Fallback string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") {
			// Found first ## heading; next meaningful line is summary.
			for scanner.Scan() {
				next := strings.TrimSpace(scanner.Text())
				if next == "" || strings.HasPrefix(next, "#") {
					continue
				}
				next = strings.TrimPrefix(next, "- ")
				next = strings.TrimSpace(next)
				h2Summary = next
				break
			}
			if h2Summary != "" {
				return h2Summary
			}
		}

		if strings.HasPrefix(trimmed, "# ") && !afterH1 {
			afterH1 = true
			continue
		}

		if afterH1 && h1Fallback == "" && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			h1Fallback = strings.TrimPrefix(trimmed, "- ")
			h1Fallback = strings.TrimSpace(h1Fallback)
		}
	}

	return h1Fallback
}
