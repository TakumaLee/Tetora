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
	"tetora/internal/classify"
	"tetora/internal/db"
	"tetora/internal/integration/notes"
	"tetora/internal/life/contacts"
	"tetora/internal/life/profile"
	"tetora/internal/life/tasks"
	"tetora/internal/log"
	"tetora/internal/nlp"
	"time"
	bpkg "tetora/internal/automation/briefing"
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

// --- from briefing_test.go ---

// --- Test helpers ---

func setupBriefingTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	// Create minimal tables that briefing queries against.
	ddl := `
CREATE TABLE IF NOT EXISTS reminders (
    id TEXT PRIMARY KEY,
    message TEXT NOT NULL,
    remind_at TEXT NOT NULL,
    status TEXT DEFAULT 'pending'
);
CREATE TABLE IF NOT EXISTS history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel TEXT DEFAULT '',
    timestamp TEXT NOT NULL,
    message TEXT DEFAULT ''
);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init briefing test DB: %v: %s", err, string(out))
	}
	// Also init habits, user_tasks, goals, expenses tables.
	if err := initHabitsDB(dbPath); err != nil {
		t.Fatalf("initHabitsDB: %v", err)
	}
	if err := initTaskManagerDB(dbPath); err != nil {
		t.Fatalf("initTaskManagerDB: %v", err)
	}
	if err := initGoalsDB(dbPath); err != nil {
		t.Fatalf("initGoalsDB: %v", err)
	}
	if err := initFinanceDB(dbPath); err != nil {
		t.Fatalf("initFinanceDB: %v", err)
	}
	return dbPath
}

// setupBriefingService creates a briefing service with all optional globals cleared
// to nil for isolation. Callers that need a specific global must set it BEFORE calling
// this function so it gets captured into the service's deps.
func setupBriefingService(t *testing.T) (*bpkg.Service, string, func()) {
	t.Helper()
	dbPath := setupBriefingTestDB(t)
	cfg := &Config{HistoryDB: dbPath}

	// Save globals.
	oldScheduling := globalSchedulingService
	oldContacts := globalContactsService
	oldHabits := globalHabitsService
	oldGoals := globalGoalsService
	oldFinance := globalFinanceService
	oldTaskMgr := globalTaskManager
	oldInsights := globalInsightsEngine

	// Clear all globals for isolated test.
	globalSchedulingService = nil
	globalContactsService = nil
	globalHabitsService = nil
	globalGoalsService = nil
	globalFinanceService = nil
	globalTaskManager = nil
	globalInsightsEngine = nil

	svc := newBriefingService(cfg)

	cleanup := func() {
		globalSchedulingService = oldScheduling
		globalContactsService = oldContacts
		globalHabitsService = oldHabits
		globalGoalsService = oldGoals
		globalFinanceService = oldFinance
		globalTaskManager = oldTaskMgr
		globalInsightsEngine = oldInsights
	}

	return svc, dbPath, cleanup
}

// testBriefingAppCtx creates a context with an App containing the given BriefingService.
func testBriefingAppCtx(svc *bpkg.Service) context.Context {
	app := &App{Briefing: svc}
	return withApp(context.Background(), app)
}

func briefingExecSQL(t *testing.T, dbPath, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("briefingExecSQL: %v: %s", err, string(out))
	}
}

// --- Constructor ---

func TestNewBriefingService(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	svc := newBriefingService(cfg)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.DBPath() != cfg.HistoryDB {
		t.Errorf("expected dbPath %s, got %s", cfg.HistoryDB, svc.DBPath())
	}
}

// --- Greeting tests ---

func TestMorningGreeting(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{})

	tests := []struct {
		name string
		hour int
		want string
	}{
		{"early_bird", 4, "Early bird!"},
		{"morning", 8, "Good morning!"},
		{"afternoon", 14, "Hello!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			date := time.Date(2026, 2, 23, tt.hour, 0, 0, 0, time.UTC)
			greeting := svc.MorningGreeting(date)
			if !strings.Contains(greeting, tt.want) {
				t.Errorf("MorningGreeting(%d:00) = %q, want to contain %q", tt.hour, greeting, tt.want)
			}
			// Should always contain the weekday and formatted date.
			if !strings.Contains(greeting, "Monday") {
				t.Errorf("greeting %q should contain weekday", greeting)
			}
			if !strings.Contains(greeting, "February 23, 2026") {
				t.Errorf("greeting %q should contain formatted date", greeting)
			}
		})
	}
}

func TestEveningGreeting(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{})
	date := time.Date(2026, 2, 23, 20, 0, 0, 0, time.UTC) // Monday
	greeting := svc.EveningGreeting(date)
	if !strings.Contains(greeting, "Good evening!") {
		t.Errorf("EveningGreeting = %q, want to contain 'Good evening!'", greeting)
	}
	if !strings.Contains(greeting, "Monday") {
		t.Errorf("EveningGreeting = %q, want to contain weekday", greeting)
	}
}

// --- Morning/Evening with no services ---

func TestGenerateMorning_NoServices(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	date := time.Date(2026, 2, 23, 8, 0, 0, 0, time.UTC)
	briefing, err := svc.GenerateMorning(date)
	if err != nil {
		t.Fatalf("GenerateMorning: %v", err)
	}
	if briefing.Type != "morning" {
		t.Errorf("expected type morning, got %s", briefing.Type)
	}
	if briefing.Date != "2026-02-23" {
		t.Errorf("expected date 2026-02-23, got %s", briefing.Date)
	}
	if briefing.Greeting == "" {
		t.Error("expected non-empty greeting")
	}
	// With no services and no data, sections should be empty.
	if len(briefing.Sections) != 0 {
		t.Errorf("expected 0 sections with no services, got %d", len(briefing.Sections))
	}
	if briefing.Quote == "" {
		t.Error("expected non-empty quote")
	}
	if briefing.GeneratedAt == "" {
		t.Error("expected non-empty generated_at")
	}
}

func TestGenerateEvening_NoServices(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	date := time.Date(2026, 2, 23, 20, 0, 0, 0, time.UTC)
	briefing, err := svc.GenerateEvening(date)
	if err != nil {
		t.Fatalf("GenerateEvening: %v", err)
	}
	if briefing.Type != "evening" {
		t.Errorf("expected type evening, got %s", briefing.Type)
	}
	if briefing.Date != "2026-02-23" {
		t.Errorf("expected date 2026-02-23, got %s", briefing.Date)
	}
	if briefing.Greeting == "" {
		t.Error("expected non-empty greeting")
	}
	if len(briefing.Sections) != 0 {
		t.Errorf("expected 0 sections with no services, got %d", len(briefing.Sections))
	}
	if briefing.Quote == "" {
		t.Error("expected non-empty reflection prompt")
	}
}

// --- FormatBriefing ---

func TestFormatBriefing(t *testing.T) {
	briefing := &bpkg.Briefing{
		Type:     "morning",
		Date:     "2026-02-23",
		Greeting: "Good morning! It's Monday, February 23, 2026.",
		Sections: []bpkg.BriefingSection{
			{
				Title:   "Today's Schedule",
				Icon:    "calendar",
				Items:   []string{"09:00 -- Standup", "14:00 -- Review"},
				Summary: "2 events today",
			},
			{
				Title:   "Tasks Due Today",
				Icon:    "check",
				Items:   []string{"[URGENT] Fix bug"},
				Summary: "1 tasks due",
			},
		},
		Quote:       "The secret of getting ahead is getting started. -- Mark Twain",
		GeneratedAt: "2026-02-23T08:00:00Z",
	}

	output := bpkg.FormatBriefing(briefing)

	// Check header.
	if !strings.Contains(output, "## Morning Briefing -- 2026-02-23") {
		t.Errorf("missing header in output:\n%s", output)
	}
	if !strings.Contains(output, "Good morning!") {
		t.Errorf("missing greeting in output:\n%s", output)
	}
	// Check sections.
	if !strings.Contains(output, "### calendar Today's Schedule") {
		t.Errorf("missing schedule section in output:\n%s", output)
	}
	if !strings.Contains(output, "- 09:00 -- Standup") {
		t.Errorf("missing schedule item in output:\n%s", output)
	}
	if !strings.Contains(output, "*2 events today*") {
		t.Errorf("missing summary in output:\n%s", output)
	}
	if !strings.Contains(output, "[URGENT] Fix bug") {
		t.Errorf("missing task item in output:\n%s", output)
	}
	// Check quote (morning = blockquote).
	if !strings.Contains(output, "> The secret of getting ahead") {
		t.Errorf("missing quote in output:\n%s", output)
	}
}

func TestFormatBriefing_Evening(t *testing.T) {
	briefing := &bpkg.Briefing{
		Type:     "evening",
		Date:     "2026-02-23",
		Greeting: "Good evening! Here's your Monday wrap-up.",
		Quote:    "What was the best part of your day?",
	}

	output := bpkg.FormatBriefing(briefing)

	if !strings.Contains(output, "## Evening Briefing -- 2026-02-23") {
		t.Errorf("missing header in output:\n%s", output)
	}
	// Evening uses **Reflection:** prefix.
	if !strings.Contains(output, "**Reflection:** What was the best part") {
		t.Errorf("missing reflection in output:\n%s", output)
	}
}

func TestFormatBriefing_EmptySections(t *testing.T) {
	briefing := &bpkg.Briefing{
		Type:     "morning",
		Date:     "2026-01-01",
		Greeting: "Hello!",
	}
	output := bpkg.FormatBriefing(briefing)
	if !strings.Contains(output, "## Morning Briefing") {
		t.Errorf("missing header in empty briefing output:\n%s", output)
	}
	if !strings.Contains(output, "Hello!") {
		t.Errorf("missing greeting in empty briefing output:\n%s", output)
	}
}

// --- Quote / Reflection variation ---

func TestDailyQuote_DifferentDays(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{})
	seen := make(map[string]bool)
	for day := 1; day <= 7; day++ {
		date := time.Date(2026, 1, day, 8, 0, 0, 0, time.UTC)
		q := svc.DailyQuote(date)
		if q == "" {
			t.Errorf("empty quote for day %d", day)
		}
		seen[q] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected different quotes for different days, got %d unique", len(seen))
	}
}

func TestEveningReflection_DifferentDays(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{})
	seen := make(map[string]bool)
	for day := 1; day <= 7; day++ {
		date := time.Date(2026, 1, day, 20, 0, 0, 0, time.UTC)
		p := svc.EveningReflection(date)
		if p == "" {
			t.Errorf("empty reflection for day %d", day)
		}
		seen[p] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected different reflections for different days, got %d unique", len(seen))
	}
}

// --- capitalizeFirst ---

func TestCapitalizeFirst(t *testing.T) {
	tests := []struct{ in, want string }{
		{"morning", "Morning"},
		{"evening", "Evening"},
		{"", ""},
		{"a", "A"},
		{"ABC", "ABC"},
	}
	for _, tt := range tests {
		got := bpkg.CapitalizeFirst(tt.in)
		if got != tt.want {
			t.Errorf("bpkg.CapitalizeFirst(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- Tool handler tests ---

func TestToolBriefingMorning_NotInitialized(t *testing.T) {
	ctx := withApp(context.Background(), &App{})
	_, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("expected 'not initialized' error, got: %v", err)
	}
}

func TestToolBriefingEvening_NotInitialized(t *testing.T) {
	ctx := withApp(context.Background(), &App{})
	_, err := toolBriefingEvening(ctx, &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("expected 'not initialized' error, got: %v", err)
	}
}

func TestToolBriefingMorning(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	result, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolBriefingMorning: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
	if !strings.Contains(result, "Morning Briefing") {
		t.Errorf("result should contain 'Morning Briefing', got:\n%s", result)
	}
}

func TestToolBriefingEvening(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	result, err := toolBriefingEvening(ctx, &Config{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolBriefingEvening: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
	if !strings.Contains(result, "Evening Briefing") {
		t.Errorf("result should contain 'Evening Briefing', got:\n%s", result)
	}
	if !strings.Contains(result, "Reflection:") {
		t.Errorf("result should contain reflection prompt, got:\n%s", result)
	}
}

func TestToolBriefingMorning_WithDate(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	result, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{"date":"2026-03-15"}`))
	if err != nil {
		t.Fatalf("toolBriefingMorning with date: %v", err)
	}
	if !strings.Contains(result, "2026-03-15") {
		t.Errorf("result should contain specified date, got:\n%s", result)
	}
}

func TestToolBriefingMorning_InvalidDate(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	_, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{"date":"bad-date"}`))
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
	if !strings.Contains(err.Error(), "invalid date format") {
		t.Errorf("expected 'invalid date format' error, got: %v", err)
	}
}

func TestToolBriefingEvening_WithDate(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	result, err := toolBriefingEvening(ctx, &Config{}, json.RawMessage(`{"date":"2026-03-15"}`))
	if err != nil {
		t.Fatalf("toolBriefingEvening with date: %v", err)
	}
	if !strings.Contains(result, "2026-03-15") {
		t.Errorf("result should contain specified date, got:\n%s", result)
	}
}

func TestToolBriefingMorning_InvalidJSON(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	_, err := toolBriefingMorning(ctx, &Config{}, json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestToolBriefingEvening_InvalidJSON(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	ctx := testBriefingAppCtx(svc)
	_, err := toolBriefingEvening(ctx, &Config{}, json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- Section-level tests ---

func TestScheduleSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:  db.Query,
		Escape: db.Escape,
		// ViewSchedule is nil by default
	})
	sec := svc.ScheduleSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when ViewSchedule is nil")
	}
}

func TestRemindersSection_NoDB(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{Query: db.Query, Escape: db.Escape})
	sec := svc.RemindersSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when dbPath is empty")
	}
}

func TestTasksSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:          db.Query,
		Escape:         db.Escape,
		TasksAvailable: false,
	})
	sec := svc.TasksSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when TasksAvailable is false")
	}
}

func TestHabitsSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:           db.Query,
		Escape:          db.Escape,
		HabitsAvailable: false,
	})
	sec := svc.HabitsSection("2026-02-23", time.Monday)
	if sec != nil {
		t.Error("expected nil when HabitsAvailable is false")
	}
}

func TestGoalsSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:          db.Query,
		Escape:         db.Escape,
		GoalsAvailable: false,
	})
	sec := svc.GoalsSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when GoalsAvailable is false")
	}
}

func TestContactsSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:  db.Query,
		Escape: db.Escape,
		// GetUpcomingEvents is nil by default
	})
	sec := svc.ContactsSection()
	if sec != nil {
		t.Error("expected nil when GetUpcomingEvents is nil")
	}
}

func TestSpendingSection_NilService(t *testing.T) {
	svc := bpkg.New("/tmp/test.db", bpkg.Deps{
		Query:            db.Query,
		Escape:           db.Escape,
		FinanceAvailable: false,
	})
	sec := svc.SpendingSection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when FinanceAvailable is false")
	}
}

func TestDaySummarySection_NoDB(t *testing.T) {
	svc := bpkg.New("", bpkg.Deps{Query: db.Query, Escape: db.Escape})
	sec := svc.DaySummarySection("2026-02-23")
	if sec != nil {
		t.Error("expected nil when dbPath is empty")
	}
}

// --- Data-driven section tests ---

func TestRemindersSection_WithData(t *testing.T) {
	svc, dbPath, cleanup := setupBriefingService(t)
	defer cleanup()

	today := time.Now().UTC().Format("2006-01-02")
	remindAt := today + "T10:30:00Z"
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO reminders (id, message, remind_at, status) VALUES ('r1', 'Buy groceries', '%s', 'pending')`,
		remindAt))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO reminders (id, message, remind_at, status) VALUES ('r2', 'Call dentist', '%s', 'pending')`,
		today+"T14:00:00Z"))

	sec := svc.RemindersSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if sec.Title != "Reminders" {
		t.Errorf("expected title 'Reminders', got %q", sec.Title)
	}
	if len(sec.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(sec.Items))
	}
	if !strings.Contains(sec.Summary, "2 reminders") {
		t.Errorf("expected summary to mention 2 reminders, got %q", sec.Summary)
	}
}

func TestTasksSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so TasksAvailable=true is captured.
	oldTaskMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(&Config{HistoryDB: dbPath})
	defer func() { globalTaskManager = oldTaskMgr }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	today := time.Now().UTC().Format("2006-01-02")
	dueAt := today + "T23:59:59Z"
	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO user_tasks (id, user_id, title, status, priority, due_at, parent_id, tags, created_at, updated_at)
		 VALUES ('t1', 'default', 'Urgent task', 'todo', 1, '%s', '', '[]', '%s', '%s')`,
		dueAt, now, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO user_tasks (id, user_id, title, status, priority, due_at, parent_id, tags, created_at, updated_at)
		 VALUES ('t2', 'default', 'Normal task', 'todo', 3, '%s', '', '[]', '%s', '%s')`,
		dueAt, now, now))

	sec := svc.TasksSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(sec.Items))
	}
	// First task is urgent.
	found := false
	for _, item := range sec.Items {
		if strings.Contains(item, "[URGENT]") && strings.Contains(item, "Urgent task") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected [URGENT] prefix for priority 1 task, items: %v", sec.Items)
	}
}

func TestHabitsSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so HabitsAvailable=true is captured.
	oldHabits := globalHabitsService
	globalHabitsService = newHabitsService(&Config{HistoryDB: dbPath})
	defer func() { globalHabitsService = oldHabits }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	now := time.Now().UTC().Format(time.RFC3339)
	today := time.Now().UTC().Format("2006-01-02")
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO habits (id, name, frequency, target_count, archived_at, created_at, scope)
		 VALUES ('h1', 'Morning Run', 'daily', 1, '', '%s', '')`, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO habits (id, name, frequency, target_count, archived_at, created_at, scope)
		 VALUES ('h2', 'Read', 'daily', 1, '', '%s', '')`, now))
	// Log completion for h1.
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO habit_logs (id, habit_id, logged_at, value)
		 VALUES ('l1', 'h1', '%s', 1.0)`, now))

	sec := svc.HabitsSection(today, time.Now().UTC().Weekday())
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 2 {
		t.Errorf("expected 2 items, got %d: %v", len(sec.Items), sec.Items)
	}
	// h1 should be done, h2 should be todo.
	doneFound := false
	todoFound := false
	for _, item := range sec.Items {
		if strings.Contains(item, "[done]") && strings.Contains(item, "Morning Run") {
			doneFound = true
		}
		if strings.Contains(item, "[todo]") && strings.Contains(item, "Read") {
			todoFound = true
		}
	}
	if !doneFound {
		t.Errorf("expected [done] for Morning Run, items: %v", sec.Items)
	}
	if !todoFound {
		t.Errorf("expected [todo] for Read, items: %v", sec.Items)
	}
	if !strings.Contains(sec.Summary, "1 pending") {
		t.Errorf("summary should contain '1 pending', got %q", sec.Summary)
	}
}

func TestHabitsSection_WeeklyOnNonMonday(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so HabitsAvailable=true is captured.
	oldHabits := globalHabitsService
	globalHabitsService = newHabitsService(&Config{HistoryDB: dbPath})
	defer func() { globalHabitsService = oldHabits }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	now := time.Now().UTC().Format(time.RFC3339)
	today := time.Now().UTC().Format("2006-01-02")
	// Only a weekly habit, no daily.
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO habits (id, name, frequency, target_count, archived_at, created_at, scope)
		 VALUES ('h1', 'Weekly Review', 'weekly', 1, '', '%s', '')`, now))

	// On a non-Monday, weekly habits should be filtered out.
	sec := svc.HabitsSection(today, time.Wednesday)
	if sec != nil {
		// Should be nil because only weekly habits, and it's not Monday.
		t.Errorf("expected nil section on non-Monday for weekly-only habits, got %v", sec.Items)
	}
}

func TestGoalsSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so GoalsAvailable=true is captured.
	oldGoals := globalGoalsService
	globalGoalsService = newGoalsService(&Config{HistoryDB: dbPath})
	defer func() { globalGoalsService = oldGoals }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	now := time.Now().UTC().Format(time.RFC3339)
	today := time.Now().UTC().Format("2006-01-02")
	// Goal with deadline in 3 days.
	deadline := time.Now().UTC().Add(3 * 24 * time.Hour).Format("2006-01-02")
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO goals (id, user_id, title, status, target_date, milestones, review_notes, created_at, updated_at)
		 VALUES ('g1', 'default', 'Ship feature', 'active', '%s', '[]', '[]', '%s', '%s')`,
		deadline, now, now))

	sec := svc.GoalsSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 1 {
		t.Errorf("expected 1 goal item, got %d", len(sec.Items))
	}
	if !strings.Contains(sec.Items[0], "Ship feature") {
		t.Errorf("expected item to contain 'Ship feature', got %q", sec.Items[0])
	}
}

func TestSpendingSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so FinanceAvailable=true is captured.
	oldFinance := globalFinanceService
	globalFinanceService = newFinanceService(&Config{HistoryDB: dbPath})
	defer func() { globalFinanceService = oldFinance }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	today := time.Now().UTC().Format("2006-01-02")
	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO expenses (user_id, amount, currency, category, description, tags, date, created_at)
		 VALUES ('default', 350, 'TWD', 'food', 'lunch', '[]', '%s', '%s')`,
		today, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO expenses (user_id, amount, currency, category, description, tags, date, created_at)
		 VALUES ('default', 200, 'TWD', 'transport', 'taxi', '[]', '%s', '%s')`,
		today, now))

	sec := svc.SpendingSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 2 {
		t.Errorf("expected 2 categories, got %d: %v", len(sec.Items), sec.Items)
	}
	if !strings.Contains(sec.Summary, "550") {
		t.Errorf("expected total 550 in summary, got %q", sec.Summary)
	}
}

func TestTasksCompletedSection_WithData(t *testing.T) {
	dbPath := setupBriefingTestDB(t)

	// Set global BEFORE creating service so TasksAvailable=true is captured.
	oldTaskMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(&Config{HistoryDB: dbPath})
	defer func() { globalTaskManager = oldTaskMgr }()

	svc := newBriefingService(&Config{HistoryDB: dbPath})

	today := time.Now().UTC().Format("2006-01-02")
	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO user_tasks (id, user_id, title, status, priority, due_at, parent_id, tags, created_at, updated_at, completed_at)
		 VALUES ('t1', 'default', 'Done task', 'done', 2, '', '', '[]', '%s', '%s', '%s')`,
		now, now, now))

	sec := svc.TasksCompletedSection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if len(sec.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(sec.Items))
	}
	if sec.Items[0] != "Done task" {
		t.Errorf("expected 'Done task', got %q", sec.Items[0])
	}
}

func TestDaySummarySection_WithData(t *testing.T) {
	svc, dbPath, cleanup := setupBriefingService(t)
	defer cleanup()

	today := time.Now().UTC().Format("2006-01-02")
	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO history (channel, timestamp, message)
		 VALUES ('discord', '%s', 'hello')`, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO history (channel, timestamp, message)
		 VALUES ('discord', '%s', 'world')`, now))
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO history (channel, timestamp, message)
		 VALUES ('line', '%s', 'hi')`, now))

	sec := svc.DaySummarySection(today)
	if sec == nil {
		t.Fatal("expected non-nil section")
	}
	if sec.Title != "Day Summary" {
		t.Errorf("expected title 'Day Summary', got %q", sec.Title)
	}
	if !strings.Contains(sec.Summary, "3 total interactions") {
		t.Errorf("expected 3 total interactions, got %q", sec.Summary)
	}
}

func TestTomorrowPreviewSection_NoData(t *testing.T) {
	svc, _, cleanup := setupBriefingService(t)
	defer cleanup()

	tomorrow := time.Now().UTC().Add(24 * time.Hour)
	sec := svc.TomorrowPreviewSection(tomorrow)
	// No scheduling service and no tasks -> nil.
	if sec != nil {
		t.Errorf("expected nil with no data, got %v", sec)
	}
}

// --- Full integration: morning with data ---

func TestGenerateMorning_WithReminders(t *testing.T) {
	svc, dbPath, cleanup := setupBriefingService(t)
	defer cleanup()

	today := time.Now().UTC().Format("2006-01-02")
	remindAt := today + "T09:00:00Z"
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO reminders (id, message, remind_at, status) VALUES ('r1', 'Team meeting', '%s', 'pending')`,
		remindAt))

	briefing, err := svc.GenerateMorning(time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateMorning: %v", err)
	}

	// Should have at least the reminders section.
	found := false
	for _, sec := range briefing.Sections {
		if sec.Title == "Reminders" {
			found = true
			if len(sec.Items) != 1 {
				t.Errorf("expected 1 reminder, got %d", len(sec.Items))
			}
		}
	}
	if !found {
		t.Error("expected Reminders section in morning briefing")
	}
}

// --- Full integration: evening with data ---

func TestGenerateEvening_WithHistory(t *testing.T) {
	svc, dbPath, cleanup := setupBriefingService(t)
	defer cleanup()

	now := time.Now().UTC().Format(time.RFC3339)
	briefingExecSQL(t, dbPath, fmt.Sprintf(
		`INSERT INTO history (channel, timestamp, message) VALUES ('slack', '%s', 'test')`, now))

	briefing, err := svc.GenerateEvening(time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateEvening: %v", err)
	}

	found := false
	for _, sec := range briefing.Sections {
		if sec.Title == "Day Summary" {
			found = true
			if len(sec.Items) != 1 {
				t.Errorf("expected 1 channel in summary, got %d", len(sec.Items))
			}
		}
	}
	if !found {
		t.Error("expected Day Summary section in evening briefing")
	}
}

// --- Briefing serialization ---

func TestBriefingJSON(t *testing.T) {
	briefing := &bpkg.Briefing{
		Type:     "morning",
		Date:     "2026-02-23",
		Greeting: "Hello",
		Sections: []bpkg.BriefingSection{
			{Title: "Test", Icon: "star", Items: []string{"item1"}, Summary: "1 item"},
		},
		Quote:       "A quote",
		GeneratedAt: "2026-02-23T08:00:00Z",
	}

	data, err := json.Marshal(briefing)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded bpkg.Briefing
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "morning" {
		t.Errorf("expected type morning, got %s", decoded.Type)
	}
	if len(decoded.Sections) != 1 {
		t.Errorf("expected 1 section, got %d", len(decoded.Sections))
	}
	if decoded.Sections[0].Items[0] != "item1" {
		t.Errorf("expected item1, got %s", decoded.Sections[0].Items[0])
	}
}

// --- from capture_test.go ---

func TestClassifyCapture(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"spent $20 on lunch", "expense"},
		{"paid 500元 for groceries", "expense"},
		{"bought new shoes cost $80", "expense"},
		{"300円のコーヒー", "expense"},
		{"remind me to call doctor", "reminder"},
		{"deadline for project is friday", "reminder"},
		{"don't forget to buy milk", "reminder"},
		{"phone number is 555-1234", "contact"},
		{"email john@example.com", "contact"},
		{"birthday party for alice", "contact"},
		{"todo: review PRs", "task"},
		{"need to fix the login bug", "task"},
		{"should update the docs", "task"},
		{"idea: build a CLI tool", "idea"},
		{"what if we use websockets", "idea"},
		{"the sky is blue today", "note"},
		{"random thought about architecture", "note"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := classifyCapture(tt.input)
			if got != tt.expected {
				t.Errorf("classifyCapture(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestClassifyCapture_DefaultNote(t *testing.T) {
	inputs := []string{
		"hello world",
		"meeting notes from today",
		"考えたこと",
	}
	for _, input := range inputs {
		got := classifyCapture(input)
		if got != "note" {
			t.Errorf("classifyCapture(%q) = %q, want 'note'", input, got)
		}
	}
}

// --- from contacts_test.go ---

// newTestContactsService creates a ContactsService with a temp DB for testing.
func newTestContactsService(t *testing.T) *ContactsService {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "contacts_test.db")
	if err := initContactsDB(dbPath); err != nil {
		t.Fatalf("initContactsDB: %v", err)
	}
	return contacts.New(dbPath, makeLifeDB(), nil, nil)
}

// testAddContact is a test helper that adapts the old map-based API to the new struct API.
func testAddContact(t *testing.T, cs *ContactsService, name string, fields map[string]any) (*Contact, error) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	c := &Contact{ID: newUUID(), Name: name, CreatedAt: now, UpdatedAt: now}
	if fields != nil {
		if v, ok := fields["email"].(string); ok {
			c.Email = v
		}
		if v, ok := fields["phone"].(string); ok {
			c.Phone = v
		}
		if v, ok := fields["birthday"].(string); ok {
			c.Birthday = v
		}
		if v, ok := fields["anniversary"].(string); ok {
			c.Anniversary = v
		}
		if v, ok := fields["notes"].(string); ok {
			c.Notes = v
		}
		if v, ok := fields["nickname"].(string); ok {
			c.Nickname = v
		}
		if v, ok := fields["relationship"].(string); ok {
			c.Relationship = v
		}
		if v, ok := fields["tags"]; ok {
			switch tv := v.(type) {
			case []string:
				c.Tags = tv
			case []any:
				for _, s := range tv {
					if str, ok := s.(string); ok {
						c.Tags = append(c.Tags, str)
					}
				}
			}
		}
		if v, ok := fields["channel_ids"]; ok {
			switch cv := v.(type) {
			case map[string]string:
				c.ChannelIDs = cv
			case map[string]any:
				c.ChannelIDs = make(map[string]string)
				for k, val := range cv {
					if str, ok := val.(string); ok {
						c.ChannelIDs[k] = str
					}
				}
			}
		}
	}
	err := cs.AddContact(c)
	return c, err
}

func TestInitContactsDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "contacts_init.db")
	if err := initContactsDB(dbPath); err != nil {
		t.Fatalf("initContactsDB failed: %v", err)
	}
	// Calling again should be idempotent.
	if err := initContactsDB(dbPath); err != nil {
		t.Fatalf("initContactsDB idempotent failed: %v", err)
	}

	// Verify tables exist.
	rows, err := db.Query(dbPath, `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("db.Query: %v", err)
	}
	tableNames := make(map[string]bool)
	for _, row := range rows {
		tableNames[jsonStr(row["name"])] = true
	}
	for _, expected := range []string{"contacts", "contact_interactions"} {
		if !tableNames[expected] {
			t.Errorf("expected table %s to exist", expected)
		}
	}
}

func TestAddContact(t *testing.T) {
	cs := newTestContactsService(t)

	now := time.Now().UTC().Format(time.RFC3339)
	c := &Contact{
		ID:           newUUID(),
		Name:         "Alice Smith",
		Email:        "alice@example.com",
		Phone:        "+1-555-0100",
		Birthday:     "1990-03-15",
		Relationship: "friend",
		Tags:         []string{"work", "tennis"},
		ChannelIDs:   map[string]string{"discord": "12345"},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := cs.AddContact(c); err != nil {
		t.Fatalf("AddContact: %v", err)
	}
	if c.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if c.Name != "Alice Smith" {
		t.Errorf("name = %q, want %q", c.Name, "Alice Smith")
	}
	if c.Email != "alice@example.com" {
		t.Errorf("email = %q, want %q", c.Email, "alice@example.com")
	}
	if c.Birthday != "1990-03-15" {
		t.Errorf("birthday = %q, want %q", c.Birthday, "1990-03-15")
	}
	if c.Relationship != "friend" {
		t.Errorf("relationship = %q, want %q", c.Relationship, "friend")
	}
	if len(c.Tags) != 2 || c.Tags[0] != "work" || c.Tags[1] != "tennis" {
		t.Errorf("tags = %v, want [work tennis]", c.Tags)
	}
	if c.ChannelIDs["discord"] != "12345" {
		t.Errorf("channel_ids = %v, want discord=12345", c.ChannelIDs)
	}

	// Verify it can be retrieved.
	fetched, err := cs.GetContact(c.ID)
	if err != nil {
		t.Fatalf("GetContact: %v", err)
	}
	if fetched.Name != "Alice Smith" {
		t.Errorf("fetched name = %q, want %q", fetched.Name, "Alice Smith")
	}
	if fetched.Email != "alice@example.com" {
		t.Errorf("fetched email = %q, want %q", fetched.Email, "alice@example.com")
	}
	if len(fetched.Tags) != 2 {
		t.Errorf("fetched tags = %v, want 2 items", fetched.Tags)
	}
}

func TestAddContact_EmptyName(t *testing.T) {
	cs := newTestContactsService(t)

	err := cs.AddContact(&Contact{ID: newUUID(), Name: ""})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("unexpected error: %v", err)
	}

	// Whitespace-only name should also fail.
	err = cs.AddContact(&Contact{ID: newUUID(), Name: "   "})
	if err == nil {
		t.Fatal("expected error for whitespace-only name")
	}
}

func TestAddContact_AnyTags(t *testing.T) {
	cs := newTestContactsService(t)

	// Test with []any tags (as would come from JSON unmarshaling).
	c, err := testAddContact(t, cs, "Bob", map[string]any{
		"tags": []any{"a", "b"},
	})
	if err != nil {
		t.Fatalf("AddContact: %v", err)
	}
	if len(c.Tags) != 2 {
		t.Errorf("tags = %v, want 2 items", c.Tags)
	}
}

func TestAddContact_AnyChannelIDs(t *testing.T) {
	cs := newTestContactsService(t)

	// Test with map[string]any channel_ids.
	c, err := testAddContact(t, cs, "Charlie", map[string]any{
		"channel_ids": map[string]any{"telegram": "99999"},
	})
	if err != nil {
		t.Fatalf("AddContact: %v", err)
	}
	if c.ChannelIDs["telegram"] != "99999" {
		t.Errorf("channel_ids = %v", c.ChannelIDs)
	}
}

func TestUpdateContact(t *testing.T) {
	cs := newTestContactsService(t)

	c, err := testAddContact(t, cs, "Alice", map[string]any{
		"email": "alice@old.com",
	})
	if err != nil {
		t.Fatalf("AddContact: %v", err)
	}

	// Update email and add nickname.
	updated, err := cs.UpdateContact(c.ID, map[string]any{
		"email":    "alice@new.com",
		"nickname": "Ali",
	})
	if err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}
	if updated.Email != "alice@new.com" {
		t.Errorf("email = %q, want %q", updated.Email, "alice@new.com")
	}
	if updated.Nickname != "Ali" {
		t.Errorf("nickname = %q, want %q", updated.Nickname, "Ali")
	}

	// Update with empty fields returns contact as-is.
	same, err := cs.UpdateContact(c.ID, map[string]any{})
	if err != nil {
		t.Fatalf("UpdateContact empty: %v", err)
	}
	if same.Email != "alice@new.com" {
		t.Errorf("email = %q, want %q", same.Email, "alice@new.com")
	}

	// Update name to empty should fail.
	_, err = cs.UpdateContact(c.ID, map[string]any{"name": ""})
	if err == nil {
		t.Fatal("expected error for empty name update")
	}

	// Unknown field.
	_, err = cs.UpdateContact(c.ID, map[string]any{"unknown_field": "val"})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestUpdateContact_Tags(t *testing.T) {
	cs := newTestContactsService(t)

	c, err := testAddContact(t, cs, "Dora", map[string]any{
		"tags": []string{"old"},
	})
	if err != nil {
		t.Fatalf("AddContact: %v", err)
	}

	updated, err := cs.UpdateContact(c.ID, map[string]any{
		"tags": []string{"new", "updated"},
	})
	if err != nil {
		t.Fatalf("UpdateContact tags: %v", err)
	}
	if len(updated.Tags) != 2 || updated.Tags[0] != "new" {
		t.Errorf("tags = %v, want [new updated]", updated.Tags)
	}
}

func TestSearchContacts(t *testing.T) {
	cs := newTestContactsService(t)

	testAddContact(t, cs, "Alice Smith", map[string]any{"email": "alice@example.com", "notes": "tennis player"})
	testAddContact(t, cs, "Bob Jones", map[string]any{"email": "bob@example.com"})
	testAddContact(t, cs, "Charlie Smith", map[string]any{"nickname": "Chuck"})

	// Search by name.
	results, err := cs.SearchContacts("Smith", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}

	// Search by email.
	results, err = cs.SearchContacts("bob@", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}

	// Search by nickname.
	results, err = cs.SearchContacts("Chuck", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}

	// Search by notes.
	results, err = cs.SearchContacts("tennis", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results for 'tennis', want 1", len(results))
	}

	// No match.
	results, err = cs.SearchContacts("zzzzz", 10)
	if err != nil {
		t.Fatalf("SearchContacts: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestGetContact(t *testing.T) {
	cs := newTestContactsService(t)

	c, _ := testAddContact(t, cs, "Eve", nil)
	fetched, err := cs.GetContact(c.ID)
	if err != nil {
		t.Fatalf("GetContact: %v", err)
	}
	if fetched.Name != "Eve" {
		t.Errorf("name = %q, want %q", fetched.Name, "Eve")
	}
}

func TestGetContact_NotFound(t *testing.T) {
	cs := newTestContactsService(t)

	_, err := cs.GetContact("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}

	// Empty ID.
	_, err = cs.GetContact("")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestListContacts(t *testing.T) {
	cs := newTestContactsService(t)

	testAddContact(t, cs, "Alice", map[string]any{"relationship": "friend"})
	testAddContact(t, cs, "Bob", map[string]any{"relationship": "colleague"})
	testAddContact(t, cs, "Charlie", map[string]any{"relationship": "friend"})

	// List all.
	all, err := cs.ListContacts("", 50)
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("got %d contacts, want 3", len(all))
	}
}

func TestListContacts_FilterRelationship(t *testing.T) {
	cs := newTestContactsService(t)

	testAddContact(t, cs, "Alice", map[string]any{"relationship": "friend"})
	testAddContact(t, cs, "Bob", map[string]any{"relationship": "colleague"})
	testAddContact(t, cs, "Charlie", map[string]any{"relationship": "friend"})

	// Filter by relationship.
	friends, err := cs.ListContacts("friend", 50)
	if err != nil {
		t.Fatalf("ListContacts friends: %v", err)
	}
	if len(friends) != 2 {
		t.Errorf("got %d friends, want 2", len(friends))
	}

	colleagues, err := cs.ListContacts("colleague", 50)
	if err != nil {
		t.Fatalf("ListContacts colleagues: %v", err)
	}
	if len(colleagues) != 1 {
		t.Errorf("got %d colleagues, want 1", len(colleagues))
	}

	// Empty filter.
	none, err := cs.ListContacts("acquaintance", 50)
	if err != nil {
		t.Fatalf("ListContacts acquaintance: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("got %d acquaintances, want 0", len(none))
	}
}

func TestLogInteraction(t *testing.T) {
	cs := newTestContactsService(t)

	c, _ := testAddContact(t, cs, "Frank", nil)

	err := cs.LogInteraction(newUUID(), c.ID, "discord", "message", "Chatted about project", "positive")
	if err != nil {
		t.Fatalf("LogInteraction: %v", err)
	}

	err = cs.LogInteraction(newUUID(), c.ID, "email", "email", "Sent proposal", "neutral")
	if err != nil {
		t.Fatalf("LogInteraction 2: %v", err)
	}

	// Logging to nonexistent contact should fail.
	err = cs.LogInteraction(newUUID(), "nonexistent", "discord", "message", "test", "")
	if err == nil {
		t.Fatal("expected error for nonexistent contact")
	}

	// Missing contact ID.
	err = cs.LogInteraction(newUUID(), "", "discord", "message", "test", "")
	if err == nil {
		t.Fatal("expected error for empty contact ID")
	}

	// Missing interaction type.
	err = cs.LogInteraction(newUUID(), c.ID, "discord", "", "test", "")
	if err == nil {
		t.Fatal("expected error for empty interaction type")
	}
}

func TestGetContactInteractions(t *testing.T) {
	cs := newTestContactsService(t)

	c, _ := testAddContact(t, cs, "Grace", nil)
	cs.LogInteraction(newUUID(), c.ID, "discord", "message", "hello", "positive")
	cs.LogInteraction(newUUID(), c.ID, "telegram", "call", "video call", "neutral")

	interactions, err := cs.GetContactInteractions(c.ID, 10)
	if err != nil {
		t.Fatalf("GetContactInteractions: %v", err)
	}
	if len(interactions) != 2 {
		t.Errorf("got %d interactions, want 2", len(interactions))
	}
	// Verify both interaction types are present.
	types := make(map[string]bool)
	for _, i := range interactions {
		types[i.InteractionType] = true
	}
	if !types["message"] || !types["call"] {
		t.Errorf("expected message and call types, got %v", types)
	}

	// Empty contact ID.
	_, err = cs.GetContactInteractions("", 10)
	if err == nil {
		t.Fatal("expected error for empty contact ID")
	}
}

func TestGetUpcomingEvents_Birthday(t *testing.T) {
	cs := newTestContactsService(t)

	// Calculate a birthday that is 5 days from now (current year).
	upcoming := time.Now().UTC().Add(5 * 24 * time.Hour).Format("2006-01-02")
	// Use a fixed year (past) so it tests the "this year's occurrence" logic.
	upcomingPastYear := "1990" + upcoming[4:]

	testAddContact(t, cs, "Hank", map[string]any{"birthday": upcomingPastYear})

	// Also add someone whose birthday was yesterday (should not show for 7-day window,
	// unless we are within 365 days which it always is).
	yesterday := time.Now().UTC().Add(-1 * 24 * time.Hour).Format("2006-01-02")
	yesterdayPast := "1985" + yesterday[4:]
	testAddContact(t, cs, "Ivy", map[string]any{"birthday": yesterdayPast})

	events, err := cs.GetUpcomingEvents(7)
	if err != nil {
		t.Fatalf("GetUpcomingEvents: %v", err)
	}

	// Should find Hank's birthday (5 days away), not Ivy's (yesterday wraps to next year).
	found := false
	for _, ev := range events {
		if ev["contact_name"] == "Hank" && ev["event_type"] == "birthday" {
			found = true
			daysUntil, ok := ev["days_until"].(int)
			if !ok {
				t.Errorf("days_until not int: %v", ev["days_until"])
			}
			if daysUntil < 4 || daysUntil > 6 {
				t.Errorf("days_until = %d, expected ~5", daysUntil)
			}
		}
	}
	if !found {
		t.Errorf("Hank's birthday not found in events: %v", events)
	}
}

func TestGetUpcomingEvents_NoBirthdays(t *testing.T) {
	cs := newTestContactsService(t)

	// Contact with no birthday/anniversary.
	testAddContact(t, cs, "Jake", nil)

	events, err := cs.GetUpcomingEvents(30)
	if err != nil {
		t.Fatalf("GetUpcomingEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

func TestGetUpcomingEvents_Anniversary(t *testing.T) {
	cs := newTestContactsService(t)

	// Anniversary 10 days from now.
	upcoming := time.Now().UTC().Add(10 * 24 * time.Hour).Format("2006-01-02")
	annivPast := "2015" + upcoming[4:]

	testAddContact(t, cs, "Kate", map[string]any{"anniversary": annivPast})

	events, err := cs.GetUpcomingEvents(15)
	if err != nil {
		t.Fatalf("GetUpcomingEvents: %v", err)
	}

	found := false
	for _, ev := range events {
		if ev["contact_name"] == "Kate" && ev["event_type"] == "anniversary" {
			found = true
		}
	}
	if !found {
		t.Errorf("Kate's anniversary not found in events: %v", events)
	}
}

func TestGetInactiveContacts(t *testing.T) {
	cs := newTestContactsService(t)

	c1, _ := testAddContact(t, cs, "Larry", nil)
	_, _ = testAddContact(t, cs, "Mona", nil)

	// Log a recent interaction for Larry.
	cs.LogInteraction(newUUID(), c1.ID, "discord", "message", "recent chat", "positive")

	// Mona has no interaction, so she should be inactive.
	inactive, err := cs.GetInactiveContacts(7)
	if err != nil {
		t.Fatalf("GetInactiveContacts: %v", err)
	}

	found := false
	for _, c := range inactive {
		if c.Name == "Mona" {
			found = true
		}
		if c.Name == "Larry" {
			t.Error("Larry should NOT be inactive (has recent interaction)")
		}
	}
	if !found {
		t.Error("Mona should be in inactive list")
	}
}

func TestGetInactiveContacts_AllActive(t *testing.T) {
	cs := newTestContactsService(t)

	c1, _ := testAddContact(t, cs, "Ned", nil)
	cs.LogInteraction(newUUID(), c1.ID, "discord", "message", "chat", "positive")

	inactive, err := cs.GetInactiveContacts(7)
	if err != nil {
		t.Fatalf("GetInactiveContacts: %v", err)
	}
	if len(inactive) != 0 {
		t.Errorf("got %d inactive, want 0", len(inactive))
	}
}

// --- Tool Handler Tests ---

func TestToolContactAdd(t *testing.T) {
	cs := newTestContactsService(t)
	ctx := withApp(context.Background(), &App{Contacts: cs})

	input := `{"name":"Oscar","email":"oscar@test.com","relationship":"colleague","tags":["dev"]}`
	result, err := toolContactAdd(ctx, &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactAdd: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "added" {
		t.Errorf("status = %v, want added", resp["status"])
	}

	// Verify the contact was saved.
	contacts, _ := cs.ListContacts("", 10)
	if len(contacts) != 1 {
		t.Fatalf("got %d contacts, want 1", len(contacts))
	}
	if contacts[0].Name != "Oscar" {
		t.Errorf("name = %q, want Oscar", contacts[0].Name)
	}
}

func TestToolContactAdd_EmptyName(t *testing.T) {
	cs := newTestContactsService(t)
	ctx := withApp(context.Background(), &App{Contacts: cs})

	input := `{"name":""}`
	_, err := toolContactAdd(ctx, &Config{}, json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestToolContactSearch(t *testing.T) {
	cs := newTestContactsService(t)
	ctx := withApp(context.Background(), &App{Contacts: cs})

	testAddContact(t, cs, "Patricia", map[string]any{"email": "pat@test.com"})
	testAddContact(t, cs, "Paul", map[string]any{"email": "paul@test.com"})

	input := `{"query":"Pa","limit":10}`
	result, err := toolContactSearch(ctx, &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactSearch: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	count := int(resp["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestToolContactSearch_NoQuery(t *testing.T) {
	cs := newTestContactsService(t)
	ctx := withApp(context.Background(), &App{Contacts: cs})

	input := `{"query":""}`
	_, err := toolContactSearch(ctx, &Config{}, json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestToolContactLog(t *testing.T) {
	cs := newTestContactsService(t)
	ctx := withApp(context.Background(), &App{Contacts: cs})

	c, _ := testAddContact(t, cs, "Quinn", nil)

	input := `{"contact_id":"` + c.ID + `","type":"message","summary":"had lunch","sentiment":"positive"}`
	result, err := toolContactLog(ctx, &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactLog: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "logged" {
		t.Errorf("status = %v, want logged", resp["status"])
	}

	// Verify interaction was saved.
	interactions, _ := cs.GetContactInteractions(c.ID, 10)
	if len(interactions) != 1 {
		t.Fatalf("got %d interactions, want 1", len(interactions))
	}
	if interactions[0].Summary != "had lunch" {
		t.Errorf("summary = %q, want 'had lunch'", interactions[0].Summary)
	}
}

func TestToolContactLog_NoContactID(t *testing.T) {
	cs := newTestContactsService(t)
	ctx := withApp(context.Background(), &App{Contacts: cs})

	input := `{"contact_id":"","type":"message"}`
	_, err := toolContactLog(ctx, &Config{}, json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error for empty contact_id")
	}
}

func TestToolContactList(t *testing.T) {
	cs := newTestContactsService(t)
	ctx := withApp(context.Background(), &App{Contacts: cs})

	testAddContact(t, cs, "Ruth", map[string]any{"relationship": "family"})
	testAddContact(t, cs, "Sam", map[string]any{"relationship": "friend"})

	input := `{"relationship":"family","limit":10}`
	result, err := toolContactList(ctx, &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactList: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	count := int(resp["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestToolContactUpcoming(t *testing.T) {
	cs := newTestContactsService(t)
	ctx := withApp(context.Background(), &App{Contacts: cs})

	upcoming := time.Now().UTC().Add(3 * 24 * time.Hour).Format("2006-01-02")
	bdayPast := "1992" + upcoming[4:]
	testAddContact(t, cs, "Tina", map[string]any{"birthday": bdayPast})

	input := `{"days":7}`
	result, err := toolContactUpcoming(ctx, &Config{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("toolContactUpcoming: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	count := int(resp["count"].(float64))
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestToolContactServiceNil(t *testing.T) {
	_, err := toolContactAdd(context.Background(), &Config{}, json.RawMessage(`{"name":"test"}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	_, err = toolContactSearch(context.Background(), &Config{}, json.RawMessage(`{"query":"test"}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	_, err = toolContactList(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	_, err = toolContactUpcoming(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
	_, err = toolContactLog(context.Background(), &Config{}, json.RawMessage(`{"contact_id":"x","type":"message"}`))
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
}

func TestDaysUntilEvent(t *testing.T) {
	today := time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC)
	endDate := today.Add(30 * 24 * time.Hour) // 30 days out

	// Event in 5 days (Feb 28).
	d, ok := contacts.DaysUntilEvent("1990-02-28", today, endDate)
	if !ok {
		t.Fatal("expected to find event")
	}
	if d != 5 {
		t.Errorf("days_until = %d, want 5", d)
	}

	// Event today (Feb 23).
	d, ok = contacts.DaysUntilEvent("1990-02-23", today, endDate)
	if !ok {
		t.Fatal("expected to find event for today")
	}
	if d != 0 {
		t.Errorf("days_until = %d, want 0", d)
	}

	// Event far in future (next January) — outside 30 day window.
	_, ok = contacts.DaysUntilEvent("1990-01-01", today, endDate)
	// Jan 1 is ~312 days away from Feb 23 (next year), so outside 30-day window.
	if ok {
		t.Error("expected NOT to find event outside window")
	}

	// Invalid date.
	_, ok = contacts.DaysUntilEvent("bad", today, endDate)
	if ok {
		t.Error("expected false for invalid date")
	}
}

// --- from family_test.go ---

// newTestFamilyService creates a FamilyService with a temp DB for testing.
func newTestFamilyService(t *testing.T) *FamilyService {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "family_test.db")
	if err := initFamilyDB(dbPath); err != nil {
		t.Fatalf("initFamilyDB: %v", err)
	}
	svc, err := newFamilyService(&Config{HistoryDB: dbPath}, FamilyConfig{
		Enabled:          true,
		MaxUsers:         10,
		DefaultBudget:    0,
		DefaultRateLimit: 100,
	})
	if err != nil {
		t.Fatalf("newFamilyService: %v", err)
	}
	return svc
}

func TestInitFamilyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "family_init.db")
	if err := initFamilyDB(dbPath); err != nil {
		t.Fatalf("initFamilyDB failed: %v", err)
	}
	// Calling again should be idempotent.
	if err := initFamilyDB(dbPath); err != nil {
		t.Fatalf("initFamilyDB idempotent failed: %v", err)
	}

	// Verify tables exist.
	rows, err := db.Query(dbPath, `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("db.Query: %v", err)
	}
	tableNames := make(map[string]bool)
	for _, row := range rows {
		tableNames[jsonStr(row["name"])] = true
	}
	for _, expected := range []string{"family_users", "user_permissions", "shared_lists", "shared_list_items"} {
		if !tableNames[expected] {
			t.Errorf("expected table %s to exist", expected)
		}
	}
}

func TestAddUser(t *testing.T) {
	fs := newTestFamilyService(t)

	// Add a member.
	if err := fs.AddUser("user1", "Alice", "member"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	// Add an admin.
	if err := fs.AddUser("admin1", "Bob", "admin"); err != nil {
		t.Fatalf("AddUser admin: %v", err)
	}

	// Duplicate should fail.
	err := fs.AddUser("user1", "Alice2", "member")
	if err == nil {
		t.Fatal("expected error for duplicate user")
	}

	// Invalid role.
	err = fs.AddUser("user2", "Charlie", "superuser")
	if err == nil {
		t.Fatal("expected error for invalid role")
	}

	// Empty user ID.
	err = fs.AddUser("", "Nobody", "member")
	if err == nil {
		t.Fatal("expected error for empty user ID")
	}
}

func TestAddUserMaxLimit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "family_limit.db")
	if err := initFamilyDB(dbPath); err != nil {
		t.Fatalf("initFamilyDB: %v", err)
	}
	fs, err := newFamilyService(&Config{HistoryDB: dbPath}, FamilyConfig{MaxUsers: 3})
	if err != nil {
		t.Fatalf("newFamilyService: %v", err)
	}
	_ = fs

	for i := 0; i < 3; i++ {
		if err := fs.AddUser(jsonStr(i), "User", "member"); err != nil {
			t.Fatalf("AddUser %d: %v", i, err)
		}
	}
	// 4th should fail.
	err = fs.AddUser("extra", "Extra", "member")
	if err == nil {
		t.Fatal("expected max users error")
	}
	if !strings.Contains(err.Error(), "max users") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRemoveUser(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	if err := fs.RemoveUser("user1"); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}

	// User should no longer appear in active list.
	users, _ := fs.ListUsers()
	for _, u := range users {
		if u.UserID == "user1" {
			t.Fatal("removed user should not appear in active list")
		}
	}

	// GetUser should fail for active-only.
	_, err := fs.GetUser("user1")
	if err == nil {
		t.Fatal("expected error for removed user")
	}

	// Remove empty user ID.
	if err := fs.RemoveUser(""); err == nil {
		t.Fatal("expected error for empty user ID")
	}
}

func TestRemoveAndReaddUser(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")
	fs.RemoveUser("user1")

	// Re-adding should reactivate.
	if err := fs.AddUser("user1", "Alice Reactivated", "admin"); err != nil {
		t.Fatalf("re-add user: %v", err)
	}

	user, err := fs.GetUser("user1")
	if err != nil {
		t.Fatalf("GetUser after reactivation: %v", err)
	}
	if user.Role != "admin" {
		t.Errorf("expected role admin, got %s", user.Role)
	}
	if !user.Active {
		t.Error("expected user to be active")
	}
}

func TestGetUser(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	user, err := fs.GetUser("user1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.UserID != "user1" {
		t.Errorf("expected user1, got %s", user.UserID)
	}
	if user.DisplayName != "Alice" {
		t.Errorf("expected Alice, got %s", user.DisplayName)
	}
	if user.Role != "member" {
		t.Errorf("expected member, got %s", user.Role)
	}
	if !user.Active {
		t.Error("expected active user")
	}

	// Non-existent user.
	_, err = fs.GetUser("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestListUsers(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")
	fs.AddUser("user2", "Bob", "admin")
	fs.AddUser("user3", "Charlie", "guest")

	users, err := fs.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(users))
	}

	// Remove one and verify count.
	fs.RemoveUser("user2")
	users, _ = fs.ListUsers()
	if len(users) != 2 {
		t.Errorf("expected 2 users after removal, got %d", len(users))
	}
}

func TestUpdateUser(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	// Update display name.
	if err := fs.UpdateUser("user1", map[string]any{"displayName": "Alice Updated"}); err != nil {
		t.Fatalf("UpdateUser displayName: %v", err)
	}
	user, _ := fs.GetUser("user1")
	if user.DisplayName != "Alice Updated" {
		t.Errorf("expected 'Alice Updated', got %s", user.DisplayName)
	}

	// Update role.
	if err := fs.UpdateUser("user1", map[string]any{"role": "admin"}); err != nil {
		t.Fatalf("UpdateUser role: %v", err)
	}
	user, _ = fs.GetUser("user1")
	if user.Role != "admin" {
		t.Errorf("expected admin, got %s", user.Role)
	}

	// Invalid role.
	if err := fs.UpdateUser("user1", map[string]any{"role": "superuser"}); err == nil {
		t.Fatal("expected error for invalid role")
	}

	// Unknown field.
	if err := fs.UpdateUser("user1", map[string]any{"unknown": "value"}); err == nil {
		t.Fatal("expected error for unknown field")
	}

	// Update rate limit and budget.
	if err := fs.UpdateUser("user1", map[string]any{
		"rateLimitDaily": float64(200),
		"budgetMonthly":  50.0,
	}); err != nil {
		t.Fatalf("UpdateUser rateLimit/budget: %v", err)
	}
	user, _ = fs.GetUser("user1")
	if user.RateLimitDaily != 200 {
		t.Errorf("expected rate limit 200, got %d", user.RateLimitDaily)
	}

	// Empty updates should be fine.
	if err := fs.UpdateUser("user1", map[string]any{}); err != nil {
		t.Fatalf("UpdateUser empty: %v", err)
	}
}

func TestGrantPermission(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	if err := fs.GrantPermission("user1", "task.write"); err != nil {
		t.Fatalf("GrantPermission: %v", err)
	}

	// Grant again (idempotent).
	if err := fs.GrantPermission("user1", "task.write"); err != nil {
		t.Fatalf("GrantPermission idempotent: %v", err)
	}

	// Empty args.
	if err := fs.GrantPermission("", "task.write"); err == nil {
		t.Fatal("expected error for empty user ID")
	}
	if err := fs.GrantPermission("user1", ""); err == nil {
		t.Fatal("expected error for empty permission")
	}
}

func TestRevokePermission(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")
	fs.GrantPermission("user1", "task.write")

	if err := fs.RevokePermission("user1", "task.write"); err != nil {
		t.Fatalf("RevokePermission: %v", err)
	}

	has, err := fs.HasPermission("user1", "task.write")
	if err != nil {
		t.Fatalf("HasPermission: %v", err)
	}
	if has {
		t.Error("expected permission to be revoked")
	}
}

func TestHasPermission(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")
	fs.AddUser("admin1", "Bob", "admin")

	// Member without permission.
	has, err := fs.HasPermission("user1", "task.write")
	if err != nil {
		t.Fatalf("HasPermission: %v", err)
	}
	if has {
		t.Error("expected no permission for member without grant")
	}

	// Grant and check.
	fs.GrantPermission("user1", "task.write")
	has, _ = fs.HasPermission("user1", "task.write")
	if !has {
		t.Error("expected permission after grant")
	}

	// Admin has all permissions.
	has, _ = fs.HasPermission("admin1", "anything.at.all")
	if !has {
		t.Error("expected admin to have all permissions")
	}

	// Non-existent user.
	_, err = fs.HasPermission("nonexistent", "task.write")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestGetPermissions(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	// No permissions initially.
	perms, err := fs.GetPermissions("user1")
	if err != nil {
		t.Fatalf("GetPermissions: %v", err)
	}
	if len(perms) != 0 {
		t.Errorf("expected 0 permissions, got %d", len(perms))
	}

	// Grant some.
	fs.GrantPermission("user1", "task.write")
	fs.GrantPermission("user1", "expense.read")
	perms, _ = fs.GetPermissions("user1")
	if len(perms) != 2 {
		t.Errorf("expected 2 permissions, got %d", len(perms))
	}

	// Revoke one.
	fs.RevokePermission("user1", "task.write")
	perms, _ = fs.GetPermissions("user1")
	if len(perms) != 1 {
		t.Errorf("expected 1 permission, got %d", len(perms))
	}
}

func TestCheckRateLimit(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	// No history DB configured — should allow.
	allowed, remaining, err := fs.CheckRateLimit("user1")
	if err != nil {
		t.Fatalf("CheckRateLimit: %v", err)
	}
	if !allowed {
		t.Error("expected allowed with no history")
	}
	if remaining != 100 {
		t.Errorf("expected 100 remaining, got %d", remaining)
	}

	// Non-existent user.
	_, _, err = fs.CheckRateLimit("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}

	// User with 0 rate limit (unlimited).
	fs.UpdateUser("user1", map[string]any{"rateLimitDaily": float64(0)})
	allowed, remaining, _ = fs.CheckRateLimit("user1")
	if !allowed {
		t.Error("expected unlimited user to be allowed")
	}
	if remaining != -1 {
		t.Errorf("expected -1 for unlimited, got %d", remaining)
	}
}

func TestCreateList(t *testing.T) {
	fs := newTestFamilyService(t)

	list, err := fs.CreateList("Groceries", "shopping", "user1", newUUID)
	if err != nil {
		t.Fatalf("CreateList: %v", err)
	}
	if list.Name != "Groceries" {
		t.Errorf("expected Groceries, got %s", list.Name)
	}
	if list.ListType != "shopping" {
		t.Errorf("expected shopping, got %s", list.ListType)
	}
	if list.ID == "" {
		t.Error("expected non-empty list ID")
	}

	// Empty name.
	_, err = fs.CreateList("", "shopping", "user1", newUUID)
	if err == nil {
		t.Fatal("expected error for empty name")
	}

	// Default type.
	list2, err := fs.CreateList("Tasks", "", "user1", newUUID)
	if err != nil {
		t.Fatalf("CreateList default type: %v", err)
	}
	if list2.ListType != "shopping" {
		t.Errorf("expected default type shopping, got %s", list2.ListType)
	}
}

func TestGetList(t *testing.T) {
	fs := newTestFamilyService(t)
	created, _ := fs.CreateList("Groceries", "shopping", "user1", newUUID)

	got, err := fs.GetList(created.ID)
	if err != nil {
		t.Fatalf("GetList: %v", err)
	}
	if got.Name != "Groceries" {
		t.Errorf("expected Groceries, got %s", got.Name)
	}

	// Non-existent.
	_, err = fs.GetList("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent list")
	}
}

func TestListLists(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.CreateList("List1", "shopping", "user1", newUUID)
	fs.CreateList("List2", "todo", "user1", newUUID)

	lists, err := fs.ListLists()
	if err != nil {
		t.Fatalf("ListLists: %v", err)
	}
	if len(lists) != 2 {
		t.Errorf("expected 2 lists, got %d", len(lists))
	}
}

func TestDeleteList(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("ToDelete", "shopping", "user1", newUUID)
	fs.AddListItem(list.ID, "Milk", "1L", "user1")

	if err := fs.DeleteList(list.ID); err != nil {
		t.Fatalf("DeleteList: %v", err)
	}

	// Should be gone.
	_, err := fs.GetList(list.ID)
	if err == nil {
		t.Fatal("expected error for deleted list")
	}

	// Items should also be gone.
	items, _ := fs.GetListItems(list.ID)
	if len(items) != 0 {
		t.Errorf("expected 0 items after delete, got %d", len(items))
	}
}

func TestAddListItem(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("Groceries", "shopping", "user1", newUUID)

	item, err := fs.AddListItem(list.ID, "Milk", "2L", "user1")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	if item.Text != "Milk" {
		t.Errorf("expected Milk, got %s", item.Text)
	}
	if item.Quantity != "2L" {
		t.Errorf("expected 2L, got %s", item.Quantity)
	}
	if item.Checked {
		t.Error("expected unchecked")
	}

	// Empty text.
	_, err = fs.AddListItem(list.ID, "", "", "user1")
	if err == nil {
		t.Fatal("expected error for empty text")
	}

	// Empty list ID.
	_, err = fs.AddListItem("", "Eggs", "", "user1")
	if err == nil {
		t.Fatal("expected error for empty list ID")
	}
}

func TestCheckItem(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("Groceries", "shopping", "user1", newUUID)
	item, _ := fs.AddListItem(list.ID, "Milk", "1L", "user1")

	// Check.
	if err := fs.CheckItem(item.ID, true); err != nil {
		t.Fatalf("CheckItem true: %v", err)
	}
	items, _ := fs.GetListItems(list.ID)
	if len(items) == 0 {
		t.Fatal("expected at least one item")
	}
	if !items[0].Checked {
		t.Error("expected item to be checked")
	}

	// Uncheck.
	if err := fs.CheckItem(item.ID, false); err != nil {
		t.Fatalf("CheckItem false: %v", err)
	}
	items, _ = fs.GetListItems(list.ID)
	if items[0].Checked {
		t.Error("expected item to be unchecked")
	}
}

func TestRemoveListItem(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("Groceries", "shopping", "user1", newUUID)
	item, _ := fs.AddListItem(list.ID, "Milk", "1L", "user1")

	if err := fs.RemoveListItem(item.ID); err != nil {
		t.Fatalf("RemoveListItem: %v", err)
	}

	items, _ := fs.GetListItems(list.ID)
	if len(items) != 0 {
		t.Errorf("expected 0 items after removal, got %d", len(items))
	}
}

func TestGetListItems(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("Groceries", "shopping", "user1", newUUID)
	fs.AddListItem(list.ID, "Milk", "1L", "user1")
	fs.AddListItem(list.ID, "Eggs", "12", "user2")
	fs.AddListItem(list.ID, "Bread", "", "user1")

	items, err := fs.GetListItems(list.ID)
	if err != nil {
		t.Fatalf("GetListItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].Text != "Milk" {
		t.Errorf("expected Milk first, got %s", items[0].Text)
	}
	if items[1].AddedBy != "user2" {
		t.Errorf("expected user2 for Eggs, got %s", items[1].AddedBy)
	}
}

func TestToolFamilyListAdd(t *testing.T) {
	fs := newTestFamilyService(t)
	oldGlobal := globalFamilyService
	globalFamilyService = fs
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()

	// Add without listId — should create default shopping list.
	input, _ := json.Marshal(map[string]any{"text": "Apples", "quantity": "3"})
	result, err := toolFamilyListAdd(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolFamilyListAdd: %v", err)
	}
	if !strings.Contains(result, "added") {
		t.Errorf("expected 'added' in result, got: %s", result)
	}

	// View to check it's there.
	viewInput, _ := json.Marshal(map[string]any{"listType": "shopping"})
	viewResult, err := toolFamilyListView(ctx, cfg, viewInput)
	if err != nil {
		t.Fatalf("toolFamilyListView: %v", err)
	}
	if !strings.Contains(viewResult, "Shopping") {
		t.Errorf("expected 'Shopping' list in result, got: %s", viewResult)
	}

	// Parse the list ID from the created list.
	lists, _ := fs.ListLists()
	if len(lists) == 0 {
		t.Fatal("expected at least one list")
	}

	// Add with explicit listId.
	input2, _ := json.Marshal(map[string]any{"listId": lists[0].ID, "text": "Bananas"})
	result2, err := toolFamilyListAdd(ctx, cfg, input2)
	if err != nil {
		t.Fatalf("toolFamilyListAdd with listId: %v", err)
	}
	if !strings.Contains(result2, "Bananas") {
		t.Errorf("expected 'Bananas' in result, got: %s", result2)
	}

	// Missing text.
	input3, _ := json.Marshal(map[string]any{"listId": lists[0].ID})
	_, err = toolFamilyListAdd(ctx, cfg, input3)
	if err == nil {
		t.Fatal("expected error for missing text")
	}
}

func TestToolFamilyListView(t *testing.T) {
	fs := newTestFamilyService(t)
	oldGlobal := globalFamilyService
	globalFamilyService = fs
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()

	// Create lists.
	list1, _ := fs.CreateList("Groceries", "shopping", "user1", newUUID)
	fs.CreateList("Tasks", "todo", "user1", newUUID)
	fs.AddListItem(list1.ID, "Milk", "1L", "user1")

	// View all lists.
	input, _ := json.Marshal(map[string]any{})
	result, err := toolFamilyListView(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolFamilyListView all: %v", err)
	}
	if !strings.Contains(result, "Groceries") || !strings.Contains(result, "Tasks") {
		t.Errorf("expected both lists in result, got: %s", result)
	}

	// View by type.
	input2, _ := json.Marshal(map[string]any{"listType": "todo"})
	result2, err := toolFamilyListView(ctx, cfg, input2)
	if err != nil {
		t.Fatalf("toolFamilyListView by type: %v", err)
	}
	if !strings.Contains(result2, "Tasks") {
		t.Errorf("expected Tasks in result, got: %s", result2)
	}
	if strings.Contains(result2, "Groceries") {
		t.Errorf("did not expect Groceries in todo filter, got: %s", result2)
	}

	// View specific list items.
	input3, _ := json.Marshal(map[string]any{"listId": list1.ID})
	result3, err := toolFamilyListView(ctx, cfg, input3)
	if err != nil {
		t.Fatalf("toolFamilyListView items: %v", err)
	}
	if !strings.Contains(result3, "Milk") {
		t.Errorf("expected Milk in items, got: %s", result3)
	}
}

func TestToolUserSwitch(t *testing.T) {
	fs := newTestFamilyService(t)
	oldGlobal := globalFamilyService
	globalFamilyService = fs
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()

	fs.AddUser("user1", "Alice", "member")
	fs.GrantPermission("user1", "task.write")

	input, _ := json.Marshal(map[string]any{"userId": "user1"})
	result, err := toolUserSwitch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolUserSwitch: %v", err)
	}
	if !strings.Contains(result, "switched") {
		t.Errorf("expected 'switched' in result, got: %s", result)
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("expected 'Alice' in result, got: %s", result)
	}
	if !strings.Contains(result, "task.write") {
		t.Errorf("expected 'task.write' in result, got: %s", result)
	}

	// Non-existent user.
	input2, _ := json.Marshal(map[string]any{"userId": "nonexistent"})
	_, err = toolUserSwitch(ctx, cfg, input2)
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}

	// Empty userId.
	input3, _ := json.Marshal(map[string]any{})
	_, err = toolUserSwitch(ctx, cfg, input3)
	if err == nil {
		t.Fatal("expected error for empty userId")
	}
}

func TestToolFamilyManage(t *testing.T) {
	fs := newTestFamilyService(t)
	oldGlobal := globalFamilyService
	globalFamilyService = fs
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()

	// Add user.
	input, _ := json.Marshal(map[string]any{"action": "add", "userId": "user1", "displayName": "Alice", "role": "member"})
	result, err := toolFamilyManage(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolFamilyManage add: %v", err)
	}
	if !strings.Contains(result, "added") {
		t.Errorf("expected 'added' in result, got: %s", result)
	}

	// List users.
	input2, _ := json.Marshal(map[string]any{"action": "list"})
	result2, err := toolFamilyManage(ctx, cfg, input2)
	if err != nil {
		t.Fatalf("toolFamilyManage list: %v", err)
	}
	if !strings.Contains(result2, "Alice") {
		t.Errorf("expected 'Alice' in list result, got: %s", result2)
	}

	// Update user.
	input3, _ := json.Marshal(map[string]any{"action": "update", "userId": "user1", "displayName": "Alice Updated"})
	result3, err := toolFamilyManage(ctx, cfg, input3)
	if err != nil {
		t.Fatalf("toolFamilyManage update: %v", err)
	}
	if !strings.Contains(result3, "updated") {
		t.Errorf("expected 'updated' in result, got: %s", result3)
	}

	// Permissions: grant.
	input4, _ := json.Marshal(map[string]any{"action": "permissions", "userId": "user1", "permission": "task.write", "grant": true})
	result4, err := toolFamilyManage(ctx, cfg, input4)
	if err != nil {
		t.Fatalf("toolFamilyManage permissions grant: %v", err)
	}
	if !strings.Contains(result4, "task.write") {
		t.Errorf("expected 'task.write' in result, got: %s", result4)
	}

	// Permissions: revoke.
	input5, _ := json.Marshal(map[string]any{"action": "permissions", "userId": "user1", "permission": "task.write", "grant": false})
	_, err = toolFamilyManage(ctx, cfg, input5)
	if err != nil {
		t.Fatalf("toolFamilyManage permissions revoke: %v", err)
	}

	// Remove user.
	input6, _ := json.Marshal(map[string]any{"action": "remove", "userId": "user1"})
	result6, err := toolFamilyManage(ctx, cfg, input6)
	if err != nil {
		t.Fatalf("toolFamilyManage remove: %v", err)
	}
	if !strings.Contains(result6, "removed") {
		t.Errorf("expected 'removed' in result, got: %s", result6)
	}

	// Unknown action.
	input7, _ := json.Marshal(map[string]any{"action": "unknown"})
	_, err = toolFamilyManage(ctx, cfg, input7)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestToolFamilyNotEnabled(t *testing.T) {
	oldGlobal := globalFamilyService
	globalFamilyService = nil
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()
	input, _ := json.Marshal(map[string]any{})

	if _, err := toolFamilyListAdd(ctx, cfg, input); err == nil {
		t.Fatal("expected error when family not enabled")
	}
	if _, err := toolFamilyListView(ctx, cfg, input); err == nil {
		t.Fatal("expected error when family not enabled")
	}
	if _, err := toolUserSwitch(ctx, cfg, input); err == nil {
		t.Fatal("expected error when family not enabled")
	}
	if _, err := toolFamilyManage(ctx, cfg, input); err == nil {
		t.Fatal("expected error when family not enabled")
	}
}

func TestFamilyConfigDefaults(t *testing.T) {
	c := FamilyConfig{}
	if c.MaxUsersOrDefault() != 10 {
		t.Errorf("expected default maxUsers 10, got %d", c.MaxUsersOrDefault())
	}
	if c.DefaultRateLimitOrDefault() != 100 {
		t.Errorf("expected default rateLimit 100, got %d", c.DefaultRateLimitOrDefault())
	}

	c2 := FamilyConfig{MaxUsers: 5, DefaultRateLimit: 50}
	if c2.MaxUsersOrDefault() != 5 {
		t.Errorf("expected maxUsers 5, got %d", c2.MaxUsersOrDefault())
	}
	if c2.DefaultRateLimitOrDefault() != 50 {
		t.Errorf("expected rateLimit 50, got %d", c2.DefaultRateLimitOrDefault())
	}
}

// --- from finance_test.go ---

func setupFinanceTestDB(t *testing.T) (string, *FinanceService) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initFinanceDB(dbPath); err != nil {
		t.Fatalf("initFinanceDB: %v", err)
	}
	cfg := &Config{
		HistoryDB: dbPath,
		Finance: FinanceConfig{
			Enabled:         true,
			DefaultCurrency: "TWD",
		},
	}
	svc := newFinanceService(cfg)
	return dbPath, svc
}

func TestInitFinanceDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initFinanceDB(dbPath); err != nil {
		t.Fatalf("initFinanceDB: %v", err)
	}
	// Verify tables exist.
	rows, err := db.Query(dbPath, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
	}
	names := make(map[string]bool)
	for _, row := range rows {
		names[jsonStr(row["name"])] = true
	}
	for _, want := range []string{"expenses", "expense_budgets", "price_watches"} {
		if !names[want] {
			t.Errorf("missing table %s, have: %v", want, names)
		}
	}
}

func TestInitFinanceDB_InvalidPath(t *testing.T) {
	err := initFinanceDB("/nonexistent/path/db.sqlite")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestParseExpenseNL_Chinese(t *testing.T) {
	tests := []struct {
		input       string
		wantAmount  float64
		wantCur     string
		wantCat     string
		wantDescLen bool // true = non-empty description
	}{
		{"午餐 350 元", 350, "TWD", "food", true},
		{"早餐 80 元", 80, "TWD", "food", true},
		{"電費 2000", 2000, "TWD", "utilities", true},
		{"計程車 250", 250, "TWD", "transport", true},
		{"Netflix 訂閱 390", 390, "TWD", "entertainment", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			amount, cur, cat, desc := parseExpenseNL(tt.input, "TWD")
			if amount != tt.wantAmount {
				t.Errorf("amount: got %f, want %f", amount, tt.wantAmount)
			}
			if cur != tt.wantCur {
				t.Errorf("currency: got %s, want %s", cur, tt.wantCur)
			}
			if cat != tt.wantCat {
				t.Errorf("category: got %s, want %s", cat, tt.wantCat)
			}
			if tt.wantDescLen && desc == "" {
				t.Error("expected non-empty description")
			}
		})
	}
}

func TestParseExpenseNL_English(t *testing.T) {
	tests := []struct {
		input      string
		wantAmount float64
		wantCur    string
		wantCat    string
	}{
		{"coffee $5.50", 5.50, "USD", "food"},
		{"$12 lunch", 12, "USD", "food"},
		{"rent 2000", 2000, "TWD", "housing"},
		{"uber 350", 350, "TWD", "transport"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			amount, cur, cat, _ := parseExpenseNL(tt.input, "TWD")
			if amount != tt.wantAmount {
				t.Errorf("amount: got %f, want %f", amount, tt.wantAmount)
			}
			if cur != tt.wantCur {
				t.Errorf("currency: got %s, want %s", cur, tt.wantCur)
			}
			if cat != tt.wantCat {
				t.Errorf("category: got %s, want %s", cat, tt.wantCat)
			}
		})
	}
}

func TestParseExpenseNL_EmptyInput(t *testing.T) {
	amount, cur, cat, desc := parseExpenseNL("", "USD")
	if amount != 0 || cur != "USD" || cat != "other" || desc != "" {
		t.Errorf("unexpected result for empty: amount=%f cur=%s cat=%s desc=%s", amount, cur, cat, desc)
	}
}

func TestParseExpenseNL_Euro(t *testing.T) {
	amount, cur, _, _ := parseExpenseNL("groceries €25", "TWD")
	if amount != 25 {
		t.Errorf("amount: got %f, want 25", amount)
	}
	if cur != "EUR" {
		t.Errorf("currency: got %s, want EUR", cur)
	}
}

func TestParseExpenseNL_Yen(t *testing.T) {
	// Large yen amount should be JPY.
	amount, cur, _, _ := parseExpenseNL("ラーメン ¥1500", "TWD")
	if amount != 1500 {
		t.Errorf("amount: got %f, want 1500", amount)
	}
	if cur != "JPY" {
		t.Errorf("currency: got %s, want JPY", cur)
	}

	// Explicit 円 marker.
	amount2, cur2, _, _ := parseExpenseNL("寿司 3000 円", "TWD")
	if amount2 != 3000 {
		t.Errorf("amount: got %f, want 3000", amount2)
	}
	if cur2 != "JPY" {
		t.Errorf("currency: got %s, want JPY", cur2)
	}
}

// TestCategorizeExpense moved to internal/life/finance/finance_test.go.

func TestAddExpense(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	exp, err := svc.AddExpense("user1", 350, "TWD", "food", "lunch", nil)
	if err != nil {
		t.Fatalf("AddExpense: %v", err)
	}
	if exp.Amount != 350 {
		t.Errorf("amount: got %f, want 350", exp.Amount)
	}
	if exp.Currency != "TWD" {
		t.Errorf("currency: got %s, want TWD", exp.Currency)
	}
	if exp.Category != "food" {
		t.Errorf("category: got %s, want food", exp.Category)
	}
	if exp.UserID != "user1" {
		t.Errorf("userID: got %s, want user1", exp.UserID)
	}
}

func TestAddExpense_Defaults(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	// Empty userID should default.
	exp, err := svc.AddExpense("", 100, "", "", "coffee", nil)
	if err != nil {
		t.Fatalf("AddExpense: %v", err)
	}
	if exp.UserID != "default" {
		t.Errorf("userID: got %s, want default", exp.UserID)
	}
	if exp.Currency != "TWD" {
		t.Errorf("currency: got %s, want TWD", exp.Currency)
	}
	if exp.Category != "food" {
		t.Errorf("category: got %s, want food (auto-categorized from 'coffee')", exp.Category)
	}
}

func TestAddExpense_InvalidAmount(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	_, err := svc.AddExpense("user1", 0, "TWD", "food", "test", nil)
	if err == nil {
		t.Fatal("expected error for zero amount")
	}
	_, err = svc.AddExpense("user1", -10, "TWD", "food", "test", nil)
	if err == nil {
		t.Fatal("expected error for negative amount")
	}
}

func TestListExpenses(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	// Add some expenses.
	svc.AddExpense("user1", 100, "TWD", "food", "breakfast", nil)
	svc.AddExpense("user1", 200, "TWD", "transport", "taxi", nil)
	svc.AddExpense("user1", 300, "TWD", "food", "dinner", nil)
	svc.AddExpense("user2", 500, "TWD", "food", "lunch", nil)

	// List all for user1.
	expenses, err := svc.ListExpenses("user1", "", "", 10)
	if err != nil {
		t.Fatalf("ListExpenses: %v", err)
	}
	if len(expenses) != 3 {
		t.Errorf("expected 3 expenses, got %d", len(expenses))
	}

	// List by category.
	foodExpenses, err := svc.ListExpenses("user1", "", "food", 10)
	if err != nil {
		t.Fatalf("ListExpenses: %v", err)
	}
	if len(foodExpenses) != 2 {
		t.Errorf("expected 2 food expenses, got %d", len(foodExpenses))
	}

	// List for user2.
	user2Expenses, err := svc.ListExpenses("user2", "", "", 10)
	if err != nil {
		t.Fatalf("ListExpenses: %v", err)
	}
	if len(user2Expenses) != 1 {
		t.Errorf("expected 1 expense for user2, got %d", len(user2Expenses))
	}
}

func TestListExpenses_DefaultUser(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.AddExpense("", 100, "TWD", "food", "test", nil)

	expenses, err := svc.ListExpenses("", "", "", 10)
	if err != nil {
		t.Fatalf("ListExpenses: %v", err)
	}
	if len(expenses) != 1 {
		t.Fatalf("expected 1 expense, got %d", len(expenses))
	}
	if expenses[0].UserID != "default" {
		t.Errorf("expected default user, got %s", expenses[0].UserID)
	}
}

func TestDeleteExpense(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	exp, _ := svc.AddExpense("user1", 100, "TWD", "food", "test", nil)

	// Verify it exists.
	expenses, _ := svc.ListExpenses("user1", "", "", 10)
	if len(expenses) != 1 {
		t.Fatalf("expected 1 expense, got %d", len(expenses))
	}

	// Delete it.
	if err := svc.DeleteExpense("user1", exp.ID); err != nil {
		t.Fatalf("DeleteExpense: %v", err)
	}

	// Verify it's gone.
	expenses, _ = svc.ListExpenses("user1", "", "", 10)
	if len(expenses) != 0 {
		t.Errorf("expected 0 expenses after delete, got %d", len(expenses))
	}
}

func TestGenerateReport_Today(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.AddExpense("user1", 100, "TWD", "food", "breakfast", nil)
	svc.AddExpense("user1", 250, "TWD", "transport", "taxi", nil)
	svc.AddExpense("user1", 150, "TWD", "food", "lunch", nil)

	report, err := svc.GenerateReport("user1", "today", "TWD")
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}

	if report.TotalAmount != 500 {
		t.Errorf("total: got %f, want 500", report.TotalAmount)
	}
	if report.Count != 3 {
		t.Errorf("count: got %d, want 3", report.Count)
	}
	if report.ByCategory["food"] != 250 {
		t.Errorf("food: got %f, want 250", report.ByCategory["food"])
	}
	if report.ByCategory["transport"] != 250 {
		t.Errorf("transport: got %f, want 250", report.ByCategory["transport"])
	}
	if report.Currency != "TWD" {
		t.Errorf("currency: got %s, want TWD", report.Currency)
	}
}

func TestGenerateReport_Week(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.AddExpense("user1", 500, "TWD", "food", "groceries", nil)

	report, err := svc.GenerateReport("user1", "week", "TWD")
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.Period != "week" {
		t.Errorf("period: got %s, want week", report.Period)
	}
	if report.Count != 1 {
		t.Errorf("count: got %d, want 1", report.Count)
	}
}

func TestGenerateReport_Month(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.AddExpense("user1", 1000, "TWD", "housing", "rent", nil)
	svc.AddExpense("user1", 500, "TWD", "utilities", "electric", nil)

	report, err := svc.GenerateReport("user1", "month", "TWD")
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.TotalAmount != 1500 {
		t.Errorf("total: got %f, want 1500", report.TotalAmount)
	}
}

func TestGenerateReport_Empty(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	report, err := svc.GenerateReport("nobody", "today", "TWD")
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.TotalAmount != 0 {
		t.Errorf("total: got %f, want 0", report.TotalAmount)
	}
	if report.Count != 0 {
		t.Errorf("count: got %d, want 0", report.Count)
	}
}

func TestSetBudget(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	err := svc.SetBudget("user1", "food", 10000, "TWD")
	if err != nil {
		t.Fatalf("SetBudget: %v", err)
	}

	budgets, err := svc.GetBudgets("user1")
	if err != nil {
		t.Fatalf("GetBudgets: %v", err)
	}
	if len(budgets) != 1 {
		t.Fatalf("expected 1 budget, got %d", len(budgets))
	}
	if budgets[0].Category != "food" {
		t.Errorf("category: got %s, want food", budgets[0].Category)
	}
	if budgets[0].MonthlyLimit != 10000 {
		t.Errorf("limit: got %f, want 10000", budgets[0].MonthlyLimit)
	}
}

func TestSetBudget_Update(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.SetBudget("user1", "food", 10000, "TWD")
	// Update the same category.
	svc.SetBudget("user1", "food", 15000, "TWD")

	budgets, _ := svc.GetBudgets("user1")
	if len(budgets) != 1 {
		t.Fatalf("expected 1 budget after update, got %d", len(budgets))
	}
	if budgets[0].MonthlyLimit != 15000 {
		t.Errorf("limit after update: got %f, want 15000", budgets[0].MonthlyLimit)
	}
}

func TestSetBudget_Validation(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	if err := svc.SetBudget("user1", "", 1000, "TWD"); err == nil {
		t.Error("expected error for empty category")
	}
	if err := svc.SetBudget("user1", "food", 0, "TWD"); err == nil {
		t.Error("expected error for zero limit")
	}
	if err := svc.SetBudget("user1", "food", -100, "TWD"); err == nil {
		t.Error("expected error for negative limit")
	}
}

func TestGetBudgets_Empty(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	budgets, err := svc.GetBudgets("nobody")
	if err != nil {
		t.Fatalf("GetBudgets: %v", err)
	}
	if len(budgets) != 0 {
		t.Errorf("expected 0 budgets, got %d", len(budgets))
	}
}

func TestCheckBudgets(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	// Set budgets.
	svc.SetBudget("user1", "food", 5000, "TWD")
	svc.SetBudget("user1", "transport", 3000, "TWD")

	// Add expenses.
	svc.AddExpense("user1", 2500, "TWD", "food", "groceries", nil)
	svc.AddExpense("user1", 3500, "TWD", "transport", "taxi rides", nil)

	statuses, err := svc.CheckBudgets("user1")
	if err != nil {
		t.Fatalf("CheckBudgets: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 budget statuses, got %d", len(statuses))
	}

	// Find food and transport statuses.
	statusMap := make(map[string]ExpenseBudgetStatus)
	for _, s := range statuses {
		statusMap[s.Category] = s
	}

	food := statusMap["food"]
	if food.Spent != 2500 {
		t.Errorf("food spent: got %f, want 2500", food.Spent)
	}
	if food.Remaining != 2500 {
		t.Errorf("food remaining: got %f, want 2500", food.Remaining)
	}
	if food.OverBudget {
		t.Error("food should not be over budget")
	}
	if food.Percentage != 50 {
		t.Errorf("food percentage: got %f, want 50", food.Percentage)
	}

	transport := statusMap["transport"]
	if transport.Spent != 3500 {
		t.Errorf("transport spent: got %f, want 3500", transport.Spent)
	}
	if !transport.OverBudget {
		t.Error("transport should be over budget")
	}
}

func TestCheckBudgets_NoBudgets(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	statuses, err := svc.CheckBudgets("nobody")
	if err != nil {
		t.Fatalf("CheckBudgets: %v", err)
	}
	if statuses != nil {
		t.Errorf("expected nil statuses, got %v", statuses)
	}
}

func TestToolExpenseAdd(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"text":   "午餐 350 元",
		"userId": "tester",
	})

	result, err := toolExpenseAdd(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseAdd: %v", err)
	}
	if !strings.Contains(result, "350") {
		t.Errorf("expected 350 in result, got: %s", result)
	}
	if !strings.Contains(result, "food") {
		t.Errorf("expected food category, got: %s", result)
	}
}

func TestToolExpenseAdd_ExplicitFields(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"amount":      99.99,
		"currency":    "USD",
		"category":    "entertainment",
		"description": "movie ticket",
		"userId":      "tester",
	})

	result, err := toolExpenseAdd(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseAdd: %v", err)
	}
	if !strings.Contains(result, "99.99") {
		t.Errorf("expected 99.99 in result, got: %s", result)
	}
	if !strings.Contains(result, "USD") {
		t.Errorf("expected USD, got: %s", result)
	}
}

func TestToolExpenseAdd_NoAmount(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"text": "something without number",
	})

	_, err := toolExpenseAdd(ctx, cfg, input)
	if err == nil {
		t.Fatal("expected error when no amount can be determined")
	}
}

func TestToolExpenseReport(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	svc.AddExpense("tester", 500, "TWD", "food", "dinner", nil)

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"period": "today",
		"userId": "tester",
	})

	result, err := toolExpenseReport(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseReport: %v", err)
	}
	if !strings.Contains(result, "500") {
		t.Errorf("expected 500 in report, got: %s", result)
	}
	if !strings.Contains(result, "food") {
		t.Errorf("expected food category in report, got: %s", result)
	}
}

func TestToolExpenseBudget_Set(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"action":   "set",
		"category": "food",
		"limit":    10000,
		"userId":   "tester",
	})

	result, err := toolExpenseBudget(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseBudget set: %v", err)
	}
	if !strings.Contains(result, "food") || !strings.Contains(result, "10000") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestToolExpenseBudget_List(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	svc.SetBudget("tester", "food", 5000, "TWD")

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"action": "list",
		"userId": "tester",
	})

	result, err := toolExpenseBudget(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseBudget list: %v", err)
	}
	if !strings.Contains(result, "food") {
		t.Errorf("expected food in budget list, got: %s", result)
	}
}

func TestToolExpenseBudget_Check(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	svc.SetBudget("tester", "food", 5000, "TWD")
	svc.AddExpense("tester", 2000, "TWD", "food", "groceries", nil)

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"action": "check",
		"userId": "tester",
	})

	result, err := toolExpenseBudget(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseBudget check: %v", err)
	}
	if !strings.Contains(result, "food") {
		t.Errorf("expected food in budget check, got: %s", result)
	}
	if !strings.Contains(result, "2000") {
		t.Errorf("expected 2000 spent, got: %s", result)
	}
}

func TestToolExpenseBudget_InvalidAction(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"action": "invalid",
	})

	_, err := toolExpenseBudget(ctx, cfg, input)
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestToolExpenseAdd_NotInitialized(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"text": "lunch 100"})
	_, err := toolExpenseAdd(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when service not initialized")
	}
}

func TestFinanceConfig_DefaultCurrency(t *testing.T) {
	cfg := FinanceConfig{}
	if got := cfg.DefaultCurrencyOrTWD(); got != "TWD" {
		t.Errorf("default currency: got %s, want TWD", got)
	}

	cfg.DefaultCurrency = "JPY"
	if got := cfg.DefaultCurrencyOrTWD(); got != "JPY" {
		t.Errorf("custom currency: got %s, want JPY", got)
	}
}

func TestPeriodToDateFilter(t *testing.T) {
	tests := []struct {
		period string
		hasSQL bool
	}{
		{"today", true},
		{"week", true},
		{"month", true},
		{"year", true},
		{"all", false},
		{"", false},
	}

	for _, tt := range tests {
		result := periodToDateFilter(tt.period)
		if tt.hasSQL && result == "" {
			t.Errorf("period %q: expected SQL filter, got empty", tt.period)
		}
		if !tt.hasSQL && result != "" {
			t.Errorf("period %q: expected empty filter, got %s", tt.period, result)
		}
	}
}

// Ensure the test DB file gets cleaned up.
func TestFinanceDB_Cleanup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cleanup.db")
	initFinanceDB(dbPath)
	_, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("DB file should exist: %v", err)
	}
}

// --- from goals_test.go ---

// --- Test Helpers ---

func setupGoalsTestDB(t *testing.T) (string, *GoalsService) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_goals.db")

	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatalf("create db file: %v", err)
	}
	f.Close()

	if err := initGoalsDB(dbPath); err != nil {
		t.Fatalf("initGoalsDB: %v", err)
	}
	cfg := &Config{HistoryDB: dbPath}
	svc := newGoalsService(cfg)
	return dbPath, svc
}

// --- DB Init ---

func TestInitGoalsDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	f.Close()

	if err := initGoalsDB(dbPath); err != nil {
		t.Fatalf("initGoalsDB: %v", err)
	}

	// Verify table exists by running a query.
	rows, err := db.Query(dbPath, "SELECT COUNT(*) as cnt FROM goals;")
	if err != nil {
		t.Fatalf("query after init: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected query result")
	}
}

// --- CreateGoal ---

func TestCreateGoal(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, err := svc.CreateGoal(newUUID(), "user1", "Learn Go", "Master Go programming", "learning", "2026-12-31", newUUID)
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if goal.ID == "" {
		t.Error("expected non-empty ID")
	}
	if goal.Title != "Learn Go" {
		t.Errorf("expected title 'Learn Go', got %q", goal.Title)
	}
	if goal.Status != "active" {
		t.Errorf("expected status 'active', got %q", goal.Status)
	}
	if goal.Progress != 0 {
		t.Errorf("expected progress 0, got %d", goal.Progress)
	}
	if goal.Category != "learning" {
		t.Errorf("expected category 'learning', got %q", goal.Category)
	}
	if goal.TargetDate != "2026-12-31" {
		t.Errorf("expected target_date '2026-12-31', got %q", goal.TargetDate)
	}
	if goal.CreatedAt == "" {
		t.Error("expected non-empty CreatedAt")
	}
	// Should have default milestones since description has no numbered/bullet items.
	if len(goal.Milestones) != 3 {
		t.Errorf("expected 3 default milestones, got %d", len(goal.Milestones))
	}
}

func TestCreateGoal_EmptyTitle(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	_, err := svc.CreateGoal(newUUID(), "user1", "", "no title", "", "", newUUID)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestCreateGoal_WithMilestones(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	desc := `Study plan:
1. Buy textbooks
2. Complete chapters 1-5
3. Take practice tests
4. Review weak areas`

	goal, err := svc.CreateGoal(newUUID(), "user1", "Pass JLPT N2", desc, "learning", "2026-07-01", newUUID)
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if len(goal.Milestones) != 4 {
		t.Errorf("expected 4 milestones from description, got %d", len(goal.Milestones))
	}
	if goal.Milestones[0].Title != "Buy textbooks" {
		t.Errorf("expected first milestone 'Buy textbooks', got %q", goal.Milestones[0].Title)
	}
	if goal.Milestones[3].Title != "Review weak areas" {
		t.Errorf("expected last milestone 'Review weak areas', got %q", goal.Milestones[3].Title)
	}
}

func TestCreateGoal_BulletMilestones(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	desc := `Steps:
- Research options
- Make a decision
- Execute plan`

	goal, err := svc.CreateGoal(newUUID(), "user1", "Buy a house", desc, "financial", "2027-01-01", newUUID)
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if len(goal.Milestones) != 3 {
		t.Errorf("expected 3 milestones from bullets, got %d", len(goal.Milestones))
	}
	if goal.Milestones[0].Title != "Research options" {
		t.Errorf("expected 'Research options', got %q", goal.Milestones[0].Title)
	}
}

func TestCreateGoal_DefaultUserID(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, err := svc.CreateGoal(newUUID(), "", "Test goal", "", "", "", newUUID)
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if goal.UserID != "default" {
		t.Errorf("expected user_id 'default', got %q", goal.UserID)
	}
}

// --- ListGoals ---

func TestListGoals(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	// Create multiple goals.
	svc.CreateGoal(newUUID(), "user1", "Goal A", "", "career", "", newUUID)
	svc.CreateGoal(newUUID(), "user1", "Goal B", "", "health", "", newUUID)
	svc.CreateGoal(newUUID(), "user2", "Goal C", "", "learning", "", newUUID)

	goals, err := svc.ListGoals("user1", "", 10)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(goals) != 2 {
		t.Errorf("expected 2 goals for user1, got %d", len(goals))
	}
}

func TestListGoals_FilterStatus(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	g1, _ := svc.CreateGoal(newUUID(), "user1", "Active goal", "", "", "", newUUID)
	svc.CreateGoal(newUUID(), "user1", "Another active", "", "", "", newUUID)

	// Complete one goal.
	svc.UpdateGoal(g1.ID, map[string]any{"status": "completed"})

	active, err := svc.ListGoals("user1", "active", 10)
	if err != nil {
		t.Fatalf("ListGoals active: %v", err)
	}
	if len(active) != 1 {
		t.Errorf("expected 1 active goal, got %d", len(active))
	}

	completed, err := svc.ListGoals("user1", "completed", 10)
	if err != nil {
		t.Fatalf("ListGoals completed: %v", err)
	}
	if len(completed) != 1 {
		t.Errorf("expected 1 completed goal, got %d", len(completed))
	}
}

func TestListGoals_Limit(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	for i := 0; i < 5; i++ {
		svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)
	}

	goals, err := svc.ListGoals("user1", "", 3)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	if len(goals) != 3 {
		t.Errorf("expected 3 goals with limit, got %d", len(goals))
	}
}

// --- GetGoal ---

func TestGetGoal(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal(newUUID(), "user1", "Test Goal", "Some desc", "career", "2026-12-31", newUUID)

	got, err := svc.GetGoal(created.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if got.Title != "Test Goal" {
		t.Errorf("expected 'Test Goal', got %q", got.Title)
	}
	if got.Description != "Some desc" {
		t.Errorf("expected 'Some desc', got %q", got.Description)
	}
	if len(got.Milestones) != 3 {
		t.Errorf("expected 3 milestones, got %d", len(got.Milestones))
	}
}

func TestGetGoal_NotFound(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	_, err := svc.GetGoal("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent goal")
	}
}

// --- UpdateGoal ---

func TestUpdateGoal_Status(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)

	updated, err := svc.UpdateGoal(created.ID, map[string]any{"status": "paused"})
	if err != nil {
		t.Fatalf("UpdateGoal: %v", err)
	}
	if updated.Status != "paused" {
		t.Errorf("expected status 'paused', got %q", updated.Status)
	}
}

func TestUpdateGoal_Progress(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)

	updated, err := svc.UpdateGoal(created.ID, map[string]any{"progress": 50})
	if err != nil {
		t.Fatalf("UpdateGoal: %v", err)
	}
	if updated.Progress != 50 {
		t.Errorf("expected progress 50, got %d", updated.Progress)
	}
}

func TestUpdateGoal_MultipleFields(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal(newUUID(), "user1", "Old Title", "", "", "", newUUID)

	updated, err := svc.UpdateGoal(created.ID, map[string]any{
		"title":       "New Title",
		"category":    "health",
		"target_date": "2027-01-01",
	})
	if err != nil {
		t.Fatalf("UpdateGoal: %v", err)
	}
	if updated.Title != "New Title" {
		t.Errorf("expected 'New Title', got %q", updated.Title)
	}
	if updated.Category != "health" {
		t.Errorf("expected 'health', got %q", updated.Category)
	}
	if updated.TargetDate != "2027-01-01" {
		t.Errorf("expected '2027-01-01', got %q", updated.TargetDate)
	}
}

func TestUpdateGoal_EmptyFields(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	created, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)

	updated, err := svc.UpdateGoal(created.ID, map[string]any{})
	if err != nil {
		t.Fatalf("UpdateGoal empty: %v", err)
	}
	if updated.Title != "Goal" {
		t.Errorf("expected unchanged title 'Goal', got %q", updated.Title)
	}
}

// --- CompleteMilestone ---

func TestCompleteMilestone(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)
	if len(goal.Milestones) < 3 {
		t.Fatalf("expected at least 3 milestones, got %d", len(goal.Milestones))
	}

	err := svc.CompleteMilestone(goal.ID, goal.Milestones[0].ID)
	if err != nil {
		t.Fatalf("CompleteMilestone: %v", err)
	}

	got, _ := svc.GetGoal(goal.ID)
	if !got.Milestones[0].Done {
		t.Error("expected first milestone to be done")
	}
}

func TestCompleteMilestone_AutoProgress(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)
	// Default: 3 milestones.

	// Complete 1 of 3 -> 33%.
	svc.CompleteMilestone(goal.ID, goal.Milestones[0].ID)
	got, _ := svc.GetGoal(goal.ID)
	if got.Progress != 33 {
		t.Errorf("expected progress 33 after 1/3, got %d", got.Progress)
	}

	// Complete 2 of 3 -> 66%.
	svc.CompleteMilestone(goal.ID, goal.Milestones[1].ID)
	got, _ = svc.GetGoal(goal.ID)
	if got.Progress != 66 {
		t.Errorf("expected progress 66 after 2/3, got %d", got.Progress)
	}

	// Complete 3 of 3 -> 100%.
	svc.CompleteMilestone(goal.ID, goal.Milestones[2].ID)
	got, _ = svc.GetGoal(goal.ID)
	if got.Progress != 100 {
		t.Errorf("expected progress 100 after 3/3, got %d", got.Progress)
	}
}

func TestCompleteMilestone_NotFound(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)

	err := svc.CompleteMilestone(goal.ID, "nonexistent-milestone")
	if err == nil {
		t.Fatal("expected error for nonexistent milestone")
	}
}

// --- AddMilestone ---

func TestAddMilestone(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)
	initialCount := len(goal.Milestones)

	updated, err := svc.AddMilestone(goal.ID, newUUID(), "New milestone", "2026-06-01")
	if err != nil {
		t.Fatalf("AddMilestone: %v", err)
	}
	if len(updated.Milestones) != initialCount+1 {
		t.Errorf("expected %d milestones, got %d", initialCount+1, len(updated.Milestones))
	}

	last := updated.Milestones[len(updated.Milestones)-1]
	if last.Title != "New milestone" {
		t.Errorf("expected 'New milestone', got %q", last.Title)
	}
	if last.DueDate != "2026-06-01" {
		t.Errorf("expected due_date '2026-06-01', got %q", last.DueDate)
	}
	if last.Done {
		t.Error("new milestone should not be done")
	}
}

func TestAddMilestone_EmptyTitle(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)

	_, err := svc.AddMilestone(goal.ID, newUUID(), "", "")
	if err == nil {
		t.Fatal("expected error for empty milestone title")
	}
}

func TestAddMilestone_RecalculatesProgress(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)
	// Default 3 milestones. Complete 1 -> 33%.
	svc.CompleteMilestone(goal.ID, goal.Milestones[0].ID)

	// Add a 4th milestone. Now 1/4 done -> 25%.
	updated, _ := svc.AddMilestone(goal.ID, newUUID(), "Extra step", "")
	if updated.Progress != 25 {
		t.Errorf("expected progress 25 after adding milestone (1/4 done), got %d", updated.Progress)
	}
}

// --- ReviewGoal ---

func TestReviewGoal(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)

	err := svc.ReviewGoal(goal.ID, "Making good progress")
	if err != nil {
		t.Fatalf("ReviewGoal: %v", err)
	}

	got, _ := svc.GetGoal(goal.ID)
	if len(got.ReviewNotes) != 1 {
		t.Fatalf("expected 1 review note, got %d", len(got.ReviewNotes))
	}
	if got.ReviewNotes[0].Note != "Making good progress" {
		t.Errorf("expected note 'Making good progress', got %q", got.ReviewNotes[0].Note)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if got.ReviewNotes[0].Date != today {
		t.Errorf("expected date %s, got %s", today, got.ReviewNotes[0].Date)
	}
}

func TestReviewGoal_Multiple(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)

	svc.ReviewGoal(goal.ID, "Week 1 review")
	svc.ReviewGoal(goal.ID, "Week 2 review")

	got, _ := svc.GetGoal(goal.ID)
	if len(got.ReviewNotes) != 2 {
		t.Errorf("expected 2 review notes, got %d", len(got.ReviewNotes))
	}
}

func TestReviewGoal_EmptyNote(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Goal", "", "", "", newUUID)

	err := svc.ReviewGoal(goal.ID, "")
	if err == nil {
		t.Fatal("expected error for empty note")
	}
}

// --- GetStaleGoals ---

func TestGetStaleGoals(t *testing.T) {
	dbPath, svc := setupGoalsTestDB(t)

	// Create a goal and manually set its updated_at to 30 days ago.
	goal, _ := svc.CreateGoal(newUUID(), "user1", "Old goal", "", "", "", newUUID)
	oldDate := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	svc.UpdateGoal(goal.ID, map[string]any{"title": "Old goal"}) // just to have a valid update
	// Force the updated_at to be old via direct SQL.
	forceSQL := "UPDATE goals SET updated_at = '" + db.Escape(oldDate) + "' WHERE id = '" + db.Escape(goal.ID) + "';"
	db.Query(dbPath, forceSQL)

	// Create a fresh goal.
	svc.CreateGoal(newUUID(), "user1", "Fresh goal", "", "", "", newUUID)

	stale, err := svc.GetStaleGoals("user1", 14)
	if err != nil {
		t.Fatalf("GetStaleGoals: %v", err)
	}
	if len(stale) != 1 {
		t.Errorf("expected 1 stale goal, got %d", len(stale))
	}
	if len(stale) > 0 && stale[0].Title != "Old goal" {
		t.Errorf("expected stale goal 'Old goal', got %q", stale[0].Title)
	}
}

func TestGetStaleGoals_NoStale(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	// Create a fresh goal (just created, not stale).
	svc.CreateGoal(newUUID(), "user1", "Fresh goal", "", "", "", newUUID)

	stale, err := svc.GetStaleGoals("user1", 14)
	if err != nil {
		t.Fatalf("GetStaleGoals: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected 0 stale goals, got %d", len(stale))
	}
}

func TestGetStaleGoals_ExcludesCompleted(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	goal, _ := svc.CreateGoal(newUUID(), "user1", "Old completed", "", "", "", newUUID)
	oldDate := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	svc.UpdateGoal(goal.ID, map[string]any{"status": "completed"})
	forceSQL := "UPDATE goals SET updated_at = '" + db.Escape(oldDate) + "' WHERE id = '" + db.Escape(goal.ID) + "';"
	db.Query(svc.DBPath(), forceSQL)

	stale, err := svc.GetStaleGoals("user1", 14)
	if err != nil {
		t.Fatalf("GetStaleGoals: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected 0 stale goals (completed excluded), got %d", len(stale))
	}
}

// --- GoalSummary ---

func TestGoalSummary(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	svc.CreateGoal(newUUID(), "user1", "Career goal", "", "career", "", newUUID)
	svc.CreateGoal(newUUID(), "user1", "Health goal", "", "health", "", newUUID)
	g3, _ := svc.CreateGoal(newUUID(), "user1", "Learning goal", "", "learning", "", newUUID)

	// Complete one goal.
	svc.UpdateGoal(g3.ID, map[string]any{"status": "completed"})

	summary, err := svc.GoalSummary("user1")
	if err != nil {
		t.Fatalf("GoalSummary: %v", err)
	}

	activeCount, ok := summary["active_count"]
	if !ok {
		t.Fatal("expected active_count in summary")
	}
	if activeCount.(int) != 2 {
		t.Errorf("expected 2 active, got %v", activeCount)
	}

	totalCount, ok := summary["total_count"]
	if !ok {
		t.Fatal("expected total_count in summary")
	}
	if totalCount.(int) != 3 {
		t.Errorf("expected 3 total, got %v", totalCount)
	}

	byCategory, ok := summary["by_category"]
	if !ok {
		t.Fatal("expected by_category in summary")
	}
	cats := byCategory.(map[string]int)
	if cats["career"] != 1 {
		t.Errorf("expected 1 career goal, got %d", cats["career"])
	}
	if cats["health"] != 1 {
		t.Errorf("expected 1 health goal, got %d", cats["health"])
	}
}

func TestGoalSummary_Overdue(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	// Create a goal with past target date.
	svc.CreateGoal(newUUID(), "user1", "Overdue goal", "", "", "2020-01-01", newUUID)
	svc.CreateGoal(newUUID(), "user1", "Future goal", "", "", "2099-12-31", newUUID)

	summary, err := svc.GoalSummary("user1")
	if err != nil {
		t.Fatalf("GoalSummary: %v", err)
	}

	overdue, ok := summary["overdue"]
	if !ok {
		t.Fatal("expected overdue in summary")
	}
	if overdue.(int) != 1 {
		t.Errorf("expected 1 overdue, got %v", overdue)
	}
}

func TestGoalSummary_Empty(t *testing.T) {
	_, svc := setupGoalsTestDB(t)

	summary, err := svc.GoalSummary("user1")
	if err != nil {
		t.Fatalf("GoalSummary: %v", err)
	}
	if summary["active_count"].(int) != 0 {
		t.Errorf("expected 0 active, got %v", summary["active_count"])
	}
	if summary["total_count"].(int) != 0 {
		t.Errorf("expected 0 total, got %v", summary["total_count"])
	}
}

// --- Tool Handlers ---

func TestToolGoalCreate(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	ctx := withApp(context.Background(), &App{Goals: svc})

	input := json.RawMessage(`{"title":"Pass JLPT N2","description":"Study Japanese","category":"learning","target_date":"2026-07-01"}`)
	result, err := toolGoalCreate(ctx, &Config{}, input)
	if err != nil {
		t.Fatalf("toolGoalCreate: %v", err)
	}

	var goal Goal
	if err := json.Unmarshal([]byte(result), &goal); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if goal.Title != "Pass JLPT N2" {
		t.Errorf("expected title 'Pass JLPT N2', got %q", goal.Title)
	}
	if goal.Category != "learning" {
		t.Errorf("expected category 'learning', got %q", goal.Category)
	}
}

func TestToolGoalCreate_EmptyTitle(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	ctx := withApp(context.Background(), &App{Goals: svc})

	input := json.RawMessage(`{"title":"","category":"learning"}`)
	_, err := toolGoalCreate(ctx, &Config{}, input)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestToolGoalCreate_NilService(t *testing.T) {
	input := json.RawMessage(`{"title":"test"}`)
	_, err := toolGoalCreate(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
}

func TestToolGoalList(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	ctx := withApp(context.Background(), &App{Goals: svc})

	// Create some goals.
	svc.CreateGoal(newUUID(), "default", "Goal A", "", "", "", newUUID)
	svc.CreateGoal(newUUID(), "default", "Goal B", "", "", "", newUUID)

	input := json.RawMessage(`{"user_id":"default","status":"active","limit":10}`)
	result, err := toolGoalList(ctx, &Config{}, input)
	if err != nil {
		t.Fatalf("toolGoalList: %v", err)
	}

	var goals []Goal
	if err := json.Unmarshal([]byte(result), &goals); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(goals) != 2 {
		t.Errorf("expected 2 goals, got %d", len(goals))
	}
}

func TestToolGoalUpdate(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	ctx := withApp(context.Background(), &App{Goals: svc})

	goal, _ := svc.CreateGoal(newUUID(), "default", "Goal", "", "", "", newUUID)

	// Test update action.
	input := json.RawMessage(`{"id":"` + goal.ID + `","action":"update","status":"paused"}`)
	result, err := toolGoalUpdate(ctx, &Config{}, input)
	if err != nil {
		t.Fatalf("toolGoalUpdate: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}

	got, _ := svc.GetGoal(goal.ID)
	if got.Status != "paused" {
		t.Errorf("expected status 'paused', got %q", got.Status)
	}
}

func TestToolGoalUpdate_CompleteMilestone(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	ctx := withApp(context.Background(), &App{Goals: svc})

	goal, _ := svc.CreateGoal(newUUID(), "default", "Goal", "", "", "", newUUID)
	msID := goal.Milestones[0].ID

	input := json.RawMessage(`{"id":"` + goal.ID + `","action":"complete_milestone","milestone_id":"` + msID + `"}`)
	result, err := toolGoalUpdate(ctx, &Config{}, input)
	if err != nil {
		t.Fatalf("toolGoalUpdate complete_milestone: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}

	got, _ := svc.GetGoal(goal.ID)
	if !got.Milestones[0].Done {
		t.Error("expected milestone to be done")
	}
}

func TestToolGoalUpdate_AddMilestone(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	ctx := withApp(context.Background(), &App{Goals: svc})

	goal, _ := svc.CreateGoal(newUUID(), "default", "Goal", "", "", "", newUUID)
	initialCount := len(goal.Milestones)

	input := json.RawMessage(`{"id":"` + goal.ID + `","action":"add_milestone","title":"Extra step","due_date":"2026-06-01"}`)
	_, err := toolGoalUpdate(ctx, &Config{}, input)
	if err != nil {
		t.Fatalf("toolGoalUpdate add_milestone: %v", err)
	}

	got, _ := svc.GetGoal(goal.ID)
	if len(got.Milestones) != initialCount+1 {
		t.Errorf("expected %d milestones, got %d", initialCount+1, len(got.Milestones))
	}
}

func TestToolGoalUpdate_Review(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	ctx := withApp(context.Background(), &App{Goals: svc})

	goal, _ := svc.CreateGoal(newUUID(), "default", "Goal", "", "", "", newUUID)

	input := json.RawMessage(`{"id":"` + goal.ID + `","action":"review","note":"Going well"}`)
	_, err := toolGoalUpdate(ctx, &Config{}, input)
	if err != nil {
		t.Fatalf("toolGoalUpdate review: %v", err)
	}

	got, _ := svc.GetGoal(goal.ID)
	if len(got.ReviewNotes) != 1 {
		t.Errorf("expected 1 review note, got %d", len(got.ReviewNotes))
	}
}

func TestToolGoalUpdate_MissingID(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	ctx := withApp(context.Background(), &App{Goals: svc})

	input := json.RawMessage(`{"action":"update","status":"paused"}`)
	_, err := toolGoalUpdate(ctx, &Config{}, input)
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestToolGoalReview(t *testing.T) {
	_, svc := setupGoalsTestDB(t)
	ctx := withApp(context.Background(), &App{Goals: svc})

	svc.CreateGoal(newUUID(), "default", "Active goal", "", "career", "", newUUID)

	input := json.RawMessage(`{"user_id":"default"}`)
	result, err := toolGoalReview(ctx, &Config{}, input)
	if err != nil {
		t.Fatalf("toolGoalReview: %v", err)
	}

	var review map[string]any
	if err := json.Unmarshal([]byte(result), &review); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, ok := review["summary"]; !ok {
		t.Error("expected summary in review result")
	}
	if _, ok := review["stale_goals"]; !ok {
		t.Error("expected stale_goals in review result")
	}
}

func TestToolGoalReview_NilService(t *testing.T) {
	input := json.RawMessage(`{"user_id":"default"}`)
	_, err := toolGoalReview(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when service is nil")
	}
}

// --- Milestone Parsing ---

func TestParseMilestonesFromDescription_Numbered(t *testing.T) {
	desc := "1. First step\n2. Second step\n3. Third step"
	ms := parseMilestonesFromDescription(desc)
	if len(ms) != 3 {
		t.Errorf("expected 3 milestones, got %d", len(ms))
	}
	if ms[0].Title != "First step" {
		t.Errorf("expected 'First step', got %q", ms[0].Title)
	}
}

func TestParseMilestonesFromDescription_Bullets(t *testing.T) {
	desc := "- Step A\n- Step B\n- Step C"
	ms := parseMilestonesFromDescription(desc)
	if len(ms) != 3 {
		t.Errorf("expected 3 milestones, got %d", len(ms))
	}
	if ms[0].Title != "Step A" {
		t.Errorf("expected 'Step A', got %q", ms[0].Title)
	}
}

func TestParseMilestonesFromDescription_Empty(t *testing.T) {
	ms := parseMilestonesFromDescription("")
	if len(ms) != 3 {
		t.Errorf("expected 3 default milestones, got %d", len(ms))
	}
	if ms[0].Title != "Plan" {
		t.Errorf("expected 'Plan', got %q", ms[0].Title)
	}
	if ms[1].Title != "Execute" {
		t.Errorf("expected 'Execute', got %q", ms[1].Title)
	}
	if ms[2].Title != "Review" {
		t.Errorf("expected 'Review', got %q", ms[2].Title)
	}
}

func TestParseMilestonesFromDescription_NoPatterns(t *testing.T) {
	desc := "Just a plain text description with no bullet points or numbers."
	ms := parseMilestonesFromDescription(desc)
	if len(ms) != 3 {
		t.Errorf("expected 3 default milestones for plain text, got %d", len(ms))
	}
}

func TestParseMilestonesFromDescription_SingleBullet(t *testing.T) {
	// Only 1 bullet point is not enough to count as structured milestones.
	desc := "- Only one item"
	ms := parseMilestonesFromDescription(desc)
	if len(ms) != 3 {
		t.Errorf("expected 3 default milestones for single bullet, got %d", len(ms))
	}
}

// --- Progress Calculation ---

func TestCalculateMilestoneProgress(t *testing.T) {
	tests := []struct {
		name     string
		ms       []Milestone
		expected int
	}{
		{"empty", []Milestone{}, 0},
		{"none done", []Milestone{{Done: false}, {Done: false}}, 0},
		{"half done", []Milestone{{Done: true}, {Done: false}}, 50},
		{"all done", []Milestone{{Done: true}, {Done: true}}, 100},
		{"one of three", []Milestone{{Done: true}, {Done: false}, {Done: false}}, 33},
		{"two of three", []Milestone{{Done: true}, {Done: true}, {Done: false}}, 66},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateMilestoneProgress(tt.ms)
			if got != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, got)
			}
		})
	}
}

// --- from habits_test.go ---

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
		db.Escape(logID) + "', '" + db.Escape(habitID) + "', '" +
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
	rows, err := db.Query(dbPath, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
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

	id := newUUID()
	if err := svc.CreateHabit(id, "Morning Run", "Run 5km every morning", "daily", "fitness", "", 1); err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	// Verify in DB.
	rows, err := db.Query(dbPath, "SELECT id, name, frequency, target_count, category FROM habits")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
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
	err := svc.CreateHabit(newUUID(), "", "", "daily", "", "", 1)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestCreateHabit_Defaults(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)
	id := newUUID()
	err := svc.CreateHabit(id, "Meditate", "", "", "", "", 0)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	rows, err := db.Query(dbPath, "SELECT frequency, target_count, category FROM habits WHERE id = '"+db.Escape(id)+"'")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
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

	id := newUUID()
	err := svc.CreateHabit(id, "Read", "Read for 30 minutes", "daily", "learning", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	if err := svc.LogHabit(newUUID(), id, "Read chapter 5", "", 1.0); err != nil {
		t.Fatalf("LogHabit: %v", err)
	}

	rows, err := db.Query(dbPath, "SELECT habit_id, note, value FROM habit_logs")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
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
	err := svc.LogHabit(newUUID(), "nonexistent-id", "", "", 1.0)
	if err == nil {
		t.Fatal("expected error for nonexistent habit")
	}
}

func TestLogHabit_EmptyID(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	err := svc.LogHabit(newUUID(), "", "", "", 1.0)
	if err == nil {
		t.Fatal("expected error for empty habit_id")
	}
}

func TestGetStreak_Daily(t *testing.T) {
	dbPath, svc := setupHabitsTestDB(t)

	id := newUUID()
	err := svc.CreateHabit(id, "Exercise", "", "daily", "", "", 1)
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

	id := newUUID()
	err := svc.CreateHabit(id, "Meditate", "", "daily", "", "", 1)
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

	id := newUUID()
	err := svc.CreateHabit(id, "Weekly Review", "", "weekly", "", "", 1)
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

	id1 := newUUID()
	err := svc.CreateHabit(id1, "Exercise", "", "daily", "fitness", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit 1: %v", err)
	}
	id2 := newUUID()
	if err = svc.CreateHabit(id2, "Read", "", "daily", "learning", "", 1); err != nil {
		t.Fatalf("CreateHabit 2: %v", err)
	}

	// Log one habit today.
	if err := svc.LogHabit(newUUID(), id1, "", "", 1.0); err != nil {
		t.Fatalf("LogHabit: %v", err)
	}

	status, err := svc.HabitStatus("", log.Warn)
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

	id := newUUID()
	err := svc.CreateHabit(id, "Journal", "", "daily", "", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	// Log for 3 days.
	for i := 0; i < 3; i++ {
		if err := svc.LogHabit(newUUID(), id, "", "", 1.0); err != nil {
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
	svc.CreateHabit(newUUID(), "A", "", "daily", "", "", 1)
	svc.CreateHabit(newUUID(), "B", "", "daily", "", "", 1)

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

	if err := svc.LogHealth(newUUID(), "steps", 8500, "steps", "manual", ""); err != nil {
		t.Fatalf("LogHealth: %v", err)
	}
	if err := svc.LogHealth(newUUID(), "steps", 10200, "steps", "apple_health", ""); err != nil {
		t.Fatalf("LogHealth: %v", err)
	}

	rows, err := db.Query(dbPath, "SELECT metric, value, unit, source FROM health_data ORDER BY value ASC")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
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
	err := svc.LogHealth(newUUID(), "", 100, "", "", "")
	if err == nil {
		t.Fatal("expected error for empty metric")
	}
}

func TestGetHealthSummary(t *testing.T) {
	_, svc := setupHabitsTestDB(t)

	// Log several data points.
	values := []float64{7.5, 8.0, 6.5, 7.0, 8.5}
	for _, v := range values {
		if err := svc.LogHealth(newUUID(), "sleep_hours", v, "hours", "manual", ""); err != nil {
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

	id := newUUID()
	err := svc.CreateHabit(id, "Meditate", "", "daily", "", "", 1)
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

	id := newUUID()
	err := svc.CreateHabit(id, "Read", "", "daily", "", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	// Log today -- no alert expected.
	if err := svc.LogHabit(newUUID(), id, "", "", 1.0); err != nil {
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
	ctx := withApp(context.Background(), &App{Habits: svc})

	input := json.RawMessage(`{"name":"Push-ups","frequency":"daily","targetCount":3,"category":"fitness"}`)
	result, err := toolHabitCreate(ctx, &Config{}, input)
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
	input := json.RawMessage(`{"name":"Test"}`)
	_, err := toolHabitCreate(context.Background(), &Config{}, input)
	if err == nil {
		t.Fatal("expected error when service not initialized")
	}
}

func TestToolHabitLog(t *testing.T) {
	_, svc := setupHabitsTestDB(t)
	ctx := withApp(context.Background(), &App{Habits: svc})

	// Create a habit first.
	id := newUUID()
	err := svc.CreateHabit(id, "Water", "Drink 8 glasses", "daily", "health", "", 1)
	if err != nil {
		t.Fatalf("CreateHabit: %v", err)
	}

	input := json.RawMessage(`{"habitId":"` + id + `","note":"glass 1","value":1}`)
	result, err := toolHabitLog(ctx, &Config{}, input)
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
	ctx := withApp(context.Background(), &App{Habits: svc})

	svc.CreateHabit(newUUID(), "A", "", "daily", "", "", 1)
	svc.CreateHabit(newUUID(), "B", "", "daily", "", "", 1)

	input := json.RawMessage(`{}`)
	result, err := toolHabitStatus(ctx, &Config{}, input)
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

// --- from price_watch_test.go ---

func setupPriceWatchTestDB(t *testing.T) (string, *PriceWatchEngine) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initFinanceDB(dbPath); err != nil {
		t.Fatalf("initFinanceDB: %v", err)
	}
	cfg := &Config{
		HistoryDB: dbPath,
		Finance: FinanceConfig{
			Enabled:         true,
			DefaultCurrency: "TWD",
		},
	}
	engine := newPriceWatchEngine(cfg)
	return dbPath, engine
}

func TestAddWatch(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	err := engine.AddWatch("user1", "USD", "JPY", "gt", 150.0, "telegram")
	if err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	watches, err := engine.ListWatches("user1")
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("expected 1 watch, got %d", len(watches))
	}
	if watches[0].FromCurrency != "USD" || watches[0].ToCurrency != "JPY" {
		t.Errorf("currencies: got %s/%s", watches[0].FromCurrency, watches[0].ToCurrency)
	}
	if watches[0].Condition != "gt" {
		t.Errorf("condition: got %s, want gt", watches[0].Condition)
	}
	if watches[0].Threshold != 150.0 {
		t.Errorf("threshold: got %f, want 150", watches[0].Threshold)
	}
	if watches[0].Status != "active" {
		t.Errorf("status: got %s, want active", watches[0].Status)
	}
}

func TestAddWatch_Validation(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	// Missing currencies.
	if err := engine.AddWatch("user1", "", "JPY", "gt", 150, ""); err == nil {
		t.Error("expected error for empty from currency")
	}
	if err := engine.AddWatch("user1", "USD", "", "gt", 150, ""); err == nil {
		t.Error("expected error for empty to currency")
	}

	// Invalid condition.
	if err := engine.AddWatch("user1", "USD", "JPY", "eq", 150, ""); err == nil {
		t.Error("expected error for invalid condition")
	}

	// Invalid threshold.
	if err := engine.AddWatch("user1", "USD", "JPY", "gt", 0, ""); err == nil {
		t.Error("expected error for zero threshold")
	}
	if err := engine.AddWatch("user1", "USD", "JPY", "gt", -10, ""); err == nil {
		t.Error("expected error for negative threshold")
	}
}

func TestListWatches(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	engine.AddWatch("user1", "USD", "JPY", "gt", 150, "")
	engine.AddWatch("user1", "EUR", "USD", "lt", 1.0, "")
	engine.AddWatch("user2", "GBP", "USD", "gt", 1.3, "")

	watches, err := engine.ListWatches("user1")
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 2 {
		t.Errorf("expected 2 watches for user1, got %d", len(watches))
	}

	watches2, err := engine.ListWatches("user2")
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches2) != 1 {
		t.Errorf("expected 1 watch for user2, got %d", len(watches2))
	}
}

func TestListWatches_Empty(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	watches, err := engine.ListWatches("nobody")
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 0 {
		t.Errorf("expected 0 watches, got %d", len(watches))
	}
}

func TestCancelWatch(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	engine.AddWatch("user1", "USD", "JPY", "gt", 150, "")

	watches, _ := engine.ListWatches("user1")
	if len(watches) != 1 {
		t.Fatalf("expected 1 watch, got %d", len(watches))
	}

	err := engine.CancelWatch(watches[0].ID)
	if err != nil {
		t.Fatalf("CancelWatch: %v", err)
	}

	// Verify it's cancelled.
	watches, _ = engine.ListWatches("user1")
	if len(watches) != 1 {
		t.Fatalf("expected 1 watch (cancelled), got %d", len(watches))
	}
	if watches[0].Status != "cancelled" {
		t.Errorf("status: got %s, want cancelled", watches[0].Status)
	}
}

func TestCheckWatches_Triggered(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	// Mock the Frankfurter API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"rates": map[string]float64{"JPY": 155.0},
		})
	}))
	defer srv.Close()

	engine.SetBaseURL(srv.URL)

	// Add a watch: alert when USD/JPY > 150.
	engine.AddWatch("user1", "USD", "JPY", "gt", 150.0, "")

	triggered, err := engine.CheckWatches(context.Background())
	if err != nil {
		t.Fatalf("CheckWatches: %v", err)
	}
	if len(triggered) != 1 {
		t.Fatalf("expected 1 triggered, got %d", len(triggered))
	}
	if triggered[0].Status != "triggered" {
		t.Errorf("status: got %s, want triggered", triggered[0].Status)
	}

	// After trigger, check again — should not trigger (status = triggered).
	triggered2, err := engine.CheckWatches(context.Background())
	if err != nil {
		t.Fatalf("CheckWatches 2nd: %v", err)
	}
	if len(triggered2) != 0 {
		t.Errorf("expected 0 triggered on 2nd check, got %d", len(triggered2))
	}
}

func TestCheckWatches_NotTriggered(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"rates": map[string]float64{"JPY": 145.0},
		})
	}))
	defer srv.Close()

	engine.SetBaseURL(srv.URL)

	// Watch: alert when USD/JPY > 150. Rate is 145, should NOT trigger.
	engine.AddWatch("user1", "USD", "JPY", "gt", 150.0, "")

	triggered, err := engine.CheckWatches(context.Background())
	if err != nil {
		t.Fatalf("CheckWatches: %v", err)
	}
	if len(triggered) != 0 {
		t.Errorf("expected 0 triggered, got %d", len(triggered))
	}
}

func TestCheckWatches_LessThan(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"rates": map[string]float64{"USD": 0.95},
		})
	}))
	defer srv.Close()

	engine.SetBaseURL(srv.URL)

	// Watch: alert when EUR/USD < 1.0.
	engine.AddWatch("user1", "EUR", "USD", "lt", 1.0, "")

	triggered, err := engine.CheckWatches(context.Background())
	if err != nil {
		t.Fatalf("CheckWatches: %v", err)
	}
	if len(triggered) != 1 {
		t.Fatalf("expected 1 triggered, got %d", len(triggered))
	}
}

func TestToolPriceWatch_Add(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"action":    "add",
		"from":      "USD",
		"to":        "JPY",
		"condition": "gt",
		"threshold": 155.0,
		"userId":    "tester",
	})

	result, err := toolPriceWatch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolPriceWatch add: %v", err)
	}
	if !strings.Contains(result, "watch added") {
		t.Errorf("expected 'watch added' in result, got: %s", result)
	}
}

func TestToolPriceWatch_List(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	cfg := &Config{HistoryDB: svc.DBPath()}

	// Add a watch first.
	engine := newPriceWatchEngine(cfg)
	engine.AddWatch("tester", "USD", "JPY", "gt", 155, "")

	input, _ := json.Marshal(map[string]any{
		"action": "list",
		"userId": "tester",
	})

	result, err := toolPriceWatch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolPriceWatch list: %v", err)
	}
	if !strings.Contains(result, "USD") || !strings.Contains(result, "JPY") {
		t.Errorf("expected USD/JPY in list result, got: %s", result)
	}
}

func TestToolPriceWatch_Cancel(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	cfg := &Config{HistoryDB: svc.DBPath()}
	engine := newPriceWatchEngine(cfg)
	engine.AddWatch("tester", "USD", "JPY", "gt", 155, "")

	watches, _ := engine.ListWatches("tester")
	if len(watches) == 0 {
		t.Fatal("expected at least 1 watch")
	}

	input, _ := json.Marshal(map[string]any{
		"action": "cancel",
		"id":     watches[0].ID,
	})

	result, err := toolPriceWatch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolPriceWatch cancel: %v", err)
	}
	if !strings.Contains(result, "cancelled") {
		t.Errorf("expected 'cancelled' in result, got: %s", result)
	}
}

func TestToolPriceWatch_InvalidAction(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	ctx := withApp(context.Background(), &App{Finance: svc})

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{
		"action": "invalid",
	})

	_, err := toolPriceWatch(ctx, cfg, input)
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestToolPriceWatch_NotInitialized(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"action": "list"})
	_, err := toolPriceWatch(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when service not initialized")
	}
}

// --- from reminder_test.go ---

// --- P19.3: Smart Reminders Tests ---

// testReminderDB creates a temporary SQLite DB for testing.
func testReminderDB(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "reminder_test_*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })

	if err := initReminderDB(path); err != nil {
		t.Fatalf("init reminder db: %v", err)
	}
	return path
}

func testExecSQL(t *testing.T, dbPath, sql string) {
	t.Helper()
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("exec sql: %s: %v", string(out), err)
	}
}

// --- parseNaturalTime Tests ---

func TestParseNaturalTime_Japanese(t *testing.T) {
	// "5分後"
	t.Run("5min_later", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("5分後")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(5 * time.Minute)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})

	// "1時間後"
	t.Run("1hour_later", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("1時間後")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(1 * time.Hour)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "30秒後"
	t.Run("30sec_later", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("30秒後")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(30 * time.Second)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "明日"
	t.Run("tomorrow", func(t *testing.T) {
		got, err := parseNaturalTime("明日")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		if got.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d", tomorrow.Day(), got.Day())
		}
	})

	// "明日3時"
	t.Run("tomorrow_3am", func(t *testing.T) {
		got, err := parseNaturalTime("明日3時")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		// Compare in local timezone since the time was created in local tz.
		local := got.In(time.Now().Location())
		if local.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d (local)", tomorrow.Day(), local.Day())
		}
		if local.Hour() != 3 {
			t.Errorf("expected hour 3, got %d", local.Hour())
		}
	})

	// "来週月曜"
	t.Run("next_monday", func(t *testing.T) {
		got, err := parseNaturalTime("来週月曜")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		local := got.In(time.Now().Location())
		if local.Weekday() != time.Monday {
			t.Errorf("expected Monday, got %v", local.Weekday())
		}
		if !got.After(time.Now().UTC()) {
			t.Errorf("expected future time, got %v", got)
		}
	})
}

func TestParseNaturalTime_English(t *testing.T) {
	// "in 5 min"
	t.Run("in_5_min", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("in 5 min")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(5 * time.Minute)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "in 1 hour"
	t.Run("in_1_hour", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("in 1 hour")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(1 * time.Hour)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "tomorrow"
	t.Run("tomorrow", func(t *testing.T) {
		got, err := parseNaturalTime("tomorrow")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		if got.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d", tomorrow.Day(), got.Day())
		}
	})

	// "tomorrow 3pm"
	t.Run("tomorrow_3pm", func(t *testing.T) {
		got, err := parseNaturalTime("tomorrow 3pm")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		if got.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d", tomorrow.Day(), got.Day())
		}
		local := got.In(time.Now().Location())
		if local.Hour() != 15 {
			t.Errorf("expected hour 15, got %d", local.Hour())
		}
	})

	// "next monday"
	t.Run("next_monday", func(t *testing.T) {
		got, err := parseNaturalTime("next monday")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		local := got.In(time.Now().Location())
		if local.Weekday() != time.Monday {
			t.Errorf("expected Monday, got %v", local.Weekday())
		}
	})

	// "in 30 seconds"
	t.Run("in_30_seconds", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("in 30 seconds")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(30 * time.Second)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})
}

func TestParseNaturalTime_Chinese(t *testing.T) {
	// "5分鐘後"
	t.Run("5min_later", func(t *testing.T) {
		before := time.Now().UTC()
		got, err := parseNaturalTime("5分鐘後")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := before.Add(5 * time.Minute)
		diff := got.Sub(expected)
		if diff < -2*time.Second || diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v", expected, got)
		}
	})

	// "明天下午3點"
	t.Run("tomorrow_3pm", func(t *testing.T) {
		got, err := parseNaturalTime("明天下午3點")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tomorrow := time.Now().AddDate(0, 0, 1)
		if got.Day() != tomorrow.Day() {
			t.Errorf("expected day %d, got day %d", tomorrow.Day(), got.Day())
		}
		local := got.In(time.Now().Location())
		if local.Hour() != 15 {
			t.Errorf("expected hour 15, got %d", local.Hour())
		}
	})

	// "下週一"
	t.Run("next_monday", func(t *testing.T) {
		got, err := parseNaturalTime("下週一")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		local := got.In(time.Now().Location())
		if local.Weekday() != time.Monday {
			t.Errorf("expected Monday, got %v", local.Weekday())
		}
	})
}

func TestParseNaturalTime_Absolute(t *testing.T) {
	// ISO format
	t.Run("iso", func(t *testing.T) {
		got, err := parseNaturalTime("2025-06-15 14:00")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Hour() != 14 || got.Minute() != 0 {
			t.Errorf("expected 14:00, got %02d:%02d", got.Hour(), got.Minute())
		}
	})

	// Time only: "15:30"
	t.Run("time_only", func(t *testing.T) {
		got, err := parseNaturalTime("15:30")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		local := got.In(time.Now().Location())
		if local.Hour() != 15 || local.Minute() != 30 {
			t.Errorf("expected 15:30, got %02d:%02d", local.Hour(), local.Minute())
		}
	})
}

func TestParseNaturalTime_Error(t *testing.T) {
	_, err := parseNaturalTime("")
	if err == nil {
		t.Error("expected error for empty input")
	}

	_, err = parseNaturalTime("garbage text that is not a time")
	if err == nil {
		t.Error("expected error for garbage input")
	}
}

// --- DB Operation Tests ---

func TestReminderAddAndList(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	re := newReminderEngine(cfg, nil)

	// Add a reminder.
	due := time.Now().Add(1 * time.Hour)
	rem, err := re.Add("Test reminder", due, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}
	if rem.ID == "" {
		t.Error("expected non-empty ID")
	}
	if rem.Text != "Test reminder" {
		t.Errorf("expected text 'Test reminder', got %q", rem.Text)
	}
	if rem.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", rem.Status)
	}

	// List reminders.
	list, err := re.List("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 reminder, got %d", len(list))
	}
	if list[0].Text != "Test reminder" {
		t.Errorf("expected text 'Test reminder', got %q", list[0].Text)
	}

	// List with different user should be empty.
	list2, err := re.List("user2")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list2) != 0 {
		t.Errorf("expected 0 reminders for user2, got %d", len(list2))
	}
}

func TestReminderCancel(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	re := newReminderEngine(cfg, nil)

	due := time.Now().Add(1 * time.Hour)
	rem, err := re.Add("Cancel me", due, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Cancel it.
	if err := re.Cancel(rem.ID, "user1"); err != nil {
		t.Fatalf("cancelReminder: %v", err)
	}

	// Should no longer appear in list.
	list, err := re.List("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 reminders after cancel, got %d", len(list))
	}
}

func TestReminderSnooze(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	re := newReminderEngine(cfg, nil)

	// Create a reminder due 5 minutes from now.
	due := time.Now().Add(5 * time.Minute)
	rem, err := re.Add("Snooze me", due, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Snooze by 1 hour.
	if err := re.Snooze(rem.ID, 1*time.Hour); err != nil {
		t.Fatalf("snoozeReminder: %v", err)
	}

	// Verify the due_at moved forward.
	list, err := re.List("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 reminder, got %d", len(list))
	}

	newDue, err := time.Parse(time.RFC3339, list[0].DueAt)
	if err != nil {
		t.Fatalf("parse due_at: %v", err)
	}
	expectedMin := due.Add(55 * time.Minute) // at least 55 minutes later
	if newDue.Before(expectedMin) {
		t.Errorf("expected due_at after %v, got %v", expectedMin, newDue)
	}
}

func TestReminderTick(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	var notifications []string
	notifyFn := func(text string) {
		notifications = append(notifications, text)
	}

	re := newReminderEngine(cfg, notifyFn)

	// Insert a reminder that is already due (in the past).
	pastDue := time.Now().Add(-1 * time.Minute)
	_, err := re.Add("Past due reminder", pastDue, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Insert a reminder that is not yet due.
	futureDue := time.Now().Add(1 * time.Hour)
	_, err = re.Add("Future reminder", futureDue, "", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Run tick.
	re.Tick()

	// Should have fired the past-due reminder.
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d: %v", len(notifications), notifications)
	}
	if !strings.Contains(notifications[0], "Past due reminder") {
		t.Errorf("expected notification about 'Past due reminder', got %q", notifications[0])
	}

	// The past-due reminder should now be 'fired'.
	list, err := re.List("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 pending reminder (the future one), got %d", len(list))
	}
}

func TestReminderRecurring(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true},
	}

	var notifications []string
	notifyFn := func(text string) {
		notifications = append(notifications, text)
	}

	re := newReminderEngine(cfg, notifyFn)

	// Insert a recurring reminder that is already due.
	pastDue := time.Now().Add(-1 * time.Minute)
	rem, err := re.Add("Daily standup", pastDue, "0 9 * * *", "api", "user1")
	if err != nil {
		t.Fatalf("addReminder: %v", err)
	}

	// Run tick — should fire and reschedule.
	re.Tick()

	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}

	// The reminder should still be pending (rescheduled, not fired).
	list, err := re.List("user1")
	if err != nil {
		t.Fatalf("listReminders: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 reminder (rescheduled), got %d", len(list))
	}

	// The due_at should be in the future.
	newDue, err := time.Parse(time.RFC3339, list[0].DueAt)
	if err != nil {
		t.Fatalf("parse due_at: %v", err)
	}
	if !newDue.After(time.Now().UTC()) {
		t.Errorf("expected rescheduled due_at in future, got %v", newDue)
	}

	_ = rem // suppress unused
}

func TestReminderMaxPerUser(t *testing.T) {
	dbPath := testReminderDB(t)
	cfg := &Config{
		HistoryDB: dbPath,
		Reminders: ReminderConfig{Enabled: true, MaxPerUser: 3},
	}

	re := newReminderEngine(cfg, nil)

	due := time.Now().Add(1 * time.Hour)
	for i := 0; i < 3; i++ {
		_, err := re.Add(fmt.Sprintf("Reminder %d", i), due, "", "api", "user1")
		if err != nil {
			t.Fatalf("addReminder %d: %v", i, err)
		}
	}

	// 4th should fail.
	_, err := re.Add("Too many", due, "", "api", "user1")
	if err == nil {
		t.Error("expected error when exceeding max per user")
	}
	if !strings.Contains(err.Error(), "maximum") {
		t.Errorf("expected max limit error, got: %v", err)
	}
}

func TestNextCronTime(t *testing.T) {
	// Every day at 9:00.
	now := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	next := nextCronTime("0 9 * * *", now)
	if next.IsZero() {
		t.Fatal("expected non-zero next time")
	}
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Errorf("expected 09:00, got %02d:%02d", next.Hour(), next.Minute())
	}
	if !next.After(now) {
		t.Errorf("expected next > now, got %v", next)
	}
}

func TestInitReminderDB(t *testing.T) {
	dbPath := testReminderDB(t)

	// Table should exist. Try inserting.
	sql := fmt.Sprintf(
		`INSERT INTO reminders (id, text, due_at, status, created_at)
		 VALUES ('test1', 'hello', '2025-01-01T00:00:00Z', 'pending', '2025-01-01T00:00:00Z')`)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("insert into reminders: %s: %v", string(out), err)
	}

	// Verify it exists.
	rows, err := db.Query(dbPath, "SELECT * FROM reminders WHERE id = 'test1'")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

// --- from task_sync_test.go ---

// --- Todoist Sync Tests ---

func TestNewTodoistSync(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ts := newTodoistSync(cfg)
	if ts == nil {
		t.Fatal("expected non-nil TodoistSync")
	}
}

func TestTodoistPriorityConversion(t *testing.T) {
	tests := []struct {
		todoist int
		local   int
	}{
		{4, 1}, // urgent
		{3, 2}, // high
		{2, 3}, // medium
		{1, 4}, // normal
	}
	for _, tt := range tests {
		got := tasks.TodoistPriorityToLocal(tt.todoist)
		if got != tt.local {
			t.Errorf("TodoistPriorityToLocal(%d) = %d, want %d", tt.todoist, got, tt.local)
		}
		back := tasks.LocalPriorityToTodoist(tt.local)
		if back != tt.todoist {
			t.Errorf("LocalPriorityToTodoist(%d) = %d, want %d", tt.local, back, tt.todoist)
		}
	}
}

func TestTodoistPullTasks_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ts := newTodoistSync(cfg)
	_, err := ts.PullTasks("user1")
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestTodoistPullTasks_MockServer(t *testing.T) {
	// Set up test DB.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	// Mock Todoist API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode([]TodoistTask{
			{
				ID:      "td-1",
				Content: "Test Todoist Task",
				Priority: 4,
				Due: &struct {
					Date string `json:"date"`
				}{Date: "2026-03-01"},
			},
			{
				ID:          "td-2",
				Content:     "Completed task",
				IsCompleted: true,
			},
		})
	}))
	defer srv.Close()

	origBase := tasks.TodoistAPIBase
	tasks.TodoistAPIBase = srv.URL
	defer func() { tasks.TodoistAPIBase = origBase }()

	cfg := &Config{
		HistoryDB: dbPath,
		TaskManager: TaskManagerConfig{
			Enabled: true,
			Todoist: TodoistConfig{
				Enabled: true,
				APIKey:  "test-key",
			},
		},
	}

	oldMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(cfg)
	defer func() { globalTaskManager = oldMgr }()

	ts := newTodoistSync(cfg)
	pulled, err := ts.PullTasks("user1")
	if err != nil {
		t.Fatalf("PullTasks: %v", err)
	}
	if pulled != 2 {
		t.Errorf("expected 2 pulled, got %d", pulled)
	}

	// Verify tasks were created.
	taskList, _ := globalTaskManager.ListTasks("user1", TaskFilter{})
	if len(taskList) != 2 {
		t.Errorf("expected 2 tasks in DB, got %d", len(taskList))
	}
}

func TestTodoistPushTask_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ts := newTodoistSync(cfg)
	err := ts.PushTask(UserTask{Title: "test"})
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestTodoistSyncAll_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ts := newTodoistSync(cfg)
	_, _, err := ts.SyncAll("user1")
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestToolTodoistSync_NotEnabled(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"action": "sync"})
	_, err := toolTodoistSync(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when not enabled")
	}
}

func TestToolTodoistSync_UnknownAction(t *testing.T) {
	cfg := &Config{
		TaskManager: TaskManagerConfig{
			Todoist: TodoistConfig{Enabled: true, APIKey: "key"},
		},
	}
	input, _ := json.Marshal(map[string]string{"action": "invalid"})
	_, err := toolTodoistSync(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// --- Notion Sync Tests ---

func TestNewNotionSync(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ns := newNotionSync(cfg)
	if ns == nil {
		t.Fatal("expected non-nil NotionSync")
	}
}

func TestNotionPullTasks_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ns := newNotionSync(cfg)
	_, err := ns.PullTasks("user1")
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestNotionPullTasks_NoDatabaseID(t *testing.T) {
	cfg := &Config{
		HistoryDB: "/tmp/test.db",
		TaskManager: TaskManagerConfig{
			Notion: NotionConfig{APIKey: "key"},
		},
	}
	ns := newNotionSync(cfg)
	_, err := ns.PullTasks("user1")
	if err == nil {
		t.Fatal("expected error when database ID is missing")
	}
}

func TestNotionPullTasks_MockServer(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer notion-key" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{
					"id": "notion-page-1",
					"properties": map[string]any{
						"Name": map[string]any{
							"title": []map[string]any{
								{"plain_text": "Notion Task 1"},
							},
						},
						"Status": map[string]any{
							"select": map[string]any{"name": "To Do"},
						},
						"Priority": map[string]any{
							"select": map[string]any{"name": "High"},
						},
						"Due Date": map[string]any{
							"date": map[string]any{"start": "2026-04-01"},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	origBase := tasks.NotionAPIBase
	tasks.NotionAPIBase = srv.URL
	defer func() { tasks.NotionAPIBase = origBase }()

	cfg := &Config{
		HistoryDB: dbPath,
		TaskManager: TaskManagerConfig{
			Enabled: true,
			Notion: NotionConfig{
				Enabled:    true,
				APIKey:     "notion-key",
				DatabaseID: "db-123",
			},
		},
	}

	oldMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(cfg)
	defer func() { globalTaskManager = oldMgr }()

	ns := newNotionSync(cfg)
	pulled, err := ns.PullTasks("user1")
	if err != nil {
		t.Fatalf("PullTasks: %v", err)
	}
	if pulled != 1 {
		t.Errorf("expected 1 pulled, got %d", pulled)
	}
}

func TestNotionPushTask_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ns := newNotionSync(cfg)
	err := ns.PushTask(UserTask{Title: "test"})
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestNotionSyncAll_NoAPIKey(t *testing.T) {
	cfg := &Config{HistoryDB: "/tmp/test.db"}
	ns := newNotionSync(cfg)
	_, _, err := ns.SyncAll("user1")
	if err == nil {
		t.Fatal("expected error when API key is missing")
	}
}

func TestToolNotionSync_NotEnabled(t *testing.T) {
	cfg := &Config{}
	input, _ := json.Marshal(map[string]string{"action": "sync"})
	_, err := toolNotionSync(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when not enabled")
	}
}

func TestToolNotionSync_UnknownAction(t *testing.T) {
	cfg := &Config{
		TaskManager: TaskManagerConfig{
			Notion: NotionConfig{Enabled: true, APIKey: "key", DatabaseID: "db"},
		},
	}
	input, _ := json.Marshal(map[string]string{"action": "invalid"})
	_, err := toolNotionSync(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// --- Notion Status/Priority Mapping Tests ---

func TestNotionStatusMapping(t *testing.T) {
	tests := []struct {
		local  string
		notion string
	}{
		{"todo", "To Do"},
		{"in_progress", "In Progress"},
		{"done", "Done"},
		{"cancelled", "Cancelled"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := tasks.LocalStatusToNotion(tt.local)
		if got != tt.notion {
			t.Errorf("LocalStatusToNotion(%q) = %q, want %q", tt.local, got, tt.notion)
		}
	}
}

func TestNotionPriorityMapping(t *testing.T) {
	tests := []struct {
		local  int
		notion string
	}{
		{1, "Urgent"},
		{2, "High"},
		{3, "Medium"},
		{4, "Low"},
		{0, ""},
	}
	for _, tt := range tests {
		got := tasks.LocalPriorityToNotion(tt.local)
		if got != tt.notion {
			t.Errorf("LocalPriorityToNotion(%d) = %q, want %q", tt.local, got, tt.notion)
		}
	}
}

// --- findTaskByExternalID Tests ---

func TestFindTaskByExternalID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	f, _ := os.Create(dbPath)
	f.Close()
	initTaskManagerDB(dbPath)

	cfg := &Config{HistoryDB: dbPath}
	oldMgr := globalTaskManager
	globalTaskManager = newTaskManagerService(cfg)
	defer func() { globalTaskManager = oldMgr }()

	// Create a task with external ID.
	globalTaskManager.CreateTask(UserTask{
		UserID:         "u1",
		Title:          "Synced task",
		ExternalID:     "ext-123",
		ExternalSource: "todoist",
	})

	// Find it.
	found, err := findTaskByExternalID(dbPath, "todoist", "ext-123")
	if err != nil {
		t.Fatalf("findTaskByExternalID: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find task")
	}
	if found.Title != "Synced task" {
		t.Errorf("expected title 'Synced task', got %q", found.Title)
	}

	// Not found.
	notFound, _ := findTaskByExternalID(dbPath, "todoist", "nonexistent")
	if notFound != nil {
		t.Error("expected nil for nonexistent external ID")
	}
}

// --- from timetracking_test.go ---

func TestTimeTracking_InitDB(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")

	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	// Verify table exists.
	rows, err := db.Query(dbPath, "SELECT name FROM sqlite_master WHERE type='table' AND name='time_entries';")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("time_entries table not created")
	}
}

func TestTimeTracking_StartStop(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	// Start timer.
	entry, err := svc.StartTimer("testuser", "myproject", "coding", []string{"go"}, newUUID)
	if err != nil {
		t.Fatalf("StartTimer: %v", err)
	}
	if entry.Project != "myproject" {
		t.Errorf("project = %q, want 'myproject'", entry.Project)
	}
	if entry.Activity != "coding" {
		t.Errorf("activity = %q, want 'coding'", entry.Activity)
	}

	// Check running.
	running, err := svc.GetRunning("testuser")
	if err != nil {
		t.Fatalf("GetRunning: %v", err)
	}
	if running == nil {
		t.Fatal("expected running timer, got nil")
	}
	if running.ID != entry.ID {
		t.Errorf("running id = %q, want %q", running.ID, entry.ID)
	}

	// Stop timer.
	stopped, err := svc.StopTimer("testuser")
	if err != nil {
		t.Fatalf("StopTimer: %v", err)
	}
	if stopped.EndTime == "" {
		t.Error("expected end_time to be set")
	}
	if stopped.DurationMinutes < 1 {
		t.Errorf("expected duration >= 1, got %d", stopped.DurationMinutes)
	}

	// No running timer after stop.
	running, err = svc.GetRunning("testuser")
	if err != nil {
		t.Fatalf("GetRunning: %v", err)
	}
	if running != nil {
		t.Error("expected no running timer after stop")
	}
}

func TestTimeTracking_AutoStop(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	// Start first timer.
	first, err := svc.StartTimer("testuser", "proj1", "task1", nil, newUUID)
	if err != nil {
		t.Fatalf("StartTimer first: %v", err)
	}

	// Start second timer (should auto-stop first).
	second, err := svc.StartTimer("testuser", "proj2", "task2", nil, newUUID)
	if err != nil {
		t.Fatalf("StartTimer second: %v", err)
	}

	// Verify first is stopped.
	rows, _ := db.Query(dbPath, "SELECT end_time FROM time_entries WHERE id = '"+first.ID+"';")
	if len(rows) == 0 {
		t.Fatal("first entry not found")
	}
	if jsonStr(rows[0]["end_time"]) == "" {
		t.Error("first timer should be auto-stopped")
	}

	// Verify second is running.
	running, err := svc.GetRunning("testuser")
	if err != nil {
		t.Fatalf("GetRunning: %v", err)
	}
	if running == nil || running.ID != second.ID {
		t.Error("second timer should be running")
	}
}

func TestTimeTracking_ManualLog(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	entry, err := svc.LogEntry("testuser", "reading", "book", 60, "2025-01-15", "Read chapter 3", []string{"learn"}, newUUID)
	if err != nil {
		t.Fatalf("LogEntry: %v", err)
	}
	if entry.DurationMinutes != 60 {
		t.Errorf("duration = %d, want 60", entry.DurationMinutes)
	}
	if entry.Note != "Read chapter 3" {
		t.Errorf("note = %q, want 'Read chapter 3'", entry.Note)
	}
}

func TestTimeTracking_Report(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	if err := initTimeTrackingDB(dbPath); err != nil {
		t.Fatalf("initTimeTrackingDB: %v", err)
	}

	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	// Log some entries.
	svc.LogEntry("testuser", "proj1", "coding", 120, "", "session 1", nil, newUUID)
	svc.LogEntry("testuser", "proj1", "review", 30, "", "session 2", nil, newUUID)
	svc.LogEntry("testuser", "proj2", "design", 60, "", "session 3", nil, newUUID)

	report, err := svc.Report("testuser", "month", "")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	if report.EntryCount != 3 {
		t.Errorf("entry_count = %d, want 3", report.EntryCount)
	}
	if report.TotalHours < 3.4 || report.TotalHours > 3.6 {
		t.Errorf("total_hours = %.2f, want ~3.5", report.TotalHours)
	}
	if _, ok := report.ByProject["proj1"]; !ok {
		t.Error("expected proj1 in by_project")
	}
	if _, ok := report.ByProject["proj2"]; !ok {
		t.Error("expected proj2 in by_project")
	}
}

func TestTimeTracking_LogEntry_InvalidDuration(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	_ = initTimeTrackingDB(dbPath)
	cfg := &Config{HistoryDB: dbPath}
	svc := newTimeTrackingService(cfg)

	_, err := svc.LogEntry("testuser", "proj", "act", 0, "", "", nil, newUUID)
	if err == nil {
		t.Error("expected error for zero duration")
	}

	_, err = svc.LogEntry("testuser", "proj", "act", -5, "", "", nil, newUUID)
	if err == nil {
		t.Error("expected error for negative duration")
	}
}

// Ensure unused import doesn't break.
var _ = os.DevNull

// --- from tool_ingest_test.go ---

func TestParseSitemapURLs(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://example.com/page1</loc>
  </url>
  <url>
    <loc>https://example.com/page2</loc>
  </url>
  <url>
    <loc>https://example.com/about</loc>
  </url>
</urlset>`

	urls := parseSitemapURLs(xml)
	if len(urls) != 3 {
		t.Fatalf("expected 3 urls, got %d", len(urls))
	}
	expected := []string{
		"https://example.com/page1",
		"https://example.com/page2",
		"https://example.com/about",
	}
	for i, u := range urls {
		if u != expected[i] {
			t.Errorf("url[%d]: want %q, got %q", i, expected[i], u)
		}
	}
}

func TestParseSitemapURLsEmpty(t *testing.T) {
	urls := parseSitemapURLs("")
	if len(urls) != 0 {
		t.Errorf("expected 0 urls from empty content, got %d", len(urls))
	}
}

func TestParseSitemapIndex(t *testing.T) {
	// Create a child sitemap server.
	childXML := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/child1</loc></url>
  <url><loc>https://example.com/child2</loc></url>
</urlset>`

	childSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, childXML)
	}))
	defer childSrv.Close()

	indexXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap>
    <loc>%s/sitemap1.xml</loc>
  </sitemap>
</sitemapindex>`, childSrv.URL)

	// Create index server.
	indexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, indexXML)
	}))
	defer indexSrv.Close()

	ctx := context.Background()
	urls, err := fetchSitemapURLs(ctx, indexSrv.URL)
	if err != nil {
		t.Fatalf("fetchSitemapURLs: %v", err)
	}
	if len(urls) != 2 {
		t.Fatalf("expected 2 urls from sitemapindex, got %d", len(urls))
	}
	if urls[0] != "https://example.com/child1" {
		t.Errorf("url[0]: want https://example.com/child1, got %s", urls[0])
	}
}

func TestFilterURLs(t *testing.T) {
	urls := []string{
		"https://example.com/docs/api",
		"https://example.com/docs/guide",
		"https://example.com/blog/post1",
		"https://example.com/about",
	}

	t.Run("no_filters", func(t *testing.T) {
		result := filterURLs(urls, nil, nil)
		if len(result) != 4 {
			t.Errorf("expected 4, got %d", len(result))
		}
	})

	t.Run("include_only", func(t *testing.T) {
		result := filterURLs(urls, []string{"example.com/docs/*"}, nil)
		if len(result) != 2 {
			t.Errorf("expected 2 docs urls, got %d: %v", len(result), result)
		}
	})

	t.Run("exclude_only", func(t *testing.T) {
		result := filterURLs(urls, nil, []string{"example.com/blog/*"})
		if len(result) != 3 {
			t.Errorf("expected 3 non-blog urls, got %d: %v", len(result), result)
		}
	})

	t.Run("include_and_exclude", func(t *testing.T) {
		result := filterURLs(urls, []string{"example.com/docs/*"}, []string{"example.com/docs/api"})
		if len(result) != 1 {
			t.Errorf("expected 1 (docs minus api), got %d: %v", len(result), result)
		}
		if len(result) > 0 && !strings.Contains(result[0], "guide") {
			t.Errorf("expected guide url, got %s", result[0])
		}
	})
}

func TestURLToSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com/page", "example-com_page"},
		{"https://example.com/docs/api/v2", "example-com_docs_api_v2"},
		{"https://example.com/", "example-com"},
		{"https://example.com/page?q=1#section", "example-com_page"},
		{"http://test.org/a/b/c/", "test-org_a_b_c"},
		{"", "page"},
	}

	for _, tt := range tests {
		got := urlToSlug(tt.input)
		if got != tt.want {
			t.Errorf("urlToSlug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestURLToSlugLongURL(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", 200)
	slug := urlToSlug(long)
	if len(slug) > 100 {
		t.Errorf("slug too long: %d chars", len(slug))
	}
}

func TestWebCrawlSingle(t *testing.T) {
	// Set up a local HTTP server serving an HTML page.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h1>Hello World</h1><p>Test content here.</p></body></html>")
	}))
	defer srv.Close()

	tmp := t.TempDir()
	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":  srv.URL + "/test-page",
		"mode": "single",
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebCrawl: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if int(result["total"].(float64)) != 1 {
		t.Errorf("total: want 1, got %v", result["total"])
	}
	if int(result["imported"].(float64)) != 1 {
		t.Errorf("imported: want 1, got %v", result["imported"])
	}

	// Verify note was created.
	slug := urlToSlug(srv.URL + "/test-page")
	notePath := filepath.Join(tmp, slug+".md")
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("note file not found: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "source:") {
		t.Errorf("note should contain source header")
	}
	if !strings.Contains(content, "Hello World") {
		t.Errorf("note should contain stripped text 'Hello World'")
	}
}

func TestWebCrawlSitemap(t *testing.T) {
	// Two page servers.
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><body><h1>Page: %s</h1></body></html>", r.URL.Path)
	}))
	defer pageSrv.Close()

	sitemapXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/page1</loc></url>
  <url><loc>%s/page2</loc></url>
</urlset>`, pageSrv.URL, pageSrv.URL)

	sitemapSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, sitemapXML)
	}))
	defer sitemapSrv.Close()

	tmp := t.TempDir()
	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":    sitemapSrv.URL + "/sitemap.xml",
		"prefix": "docs",
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebCrawl sitemap: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	if int(result["total"].(float64)) != 2 {
		t.Errorf("total: want 2, got %v", result["total"])
	}
	if int(result["imported"].(float64)) != 2 {
		t.Errorf("imported: want 2, got %v", result["imported"])
	}

	// Verify notes were created under prefix.
	docsDir := filepath.Join(tmp, "docs")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		t.Fatalf("docs dir not found: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 notes in docs/, got %d", len(entries))
	}
}

func TestWebCrawlDedup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>Same content every time</body></html>")
	}))
	defer srv.Close()

	tmp := t.TempDir()
	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	// First import.
	input, _ := json.Marshal(map[string]any{
		"url":   srv.URL + "/page",
		"mode":  "single",
		"dedup": true,
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("first crawl: %v", err)
	}

	var result1 map[string]any
	json.Unmarshal([]byte(out), &result1)
	if int(result1["imported"].(float64)) != 1 {
		t.Errorf("first crawl: expected 1 imported, got %v", result1["imported"])
	}

	// Second import with dedup - should skip.
	out2, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("second crawl: %v", err)
	}

	var result2 map[string]any
	json.Unmarshal([]byte(out2), &result2)
	if int(result2["skipped"].(float64)) != 1 {
		t.Errorf("second crawl: expected 1 skipped, got %v (result: %v)", result2["skipped"], result2)
	}
}

func TestWebCrawlEmptyPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body></body></html>")
	}))
	defer srv.Close()

	tmp := t.TempDir()
	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":  srv.URL + "/empty",
		"mode": "single",
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebCrawl: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if int(result["skipped"].(float64)) != 1 {
		t.Errorf("expected 1 skipped for empty page, got %v", result["skipped"])
	}
}

func TestWebCrawlMaxPages(t *testing.T) {
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><body>Page %s</body></html>", r.URL.Path)
	}))
	defer pageSrv.Close()

	// Sitemap with 5 URLs but max_pages=2.
	var locs strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&locs, "<url><loc>%s/p%d</loc></url>\n", pageSrv.URL, i)
	}
	sitemapXML := fmt.Sprintf(`<?xml version="1.0"?><urlset>%s</urlset>`, locs.String())

	sitemapSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, sitemapXML)
	}))
	defer sitemapSrv.Close()

	tmp := t.TempDir()
	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":       sitemapSrv.URL,
		"max_pages": 2,
	})
	out, err := toolWebCrawl(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolWebCrawl: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if int(result["total"].(float64)) != 2 {
		t.Errorf("expected total=2 with max_pages, got %v", result["total"])
	}
}

func TestSourceAuditURL(t *testing.T) {
	// Create a sitemap server.
	sitemapXML := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/page1</loc></url>
  <url><loc>https://example.com/page2</loc></url>
  <url><loc>https://example.com/page3</loc></url>
</urlset>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, sitemapXML)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	svc := notes.New(NotesConfig{Enabled: true, VaultPath: tmp, DefaultExt: ".md"}, tmp, false, nil, nil, nil, nil)
	setGlobalNotesService(svc)
	defer setGlobalNotesService(nil)

	// Pre-create notes for page1 and page2 (but not page3).
	slug1 := urlToSlug("https://example.com/page1")
	slug2 := urlToSlug("https://example.com/page2")
	os.WriteFile(filepath.Join(tmp, slug1+".md"), []byte("content1"), 0o644)
	os.WriteFile(filepath.Join(tmp, slug2+".md"), []byte("content2"), 0o644)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"sitemap_url": srv.URL + "/sitemap.xml",
	})
	out, err := toolSourceAuditURL(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolSourceAuditURL: %v", err)
	}

	var result map[string]any
	json.Unmarshal([]byte(out), &result)

	if int(result["total"].(float64)) != 3 {
		t.Errorf("total: want 3, got %v", result["total"])
	}
	if int(result["existing"].(float64)) != 2 {
		t.Errorf("existing: want 2, got %v", result["existing"])
	}
	if int(result["missing_count"].(float64)) != 1 {
		t.Errorf("missing_count: want 1, got %v", result["missing_count"])
	}
}

func TestWebCrawlNoNotesService(t *testing.T) {
	setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"url":  "https://example.com/sitemap.xml",
		"mode": "single",
	})
	_, err := toolWebCrawl(ctx, cfg, input)
	if err == nil {
		t.Fatal("expected error when notes service is nil")
	}
	if !strings.Contains(err.Error(), "notes service not enabled") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSourceAuditURLNoNotesService(t *testing.T) {
	setGlobalNotesService(nil)

	ctx := context.Background()
	cfg := &Config{}

	input, _ := json.Marshal(map[string]any{
		"sitemap_url": "https://example.com/sitemap.xml",
	})
	_, err := toolSourceAuditURL(ctx, cfg, input)
	if err == nil {
		t.Fatal("expected error when notes service is nil")
	}
	if !strings.Contains(err.Error(), "notes service not enabled") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- from user_profile_test.go ---

// testProfileDB creates a temp DB file and initializes schema.
func testProfileDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_profile.db")
	if err := initUserProfileDB(dbPath); err != nil {
		t.Fatalf("initUserProfileDB: %v", err)
	}
	return dbPath
}

func testProfileService(t *testing.T, dbPath string) *UserProfileService {
	t.Helper()
	cfg := profile.Config{Enabled: true, SentimentEnabled: false}
	sentimentFn := func(text string) (float64, []string) {
		r := nlp.Analyze(text)
		return r.Score, r.Keywords
	}
	return profile.New(dbPath, cfg, makeLifeDB(), newUUID, sentimentFn, nlp.Label)
}

func TestInitUserProfileDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "init_test.db")
	err := initUserProfileDB(dbPath)
	if err != nil {
		t.Fatalf("initUserProfileDB failed: %v", err)
	}
	// DB file should exist.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file not created: %v", err)
	}

	// Idempotent: run again should not fail.
	err = initUserProfileDB(dbPath)
	if err != nil {
		t.Fatalf("initUserProfileDB second call failed: %v", err)
	}
}

func TestCreateProfile(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := testProfileService(t, dbPath)

	p := UserProfile{
		ID:                "user-001",
		DisplayName:       "Alice",
		PreferredLanguage: "en",
		Timezone:          "America/New_York",
	}
	err := svc.CreateProfile(p)
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// Duplicate insert should not fail (INSERT OR IGNORE).
	err = svc.CreateProfile(p)
	if err != nil {
		t.Fatalf("CreateProfile duplicate: %v", err)
	}
}

func TestGetProfile(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := testProfileService(t, dbPath)

	// Non-existent.
	p, err := svc.GetProfile("nonexistent")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil for nonexistent, got %+v", p)
	}

	// Create and retrieve.
	svc.CreateProfile(UserProfile{ID: "user-002", DisplayName: "Bob", PreferredLanguage: "ja"})
	p, err = svc.GetProfile("user-002")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p == nil {
		t.Fatal("expected profile, got nil")
	}
	if p.DisplayName != "Bob" {
		t.Errorf("DisplayName = %q, want 'Bob'", p.DisplayName)
	}
	if p.PreferredLanguage != "ja" {
		t.Errorf("PreferredLanguage = %q, want 'ja'", p.PreferredLanguage)
	}
}

func TestUpdateProfile(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := testProfileService(t, dbPath)

	svc.CreateProfile(UserProfile{ID: "user-003", DisplayName: "Charlie"})

	err := svc.UpdateProfile("user-003", map[string]string{
		"displayName":       "Charles",
		"preferredLanguage": "fr",
		"timezone":          "Europe/Paris",
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	p, _ := svc.GetProfile("user-003")
	if p == nil {
		t.Fatal("expected profile, got nil")
	}
	if p.DisplayName != "Charles" {
		t.Errorf("DisplayName = %q, want 'Charles'", p.DisplayName)
	}
	if p.PreferredLanguage != "fr" {
		t.Errorf("PreferredLanguage = %q, want 'fr'", p.PreferredLanguage)
	}
	if p.Timezone != "Europe/Paris" {
		t.Errorf("Timezone = %q, want 'Europe/Paris'", p.Timezone)
	}
}

func TestResolveUser(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := testProfileService(t, dbPath)

	// First resolution creates a new user.
	uid1, err := svc.ResolveUser("tg:12345")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if uid1 == "" {
		t.Fatal("expected non-empty userID")
	}

	// Second resolution returns same user.
	uid2, err := svc.ResolveUser("tg:12345")
	if err != nil {
		t.Fatalf("ResolveUser second call: %v", err)
	}
	if uid1 != uid2 {
		t.Errorf("ResolveUser returned different IDs: %q vs %q", uid1, uid2)
	}

	// Different channel key creates different user.
	uid3, err := svc.ResolveUser("discord:67890")
	if err != nil {
		t.Fatalf("ResolveUser different channel: %v", err)
	}
	if uid3 == uid1 {
		t.Error("different channel keys should create different users")
	}
}

func TestLinkChannel(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := testProfileService(t, dbPath)

	// Create a user.
	svc.CreateProfile(UserProfile{ID: "user-link-001", DisplayName: "LinkTest"})

	// Link a channel.
	err := svc.LinkChannel("user-link-001", "slack:abc", "LinkTest Slack")
	if err != nil {
		t.Fatalf("LinkChannel: %v", err)
	}

	// Resolve the channel should return same user.
	uid, err := svc.ResolveUser("slack:abc")
	if err != nil {
		t.Fatalf("ResolveUser after link: %v", err)
	}
	if uid != "user-link-001" {
		t.Errorf("ResolveUser = %q, want 'user-link-001'", uid)
	}

	// Link another channel to same user.
	err = svc.LinkChannel("user-link-001", "tg:99999", "LinkTest TG")
	if err != nil {
		t.Fatalf("LinkChannel second: %v", err)
	}
}

func TestGetChannelIdentities(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := testProfileService(t, dbPath)

	svc.CreateProfile(UserProfile{ID: "user-ci-001"})
	svc.LinkChannel("user-ci-001", "tg:111", "TG User")
	svc.LinkChannel("user-ci-001", "discord:222", "Discord User")
	svc.LinkChannel("user-ci-001", "slack:333", "Slack User")

	ids, err := svc.GetChannelIdentities("user-ci-001")
	if err != nil {
		t.Fatalf("GetChannelIdentities: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 identities, got %d", len(ids))
	}

	// Verify all channel keys present.
	keys := map[string]bool{}
	for _, id := range ids {
		keys[id.ChannelKey] = true
	}
	for _, k := range []string{"tg:111", "discord:222", "slack:333"} {
		if !keys[k] {
			t.Errorf("missing channel key %q", k)
		}
	}
}

func TestObservePreference(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := testProfileService(t, dbPath)

	svc.CreateProfile(UserProfile{ID: "user-pref-001"})

	// First observation.
	err := svc.ObservePreference("user-pref-001", "food", "favorite_cuisine", "japanese")
	if err != nil {
		t.Fatalf("ObservePreference: %v", err)
	}

	prefs, err := svc.GetPreferences("user-pref-001", "food")
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	if len(prefs) != 1 {
		t.Fatalf("expected 1 preference, got %d", len(prefs))
	}
	if prefs[0].Key != "favorite_cuisine" {
		t.Errorf("Key = %q, want 'favorite_cuisine'", prefs[0].Key)
	}
	if prefs[0].Value != "japanese" {
		t.Errorf("Value = %q, want 'japanese'", prefs[0].Value)
	}
	if prefs[0].ObservedCount != 1 {
		t.Errorf("ObservedCount = %d, want 1", prefs[0].ObservedCount)
	}
	if prefs[0].Confidence != 0.5 {
		t.Errorf("Confidence = %f, want 0.5", prefs[0].Confidence)
	}
}

func TestGetPreferences_ConfidenceGrows(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := testProfileService(t, dbPath)

	svc.CreateProfile(UserProfile{ID: "user-conf-001"})

	// Observe same preference multiple times.
	for i := 0; i < 5; i++ {
		err := svc.ObservePreference("user-conf-001", "schedule", "morning_person", "true")
		if err != nil {
			t.Fatalf("ObservePreference iteration %d: %v", i, err)
		}
	}

	prefs, err := svc.GetPreferences("user-conf-001", "schedule")
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	if len(prefs) != 1 {
		t.Fatalf("expected 1 preference, got %d", len(prefs))
	}
	if prefs[0].ObservedCount != 5 {
		t.Errorf("ObservedCount = %d, want 5", prefs[0].ObservedCount)
	}
	// Confidence should have grown: min(1.0, 0.5 + 5*0.1) = 1.0
	if prefs[0].Confidence != 1.0 {
		t.Errorf("Confidence = %f, want 1.0", prefs[0].Confidence)
	}
}

func TestRecordMessage(t *testing.T) {
	dbPath := testProfileDB(t)
	cfg := profile.Config{
		Enabled:          true,
		SentimentEnabled: true,
	}
	sentimentFn := func(text string) (float64, []string) {
		r := nlp.Analyze(text)
		return r.Score, r.Keywords
	}
	svc := profile.New(dbPath, cfg, makeLifeDB(), newUUID, sentimentFn, nlp.Label)

	// Record a positive message.
	err := svc.RecordMessage("tg:msg-001", "TestUser", "I'm so happy today!")
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}

	// Verify user was created.
	uid, err := svc.ResolveUser("tg:msg-001")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}

	// Check mood log.
	mood, err := svc.GetMoodTrend(uid, 7)
	if err != nil {
		t.Fatalf("GetMoodTrend: %v", err)
	}
	if len(mood) == 0 {
		t.Fatal("expected mood entries after positive message")
	}
	score, ok := mood[0]["sentimentScore"].(float64)
	if !ok || score <= 0 {
		t.Errorf("expected positive sentiment score, got %v", mood[0]["sentimentScore"])
	}
}

func TestRecordMessage_NoSentiment(t *testing.T) {
	dbPath := testProfileDB(t)
	cfg := profile.Config{
		Enabled:          true,
		SentimentEnabled: false,
	}
	sentimentFn := func(text string) (float64, []string) {
		r := nlp.Analyze(text)
		return r.Score, r.Keywords
	}
	svc := profile.New(dbPath, cfg, makeLifeDB(), newUUID, sentimentFn, nlp.Label)

	// Record a message -- should not log mood.
	err := svc.RecordMessage("tg:nosenti-001", "TestUser", "I'm so happy today!")
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}

	uid, _ := svc.ResolveUser("tg:nosenti-001")
	mood, _ := svc.GetMoodTrend(uid, 7)
	if len(mood) != 0 {
		t.Errorf("expected no mood entries with sentiment disabled, got %d", len(mood))
	}
}

func TestGetMoodTrend(t *testing.T) {
	dbPath := testProfileDB(t)
	cfg := profile.Config{
		Enabled:          true,
		SentimentEnabled: true,
	}
	sentimentFn := func(text string) (float64, []string) {
		r := nlp.Analyze(text)
		return r.Score, r.Keywords
	}
	svc := profile.New(dbPath, cfg, makeLifeDB(), newUUID, sentimentFn, nlp.Label)

	// Record multiple messages with different sentiments.
	svc.RecordMessage("tg:mood-001", "MoodUser", "I'm so happy and love this!")
	svc.RecordMessage("tg:mood-001", "MoodUser", "This is terrible and awful")
	svc.RecordMessage("tg:mood-001", "MoodUser", "Thanks, great work!")

	uid, _ := svc.ResolveUser("tg:mood-001")
	mood, err := svc.GetMoodTrend(uid, 7)
	if err != nil {
		t.Fatalf("GetMoodTrend: %v", err)
	}
	// Should have at least some entries (neutral messages won't be logged).
	if len(mood) < 2 {
		t.Errorf("expected at least 2 mood entries, got %d", len(mood))
	}
}

func TestGetUserContext(t *testing.T) {
	dbPath := testProfileDB(t)
	cfg := profile.Config{
		Enabled:          true,
		SentimentEnabled: true,
	}
	sentimentFn := func(text string) (float64, []string) {
		r := nlp.Analyze(text)
		return r.Score, r.Keywords
	}
	svc := profile.New(dbPath, cfg, makeLifeDB(), newUUID, sentimentFn, nlp.Label)

	// Set up a user with data.
	svc.RecordMessage("tg:ctx-001", "ContextUser", "I love sushi!")
	uid, _ := svc.ResolveUser("tg:ctx-001")
	svc.UpdateProfile(uid, map[string]string{
		"displayName":       "ContextUser",
		"preferredLanguage": "ja",
		"timezone":          "Asia/Tokyo",
	})
	svc.ObservePreference(uid, "food", "favorite", "sushi")

	// Get full context.
	ctx, err := svc.GetUserContext("tg:ctx-001")
	if err != nil {
		t.Fatalf("GetUserContext: %v", err)
	}
	if ctx["userId"] != uid {
		t.Errorf("userId = %v, want %s", ctx["userId"], uid)
	}
	if ctx["profile"] == nil {
		t.Error("expected profile in context")
	}
	if ctx["preferences"] == nil {
		t.Error("expected preferences in context")
	}
}

func TestGetPreferences_FilterByCategory(t *testing.T) {
	dbPath := testProfileDB(t)
	svc := testProfileService(t, dbPath)

	svc.CreateProfile(UserProfile{ID: "user-cat-001"})
	svc.ObservePreference("user-cat-001", "food", "favorite", "sushi")
	svc.ObservePreference("user-cat-001", "schedule", "wake_time", "7am")

	// Filter by food.
	prefs, _ := svc.GetPreferences("user-cat-001", "food")
	if len(prefs) != 1 {
		t.Errorf("expected 1 food preference, got %d", len(prefs))
	}

	// Filter by schedule.
	prefs, _ = svc.GetPreferences("user-cat-001", "schedule")
	if len(prefs) != 1 {
		t.Errorf("expected 1 schedule preference, got %d", len(prefs))
	}

	// All.
	prefs, _ = svc.GetPreferences("user-cat-001", "")
	if len(prefs) != 2 {
		t.Errorf("expected 2 total preferences, got %d", len(prefs))
	}
}

func TestCanvasEngine_NewSession(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled:      true,
			AllowScripts: false,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test Canvas", "<p>Hello World</p>", "800px", "600px")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	if session.ID == "" {
		t.Error("session ID is empty")
	}
	if session.Title != "Test Canvas" {
		t.Errorf("expected title 'Test Canvas', got %q", session.Title)
	}
	if session.Content != "<p>Hello World</p>" {
		t.Errorf("expected content '<p>Hello World</p>', got %q", session.Content)
	}
	if session.Width != "800px" {
		t.Errorf("expected width '800px', got %q", session.Width)
	}
	if session.Height != "600px" {
		t.Errorf("expected height '600px', got %q", session.Height)
	}
	if session.Source != "builtin" {
		t.Errorf("expected source 'builtin', got %q", session.Source)
	}
}

func TestCanvasEngine_UpdateSession(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled:      true,
			AllowScripts: false,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test", "<p>Original</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	err = ce.updateCanvas(session.ID, "<p>Updated</p>")
	if err != nil {
		t.Fatalf("updateCanvas failed: %v", err)
	}

	updated, err := ce.getCanvas(session.ID)
	if err != nil {
		t.Fatalf("getCanvas failed: %v", err)
	}

	if updated.Content != "<p>Updated</p>" {
		t.Errorf("expected content '<p>Updated</p>', got %q", updated.Content)
	}
}

func TestCanvasEngine_CloseSession(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	err = ce.closeCanvas(session.ID)
	if err != nil {
		t.Fatalf("closeCanvas failed: %v", err)
	}

	_, err = ce.getCanvas(session.ID)
	if err == nil {
		t.Error("expected error when getting closed session, got nil")
	}
}

func TestCanvasEngine_ListSessions(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	ce.renderCanvas("Canvas 1", "<p>Test 1</p>", "", "")
	ce.renderCanvas("Canvas 2", "<p>Test 2</p>", "", "")
	ce.renderCanvas("Canvas 3", "<p>Test 3</p>", "", "")

	sessions := ce.listCanvasSessions()
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestCanvasEngine_GetSession(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	retrieved, err := ce.getCanvas(session.ID)
	if err != nil {
		t.Fatalf("getCanvas failed: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("expected ID %q, got %q", session.ID, retrieved.ID)
	}
}

func TestCanvasEngine_NotFound(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	_, err := ce.getCanvas("nonexistent")
	if err == nil {
		t.Error("expected error when getting nonexistent session, got nil")
	}

	err = ce.updateCanvas("nonexistent", "<p>Test</p>")
	if err == nil {
		t.Error("expected error when updating nonexistent session, got nil")
	}

	err = ce.closeCanvas("nonexistent")
	if err == nil {
		t.Error("expected error when closing nonexistent session, got nil")
	}
}

func TestCanvasRender_Tool(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled:      true,
			AllowScripts: false,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	handler := toolCanvasRender(context.Background(), ce)

	input := json.RawMessage(`{
		"title": "Test Canvas",
		"content": "<div>Test Content</div>",
		"width": "1000px",
		"height": "500px"
	}`)

	result, err := handler(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("canvas_render handler failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	if res["id"] == "" {
		t.Error("expected id in result")
	}
	if res["title"] != "Test Canvas" {
		t.Errorf("expected title 'Test Canvas', got %v", res["title"])
	}
}

func TestCanvasUpdate_Tool(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test", "<p>Original</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	handler := toolCanvasUpdate(context.Background(), ce)

	input := json.RawMessage(fmt.Sprintf(`{
		"id": "%s",
		"content": "<p>Updated Content</p>"
	}`, session.ID))

	result, err := handler(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("canvas_update handler failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	if res["id"] != session.ID {
		t.Errorf("expected id %q, got %v", session.ID, res["id"])
	}

	updated, _ := ce.getCanvas(session.ID)
	if updated.Content != "<p>Updated Content</p>" {
		t.Errorf("expected updated content, got %q", updated.Content)
	}
}

func TestCanvasClose_Tool(t *testing.T) {
	cfg := &Config{
		Canvas: CanvasConfig{
			Enabled: true,
		},
	}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("Test", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	handler := toolCanvasClose(context.Background(), ce)

	input := json.RawMessage(fmt.Sprintf(`{
		"id": "%s"
	}`, session.ID))

	result, err := handler(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("canvas_close handler failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	if res["id"] != session.ID {
		t.Errorf("expected id %q, got %v", session.ID, res["id"])
	}

	_, err = ce.getCanvas(session.ID)
	if err == nil {
		t.Error("expected error when getting closed session")
	}
}

func TestCanvasConfig_Default(t *testing.T) {
	cfg := &Config{}
	ce := newCanvasEngine(cfg, nil)

	session, err := ce.renderCanvas("", "<p>Test</p>", "", "")
	if err != nil {
		t.Fatalf("renderCanvas failed: %v", err)
	}

	if session.Title != "Canvas" {
		t.Errorf("expected default title 'Canvas', got %q", session.Title)
	}
	if session.Width != "100%" {
		t.Errorf("expected default width '100%%', got %q", session.Width)
	}
	if session.Height != "400px" {
		t.Errorf("expected default height '400px', got %q", session.Height)
	}
}

func TestStripScriptTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "<p>Hello</p><script>alert('xss')</script><p>World</p>",
			expected: "<p>Hello</p><p>World</p>",
		},
		{
			input:    "<SCRIPT>alert('xss')</SCRIPT>",
			expected: "",
		},
		{
			input:    "<p>No scripts here</p>",
			expected: "<p>No scripts here</p>",
		},
		{
			input:    "<script src='evil.js'></script><div>Content</div>",
			expected: "<div>Content</div>",
		},
	}

	for _, tc := range tests {
		result := stripScriptTags(tc.input)
		if result != tc.expected {
			t.Errorf("stripScriptTags(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}
