package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initDB creates all required tables for tool tests.
func initDB(dbPath string) {
	sql := `
CREATE TABLE IF NOT EXISTS agent_memory (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  role TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_memory_role_key ON agent_memory(role, key);
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  session_id TEXT,
  channel_type TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT '',
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
	tmpDB := filepath.Join(t.TempDir(), "test.db")
	initDB(tmpDB)

	// Insert test memory.
	_, err := queryDB(tmpDB, `INSERT INTO agent_memory (role, key, value, updated_at)
	                          VALUES ('test', 'key1', 'hello world', datetime('now'))`)
	if err != nil {
		t.Fatalf("insert memory: %v", err)
	}

	cfg := &Config{HistoryDB: tmpDB}
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
	tmpDB := filepath.Join(t.TempDir(), "test.db")
	initDB(tmpDB)

	// Insert test memory.
	_, err := queryDB(tmpDB, `INSERT INTO agent_memory (role, key, value, updated_at)
	                          VALUES ('test', 'mykey', 'myvalue', datetime('now'))`)
	if err != nil {
		t.Fatalf("insert memory: %v", err)
	}

	cfg := &Config{HistoryDB: tmpDB}
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
	_, err := queryDB(tmpDB, `INSERT INTO knowledge (filename, content, snippet, indexed_at)
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
	_, err := queryDB(tmpDB, `INSERT INTO sessions (session_id, channel_type, channel_id, message_count, created_at, updated_at)
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
