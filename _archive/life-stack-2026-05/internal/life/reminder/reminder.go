// Package reminder provides smart reminder management with natural language time parsing.
package reminder

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/life/lifedb"
)

// Reminder represents a scheduled reminder.
type Reminder struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	DueAt     string `json:"dueAt"`     // ISO 8601 UTC
	Recurring string `json:"recurring"` // cron expression (empty = one-shot)
	Status    string `json:"status"`    // pending, fired, cancelled
	Channel   string `json:"channel"`
	UserID    string `json:"userId"`
	CreatedAt string `json:"createdAt"` // ISO 8601 UTC
}

// NextCronTimeFn computes the next occurrence of a cron expression after the given time.
// Returns zero time if the expression is invalid or no next time exists.
type NextCronTimeFn func(expr string, after time.Time) time.Time

// Engine manages reminders with a periodic ticker.
type Engine struct {
	db           lifedb.DB
	dbPath       string
	checkInterval time.Duration
	maxPerUser   int
	notifyFn     func(string)
	nextCronTime NextCronTimeFn

	mu     sync.Mutex
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// Config holds engine configuration.
type Config struct {
	CheckInterval time.Duration // default 30s
	MaxPerUser    int           // default 50
}

func (c Config) checkIntervalOrDefault() time.Duration {
	if c.CheckInterval > 0 {
		return c.CheckInterval
	}
	return 30 * time.Second
}

func (c Config) maxPerUserOrDefault() int {
	if c.MaxPerUser > 0 {
		return c.MaxPerUser
	}
	return 50
}

// InitDB creates the reminders table if it does not exist.
func InitDB(dbPath string) error {
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

// New creates a new reminder Engine.
// notifyFn is called with a message string when a reminder fires (may be nil).
// nextCronTime is injected from the root cron package.
func New(dbPath string, cfg Config, db lifedb.DB, notifyFn func(string), nextCronTime NextCronTimeFn) *Engine {
	return &Engine{
		db:            db,
		dbPath:        dbPath,
		checkInterval: cfg.checkIntervalOrDefault(),
		maxPerUser:    cfg.maxPerUserOrDefault(),
		notifyFn:      notifyFn,
		nextCronTime:  nextCronTime,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the periodic reminder check goroutine.
func (re *Engine) Start() {
	re.wg.Add(1)
	go func() {
		defer re.wg.Done()
		ticker := time.NewTicker(re.checkInterval)
		defer ticker.Stop()

		re.db.LogInfo("reminder engine started", "interval", re.checkInterval.String())

		for {
			select {
			case <-re.stopCh:
				return
			case <-ticker.C:
				re.tick()
			}
		}
	}()
}

// Stop halts the reminder engine.
func (re *Engine) Stop() {
	close(re.stopCh)
	re.wg.Wait()
}

// Tick runs one check cycle, firing any due reminders.
func (re *Engine) Tick() { re.tick() }

func (re *Engine) tick() {
	re.mu.Lock()
	defer re.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`SELECT id, text, due_at, recurring, status, channel, user_id, created_at
		 FROM reminders WHERE status = 'pending' AND due_at <= '%s'
		 ORDER BY due_at ASC LIMIT 100`, re.db.Escape(now))

	rows, err := re.db.Query(re.dbPath, sql)
	if err != nil {
		re.db.LogWarn("reminder tick query failed", "error", err)
		return
	}

	for _, row := range rows {
		id := fmt.Sprintf("%v", row["id"])
		text := fmt.Sprintf("%v", row["text"])
		recurring := fmt.Sprintf("%v", row["recurring"])
		channel := fmt.Sprintf("%v", row["channel"])
		userID := fmt.Sprintf("%v", row["user_id"])

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
		re.db.LogInfo("reminder fired", "id", id, "text", text, "channel", channel, "userId", userID)

		if recurring != "" && recurring != "<nil>" && re.nextCronTime != nil {
			nextTime := re.nextCronTime(recurring, time.Now().UTC())
			if !nextTime.IsZero() {
				updateSQL := fmt.Sprintf(
					`UPDATE reminders SET due_at = '%s' WHERE id = '%s'`,
					nextTime.Format(time.RFC3339), re.db.Escape(id))
				execCmd := exec.Command("sqlite3", re.dbPath, updateSQL)
				if out, err := execCmd.CombinedOutput(); err != nil {
					re.db.LogWarn("reminder reschedule failed", "id", id, "error", fmt.Sprintf("%s: %v", string(out), err))
				} else {
					re.db.LogInfo("reminder rescheduled", "id", id, "nextDue", nextTime.Format(time.RFC3339))
				}
				continue
			}
		}

		updateSQL := fmt.Sprintf(
			`UPDATE reminders SET status = 'fired' WHERE id = '%s'`, re.db.Escape(id))
		execCmd := exec.Command("sqlite3", re.dbPath, updateSQL)
		if out, err := execCmd.CombinedOutput(); err != nil {
			re.db.LogWarn("reminder mark fired failed", "id", id, "error", fmt.Sprintf("%s: %v", string(out), err))
		}
	}
}

// Add inserts a new reminder into the database.
func (re *Engine) Add(text string, dueAt time.Time, recurring, channel, userID string) (Reminder, error) {
	re.mu.Lock()
	defer re.mu.Unlock()

	if userID != "" {
		countSQL := fmt.Sprintf(
			`SELECT COUNT(*) as cnt FROM reminders WHERE user_id = '%s' AND status = 'pending'`,
			re.db.Escape(userID))
		rows, err := re.db.Query(re.dbPath, countSQL)
		if err == nil && len(rows) > 0 {
			if cnt, ok := rows[0]["cnt"].(float64); ok && int(cnt) >= re.maxPerUser {
				return Reminder{}, fmt.Errorf("user %s has reached the maximum of %d active reminders", userID, re.maxPerUser)
			}
		}
	}

	id := fmt.Sprintf("rem_%d", time.Now().UnixNano())
	now := time.Now().UTC().Format(time.RFC3339)
	dueStr := dueAt.UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO reminders (id, text, due_at, recurring, status, channel, user_id, created_at)
		 VALUES ('%s', '%s', '%s', '%s', 'pending', '%s', '%s', '%s')`,
		re.db.Escape(id), re.db.Escape(text), re.db.Escape(dueStr),
		re.db.Escape(recurring), re.db.Escape(channel), re.db.Escape(userID), re.db.Escape(now))

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
	re.db.LogInfo("reminder added", "id", id, "text", text, "dueAt", dueStr)
	return r, nil
}

// Cancel sets a reminder's status to cancelled.
func (re *Engine) Cancel(id, userID string) error {
	re.mu.Lock()
	defer re.mu.Unlock()

	if userID != "" {
		checkSQL := fmt.Sprintf(
			`SELECT user_id FROM reminders WHERE id = '%s' AND status = 'pending'`,
			re.db.Escape(id))
		rows, err := re.db.Query(re.dbPath, checkSQL)
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
		re.db.Escape(id))
	cmd := exec.Command("sqlite3", re.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cancel reminder: %s: %w", string(out), err)
	}

	re.db.LogInfo("reminder cancelled", "id", id)
	return nil
}

// List returns pending reminders for a user, or all if userID is empty.
func (re *Engine) List(userID string) ([]Reminder, error) {
	re.mu.Lock()
	defer re.mu.Unlock()

	sql := `SELECT id, text, due_at, recurring, status, channel, user_id, created_at
		FROM reminders WHERE status = 'pending' ORDER BY due_at ASC LIMIT 200`
	if userID != "" {
		sql = fmt.Sprintf(
			`SELECT id, text, due_at, recurring, status, channel, user_id, created_at
			 FROM reminders WHERE status = 'pending' AND user_id = '%s'
			 ORDER BY due_at ASC LIMIT 200`, re.db.Escape(userID))
	}

	rows, err := re.db.Query(re.dbPath, sql)
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

// Snooze pushes a reminder's due_at forward by the given duration.
func (re *Engine) Snooze(id string, duration time.Duration) error {
	re.mu.Lock()
	defer re.mu.Unlock()

	checkSQL := fmt.Sprintf(
		`SELECT due_at FROM reminders WHERE id = '%s' AND status = 'pending'`,
		re.db.Escape(id))
	rows, err := re.db.Query(re.dbPath, checkSQL)
	if err != nil {
		return fmt.Errorf("query reminder: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("reminder %s not found or not pending", id)
	}

	dueStr := fmt.Sprintf("%v", rows[0]["due_at"])
	dueAt, err := time.Parse(time.RFC3339, dueStr)
	if err != nil {
		dueAt, err = time.Parse("2006-01-02T15:04:05Z", dueStr)
		if err != nil {
			dueAt = time.Now().UTC()
		}
	}

	if dueAt.Before(time.Now().UTC()) {
		dueAt = time.Now().UTC()
	}

	newDue := dueAt.Add(duration).UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE reminders SET due_at = '%s' WHERE id = '%s'`,
		re.db.Escape(newDue), re.db.Escape(id))
	cmd := exec.Command("sqlite3", re.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("snooze reminder: %s: %w", string(out), err)
	}

	re.db.LogInfo("reminder snoozed", "id", id, "newDue", newDue, "duration", duration.String())
	return nil
}

// --- Natural Language Time Parser ---

// ParseNaturalTime parses natural language time expressions in Japanese, English, and Chinese.
func ParseNaturalTime(input string) (time.Time, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return time.Time{}, fmt.Errorf("empty time input")
	}
	now := time.Now()

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

	if t, err := parseTimeOnly(input, now); err == nil {
		return t.UTC(), nil
	}
	if t, ok := parseRelativeDuration(input, now); ok {
		return t.UTC(), nil
	}
	if t, ok := parseJapanese(input, now); ok {
		return t.UTC(), nil
	}
	if t, ok := parseChinese(input, now); ok {
		return t.UTC(), nil
	}
	if t, ok := parseEnglish(input, now); ok {
		return t.UTC(), nil
	}

	return time.Time{}, fmt.Errorf("cannot parse time: %q", input)
}

func parseTimeOnly(input string, now time.Time) (time.Time, error) {
	input = strings.ToLower(strings.TrimSpace(input))

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

func parseRelativeDuration(input string, now time.Time) (time.Time, bool) {
	lower := strings.ToLower(input)

	reEn := regexp.MustCompile(`^in\s+(\d+)\s*(sec(?:ond)?s?|min(?:ute)?s?|hours?|days?|weeks?)$`)
	if m := reEn.FindStringSubmatch(lower); m != nil {
		n, _ := strconv.Atoi(m[1])
		d := parseDurationUnit(m[2], n)
		return now.Add(d), true
	}

	reJa := regexp.MustCompile(`^(\d+)\s*(秒|分|時間|日|週間)後$`)
	if m := reJa.FindStringSubmatch(input); m != nil {
		n, _ := strconv.Atoi(m[1])
		d := parseJaDurationUnit(m[2], n)
		return now.Add(d), true
	}

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

func parseJapanese(input string, now time.Time) (time.Time, bool) {
	if strings.HasPrefix(input, "明日") {
		tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		h, m := 9, 0
		rest := strings.TrimPrefix(input, "明日")
		if rest != "" {
			if hh, mm, ok := parseJaTime(rest); ok {
				h, m = hh, mm
			}
		}
		t := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), h, m, 0, 0, now.Location())
		return t, true
	}

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

	if strings.HasPrefix(input, "来週") {
		rest := strings.TrimPrefix(input, "来週")
		dow := parseJaDow(rest)
		if dow >= 0 {
			t := nextWeekday(now, time.Weekday(dow))
			return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, now.Location()), true
		}
	}

	return time.Time{}, false
}

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

func parseJaDow(s string) int {
	s = strings.TrimSpace(s)
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

func parseChinese(input string, now time.Time) (time.Time, bool) {
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

	if strings.HasPrefix(input, "下週") || strings.HasPrefix(input, "下周") {
		rest := input
		if strings.HasPrefix(input, "下週") {
			rest = strings.TrimPrefix(input, "下週")
		} else {
			rest = strings.TrimPrefix(input, "下周")
		}
		dow := parseZhDow(rest)
		if dow >= 0 {
			t := nextWeekday(now, time.Weekday(dow))
			return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, now.Location()), true
		}
	}

	return time.Time{}, false
}

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
			h = 12
		}
		if h >= 0 && h <= 23 && min >= 0 && min <= 59 {
			return h, min, true
		}
	}
	return 0, 0, false
}

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

func parseEnglish(input string, now time.Time) (time.Time, bool) {
	lower := strings.ToLower(strings.TrimSpace(input))

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

	if strings.HasPrefix(lower, "next ") {
		rest := strings.TrimPrefix(lower, "next ")
		dow := parseEnDow(rest)
		if dow >= 0 {
			t := nextWeekday(now, time.Weekday(dow))
			return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, now.Location()), true
		}
	}

	return time.Time{}, false
}

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

func nextWeekday(now time.Time, target time.Weekday) time.Time {
	current := now.Weekday()
	daysAhead := int(target) - int(current)
	if daysAhead <= 0 {
		daysAhead += 7
	}
	return now.AddDate(0, 0, daysAhead)
}
