package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupHabitsTestDB(t *testing.T) (string, *HabitsService) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initHabitsDB(dbPath); err != nil {
		t.Fatalf("initHabitsDB: %v", err)
	}
	cfg := &Config{HistoryDB: dbPath}
	svc := newHabitsService(cfg)
	return dbPath, svc
}

// insertHabitLog inserts a habit log entry at a specific time for testing.
func insertHabitLog(t *testing.T, dbPath, habitID string, at time.Time) {
	t.Helper()
	logID := newUUID()
	sql := "INSERT INTO habit_logs (id, habit_id, logged_at, value) VALUES ('" +
		escapeSQLite(logID) + "', '" + escapeSQLite(habitID) + "', '" +
		at.Format(time.RFC3339) + "', 1.0)"
	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("insert habit log: %v: %s", err, string(out))
	}
}

func TestInitHabitsDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := initHabitsDB(dbPath); err != nil {
		t.Fatalf("initHabitsDB: %v", err)
	}
	// Verify tables exist.
	rows, err := queryDB(dbPath, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	names := make(map[string]bool)
	for _, row := range rows {
		names[jsonStr(row["name"])] = true
	}
	for _, want := range []string{"habits", "habit_logs", "health_data"} {
		if !names[want] {
			t.Errorf("missing table %s, have: %v", want, names)
		}
	}
}

func TestInitHabitsDB_InvalidPath(t *testing.T) {
	err := initHabitsDB("/nonexistent/path/db.sqlite")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestCreateHabit(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)

	id, err := svc.CreateHabit("Morning Run", "Run 5km every morning", "daily", "fitness", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	// Verify in DB.
	rows, err := queryDB(dbPath, "SELECT id, name, frequency, target_count, category FROM habits")
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if jsonStr(rows[0]["name"]) != "Morning Run" {
		t.Errorf("name: got %s, want Morning Run", jsonStr(rows[0]["name"]))
	}
	if jsonStr(rows[0]["frequency"]) != "daily" {
		t.Errorf("frequency: got %s, want daily", jsonStr(rows[0]["frequency"]))
	}
	if jsonStr(rows[0]["category"]) != "fitness" {
		t.Errorf("category: got %s, want fitness", jsonStr(rows[0]["category"]))
	}
}

func TestCreateHabit_EmptyName(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	_, err := svc.CreateHabit("", "", "daily", "", "", 1)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestCreateHabit_Defaults(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)
	id, err := svc.CreateHabit("Meditate", "", "", "", "", 0)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	rows, err := queryDB(dbPath, "SELECT frequency, target_count, category FROM habits WHERE id = '"+escapeSQLite(id)+"'")
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if jsonStr(rows[0]["frequency"]) != "daily" {
		t.Errorf("frequency default: got %s, want daily", jsonStr(rows[0]["frequency"]))
	}
	if int(jsonFloat(rows[0]["target_count"])) != 1 {
		t.Errorf("target_count default: got %v, want 1", rows[0]["target_count"])
	}
	if jsonStr(rows[0]["category"]) != "general" {
		t.Errorf("category default: got %s, want general", jsonStr(rows[0]["category"]))
	}
}

func TestLogHabit(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)

	id, err := svc.CreateHabit("Read", "Read for 30 minutes", "daily", "learning", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	if err := svc.LogHabit(id, "Read chapter 5", "", 1.0); err != nil {
		t.Fatalf("LogHabit: %v", err)
	}

	rows, err := queryDB(dbPath, "SELECT habit_id, note, value FROM habit_logs")
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 log, got %d", len(rows))
	}
	if jsonStr(rows[0]["habit_id"]) != id {
		t.Errorf("habit_id: got %s, want %s", jsonStr(rows[0]["habit_id"]), id)
	}
	if jsonStr(rows[0]["note"]) != "Read chapter 5" {
		t.Errorf("note: got %s, want 'Read chapter 5'", jsonStr(rows[0]["note"]))
	}
}

func TestLogHabit_NotFound(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	err := svc.LogHabit("nonexistent-id", "", "", 1.0)
	if err == nil {
		t.Fatal("expected error for nonexistent habit")
	}
}

func TestLogHabit_EmptyID(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	err := svc.LogHabit("", "", "", 1.0)
	if err == nil {
		t.Fatal("expected error for empty habit_id")
	}
}

func TestGetStreak_Daily(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)

	id, err := svc.CreateHabit("Exercise", "", "daily", "", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	// Insert logs for the last 5 consecutive days (including today).
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		d := now.AddDate(0, 0, -i)
		insertHabitLog(t, dbPath, id, d)
	}

	current, longest, err := svc.GetStreak(id, "")
	if err != nil {
		t.Fatalf("GetStreak: %v", err)
	}
	if current != 5 {
		t.Errorf("current streak: got %d, want 5", current)
	}
	if longest != 5 {
		t.Errorf("longest streak: got %d, want 5", longest)
	}
}

func TestGetStreak_Gap(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)

	id, err := svc.CreateHabit("Meditate", "", "daily", "", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	now := time.Now().UTC()
	// Log today and yesterday (current streak = 2).
	for i := 0; i < 2; i++ {
		d := now.AddDate(0, 0, -i)
		insertHabitLog(t, dbPath, id, d)
	}
	// Skip day -2, then log days -3, -4, -5 (streak of 3).
	for i := 3; i < 6; i++ {
		d := now.AddDate(0, 0, -i)
		insertHabitLog(t, dbPath, id, d)
	}

	current, longest, err := svc.GetStreak(id, "")
	if err != nil {
		t.Fatalf("GetStreak: %v", err)
	}
	if current != 2 {
		t.Errorf("current streak: got %d, want 2", current)
	}
	if longest != 3 {
		t.Errorf("longest streak: got %d, want 3", longest)
	}
}

func TestGetStreak_Weekly(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)

	id, err := svc.CreateHabit("Weekly Review", "", "weekly", "", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	// Insert logs for 3 consecutive weeks.
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		d := now.AddDate(0, 0, -7*i)
		insertHabitLog(t, dbPath, id, d)
	}

	current, longest, err := svc.GetStreak(id, "")
	if err != nil {
		t.Fatalf("GetStreak: %v", err)
	}
	// Weekly streak should be at least 1 (current week).
	if current < 1 {
		t.Errorf("current weekly streak: got %d, want >= 1", current)
	}
	if longest < current {
		t.Errorf("longest should be >= current: got longest=%d, current=%d", longest, current)
	}
}

func TestGetStreak_NotFound(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	_, _, err := svc.GetStreak("nonexistent", "")
	if err == nil {
		t.Fatal("expected error for nonexistent habit")
	}
}

func TestHabitStatus_MultipleHabits(t *testing.T) {
	_, svc := setupHabitsTestDB(t)

	id1, err := svc.CreateHabit("Exercise", "", "daily", "fitness", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit 1: %v", err)
	}
	id2, err := svc.CreateHabit("Read", "", "daily", "learning", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit 2: %v", err)
	}

	// Log one habit today.
	if err := svc.LogHabit(id1, "", "", 1.0); err != nil {
		t.Fatalf("LogHabit: %v", err)
	}

	status, err := svc.HabitStatus("")
	if err != nil {
		t.Fatalf("HabitStatus: %v", err)
	}
	if len(status) != 2 {
		t.Fatalf("expected 2 habits, got %d", len(status))
	}

	// Find each habit in status.
	var found1, found2 bool
	for _, s := range status {
		sid := jsonStr(s["id"])
		if sid == id1 {
			found1 = true
			if complete, ok := s["today_complete"].(bool); !ok || !complete {
				t.Errorf("habit 1 should be complete today")
			}
		}
		if sid == id2 {
			found2 = true
			if complete, ok := s["today_complete"].(bool); !ok || complete {
				t.Errorf("habit 2 should not be complete today")
			}
		}
	}
	if !found1 || !found2 {
		t.Errorf("missing habits in status: found1=%v, found2=%v", found1, found2)
	}
}

func TestHabitReport_Week(t *testing.T) {
	_, svc := setupHabitsTestDB(t)

	id, err := svc.CreateHabit("Journal", "", "daily", "", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	// Log for 3 days.
	for i := 0; i < 3; i++ {
		if err := svc.LogHabit(id, "", "", 1.0); err != nil {
			t.Fatalf("LogHabit %d: %v", i, err)
		}
	}

	report, err := svc.HabitReport(id, "week", "")
	if err != nil {
		t.Fatalf("HabitReport: %v", err)
	}

	if report["period"] != "week" {
		t.Errorf("period: got %v, want week", report["period"])
	}
	if report["habit_id"] != id {
		t.Errorf("habit_id: got %v, want %s", report["habit_id"], id)
	}
	if logs, ok := report["total_logs"].(int); !ok || logs < 3 {
		t.Errorf("total_logs: got %v, want >= 3", report["total_logs"])
	}
	if _, ok := report["streak"]; !ok {
		t.Error("expected streak info in report")
	}
	if _, ok := report["completion_rate"]; !ok {
		t.Error("expected completion_rate in report")
	}
}

func TestHabitReport_AllHabits(t *testing.T) {
	_, svc := setupHabitsTestDB(t)

	// Create two habits, log some.
	svc.CreateHabit("A", "", "daily", "", "", 1)
	svc.CreateHabit("B", "", "daily", "", "", 1)

	report, err := svc.HabitReport("", "month", "")
	if err != nil {
		t.Fatalf("HabitReport all: %v", err)
	}
	if report["period"] != "month" {
		t.Errorf("period: got %v, want month", report["period"])
	}
}

func TestLogHealth(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)

	if err := svc.LogHealth("steps", 8500, "steps", "manual", ""); err != nil {
		t.Fatalf("LogHealth: %v", err)
	}
	if err := svc.LogHealth("steps", 10200, "steps", "apple_health", ""); err != nil {
		t.Fatalf("LogHealth: %v", err)
	}

	rows, err := queryDB(dbPath, "SELECT metric, value, unit, source FROM health_data ORDER BY value ASC")
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if jsonFloat(rows[0]["value"]) != 8500 {
		t.Errorf("first value: got %v, want 8500", rows[0]["value"])
	}
	if jsonStr(rows[1]["source"]) != "apple_health" {
		t.Errorf("second source: got %s, want apple_health", jsonStr(rows[1]["source"]))
	}
}

func TestLogHealth_EmptyMetric(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	err := svc.LogHealth("", 100, "", "", "")
	if err == nil {
		t.Fatal("expected error for empty metric")
	}
}

func TestGetHealthSummary(t *testing.T) {
	_, svc := setupHabitsTestDB(t)

	// Log several data points.
	values := []float64{7.5, 8.0, 6.5, 7.0, 8.5}
	for _, v := range values {
		if err := svc.LogHealth("sleep_hours", v, "hours", "manual", ""); err != nil {
			t.Fatalf("LogHealth: %v", err)
		}
	}

	summary, err := svc.GetHealthSummary("sleep_hours", "week", "")
	if err != nil {
		t.Fatalf("GetHealthSummary: %v", err)
	}

	if summary["metric"] != "sleep_hours" {
		t.Errorf("metric: got %v, want sleep_hours", summary["metric"])
	}
	if cnt, ok := summary["count"].(int); !ok || cnt != 5 {
		t.Errorf("count: got %v, want 5", summary["count"])
	}
	if avg, ok := summary["avg"].(float64); !ok || avg < 7.0 || avg > 8.0 {
		t.Errorf("avg: got %v, want between 7.0 and 8.0", summary["avg"])
	}
	if min, ok := summary["min"].(float64); !ok || min != 6.5 {
		t.Errorf("min: got %v, want 6.5", summary["min"])
	}
	if max, ok := summary["max"].(float64); !ok || max != 8.5 {
		t.Errorf("max: got %v, want 8.5", summary["max"])
	}
	if unit, ok := summary["unit"].(string); !ok || unit != "hours" {
		t.Errorf("unit: got %v, want hours", summary["unit"])
	}
}

func TestGetHealthSummary_NoData(t *testing.T) {
	_, svc := setupHabitsTestDB(t)

	summary, err := svc.GetHealthSummary("nonexistent_metric", "week", "")
	if err != nil {
		t.Fatalf("GetHealthSummary: %v", err)
	}
	if cnt, ok := summary["count"].(int); !ok || cnt != 0 {
		t.Errorf("count for empty metric: got %v, want 0", summary["count"])
	}
	if summary["trend"] != "no_data" {
		t.Errorf("trend: got %v, want no_data", summary["trend"])
	}
}

func TestCheckStreakAlerts(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)

	id, err := svc.CreateHabit("Meditate", "", "daily", "", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	// Insert a log for yesterday (so there is a streak of 1 at risk).
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	insertHabitLog(t, dbPath, id, yesterday)

	alerts, err := svc.CheckStreakAlerts("")
	if err != nil {
		t.Fatalf("CheckStreakAlerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d: %v", len(alerts), alerts)
	}
	if !strings.Contains(alerts[0], "Meditate") {
		t.Errorf("alert should mention habit name, got: %s", alerts[0])
	}
}

func TestCheckStreakAlerts_NoAlert(t *testing.T) {
	_, svc := setupHabitsTestDB(t)

	id, err := svc.CreateHabit("Read", "", "daily", "", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	// Log today -- no alert expected.
	if err := svc.LogHabit(id, "", "", 1.0); err != nil {
		t.Fatalf("LogHabit: %v", err)
	}

	alerts, err := svc.CheckStreakAlerts("")
	if err != nil {
		t.Fatalf("CheckStreakAlerts: %v", err)
	}
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d: %v", len(alerts), alerts)
	}
}

// --- Tool Handler Tests ---

func TestToolHabitCreate(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	oldGlobal := globalHabitsService
	globalHabitsService = svc
	defer func() { globalHabitsService = oldGlobal }()

	input := json.RawMessage(`{"name":"Push-ups","frequency":"daily","targetCount":3,"category":"fitness"}`)
	result, err := toolHabitCreate(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolHabitCreate: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "created" {
		t.Errorf("status: got %v, want created", resp["status"])
	}
	if resp["habit_id"] == nil || resp["habit_id"] == "" {
		t.Error("expected habit_id in response")
	}
}

func TestToolHabitCreate_NotInitialized(t *testing.T) {
	oldGlobal := globalHabitsService
	globalHabitsService = nil
	defer func() { globalHabitsService = oldGlobal }()

	input := json.RawMessage(`{"name":"Test"}`)
	_, err := toolHabitCreate(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when service not initialized")
	}
}

func TestToolHabitLog(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	oldGlobal := globalHabitsService
	globalHabitsService = svc
	defer func() { globalHabitsService = oldGlobal }()

	// Create a habit first.
	id, err := svc.CreateHabit("Water", "Drink 8 glasses", "daily", "health", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	input := json.RawMessage(`{"habitId":"` + id + `","note":"glass 1","value":1}`)
	result, err := toolHabitLog(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolHabitLog: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "logged" {
		t.Errorf("status: got %v, want logged", resp["status"])
	}
}

func TestToolHabitStatus(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	oldGlobal := globalHabitsService
	globalHabitsService = svc
	defer func() { globalHabitsService = oldGlobal }()

	svc.CreateHabit("A", "", "daily", "", "", 1)
	svc.CreateHabit("B", "", "daily", "", "", 1)

	input := json.RawMessage(`{}`)
	result, err := toolHabitStatus(context.Background(), svc.cfg, input)
	if err != nil {
		t.Fatalf("toolHabitStatus: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["count"] != float64(2) {
		t.Errorf("count: got %v, want 2", resp["count"])
	}
}
