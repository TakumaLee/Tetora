package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tetora/internal/classify"
	"tetora/internal/db"
)

// initDB creates all required tables for tool tests.
func initDB(dbPath string) {
	sql := `
CREATE TABLE IF NOT EXISTS agent_memory (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_memory_agent_key ON agent_memory(agent, key);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  session_id TEXT,
  channel_type TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL DEFAULT '',
  agent TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  message_count INTEGER DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS knowledge (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  filename TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  snippet TEXT NOT NULL DEFAULT '',
  indexed_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	cmd.Run()
}

func TestToolRegistry(t *testing.T) {
	cfg := &Config{Tools: ToolConfig{}}
	reg := NewToolRegistry(cfg)

	// Check built-in tools are registered.
	tools := reg.List()
	if len(tools) == 0 {
		t.Fatal("expected built-in tools to be registered")
	}

	// Check Get.
	tool, ok := reg.Get("read")
	if !ok {
		t.Fatal("expected read tool to be registered")
	}
	if tool.Name != "read" {
		t.Errorf("tool name = %q, want read", tool.Name)
	}

	// Check ListForProvider.
	forProvider := reg.ListForProvider()
	if len(forProvider) == 0 {
		t.Fatal("expected tools for provider")
	}
	for _, tool := range forProvider {
		if tool["name"] == "" {
			t.Error("tool missing name")
		}
	}
}

func TestToolRegistryDisableBuiltin(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			Builtin: map[string]bool{
				"exec":  false,
				"write": false,
			},
		},
	}
	reg := NewToolRegistry(cfg)

	if _, ok := reg.Get("exec"); ok {
		t.Error("exec tool should be disabled")
	}
	if _, ok := reg.Get("write"); ok {
		t.Error("write tool should be disabled")
	}
	if _, ok := reg.Get("read"); !ok {
		t.Error("read tool should be enabled")
	}
}

func TestLoopDetector(t *testing.T) {
	d := NewLoopDetector()
	input1 := json.RawMessage(`{"command": "ls"}`)
	input2 := json.RawMessage(`{"command": "pwd"}`)

	// First call: no loop.
	d.Record("exec", input1)
	isLoop, _ := d.Check("exec", input1)
	if isLoop {
		t.Error("expected no loop on first call")
	}

	// Second and third calls: no loop.
	d.Record("exec", input1)
	isLoop, _ = d.Check("exec", input1)
	if isLoop {
		t.Error("expected no loop on second call")
	}

	d.Record("exec", input1)
	isLoop, _ = d.Check("exec", input1)
	if !isLoop {
		t.Error("expected loop detected on third call (>= maxRep)")
	}

	// Different input: no loop.
	d.Record("exec", input2)
	isLoop, _ = d.Check("exec", input2)
	if isLoop {
		t.Error("expected no loop for different input")
	}
}

func TestToolExec(t *testing.T) {
	cfg := &Config{}
	ctx := context.Background()

	input := json.RawMessage(`{"command": "echo hello", "timeout": 5}`)
	result, err := toolExec(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolExec failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if !strings.Contains(res["stdout"].(string), "hello") {
		t.Errorf("stdout = %q, want contains 'hello'", res["stdout"])
	}
	if res["exitCode"].(float64) != 0 {
		t.Errorf("exitCode = %v, want 0", res["exitCode"])
	}
}

func TestToolExecTimeout(t *testing.T) {
	cfg := &Config{}
	ctx := context.Background()

	input := json.RawMessage(`{"command": "sleep 10", "timeout": 0.1}`)
	_, err := toolExec(ctx, cfg, input)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestToolRead(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	content := "line1\nline2\nline3\nline4"
	os.WriteFile(tmpFile, []byte(content), 0o644)

	cfg := &Config{}
	ctx := context.Background()

	// Read entire file.
	input := json.RawMessage(`{"path": "` + tmpFile + `"}`)
	result, err := toolRead(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolRead failed: %v", err)
	}
	if result != content {
		t.Errorf("result = %q, want %q", result, content)
	}

	// Read with offset.
	input = json.RawMessage(`{"path": "` + tmpFile + `", "offset": 2}`)
	result, err = toolRead(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolRead failed: %v", err)
	}
	if result != "line3\nline4" {
		t.Errorf("result = %q, want line3\\nline4", result)
	}

	// Read with limit.
	input = json.RawMessage(`{"path": "` + tmpFile + `", "limit": 2}`)
	result, err = toolRead(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolRead failed: %v", err)
	}
	if result != "line1\nline2" {
		t.Errorf("result = %q, want line1\\nline2", result)
	}
}

func TestToolWrite(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	cfg := &Config{}
	ctx := context.Background()

	input := json.RawMessage(`{"path": "` + tmpFile + `", "content": "hello"}`)
	result, err := toolWrite(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWrite failed: %v", err)
	}
	if !strings.Contains(result, "wrote 5 bytes") {
		t.Errorf("result = %q, want contains 'wrote 5 bytes'", result)
	}

	// Verify file contents.
	data, _ := os.ReadFile(tmpFile)
	if string(data) != "hello" {
		t.Errorf("file content = %q, want hello", string(data))
	}
}

func TestToolEdit(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(tmpFile, []byte("foo bar baz"), 0o644)

	cfg := &Config{}
	ctx := context.Background()

	input := json.RawMessage(`{"path": "` + tmpFile + `", "old_string": "bar", "new_string": "qux"}`)
	result, err := toolEdit(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolEdit failed: %v", err)
	}
	if !strings.Contains(result, "replaced 1 occurrence") {
		t.Errorf("result = %q, want contains 'replaced 1 occurrence'", result)
	}

	// Verify file contents.
	data, _ := os.ReadFile(tmpFile)
	if string(data) != "foo qux baz" {
		t.Errorf("file content = %q, want 'foo qux baz'", string(data))
	}
}

func TestToolEditNotUnique(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(tmpFile, []byte("foo foo foo"), 0o644)

	cfg := &Config{}
	ctx := context.Background()

	input := json.RawMessage(`{"path": "` + tmpFile + `", "old_string": "foo", "new_string": "bar"}`)
	_, err := toolEdit(ctx, cfg, input)
	if err == nil || !strings.Contains(err.Error(), "not unique") {
		t.Errorf("expected 'not unique' error, got %v", err)
	}
}

func TestToolWebFetch(t *testing.T) {
	// This test requires network access; skip if unavailable.
	cfg := &Config{}
	ctx := context.Background()

	input := json.RawMessage(`{"url": "https://example.com"}`)
	result, err := toolWebFetch(ctx, cfg, input)
	if err != nil {
		t.Skipf("toolWebFetch failed (network unavailable?): %v", err)
	}
	if !strings.Contains(result, "Example Domain") {
		t.Logf("result = %q", result)
		t.Error("expected result to contain 'Example Domain'")
	}
}

func TestToolMemorySearch(t *testing.T) {
	// toolMemorySearch uses filesystem-based memory, not DB.
	// Create a temp workspace with a memory file.
	tmpDir := t.TempDir()
	memDir := filepath.Join(tmpDir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "key1.md"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write memory file: %v", err)
	}

	cfg := &Config{WorkspaceDir: tmpDir}
	ctx := context.Background()

	input := json.RawMessage(`{"query": "hello"}`)
	result, err := toolMemorySearch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolMemorySearch failed: %v", err)
	}

	var res []map[string]string
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res) == 0 {
		t.Error("expected at least one result")
	}
	if len(res) > 0 && res[0]["key"] != "key1" {
		t.Errorf("key = %q, want key1", res[0]["key"])
	}
}

func TestToolMemoryGet(t *testing.T) {
	// toolMemoryGet uses filesystem-based memory, not DB.
	// Create a temp workspace with a memory file.
	tmpDir := t.TempDir()
	memDir := filepath.Join(tmpDir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "mykey.md"), []byte("myvalue"), 0o644); err != nil {
		t.Fatalf("write memory file: %v", err)
	}

	cfg := &Config{WorkspaceDir: tmpDir}
	ctx := context.Background()

	input := json.RawMessage(`{"key": "mykey"}`)
	result, err := toolMemoryGet(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolMemoryGet failed: %v", err)
	}
	if result != "myvalue" {
		t.Errorf("result = %q, want myvalue", result)
	}
}

func TestToolKnowledgeSearch(t *testing.T) {
	tmpDB := filepath.Join(t.TempDir(), "test.db")
	initDB(tmpDB)

	// Insert test knowledge.
	_, err := db.Query(tmpDB, `INSERT INTO knowledge (filename, content, snippet, indexed_at)
	                          VALUES ('doc.txt', 'machine learning algorithms', 'machine learning', datetime('now'))`)
	if err != nil {
		t.Fatalf("insert knowledge: %v", err)
	}

	cfg := &Config{HistoryDB: tmpDB}
	ctx := context.Background()

	input := json.RawMessage(`{"query": "learning", "limit": 5}`)
	result, err := toolKnowledgeSearch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolKnowledgeSearch failed: %v", err)
	}

	var res []map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res) == 0 {
		t.Error("expected at least one result")
	}
}

func TestToolSessionList(t *testing.T) {
	tmpDB := filepath.Join(t.TempDir(), "test.db")
	initDB(tmpDB)

	// Insert test session.
	_, err := db.Query(tmpDB, `INSERT INTO sessions (session_id, channel_type, channel_id, message_count, created_at, updated_at)
	                          VALUES ('sess1', 'telegram', '12345', 5, datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	cfg := &Config{HistoryDB: tmpDB}
	ctx := context.Background()

	input := json.RawMessage(`{}`)
	result, err := toolSessionList(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolSessionList failed: %v", err)
	}

	var res []map[string]string
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res) == 0 {
		t.Error("expected at least one result")
	}
	if len(res) > 0 && res[0]["session_id"] != "sess1" {
		t.Errorf("session_id = %q, want sess1", res[0]["session_id"])
	}
}

func TestToolMessage(t *testing.T) {
	cfg := &Config{
		Telegram: TelegramConfig{Enabled: false},
		Slack:    SlackBotConfig{Enabled: false},
		Discord:  DiscordBotConfig{Enabled: false},
	}
	ctx := context.Background()

	input := json.RawMessage(`{"channel": "telegram", "message": "test"}`)
	_, err := toolMessage(ctx, cfg, input)
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("expected 'not enabled' error, got %v", err)
	}
}

func TestToolCronList(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "jobs.json")
	jobs := []CronJobConfig{
		{ID: "1", Name: "test1", Schedule: "@hourly", Enabled: true, Task: CronTaskConfig{Prompt: "test prompt"}},
	}
	saveCronJobs(tmpFile, jobs)

	cfg := &Config{JobsFile: tmpFile}
	ctx := context.Background()

	input := json.RawMessage(`{}`)
	result, err := toolCronList(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolCronList failed: %v", err)
	}

	var res []map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("len(res) = %d, want 1", len(res))
	}
	if res[0]["name"] != "test1" {
		t.Errorf("name = %q, want test1", res[0]["name"])
	}
}

func TestToolCronCreate(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "jobs.json")
	cfg := &Config{JobsFile: tmpFile}
	ctx := context.Background()

	// Create new job.
	input := json.RawMessage(`{"name": "myjob", "schedule": "@daily", "prompt": "hello"}`)
	result, err := toolCronCreate(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolCronCreate failed: %v", err)
	}
	if !strings.Contains(result, "created") {
		t.Errorf("result = %q, want contains 'created'", result)
	}

	// Verify job was saved.
	jobs, _ := loadCronJobs(tmpFile)
	if len(jobs) != 1 {
		t.Fatalf("len(jobs) = %d, want 1", len(jobs))
	}
	if jobs[0].Name != "myjob" {
		t.Errorf("job name = %q, want myjob", jobs[0].Name)
	}

	// Update existing job.
	input = json.RawMessage(`{"name": "myjob", "schedule": "@hourly", "prompt": "updated"}`)
	result, err = toolCronCreate(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolCronCreate failed: %v", err)
	}
	if !strings.Contains(result, "updated") {
		t.Errorf("result = %q, want contains 'updated'", result)
	}

	jobs, _ = loadCronJobs(tmpFile)
	if jobs[0].Schedule != "@hourly" {
		t.Errorf("schedule = %q, want @hourly", jobs[0].Schedule)
	}
}

func TestToolCronDelete(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "jobs.json")
	jobs := []CronJobConfig{
		{ID: "1", Name: "job1", Schedule: "@hourly", Enabled: true, Task: CronTaskConfig{Prompt: "test"}},
		{ID: "2", Name: "job2", Schedule: "@daily", Enabled: true, Task: CronTaskConfig{Prompt: "test2"}},
	}
	saveCronJobs(tmpFile, jobs)

	cfg := &Config{JobsFile: tmpFile}
	ctx := context.Background()

	input := json.RawMessage(`{"name": "job1"}`)
	result, err := toolCronDelete(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolCronDelete failed: %v", err)
	}
	if !strings.Contains(result, "deleted") {
		t.Errorf("result = %q, want contains 'deleted'", result)
	}

	// Verify job was deleted.
	jobs, _ = loadCronJobs(tmpFile)
	if len(jobs) != 1 {
		t.Fatalf("len(jobs) = %d, want 1", len(jobs))
	}
	if jobs[0].Name != "job2" {
		t.Errorf("remaining job name = %q, want job2", jobs[0].Name)
	}

	// Delete non-existent job.
	input = json.RawMessage(`{"name": "nonexistent"}`)
	_, err = toolCronDelete(ctx, cfg, input)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %v", err)
	}
}

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"<p>hello</p>", "hello"},
		{"<a href='#'>link</a>", "link"},
		{"plain text", "plain text"},
		{"<div><span>nested</span></div>", "nested"},
		{"text <b>with</b> tags", "text with tags"},
	}

	for _, tt := range tests {
		got := stripHTMLTags(tt.input)
		if got != tt.want {
			t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- from tool_imagegen_test.go ---

func newImageGenTestServer(t *testing.T, authKey string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(405)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+authKey {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "unauthorized"},
			})
			return
		}

		// Decode request body to verify.
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"url":             "https://example.com/generated-image.png",
					"revised_prompt":  "a fluffy orange cat sitting on a windowsill",
				},
			},
		})
	}))
}

func TestToolImageGenerate(t *testing.T) {
	server := newImageGenTestServer(t, "test-key")
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	// Reset limiter.
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 1.00,
			Quality:    "standard",
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "a cat"})
	result, err := toolImageGenerate(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "https://example.com/generated-image.png") {
		t.Errorf("expected URL in result, got: %s", result)
	}
	if !strings.Contains(result, "dall-e-3") {
		t.Errorf("expected model in result, got: %s", result)
	}
	if !strings.Contains(result, "revised_prompt") || !strings.Contains(result, "fluffy orange cat") {
		// The revised prompt should appear in output.
		if !strings.Contains(result, "Revised prompt:") {
			t.Errorf("expected revised prompt in result, got: %s", result)
		}
	}
	if !strings.Contains(result, "$0.040") {
		t.Errorf("expected cost in result, got: %s", result)
	}
}

func TestToolImageGenerateCustomSize(t *testing.T) {
	server := newImageGenTestServer(t, "test-key")
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 5.00,
			Quality:    "hd",
		},
	}

	input, _ := json.Marshal(map[string]any{
		"prompt": "a landscape",
		"size":   "1792x1024",
	})
	result, err := toolImageGenerate(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "1792x1024") {
		t.Errorf("expected size in result, got: %s", result)
	}
	if !strings.Contains(result, "$0.120") {
		t.Errorf("expected HD large cost $0.120, got: %s", result)
	}
}

func TestToolImageGenerateInvalidSize(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]any{
		"prompt": "test",
		"size":   "512x512",
	})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for invalid size")
	}
	if !strings.Contains(err.Error(), "invalid size") {
		t.Errorf("expected invalid size error, got: %v", err)
	}
}

func TestToolImageGenerateEmptyPrompt(t *testing.T) {
	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled: true,
			APIKey:  "test-key",
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": ""})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
	if !strings.Contains(err.Error(), "prompt is required") {
		t.Errorf("expected prompt required error, got: %v", err)
	}
}

func TestToolImageGenerateNoAPIKey(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "apiKey not configured") {
		t.Errorf("expected apiKey error, got: %v", err)
	}
}

func TestToolImageGenerateDailyLimit(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 2,
			MaxCostDay: 10.00,
		},
	}

	// Simulate 2 previous generations.
	globalImageGenLimiter.Date = timeNowFormatDate()
	globalImageGenLimiter.Count = 2
	globalImageGenLimiter.CostUSD = 0.08

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for daily limit")
	}
	if !strings.Contains(err.Error(), "daily limit reached") {
		t.Errorf("expected daily limit error, got: %v", err)
	}
}

func TestToolImageGenerateCostLimit(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 100,
			MaxCostDay: 0.05,
		},
	}

	// Simulate cost already exceeded.
	globalImageGenLimiter.Date = timeNowFormatDate()
	globalImageGenLimiter.Count = 1
	globalImageGenLimiter.CostUSD = 0.06

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for cost limit")
	}
	if !strings.Contains(err.Error(), "daily cost limit reached") {
		t.Errorf("expected cost limit error, got: %v", err)
	}
}

func TestToolImageGenerateDailyLimitReset(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 2,
			MaxCostDay: 10.00,
		},
	}

	// Set a past date - should reset on check.
	globalImageGenLimiter.Date = "2020-01-01"
	globalImageGenLimiter.Count = 100
	globalImageGenLimiter.CostUSD = 999.00

	ok, _ := globalImageGenLimiter.Check(cfg)
	if !ok {
		t.Fatal("expected limit to reset for new day")
	}
	if globalImageGenLimiter.Count != 0 {
		t.Errorf("expected count reset to 0, got %d", globalImageGenLimiter.Count)
	}
	if globalImageGenLimiter.CostUSD != 0 {
		t.Errorf("expected cost reset to 0, got %f", globalImageGenLimiter.CostUSD)
	}
}

func TestToolImageGenerateStatus(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	// Set some usage.
	globalImageGenLimiter.Date = timeNowFormatDate()
	globalImageGenLimiter.Count = 3
	globalImageGenLimiter.CostUSD = 0.160

	result, err := toolImageGenerateStatus(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Generated: 3 / 10") {
		t.Errorf("expected generation count, got: %s", result)
	}
	if !strings.Contains(result, "$0.160") {
		t.Errorf("expected cost in result, got: %s", result)
	}
	if !strings.Contains(result, "Remaining: 7 images") {
		t.Errorf("expected remaining count, got: %s", result)
	}
}

func TestToolImageGenerateStatusEmpty(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			DailyLimit: 5,
			MaxCostDay: 2.00,
		},
	}

	result, err := toolImageGenerateStatus(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Generated: 0 / 5") {
		t.Errorf("expected zero usage, got: %s", result)
	}
	if !strings.Contains(result, "Remaining: 5 images") {
		t.Errorf("expected full remaining, got: %s", result)
	}
}

func TestToolImageGenerateStatusDefaultLimits(t *testing.T) {
	globalImageGenLimiter = &imageGenLimiter{}

	// No limits configured - should use defaults.
	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled: true,
		},
	}

	result, err := toolImageGenerateStatus(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default limit is 10, default max cost is 1.00.
	if !strings.Contains(result, "/ 10") {
		t.Errorf("expected default limit 10, got: %s", result)
	}
	if !strings.Contains(result, "$1.00") {
		t.Errorf("expected default max cost $1.00, got: %s", result)
	}
}

func TestToolImageGenerateAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "content policy violation",
			},
		})
	}))
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for API error")
	}
	if !strings.Contains(err.Error(), "content policy violation") {
		t.Errorf("expected API error message, got: %v", err)
	}
}

func TestToolImageGenerateAuthError(t *testing.T) {
	server := newImageGenTestServer(t, "correct-key")
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "wrong-key",
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for auth failure")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected unauthorized error, got: %v", err)
	}
}

func TestToolImageGenerateEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{},
		})
	}))
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			DailyLimit: 10,
			MaxCostDay: 1.00,
		},
	}

	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	_, err := toolImageGenerate(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
	if !strings.Contains(err.Error(), "no image generated") {
		t.Errorf("expected no image error, got: %v", err)
	}
}

func TestToolImageGenerateRecordUsage(t *testing.T) {
	server := newImageGenTestServer(t, "test-key")
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 1.00,
			Quality:    "standard",
		},
	}

	// Generate 3 images.
	for i := 0; i < 3; i++ {
		input, _ := json.Marshal(map[string]string{"prompt": "test"})
		_, err := toolImageGenerate(context.Background(), cfg, input)
		if err != nil {
			t.Fatalf("generation %d: unexpected error: %v", i+1, err)
		}
	}

	globalImageGenLimiter.Mu.Lock()
	if globalImageGenLimiter.Count != 3 {
		t.Errorf("expected count=3, got %d", globalImageGenLimiter.Count)
	}
	expectedCost := 0.040 * 3
	if globalImageGenLimiter.CostUSD < expectedCost-0.001 || globalImageGenLimiter.CostUSD > expectedCost+0.001 {
		t.Errorf("expected cost ~$%.3f, got $%.3f", expectedCost, globalImageGenLimiter.CostUSD)
	}
	globalImageGenLimiter.Mu.Unlock()
}

func TestToolImageGenerateQualityOverride(t *testing.T) {
	// Track what quality was sent to the API.
	var receivedQuality string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		if q, ok := reqBody["quality"].(string); ok {
			receivedQuality = q
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"url": "https://example.com/img.png", "revised_prompt": ""},
			},
		})
	}))
	defer server.Close()

	old := imageGenBaseURL
	imageGenBaseURL = server.URL
	defer func() { imageGenBaseURL = old }()

	globalImageGenLimiter = &imageGenLimiter{}

	cfg := &Config{
		ImageGen: ImageGenConfig{
			Enabled:    true,
			APIKey:     "test-key",
			Model:      "dall-e-3",
			DailyLimit: 10,
			MaxCostDay: 5.00,
			Quality:    "standard", // Default config quality.
		},
	}

	// Override quality to hd via input args.
	input, _ := json.Marshal(map[string]any{
		"prompt":  "test",
		"quality": "hd",
	})
	result, err := toolImageGenerate(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedQuality != "hd" {
		t.Errorf("expected quality=hd sent to API, got %q", receivedQuality)
	}
	if !strings.Contains(result, "$0.080") {
		t.Errorf("expected HD cost $0.080, got: %s", result)
	}
}

func TestEstimateImageCost(t *testing.T) {
	tests := []struct {
		model, quality, size string
		want                 float64
	}{
		{"dall-e-3", "standard", "1024x1024", 0.040},
		{"dall-e-3", "hd", "1024x1024", 0.080},
		{"dall-e-3", "standard", "1024x1792", 0.080},
		{"dall-e-3", "standard", "1792x1024", 0.080},
		{"dall-e-3", "hd", "1024x1792", 0.120},
		{"dall-e-3", "hd", "1792x1024", 0.120},
		{"dall-e-2", "standard", "1024x1024", 0.020},
		{"dall-e-2", "hd", "1024x1024", 0.020},
		{"", "standard", "1024x1024", 0.040},   // empty model defaults to dall-e-3 pricing
		{"", "hd", "1024x1792", 0.120},          // empty model defaults to dall-e-3 pricing
	}
	for _, tt := range tests {
		got := estimateImageCost(tt.model, tt.quality, tt.size)
		if got != tt.want {
			t.Errorf("estimateImageCost(%q, %q, %q) = %f, want %f",
				tt.model, tt.quality, tt.size, got, tt.want)
		}
	}
}

func TestImageGenLimiterCheck(t *testing.T) {
	cfg := &Config{
		ImageGen: ImageGenConfig{
			DailyLimit: 5,
			MaxCostDay: 0.50,
		},
	}

	l := &imageGenLimiter{}

	// Fresh limiter should pass.
	ok, reason := l.Check(cfg)
	if !ok {
		t.Fatalf("expected ok, got blocked: %s", reason)
	}

	// At limit should block.
	l.Date = timeNowFormatDate()
	l.Count = 5
	ok, reason = l.Check(cfg)
	if ok {
		t.Fatal("expected blocked at daily limit")
	}
	if !strings.Contains(reason, "daily limit reached") {
		t.Errorf("unexpected reason: %s", reason)
	}

	// Cost limit should block.
	l.Count = 1
	l.CostUSD = 0.55
	ok, reason = l.Check(cfg)
	if ok {
		t.Fatal("expected blocked at cost limit")
	}
	if !strings.Contains(reason, "daily cost limit reached") {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestImageGenLimiterRecord(t *testing.T) {
	l := &imageGenLimiter{}
	l.Record(0.040)
	l.Record(0.080)

	if l.Count != 2 {
		t.Errorf("expected count=2, got %d", l.Count)
	}
	if l.CostUSD < 0.119 || l.CostUSD > 0.121 {
		t.Errorf("expected cost ~$0.120, got $%.3f", l.CostUSD)
	}
}

// timeNowFormatDate returns today's date string matching the limiter format.
func timeNowFormatDate() string {
	return timeNowFormat("2006-01-02")
}

func timeNowFormat(layout string) string {
	return time.Now().Format(layout)
}

// --- from tool_policy_test.go ---

// TestProfileResolution tests tool profile resolution.
func TestProfileResolution(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			Profiles: map[string]ToolProfile{
				"custom": {
					Name:  "custom",
					Allow: []string{"read", "write"},
				},
			},
		},
	}

	tests := []struct {
		name         string
		profileName  string
		wantLen      int
		wantContains []string
	}{
		{
			name:         "minimal profile",
			profileName:  "minimal",
			wantLen:      3,
			wantContains: []string{"memory_search", "memory_get", "knowledge_search"},
		},
		{
			name:         "standard profile",
			profileName:  "standard",
			wantLen:      9,
			wantContains: []string{"read", "write", "exec", "memory_search"},
		},
		{
			name:         "custom profile",
			profileName:  "custom",
			wantLen:      2,
			wantContains: []string{"read", "write"},
		},
		{
			name:         "default to standard",
			profileName:  "",
			wantLen:      9,
			wantContains: []string{"read", "write", "exec"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := getProfile(cfg, tt.profileName)
			if len(profile.Allow) != tt.wantLen {
				t.Errorf("got %d tools, want %d", len(profile.Allow), tt.wantLen)
			}
			for _, tool := range tt.wantContains {
				found := false
				for _, allowed := range profile.Allow {
					if allowed == tool {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("profile missing expected tool: %s", tool)
				}
			}
		})
	}
}

// TestAllowDenyMerge tests allow/deny list merging.
func TestAllowDenyMerge(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{},
		Agents: map[string]AgentConfig{
			"test1": {
				ToolPolicy: AgentToolPolicy{
					Profile: "minimal",
					Allow:   []string{"read", "write"},
					Deny:    []string{"memory_search"},
				},
			},
			"test2": {
				ToolPolicy: AgentToolPolicy{
					Profile: "standard",
					Deny:    []string{"exec", "edit"},
				},
			},
		},
	}
	cfg.Runtime.ToolRegistry = NewToolRegistry(cfg)

	// Test role test1: minimal + read,write - memory_search
	allowed := resolveAllowedTools(cfg, "test1")
	if allowed["memory_search"] {
		t.Error("memory_search should be denied")
	}
	if !allowed["read"] {
		t.Error("read should be allowed")
	}
	if !allowed["write"] {
		t.Error("write should be allowed")
	}
	if !allowed["memory_get"] {
		t.Error("memory_get from minimal should be allowed")
	}

	// Test role test2: standard - exec,edit
	allowed = resolveAllowedTools(cfg, "test2")
	if allowed["exec"] {
		t.Error("exec should be denied")
	}
	if allowed["edit"] {
		t.Error("edit should be denied")
	}
	if !allowed["read"] {
		t.Error("read from standard should be allowed")
	}
}

// TestTrustLevelFiltering tests trust-level filtering.
func TestTrustLevelFiltering(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{},
		Agents: map[string]AgentConfig{
			"observer": {TrustLevel: TrustObserve},
			"suggester": {TrustLevel: TrustSuggest},
			"auto": {TrustLevel: TrustAuto},
		},
	}

	call := ToolCall{
		ID:    "test-1",
		Name:  "exec",
		Input: json.RawMessage(`{"command":"echo test"}`),
	}

	// Test observe mode.
	result, shouldExec := filterToolCall(cfg, "observer", call)
	if shouldExec {
		t.Error("observe mode should not execute")
	}
	if result == nil {
		t.Fatal("observe mode should return result")
	}
	if !containsString(result.Content, "OBSERVE MODE") {
		t.Errorf("observe result should contain 'OBSERVE MODE', got: %s", result.Content)
	}

	// Test suggest mode.
	result, shouldExec = filterToolCall(cfg, "suggester", call)
	if shouldExec {
		t.Error("suggest mode should not execute")
	}
	if result == nil {
		t.Fatal("suggest mode should return result")
	}
	if !containsString(result.Content, "APPROVAL REQUIRED") {
		t.Errorf("suggest result should contain 'APPROVAL REQUIRED', got: %s", result.Content)
	}

	// Test auto mode.
	result, shouldExec = filterToolCall(cfg, "auto", call)
	if !shouldExec {
		t.Error("auto mode should execute")
	}
	if result != nil {
		t.Error("auto mode should return nil result")
	}
}

// TestToolTrustOverride tests per-tool trust overrides.
func TestToolTrustOverride(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			TrustOverride: map[string]string{
				"exec": TrustSuggest,
			},
		},
		Agents: map[string]AgentConfig{
			"test": {TrustLevel: TrustAuto},
		},
	}

	// exec should be suggest due to override, even though role is auto.
	level := getToolTrustLevel(cfg, "test", "exec")
	if level != TrustSuggest {
		t.Errorf("got trust level %s, want %s", level, TrustSuggest)
	}

	// read should be auto (no override).
	level = getToolTrustLevel(cfg, "test", "read")
	if level != TrustAuto {
		t.Errorf("got trust level %s, want %s", level, TrustAuto)
	}
}

// TestLoopDetection tests the enhanced loop detector.
func TestLoopDetection(t *testing.T) {
	detector := NewLoopDetector()

	input1 := json.RawMessage(`{"path":"/test"}`)
	input2 := json.RawMessage(`{"path":"/other"}`)

	// Same tool, same input - should detect loop after maxRepeat.
	detector.Record("read", input1)
	isLoop, _ := detector.Check("read", input1)
	if isLoop {
		t.Error("should not detect loop on first repeat")
	}

	detector.Record("read", input1)
	isLoop, _ = detector.Check("read", input1)
	if isLoop {
		t.Error("should not detect loop on second repeat")
	}

	detector.Record("read", input1)
	isLoop, msg := detector.Check("read", input1)
	if !isLoop {
		t.Error("should detect loop on third repeat")
	}
	if !containsString(msg, "loop detected") {
		t.Errorf("loop message should contain 'loop detected', got: %s", msg)
	}

	// Different input - no loop.
	detector.Reset()
	detector.Record("read", input1)
	detector.Record("read", input2)
	isLoop, _ = detector.Check("read", input1)
	if isLoop {
		t.Error("should not detect loop with different inputs")
	}
}

// TestLoopPatternDetection tests multi-tool pattern detection.
func TestLoopPatternDetection(t *testing.T) {
	detector := NewLoopDetector()

	input := json.RawMessage(`{"test":"value"}`)

	// Create A→B→A→B→A→B pattern.
	for i := 0; i < 6; i++ {
		if i%2 == 0 {
			detector.Record("toolA", input)
		} else {
			detector.Record("toolB", input)
		}
	}

	isLoop, msg := detector.detectToolLoopPattern()
	if !isLoop {
		t.Error("should detect repeating pattern")
	}
	if !containsString(msg, "pattern detected") {
		t.Errorf("pattern message should contain 'pattern detected', got: %s", msg)
	}
}

// TestLoopHistoryLimit tests that history is trimmed to maxHistory.
func TestLoopHistoryLimit(t *testing.T) {
	detector := NewLoopDetector()
	detector.maxHistory = 5

	input := json.RawMessage(`{"test":"value"}`)

	// Record 10 entries.
	for i := 0; i < 10; i++ {
		detector.Record("test", input)
	}

	// History should be trimmed to 5.
	if len(detector.history) != 5 {
		t.Errorf("got history length %d, want 5", len(detector.history))
	}
}

// TestFullProfileWildcard tests the "*" wildcard in full profile.
func TestFullProfileWildcard(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{},
		Agents: map[string]AgentConfig{
			"admin": {
				ToolPolicy: AgentToolPolicy{
					Profile: "full",
				},
			},
		},
	}
	cfg.Runtime.ToolRegistry = NewToolRegistry(cfg)

	allowed := resolveAllowedTools(cfg, "admin")

	// Should have all registered tools.
	allTools := cfg.Runtime.ToolRegistry.(*ToolRegistry).List()
	if len(allowed) != len(allTools) {
		t.Errorf("full profile should allow all tools, got %d, want %d", len(allowed), len(allTools))
	}

	for _, tool := range allTools {
		if !allowed[tool.Name] {
			t.Errorf("full profile should allow %s", tool.Name)
		}
	}
}

// TestToolPolicySummary tests the summary generation.
func TestToolPolicySummary(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{},
		Agents: map[string]AgentConfig{
			"test": {
				ToolPolicy: AgentToolPolicy{
					Profile: "standard",
					Allow:   []string{"extra_tool"},
					Deny:    []string{"exec"},
				},
			},
		},
	}
	cfg.Runtime.ToolRegistry = NewToolRegistry(cfg)

	summary := getToolPolicySummary(cfg, "test")

	if !containsString(summary, "standard") {
		t.Error("summary should contain profile name")
	}
	if !containsString(summary, "Allowed:") {
		t.Error("summary should contain allowed count")
	}
}

// containsString is defined in proactive_test.go

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- P28.0: Approval Gate Tests ---

func TestNeedsApproval(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		tools    []string
		toolName string
		want     bool
	}{
		{"disabled", false, []string{"exec"}, "exec", false},
		{"enabled, tool in list", true, []string{"exec", "write"}, "exec", true},
		{"enabled, tool not in list", true, []string{"exec", "write"}, "read", false},
		{"enabled, empty list", true, nil, "exec", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				ApprovalGates: ApprovalGateConfig{
					Enabled: tt.enabled,
					Tools:   tt.tools,
				},
			}
			got := needsApproval(cfg, tt.toolName)
			if got != tt.want {
				t.Errorf("needsApproval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSummarizeToolCall(t *testing.T) {
	tests := []struct {
		name       string
		tc         ToolCall
		wantSubstr string
	}{
		{
			"exec",
			ToolCall{Name: "exec", Input: json.RawMessage(`{"command":"ls -la"}`)},
			"Run command: ls -la",
		},
		{
			"write",
			ToolCall{Name: "write", Input: json.RawMessage(`{"path":"/tmp/test.txt"}`)},
			"Write file: /tmp/test.txt",
		},
		{
			"email_send",
			ToolCall{Name: "email_send", Input: json.RawMessage(`{"to":"user@example.com"}`)},
			"Send email to: user@example.com",
		},
		{
			"generic",
			ToolCall{Name: "custom_tool", Input: json.RawMessage(`{"key":"value"}`)},
			"Execute custom_tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeToolCall(tt.tc)
			if !containsString(got, tt.wantSubstr) {
				t.Errorf("summarizeToolCall() = %q, want to contain %q", got, tt.wantSubstr)
			}
		})
	}
}

// mockApprovalGate is a test implementation of ApprovalGate.
type mockApprovalGate struct {
	respondWith  bool
	respondErr   error
	delay        time.Duration
	autoApproved map[string]bool
}

func (m *mockApprovalGate) RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return m.respondWith, m.respondErr
}

func (m *mockApprovalGate) AutoApprove(toolName string) {
	if m.autoApproved == nil {
		m.autoApproved = make(map[string]bool)
	}
	m.autoApproved[toolName] = true
}

func (m *mockApprovalGate) IsAutoApproved(toolName string) bool {
	if m.autoApproved == nil {
		return false
	}
	return m.autoApproved[toolName]
}

func TestApprovalGateTimeout(t *testing.T) {
	gate := &mockApprovalGate{delay: 5 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	approved, err := gate.RequestApproval(ctx, ApprovalRequest{
		ID:   "test-1",
		Tool: "exec",
	})

	if approved {
		t.Error("should not be approved on timeout")
	}
	if err == nil {
		t.Error("should return error on timeout")
	}
}

func TestGateReason(t *testing.T) {
	if r := gateReason(nil, false); r != "rejected by user" {
		t.Errorf("got %q, want %q", r, "rejected by user")
	}
	if r := gateReason(fmt.Errorf("timeout"), false); r != "timeout" {
		t.Errorf("got %q, want %q", r, "timeout")
	}
	if r := gateReason(nil, true); r != "approved" {
		t.Errorf("got %q, want %q", r, "approved")
	}
}

func TestAutoApproveFlow(t *testing.T) {
	gate := &mockApprovalGate{respondWith: false}

	// Initially not auto-approved.
	if gate.IsAutoApproved("exec") {
		t.Error("exec should not be auto-approved initially")
	}

	// Auto-approve exec.
	gate.AutoApprove("exec")

	if !gate.IsAutoApproved("exec") {
		t.Error("exec should be auto-approved after AutoApprove")
	}

	// Other tools still not approved.
	if gate.IsAutoApproved("write") {
		t.Error("write should not be auto-approved")
	}
}

func TestConfigAutoApproveTools(t *testing.T) {
	cfg := &Config{
		ApprovalGates: ApprovalGateConfig{
			Enabled:          true,
			Tools:            []string{"exec", "write", "delete"},
			AutoApproveTools: []string{"exec"},
		},
	}

	// exec needs approval per config.
	if !needsApproval(cfg, "exec") {
		t.Error("exec should need approval")
	}

	// Simulate what dispatch.go does: check auto-approved before requesting.
	gate := &mockApprovalGate{}
	// Pre-load from config.
	for _, tool := range cfg.ApprovalGates.AutoApproveTools {
		gate.AutoApprove(tool)
	}

	// exec is auto-approved → skip gate.
	if !gate.IsAutoApproved("exec") {
		t.Error("exec should be auto-approved from config")
	}

	// write still needs full approval.
	if gate.IsAutoApproved("write") {
		t.Error("write should not be auto-approved")
	}

	// delete still needs full approval.
	if gate.IsAutoApproved("delete") {
		t.Error("delete should not be auto-approved")
	}
}

// --- from tool_complexity_test.go ---

func TestToolsForComplexity(t *testing.T) {
	tests := []struct {
		name       string
		complexity classify.Complexity
		want       string
	}{
		{"simple returns none", classify.Simple, "none"},
		{"standard returns standard", classify.Standard, "standard"},
		{"complex returns full", classify.Complex, "full"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolsForComplexity(tt.complexity)
			if got != tt.want {
				t.Errorf("ToolsForComplexity(%v) = %q, want %q", tt.complexity, got, tt.want)
			}
		})
	}
}

func TestToolsForComplexityProfileIntegration(t *testing.T) {
	// Verify that the profile returned by ToolsForComplexity is handled
	// correctly by ToolsForProfile.

	// "none" profile should return nil from ToolsForProfile (unknown profile).
	profile := ToolsForComplexity(classify.Simple)
	if profile != "none" {
		t.Fatalf("expected 'none' for simple, got %q", profile)
	}
	allowed := ToolsForProfile(profile)
	if allowed != nil {
		t.Error("ToolsForProfile('none') should return nil (unknown profile)")
	}

	// "standard" should return a non-nil set with known tools.
	profile = ToolsForComplexity(classify.Standard)
	if profile != "standard" {
		t.Fatalf("expected 'standard', got %q", profile)
	}
	allowed = ToolsForProfile(profile)
	if allowed == nil {
		t.Fatal("ToolsForProfile('standard') should return non-nil tool set")
	}
	if !allowed["memory_get"] {
		t.Error("standard profile should include memory_get")
	}
	if !allowed["web_search"] {
		t.Error("standard profile should include web_search")
	}

	// "full" should return nil (all tools).
	profile = ToolsForComplexity(classify.Complex)
	if profile != "full" {
		t.Fatalf("expected 'full', got %q", profile)
	}
	allowed = ToolsForProfile(profile)
	if allowed != nil {
		t.Error("ToolsForProfile('full') should return nil (all tools)")
	}
}

// --- from tool_web_test.go ---

// --- Web Fetch Tests ---

func TestWebFetch_HTML(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><h1>Test Page</h1><p>This is a test.</p></body></html>"))
	}))
	defer mockServer.Close()

	cfg := &Config{}
	ctx := context.Background()
	input := json.RawMessage(`{"url": "` + mockServer.URL + `"}`)

	result, err := toolWebFetch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebFetch failed: %v", err)
	}

	if !strings.Contains(result, "Test Page") {
		t.Errorf("expected 'Test Page' in result, got: %s", result)
	}
	if !strings.Contains(result, "This is a test") {
		t.Errorf("expected 'This is a test' in result, got: %s", result)
	}
	if strings.Contains(result, "<html>") || strings.Contains(result, "<body>") {
		t.Errorf("expected HTML tags to be stripped, got: %s", result)
	}
}

func TestWebFetch_PlainText(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Plain text content"))
	}))
	defer mockServer.Close()

	cfg := &Config{}
	ctx := context.Background()
	input := json.RawMessage(`{"url": "` + mockServer.URL + `"}`)

	result, err := toolWebFetch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebFetch failed: %v", err)
	}

	if result != "Plain text content" {
		t.Errorf("expected 'Plain text content', got: %s", result)
	}
}

func TestWebFetch_MaxLength(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a long HTML page.
		longContent := strings.Repeat("<p>Lorem ipsum dolor sit amet. </p>", 1000)
		w.Write([]byte("<html><body>" + longContent + "</body></html>"))
	}))
	defer mockServer.Close()

	cfg := &Config{}
	ctx := context.Background()
	input := json.RawMessage(`{"url": "` + mockServer.URL + `", "maxLength": 100}`)

	result, err := toolWebFetch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebFetch failed: %v", err)
	}

	if len(result) > 100 {
		t.Errorf("expected result length <= 100, got %d", len(result))
	}
}

func TestWebFetch_Timeout(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than timeout.
		ctx := r.Context()
		select {
		case <-ctx.Done():
			return
		}
	}))
	defer mockServer.Close()

	cfg := &Config{}
	ctx := context.Background()
	input := json.RawMessage(`{"url": "` + mockServer.URL + `"}`)

	_, err := toolWebFetch(ctx, cfg, input)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestWebFetch_InvalidURL(t *testing.T) {
	cfg := &Config{}
	ctx := context.Background()
	input := json.RawMessage(`{"url": "not-a-valid-url"}`)

	_, err := toolWebFetch(ctx, cfg, input)
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}
