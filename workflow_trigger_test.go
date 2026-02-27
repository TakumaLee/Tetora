package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTriggerCronMatch(t *testing.T) {
	// Trigger with a cron expression that matches the current time.
	now := time.Now()
	cron := fmt.Sprintf("%d %d * * *", now.Minute(), now.Hour())

	boolTrue := true
	trigger := WorkflowTriggerConfig{
		Name:         "test-cron",
		WorkflowName: "test-wf",
		Enabled:      &boolTrue,
		Trigger: TriggerSpec{
			Type: "cron",
			Cron: cron,
		},
	}

	expr, err := parseCronExpr(trigger.Trigger.Cron)
	if err != nil {
		t.Fatalf("parseCronExpr: %v", err)
	}

	if !expr.matches(now) {
		t.Errorf("expected cron %q to match current time %v", cron, now)
	}
}

func TestTriggerCooldown(t *testing.T) {
	cfg := &Config{
		WorkflowTriggers: []WorkflowTriggerConfig{
			{Name: "cd-test", WorkflowName: "wf", Cooldown: "1h"},
		},
	}

	engine := &WorkflowTriggerEngine{
		cfg:       cfg,
		triggers:  cfg.WorkflowTriggers,
		cooldowns: make(map[string]time.Time),
		lastFired: make(map[string]time.Time),
	}

	// First call: should be allowed (no cooldown set yet).
	if !engine.checkCooldown("cd-test") {
		t.Error("expected checkCooldown to return true (no cooldown set)")
	}

	// Set cooldown to 1 hour from now.
	engine.mu.Lock()
	engine.cooldowns["cd-test"] = time.Now().Add(1 * time.Hour)
	engine.mu.Unlock()

	// Now should be in cooldown.
	if engine.checkCooldown("cd-test") {
		t.Error("expected checkCooldown to return false (in cooldown)")
	}

	// Set cooldown to the past.
	engine.mu.Lock()
	engine.cooldowns["cd-test"] = time.Now().Add(-1 * time.Second)
	engine.mu.Unlock()

	// Should be past cooldown.
	if !engine.checkCooldown("cd-test") {
		t.Error("expected checkCooldown to return true (past cooldown)")
	}
}

func TestTriggerEnabled(t *testing.T) {
	// nil Enabled -> should be enabled (default).
	t1 := WorkflowTriggerConfig{Name: "t1"}
	if !t1.isEnabled() {
		t.Error("expected nil Enabled to default to true")
	}

	// Explicit true.
	boolTrue := true
	t2 := WorkflowTriggerConfig{Name: "t2", Enabled: &boolTrue}
	if !t2.isEnabled() {
		t.Error("expected Enabled=true to be enabled")
	}

	// Explicit false.
	boolFalse := false
	t3 := WorkflowTriggerConfig{Name: "t3", Enabled: &boolFalse}
	if t3.isEnabled() {
		t.Error("expected Enabled=false to be disabled")
	}
}

func TestToolCallStep(t *testing.T) {
	// Create a minimal config with a tool registry and a mock tool.
	cfg := &Config{
		toolRegistry: &ToolRegistry{
			tools: make(map[string]*ToolDef),
		},
	}

	// Register a mock tool.
	cfg.toolRegistry.Register(&ToolDef{
		Name:        "mock_tool",
		Description: "A mock tool for testing",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`),
		Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			var args struct {
				Msg string `json:"msg"`
			}
			json.Unmarshal(input, &args)
			return "echo: " + args.Msg, nil
		},
	})

	wf := &Workflow{Name: "test-wf", Steps: []WorkflowStep{
		{ID: "s1", Type: "tool_call", ToolName: "mock_tool", ToolInput: map[string]string{"msg": "hello"}},
	}}

	exec := &workflowExecutor{
		cfg:      cfg,
		workflow: wf,
		run:      &WorkflowRun{ID: "run1", StepResults: make(map[string]*StepRunResult)},
		wCtx:     newWorkflowContext(wf, nil),
		mode:     WorkflowModeLive,
	}

	result := &StepRunResult{StepID: "s1"}
	step := &wf.Steps[0]
	exec.runToolCallStep(context.Background(), step, result, exec.wCtx)

	if result.Status != "success" {
		t.Errorf("expected success, got %q (error: %s)", result.Status, result.Error)
	}
	if result.Output != "echo: hello" {
		t.Errorf("expected output %q, got %q", "echo: hello", result.Output)
	}
}

func TestToolCallStepWithVarExpansion(t *testing.T) {
	cfg := &Config{
		toolRegistry: &ToolRegistry{
			tools: make(map[string]*ToolDef),
		},
	}

	cfg.toolRegistry.Register(&ToolDef{
		Name: "echo_tool",
		Handler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			var args struct {
				Msg string `json:"msg"`
			}
			json.Unmarshal(input, &args)
			return args.Msg, nil
		},
	})

	wf := &Workflow{
		Name:      "test-wf",
		Variables: map[string]string{"greeting": "world"},
		Steps: []WorkflowStep{
			{ID: "s1", Type: "tool_call", ToolName: "echo_tool", ToolInput: map[string]string{"msg": "hello {{greeting}}"}},
		},
	}

	exec := &workflowExecutor{
		cfg:      cfg,
		workflow: wf,
		run:      &WorkflowRun{ID: "run1", StepResults: make(map[string]*StepRunResult)},
		wCtx:     newWorkflowContext(wf, nil),
		mode:     WorkflowModeLive,
	}

	result := &StepRunResult{StepID: "s1"}
	exec.runToolCallStep(context.Background(), &wf.Steps[0], result, exec.wCtx)

	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
	if result.Output != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", result.Output)
	}
}

func TestDelayStep(t *testing.T) {
	wf := &Workflow{Name: "test-wf", Steps: []WorkflowStep{
		{ID: "d1", Type: "delay", Delay: "10ms"},
	}}

	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: wf,
		run:      &WorkflowRun{ID: "run1", StepResults: make(map[string]*StepRunResult)},
		wCtx:     newWorkflowContext(wf, nil),
		mode:     WorkflowModeLive,
	}

	result := &StepRunResult{StepID: "d1"}
	start := time.Now()
	exec.runDelayStep(context.Background(), &wf.Steps[0], result)
	elapsed := time.Since(start)

	if result.Status != "success" {
		t.Errorf("expected success, got %q (error: %s)", result.Status, result.Error)
	}
	if elapsed < 10*time.Millisecond {
		t.Errorf("delay too short: %v", elapsed)
	}
}

func TestDelayStepCancel(t *testing.T) {
	wf := &Workflow{Name: "test-wf", Steps: []WorkflowStep{
		{ID: "d1", Type: "delay", Delay: "10s"},
	}}

	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: wf,
		run:      &WorkflowRun{ID: "run1", StepResults: make(map[string]*StepRunResult)},
		wCtx:     newWorkflowContext(wf, nil),
		mode:     WorkflowModeLive,
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := &StepRunResult{StepID: "d1"}

	// Cancel after 10ms.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	exec.runDelayStep(ctx, &wf.Steps[0], result)

	if result.Status != "cancelled" {
		t.Errorf("expected cancelled, got %q", result.Status)
	}
}

func TestNotifyStep(t *testing.T) {
	broker := newSSEBroker()

	wf := &Workflow{
		Name:      "test-wf",
		Variables: map[string]string{"user": "alice"},
		Steps: []WorkflowStep{
			{ID: "n1", Type: "notify", NotifyMsg: "Hello {{user}}, workflow done!", NotifyTo: "telegram"},
		},
	}

	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: wf,
		run:      &WorkflowRun{ID: "run1", StepResults: make(map[string]*StepRunResult)},
		wCtx:     newWorkflowContext(wf, nil),
		broker:   broker,
		mode:     WorkflowModeLive,
	}

	result := &StepRunResult{StepID: "n1"}
	exec.runNotifyStep(&wf.Steps[0], result, exec.wCtx)

	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
	expected := "Hello alice, workflow done!"
	if result.Output != expected {
		t.Errorf("expected %q, got %q", expected, result.Output)
	}
}

func TestWebhookTrigger(t *testing.T) {
	boolTrue := true
	cfg := &Config{
		WorkflowTriggers: []WorkflowTriggerConfig{
			{
				Name:         "my-webhook",
				WorkflowName: "nonexistent-wf",
				Enabled:      &boolTrue,
				Trigger: TriggerSpec{
					Type:    "webhook",
					Webhook: "my-webhook",
				},
			},
		},
	}

	engine := newWorkflowTriggerEngine(cfg, nil, make(chan struct{}, 1), nil, nil)

	// Webhook trigger for unknown name should fail.
	err := engine.HandleWebhookTrigger("unknown", nil)
	if err == nil {
		t.Error("expected error for unknown webhook trigger")
	}

	// Webhook trigger for known name should succeed (even though workflow doesn't exist,
	// executeTrigger runs async and logs the error).
	err = engine.HandleWebhookTrigger("my-webhook", map[string]string{"key": "val"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWebhookTriggerDisabled(t *testing.T) {
	boolFalse := false
	cfg := &Config{
		WorkflowTriggers: []WorkflowTriggerConfig{
			{
				Name:         "disabled-hook",
				WorkflowName: "wf",
				Enabled:      &boolFalse,
				Trigger: TriggerSpec{
					Type:    "webhook",
					Webhook: "disabled-hook",
				},
			},
		},
	}

	engine := newWorkflowTriggerEngine(cfg, nil, make(chan struct{}, 1), nil, nil)

	err := engine.HandleWebhookTrigger("disabled-hook", nil)
	if err == nil {
		t.Error("expected error for disabled webhook trigger")
	}
	if err != nil && !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected disabled error, got: %v", err)
	}
}

func TestTriggerRunRecording(t *testing.T) {
	// Create a temporary DB.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	initTriggerRunsTable(dbPath)

	// Record a run.
	now := time.Now().Format(time.RFC3339)
	recordTriggerRun(dbPath, "test-trigger", "test-wf", "run-123", "success", now, now, "")

	// Query it back.
	runs, err := queryTriggerRuns(dbPath, "test-trigger", 10)
	if err != nil {
		t.Fatalf("queryTriggerRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if fmt.Sprintf("%v", runs[0]["trigger_name"]) != "test-trigger" {
		t.Errorf("expected trigger_name=test-trigger, got %v", runs[0]["trigger_name"])
	}
	if fmt.Sprintf("%v", runs[0]["status"]) != "success" {
		t.Errorf("expected status=success, got %v", runs[0]["status"])
	}

	// Record another run.
	recordTriggerRun(dbPath, "other-trigger", "other-wf", "run-456", "error", now, now, "boom")

	// Query all runs.
	allRuns, err := queryTriggerRuns(dbPath, "", 10)
	if err != nil {
		t.Fatalf("queryTriggerRuns all: %v", err)
	}
	if len(allRuns) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(allRuns))
	}

	// Query filtered.
	filtered, err := queryTriggerRuns(dbPath, "other-trigger", 10)
	if err != nil {
		t.Fatalf("queryTriggerRuns filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered run, got %d", len(filtered))
	}
}

func TestTriggerRunRecordingEmptyDB(t *testing.T) {
	// Should not panic with empty DB path.
	recordTriggerRun("", "t", "w", "r", "s", "", "", "")
	runs, err := queryTriggerRuns("", "", 10)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if runs != nil {
		t.Errorf("expected nil runs, got %v", runs)
	}
}

func TestListTriggers(t *testing.T) {
	boolTrue := true
	boolFalse := false
	cfg := &Config{
		WorkflowTriggers: []WorkflowTriggerConfig{
			{
				Name:         "cron-daily",
				WorkflowName: "daily-report",
				Enabled:      &boolTrue,
				Trigger:      TriggerSpec{Type: "cron", Cron: "0 9 * * *"},
				Cooldown:     "1h",
			},
			{
				Name:         "event-complete",
				WorkflowName: "post-process",
				Enabled:      &boolFalse,
				Trigger:      TriggerSpec{Type: "event", Event: "workflow_completed"},
			},
			{
				Name:         "webhook-deploy",
				WorkflowName: "deploy-pipeline",
				Enabled:      &boolTrue,
				Trigger:      TriggerSpec{Type: "webhook", Webhook: "deploy"},
			},
		},
	}

	engine := newWorkflowTriggerEngine(cfg, nil, make(chan struct{}, 1), nil, nil)
	infos := engine.ListTriggers()

	if len(infos) != 3 {
		t.Fatalf("expected 3 triggers, got %d", len(infos))
	}

	// Check first trigger.
	if infos[0].Name != "cron-daily" {
		t.Errorf("expected name=cron-daily, got %s", infos[0].Name)
	}
	if !infos[0].Enabled {
		t.Error("expected cron-daily to be enabled")
	}
	if infos[0].Type != "cron" {
		t.Errorf("expected type=cron, got %s", infos[0].Type)
	}
	if infos[0].NextCron == "" {
		t.Error("expected NextCron to be set for cron trigger")
	}
	if infos[0].Cooldown != "1h" {
		t.Errorf("expected cooldown=1h, got %s", infos[0].Cooldown)
	}

	// Check disabled trigger.
	if infos[1].Enabled {
		t.Error("expected event-complete to be disabled")
	}
}

func TestFireTrigger(t *testing.T) {
	boolTrue := true

	// Create a temp dir with a workflow file.
	tmpDir := t.TempDir()
	wfDir := filepath.Join(tmpDir, "workflows")
	os.MkdirAll(wfDir, 0o755)

	// Write a simple workflow.
	wf := Workflow{
		Name: "test-fire-wf",
		Steps: []WorkflowStep{
			{ID: "s1", Type: "delay", Delay: "1ms"},
		},
	}
	wfData, _ := json.MarshalIndent(wf, "", "  ")
	os.WriteFile(filepath.Join(wfDir, "test-fire-wf.json"), wfData, 0o644)

	cfg := &Config{
		baseDir: tmpDir,
		WorkflowTriggers: []WorkflowTriggerConfig{
			{
				Name:         "fire-test",
				WorkflowName: "test-fire-wf",
				Enabled:      &boolTrue,
				Trigger:      TriggerSpec{Type: "webhook"},
			},
		},
	}

	engine := newWorkflowTriggerEngine(cfg, nil, make(chan struct{}, 3), nil, nil)

	// Fire existing trigger.
	err := engine.FireTrigger("fire-test")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Fire non-existent trigger.
	err = engine.FireTrigger("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent trigger")
	}

	// Wait a bit for the async execution.
	time.Sleep(50 * time.Millisecond)
}

func TestFireTriggerDisabled(t *testing.T) {
	boolFalse := false
	cfg := &Config{
		WorkflowTriggers: []WorkflowTriggerConfig{
			{
				Name:         "disabled-fire",
				WorkflowName: "wf",
				Enabled:      &boolFalse,
				Trigger:      TriggerSpec{Type: "webhook"},
			},
		},
	}

	engine := newWorkflowTriggerEngine(cfg, nil, make(chan struct{}, 1), nil, nil)

	err := engine.FireTrigger("disabled-fire")
	if err == nil {
		t.Error("expected error for disabled trigger")
	}
}

func TestMatchEventType(t *testing.T) {
	tests := []struct {
		eventType string
		pattern   string
		want      bool
	}{
		{"workflow_completed", "workflow_completed", true},
		{"workflow_completed", "workflow_started", false},
		{"workflow_completed", "workflow_*", true},
		{"workflow_started", "workflow_*", true},
		{"step_completed", "workflow_*", false},
		{"step_completed", "step_*", true},
		{"anything", "*", true},
	}

	for _, tt := range tests {
		got := matchEventType(tt.eventType, tt.pattern)
		if got != tt.want {
			t.Errorf("matchEventType(%q, %q) = %v, want %v", tt.eventType, tt.pattern, got, tt.want)
		}
	}
}

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"name": "world", "count": "42"}

	got := expandVars("hello {{name}}, count={{count}}", vars)
	if got != "hello world, count=42" {
		t.Errorf("expandVars = %q, want %q", got, "hello world, count=42")
	}

	// No match.
	got2 := expandVars("{{missing}}", vars)
	if got2 != "{{missing}}" {
		t.Errorf("expandVars = %q, want %q", got2, "{{missing}}")
	}
}

func TestExpandToolInput(t *testing.T) {
	vars := map[string]string{"dir": "/tmp", "file": "test.txt"}
	input := map[string]string{
		"path":    "{{dir}}/{{file}}",
		"literal": "no-vars-here",
	}

	result := expandToolInput(input, vars)
	if result["path"] != "/tmp/test.txt" {
		t.Errorf("expected /tmp/test.txt, got %s", result["path"])
	}
	if result["literal"] != "no-vars-here" {
		t.Errorf("expected no-vars-here, got %s", result["literal"])
	}
}

func TestToolInputToJSON(t *testing.T) {
	input := map[string]string{"key": "value", "num": "42"}
	raw := toolInputToJSON(input)

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["key"] != "value" {
		t.Errorf("expected key=value, got %v", parsed["key"])
	}
}

func TestToolInputToJSON_Empty(t *testing.T) {
	raw := toolInputToJSON(nil)
	if string(raw) != "{}" {
		t.Errorf("expected {}, got %s", string(raw))
	}
}

func TestValidateStep_NewTypes(t *testing.T) {
	allIDs := map[string]bool{"s1": true, "s2": true}

	// tool_call without toolName.
	errs := validateStep(WorkflowStep{ID: "s1", Type: "tool_call"}, allIDs)
	if len(errs) == 0 {
		t.Error("expected error for tool_call without toolName")
	}

	// tool_call with toolName.
	errs = validateStep(WorkflowStep{ID: "s1", Type: "tool_call", ToolName: "exec"}, allIDs)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}

	// delay without duration.
	errs = validateStep(WorkflowStep{ID: "s1", Type: "delay"}, allIDs)
	if len(errs) == 0 {
		t.Error("expected error for delay without duration")
	}

	// delay with invalid duration.
	errs = validateStep(WorkflowStep{ID: "s1", Type: "delay", Delay: "bad"}, allIDs)
	if len(errs) == 0 {
		t.Error("expected error for delay with invalid duration")
	}

	// delay with valid duration.
	errs = validateStep(WorkflowStep{ID: "s1", Type: "delay", Delay: "5s"}, allIDs)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}

	// notify without message.
	errs = validateStep(WorkflowStep{ID: "s1", Type: "notify"}, allIDs)
	if len(errs) == 0 {
		t.Error("expected error for notify without notifyMsg")
	}

	// notify with message.
	errs = validateStep(WorkflowStep{ID: "s1", Type: "notify", NotifyMsg: "hello"}, allIDs)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

