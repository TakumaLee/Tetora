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

var reDailyGuideDate = regexp.MustCompile(`daily-guide-(\d{4}-\d{2}-\d{2})\.md$`)

// stockTradingReportsDir resolves the stock-trading reports directory.
// Priority:
//  1. STOCK_TRADING_REPORTS_DIR env
//  2. $HOME/Workspace/Projects/01-Personal/stock-trading/reports
func stockTradingReportsDir() string {
	if p := os.Getenv("STOCK_TRADING_REPORTS_DIR"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Workspace", "Projects", "01-Personal", "stock-trading", "reports")
}

// taipeiLoc returns the Asia/Taipei location (falls back to UTC on error).
// Taiwan stock recommendations are pinned to local trading day.
func taipeiLoc() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.UTC
	}
	return loc
}

func updateTaiwanStockManual(ctx context.Context, cfg *config.Config, front json.RawMessage) (map[string]any, error) {
	dir := stockTradingReportsDir()
	if dir == "" {
		return nil, nil
	}

	pattern := filepath.Join(dir, "daily-guide-*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob daily-guide files: %w", err)
	}

	var latestPath string
	var latestDate time.Time
	for _, m := range matches {
		sub := reDailyGuideDate.FindStringSubmatch(filepath.Base(m))
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

	now := time.Now().In(taipeiLoc())
	today := now.Format("2006-01-02")

	// Weekend handling + stale detection when no file exists.
	if latestPath == "" {
		if isWeekend(now) {
			return map[string]any{
				"summary":      "[auto] 週末休市，無交易訊號",
				"status":       "",
				"last_updated": time.Now().UTC().Format(time.RFC3339),
			}, nil
		}
		log.Warn("taiwan-stock-manual autoupdate: no daily-guide files found", "pattern", pattern)
		return nil, nil
	}

	summary := buildDailyGuideSummary(latestPath, latestDate, now)

	// Status rules (Taipei local time):
	//   - Today's file exists → green
	//   - Weekend, last file is most recent weekday → green
	//   - Weekday, today's file missing (script hasn't run yet at 08:25) → yellow
	//   - Weekday, >=3 business days stale → red
	status := "green"
	daysOld := businessDaysBetween(latestDate, now)
	if !isWeekend(now) {
		if daysOld >= 3 {
			status = "red"
		} else if latestDate.Format("2006-01-02") != today {
			status = "yellow"
		}
	}

	updates := map[string]any{
		"summary":       summary,
		"status":        status,
		"last_updated":  time.Now().UTC().Format(time.RFC3339),
		"last_intel_at": latestDate.Format(time.RFC3339),
	}
	return updates, nil
}

func isWeekend(t time.Time) bool {
	wd := t.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// businessDaysBetween returns the number of Mon-Fri days strictly between
// `from` and `to` (not including from or to). Rough approximation — good
// enough for staleness thresholds.
func businessDaysBetween(from, to time.Time) int {
	if !to.After(from) {
		return 0
	}
	days := 0
	for d := from.AddDate(0, 0, 1); d.Before(to); d = d.AddDate(0, 0, 1) {
		if !isWeekend(d) {
			days++
		}
	}
	return days
}

// buildDailyGuideSummary reads the first ~4KB of the file and extracts a
// compact summary: regime line + top 3 stocks (names + scores). Caps output
// at 200 chars.
func buildDailyGuideSummary(path string, fileDate, now time.Time) string {
	f, err := os.Open(path)
	if err != nil {
		return "[auto] 讀取每日指南失敗"
	}
	defer f.Close()

	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	content := string(buf[:n])

	var regime string
	var topStocks []string

	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	reStock := regexp.MustCompile(`#\d+\s+([^（]+)（(\d+)）\s+⭐\s+(\d+)分`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "📈 大盤") {
			regime = strings.TrimPrefix(line, "📈 ")
		}
		if len(topStocks) < 3 {
			if m := reStock.FindStringSubmatch(line); m != nil {
				name := strings.TrimSpace(m[1])
				code := m[2]
				score := m[3]
				topStocks = append(topStocks, fmt.Sprintf("%s(%s) %s分", name, code, score))
			}
		}
	}

	parts := []string{}
	if regime != "" {
		// Keep it short: only the 多/空/盤 token and risk level.
		regimeShort := compressRegime(regime)
		parts = append(parts, regimeShort)
	}
	if len(topStocks) > 0 {
		parts = append(parts, "Top: "+strings.Join(topStocks, "、"))
	}
	if len(parts) == 0 {
		parts = append(parts, "當日指南已生成")
	}

	// Staleness tag when the file isn't today's.
	today := now.Format("2006-01-02")
	if fileDate.Format("2006-01-02") != today && !isWeekend(now) {
		parts = append(parts, "("+fileDate.Format("01/02")+"，今日未更新)")
	}

	summary := "[auto] " + strings.Join(parts, " ｜ ")
	if len(summary) > 200 {
		summary = summary[:200]
	}
	return summary
}

// compressRegime trims "大盤：多 | 風險：低 | S&P500 +1.2% · NASDAQ +1.5%"
// down to "大盤 多｜風險 低".
func compressRegime(line string) string {
	// Strip markdown bold.
	line = strings.ReplaceAll(line, "**", "")
	// Split by "|" and keep first 2 segments.
	parts := strings.Split(line, "|")
	keep := parts
	if len(parts) > 2 {
		keep = parts[:2]
	}
	var cleaned []string
	for _, p := range keep {
		cleaned = append(cleaned, strings.TrimSpace(p))
	}
	return strings.Join(cleaned, "｜")
}
