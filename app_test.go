package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tetora/internal/db"
	"tetora/internal/export"
	"tetora/internal/log"
	"tetora/internal/migrate"
	"tetora/internal/scheduling"
)

func TestAppSyncToGlobals(t *testing.T) {
	// Save and restore globals.
	oldProfile := globalUserProfileService
	oldFinance := globalFinanceService
	oldScheduling := globalSchedulingService
	defer func() {
		globalUserProfileService = oldProfile
		globalFinanceService = oldFinance
		globalSchedulingService = oldScheduling
	}()

	// Clear globals.
	globalUserProfileService = nil
	globalFinanceService = nil
	globalSchedulingService = nil

	// Create App with mock services.
	cfg := &Config{}
	sched := newSchedulingService(cfg)

	app := &App{
		Cfg:        cfg,
		Scheduling: sched,
	}
	app.SyncToGlobals()

	// Verify globals are set.
	if globalSchedulingService != sched {
		t.Error("SyncToGlobals should set globalSchedulingService")
	}

	// Nil fields should NOT overwrite existing globals.
	if globalUserProfileService != nil {
		t.Error("nil App.UserProfile should not set global")
	}
}

func TestAppNilSafe(t *testing.T) {
	// App with all nil fields should not panic on SyncToGlobals.
	app := &App{Cfg: &Config{}}
	app.SyncToGlobals() // should not panic
}

func TestAppSyncToGlobals_Phase2Fields(t *testing.T) {
	// Save and restore globals.
	oldLifecycle := globalLifecycleEngine
	oldTimeTracking := globalTimeTracking
	oldSpawnTracker := globalSpawnTracker
	oldJudgeCache := globalJudgeCache
	oldImageGen := globalImageGenLimiter
	defer func() {
		globalLifecycleEngine = oldLifecycle
		globalTimeTracking = oldTimeTracking
		globalSpawnTracker = oldSpawnTracker
		globalJudgeCache = oldJudgeCache
		globalImageGenLimiter = oldImageGen
	}()

	// Clear globals.
	globalLifecycleEngine = nil
	globalTimeTracking = nil

	cfg := &Config{}
	le := &LifecycleEngine{cfg: cfg}
	tt := newTimeTrackingService(cfg)
	st := newSpawnTracker()
	ig := &imageGenLimiter{}

	app := &App{
		Cfg:             cfg,
		Lifecycle:       le,
		TimeTracking:    tt,
		SpawnTracker:    st,
		ImageGenLimiter: ig,
	}
	app.SyncToGlobals()

	if globalLifecycleEngine != le {
		t.Error("SyncToGlobals should set globalLifecycleEngine")
	}
	if globalTimeTracking != tt {
		t.Error("SyncToGlobals should set globalTimeTracking")
	}
	if globalSpawnTracker != st {
		t.Error("SyncToGlobals should set globalSpawnTracker")
	}
	if globalImageGenLimiter != ig {
		t.Error("SyncToGlobals should set globalImageGenLimiter")
	}
}

// --- from integration_test.go ---

// --- Mock ToolCapableProvider ---

// mockToolProvider is a scriptable ToolCapableProvider for integration tests.
// Each call to ExecuteWithTools pops the next result from the queue.
type mockToolProvider struct {
	name    string
	results []*ProviderResult
	calls   int
	mu      sync.Mutex
}

func (m *mockToolProvider) Name() string { return m.name }

func (m *mockToolProvider) Execute(_ context.Context, _ ProviderRequest) (*ProviderResult, error) {
	return &ProviderResult{Output: "mock-execute"}, nil
}

func (m *mockToolProvider) ExecuteWithTools(_ context.Context, req ProviderRequest) (*ProviderResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.calls
	m.calls++
	if idx >= len(m.results) {
		return &ProviderResult{Output: "exhausted", StopReason: "end_turn"}, nil
	}
	return m.results[idx], nil
}

// --- Helper to build a minimal Config with tool registry ---

func testConfigWithTools(tools ...*ToolDef) *Config {
	cfg := &Config{
		DefaultProvider: "mock",
	}
	r := newEmptyRegistry()
	for _, t := range tools {
		r.Register(t)
	}
	cfg.Runtime.ToolRegistry = r
	return cfg
}

func testRegistry(p Provider) *providerRegistry {
	reg := newProviderRegistry()
	reg.Register(p.Name(), p)
	return reg
}

// echoTool returns a simple tool that echoes its input.
func echoTool() *ToolDef {
	return &ToolDef{
		Name:        "echo",
		Description: "Echoes input",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`),
		Handler: func(_ context.Context, _ *Config, input json.RawMessage) (string, error) {
			var args struct{ Msg string }
			json.Unmarshal(input, &args)
			return "echo: " + args.Msg, nil
		},
		Builtin: true,
	}
}

// counterTool returns a tool that increments an atomic counter each call.
func counterTool(counter *atomic.Int64) *ToolDef {
	return &ToolDef{
		Name:        "counter",
		Description: "Increments counter",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ context.Context, _ *Config, _ json.RawMessage) (string, error) {
			n := counter.Add(1)
			return fmt.Sprintf("count=%d", n), nil
		},
		Builtin: true,
	}
}

// --- Integration Tests ---

func TestAgenticLoop_BasicToolCall(t *testing.T) {
	// Provider returns one tool_use, then end_turn.
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				Output:     "Let me echo that.",
				StopReason: "tool_use",
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "echo", Input: json.RawMessage(`{"msg":"hello"}`)},
				},
			},
			{
				Output:     "The echo returned: hello",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool())
	task := Task{ID: "t1", Prompt: "echo hello", Provider: "mock", Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output != "The echo returned: hello" {
		t.Errorf("unexpected output: %q", result.Output)
	}
	if provider.calls != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.calls)
	}
}

func TestAgenticLoop_MultipleIterations(t *testing.T) {
	var counter atomic.Int64
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				StopReason: "tool_use",
				ToolCalls:  []ToolCall{{ID: "tc1", Name: "counter", Input: json.RawMessage(`{}`)}},
			},
			{
				StopReason: "tool_use",
				ToolCalls:  []ToolCall{{ID: "tc2", Name: "counter", Input: json.RawMessage(`{}`)}},
			},
			{
				StopReason: "tool_use",
				ToolCalls:  []ToolCall{{ID: "tc3", Name: "counter", Input: json.RawMessage(`{}`)}},
			},
			{
				Output:     "done",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(counterTool(&counter))
	task := Task{ID: "t2", Prompt: "count 3 times", Provider: "mock", Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if counter.Load() != 3 {
		t.Errorf("expected counter=3, got %d", counter.Load())
	}
	if provider.calls != 4 {
		t.Errorf("expected 4 provider calls, got %d", provider.calls)
	}
}

func TestAgenticLoop_NoToolCalls(t *testing.T) {
	// Provider immediately returns end_turn, no tool calls.
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				Output:     "No tools needed.",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool())
	task := Task{ID: "t3", Prompt: "just answer", Provider: "mock", Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output != "No tools needed." {
		t.Errorf("unexpected output: %q", result.Output)
	}
	if provider.calls != 1 {
		t.Errorf("expected 1 provider call, got %d", provider.calls)
	}
}

func TestAgenticLoop_ToolNotFound(t *testing.T) {
	// Provider requests a tool that doesn't exist.
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				StopReason: "tool_use",
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "nonexistent_tool", Input: json.RawMessage(`{}`)},
				},
			},
			{
				Output:     "I see that tool wasn't found.",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool())
	task := Task{ID: "t4", Prompt: "use missing tool", Provider: "mock", Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	// The loop should continue after the tool-not-found error and reach the second response.
	if result.Output != "I see that tool wasn't found." {
		t.Errorf("unexpected output: %q", result.Output)
	}
}

func TestAgenticLoop_BudgetExceeded(t *testing.T) {
	// Per-task budget is a soft limit: it logs a warning but continues.
	// The loop should proceed past budget and finish normally.
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				Output:     "first call",
				StopReason: "tool_use",
				CostUSD:    0.50,
				ToolCalls:  []ToolCall{{ID: "tc1", Name: "echo", Input: json.RawMessage(`{"msg":"hi"}`)}},
			},
			{
				Output:     "completed despite budget",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool())
	task := Task{ID: "t5", Prompt: "expensive", Provider: "mock", Budget: 0.10, Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	// Soft-limit: no hard error, loop continues past budget.
	if result.IsError {
		t.Fatalf("unexpected hard error: %s", result.Error)
	}
	// With soft-limit, the loop continues and the second provider call is reached.
	if result.Output != "completed despite budget" {
		t.Errorf("unexpected output: %q", result.Output)
	}
	// Provider should be called twice (budget is soft, loop continues).
	if provider.calls != 2 {
		t.Errorf("expected 2 provider calls (soft budget), got %d", provider.calls)
	}
}

func TestAgenticLoop_RoleFiltering(t *testing.T) {
	// Set up a role with limited tool access.
	var counter atomic.Int64
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				StopReason: "tool_use",
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "echo", Input: json.RawMessage(`{"msg":"allowed"}`)},
					{ID: "tc2", Name: "counter", Input: json.RawMessage(`{}`)},
				},
			},
			{
				Output:     "done with role filtering",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool(), counterTool(&counter))
	// Set up a role that only allows "echo", not "counter".
	cfg.Agents = map[string]AgentConfig{
		"limited": {
			ToolPolicy: AgentToolPolicy{
				Allow: []string{"echo"},
			},
		},
	}
	task := Task{ID: "t6", Prompt: "test role filtering", Provider: "mock", Agent: "limited"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "limited",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	// Counter tool should NOT have been executed (blocked by policy).
	if counter.Load() != 0 {
		t.Errorf("counter tool should be blocked by role policy, got count=%d", counter.Load())
	}
}

func TestDispatchConcurrent_Race(t *testing.T) {
	// Run 5 concurrent executeWithProviderAndTools calls to detect data races.
	cfg := testConfigWithTools(echoTool())

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			provider := &mockToolProvider{
				name: "mock",
				results: []*ProviderResult{
					{
						StopReason: "tool_use",
						ToolCalls: []ToolCall{
							{ID: fmt.Sprintf("tc-%d", idx), Name: "echo", Input: json.RawMessage(`{"msg":"race"}`)},
						},
					},
					{
						Output:     fmt.Sprintf("done-%d", idx),
						StopReason: "end_turn",
					},
				},
			}
			task := Task{
				ID:       fmt.Sprintf("race-%d", idx),
				Prompt:   "race test",
				Provider: "mock",
			}
			result := executeWithProviderAndTools(
				context.Background(), cfg, task, "",
				testRegistry(provider), nil, nil,
			)
			if result.IsError {
				t.Errorf("goroutine %d got error: %s", idx, result.Error)
			}
		}(i)
	}
	wg.Wait()
}

func TestConfigReload_Race(t *testing.T) {
	// Simulate config reload during dispatch by mutating cfg.toolRegistry concurrently.
	echo := echoTool()
	cfg := testConfigWithTools(echo)

	// Goroutine that repeatedly re-registers the tool (simulating hot-reload).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var reloads atomic.Int64
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Simulate reload by re-registering.
				cfg.Runtime.ToolRegistry.(*ToolRegistry).Register(echo)
				reloads.Add(1)
			}
		}
	}()

	// Run dispatches concurrently with the reload goroutine.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			provider := &mockToolProvider{
				name: "mock",
				results: []*ProviderResult{
					{
						StopReason: "tool_use",
						ToolCalls: []ToolCall{
							{ID: fmt.Sprintf("tc-%d", idx), Name: "echo", Input: json.RawMessage(`{"msg":"reload"}`)},
						},
					},
					{
						Output:     "ok",
						StopReason: "end_turn",
					},
				},
			}
			task := Task{
				ID:       fmt.Sprintf("reload-%d", idx),
				Prompt:   "reload test",
				Provider: "mock",
			}
			result := executeWithProviderAndTools(
				context.Background(), cfg, task, "",
				testRegistry(provider), nil, nil,
			)
			if result.IsError {
				t.Errorf("goroutine %d got error: %s", idx, result.Error)
			}
		}(i)
	}
	wg.Wait()
	cancel()

	if reloads.Load() == 0 {
		t.Error("expected at least one reload to have occurred")
	}
}

// --- from backup_schedule_test.go ---

func TestBackupScheduler_RunBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	backupDir := filepath.Join(dir, "backups")

	// Create a test database with some data.
	exec.Command("sqlite3", dbPath, "CREATE TABLE test(id INTEGER); INSERT INTO test VALUES(1);").Run()
	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		BaseDir:   dir,
		Ops: OpsConfig{
			BackupDir:    backupDir,
			BackupRetain: 7,
		},
	}

	bs := scheduling.NewBackupScheduler(scheduling.BackupConfig{
		DBPath:     cfg.HistoryDB,
		BackupDir:  cfg.Ops.BackupDirResolved(cfg.BaseDir),
		RetainDays: cfg.Ops.BackupRetainOrDefault(),
		EscapeSQL:  db.Escape,
		LogInfo:    log.Info,
		LogWarn:    log.Warn,
	})

	result, err := bs.RunBackup()
	if err != nil {
		t.Fatalf("RunBackup failed: %v", err)
	}

	// Verify result fields.
	if result.Filename == "" {
		t.Error("expected filename")
	}
	if result.SizeBytes <= 0 {
		t.Error("expected positive size")
	}
	if result.DurationMs < 0 {
		t.Error("expected non-negative duration")
	}
	if result.CreatedAt == "" {
		t.Error("expected createdAt")
	}

	// Verify backup file exists.
	if _, err := os.Stat(result.Filename); err != nil {
		t.Fatalf("backup file does not exist: %v", err)
	}

	// Verify backup file has content.
	info, _ := os.Stat(result.Filename)
	if info.Size() == 0 {
		t.Error("backup file is empty")
	}

	// Verify backup was logged.
	rows, err := db.Query(dbPath, "SELECT filename, status FROM backup_log ORDER BY id DESC LIMIT 1")
	if err != nil {
		t.Fatalf("query backup_log failed: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected backup_log entry")
	}
	if rows[0]["status"] != "success" {
		t.Errorf("expected status=success, got %v", rows[0]["status"])
	}
}

func TestBackupScheduler_RunBackupNoHistoryDB(t *testing.T) {
	cfg := &Config{HistoryDB: ""}
	bs := scheduling.NewBackupScheduler(scheduling.BackupConfig{
		DBPath:     cfg.HistoryDB,
		BackupDir:  cfg.Ops.BackupDirResolved(cfg.BaseDir),
		RetainDays: cfg.Ops.BackupRetainOrDefault(),
		EscapeSQL:  db.Escape,
		LogInfo:    log.Info,
		LogWarn:    log.Warn,
	})

	_, err := bs.RunBackup()
	if err == nil {
		t.Error("expected error for empty historyDB")
	}
	if !strings.Contains(err.Error(), "historyDB not configured") {
		t.Errorf("expected historyDB error, got: %v", err)
	}
}

func TestBackupScheduler_CleanOldBackups(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0o755)

	// Create some old backup files.
	oldFile := filepath.Join(backupDir, "20250101-000000_tetora.db.bak")
	os.WriteFile(oldFile, []byte("old backup"), 0o644)
	// Set modification time to 30 days ago.
	oldTime := time.Now().AddDate(0, 0, -30)
	os.Chtimes(oldFile, oldTime, oldTime)

	// Create a recent backup file.
	newFile := filepath.Join(backupDir, "20260223-000000_tetora.db.bak")
	os.WriteFile(newFile, []byte("new backup"), 0o644)

	// Create a non-backup file (should not be deleted).
	otherFile := filepath.Join(backupDir, "notes.txt")
	os.WriteFile(otherFile, []byte("notes"), 0o644)
	os.Chtimes(otherFile, oldTime, oldTime)

	cfg := &Config{
		BaseDir: dir,
		Ops: OpsConfig{
			BackupDir:    backupDir,
			BackupRetain: 7,
		},
	}

	bs := scheduling.NewBackupScheduler(scheduling.BackupConfig{
		DBPath:     cfg.HistoryDB,
		BackupDir:  cfg.Ops.BackupDirResolved(cfg.BaseDir),
		RetainDays: cfg.Ops.BackupRetainOrDefault(),
		EscapeSQL:  db.Escape,
		LogInfo:    log.Info,
		LogWarn:    log.Warn,
	})
	removed := bs.CleanOldBackups()

	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// Old backup should be gone.
	if _, err := os.Stat(oldFile); err == nil {
		t.Error("old backup should have been removed")
	}

	// New backup should remain.
	if _, err := os.Stat(newFile); err != nil {
		t.Error("new backup should remain")
	}

	// Non-backup file should remain.
	if _, err := os.Stat(otherFile); err != nil {
		t.Error("non-backup file should remain")
	}
}

func TestBackupScheduler_ListBackups(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0o755)

	// Create backup files.
	os.WriteFile(filepath.Join(backupDir, "20260101-000000_tetora.db.bak"), []byte("backup1"), 0o644)
	os.WriteFile(filepath.Join(backupDir, "20260201-000000_tetora.db.bak"), []byte("backup22"), 0o644)
	// Create a non-backup file.
	os.WriteFile(filepath.Join(backupDir, "random.txt"), []byte("not a backup"), 0o644)

	cfg := &Config{
		BaseDir: dir,
		Ops: OpsConfig{
			BackupDir: backupDir,
		},
	}

	bs := scheduling.NewBackupScheduler(scheduling.BackupConfig{
		DBPath:     cfg.HistoryDB,
		BackupDir:  cfg.Ops.BackupDirResolved(cfg.BaseDir),
		RetainDays: cfg.Ops.BackupRetainOrDefault(),
		EscapeSQL:  db.Escape,
		LogInfo:    log.Info,
		LogWarn:    log.Warn,
	})
	backups, err := bs.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}

	if len(backups) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(backups))
	}

	// Should be sorted newest first.
	if !strings.Contains(backups[0].Filename, "20260201") {
		t.Errorf("expected newest first, got %s", backups[0].Filename)
	}
}

func TestBackupScheduler_ListBackupsEmptyDir(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{
		BaseDir: dir,
		Ops: OpsConfig{
			BackupDir: filepath.Join(dir, "nonexistent"),
		},
	}

	bs := scheduling.NewBackupScheduler(scheduling.BackupConfig{
		DBPath:     cfg.HistoryDB,
		BackupDir:  cfg.Ops.BackupDirResolved(cfg.BaseDir),
		RetainDays: cfg.Ops.BackupRetainOrDefault(),
		EscapeSQL:  db.Escape,
		LogInfo:    log.Info,
		LogWarn:    log.Warn,
	})
	backups, err := bs.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("expected 0 backups, got %d", len(backups))
	}
}

func TestBackupScheduler_DefaultBackupDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	exec.Command("sqlite3", dbPath, "CREATE TABLE test(id INTEGER)").Run()
	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		BaseDir:   dir,
		Ops:       OpsConfig{}, // No backupDir set — should use default.
	}

	bs := scheduling.NewBackupScheduler(scheduling.BackupConfig{
		DBPath:     cfg.HistoryDB,
		BackupDir:  cfg.Ops.BackupDirResolved(cfg.BaseDir),
		RetainDays: cfg.Ops.BackupRetainOrDefault(),
		EscapeSQL:  db.Escape,
		LogInfo:    log.Info,
		LogWarn:    log.Warn,
	})
	result, err := bs.RunBackup()
	if err != nil {
		t.Fatalf("RunBackup with default dir failed: %v", err)
	}

	// Should be in baseDir/backups.
	expectedDir := filepath.Join(dir, "backups")
	if !strings.HasPrefix(result.Filename, expectedDir) {
		t.Errorf("expected backup in %s, got %s", expectedDir, result.Filename)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	content := "hello world"
	os.WriteFile(src, []byte(content), 0o644)

	err := scheduling.CopyFile(src, dst)
	if err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst failed: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}
}

func TestCopyFile_SourceNotExists(t *testing.T) {
	dir := t.TempDir()
	err := scheduling.CopyFile(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dst"))
	if err == nil {
		t.Error("expected error for missing source")
	}
}

// --- from export_test.go ---

func TestExportUserData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_export.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	// Create the ops tables.
	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Create some test tables that export expects.
	ddl := `
CREATE TABLE IF NOT EXISTS agent_memory (
    key TEXT, value TEXT, agent TEXT, updated_at TEXT
);
INSERT INTO agent_memory (key, value, agent, updated_at) VALUES ('name', 'test-user', 'default', '2026-01-01T00:00:00Z');
INSERT INTO agent_memory (key, value, agent, updated_at) VALUES ('pref', 'dark-mode', 'default', '2026-01-02T00:00:00Z');

CREATE TABLE IF NOT EXISTS history (
    job_id TEXT, name TEXT, status TEXT, started_at TEXT, finished_at TEXT
);
INSERT INTO history (job_id, name, status, started_at, finished_at) VALUES ('abc123', 'test-job', 'success', '2026-01-01T00:00:00Z', '2026-01-01T00:01:00Z');
`
	exec.Command("sqlite3", dbPath, ddl).Run()

	cfg := &Config{
		HistoryDB: dbPath,
		BaseDir:   dir,
		Ops: OpsConfig{
			ExportEnabled: true,
		},
	}

	result, err := export.UserData(cfg.HistoryDB, cfg.BaseDir, "")
	if err != nil {
		t.Fatalf("export.UserData failed: %v", err)
	}

	// Check result fields.
	if result.Tables == 0 {
		t.Error("expected at least 1 exported table")
	}
	if result.SizeBytes <= 0 {
		t.Error("expected positive file size")
	}
	if result.Filename == "" {
		t.Error("expected filename")
	}
	if result.CreatedAt == "" {
		t.Error("expected createdAt")
	}

	// Verify the zip file exists.
	if _, err := os.Stat(result.Filename); err != nil {
		t.Fatalf("zip file does not exist: %v", err)
	}

	// Open and inspect the zip.
	r, err := zip.OpenReader(result.Filename)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	fileNames := make(map[string]bool)
	for _, f := range r.File {
		fileNames[f.Name] = true
	}

	// Should contain manifest.
	if !fileNames["manifest.json"] {
		t.Error("expected manifest.json in zip")
	}

	// Should contain at least agent_memory.json.
	if !fileNames["agent_memory.json"] {
		t.Error("expected agent_memory.json in zip")
	}

	// Verify agent_memory.json content.
	for _, f := range r.File {
		if f.Name == "agent_memory.json" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open agent_memory.json: %v", err)
			}
			var rows []map[string]any
			if err := json.NewDecoder(rc).Decode(&rows); err != nil {
				rc.Close()
				t.Fatalf("decode agent_memory.json: %v", err)
			}
			rc.Close()
			if len(rows) != 2 {
				t.Errorf("expected 2 rows in agent_memory, got %d", len(rows))
			}
		}
	}
}

func TestExportUserData_WithUserIDFilter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_export_filter.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Create reminders table with user_id column.
	ddl := `
CREATE TABLE IF NOT EXISTS reminders (
    id TEXT, user_id TEXT, text TEXT, fire_at TEXT, status TEXT
);
INSERT INTO reminders VALUES ('r1', 'alice', 'buy milk', '2026-01-01T00:00:00Z', 'pending');
INSERT INTO reminders VALUES ('r2', 'bob', 'call mom', '2026-01-01T00:00:00Z', 'pending');
INSERT INTO reminders VALUES ('r3', 'alice', 'meeting', '2026-01-02T00:00:00Z', 'pending');
`
	exec.Command("sqlite3", dbPath, ddl).Run()

	cfg := &Config{
		HistoryDB: dbPath,
		BaseDir:   dir,
		Ops:       OpsConfig{ExportEnabled: true},
	}

	result, err := export.UserData(cfg.HistoryDB, cfg.BaseDir, "alice")
	if err != nil {
		t.Fatalf("export.UserData failed: %v", err)
	}

	// Open zip and check reminders.
	r, err := zip.OpenReader(result.Filename)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "reminders.json" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open reminders.json: %v", err)
			}
			var rows []map[string]any
			json.NewDecoder(rc).Decode(&rows)
			rc.Close()
			if len(rows) != 2 {
				t.Errorf("expected 2 reminders for alice, got %d", len(rows))
			}
		}
	}
}

func TestExportUserData_NoHistoryDB(t *testing.T) {
	cfg := &Config{HistoryDB: ""}
	_, err := export.UserData(cfg.HistoryDB, cfg.BaseDir, "")
	if err == nil {
		t.Error("expected error for empty historyDB")
	}
	if !strings.Contains(err.Error(), "historyDB not configured") {
		t.Errorf("expected historyDB error, got: %v", err)
	}
}

func TestExportUserData_MissingTables(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_export_empty.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	cfg := &Config{
		HistoryDB: dbPath,
		BaseDir:   dir,
		Ops:       OpsConfig{ExportEnabled: true},
	}

	result, err := export.UserData(cfg.HistoryDB, cfg.BaseDir, "")
	if err != nil {
		t.Fatalf("export.UserData failed even with missing tables: %v", err)
	}

	// Should still produce a zip with manifest.
	if result.Tables != 0 {
		t.Errorf("expected 0 tables exported from empty db, got %d", result.Tables)
	}

	r, err := zip.OpenReader(result.Filename)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	found := false
	for _, f := range r.File {
		if f.Name == "manifest.json" {
			found = true
		}
	}
	if !found {
		t.Error("expected manifest.json even with empty export")
	}
}

func TestCreateZipFromDir(t *testing.T) {
	srcDir := t.TempDir()
	destDir := t.TempDir()

	// Write some test files.
	os.WriteFile(filepath.Join(srcDir, "file1.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "file2.txt"), []byte("world"), 0o644)

	zipPath := filepath.Join(destDir, "test.zip")
	err := export.ZipFromDir(srcDir, zipPath)
	if err != nil {
		t.Fatalf("export.ZipFromDir failed: %v", err)
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	if len(r.File) != 2 {
		t.Errorf("expected 2 files in zip, got %d", len(r.File))
	}
}

func TestExportManifestContent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_manifest.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		BaseDir:   dir,
		Ops:       OpsConfig{ExportEnabled: true},
	}

	result, err := export.UserData(cfg.HistoryDB, cfg.BaseDir, "test-user")
	if err != nil {
		t.Fatalf("export.UserData failed: %v", err)
	}

	r, err := zip.OpenReader(result.Filename)
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "manifest.json" {
			rc, _ := f.Open()
			var manifest map[string]any
			json.NewDecoder(rc).Decode(&manifest)
			rc.Close()

			if manifest["userID"] != "test-user" {
				t.Errorf("expected userID=test-user, got %v", manifest["userID"])
			}
			if manifest["exportTimestamp"] == nil {
				t.Error("expected exportTimestamp in manifest")
			}
		}
	}
}

// --- from migrate_test.go ---

// ---------------------------------------------------------------------------
// GetConfigVersion
// ---------------------------------------------------------------------------

func TestGetConfigVersion_Missing(t *testing.T) {
	raw := map[string]json.RawMessage{
		"claudePath": json.RawMessage(`"claude"`),
	}
	if v := migrate.GetConfigVersion(raw); v != 1 {
		t.Errorf("GetConfigVersion() = %d, want 1", v)
	}
}

func TestGetConfigVersion_Present(t *testing.T) {
	raw := map[string]json.RawMessage{
		"configVersion": json.RawMessage(`2`),
	}
	if v := migrate.GetConfigVersion(raw); v != 2 {
		t.Errorf("GetConfigVersion() = %d, want 2", v)
	}
}

func TestGetConfigVersion_Invalid(t *testing.T) {
	raw := map[string]json.RawMessage{
		"configVersion": json.RawMessage(`"notanumber"`),
	}
	if v := migrate.GetConfigVersion(raw); v != 1 {
		t.Errorf("GetConfigVersion() = %d, want 1", v)
	}
}

func TestGetConfigVersion_Zero(t *testing.T) {
	raw := map[string]json.RawMessage{
		"configVersion": json.RawMessage(`0`),
	}
	if v := migrate.GetConfigVersion(raw); v != 1 {
		t.Errorf("GetConfigVersion() = %d, want 1 for zero value", v)
	}
}

// ---------------------------------------------------------------------------
// MigrateConfig
// ---------------------------------------------------------------------------

func TestMigrateConfig_DryRun(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"claudePath":    "claude",
		"maxConcurrent": 3,
		"listenAddr":    "127.0.0.1:8991",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	applied, err := migrate.MigrateConfig(configPath, true)
	if err != nil {
		t.Fatalf("MigrateConfig(dryRun=true) error: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one migration in dry run")
	}

	after, _ := os.ReadFile(configPath)
	var raw map[string]json.RawMessage
	json.Unmarshal(after, &raw)
	if _, ok := raw["configVersion"]; ok {
		t.Error("dry run should not modify config file")
	}
}

func TestMigrateConfig_Apply(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"claudePath":    "claude",
		"maxConcurrent": 3,
		"listenAddr":    "127.0.0.1:8991",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	applied, err := migrate.MigrateConfig(configPath, false)
	if err != nil {
		t.Fatalf("MigrateConfig() error: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("expected at least one migration applied")
	}

	after, _ := os.ReadFile(configPath)
	var raw map[string]json.RawMessage
	json.Unmarshal(after, &raw)

	var ver int
	if err := json.Unmarshal(raw["configVersion"], &ver); err != nil {
		t.Fatalf("parse configVersion: %v", err)
	}
	if ver != migrate.CurrentConfigVersion {
		t.Errorf("configVersion = %d, want %d", ver, migrate.CurrentConfigVersion)
	}

	if _, ok := raw["smartDispatch"]; !ok {
		t.Error("expected smartDispatch to be added by migration")
	}
	if _, ok := raw["knowledgeDir"]; !ok {
		t.Error("expected knowledgeDir to be added by migration")
	}

	entries, _ := os.ReadDir(dir)
	hasBackup := false
	for _, e := range entries {
		if len(e.Name()) > 15 && e.Name()[:12] == "config.json." {
			hasBackup = true
		}
	}
	if !hasBackup {
		t.Error("expected backup file to be created")
	}
}

func TestMigrateConfig_AlreadyUpToDate(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"configVersion": migrate.CurrentConfigVersion,
		"claudePath":    "claude",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	applied, err := migrate.MigrateConfig(configPath, false)
	if err != nil {
		t.Fatalf("MigrateConfig() error: %v", err)
	}
	if applied != nil {
		t.Errorf("expected nil applied for up-to-date config, got %v", applied)
	}
}

func TestMigrateConfig_PreservesExistingSmartDispatch(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := map[string]any{
		"claudePath": "claude",
		"smartDispatch": map[string]any{
			"enabled":     true,
			"coordinator": "custom",
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o644)

	migrate.MigrateConfig(configPath, false)

	after, _ := os.ReadFile(configPath)
	var raw map[string]json.RawMessage
	json.Unmarshal(after, &raw)

	var sd map[string]any
	json.Unmarshal(raw["smartDispatch"], &sd)
	if sd["coordinator"] != "custom" {
		t.Errorf("smartDispatch.coordinator = %v, want 'custom'", sd["coordinator"])
	}
}

func TestMigrateConfig_NonExistentFile(t *testing.T) {
	_, err := migrate.MigrateConfig("/nonexistent/path/config.json", false)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestMigrateConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte("not json"), 0o644)

	_, err := migrate.MigrateConfig(configPath, false)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
