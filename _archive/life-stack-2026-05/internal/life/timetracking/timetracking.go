// Package timetracking provides time tracking operations.
package timetracking

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"tetora/internal/life/lifedb"
)

// TimeEntry represents a time tracking entry.
type TimeEntry struct {
	ID              string   `json:"id"`
	UserID          string   `json:"user_id"`
	Project         string   `json:"project"`
	Activity        string   `json:"activity"`
	StartTime       string   `json:"start_time"`
	EndTime         string   `json:"end_time,omitempty"`
	DurationMinutes int      `json:"duration_minutes"`
	Tags            []string `json:"tags,omitempty"`
	Note            string   `json:"note,omitempty"`
	CreatedAt       string   `json:"created_at"`
}

// TimeReport contains aggregated time tracking data.
type TimeReport struct {
	TotalHours    float64            `json:"total_hours"`
	ByProject     map[string]float64 `json:"by_project"`
	ByDay         map[string]float64 `json:"by_day"`
	TopActivities []ActivitySummary  `json:"top_activities"`
	EntryCount    int                `json:"entry_count"`
}

// ActivitySummary summarizes time spent on an activity.
type ActivitySummary struct {
	Activity string  `json:"activity"`
	Hours    float64 `json:"hours"`
	Count    int     `json:"count"`
}

// Service provides time tracking operations.
type Service struct {
	db     lifedb.DB
	dbPath string
}

// New creates a new time tracking Service.
func New(dbPath string, db lifedb.DB) *Service {
	return &Service{
		db:     db,
		dbPath: dbPath,
	}
}

// InitDB creates the time_entries table.
func InitDB(dbPath string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS time_entries (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL DEFAULT 'default',
    project TEXT NOT NULL DEFAULT 'general',
    activity TEXT NOT NULL DEFAULT '',
    start_time TEXT NOT NULL,
    end_time TEXT DEFAULT '',
    duration_minutes INTEGER DEFAULT 0,
    tags TEXT DEFAULT '[]',
    note TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_time_entries_user ON time_entries(user_id);
CREATE INDEX IF NOT EXISTS idx_time_entries_project ON time_entries(user_id, project);
CREATE INDEX IF NOT EXISTS idx_time_entries_start ON time_entries(start_time);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init time_entries table: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// StartTimer starts a new time entry, auto-stopping any running timer.
func (svc *Service) StartTimer(userID, project, activity string, tags []string, newUUID func() string) (*TimeEntry, error) {
	if userID == "" {
		userID = "default"
	}
	if project == "" {
		project = "general"
	}

	if _, err := svc.StopTimer(userID); err != nil {
		if !strings.Contains(err.Error(), "no running timer") {
			return nil, fmt.Errorf("auto-stop failed: %w", err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := newUUID()

	tagsJSON, _ := json.Marshal(tags)
	if tags == nil {
		tagsJSON = []byte("[]")
	}

	sql := fmt.Sprintf(`INSERT INTO time_entries (id, user_id, project, activity, start_time, end_time, duration_minutes, tags, note, created_at)
VALUES ('%s','%s','%s','%s','%s','',0,'%s','','%s');`,
		svc.db.Escape(id),
		svc.db.Escape(userID),
		svc.db.Escape(project),
		svc.db.Escape(activity),
		svc.db.Escape(now),
		svc.db.Escape(string(tagsJSON)),
		svc.db.Escape(now),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("start timer: %s: %w", strings.TrimSpace(string(out)), err)
	}

	svc.db.LogInfo("timer started", "id", id, "project", project, "activity", activity, "user", userID)
	return &TimeEntry{
		ID:        id,
		UserID:    userID,
		Project:   project,
		Activity:  activity,
		StartTime: now,
		Tags:      tags,
		CreatedAt: now,
	}, nil
}

// StopTimer stops the running timer for a user.
func (svc *Service) StopTimer(userID string) (*TimeEntry, error) {
	if userID == "" {
		userID = "default"
	}

	running, err := svc.GetRunning(userID)
	if err != nil {
		return nil, err
	}
	if running == nil {
		return nil, fmt.Errorf("no running timer for user %s", userID)
	}

	now := time.Now().UTC()
	startTime, err := time.Parse(time.RFC3339, running.StartTime)
	if err != nil {
		return nil, fmt.Errorf("parse start time: %w", err)
	}
	duration := int(now.Sub(startTime).Minutes())
	if duration < 1 {
		duration = 1
	}

	nowStr := now.Format(time.RFC3339)
	sql := fmt.Sprintf(`UPDATE time_entries SET end_time = '%s', duration_minutes = %d WHERE id = '%s';`,
		svc.db.Escape(nowStr), duration, svc.db.Escape(running.ID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("stop timer: %s: %w", strings.TrimSpace(string(out)), err)
	}

	running.EndTime = nowStr
	running.DurationMinutes = duration
	svc.db.LogInfo("timer stopped", "id", running.ID, "duration_min", duration)
	return running, nil
}

// LogEntry creates a manual time entry (already completed).
func (svc *Service) LogEntry(userID, project, activity string, durationMin int, date, note string, tags []string, newUUID func() string) (*TimeEntry, error) {
	if userID == "" {
		userID = "default"
	}
	if project == "" {
		project = "general"
	}
	if durationMin <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}

	now := time.Now().UTC()
	id := newUUID()

	startTime := now.Add(-time.Duration(durationMin) * time.Minute)
	if date != "" {
		if t, err := time.Parse("2006-01-02", date); err == nil {
			startTime = t.UTC()
		}
	}

	tagsJSON, _ := json.Marshal(tags)
	if tags == nil {
		tagsJSON = []byte("[]")
	}

	sql := fmt.Sprintf(`INSERT INTO time_entries (id, user_id, project, activity, start_time, end_time, duration_minutes, tags, note, created_at)
VALUES ('%s','%s','%s','%s','%s','%s',%d,'%s','%s','%s');`,
		svc.db.Escape(id),
		svc.db.Escape(userID),
		svc.db.Escape(project),
		svc.db.Escape(activity),
		svc.db.Escape(startTime.Format(time.RFC3339)),
		svc.db.Escape(now.Format(time.RFC3339)),
		durationMin,
		svc.db.Escape(string(tagsJSON)),
		svc.db.Escape(note),
		svc.db.Escape(now.Format(time.RFC3339)),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("log entry: %s: %w", strings.TrimSpace(string(out)), err)
	}

	svc.db.LogInfo("time entry logged", "id", id, "project", project, "duration_min", durationMin)
	return &TimeEntry{
		ID:              id,
		UserID:          userID,
		Project:         project,
		Activity:        activity,
		StartTime:       startTime.Format(time.RFC3339),
		EndTime:         now.Format(time.RFC3339),
		DurationMinutes: durationMin,
		Tags:            tags,
		Note:            note,
		CreatedAt:       now.Format(time.RFC3339),
	}, nil
}

// GetRunning returns the currently running timer for a user, or nil.
func (svc *Service) GetRunning(userID string) (*TimeEntry, error) {
	if userID == "" {
		userID = "default"
	}
	sql := fmt.Sprintf(`SELECT * FROM time_entries WHERE user_id = '%s' AND end_time = '' ORDER BY start_time DESC LIMIT 1;`,
		svc.db.Escape(userID))
	rows, err := svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get running: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	entry := entryFromRow(rows[0])
	return &entry, nil
}

// Report generates a time tracking report for the given period.
func (svc *Service) Report(userID, period, project string) (*TimeReport, error) {
	if userID == "" {
		userID = "default"
	}

	var since time.Time
	now := time.Now().UTC()
	switch period {
	case "today":
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "week":
		since = now.AddDate(0, 0, -7)
	case "month":
		since = now.AddDate(0, -1, 0)
	case "year":
		since = now.AddDate(-1, 0, 0)
	default:
		since = now.AddDate(0, 0, -7)
	}

	conditions := []string{
		fmt.Sprintf("user_id = '%s'", svc.db.Escape(userID)),
		fmt.Sprintf("start_time >= '%s'", svc.db.Escape(since.Format(time.RFC3339))),
		"end_time != ''",
	}
	if project != "" {
		conditions = append(conditions, fmt.Sprintf("project = '%s'", svc.db.Escape(project)))
	}

	sql := fmt.Sprintf(`SELECT * FROM time_entries WHERE %s ORDER BY start_time DESC;`,
		strings.Join(conditions, " AND "))
	rows, err := svc.db.Query(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("report query: %w", err)
	}

	report := &TimeReport{
		ByProject: make(map[string]float64),
		ByDay:     make(map[string]float64),
	}
	activityMap := make(map[string]*ActivitySummary)

	for _, row := range rows {
		entry := entryFromRow(row)
		hours := float64(entry.DurationMinutes) / 60.0

		report.TotalHours += hours
		report.EntryCount++
		report.ByProject[entry.Project] += hours

		if len(entry.StartTime) >= 10 {
			day := entry.StartTime[:10]
			report.ByDay[day] += hours
		}

		if entry.Activity != "" {
			if as, ok := activityMap[entry.Activity]; ok {
				as.Hours += hours
				as.Count++
			} else {
				activityMap[entry.Activity] = &ActivitySummary{
					Activity: entry.Activity,
					Hours:    hours,
					Count:    1,
				}
			}
		}
	}

	for _, as := range activityMap {
		report.TopActivities = append(report.TopActivities, *as)
	}
	report.TotalHours = float64(int(report.TotalHours*100)) / 100

	return report, nil
}

func entryFromRow(row map[string]any) TimeEntry {
	entry := TimeEntry{
		ID:              jsonStr(row["id"]),
		UserID:          jsonStr(row["user_id"]),
		Project:         jsonStr(row["project"]),
		Activity:        jsonStr(row["activity"]),
		StartTime:       jsonStr(row["start_time"]),
		EndTime:         jsonStr(row["end_time"]),
		DurationMinutes: jsonInt(row["duration_minutes"]),
		Note:            jsonStr(row["note"]),
		CreatedAt:       jsonStr(row["created_at"]),
	}
	tagsStr := jsonStr(row["tags"])
	if tagsStr != "" {
		json.Unmarshal([]byte(tagsStr), &entry.Tags)
	}
	return entry
}

// --- local helpers ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func jsonInt(v any) int {
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
	case string:
		var i int
		fmt.Sscanf(x, "%d", &i)
		return i
	}
	return 0
}
