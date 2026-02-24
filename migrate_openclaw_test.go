package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// parseIncludeList
// ---------------------------------------------------------------------------

func TestParseIncludeList_All(t *testing.T) {
	result := parseIncludeList("all")
	for _, key := range []string{"config", "memory", "skills", "cron", "workspace"} {
		if !result[key] {
			t.Fatalf("expected %s in result, got %v", key, result)
		}
	}
}

func TestParseIncludeList_Empty(t *testing.T) {
	result := parseIncludeList("")
	for _, key := range []string{"config", "memory", "skills", "cron", "workspace"} {
		if !result[key] {
			t.Fatalf("expected %s for empty, got %v", key, result)
		}
	}
}

func TestParseIncludeList_Subset(t *testing.T) {
	result := parseIncludeList("config,skills")
	if !result["config"] || !result["skills"] {
		t.Fatal("expected config and skills")
	}
	if result["memory"] {
		t.Fatal("memory should not be included")
	}
}

func TestParseIncludeList_Whitespace(t *testing.T) {
	result := parseIncludeList(" config , memory ")
	if !result["config"] || !result["memory"] {
		t.Fatalf("expected config and memory, got %v", result)
	}
	if result["skills"] {
		t.Fatal("skills should not be included")
	}
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func TestGetNestedString(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "hello",
			},
		},
	}
	if got := getNestedString(m, "a", "b", "c"); got != "hello" {
		t.Errorf("expected hello, got %s", got)
	}
	if got := getNestedString(m, "a", "x"); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestGetNestedInt(t *testing.T) {
	m := map[string]any{
		"x": map[string]any{
			"y": float64(42),
		},
	}
	if got := getNestedInt(m, "x", "y"); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
	if got := getNestedInt(m, "x", "z"); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestGetNestedBool(t *testing.T) {
	m := map[string]any{
		"enabled": true,
	}
	if got := getNestedBool(m, "enabled"); !got {
		t.Error("expected true")
	}
	if got := getNestedBool(m, "missing"); got {
		t.Error("expected false for missing")
	}
}

func TestGetNestedMap(t *testing.T) {
	inner := map[string]any{"key": "val"}
	m := map[string]any{"outer": inner}
	got := getNestedMap(m, "outer")
	if got == nil || got["key"] != "val" {
		t.Errorf("expected inner map, got %v", got)
	}
	if got := getNestedMap(m, "missing"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestGetNestedSlice(t *testing.T) {
	m := map[string]any{"arr": []any{1, 2, 3}}
	got := getNestedSlice(m, "arr")
	if len(got) != 3 {
		t.Errorf("expected 3 elements, got %d", len(got))
	}
}

func TestMaskSecret(t *testing.T) {
	if got := maskSecret("abcdef"); got != "abcd****" {
		t.Errorf("expected abcd****, got %s", got)
	}
	if got := maskSecret("ab"); got != "****" {
		t.Errorf("expected ****, got %s", got)
	}
}

func TestStripModelPrefix(t *testing.T) {
	if got := stripModelPrefix("anthropic/claude-3"); got != "claude-3" {
		t.Errorf("expected claude-3, got %s", got)
	}
	if got := stripModelPrefix("openai/gpt-4"); got != "gpt-4" {
		t.Errorf("expected gpt-4, got %s", got)
	}
	if got := stripModelPrefix("claude-3"); got != "claude-3" {
		t.Errorf("expected claude-3, got %s", got)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Daily Review", "daily-review"},
		{"hello world!", "hello-world"},
		{"  extra  spaces  ", "extra-spaces"},
		{"already-slugged", "already-slugged"},
	}
	for _, tt := range tests {
		if got := slugify(tt.in); got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// migrateOpenClaw — missing directory
// ---------------------------------------------------------------------------

func TestMigrateOpenClaw_MissingDir(t *testing.T) {
	cfg := &Config{baseDir: t.TempDir()}
	_, err := migrateOpenClaw(cfg, "/nonexistent/openclaw", false, parseIncludeList("all"), false)
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}

// ---------------------------------------------------------------------------
// migrateOpenClawConfig — nested JSON parsing
// ---------------------------------------------------------------------------

func TestMigrateOpenClawConfig_Nested(t *testing.T) {
	ocDir := t.TempDir()

	ocConfig := map[string]any{
		"channels": map[string]any{
			"telegram": map[string]any{
				"botToken":  "tg-token-12345",
				"allowFrom": []any{float64(159996130)},
			},
			"discord": map[string]any{
				"token": "disc-token-xyz",
			},
			"slack": map[string]any{
				"botToken": "slack-token-abc",
				"appToken": "slack-app-old",
			},
		},
		"agents": map[string]any{
			"defaults": map[string]any{
				"model": map[string]any{
					"primary": "anthropic/claude-sonnet-4-5-20250929",
				},
				"maxConcurrent": float64(5),
			},
		},
		"gateway": map[string]any{
			"port": float64(8080),
			"auth": map[string]any{
				"token": "gw-secret-999",
			},
		},
		"models": map[string]any{
			"providers": map[string]any{
				"anthropic": map[string]any{
					"baseUrl": "https://api.anthropic.com/v1",
					"apiKey":  "sk-ant-key123",
				},
			},
		},
	}
	data, _ := json.Marshal(ocConfig)
	os.WriteFile(filepath.Join(ocDir, "openclaw.json"), data, 0o644)

	cfg := &Config{baseDir: t.TempDir()}
	report := &MigrationReport{}

	err := migrateOpenClawConfig(cfg, ocDir, false, true, report)
	if err != nil {
		t.Fatalf("config migration failed: %v", err)
	}

	// All fields should be merged.
	// telegram.botToken, chatID, discord, slack, model, maxConcurrent, port, gw token, anthropic baseURL, anthropic apiKey = 10
	if report.ConfigMerged != 10 {
		t.Errorf("expected 10 config fields merged, got %d", report.ConfigMerged)
	}

	if cfg.Telegram.BotToken != "tg-token-12345" {
		t.Errorf("telegram token: got %s", cfg.Telegram.BotToken)
	}
	if cfg.Telegram.ChatID != 159996130 {
		t.Errorf("telegram chatID: got %d", cfg.Telegram.ChatID)
	}
	if cfg.Discord.BotToken != "disc-token-xyz" {
		t.Errorf("discord token: got %s", cfg.Discord.BotToken)
	}
	if cfg.Slack.BotToken != "slack-token-abc" {
		t.Errorf("slack token: got %s", cfg.Slack.BotToken)
	}
	if cfg.DefaultModel != "claude-sonnet-4-5-20250929" {
		t.Errorf("default model: got %s", cfg.DefaultModel)
	}
	if cfg.MaxConcurrent != 5 {
		t.Errorf("max concurrent: got %d", cfg.MaxConcurrent)
	}
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("listen addr: got %s", cfg.ListenAddr)
	}
	if cfg.APIToken != "gw-secret-999" {
		t.Errorf("api token: got %s", cfg.APIToken)
	}
	if cfg.Providers["anthropic"].BaseURL != "https://api.anthropic.com/v1" {
		t.Errorf("anthropic base URL: got %s", cfg.Providers["anthropic"].BaseURL)
	}
	if cfg.Providers["anthropic"].APIKey != "sk-ant-key123" {
		t.Errorf("anthropic api key: got %s", cfg.Providers["anthropic"].APIKey)
	}

	// Should have Slack appToken warning.
	found := false
	for _, w := range report.Warnings {
		if w == "Slack appToken found — Tetora uses signingSecret instead" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Slack appToken warning, got: %v", report.Warnings)
	}
}

func TestMigrateOpenClawConfig_NoSecrets(t *testing.T) {
	ocDir := t.TempDir()

	ocConfig := map[string]any{
		"channels": map[string]any{
			"telegram": map[string]any{
				"botToken": "tg-token-secret",
			},
		},
	}
	data, _ := json.Marshal(ocConfig)
	os.WriteFile(filepath.Join(ocDir, "openclaw.json"), data, 0o644)

	cfg := &Config{baseDir: t.TempDir()}
	report := &MigrationReport{}

	// includeSecrets=false: field counted but not set.
	err := migrateOpenClawConfig(cfg, ocDir, false, false, report)
	if err != nil {
		t.Fatalf("config migration failed: %v", err)
	}

	if report.ConfigMerged != 1 {
		t.Errorf("expected 1 merged, got %d", report.ConfigMerged)
	}
	if cfg.Telegram.BotToken != "" {
		t.Errorf("secret should not be set when includeSecrets=false, got %s", cfg.Telegram.BotToken)
	}
}

func TestMigrateOpenClawConfig_NoOverwrite(t *testing.T) {
	ocDir := t.TempDir()

	ocConfig := map[string]any{
		"channels": map[string]any{
			"telegram": map[string]any{
				"botToken": "oc-token",
			},
		},
	}
	data, _ := json.Marshal(ocConfig)
	os.WriteFile(filepath.Join(ocDir, "openclaw.json"), data, 0o644)

	cfg := &Config{baseDir: t.TempDir()}
	cfg.Telegram.BotToken = "existing-token"

	report := &MigrationReport{}
	err := migrateOpenClawConfig(cfg, ocDir, false, true, report)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	if cfg.Telegram.BotToken != "existing-token" {
		t.Errorf("should not overwrite existing token, got %s", cfg.Telegram.BotToken)
	}
	if report.ConfigMerged != 0 {
		t.Errorf("expected 0 merged, got %d", report.ConfigMerged)
	}
}

// ---------------------------------------------------------------------------
// migrateOpenClawMemory — workspace/memory/ path
// ---------------------------------------------------------------------------

func TestMigrateOpenClawMemory(t *testing.T) {
	ocDir := t.TempDir()
	tetoraDir := t.TempDir()

	// Create workspace/memory/ (correct path).
	memDir := filepath.Join(ocDir, "workspace", "memory")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, "note1.md"), []byte("# Note 1"), 0o644)
	os.WriteFile(filepath.Join(memDir, "note2.md"), []byte("# Note 2"), 0o644)
	os.WriteFile(filepath.Join(memDir, "main.sqlite"), []byte("binary"), 0o644)

	cfg := &Config{baseDir: tetoraDir}
	report := &MigrationReport{}

	err := migrateOpenClawMemory(cfg, ocDir, false, report)
	if err != nil {
		t.Fatalf("memory migration failed: %v", err)
	}

	if report.MemoryFiles != 2 {
		t.Errorf("expected 2 memory files, got %d", report.MemoryFiles)
	}

	// Check sqlite skip warning.
	found := false
	for _, w := range report.Warnings {
		if w == "skipping main.sqlite — embeddings not portable, will auto-rebuild" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected sqlite skip warning, got: %v", report.Warnings)
	}

	// Verify files exist.
	vaultDir := filepath.Join(tetoraDir, "vault", "openclaw-memory")
	data, err := os.ReadFile(filepath.Join(vaultDir, "note1.md"))
	if err != nil {
		t.Fatalf("note1.md not copied: %v", err)
	}
	if string(data) != "# Note 1" {
		t.Errorf("content mismatch: %s", string(data))
	}
}

func TestMigrateOpenClawMemory_DryRun(t *testing.T) {
	ocDir := t.TempDir()
	tetoraDir := t.TempDir()

	memDir := filepath.Join(ocDir, "workspace", "memory")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, "note.md"), []byte("# Note"), 0o644)

	cfg := &Config{baseDir: tetoraDir}
	report := &MigrationReport{}

	err := migrateOpenClawMemory(cfg, ocDir, true, report)
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	if report.MemoryFiles != 1 {
		t.Errorf("expected 1 counted, got %d", report.MemoryFiles)
	}

	// Should not actually create files.
	vaultDir := filepath.Join(tetoraDir, "vault", "openclaw-memory")
	if _, err := os.Stat(vaultDir); !os.IsNotExist(err) {
		t.Fatal("dry run should not create vault directory")
	}
}

// ---------------------------------------------------------------------------
// migrateOpenClawWorkspace
// ---------------------------------------------------------------------------

func TestMigrateOpenClawWorkspace(t *testing.T) {
	ocDir := t.TempDir()
	tetoraDir := t.TempDir()

	wsDir := filepath.Join(ocDir, "workspace")
	os.MkdirAll(wsDir, 0o755)
	os.WriteFile(filepath.Join(wsDir, "SOUL.md"), []byte("soul content"), 0o644)
	os.WriteFile(filepath.Join(wsDir, "AGENTS.md"), []byte("agents content"), 0o644)
	os.WriteFile(filepath.Join(wsDir, "USER.md"), []byte("user content"), 0o644)
	os.WriteFile(filepath.Join(wsDir, "IDENTITY.md"), []byte("identity content"), 0o644)
	os.WriteFile(filepath.Join(wsDir, "MEMORY.md"), []byte("memory content"), 0o644)
	os.WriteFile(filepath.Join(wsDir, "HEARTBEAT.md"), []byte("heartbeat"), 0o644)
	os.WriteFile(filepath.Join(wsDir, "random.txt"), []byte("ignored"), 0o644)

	cfg := &Config{baseDir: tetoraDir}
	report := &MigrationReport{}

	err := migrateOpenClawWorkspace(cfg, ocDir, false, report)
	if err != nil {
		t.Fatalf("workspace migration failed: %v", err)
	}

	// SOUL.md + AGENTS.md + USER.md + IDENTITY.md + MEMORY.md = 5
	if report.WorkspaceFiles != 5 {
		t.Errorf("expected 5 workspace files, got %d", report.WorkspaceFiles)
	}

	// SOUL.md -> workspace/SOUL-openclaw.md
	soulData, err := os.ReadFile(filepath.Join(tetoraDir, "workspace", "SOUL-openclaw.md"))
	if err != nil {
		t.Fatalf("SOUL-openclaw.md not created: %v", err)
	}
	if string(soulData) != "soul content" {
		t.Errorf("SOUL content mismatch: %s", string(soulData))
	}

	// AGENTS.md -> vault/openclaw-workspace/AGENTS.md
	vaultPath := filepath.Join(tetoraDir, "vault", "openclaw-workspace")
	agentsData, err := os.ReadFile(filepath.Join(vaultPath, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md not copied: %v", err)
	}
	if string(agentsData) != "agents content" {
		t.Errorf("AGENTS content mismatch: %s", string(agentsData))
	}

	// MEMORY.md -> vault/openclaw-memory/MEMORY.md
	memPath := filepath.Join(tetoraDir, "vault", "openclaw-memory")
	memData, err := os.ReadFile(filepath.Join(memPath, "MEMORY.md"))
	if err != nil {
		t.Fatalf("MEMORY.md not copied: %v", err)
	}
	if string(memData) != "memory content" {
		t.Errorf("MEMORY content mismatch: %s", string(memData))
	}

	// HEARTBEAT.md warning.
	found := false
	for _, w := range report.Warnings {
		if w == "HEARTBEAT.md found — convert to Tetora cron job manually" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected HEARTBEAT warning, got: %v", report.Warnings)
	}
}

// ---------------------------------------------------------------------------
// migrateOpenClawSkills — file-based and folder-based
// ---------------------------------------------------------------------------

func TestMigrateOpenClawSkills_FolderBased(t *testing.T) {
	ocDir := t.TempDir()
	tetoraDir := t.TempDir()

	skillsDir := filepath.Join(ocDir, "skills")
	os.MkdirAll(skillsDir, 0o755)

	// File-based skill.
	os.WriteFile(filepath.Join(skillsDir, "greeting.md"), []byte("say hello"), 0o644)

	// Folder-based skill with SKILL.md.
	folderSkill := filepath.Join(skillsDir, "research")
	os.MkdirAll(folderSkill, 0o755)
	os.WriteFile(filepath.Join(folderSkill, "SKILL.md"), []byte("research skill"), 0o644)
	os.WriteFile(filepath.Join(folderSkill, "prompt.txt"), []byte("search query"), 0o644)

	// Directory without SKILL.md (not a skill folder).
	notSkill := filepath.Join(skillsDir, "random-dir")
	os.MkdirAll(notSkill, 0o755)
	os.WriteFile(filepath.Join(notSkill, "something.txt"), []byte("not a skill"), 0o644)

	cfg := &Config{baseDir: tetoraDir}
	report := &MigrationReport{}

	err := migrateOpenClawSkills(cfg, ocDir, false, report)
	if err != nil {
		t.Fatalf("skills migration failed: %v", err)
	}

	// greeting.md + research folder = 2 (random-dir skipped)
	if report.SkillsImported != 2 {
		t.Errorf("expected 2 skills, got %d", report.SkillsImported)
	}

	// Verify file-based skill.
	data, err := os.ReadFile(filepath.Join(tetoraDir, "skills", "greeting.md"))
	if err != nil {
		t.Fatalf("greeting.md not copied: %v", err)
	}
	if string(data) != "say hello" {
		t.Errorf("greeting content mismatch: %s", string(data))
	}

	// Verify folder-based skill.
	data, err = os.ReadFile(filepath.Join(tetoraDir, "skills", "research", "SKILL.md"))
	if err != nil {
		t.Fatalf("research/SKILL.md not copied: %v", err)
	}
	if string(data) != "research skill" {
		t.Errorf("research skill content mismatch: %s", string(data))
	}
	data, err = os.ReadFile(filepath.Join(tetoraDir, "skills", "research", "prompt.txt"))
	if err != nil {
		t.Fatalf("research/prompt.txt not copied: %v", err)
	}
	if string(data) != "search query" {
		t.Errorf("prompt content mismatch: %s", string(data))
	}
}

// ---------------------------------------------------------------------------
// migrateOpenClawCron
// ---------------------------------------------------------------------------

func TestMigrateOpenClawCron(t *testing.T) {
	ocDir := t.TempDir()
	tetoraDir := t.TempDir()

	cronDir := filepath.Join(ocDir, "cron")
	os.MkdirAll(cronDir, 0o755)

	cronJobs := []map[string]any{
		{
			"name":    "Daily Review",
			"enabled": true,
			"schedule": map[string]any{
				"expr": "30 3 * * *",
				"tz":   "Asia/Taipei",
			},
			"payload": map[string]any{
				"message":        "Run daily review",
				"timeoutSeconds": float64(900),
				"model":          "anthropic/claude-sonnet-4-5-20250929",
			},
			"delivery": map[string]any{
				"mode":    "announce",
				"channel": "telegram",
				"to":      "159996130",
			},
		},
		{
			"name":    "Weekly Cleanup",
			"enabled": false,
			"schedule": map[string]any{
				"expr": "0 0 * * 0",
				"tz":   "UTC",
			},
			"payload": map[string]any{
				"message": "Cleanup old files",
				"model":   "openai/gpt-4",
			},
			"delivery": map[string]any{
				"mode": "silent",
			},
		},
	}
	data, _ := json.Marshal(cronJobs)
	os.WriteFile(filepath.Join(cronDir, "jobs.json"), data, 0o644)

	jobsFile := filepath.Join(tetoraDir, "jobs.json")
	cfg := &Config{baseDir: tetoraDir, JobsFile: jobsFile}
	report := &MigrationReport{}

	err := migrateOpenClawCron(cfg, ocDir, false, report)
	if err != nil {
		t.Fatalf("cron migration failed: %v", err)
	}

	if report.CronJobs != 2 {
		t.Errorf("expected 2 cron jobs, got %d", report.CronJobs)
	}

	// Read back the jobs file.
	outData, err := os.ReadFile(jobsFile)
	if err != nil {
		t.Fatalf("jobs file not created: %v", err)
	}

	var jf struct {
		Jobs []CronJobConfig `json:"jobs"`
	}
	if err := json.Unmarshal(outData, &jf); err != nil {
		t.Fatalf("invalid jobs json: %v", err)
	}

	if len(jf.Jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jf.Jobs))
	}

	j0 := jf.Jobs[0]
	if j0.ID != "daily-review" {
		t.Errorf("expected ID daily-review, got %s", j0.ID)
	}
	if j0.Name != "Daily Review" {
		t.Errorf("expected name Daily Review, got %s", j0.Name)
	}
	if !j0.Enabled {
		t.Error("expected enabled=true")
	}
	if j0.Schedule != "30 3 * * *" {
		t.Errorf("expected schedule 30 3 * * *, got %s", j0.Schedule)
	}
	if j0.TZ != "Asia/Taipei" {
		t.Errorf("expected TZ Asia/Taipei, got %s", j0.TZ)
	}
	if j0.Task.Prompt != "Run daily review" {
		t.Errorf("expected prompt, got %s", j0.Task.Prompt)
	}
	if j0.Task.Model != "claude-sonnet-4-5-20250929" {
		t.Errorf("expected stripped model, got %s", j0.Task.Model)
	}
	if j0.Task.Timeout != "15m" {
		t.Errorf("expected 15m timeout, got %s", j0.Task.Timeout)
	}
	if j0.Task.Budget != 2.0 {
		t.Errorf("expected budget 2.0, got %f", j0.Task.Budget)
	}
	if !j0.Notify {
		t.Error("expected notify=true for announce mode")
	}

	j1 := jf.Jobs[1]
	if j1.ID != "weekly-cleanup" {
		t.Errorf("expected ID weekly-cleanup, got %s", j1.ID)
	}
	if j1.Notify {
		t.Error("expected notify=false for silent mode")
	}
	if j1.Task.Model != "gpt-4" {
		t.Errorf("expected gpt-4, got %s", j1.Task.Model)
	}
}

func TestMigrateOpenClawCron_SkipExisting(t *testing.T) {
	ocDir := t.TempDir()
	tetoraDir := t.TempDir()

	cronDir := filepath.Join(ocDir, "cron")
	os.MkdirAll(cronDir, 0o755)

	cronJobs := []map[string]any{
		{
			"name":     "Existing Job",
			"enabled":  true,
			"schedule": map[string]any{"expr": "0 0 * * *"},
			"payload":  map[string]any{"message": "hello"},
		},
	}
	data, _ := json.Marshal(cronJobs)
	os.WriteFile(filepath.Join(cronDir, "jobs.json"), data, 0o644)

	// Pre-create jobs file with the same job name.
	jobsFile := filepath.Join(tetoraDir, "jobs.json")
	existing := struct {
		Jobs []CronJobConfig `json:"jobs"`
	}{
		Jobs: []CronJobConfig{
			{ID: "existing-job", Name: "Existing Job", Schedule: "0 0 * * *"},
		},
	}
	existData, _ := json.Marshal(existing)
	os.WriteFile(jobsFile, existData, 0o644)

	cfg := &Config{baseDir: tetoraDir, JobsFile: jobsFile}
	report := &MigrationReport{}

	err := migrateOpenClawCron(cfg, ocDir, false, report)
	if err != nil {
		t.Fatalf("cron migration failed: %v", err)
	}

	if report.CronJobs != 0 {
		t.Errorf("expected 0 new jobs (all skipped), got %d", report.CronJobs)
	}

	// Check warning.
	found := false
	for _, w := range report.Warnings {
		if w == `cron job "Existing Job" already exists, skipped` {
			found = true
		}
	}
	if !found {
		t.Errorf("expected skip warning, got: %v", report.Warnings)
	}
}

// ---------------------------------------------------------------------------
// Full integration — migrateOpenClaw
// ---------------------------------------------------------------------------

func TestMigrateOpenClaw_DryRun(t *testing.T) {
	ocDir := t.TempDir()
	tetoraDir := t.TempDir()

	// Create nested openclaw.json.
	ocConfig := map[string]any{
		"channels": map[string]any{
			"telegram": map[string]any{
				"botToken":  "test-token-123",
				"allowFrom": []any{float64(12345)},
			},
		},
		"agents": map[string]any{
			"defaults": map[string]any{
				"model": map[string]any{
					"primary": "anthropic/claude-3-opus",
				},
				"maxConcurrent": float64(5),
			},
		},
		"gateway": map[string]any{
			"port": float64(9090),
		},
	}
	data, _ := json.Marshal(ocConfig)
	os.WriteFile(filepath.Join(ocDir, "openclaw.json"), data, 0o644)

	// Create workspace/memory/ with files.
	memDir := filepath.Join(ocDir, "workspace", "memory")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, "note1.md"), []byte("# Note 1"), 0o644)
	os.WriteFile(filepath.Join(memDir, "note2.md"), []byte("# Note 2"), 0o644)

	// Create skills directory.
	skillsDir := filepath.Join(ocDir, "skills")
	os.MkdirAll(skillsDir, 0o755)
	os.WriteFile(filepath.Join(skillsDir, "greeting.md"), []byte("say hello"), 0o644)

	cfg := &Config{baseDir: tetoraDir}

	// Dry run: nothing should be written.
	report, err := migrateOpenClaw(cfg, ocDir, true, parseIncludeList("all"), false)
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	// telegram.botToken + chatID + model + maxConcurrent + port = 5
	if report.ConfigMerged != 5 {
		t.Errorf("expected 5 config fields merged, got %d", report.ConfigMerged)
	}
	if report.MemoryFiles != 2 {
		t.Errorf("expected 2 memory files, got %d", report.MemoryFiles)
	}
	if report.SkillsImported != 1 {
		t.Errorf("expected 1 skill imported, got %d", report.SkillsImported)
	}

	// Verify nothing was actually written.
	vaultDir := filepath.Join(tetoraDir, "vault", "openclaw-memory")
	if _, err := os.Stat(vaultDir); !os.IsNotExist(err) {
		t.Fatal("dry run should not create vault directory")
	}
	skillsDstDir := filepath.Join(tetoraDir, "skills")
	if _, err := os.Stat(skillsDstDir); !os.IsNotExist(err) {
		t.Fatal("dry run should not create skills directory")
	}

	// Verify config was not modified (dry run).
	if cfg.Telegram.BotToken != "" {
		t.Fatal("dry run should not modify config")
	}
}

func TestMigrateOpenClaw_IncludeSubset(t *testing.T) {
	ocDir := t.TempDir()
	tetoraDir := t.TempDir()

	// Create nested config.
	ocConfig := map[string]any{
		"gateway": map[string]any{
			"port": float64(3000),
		},
	}
	data, _ := json.Marshal(ocConfig)
	os.WriteFile(filepath.Join(ocDir, "openclaw.json"), data, 0o644)

	memDir := filepath.Join(ocDir, "workspace", "memory")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, "note.md"), []byte("note"), 0o644)

	skillsDir := filepath.Join(ocDir, "skills")
	os.MkdirAll(skillsDir, 0o755)
	os.WriteFile(filepath.Join(skillsDir, "skill.md"), []byte("skill"), 0o644)

	// Only include config.
	cfg := &Config{baseDir: tetoraDir}
	include := parseIncludeList("config")
	report, err := migrateOpenClaw(cfg, ocDir, false, include, false)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	if report.ConfigMerged != 1 { // only port
		t.Errorf("expected 1 config merged, got %d", report.ConfigMerged)
	}
	if report.MemoryFiles != 0 {
		t.Errorf("memory should not be migrated, got %d", report.MemoryFiles)
	}
	if report.SkillsImported != 0 {
		t.Errorf("skills should not be migrated, got %d", report.SkillsImported)
	}
}

// ---------------------------------------------------------------------------
// MigrationReport JSON serialization
// ---------------------------------------------------------------------------

func TestMigrationReport_JSON(t *testing.T) {
	report := &MigrationReport{
		ConfigMerged:   3,
		MemoryFiles:    2,
		SkillsImported: 1,
		WorkspaceFiles: 4,
		CronJobs:       5,
		Warnings:       []string{"test warning"},
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded MigrationReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.ConfigMerged != 3 || decoded.MemoryFiles != 2 || decoded.SkillsImported != 1 {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
	if decoded.WorkspaceFiles != 4 || decoded.CronJobs != 5 {
		t.Errorf("new fields round-trip mismatch: %+v", decoded)
	}
	if len(decoded.Warnings) != 1 || decoded.Warnings[0] != "test warning" {
		t.Errorf("warnings mismatch: %v", decoded.Warnings)
	}
	if decoded.Errors != nil {
		t.Errorf("errors should be nil (omitempty): %v", decoded.Errors)
	}
}

// ---------------------------------------------------------------------------
// parseSoulRoleName
// ---------------------------------------------------------------------------

func TestParseSoulRoleName(t *testing.T) {
	tests := []struct {
		content  string
		fallback string
		want     string
	}{
		{"# SOUL.md — 琥珀（コハク / Kohaku）\n\n*創作擔當*", "x", "kohaku"},
		{"# SOUL.md — 黒曜（コクヨウ / Kokuyou）\n\n*工程擔當*", "x", "kokuyou"},
		{"# SOUL.md — 翡翠（ヒスイ / Hisui）\n\n*情報擔當*", "x", "hisui"},
		{"# SOUL.md - 琉璃的靈魂\n\n*我是琉璃（ルリ / Ruri），Takuma 的 AI 女僕。*", "x", "ruri"},
		{"# No romaji here\n\nplain text", "fallback", "fallback"},
		{"", "empty", "empty"},
	}

	for _, tt := range tests {
		dir := t.TempDir()
		path := filepath.Join(dir, "SOUL.md")
		os.WriteFile(path, []byte(tt.content), 0o644)

		got := parseSoulRoleName(path, tt.fallback)
		if got != tt.want {
			t.Errorf("parseSoulRoleName(%q) = %q, want %q", tt.content[:min(len(tt.content), 40)], got, tt.want)
		}
	}
}

func TestParseSoulRoleName_FileNotFound(t *testing.T) {
	got := parseSoulRoleName("/nonexistent/SOUL.md", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

// ---------------------------------------------------------------------------
// Invalid JSON
// ---------------------------------------------------------------------------

func TestMigrateOpenClaw_InvalidJSON(t *testing.T) {
	ocDir := t.TempDir()
	tetoraDir := t.TempDir()

	os.WriteFile(filepath.Join(ocDir, "openclaw.json"), []byte(`{invalid json`), 0o644)

	cfg := &Config{baseDir: tetoraDir}
	report, err := migrateOpenClaw(cfg, ocDir, false, parseIncludeList("config"), false)
	if err != nil {
		t.Fatalf("should not return top-level error for bad json: %v", err)
	}
	if len(report.Errors) == 0 {
		t.Fatal("expected error in report for invalid JSON")
	}
}
