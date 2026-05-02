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

	"tetora/internal/cli"
	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/export"
	"tetora/internal/log"
	"tetora/internal/migrate"
	"tetora/internal/scheduling"
	"tetora/internal/version"
)

// --- from app_test.go ---

func TestAppSyncToGlobals(t *testing.T) {
	oldScheduling := globalSchedulingService
	defer func() {
		globalSchedulingService = oldScheduling
	}()
	globalSchedulingService = nil

	cfg := &Config{}
	sched := newSchedulingService(cfg)
	app := &App{Cfg: cfg, Scheduling: sched}
	app.SyncToGlobals()

	if globalSchedulingService != sched {
		t.Error("SyncToGlobals should set globalSchedulingService")
	}
}

func TestAppNilSafe(t *testing.T) {
	app := &App{Cfg: &Config{}}
	app.SyncToGlobals() // should not panic
}

func TestAppSyncToGlobals_InfraFields(t *testing.T) {
	oldSpawnTracker := globalSpawnTracker
	oldJudgeCache := globalJudgeCache
	oldImageGen := globalImageGenLimiter
	defer func() {
		globalSpawnTracker = oldSpawnTracker
		globalJudgeCache = oldJudgeCache
		globalImageGenLimiter = oldImageGen
	}()

	st := newSpawnTracker()
	ig := &imageGenLimiter{}

	app := &App{
		Cfg:             &Config{},
		SpawnTracker:    st,
		ImageGenLimiter: ig,
	}
	app.SyncToGlobals()

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

// --- from config_test.go ---

// ---------------------------------------------------------------------------
// config.ResolveEnvRef
// ---------------------------------------------------------------------------

func TestResolveEnvRef_NoDollarPrefix(t *testing.T) {
	got := config.ResolveEnvRef("plaintext", "testField")
	if got != "plaintext" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "plaintext", got, "plaintext")
	}
}

func TestResolveEnvRef_WithSetEnvVar(t *testing.T) {
	t.Setenv("TETORA_TEST_SECRET", "mysecret")

	got := config.ResolveEnvRef("$TETORA_TEST_SECRET", "testField")
	if got != "mysecret" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "$TETORA_TEST_SECRET", got, "mysecret")
	}
}

func TestResolveEnvRef_WithUnsetEnvVar(t *testing.T) {
	got := config.ResolveEnvRef("$TETORA_UNSET_VAR_12345", "testField")
	if got != "" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "$TETORA_UNSET_VAR_12345", got, "")
	}
}

func TestResolveEnvRef_DollarOnly(t *testing.T) {
	got := config.ResolveEnvRef("$", "testField")
	if got != "$" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "$", got, "$")
	}
}

func TestResolveEnvRef_EmptyString(t *testing.T) {
	got := config.ResolveEnvRef("", "testField")
	if got != "" {
		t.Errorf("config.ResolveEnvRef(%q) = %q, want %q", "", got, "")
	}
}

// --- from cli_test.go ---

// --- from cli_upgrade_test.go ---

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"2.0.3", []int{2, 0, 3}},
		{"2.0.3.1", []int{2, 0, 3, 1}},
		{"2.0.2.12", []int{2, 0, 2, 12}},
		{"dev", nil},
		{"", nil},
		{"v2.0.3", []int{2, 0, 3}},
		{"abc", nil},
	}
	for _, tt := range tests {
		got := parseVersion(tt.input)
		if tt.want == nil {
			if got != nil {
				t.Errorf("parseVersion(%q) = %v, want nil", tt.input, got)
			}
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("parseVersion(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseVersion(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestIsDevVersion(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"2.0.3", false},
		{"2.0.3.1", true},
		{"2.0.2.12", true},
		{"dev", false},
	}
	for _, tt := range tests {
		if got := isDevVersion(tt.input); got != tt.want {
			t.Errorf("isDevVersion(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestVersionNewerThan(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Release vs release
		{"2.0.3", "2.0.2", true},
		{"2.0.3", "2.0.3", false},
		{"2.0.2", "2.0.3", false},
		{"2.1.0", "2.0.9", true},
		{"3.0.0", "2.9.9", true},

		// Release vs dev
		{"2.0.3", "2.0.2.12", true},  // newer release > older dev
		{"2.0.3", "2.0.3.1", false},  // same base release vs dev: release is NOT "newer" (0 < 1 at segment 4)
		{"2.0.4", "2.0.3.1", true},   // newer release > dev

		// Dev vs dev
		{"2.0.3.2", "2.0.3.1", true},
		{"2.0.3.1", "2.0.3.2", false},
	}
	for _, tt := range tests {
		if got := versionNewerThan(tt.a, tt.b); got != tt.want {
			t.Errorf("versionNewerThan(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDevBaseVersion(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"2.0.3.1", "2.0.3"},
		{"2.0.2.12", "2.0.2"},
		{"2.0.3", "2.0.3"},
	}
	for _, tt := range tests {
		if got := devBaseVersion(tt.input); got != tt.want {
			t.Errorf("devBaseVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestUpgradeScenarios verifies the upgrade decision logic for key scenarios.
func TestUpgradeScenarios(t *testing.T) {
	type scenario struct {
		name    string
		current string // tetoraVersion
		latest  string // GitHub release
		should  string // "upgrade" or "skip"
	}
	scenarios := []scenario{
		{"dev to newer release", "2.0.2.12", "2.0.3", "upgrade"},
		{"dev to same base release", "2.0.3.1", "2.0.3", "upgrade"},
		{"dev to older release", "2.0.4.1", "2.0.3", "skip"},
		{"release to same release", "2.0.3", "2.0.3", "skip"},
		{"release to newer release", "2.0.2", "2.0.3", "upgrade"},
		{"release to older release", "2.0.4", "2.0.3", "skip"},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			shouldUpgrade := false
			if s.latest == s.current {
				shouldUpgrade = false
			} else if isDevVersion(s.current) {
				base := devBaseVersion(s.current)
				if base == s.latest || versionNewerThan(s.latest, base) {
					shouldUpgrade = true
				}
			} else if versionNewerThan(s.latest, s.current) {
				shouldUpgrade = true
			}

			expected := s.should == "upgrade"
			if shouldUpgrade != expected {
				t.Errorf("current=%s latest=%s: got upgrade=%v, want %v", s.current, s.latest, shouldUpgrade, expected)
			}
		})
	}
}

// --- from ops_test.go ---

func TestInitOpsDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_ops.db")

	// Create the DB file first.
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	err := initOpsDB(dbPath)
	if err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Verify tables exist.
	for _, table := range []string{"message_queue", "backup_log", "channel_status"} {
		rows, err := db.Query(dbPath, fmt.Sprintf("SELECT name FROM sqlite_master WHERE type='table' AND name='%s'", table))
		if err != nil {
			t.Fatalf("query table %s failed: %v", table, err)
		}
		if len(rows) == 0 {
			t.Errorf("table %s not created", table)
		}
	}

	// Verify index exists.
	rows, err := db.Query(dbPath, "SELECT name FROM sqlite_master WHERE type='index' AND name='idx_mq_status'")
	if err != nil {
		t.Fatalf("query index failed: %v", err)
	}
	if len(rows) == 0 {
		t.Error("index idx_mq_status not created")
	}

	// Idempotent — should not fail on second call.
	err = initOpsDB(dbPath)
	if err != nil {
		t.Fatalf("second initOpsDB failed: %v", err)
	}
}

func TestMessageQueue_Enqueue(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:       true,
				RetryAttempts: 3,
				MaxQueueSize:  100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Enqueue a message.
	err := mq.Enqueue("telegram", "12345", "Hello World", 0)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// Verify it's in the DB.
	rows, err := db.Query(dbPath, "SELECT channel, channel_target, message_text, status, priority FROM message_queue")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["channel"]) != "telegram" {
		t.Errorf("expected channel=telegram, got %v", rows[0]["channel"])
	}
	if fmt.Sprintf("%v", rows[0]["status"]) != "pending" {
		t.Errorf("expected status=pending, got %v", rows[0]["status"])
	}

	// Test empty fields validation.
	err = mq.Enqueue("", "target", "text", 0)
	if err == nil {
		t.Error("expected error for empty channel")
	}
	err = mq.Enqueue("telegram", "", "text", 0)
	if err == nil {
		t.Error("expected error for empty target")
	}
	err = mq.Enqueue("telegram", "target", "", 0)
	if err == nil {
		t.Error("expected error for empty text")
	}
}

func TestMessageQueue_EnqueuePriority(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq_prio.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:      true,
				MaxQueueSize: 100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Enqueue with different priorities.
	mq.Enqueue("telegram", "user1", "Low priority", 0)
	mq.Enqueue("telegram", "user2", "High priority", 10)
	mq.Enqueue("telegram", "user3", "Medium priority", 5)

	// Verify order by priority DESC.
	rows, err := db.Query(dbPath, "SELECT channel_target, priority FROM message_queue WHERE status='pending' ORDER BY priority DESC, id ASC")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["channel_target"]) != "user2" {
		t.Errorf("expected high priority first, got %v", rows[0]["channel_target"])
	}
}

func TestMessageQueue_QueueSizeLimit(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq_limit.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:      true,
				MaxQueueSize: 3,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Fill up the queue.
	for i := 0; i < 3; i++ {
		err := mq.Enqueue("telegram", fmt.Sprintf("user%d", i), "msg", 0)
		if err != nil {
			t.Fatalf("Enqueue %d failed: %v", i, err)
		}
	}

	// Next one should fail.
	err := mq.Enqueue("telegram", "overflow", "msg", 0)
	if err == nil {
		t.Error("expected queue full error")
	}
	if err != nil && !strings.Contains(err.Error(), "queue full") {
		t.Errorf("expected 'queue full' error, got: %v", err)
	}
}

func TestMessageQueue_ProcessQueue(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq_process.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:       true,
				RetryAttempts: 3,
				MaxQueueSize:  100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Enqueue messages.
	mq.Enqueue("telegram", "user1", "Hello", 0)
	mq.Enqueue("slack", "channel1", "World", 5)

	// Process the queue.
	ctx := context.Background()
	mq.ProcessQueue(ctx)

	// All should be sent (attemptDelivery succeeds by default).
	rows, err := db.Query(dbPath, "SELECT status FROM message_queue ORDER BY id")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	for _, row := range rows {
		status := fmt.Sprintf("%v", row["status"])
		if status != "sent" {
			t.Errorf("expected status=sent, got %s", status)
		}
	}
}

func TestMessageQueue_ProcessQueueWithFutureRetry(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_mq_future.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:       true,
				RetryAttempts: 3,
				MaxQueueSize:  100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Insert a message with future next_retry_at.
	now := time.Now().UTC().Format(time.RFC3339)
	future := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO message_queue (channel, channel_target, message_text, priority, status, next_retry_at, created_at, updated_at) VALUES ('telegram', 'user1', 'test', 0, 'pending', '%s', '%s', '%s')`,
		future, now, now,
	)
	exec.Command("sqlite3", dbPath, sql).Run()

	// Process should not pick it up.
	ctx := context.Background()
	mq.ProcessQueue(ctx)

	rows, err := db.Query(dbPath, "SELECT status FROM message_queue")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["status"]) != "pending" {
		t.Errorf("expected status=pending (future retry), got %v", rows[0]["status"])
	}
}

func TestChannelHealth_Record(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_ch.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Record healthy.
	err := recordChannelHealth(dbPath, "telegram", "healthy", "")
	if err != nil {
		t.Fatalf("recordChannelHealth (healthy) failed: %v", err)
	}

	rows, err := db.Query(dbPath, "SELECT channel, status, failure_count FROM channel_status WHERE channel='telegram'")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["status"]) != "healthy" {
		t.Errorf("expected status=healthy, got %v", rows[0]["status"])
	}
	if jsonInt(rows[0]["failure_count"]) != 0 {
		t.Errorf("expected failure_count=0, got %v", rows[0]["failure_count"])
	}

	// Record degraded.
	err = recordChannelHealth(dbPath, "telegram", "degraded", "connection timeout")
	if err != nil {
		t.Fatalf("recordChannelHealth (degraded) failed: %v", err)
	}

	rows, err = db.Query(dbPath, "SELECT status, failure_count, last_error FROM channel_status WHERE channel='telegram'")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if fmt.Sprintf("%v", rows[0]["status"]) != "degraded" {
		t.Errorf("expected status=degraded, got %v", rows[0]["status"])
	}
	if jsonInt(rows[0]["failure_count"]) != 1 {
		t.Errorf("expected failure_count=1, got %v", rows[0]["failure_count"])
	}

	// Record another failure.
	recordChannelHealth(dbPath, "telegram", "degraded", "timeout again")
	rows, _ = db.Query(dbPath, "SELECT failure_count FROM channel_status WHERE channel='telegram'")
	if jsonInt(rows[0]["failure_count"]) != 2 {
		t.Errorf("expected failure_count=2, got %v", rows[0]["failure_count"])
	}

	// Record healthy resets failure count.
	recordChannelHealth(dbPath, "telegram", "healthy", "")
	rows, _ = db.Query(dbPath, "SELECT failure_count FROM channel_status WHERE channel='telegram'")
	if jsonInt(rows[0]["failure_count"]) != 0 {
		t.Errorf("expected failure_count=0 after healthy, got %v", rows[0]["failure_count"])
	}
}

func TestChannelHealth_Get(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_ch_get.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Record some channels.
	recordChannelHealth(dbPath, "telegram", "healthy", "")
	recordChannelHealth(dbPath, "slack", "degraded", "rate limited")
	recordChannelHealth(dbPath, "discord", "offline", "bot disconnected")

	channels, err := getChannelHealth(dbPath)
	if err != nil {
		t.Fatalf("getChannelHealth failed: %v", err)
	}
	if len(channels) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(channels))
	}

	// Should be sorted by channel name.
	if channels[0].Channel != "discord" {
		t.Errorf("expected first channel=discord, got %s", channels[0].Channel)
	}
	if channels[1].Channel != "slack" {
		t.Errorf("expected second channel=slack, got %s", channels[1].Channel)
	}
	if channels[2].Channel != "telegram" {
		t.Errorf("expected third channel=telegram, got %s", channels[2].Channel)
	}
}

func TestQueueStats(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_stats.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:      true,
				MaxQueueSize: 100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Enqueue some messages.
	mq.Enqueue("telegram", "user1", "msg1", 0)
	mq.Enqueue("telegram", "user2", "msg2", 0)

	stats := mq.QueueStats()
	if stats["pending"] != 2 {
		t.Errorf("expected pending=2, got %d", stats["pending"])
	}
	if stats["sent"] != 0 {
		t.Errorf("expected sent=0, got %d", stats["sent"])
	}

	// Process queue.
	mq.ProcessQueue(context.Background())

	stats = mq.QueueStats()
	if stats["sent"] != 2 {
		t.Errorf("expected sent=2, got %d", stats["sent"])
	}
	if stats["pending"] != 0 {
		t.Errorf("expected pending=0, got %d", stats["pending"])
	}
}

func TestQueueStats_Empty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_stats_empty.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	mq := newMessageQueueEngine(cfg)

	stats := mq.QueueStats()
	if stats["pending"] != 0 {
		t.Errorf("expected pending=0 for empty queue, got %d", stats["pending"])
	}
}

func TestSystemHealth(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_health.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB:     dbPath,
		MaxConcurrent: 3,
		DefaultModel:  "sonnet",
		Providers:     map[string]ProviderConfig{"claude": {Type: "claude-cli"}},
		Agents:        map[string]AgentConfig{"test": {}},
	}

	health := getSystemHealth(cfg)

	// Check top-level status.
	if health["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %v", health["status"])
	}

	// Check database status.
	dbHealth, ok := health["database"].(map[string]any)
	if !ok {
		t.Fatal("expected database map")
	}
	if dbHealth["status"] != "healthy" {
		t.Errorf("expected db status=healthy, got %v", dbHealth["status"])
	}

	// Check config summary.
	cfgSummary, ok := health["config"].(map[string]any)
	if !ok {
		t.Fatal("expected config map")
	}
	if cfgSummary["maxConcurrent"] != 3 {
		t.Errorf("expected maxConcurrent=3, got %v", cfgSummary["maxConcurrent"])
	}
	if cfgSummary["providers"] != 1 {
		t.Errorf("expected providers=1, got %v", cfgSummary["providers"])
	}
	if cfgSummary["agents"] != 1 {
		t.Errorf("expected agents=1, got %v", cfgSummary["agents"])
	}
}

func TestSystemHealth_DegradedWithUnhealthyChannel(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_health_degraded.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Record an unhealthy channel.
	recordChannelHealth(dbPath, "telegram", "offline", "bot disconnected")

	cfg := &Config{HistoryDB: dbPath}
	health := getSystemHealth(cfg)

	if health["status"] != "degraded" {
		t.Errorf("expected status=degraded with offline channel, got %v", health["status"])
	}
}

func TestSystemHealth_NoDatabase(t *testing.T) {
	cfg := &Config{HistoryDB: "/nonexistent/path.db"}
	health := getSystemHealth(cfg)

	if health["status"] != "degraded" {
		t.Errorf("expected status=degraded with no db, got %v", health["status"])
	}
}

func TestCleanupExpiredMessages(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_cleanup.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Insert an old sent message.
	oldTime := time.Now().UTC().AddDate(0, 0, -10).Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO message_queue (channel, channel_target, message_text, status, created_at, updated_at) VALUES ('telegram', 'user1', 'old', 'sent', '%s', '%s')`,
		oldTime, oldTime,
	)
	exec.Command("sqlite3", dbPath, sql).Run()

	// Insert a recent sent message.
	now := time.Now().UTC().Format(time.RFC3339)
	sql = fmt.Sprintf(
		`INSERT INTO message_queue (channel, channel_target, message_text, status, created_at, updated_at) VALUES ('telegram', 'user2', 'new', 'sent', '%s', '%s')`,
		now, now,
	)
	exec.Command("sqlite3", dbPath, sql).Run()

	// Cleanup with 7-day retention.
	err := cleanupExpiredMessages(dbPath, 7)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	rows, err := db.Query(dbPath, "SELECT channel_target FROM message_queue")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after cleanup, got %d", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["channel_target"]) != "user2" {
		t.Errorf("expected user2 to survive cleanup, got %v", rows[0]["channel_target"])
	}
}

func TestBoolToHealthy(t *testing.T) {
	if boolToHealthy(true) != "healthy" {
		t.Error("expected healthy for true")
	}
	if boolToHealthy(false) != "offline" {
		t.Error("expected offline for false")
	}
}

func TestQueueStatusSummary(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_summary.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	// Empty queue.
	summary := queueStatusSummary(dbPath)
	if summary != "message queue: empty" {
		t.Errorf("expected empty summary, got: %s", summary)
	}

	// Add some messages.
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < 3; i++ {
		sql := fmt.Sprintf(
			`INSERT INTO message_queue (channel, channel_target, message_text, status, created_at, updated_at) VALUES ('telegram', 'user', 'msg', 'pending', '%s', '%s')`,
			now, now,
		)
		exec.Command("sqlite3", dbPath, sql).Run()
	}

	summary = queueStatusSummary(dbPath)
	if !strings.Contains(summary, "pending=3") {
		t.Errorf("expected pending=3 in summary, got: %s", summary)
	}
}

func TestSQLInjectionSafety(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_injection.db")
	exec.Command("sqlite3", dbPath, "SELECT 1").Run()

	if err := initOpsDB(dbPath); err != nil {
		t.Fatalf("initOpsDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Ops: OpsConfig{
			MessageQueue: MessageQueueConfig{
				Enabled:      true,
				MaxQueueSize: 100,
			},
		},
	}

	mq := newMessageQueueEngine(cfg)

	// Try to inject SQL via message text.
	err := mq.Enqueue("telegram", "user1", "'; DROP TABLE message_queue; --", 0)
	if err != nil {
		t.Fatalf("Enqueue with special chars failed: %v", err)
	}

	// Table should still exist.
	rows, err := db.Query(dbPath, "SELECT COUNT(*) as cnt FROM message_queue")
	if err != nil {
		t.Fatalf("table was dropped by SQL injection! query failed: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least 1 row")
	}
}

func TestInitOpsDB_FileCreation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "brand_new.db")

	// File should not exist yet.
	if _, err := os.Stat(dbPath); err == nil {
		t.Fatal("db file should not exist yet")
	}

	err := initOpsDB(dbPath)
	if err != nil {
		t.Fatalf("initOpsDB on new file failed: %v", err)
	}

	// File should now exist.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file should exist after initOpsDB: %v", err)
	}
}

// --- from version_test.go ---

// setupVersionTestDB is a helper used by tests that exercise root-level wrappers
// or functions that depend on root package types (Workflow, Config, etc.).
func setupVersionTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := version.InitDB(dbPath); err != nil {
		t.Fatalf("version.InitDB: %v", err)
	}
	return dbPath
}

// TestHandleConfigVersionSubcommands verifies the root-level CLI dispatch
// function that depends on root types (Config, etc.).
func TestHandleConfigVersionSubcommands(t *testing.T) {
	// Just test that unknown actions return false.
	if cli.HandleConfigVersionSubcommands("unknown-action", nil) {
		t.Error("unknown action should return false")
	}
}

func TestHandleWorkflowVersionSubcommands(t *testing.T) {
	if cli.HandleWorkflowVersionSubcommands("unknown-action", nil) {
		t.Error("unknown action should return false")
	}
}

// TestRestoreWorkflowVersion exercises restoreWorkflowVersion, which stays in
// the root package because it depends on Workflow, Config, loadWorkflowByName,
// and saveWorkflow.
func TestRestoreWorkflowVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	version.InitDB(dbPath)

	cfg := &Config{
		BaseDir:   dir,
		HistoryDB: dbPath,
	}

	// Create workflow dir.
	os.MkdirAll(filepath.Join(dir, "workflows"), 0o755)

	// Write initial workflow.
	wf1 := &Workflow{Name: "test-wf", Steps: []WorkflowStep{{ID: "s1", Prompt: "v1"}}}
	saveWorkflow(cfg, wf1)

	// Get v1 ID.
	versions, _ := version.QueryVersions(dbPath, "workflow", "test-wf", 10)
	if len(versions) == 0 {
		t.Fatal("no workflow versions")
	}
	v1ID := versions[0].VersionID

	// Update workflow.
	wf2 := &Workflow{Name: "test-wf", Steps: []WorkflowStep{{ID: "s1", Prompt: "v2"}, {ID: "s2", Prompt: "new"}}}
	saveWorkflow(cfg, wf2)

	// Restore to v1.
	if err := restoreWorkflowVersion(dbPath, cfg, v1ID); err != nil {
		t.Fatalf("restoreWorkflowVersion: %v", err)
	}

	// Verify restored content.
	restored, err := loadWorkflowByName(cfg, "test-wf")
	if err != nil {
		t.Fatalf("loadWorkflowByName: %v", err)
	}
	if len(restored.Steps) != 1 {
		t.Errorf("expected 1 step after restore, got %d", len(restored.Steps))
	}
	if restored.Steps[0].Prompt != "v1" {
		t.Errorf("prompt: got %q, want %q", restored.Steps[0].Prompt, "v1")
	}
}

// TestRestoreConfigVersionInvalidType is kept here because it uses the root
// wrapper, which exercises the full call path including the type alias.
func TestRestoreConfigVersionInvalidType(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	version.SnapshotEntity(dbPath, "workflow", "my-wf", `{"name":"my-wf"}`, "test", "")
	versions, _ := version.QueryVersions(dbPath, "workflow", "my-wf", 10)
	vid := versions[0].VersionID

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{}`), 0o644)

	_, err := version.RestoreConfig(dbPath, configPath, vid)
	if err == nil {
		t.Error("expected error for wrong entity type")
	}
	if !strings.Contains(err.Error(), "not a config") {
		t.Errorf("error should mention type mismatch: %v", err)
	}
}

// TestSnapshotEntityEmptyDB verifies the empty-dbPath short-circuit through
// the internal version package (snapshotConfig, snapshotWorkflow, snapshotPrompt).
func TestSnapshotEntityEmptyDB(t *testing.T) {
	if err := version.SnapshotConfig("", "/nonexistent/config.json", "test", ""); err != nil {
		t.Errorf("expected nil error for empty dbPath, got %v", err)
	}
	if err := version.SnapshotWorkflow("", "wf", "{}", "test", ""); err != nil {
		t.Errorf("expected nil error for empty dbPath, got %v", err)
	}
	if err := version.SnapshotPrompt("", "prompt", "hello", "test", ""); err != nil {
		t.Errorf("expected nil error for empty dbPath, got %v", err)
	}
}
