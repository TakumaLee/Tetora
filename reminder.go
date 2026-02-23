package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- P19.3: Smart Reminders ---

// ReminderConfig configures the reminder engine.
type ReminderConfig struct {
	Enabled       bool   `json:"enabled,omitempty"`
	CheckInterval string `json:"checkInterval,omitempty"` // default "30s"
	MaxPerUser    int    `json:"maxPerUser,omitempty"`     // default 50
}

func (rc ReminderConfig) checkIntervalOrDefault() time.Duration {
	if rc.CheckInterval != "" {
		if d, err := time.ParseDuration(rc.CheckInterval); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func (rc ReminderConfig) maxPerUserOrDefault() int {
	if rc.MaxPerUser > 0 {
		return rc.MaxPerUser
	}
	return 50
}

// Reminder represents a scheduled reminder.
type Reminder struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	DueAt     string `json:"dueAt"`     // ISO 8601 UTC
	Recurring string `json:"recurring"` // cron expression (empty = one-shot)
	Status    string `json:"status"`    // pending, fired, cancelled
	Channel   string `json:"channel"`   // source channel (telegram, slack, api, etc.)
	UserID    string `json:"userId"`
	CreatedAt string `json:"createdAt"` // ISO 8601 UTC
}

// ReminderEngine manages reminders with a periodic ticker.
type ReminderEngine struct {
	cfg      *Config
	notifyFn func(string)
	dbPath   string

	mu     sync.Mutex
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// initReminderDB creates the reminders table if it does not exist.
func initReminderDB(dbPath string) error {
	sql := `CREATE TABLE IF NOT EXISTS reminders (
		id TEXT PRIMARY KEY,
		text TEXT NOT NULL,
		due_at TEXT NOT NULL,
		recurring TEXT DEFAULT '',
		status TEXT DEFAULT 'pending',
		channel TEXT DEFAULT '',
		user_id TEXT DEFAULT '',
		created_at TEXT NOT NULL
	);`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init reminders table: %s: %w", string(out), err)
	}
	return nil
}

// newReminderEngine creates a new ReminderEngine.
func newReminderEngine(cfg *Config, notifyFn func(string)) *ReminderEngine {
	return &ReminderEngine{
		cfg:      cfg,
		notifyFn: notifyFn,
		dbPath:   cfg.HistoryDB,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the periodic reminder check goroutine.
func (re *ReminderEngine) Start(ctx context.Context) {
	interval := re.cfg.Reminders.checkIntervalOrDefault()
	re.wg.Add(1)
	go func() {
		defer re.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		logInfo("reminder engine started", "interval", interval.String())

		for {
			select {
			case <-ctx.Done():
				return
			case <-re.stopCh:
				return
			case <-ticker.C:
				re.tick()
			}
		}
	}()
}

// Stop halts the reminder engine.
func (re *ReminderEngine) Stop() {
	close(re.stopCh)
	re.wg.Wait()
}

// tick checks for due reminders and fires them.
func (re *ReminderEngine) tick() {
	re.mu.Lock()
	defer re.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`SELECT id, text, due_at, recurring, status, channel, user_id, created_at
		 FROM reminders WHERE status = 'pending' AND due_at <= '%s'
		 ORDER BY due_at ASC LIMIT 100`, escapeSQLite(now))

	rows, err := queryDB(re.dbPath, sql)
	if err != nil {
		logWarn("reminder tick query failed", "error", err)
		return
	}

	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		text := fmt.Sprintf("%v", row["text"])
		recurring := fmt.Sprintf("%v", row["recurring"])
		channel := fmt.Sprintf("%v", row["channel"])
		userID := fmt.Sprintf("%v", row["user_id"])

		// Fire notification.
		msg := fmt.Sprintf("[Reminder] %s", text)
		if userID != "" {
			msg = fmt.Sprintf("[Reminder for %s] %s", userID, text)
		}
		if channel != "" {
			msg = fmt.Sprintf("[Reminder via %s] %s", channel, text)
		}

		if re.notifyFn != nil {
			re.notifyFn(msg)
		}
		logInfo("reminder fired", "id", id, "text", text, "channel", channel, "userId", userID)

		// Reschedule if recurring, otherwise mark as fired.
		if recurring != "" && recurring != "<nil>" {
			nextTime := nextCronTime(recurring, time.Now().UTC())
			if !nextTime.IsZero() {
				updateSQL := fmt.Sprintf(
					`UPDATE reminders SET due_at = '%s' WHERE id = '%s'`,
					nextTime.Format(time.RFC3339), escapeSQLite(id))
				execCmd := exec.Command("sqlite3", re.dbPath, updateSQL)
				if out, err := execCmd.CombinedOutput(); err != nil {
					logWarn("reminder reschedule failed", "id", id, "error", fmt.Sprintf("%s: %v", string(out), err))
				} else {
					logInfo("reminder rescheduled", "id", id, "nextDue", nextTime.Format(time.RFC3339))
				}
				continue
			}
		}

		// Mark as fired.
		updateSQL := fmt.Sprintf(
			`UPDATE reminders SET status = 'fired' WHERE id = '%s'`, escapeSQLite(id))
		execCmd := exec.Command("sqlite3", re.dbPath, updateSQL)
		if out, err := execCmd.CombinedOutput(); err != nil {
			logWarn("reminder mark fired failed", "id", id, "error", fmt.Sprintf("%s: %v", string(out), err))
		}
	}
}

// addReminder inserts a new reminder into the database.
func (re *ReminderEngine) addReminder(text string, dueAt time.Time, recurring, channel, userID string) (Reminder, error) {
	re.mu.Lock()
	defer re.mu.Unlock()

	// Check per-user limit.
	if userID != "" {
		countSQL := fmt.Sprintf(
			`SELECT COUNT(*) as cnt FROM reminders WHERE user_id = '%s' AND status = 'pending'`,
			escapeSQLite(userID))
		rows, err := queryDB(re.dbPath, countSQL)
		if err == nil && len(rows) > 0 {
			if cnt, ok := rows[0]["cnt"].(float64); ok && int(cnt) >= re.cfg.Reminders.maxPerUserOrDefault() {
				return Reminder{}, fmt.Errorf("user %s has reached the maximum of %d active reminders", userID, re.cfg.Reminders.maxPerUserOrDefault())
			}
		}
	}

	id := fmt.Sprintf("rem_%d", time.Now().UnixNano())
	now := time.Now().UTC().Format(time.RFC3339)
	dueStr := dueAt.UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO reminders (id, text, due_at, recurring, status, channel, user_id, created_at)
		 VALUES ('%s', '%s', '%s', '%s', 'pending', '%s', '%s', '%s')`,
		escapeSQLite(id), escapeSQLite(text), escapeSQLite(dueStr),
		escapeSQLite(recurring), escapeSQLite(channel), escapeSQLite(userID), escapeSQLite(now))

	cmd := exec.Command("sqlite3", re.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Reminder{}, fmt.Errorf("insert reminder: %s: %w", string(out), err)
	}

	r := Reminder{
		ID:        id,
		Text:      text,
		DueAt:     dueStr,
		Recurring: recurring,
		Status:    "pending",
		Channel:   channel,
		UserID:    userID,
		CreatedAt: now,
	}
	logInfo("reminder added", "id", id, "text", text, "dueAt", dueStr)
	return r, nil
}

// cancelReminder sets a reminder's status to cancelled.
func (re *ReminderEngine) cancelReminder(id, userID string) error {
	re.mu.Lock()
	defer re.mu.Unlock()

	// Verify ownership if userID is provided.
	if userID != "" {
		checkSQL := fmt.Sprintf(
			`SELECT user_id FROM reminders WHERE id = '%s' AND status = 'pending'`,
			escapeSQLite(id))
		rows, err := queryDB(re.dbPath, checkSQL)
		if err != nil {
			return fmt.Errorf("check reminder: %w", err)
		}
		if len(rows) == 0 {
			return fmt.Errorf("reminder %s not found or already completed", id)
		}
		owner := fmt.Sprintf("%v", rows[0]["user_id"])
		if owner != userID && owner != "<nil>" && owner != "" {
			return fmt.Errorf("reminder %s does not belong to user %s", id, userID)
		}
	}

	sql := fmt.Sprintf(
		`UPDATE reminders SET status = 'cancelled' WHERE id = '%s' AND status = 'pending'`,
		escapeSQLite(id))
	cmd := exec.Command("sqlite3", re.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cancel reminder: %s: %w", string(out), err)
	}

	logInfo("reminder cancelled", "id", id)
	return nil
}

// listReminders returns active (pending) reminders for a user, or all if userID is empty.
func (re *ReminderEngine) listReminders(userID string) ([]Reminder, error) {
	re.mu.Lock()
	defer re.mu.Unlock()

	sql := `SELECT id, text, due_at, recurring, status, channel, user_id, created_at
		FROM reminders WHERE status = 'pending' ORDER BY due_at ASC LIMIT 200`
	if userID != "" {
		sql = fmt.Sprintf(
			`SELECT id, text, due_at, recurring, status, channel, user_id, created_at
			 FROM reminders WHERE status = 'pending' AND user_id = '%s'
			 ORDER BY due_at ASC LIMIT 200`, escapeSQLite(userID))
	}

	rows, err := queryDB(re.dbPath, sql)
	if err != nil {
		return nil, err
	}

	var reminders []Reminder
	for _, row := range rows {
		reminders = append(reminders, Reminder{
			ID:        fmt.Sprintf("%v", row["id"]),
			Text:      fmt.Sprintf("%v", row["text"]),
			DueAt:     fmt.Sprintf("%v", row["due_at"]),
			Recurring: fmt.Sprintf("%v", row["recurring"]),
			Status:    fmt.Sprintf("%v", row["status"]),
			Channel:   fmt.Sprintf("%v", row["channel"]),
			UserID:    fmt.Sprintf("%v", row["user_id"]),
			CreatedAt: fmt.Sprintf("%v", row["created_at"]),
		})
	}
	return reminders, nil
}

// snoozeReminder pushes a reminder's due_at forward by the given duration.
func (re *ReminderEngine) snoozeReminder(id string, duration time.Duration) error {
	re.mu.Lock()
	defer re.mu.Unlock()

	// Get current due_at.
	checkSQL := fmt.Sprintf(
		`SELECT due_at FROM reminders WHERE id = '%s' AND status = 'pending'`,
		escapeSQLite(id))
	rows, err := queryDB(re.dbPath, checkSQL)
	if err != nil {
		return fmt.Errorf("query reminder: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("reminder %s not found or not pending", id)
	}

	dueStr := fmt.Sprintf("%v", rows[0]["due_at"])
	dueAt, err := time.Parse(time.RFC3339, dueStr)
	if err != nil {
		// Try alternative formats.
		dueAt, err = time.Parse("2006-01-02T15:04:05Z", dueStr)
		if err != nil {
			dueAt = time.Now().UTC()
		}
	}

	// If the due_at is in the past, snooze from now.
	if dueAt.Before(time.Now().UTC()) {
		dueAt = time.Now().UTC()
	}

	newDue := dueAt.Add(duration).UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE reminders SET due_at = '%s' WHERE id = '%s'`,
		escapeSQLite(newDue), escapeSQLite(id))
	cmd := exec.Command("sqlite3", re.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("snooze reminder: %s: %w", string(out), err)
	}

	logInfo("reminder snoozed", "id", id, "newDue", newDue, "duration", duration.String())
	return nil
}

// --- Natural Language Time Parser ---

// parseNaturalTime parses natural language time expressions in Japanese, English, and Chinese.
func parseNaturalTime(input string) (time.Time, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return time.Time{}, fmt.Errorf("empty time input")
	}
	now := time.Now()

	// Try absolute ISO 8601 first: "2024-01-15 14:00" or "2024-01-15T14:00:00Z"
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, input); err == nil {
			if t.Year() == 0 {
				t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
			}
			return t.UTC(), nil
		}
	}

	// Try time-only: "15:30" or "3:30pm"
	if t, err := parseTimeOnly(input, now); err == nil {
		return t.UTC(), nil
	}

	// Try relative durations.
	if t, ok := parseRelativeDuration(input, now); ok {
		return t.UTC(), nil
	}

	// Try Japanese patterns.
	if t, ok := parseJapanese(input, now); ok {
		return t.UTC(), nil
	}

	// Try Chinese patterns.
	if t, ok := parseChinese(input, now); ok {
		return t.UTC(), nil
	}

	// Try English patterns.
	if t, ok := parseEnglish(input, now); ok {
		return t.UTC(), nil
	}

	return time.Time{}, fmt.Errorf("cannot parse time: %q", input)
}

// parseTimeOnly parses "15:30" or "3:30pm" style times.
func parseTimeOnly(input string, now time.Time) (time.Time, error) {
	input = strings.ToLower(strings.TrimSpace(input))

	// 24h format: "15:30"
	re24 := regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	if m := re24.FindStringSubmatch(input); m != nil {
		h, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		if h >= 0 && h <= 23 && min >= 0 && min <= 59 {
			t := time.Date(now.Year(), now.Month(), now.Day(), h, min, 0, 0, now.Location())
			if t.Before(now) {
				t = t.Add(24 * time.Hour)
			}
			return t, nil
		}
	}

	// 12h format: "3:30pm", "3pm"
	re12 := regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*(am|pm)$`)
	if m := re12.FindStringSubmatch(input); m != nil {
		h, _ := strconv.Atoi(m[1])
		min := 0
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		if m[3] == "pm" && h != 12 {
			h += 12
		} else if m[3] == "am" && h == 12 {
			h = 0
		}
		if h >= 0 && h <= 23 && min >= 0 && min <= 59 {
			t := time.Date(now.Year(), now.Month(), now.Day(), h, min, 0, 0, now.Location())
			if t.Before(now) {
				t = t.Add(24 * time.Hour)
			}
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("not a time-only format")
}

// parseRelativeDuration handles "in 5 min", "5分後", "5分鐘後", "30秒後", "in 1 hour" etc.
func parseRelativeDuration(input string, now time.Time) (time.Time, bool) {
	lower := strings.ToLower(input)

	// English: "in X min/minute(s)/hour(s)/day(s)/sec(s)/second(s)"
	reEn := regexp.MustCompile(`^in\s+(\d+)\s*(sec(?:ond)?s?|min(?:ute)?s?|hours?|days?|weeks?)$`)
	if m := reEn.FindStringSubmatch(lower); m != nil {
		n, _ := strconv.Atoi(m[1])
		d := parseDurationUnit(m[2], n)
		return now.Add(d), true
	}

	// Japanese: "N秒後", "N分後", "N時間後", "N日後", "N週間後"
	reJa := regexp.MustCompile(`^(\d+)\s*(秒|分|時間|日|週間)後$`)
	if m := reJa.FindStringSubmatch(input); m != nil {
		n, _ := strconv.Atoi(m[1])
		d := parseJaDurationUnit(m[2], n)
		return now.Add(d), true
	}

	// Chinese: "N秒後", "N分鐘後", "N小時後", "N天後", "N週後"
	reZh := regexp.MustCompile(`^(\d+)\s*(秒|分鐘|小時|天|週)後$`)
	if m := reZh.FindStringSubmatch(input); m != nil {
		n, _ := strconv.Atoi(m[1])
		d := parseZhDurationUnit(m[2], n)
		return now.Add(d), true
	}

	return time.Time{}, false
}

func parseDurationUnit(unit string, n int) time.Duration {
	switch {
	case strings.HasPrefix(unit, "sec"):
		return time.Duration(n) * time.Second
	case strings.HasPrefix(unit, "min"):
		return time.Duration(n) * time.Minute
	case strings.HasPrefix(unit, "hour"):
		return time.Duration(n) * time.Hour
	case strings.HasPrefix(unit, "day"):
		return time.Duration(n) * 24 * time.Hour
	case strings.HasPrefix(unit, "week"):
		return time.Duration(n) * 7 * 24 * time.Hour
	}
	return time.Duration(n) * time.Minute
}

func parseJaDurationUnit(unit string, n int) time.Duration {
	switch unit {
	case "秒":
		return time.Duration(n) * time.Second
	case "分":
		return time.Duration(n) * time.Minute
	case "時間":
		return time.Duration(n) * time.Hour
	case "日":
		return time.Duration(n) * 24 * time.Hour
	case "週間":
		return time.Duration(n) * 7 * 24 * time.Hour
	}
	return time.Duration(n) * time.Minute
}

func parseZhDurationUnit(unit string, n int) time.Duration {
	switch unit {
	case "秒":
		return time.Duration(n) * time.Second
	case "分鐘":
		return time.Duration(n) * time.Minute
	case "小時":
		return time.Duration(n) * time.Hour
	case "天":
		return time.Duration(n) * 24 * time.Hour
	case "週":
		return time.Duration(n) * 7 * 24 * time.Hour
	}
	return time.Duration(n) * time.Minute
}

// parseJapanese handles Japanese date expressions.
func parseJapanese(input string, now time.Time) (time.Time, bool) {
	// "明日" / "明日N時"
	if strings.HasPrefix(input, "明日") {
		tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		h, m := 9, 0 // default 9:00 AM
		rest := strings.TrimPrefix(input, "明日")
		if rest != "" {
			if hh, mm, ok := parseJaTime(rest); ok {
				h, m = hh, mm
			}
		}
		t := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), h, m, 0, 0, now.Location())
		return t, true
	}

	// "今日N時"
	if strings.HasPrefix(input, "今日") {
		rest := strings.TrimPrefix(input, "今日")
		h, m := 9, 0
		if rest != "" {
			if hh, mm, ok := parseJaTime(rest); ok {
				h, m = hh, mm
			}
		}
		t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
		if t.Before(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t, true
	}

	// "来週月曜" etc.
	if strings.HasPrefix(input, "来週") {
		rest := strings.TrimPrefix(input, "来週")
		dow := parseJaDow(rest)
		if dow >= 0 {
			t := nextWeekday(now, time.Weekday(dow), true)
			return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, now.Location()), true
		}
	}

	return time.Time{}, false
}

// parseJaTime parses "N時", "N時M分" from Japanese time suffix.
func parseJaTime(s string) (int, int, bool) {
	re := regexp.MustCompile(`^(\d{1,2})時(?:(\d{1,2})分)?$`)
	if m := re.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		min := 0
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		if h >= 0 && h <= 23 && min >= 0 && min <= 59 {
			return h, min, true
		}
	}
	return 0, 0, false
}

// parseJaDow parses Japanese day-of-week names.
func parseJaDow(s string) int {
	s = strings.TrimSpace(s)
	// Remove trailing 曜 or 曜日
	s = strings.TrimSuffix(s, "曜日")
	s = strings.TrimSuffix(s, "曜")
	switch s {
	case "日":
		return 0
	case "月":
		return 1
	case "火":
		return 2
	case "水":
		return 3
	case "木":
		return 4
	case "金":
		return 5
	case "土":
		return 6
	}
	return -1
}

// parseChinese handles Chinese date expressions.
func parseChinese(input string, now time.Time) (time.Time, bool) {
	// "明天" / "明天下午N點"
	if strings.HasPrefix(input, "明天") {
		tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		h, m := 9, 0
		rest := strings.TrimPrefix(input, "明天")
		if rest != "" {
			if hh, mm, ok := parseZhTime(rest); ok {
				h, m = hh, mm
			}
		}
		t := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), h, m, 0, 0, now.Location())
		return t, true
	}

	// "下週一" etc.
	if strings.HasPrefix(input, "下週") || strings.HasPrefix(input, "下周") {
		rest := input
		if strings.HasPrefix(input, "下週") {
			rest = strings.TrimPrefix(input, "下週")
		} else {
			rest = strings.TrimPrefix(input, "下周")
		}
		dow := parseZhDow(rest)
		if dow >= 0 {
			t := nextWeekday(now, time.Weekday(dow), true)
			return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, now.Location()), true
		}
	}

	return time.Time{}, false
}

// parseZhTime parses Chinese time expressions like "下午3點", "上午10點30分".
func parseZhTime(s string) (int, int, bool) {
	s = strings.TrimSpace(s)
	offset := 0
	if strings.HasPrefix(s, "下午") {
		offset = 12
		s = strings.TrimPrefix(s, "下午")
	} else if strings.HasPrefix(s, "上午") {
		s = strings.TrimPrefix(s, "上午")
	}

	re := regexp.MustCompile(`^(\d{1,2})點(?:(\d{1,2})分)?$`)
	if m := re.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		min := 0
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		h += offset
		if h == 24 {
			h = 12 // 下午12點 = noon
		}
		if h >= 0 && h <= 23 && min >= 0 && min <= 59 {
			return h, min, true
		}
	}
	return 0, 0, false
}

// parseZhDow parses Chinese day-of-week.
func parseZhDow(s string) int {
	switch s {
	case "日", "天":
		return 0
	case "一":
		return 1
	case "二":
		return 2
	case "三":
		return 3
	case "四":
		return 4
	case "五":
		return 5
	case "六":
		return 6
	}
	return -1
}

// parseEnglish handles English date expressions.
func parseEnglish(input string, now time.Time) (time.Time, bool) {
	lower := strings.ToLower(strings.TrimSpace(input))

	// "tomorrow", "tomorrow 3pm", "tomorrow 15:00"
	if strings.HasPrefix(lower, "tomorrow") {
		tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		h, m := 9, 0
		rest := strings.TrimSpace(strings.TrimPrefix(lower, "tomorrow"))
		if rest != "" {
			if t, err := parseTimeOnly(rest, tomorrow); err == nil {
				return t, true
			}
		}
		t := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), h, m, 0, 0, now.Location())
		return t, true
	}

	// "next monday", "next tuesday", etc.
	if strings.HasPrefix(lower, "next ") {
		rest := strings.TrimPrefix(lower, "next ")
		dow := parseEnDow(rest)
		if dow >= 0 {
			t := nextWeekday(now, time.Weekday(dow), true)
			return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, now.Location()), true
		}
	}

	return time.Time{}, false
}

// parseEnDow parses English day-of-week names.
func parseEnDow(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	switch {
	case strings.HasPrefix(s, "sun"):
		return 0
	case strings.HasPrefix(s, "mon"):
		return 1
	case strings.HasPrefix(s, "tue"):
		return 2
	case strings.HasPrefix(s, "wed"):
		return 3
	case strings.HasPrefix(s, "thu"):
		return 4
	case strings.HasPrefix(s, "fri"):
		return 5
	case strings.HasPrefix(s, "sat"):
		return 6
	}
	return -1
}

// nextWeekday returns the next occurrence of the given weekday.
// If nextWeek is true, it skips to the next week (7+ days ahead).
func nextWeekday(now time.Time, target time.Weekday, nextWeek bool) time.Time {
	current := now.Weekday()
	daysAhead := int(target) - int(current)
	if nextWeek {
		// Always go to next week.
		if daysAhead <= 0 {
			daysAhead += 7
		}
		daysAhead += 7 // next week, not this week
		// But if the target is ahead of today in this week, just add 7 from today.
		// Simpler: always use 7 + offset approach.
		daysAhead = int(target) - int(current)
		if daysAhead <= 0 {
			daysAhead += 7
		}
		// We want "next week's X", so add 7.
		daysAhead += 7 - 7 // Actually "next week" means the coming occurrence after this week.
		// Re-think: "next monday" should be the monday of next week.
		// If today is Wednesday, "next monday" = monday in 5 days (this coming monday).
		// Actually typical interpretation: next {day} = the next occurrence.
		// Let's keep it simple: find the next occurrence that is >= 1 day away.
		daysAhead = int(target) - int(current)
		if daysAhead <= 0 {
			daysAhead += 7
		}
	} else {
		if daysAhead <= 0 {
			daysAhead += 7
		}
	}
	return now.AddDate(0, 0, daysAhead)
}

// --- Cron-based Next Time ---

// nextCronTime computes the next occurrence of a cron expression after the given time.
// Reuses parseCronExpr and nextRunAfter from cron.go.
func nextCronTime(expr string, after time.Time) time.Time {
	parsed, err := parseCronExpr(expr)
	if err != nil {
		logWarn("reminder bad cron expr", "expr", expr, "error", err)
		return time.Time{}
	}
	return nextRunAfter(parsed, time.UTC, after)
}

// --- Tool Handlers for Reminders ---

// Global reminder engine reference (set in main.go).
var globalReminderEngine *ReminderEngine

func toolReminderSet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Text      string `json:"text"`
		Time      string `json:"time"`
		Recurring string `json:"recurring"`
		Channel   string `json:"channel"`
		UserID    string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if args.Time == "" {
		return "", fmt.Errorf("time is required")
	}

	if globalReminderEngine == nil {
		return "", fmt.Errorf("reminder engine not initialized (enable reminders in config)")
	}

	dueAt, err := parseNaturalTime(args.Time)
	if err != nil {
		return "", fmt.Errorf("parse time %q: %w", args.Time, err)
	}

	// Validate recurring expression if provided.
	if args.Recurring != "" {
		if _, err := parseCronExpr(args.Recurring); err != nil {
			return "", fmt.Errorf("invalid recurring cron expression %q: %w", args.Recurring, err)
		}
	}

	rem, err := globalReminderEngine.addReminder(args.Text, dueAt, args.Recurring, args.Channel, args.UserID)
	if err != nil {
		return "", err
	}

	out, _ := json.Marshal(rem)
	return string(out), nil
}

func toolReminderList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID string `json:"user_id"`
	}
	json.Unmarshal(input, &args)

	if globalReminderEngine == nil {
		return "", fmt.Errorf("reminder engine not initialized")
	}

	reminders, err := globalReminderEngine.listReminders(args.UserID)
	if err != nil {
		return "", err
	}
	if reminders == nil {
		reminders = []Reminder{}
	}

	out, _ := json.Marshal(map[string]any{
		"reminders": reminders,
		"count":     len(reminders),
	})
	return string(out), nil
}

func toolReminderCancel(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	if globalReminderEngine == nil {
		return "", fmt.Errorf("reminder engine not initialized")
	}

	if err := globalReminderEngine.cancelReminder(args.ID, args.UserID); err != nil {
		return "", err
	}

	return fmt.Sprintf(`{"ok":true,"id":"%s","status":"cancelled"}`, args.ID), nil
}
