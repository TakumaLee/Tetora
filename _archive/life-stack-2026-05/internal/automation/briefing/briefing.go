package briefing

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// QueryFn executes a SQL query against a SQLite database at dbPath and returns
// rows as a slice of string-keyed maps.
type QueryFn func(dbPath, sql string) ([]map[string]any, error)

// EscapeFn escapes a string value for safe inclusion in a SQLite query.
type EscapeFn func(s string) string

// ScheduleEvent is a minimal representation of a single calendar event.
type ScheduleEvent struct {
	Start time.Time
	Title string
}

// ScheduleDay is a minimal representation of a day's schedule.
type ScheduleDay struct {
	Events []ScheduleEvent
}

// Deps holds injected dependencies for the briefing service.
type Deps struct {
	Query  QueryFn
	Escape EscapeFn

	// Optional service methods. Nil = service not available.
	ViewSchedule      func(dateStr string, days int) ([]ScheduleDay, error)
	GetUpcomingEvents func(days int) ([]map[string]any, error)

	// Service availability flags (for nil-check guards).
	TasksAvailable   bool
	HabitsAvailable  bool
	GoalsAvailable   bool
	FinanceAvailable bool
}

// Service generates morning and evening summaries by aggregating data from
// multiple services.
type Service struct {
	dbPath string
	deps   Deps
}

// New constructs a Service with the given database path and injected dependencies.
func New(dbPath string, deps Deps) *Service {
	return &Service{dbPath: dbPath, deps: deps}
}

// DBPath returns the database path used by the service.
func (s *Service) DBPath() string { return s.dbPath }

// BriefingSection represents one section of a briefing.
type BriefingSection struct {
	Title   string   `json:"title"`
	Icon    string   `json:"icon"`
	Items   []string `json:"items"`
	Summary string   `json:"summary,omitempty"`
}

// Briefing is the full morning or evening briefing.
type Briefing struct {
	Type        string            `json:"type"` // "morning" or "evening"
	Date        string            `json:"date"`
	Greeting    string            `json:"greeting"`
	Sections    []BriefingSection `json:"sections"`
	Quote       string            `json:"quote,omitempty"`
	GeneratedAt string            `json:"generated_at"`
}

// --- Public API ---

// GenerateMorning creates a morning briefing for the given date.
func (b *Service) GenerateMorning(date time.Time) (*Briefing, error) {
	dateStr := date.Format("2006-01-02")
	briefing := &Briefing{
		Type:        "morning",
		Date:        dateStr,
		Greeting:    b.MorningGreeting(date),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// 1. Today's schedule
	if sec := b.ScheduleSection(dateStr); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 2. Reminders due today
	if sec := b.RemindersSection(dateStr); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 3. Tasks due today
	if sec := b.TasksSection(dateStr); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 4. Habits to complete today
	if sec := b.HabitsSection(dateStr, date.Weekday()); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 5. Goal deadlines approaching
	if sec := b.GoalsSection(dateStr); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 6. Upcoming birthdays / contact events
	if sec := b.ContactsSection(); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 7. Motivational quote
	briefing.Quote = b.DailyQuote(date)

	return briefing, nil
}

// GenerateEvening creates an evening wrap-up for the given date.
func (b *Service) GenerateEvening(date time.Time) (*Briefing, error) {
	dateStr := date.Format("2006-01-02")
	briefing := &Briefing{
		Type:        "evening",
		Date:        dateStr,
		Greeting:    b.EveningGreeting(date),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// 1. Day summary (conversation / interaction counts)
	if sec := b.DaySummarySection(dateStr); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 2. Habits completed today
	if sec := b.HabitsCompletedSection(dateStr); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 3. Spending today
	if sec := b.SpendingSection(dateStr); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 4. Tasks completed today
	if sec := b.TasksCompletedSection(dateStr); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 5. Tomorrow preview
	tomorrow := date.Add(24 * time.Hour)
	if sec := b.TomorrowPreviewSection(tomorrow); sec != nil {
		briefing.Sections = append(briefing.Sections, *sec)
	}

	// 6. Reflection prompt
	briefing.Quote = b.EveningReflection(date)

	return briefing, nil
}

// --- Greeting generators ---

func (b *Service) MorningGreeting(date time.Time) string {
	hour := date.Hour()
	weekday := date.Weekday().String()
	dateStr := date.Format("January 2, 2006")
	switch {
	case hour < 6:
		return fmt.Sprintf("Early bird! It's %s, %s.", weekday, dateStr)
	case hour < 12:
		return fmt.Sprintf("Good morning! It's %s, %s.", weekday, dateStr)
	default:
		return fmt.Sprintf("Hello! It's %s, %s.", weekday, dateStr)
	}
}

func (b *Service) EveningGreeting(date time.Time) string {
	weekday := date.Weekday().String()
	return fmt.Sprintf("Good evening! Here's your %s wrap-up.", weekday)
}

// --- Morning section generators ---

func (b *Service) ScheduleSection(dateStr string) *BriefingSection {
	if b.deps.ViewSchedule == nil {
		return nil
	}
	schedules, err := b.deps.ViewSchedule(dateStr, 1)
	if err != nil || len(schedules) == 0 {
		return nil
	}
	day := schedules[0]
	if len(day.Events) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Today's Schedule", Icon: "calendar"}
	for _, ev := range day.Events {
		sec.Items = append(sec.Items, fmt.Sprintf("%s — %s", ev.Start.Format("15:04"), ev.Title))
	}
	sec.Summary = fmt.Sprintf("%d events today", len(sec.Items))
	return sec
}

func (b *Service) RemindersSection(dateStr string) *BriefingSection {
	if b.dbPath == "" {
		return nil
	}
	rows, err := b.deps.Query(b.dbPath, fmt.Sprintf(
		`SELECT message, remind_at FROM reminders WHERE date(remind_at) = '%s' AND status = 'pending' ORDER BY remind_at LIMIT 10`,
		b.deps.Escape(dateStr)))
	if err != nil || len(rows) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Reminders", Icon: "bell"}
	for _, r := range rows {
		msg := jsonStr(r["message"])
		at := jsonStr(r["remind_at"])
		if msg != "" {
			if t, err := time.Parse(time.RFC3339, at); err == nil {
				sec.Items = append(sec.Items, fmt.Sprintf("%s -- %s", t.Format("15:04"), msg))
			} else {
				sec.Items = append(sec.Items, msg)
			}
		}
	}
	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("%d reminders today", len(sec.Items))
	return sec
}

func (b *Service) TasksSection(dateStr string) *BriefingSection {
	if !b.deps.TasksAvailable || b.dbPath == "" {
		return nil
	}
	rows, err := b.deps.Query(b.dbPath, fmt.Sprintf(
		`SELECT title, priority FROM user_tasks WHERE date(due_at) = '%s' AND status != 'done' AND status != 'cancelled' ORDER BY priority ASC LIMIT 10`,
		b.deps.Escape(dateStr)))
	if err != nil || len(rows) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Tasks Due Today", Icon: "check"}
	for _, r := range rows {
		title := jsonStr(r["title"])
		priority := jsonInt(r["priority"])
		if title != "" {
			switch priority {
			case 1:
				sec.Items = append(sec.Items, fmt.Sprintf("[URGENT] %s", title))
			case 2:
				sec.Items = append(sec.Items, fmt.Sprintf("[HIGH] %s", title))
			case 4:
				sec.Items = append(sec.Items, fmt.Sprintf("[LOW] %s", title))
			default:
				sec.Items = append(sec.Items, title)
			}
		}
	}
	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("%d tasks due", len(sec.Items))
	return sec
}

func (b *Service) HabitsSection(dateStr string, weekday time.Weekday) *BriefingSection {
	if !b.deps.HabitsAvailable || b.dbPath == "" {
		return nil
	}
	rows, err := b.deps.Query(b.dbPath, fmt.Sprintf(
		`SELECT h.id, h.name, h.frequency, h.target_count,
			COALESCE((SELECT SUM(value) FROM habit_logs WHERE habit_id = h.id AND date(logged_at) = '%s'), 0) as done
		FROM habits h WHERE h.archived_at = '' OR h.archived_at IS NULL ORDER BY h.name`,
		b.deps.Escape(dateStr)))
	if err != nil || len(rows) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Habits", Icon: "repeat"}
	pending := 0
	completed := 0
	for _, r := range rows {
		name := jsonStr(r["name"])
		freq := jsonStr(r["frequency"])
		if freq == "weekly" && weekday != time.Monday {
			continue // Show weekly habits only on Mondays
		}
		target := jsonFloat(r["target_count"])
		if target < 1 {
			target = 1
		}
		done := jsonFloat(r["done"])
		if done >= target {
			completed++
			sec.Items = append(sec.Items, fmt.Sprintf("[done] %s (%g/%g)", name, done, target))
		} else {
			pending++
			sec.Items = append(sec.Items, fmt.Sprintf("[todo] %s (%g/%g)", name, done, target))
		}
	}
	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("%d pending, %d completed", pending, completed)
	return sec
}

func (b *Service) GoalsSection(dateStr string) *BriefingSection {
	if !b.deps.GoalsAvailable || b.dbPath == "" {
		return nil
	}
	endDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil
	}
	endStr := endDate.Add(7 * 24 * time.Hour).Format("2006-01-02")
	rows, err := b.deps.Query(b.dbPath, fmt.Sprintf(
		`SELECT title, target_date FROM goals WHERE status = 'active' AND target_date != '' AND target_date <= '%s' AND target_date >= '%s' ORDER BY target_date LIMIT 5`,
		b.deps.Escape(endStr), b.deps.Escape(dateStr)))
	if err != nil || len(rows) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Goal Deadlines", Icon: "target"}
	for _, r := range rows {
		title := jsonStr(r["title"])
		deadline := jsonStr(r["target_date"])
		if title != "" {
			sec.Items = append(sec.Items, fmt.Sprintf("%s (due %s)", title, deadline))
		}
	}
	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("%d goals with approaching deadlines", len(sec.Items))
	return sec
}

func (b *Service) ContactsSection() *BriefingSection {
	if b.deps.GetUpcomingEvents == nil {
		return nil
	}
	events, err := b.deps.GetUpcomingEvents(7)
	if err != nil || len(events) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Upcoming Events", Icon: "cake"}
	for _, e := range events {
		name := jsonStr(e["contact_name"])
		eventType := jsonStr(e["event_type"])
		daysUntil := jsonInt(e["days_until"])
		if name != "" {
			if eventType == "" {
				eventType = "birthday"
			}
			if daysUntil == 0 {
				sec.Items = append(sec.Items, fmt.Sprintf("Today -- %s's %s!", name, eventType))
			} else {
				sec.Items = append(sec.Items, fmt.Sprintf("In %d days -- %s's %s", daysUntil, name, eventType))
			}
		}
	}
	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("%d events this week", len(sec.Items))
	return sec
}

// --- Evening section generators ---

func (b *Service) DaySummarySection(dateStr string) *BriefingSection {
	if b.dbPath == "" {
		return nil
	}
	rows, err := b.deps.Query(b.dbPath, fmt.Sprintf(
		`SELECT channel, COUNT(*) as cnt FROM history WHERE date(timestamp) = '%s' GROUP BY channel ORDER BY cnt DESC LIMIT 5`,
		b.deps.Escape(dateStr)))
	if err != nil || len(rows) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Day Summary", Icon: "bar-chart"}
	total := 0
	for _, r := range rows {
		ch := jsonStr(r["channel"])
		cnt := jsonInt(r["cnt"])
		total += cnt
		if ch != "" {
			sec.Items = append(sec.Items, fmt.Sprintf("%s: %d messages", ch, cnt))
		}
	}
	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("%d total interactions today", total)
	return sec
}

func (b *Service) HabitsCompletedSection(dateStr string) *BriefingSection {
	if !b.deps.HabitsAvailable || b.dbPath == "" {
		return nil
	}
	rows, err := b.deps.Query(b.dbPath, fmt.Sprintf(
		`SELECT h.name, COALESCE(SUM(hl.value), 0) as done, h.target_count
		FROM habits h LEFT JOIN habit_logs hl ON h.id = hl.habit_id AND date(hl.logged_at) = '%s'
		WHERE h.archived_at = '' OR h.archived_at IS NULL GROUP BY h.id ORDER BY h.name`,
		b.deps.Escape(dateStr)))
	if err != nil || len(rows) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Habits Today", Icon: "check-circle"}
	completed := 0
	missed := 0
	for _, r := range rows {
		name := jsonStr(r["name"])
		target := jsonFloat(r["target_count"])
		if target < 1 {
			target = 1
		}
		done := jsonFloat(r["done"])
		if done >= target {
			completed++
			sec.Items = append(sec.Items, fmt.Sprintf("[completed] %s", name))
		} else {
			missed++
			sec.Items = append(sec.Items, fmt.Sprintf("[missed] %s (%g/%g)", name, done, target))
		}
	}
	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("%d completed, %d missed", completed, missed)
	return sec
}

func (b *Service) SpendingSection(dateStr string) *BriefingSection {
	if !b.deps.FinanceAvailable || b.dbPath == "" {
		return nil
	}
	rows, err := b.deps.Query(b.dbPath, fmt.Sprintf(
		`SELECT category, SUM(amount) as total FROM expenses WHERE date(created_at) = '%s' GROUP BY category ORDER BY total DESC`,
		b.deps.Escape(dateStr)))
	if err != nil || len(rows) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Spending Today", Icon: "dollar"}
	var grandTotal float64
	for _, r := range rows {
		cat := jsonStr(r["category"])
		total := jsonFloat(r["total"])
		grandTotal += total
		if cat != "" {
			sec.Items = append(sec.Items, fmt.Sprintf("%s: %.0f", cat, total))
		}
	}
	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("Total: %.0f", grandTotal)
	return sec
}

func (b *Service) TasksCompletedSection(dateStr string) *BriefingSection {
	if !b.deps.TasksAvailable || b.dbPath == "" {
		return nil
	}
	rows, err := b.deps.Query(b.dbPath, fmt.Sprintf(
		`SELECT title FROM user_tasks WHERE status = 'done' AND date(updated_at) = '%s' LIMIT 10`,
		b.deps.Escape(dateStr)))
	if err != nil || len(rows) == 0 {
		return nil
	}
	sec := &BriefingSection{Title: "Tasks Completed", Icon: "check-square"}
	for _, r := range rows {
		title := jsonStr(r["title"])
		if title != "" {
			sec.Items = append(sec.Items, title)
		}
	}
	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("%d tasks completed today", len(sec.Items))
	return sec
}

func (b *Service) TomorrowPreviewSection(tomorrow time.Time) *BriefingSection {
	tomorrowStr := tomorrow.Format("2006-01-02")
	sec := &BriefingSection{Title: "Tomorrow Preview", Icon: "fast-forward"}

	// Events from schedule.
	if b.deps.ViewSchedule != nil {
		schedules, err := b.deps.ViewSchedule(tomorrowStr, 1)
		if err == nil && len(schedules) > 0 {
			for _, ev := range schedules[0].Events {
				sec.Items = append(sec.Items, fmt.Sprintf("%s -- %s", ev.Start.Format("15:04"), ev.Title))
			}
		}
	}

	// Tasks due tomorrow.
	if b.dbPath != "" {
		rows, err := b.deps.Query(b.dbPath, fmt.Sprintf(
			`SELECT title FROM user_tasks WHERE date(due_at) = '%s' AND status != 'done' AND status != 'cancelled' LIMIT 5`,
			b.deps.Escape(tomorrowStr)))
		if err == nil {
			for _, r := range rows {
				title := jsonStr(r["title"])
				if title != "" {
					sec.Items = append(sec.Items, fmt.Sprintf("[task] %s", title))
				}
			}
		}
	}

	if len(sec.Items) == 0 {
		return nil
	}
	sec.Summary = fmt.Sprintf("%d items tomorrow", len(sec.Items))
	return sec
}

// --- Quote / Reflection ---

func (b *Service) DailyQuote(date time.Time) string {
	quotes := []string{
		"The secret of getting ahead is getting started. -- Mark Twain",
		"It is not enough to be busy; so are the ants. The question is: what are we busy about? -- Thoreau",
		"Focus on being productive instead of busy. -- Tim Ferriss",
		"The way to get started is to quit talking and begin doing. -- Walt Disney",
		"You don't have to be great to start, but you have to start to be great. -- Zig Ziglar",
		"Small daily improvements are the key to staggering long-term results.",
		"What you do today can improve all your tomorrows. -- Ralph Marston",
	}
	idx := date.YearDay() % len(quotes)
	return quotes[idx]
}

func (b *Service) EveningReflection(date time.Time) string {
	prompts := []string{
		"What was the best part of your day?",
		"What did you learn today?",
		"What would you do differently if you could redo today?",
		"Who made a positive impact on your day?",
		"What are you grateful for today?",
		"What progress did you make toward your goals?",
		"What challenged you today, and how did you handle it?",
	}
	idx := date.YearDay() % len(prompts)
	return prompts[idx]
}

// --- Format helpers ---

// FormatBriefing formats a Briefing into a readable text string.
func FormatBriefing(br *Briefing) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %s Briefing -- %s\n\n", CapitalizeFirst(br.Type), br.Date))
	sb.WriteString(br.Greeting)
	sb.WriteString("\n\n")

	for _, sec := range br.Sections {
		sb.WriteString(fmt.Sprintf("### %s %s\n", sec.Icon, sec.Title))
		for _, item := range sec.Items {
			sb.WriteString(fmt.Sprintf("- %s\n", item))
		}
		if sec.Summary != "" {
			sb.WriteString(fmt.Sprintf("*%s*\n", sec.Summary))
		}
		sb.WriteString("\n")
	}

	if br.Quote != "" {
		if br.Type == "morning" {
			sb.WriteString(fmt.Sprintf("> %s\n", br.Quote))
		} else {
			sb.WriteString(fmt.Sprintf("**Reflection:** %s\n", br.Quote))
		}
	}

	return sb.String()
}

// CapitalizeFirst uppercases the first character of a string.
func CapitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- Local JSON helpers ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	case string:
		i, _ := strconv.Atoi(val)
		return i
	default:
		return 0
	}
}
