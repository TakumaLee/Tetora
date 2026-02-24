package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// --- P29.2: Time Tracking ---

// TimeTrackingConfig holds configuration for time tracking.
type TimeTrackingConfig struct {
	Enabled bool `json:"enabled"`
}

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

// TimeTrackingService provides time tracking operations.
type TimeTrackingService struct {
	dbPath string
	cfg    *Config
}

// globalTimeTracking is the singleton time tracking service.
var globalTimeTracking *TimeTrackingService

// initTimeTrackingDB creates the time_entries table.
func initTimeTrackingDB(dbPath string) error {
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

// newTimeTrackingService creates a new TimeTrackingService.
func newTimeTrackingService(cfg *Config) *TimeTrackingService {
	return &TimeTrackingService{
		dbPath: cfg.HistoryDB,
		cfg:    cfg,
	}
}

// StartTimer starts a new time entry, auto-stopping any running timer.
func (svc *TimeTrackingService) StartTimer(userID, project, activity string, tags []string) (*TimeEntry, error) {
	if userID == "" {
		userID = "default"
	}
	if project == "" {
		project = "general"
	}

	// Auto-stop any running timer.
	if _, err := svc.StopTimer(userID); err != nil {
		// Ignore "no running timer" errors.
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
		escapeSQLite(id),
		escapeSQLite(userID),
		escapeSQLite(project),
		escapeSQLite(activity),
		escapeSQLite(now),
		escapeSQLite(string(tagsJSON)),
		escapeSQLite(now),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("start timer: %s: %w", strings.TrimSpace(string(out)), err)
	}

	logInfo("timer started", "id", id, "project", project, "activity", activity, "user", userID)
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
func (svc *TimeTrackingService) StopTimer(userID string) (*TimeEntry, error) {
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
		escapeSQLite(nowStr), duration, escapeSQLite(running.ID))
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("stop timer: %s: %w", strings.TrimSpace(string(out)), err)
	}

	running.EndTime = nowStr
	running.DurationMinutes = duration
	logInfo("timer stopped", "id", running.ID, "duration_min", duration)
	return running, nil
}

// LogEntry creates a manual time entry (already completed).
func (svc *TimeTrackingService) LogEntry(userID, project, activity string, durationMin int, date, note string, tags []string) (*TimeEntry, error) {
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
		escapeSQLite(id),
		escapeSQLite(userID),
		escapeSQLite(project),
		escapeSQLite(activity),
		escapeSQLite(startTime.Format(time.RFC3339)),
		escapeSQLite(now.Format(time.RFC3339)),
		durationMin,
		escapeSQLite(string(tagsJSON)),
		escapeSQLite(note),
		escapeSQLite(now.Format(time.RFC3339)),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("log entry: %s: %w", strings.TrimSpace(string(out)), err)
	}

	logInfo("time entry logged", "id", id, "project", project, "duration_min", durationMin)
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
func (svc *TimeTrackingService) GetRunning(userID string) (*TimeEntry, error) {
	if userID == "" {
		userID = "default"
	}
	sql := fmt.Sprintf(`SELECT * FROM time_entries WHERE user_id = '%s' AND end_time = '' ORDER BY start_time DESC LIMIT 1;`,
		escapeSQLite(userID))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get running: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	entry := timeEntryFromRow(rows[0])
	return &entry, nil
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

// Report generates a time tracking report for the given period.
func (svc *TimeTrackingService) Report(userID, period, project string) (*TimeReport, error) {
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
		fmt.Sprintf("user_id = '%s'", escapeSQLite(userID)),
		fmt.Sprintf("start_time >= '%s'", escapeSQLite(since.Format(time.RFC3339))),
		"end_time != ''",
	}
	if project != "" {
		conditions = append(conditions, fmt.Sprintf("project = '%s'", escapeSQLite(project)))
	}

	sql := fmt.Sprintf(`SELECT * FROM time_entries WHERE %s ORDER BY start_time DESC;`,
		strings.Join(conditions, " AND "))
	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("report query: %w", err)
	}

	report := &TimeReport{
		ByProject: make(map[string]float64),
		ByDay:     make(map[string]float64),
	}

	activityMap := make(map[string]*ActivitySummary)

	for _, row := range rows {
		entry := timeEntryFromRow(row)
		hours := float64(entry.DurationMinutes) / 60.0

		report.TotalHours += hours
		report.EntryCount++
		report.ByProject[entry.Project] += hours

		day := entry.StartTime[:10] // YYYY-MM-DD
		report.ByDay[day] += hours

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

	// Collect top activities.
	for _, as := range activityMap {
		report.TopActivities = append(report.TopActivities, *as)
	}

	// Round total hours.
	report.TotalHours = float64(int(report.TotalHours*100)) / 100

	return report, nil
}

// timeEntryFromRow converts a DB row to a TimeEntry.
func timeEntryFromRow(row map[string]any) TimeEntry {
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

// --- Tool Handlers ---

// toolTimeStart handles the time_start tool.
func toolTimeStart(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalTimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	var args struct {
		Project  string   `json:"project"`
		Activity string   `json:"activity"`
		Tags     []string `json:"tags"`
		UserID   string   `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	entry, err := globalTimeTracking.StartTimer(args.UserID, args.Project, args.Activity, args.Tags)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(entry, "", "  ")
	return string(out), nil
}

// toolTimeStop handles the time_stop tool.
func toolTimeStop(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalTimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	var args struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	entry, err := globalTimeTracking.StopTimer(args.UserID)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(entry, "", "  ")
	return fmt.Sprintf("Timer stopped. Duration: %d minutes\n%s", entry.DurationMinutes, string(out)), nil
}

// toolTimeLog handles the time_log tool.
func toolTimeLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalTimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	var args struct {
		Project  string   `json:"project"`
		Activity string   `json:"activity"`
		Duration int      `json:"duration"`
		Date     string   `json:"date"`
		Note     string   `json:"note"`
		Tags     []string `json:"tags"`
		UserID   string   `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Duration <= 0 {
		return "", fmt.Errorf("duration (minutes) is required and must be positive")
	}
	entry, err := globalTimeTracking.LogEntry(args.UserID, args.Project, args.Activity, args.Duration, args.Date, args.Note, args.Tags)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(entry, "", "  ")
	return string(out), nil
}

// toolTimeReport handles the time_report tool.
func toolTimeReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalTimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	var args struct {
		Period  string `json:"period"`
		Project string `json:"project"`
		UserID  string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	report, err := globalTimeTracking.Report(args.UserID, args.Period, args.Project)
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}
