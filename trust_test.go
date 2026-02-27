package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTrustTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trust_test.db")
	if err := initHistoryDB(dbPath); err != nil {
		t.Fatalf("initHistoryDB: %v", err)
	}
	initSLADB(dbPath)
	initTrustDB(dbPath)
	return dbPath
}

func testCfgWithTrust(dbPath string) *Config {
	return &Config{
		HistoryDB: dbPath,
		Agents: map[string]AgentConfig{
			"翡翠": {Model: "sonnet", TrustLevel: "suggest"},
			"黒曜": {Model: "opus", TrustLevel: "auto"},
			"琥珀": {Model: "sonnet", TrustLevel: "observe"},
		},
		Trust: TrustConfig{
			Enabled:          true,
			PromoteThreshold: 5,
		},
	}
}

// --- Trust Level Validation ---

func TestIsValidTrustLevel(t *testing.T) {
	tests := []struct {
		level string
		want  bool
	}{
		{"observe", true},
		{"suggest", true},
		{"auto", true},
		{"", false},
		{"unknown", false},
		{"AUTO", false},
	}
	for _, tt := range tests {
		if got := isValidTrustLevel(tt.level); got != tt.want {
			t.Errorf("isValidTrustLevel(%q) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestTrustLevelIndex(t *testing.T) {
	if idx := trustLevelIndex("observe"); idx != 0 {
		t.Errorf("trustLevelIndex(observe) = %d, want 0", idx)
	}
	if idx := trustLevelIndex("suggest"); idx != 1 {
		t.Errorf("trustLevelIndex(suggest) = %d, want 1", idx)
	}
	if idx := trustLevelIndex("auto"); idx != 2 {
		t.Errorf("trustLevelIndex(auto) = %d, want 2", idx)
	}
	if idx := trustLevelIndex("invalid"); idx != -1 {
		t.Errorf("trustLevelIndex(invalid) = %d, want -1", idx)
	}
}

func TestNextTrustLevel(t *testing.T) {
	if next := nextTrustLevel("observe"); next != "suggest" {
		t.Errorf("nextTrustLevel(observe) = %q, want suggest", next)
	}
	if next := nextTrustLevel("suggest"); next != "auto" {
		t.Errorf("nextTrustLevel(suggest) = %q, want auto", next)
	}
	if next := nextTrustLevel("auto"); next != "" {
		t.Errorf("nextTrustLevel(auto) = %q, want empty", next)
	}
}

// --- Trust Level Resolution ---

func TestResolveTrustLevel(t *testing.T) {
	cfg := testCfgWithTrust("")

	if level := resolveTrustLevel(cfg, "翡翠"); level != "suggest" {
		t.Errorf("翡翠 trust level = %q, want suggest", level)
	}
	if level := resolveTrustLevel(cfg, "黒曜"); level != "auto" {
		t.Errorf("黒曜 trust level = %q, want auto", level)
	}
	if level := resolveTrustLevel(cfg, "琥珀"); level != "observe" {
		t.Errorf("琥珀 trust level = %q, want observe", level)
	}
}

func TestResolveTrustLevelDisabled(t *testing.T) {
	cfg := &Config{
		Trust: TrustConfig{Enabled: false},
		Agents: map[string]AgentConfig{
			"翡翠": {TrustLevel: "observe"},
		},
	}
	// When trust is disabled, always returns auto.
	if level := resolveTrustLevel(cfg, "翡翠"); level != "auto" {
		t.Errorf("disabled trust level = %q, want auto", level)
	}
}

func TestResolveTrustLevelDefault(t *testing.T) {
	cfg := &Config{
		Trust: TrustConfig{Enabled: true},
		Agents: map[string]AgentConfig{
			"翡翠": {Model: "sonnet"}, // no TrustLevel set
		},
	}
	// Default should be auto.
	if level := resolveTrustLevel(cfg, "翡翠"); level != "auto" {
		t.Errorf("default trust level = %q, want auto", level)
	}
}

// --- Apply Trust to Task ---

func TestApplyTrustObserve(t *testing.T) {
	cfg := testCfgWithTrust("")
	task := Task{PermissionMode: "acceptEdits"}

	level, needsConfirm := applyTrustToTask(cfg, &task, "琥珀")
	if level != "observe" {
		t.Errorf("level = %q, want observe", level)
	}
	if needsConfirm {
		t.Error("observe mode should not need confirmation")
	}
	if task.PermissionMode != "plan" {
		t.Errorf("permissionMode = %q, want plan (forced by observe)", task.PermissionMode)
	}
}

func TestApplyTrustSuggest(t *testing.T) {
	cfg := testCfgWithTrust("")
	task := Task{PermissionMode: "acceptEdits"}

	level, needsConfirm := applyTrustToTask(cfg, &task, "翡翠")
	if level != "suggest" {
		t.Errorf("level = %q, want suggest", level)
	}
	if !needsConfirm {
		t.Error("suggest mode should need confirmation")
	}
	if task.PermissionMode != "acceptEdits" {
		t.Errorf("permissionMode should not change for suggest mode, got %q", task.PermissionMode)
	}
}

func TestApplyTrustAuto(t *testing.T) {
	cfg := testCfgWithTrust("")
	task := Task{PermissionMode: "acceptEdits"}

	level, needsConfirm := applyTrustToTask(cfg, &task, "黒曜")
	if level != "auto" {
		t.Errorf("level = %q, want auto", level)
	}
	if needsConfirm {
		t.Error("auto mode should not need confirmation")
	}
}

// --- DB Operations ---

func TestInitTrustDB(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	// Verify trust_events table exists.
	rows, err := queryDB(dbPath, "SELECT name FROM sqlite_master WHERE type='table' AND name='trust_events'")
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected trust_events table, got %d tables", len(rows))
	}
}

func TestRecordAndQueryTrustEvents(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	recordTrustEvent(dbPath, "翡翠", "set", "observe", "suggest", 0, "test set")
	recordTrustEvent(dbPath, "翡翠", "promote", "suggest", "auto", 10, "auto promoted")

	events, err := queryTrustEvents(dbPath, "翡翠", 10)
	if err != nil {
		t.Fatalf("queryTrustEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Most recent first.
	if jsonStr(events[0]["event_type"]) != "promote" {
		t.Errorf("first event = %q, want promote", jsonStr(events[0]["event_type"]))
	}
	if jsonStr(events[1]["event_type"]) != "set" {
		t.Errorf("second event = %q, want set", jsonStr(events[1]["event_type"]))
	}
}

func TestQueryTrustEventsAllRoles(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	recordTrustEvent(dbPath, "翡翠", "set", "", "suggest", 0, "")
	recordTrustEvent(dbPath, "黒曜", "set", "", "auto", 0, "")

	events, err := queryTrustEvents(dbPath, "", 10)
	if err != nil {
		t.Fatalf("queryTrustEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events across all roles, got %d", len(events))
	}
}

// --- Consecutive Success ---

func TestQueryConsecutiveSuccess(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	// Insert 5 successes then 1 failure then 3 successes.
	now := time.Now()
	for i := 0; i < 5; i++ {
		insertTestRun(t, dbPath, "翡翠", "success",
			now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			now.Add(time.Duration(i)*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	}
	insertTestRun(t, dbPath, "翡翠", "error",
		now.Add(5*time.Minute).Format(time.RFC3339),
		now.Add(5*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	for i := 6; i < 9; i++ {
		insertTestRun(t, dbPath, "翡翠", "success",
			now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			now.Add(time.Duration(i)*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	}

	// Most recent consecutive successes = 3 (before hitting the error).
	count := queryConsecutiveSuccess(dbPath, "翡翠")
	if count != 3 {
		t.Errorf("consecutive success = %d, want 3", count)
	}
}

func TestQueryConsecutiveSuccessEmpty(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	count := queryConsecutiveSuccess(dbPath, "翡翠")
	if count != 0 {
		t.Errorf("consecutive success = %d, want 0", count)
	}
}

func TestQueryConsecutiveSuccessAllSuccess(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	now := time.Now()
	for i := 0; i < 7; i++ {
		insertTestRun(t, dbPath, "翡翠", "success",
			now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			now.Add(time.Duration(i)*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	}

	count := queryConsecutiveSuccess(dbPath, "翡翠")
	if count != 7 {
		t.Errorf("consecutive success = %d, want 7", count)
	}
}

// --- Trust Status ---

func TestGetTrustStatus(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	// Add some successes for 翡翠.
	now := time.Now()
	for i := 0; i < 6; i++ {
		insertTestRun(t, dbPath, "翡翠", "success",
			now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			now.Add(time.Duration(i)*time.Minute+30*time.Second).Format(time.RFC3339), 0.1)
	}

	status := getTrustStatus(cfg, "翡翠")
	if status.Level != "suggest" {
		t.Errorf("level = %q, want suggest", status.Level)
	}
	if status.ConsecutiveSuccess != 6 {
		t.Errorf("consecutiveSuccess = %d, want 6", status.ConsecutiveSuccess)
	}
	if !status.PromoteReady {
		t.Error("expected promoteReady = true (6 >= threshold 5)")
	}
	if status.NextLevel != "auto" {
		t.Errorf("nextLevel = %q, want auto", status.NextLevel)
	}
	if status.TotalTasks != 6 {
		t.Errorf("totalTasks = %d, want 6", status.TotalTasks)
	}
}

func TestGetAllTrustStatuses(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	statuses := getAllTrustStatuses(cfg)
	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(statuses))
	}
}

// --- Config Update ---

func TestUpdateRoleTrustLevel(t *testing.T) {
	cfg := testCfgWithTrust("")

	if err := updateAgentTrustLevel(cfg, "翡翠", "auto"); err != nil {
		t.Fatalf("updateAgentTrustLevel: %v", err)
	}
	if level := resolveTrustLevel(cfg, "翡翠"); level != "auto" {
		t.Errorf("level = %q, want auto", level)
	}
}

func TestUpdateRoleTrustLevelInvalid(t *testing.T) {
	cfg := testCfgWithTrust("")

	if err := updateAgentTrustLevel(cfg, "翡翠", "invalid"); err == nil {
		t.Error("expected error for invalid trust level")
	}
}

func TestUpdateRoleTrustLevelUnknownRole(t *testing.T) {
	cfg := testCfgWithTrust("")

	if err := updateAgentTrustLevel(cfg, "unknown", "auto"); err == nil {
		t.Error("expected error for unknown role")
	}
}

func TestSaveRoleTrustLevel(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	// Create a minimal config.
	cfg := map[string]any{
		"agents": map[string]any{
			"翡翠": map[string]any{
				"model":      "sonnet",
				"trustLevel": "suggest",
			},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	// Update trust level.
	if err := saveAgentTrustLevel(configPath, "翡翠", "auto"); err != nil {
		t.Fatalf("saveAgentTrustLevel: %v", err)
	}

	// Read back and verify.
	data, _ = os.ReadFile(configPath)
	var result map[string]any
	json.Unmarshal(data, &result)

	roles := result["agents"].(map[string]any)
	role := roles["翡翠"].(map[string]any)
	if role["trustLevel"] != "auto" {
		t.Errorf("persisted trustLevel = %v, want auto", role["trustLevel"])
	}
}

// --- Promote Threshold ---

func TestPromoteThresholdOrDefault(t *testing.T) {
	cfg := TrustConfig{}
	if v := cfg.promoteThresholdOrDefault(); v != 10 {
		t.Errorf("default = %d, want 10", v)
	}

	cfg = TrustConfig{PromoteThreshold: 20}
	if v := cfg.promoteThresholdOrDefault(); v != 20 {
		t.Errorf("custom = %d, want 20", v)
	}
}

// --- HTTP API ---

func TestTrustAPIGetAll(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/trust", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(getAllTrustStatuses(cfg))
	})

	req := httptest.NewRequest("GET", "/trust", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var statuses []TrustStatus
	json.Unmarshal(w.Body.Bytes(), &statuses)
	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(statuses))
	}
}

func TestTrustAPIGetSingle(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/trust/", func(w http.ResponseWriter, r *http.Request) {
		role := strings.TrimPrefix(r.URL.Path, "/trust/")
		if _, ok := cfg.Agents[role]; !ok {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(getTrustStatus(cfg, role))
	})

	req := httptest.NewRequest("GET", "/trust/翡翠", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var status TrustStatus
	json.Unmarshal(w.Body.Bytes(), &status)
	if status.Level != "suggest" {
		t.Errorf("level = %q, want suggest", status.Level)
	}
}

func TestTrustAPISetLevel(t *testing.T) {
	dbPath := setupTrustTestDB(t)
	cfg := testCfgWithTrust(dbPath)

	// Write a config file for persistence.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfgJSON := map[string]any{
		"agents": map[string]any{
			"翡翠": map[string]any{"model": "sonnet", "trustLevel": "suggest"},
		},
	}
	data, _ := json.MarshalIndent(cfgJSON, "", "  ")
	os.WriteFile(configPath, data, 0o644)
	cfg.baseDir = dir

	mux := http.NewServeMux()
	mux.HandleFunc("/trust/", func(w http.ResponseWriter, r *http.Request) {
		role := strings.TrimPrefix(r.URL.Path, "/trust/")
		if _, ok := cfg.Agents[role]; !ok {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		var body struct{ Level string `json:"level"` }
		json.NewDecoder(r.Body).Decode(&body)
		updateAgentTrustLevel(cfg, role, body.Level)
		saveAgentTrustLevel(configPath, role, body.Level)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(getTrustStatus(cfg, role))
	})

	body := strings.NewReader(`{"level":"auto"}`)
	req := httptest.NewRequest("POST", "/trust/翡翠", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var status TrustStatus
	json.Unmarshal(w.Body.Bytes(), &status)
	if status.Level != "auto" {
		t.Errorf("level = %q, want auto", status.Level)
	}
}

// --- Trust Events API ---

func TestTrustEventsAPI(t *testing.T) {
	dbPath := setupTrustTestDB(t)

	recordTrustEvent(dbPath, "翡翠", "set", "observe", "suggest", 0, "via CLI")

	mux := http.NewServeMux()
	mux.HandleFunc("/trust-events", func(w http.ResponseWriter, r *http.Request) {
		role := r.URL.Query().Get("role")
		events, _ := queryTrustEvents(dbPath, role, 20)
		if events == nil {
			events = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(events)
	})

	req := httptest.NewRequest("GET", "/trust-events?role=翡翠", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var events []map[string]any
	json.Unmarshal(w.Body.Bytes(), &events)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}
