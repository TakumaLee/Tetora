package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCfg creates a minimal config for workflow exec tests.
func testWorkflowCfg(t *testing.T) (*Config, chan struct{}) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	cfg := &Config{
		BaseDir:               dir,
		HistoryDB:             dbPath,
		DefaultModel:          "sonnet",
		DefaultTimeout:        "5m",
		DefaultPermissionMode: "plan",
		DefaultWorkdir:        dir,
		DefaultProvider:       "claude",
	}
	cfg.Runtime.ProviderRegistry = initProviders(cfg)

	sem := make(chan struct{}, 4)
	return cfg, sem
}

func TestWorkflowRunRecordAndQuery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	run := &WorkflowRun{
		ID:           "run-001",
		WorkflowName: "test-wf",
		Status:       "success",
		StartedAt:    time.Now().Format(time.RFC3339),
		FinishedAt:   time.Now().Format(time.RFC3339),
		DurationMs:   1500,
		TotalCost:    0.05,
		Variables:    map[string]string{"input": "test"},
		StepResults: map[string]*StepRunResult{
			"step1": {StepID: "step1", Status: "success", Output: "done"},
		},
	}

	recordWorkflowRun(dbPath, run)

	// Query all.
	runs, err := queryWorkflowRuns(dbPath, 10, "")
	if err != nil {
		t.Fatalf("queryWorkflowRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].ID != "run-001" {
		t.Errorf("id = %q, want run-001", runs[0].ID)
	}
	if runs[0].Status != "success" {
		t.Errorf("status = %q, want success", runs[0].Status)
	}

	// Query by name.
	runs, err = queryWorkflowRuns(dbPath, 10, "test-wf")
	if err != nil {
		t.Fatalf("queryWorkflowRuns by name: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 run, got %d", len(runs))
	}

	// Query by ID.
	got, err := queryWorkflowRunByID(dbPath, "run-001")
	if err != nil {
		t.Fatalf("queryWorkflowRunByID: %v", err)
	}
	if got.WorkflowName != "test-wf" {
		t.Errorf("name = %q", got.WorkflowName)
	}
	if got.Variables["input"] != "test" {
		t.Errorf("variables = %v", got.Variables)
	}
	if sr, ok := got.StepResults["step1"]; !ok || sr.Status != "success" {
		t.Errorf("step results = %v", got.StepResults)
	}

	// Query non-existent.
	_, err = queryWorkflowRunByID(dbPath, "nope")
	if err == nil {
		t.Error("expected error for non-existent run")
	}
}

func TestWorkflowRunsEmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Query on non-existent table should return nil, not error.
	runs, err := queryWorkflowRuns(dbPath, 10, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestWorkflowRunMultiple(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	for i := 0; i < 5; i++ {
		run := &WorkflowRun{
			ID:           newUUID(),
			WorkflowName: "pipeline",
			Status:       "success",
			StartedAt:    time.Now().Format(time.RFC3339),
			StepResults:  map[string]*StepRunResult{},
		}
		recordWorkflowRun(dbPath, run)
	}

	runs, err := queryWorkflowRuns(dbPath, 3, "")
	if err != nil {
		t.Fatalf("queryWorkflowRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("expected 3 runs (limit), got %d", len(runs))
	}
}

func TestInitWorkflowRunsTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Should not panic, even called multiple times.
	initWorkflowRunsTable(dbPath)
	initWorkflowRunsTable(dbPath)

	// Empty path should be no-op.
	initWorkflowRunsTable("")
}

func TestStepRunResultTypes(t *testing.T) {
	r := &StepRunResult{
		StepID:    "test",
		Status:    "success",
		Output:    "hello",
		CostUSD:   0.01,
		TaskID:    "t-1",
		SessionID: "s-1",
		Retries:   2,
	}
	if r.StepID != "test" {
		t.Error("StepID mismatch")
	}
	if r.Retries != 2 {
		t.Error("Retries mismatch")
	}
}

func TestWorkflowExecutorPublishNobroker(t *testing.T) {
	// Ensure publishEvent doesn't panic with nil broker.
	exec := &workflowExecutor{
		broker:   nil,
		workflow: &Workflow{Name: "test"},
		run:      &WorkflowRun{ID: "run-1"},
	}
	exec.publishEvent("test", map[string]any{"key": "value"})
	// No panic = pass.
}

func TestRunConditionStep(t *testing.T) {
	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: &Workflow{Name: "test"},
		run:      &WorkflowRun{ID: "run-1", StepResults: map[string]*StepRunResult{}},
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{
				"check": {Status: "success", Output: "ok"},
			},
			Env: map[string]string{},
		},
	}

	// True condition.
	step := &WorkflowStep{
		ID:   "cond1",
		Type: "condition",
		If:   "{{steps.check.status}} == 'success'",
		Then: "stepA",
		Else: "stepB",
	}
	result := &StepRunResult{StepID: "cond1"}
	exec.runConditionStep(step, result, exec.wCtx)

	if result.Status != "success" {
		t.Errorf("status = %q, want success", result.Status)
	}
	if result.Output != "stepA" {
		t.Errorf("output = %q, want stepA (then branch)", result.Output)
	}

	// False condition.
	step.If = "{{steps.check.status}} == 'error'"
	result2 := &StepRunResult{StepID: "cond2"}
	exec.runConditionStep(step, result2, exec.wCtx)

	if result2.Output != "stepB" {
		t.Errorf("output = %q, want stepB (else branch)", result2.Output)
	}
}

func TestRunConditionStepNoElse(t *testing.T) {
	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: &Workflow{Name: "test"},
		run:      &WorkflowRun{ID: "run-1", StepResults: map[string]*StepRunResult{}},
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
	}

	step := &WorkflowStep{
		ID:   "cond",
		Type: "condition",
		If:   "{{missing}} == 'yes'",
		Then: "stepA",
	}
	result := &StepRunResult{StepID: "cond"}
	exec.runConditionStep(step, result, exec.wCtx)

	if result.Output != "" {
		t.Errorf("output = %q, want empty (no else)", result.Output)
	}
}

func TestRunSkillStepNotFound(t *testing.T) {
	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: &Workflow{Name: "test"},
		run:      &WorkflowRun{ID: "run-1", StepResults: map[string]*StepRunResult{}},
		wCtx:     newWorkflowContext(&Workflow{}, nil),
	}

	step := &WorkflowStep{
		ID:    "s1",
		Type:  "skill",
		Skill: "nonexistent-skill",
	}
	result := &StepRunResult{StepID: "s1"}
	exec.runSkillStep(context.Background(), step, result, exec.wCtx)

	if result.Status != "error" {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !contains(result.Error, "not found") {
		t.Errorf("error = %q, want contains 'not found'", result.Error)
	}
}

func TestRunStepOnceUnknownType(t *testing.T) {
	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: &Workflow{Name: "test"},
		run:      &WorkflowRun{ID: "run-1", StepResults: map[string]*StepRunResult{}},
		wCtx:     newWorkflowContext(&Workflow{}, nil),
	}

	step := &WorkflowStep{ID: "bad", Type: "bogus"}
	result := &StepRunResult{StepID: "bad"}
	exec.runStepOnce(context.Background(), step, result)

	if result.Status != "error" {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !contains(result.Error, "unknown step type") {
		t.Errorf("error = %q", result.Error)
	}
}

func TestRecordWorkflowRunEmptyDB(t *testing.T) {
	// Empty dbPath should be a no-op, no panic.
	recordWorkflowRun("", &WorkflowRun{
		ID:          "x",
		StepResults: map[string]*StepRunResult{},
	})
}

func TestWorkflowRunStepResultsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	run := &WorkflowRun{
		ID:           "rt-001",
		WorkflowName: "roundtrip",
		Status:       "success",
		StartedAt:    time.Now().Format(time.RFC3339),
		StepResults: map[string]*StepRunResult{
			"a": {StepID: "a", Status: "success", Output: "output-a", CostUSD: 0.01},
			"b": {StepID: "b", Status: "error", Error: "failed", Retries: 3},
		},
	}
	recordWorkflowRun(dbPath, run)

	got, err := queryWorkflowRunByID(dbPath, "rt-001")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if sr := got.StepResults["a"]; sr == nil || sr.Output != "output-a" {
		t.Errorf("step a = %v", got.StepResults["a"])
	}
	if sr := got.StepResults["b"]; sr == nil || sr.Retries != 3 {
		t.Errorf("step b retries = %v", got.StepResults["b"])
	}
}

func TestHandleConditionResult(t *testing.T) {
	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: &Workflow{Name: "test"},
		run: &WorkflowRun{
			ID: "run-1",
			StepResults: map[string]*StepRunResult{
				"cond":   {StepID: "cond", Status: "success"},
				"stepA":  {StepID: "stepA", Status: "pending"},
				"stepB":  {StepID: "stepB", Status: "pending"},
			},
		},
		wCtx: newWorkflowContext(&Workflow{}, nil),
	}

	step := &WorkflowStep{
		ID:   "cond",
		Type: "condition",
		Then: "stepA",
		Else: "stepB",
	}

	remaining := map[string]int{"stepA": 1, "stepB": 1}
	dependents := map[string][]string{"cond": {"stepA", "stepB"}}
	readyCh := make(chan string, 10)

	// Condition chose "then" → stepA
	result := &StepRunResult{Output: "stepA"}
	exec.handleConditionResult(step, result, remaining, dependents, readyCh)

	// stepB should be skipped.
	if exec.run.StepResults["stepB"].Status != "skipped" {
		t.Errorf("stepB status = %q, want skipped", exec.run.StepResults["stepB"].Status)
	}

	// Both should have been unblocked in readyCh.
	unblocked := make([]string, 0)
	for {
		select {
		case id := <-readyCh:
			unblocked = append(unblocked, id)
		default:
			goto done
		}
	}
done:
	if len(unblocked) != 2 {
		t.Errorf("expected 2 unblocked, got %d: %v", len(unblocked), unblocked)
	}
}

func TestRunSkillStepWithEchoSkill(t *testing.T) {
	// Create a config with an echo skill.
	cfg := &Config{
		Skills: []SkillConfig{
			{
				Name:    "echo-test",
				Command: "echo",
				Args:    []string{"hello-workflow"},
				Timeout: "5s",
			},
		},
	}

	exec := &workflowExecutor{
		cfg:      cfg,
		workflow: &Workflow{Name: "test"},
		run:      &WorkflowRun{ID: "run-1", StepResults: map[string]*StepRunResult{}},
		wCtx:     newWorkflowContext(&Workflow{}, nil),
	}

	step := &WorkflowStep{
		ID:    "s1",
		Type:  "skill",
		Skill: "echo-test",
	}
	result := &StepRunResult{StepID: "s1"}
	exec.runSkillStep(context.Background(), step, result, exec.wCtx)

	if result.Status != "success" {
		t.Errorf("status = %q, want success (error: %s)", result.Status, result.Error)
	}
	if !contains(result.Output, "hello-workflow") {
		t.Errorf("output = %q, want contains hello-workflow", result.Output)
	}
}

func TestQueryWorkflowRunsByName(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	recordWorkflowRun(dbPath, &WorkflowRun{
		ID: "r1", WorkflowName: "alpha", Status: "success",
		StartedAt: time.Now().Format(time.RFC3339), StepResults: map[string]*StepRunResult{},
	})
	recordWorkflowRun(dbPath, &WorkflowRun{
		ID: "r2", WorkflowName: "beta", Status: "success",
		StartedAt: time.Now().Format(time.RFC3339), StepResults: map[string]*StepRunResult{},
	})
	recordWorkflowRun(dbPath, &WorkflowRun{
		ID: "r3", WorkflowName: "alpha", Status: "error",
		StartedAt: time.Now().Format(time.RFC3339), StepResults: map[string]*StepRunResult{},
	})

	runs, _ := queryWorkflowRuns(dbPath, 10, "alpha")
	if len(runs) != 2 {
		t.Errorf("expected 2 alpha runs, got %d", len(runs))
	}

	runs, _ = queryWorkflowRuns(dbPath, 10, "beta")
	if len(runs) != 1 {
		t.Errorf("expected 1 beta run, got %d", len(runs))
	}

	runs, _ = queryWorkflowRuns(dbPath, 10, "nope")
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestWorkflowRunEmptyDBPath(t *testing.T) {
	// All DB functions should be no-ops with empty path.
	initWorkflowRunsTable("")
	recordWorkflowRun("", &WorkflowRun{ID: "x", StepResults: map[string]*StepRunResult{}})

	runs, err := queryWorkflowRuns("", 10, "")
	if err != nil {
		// Empty path with sqlite3 may error, that's fine.
		_ = runs
	}
}

func TestResetZombieWorkflowRuns(t *testing.T) {
	seedRun := func(dbPath, id, status string, age time.Duration) {
		t.Helper()
		recordWorkflowRun(dbPath, &WorkflowRun{
			ID:           id,
			WorkflowName: "wf",
			Status:       status,
			StartedAt:    time.Now().Add(-age).UTC().Format(time.RFC3339),
			StepResults:  map[string]*StepRunResult{},
		})
	}

	t.Run("zombie_running_4h_old", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "test.db")
		seedRun(dbPath, "zombie-run", "running", 5*time.Hour)

		n := resetZombieWorkflowRuns(dbPath, 4*time.Hour)
		if n != 1 {
			t.Fatalf("return = %d, want 1", n)
		}

		got, err := queryWorkflowRunByID(dbPath, "zombie-run")
		if err != nil {
			t.Fatalf("queryWorkflowRunByID: %v", err)
		}
		if got.Status != "error" {
			t.Errorf("status = %q, want error", got.Status)
		}
		if !strings.Contains(got.Error, "zombie") {
			t.Errorf("error = %q, want contain 'zombie'", got.Error)
		}
		if got.FinishedAt == "" {
			t.Errorf("finished_at = empty, want non-empty")
		}
	})

	t.Run("zombie_resumed_4h_old", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "test.db")
		seedRun(dbPath, "zombie-resumed", "resumed", 5*time.Hour)

		n := resetZombieWorkflowRuns(dbPath, 4*time.Hour)
		if n != 1 {
			t.Fatalf("return = %d, want 1 (resumed should be terminated)", n)
		}

		got, err := queryWorkflowRunByID(dbPath, "zombie-resumed")
		if err != nil {
			t.Fatalf("queryWorkflowRunByID: %v", err)
		}
		if got.Status != "error" {
			t.Errorf("status = %q, want error (resumed → error)", got.Status)
		}
		if !strings.Contains(got.Error, "zombie") {
			t.Errorf("error = %q, want contain 'zombie'", got.Error)
		}
		if got.FinishedAt == "" {
			t.Errorf("finished_at = empty, want non-empty")
		}
	})

	t.Run("fresh_running_untouched", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "test.db")
		seedRun(dbPath, "fresh-run", "running", 1*time.Hour)

		n := resetZombieWorkflowRuns(dbPath, 4*time.Hour)
		if n != 0 {
			t.Fatalf("return = %d, want 0", n)
		}

		got, err := queryWorkflowRunByID(dbPath, "fresh-run")
		if err != nil {
			t.Fatalf("queryWorkflowRunByID: %v", err)
		}
		if got.Status != "running" {
			t.Errorf("status = %q, want running (unchanged)", got.Status)
		}
		if got.FinishedAt != "" {
			t.Errorf("finished_at = %q, want empty", got.FinishedAt)
		}
	})

	t.Run("success_status_untouched", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "test.db")
		seedRun(dbPath, "done-run", "success", 5*time.Hour)

		n := resetZombieWorkflowRuns(dbPath, 4*time.Hour)
		if n != 0 {
			t.Fatalf("return = %d, want 0 (success is terminal)", n)
		}

		got, err := queryWorkflowRunByID(dbPath, "done-run")
		if err != nil {
			t.Fatalf("queryWorkflowRunByID: %v", err)
		}
		if got.Status != "success" {
			t.Errorf("status = %q, want success (unchanged)", got.Status)
		}
	})

	t.Run("empty_dbPath_guard", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panicked: %v", r)
			}
		}()
		if n := resetZombieWorkflowRuns("", 4*time.Hour); n != 0 {
			t.Errorf("return = %d, want 0", n)
		}
	})
}

func TestWorkflowDirCreation(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	wfDir := workflowDir(cfg)
	expected := filepath.Join(dir, "workflows")
	if wfDir != expected {
		t.Errorf("workflowDir = %q, want %q", wfDir, expected)
	}

	// ensureWorkflowDir should create it.
	if err := ensureWorkflowDir(cfg); err != nil {
		t.Fatalf("ensureWorkflowDir: %v", err)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("directory not created: %v", err)
	}
}

// --- P6.3 Handoff Tests ---

func TestHandoffDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	h := Handoff{
		ID:            "h-001",
		WorkflowRunID: "run-001",
		FromAgent:      "翡翠",
		ToAgent:        "黒曜",
		FromStepID:    "research",
		ToStepID:      "implement",
		Context:       "Research results here",
		Instruction:   "Implement the solution",
		Status:        "pending",
		CreatedAt:     time.Now().Format(time.RFC3339),
	}

	if err := recordHandoff(dbPath, h); err != nil {
		t.Fatalf("recordHandoff: %v", err)
	}

	// Query by workflow run.
	handoffs, err := queryHandoffs(dbPath, "run-001")
	if err != nil {
		t.Fatalf("queryHandoffs: %v", err)
	}
	if len(handoffs) != 1 {
		t.Fatalf("expected 1 handoff, got %d", len(handoffs))
	}
	if handoffs[0].FromAgent != "翡翠" || handoffs[0].ToAgent != "黒曜" {
		t.Errorf("roles = %s→%s", handoffs[0].FromAgent, handoffs[0].ToAgent)
	}
	if handoffs[0].Status != "pending" {
		t.Errorf("status = %q, want pending", handoffs[0].Status)
	}

	// Update status.
	if err := updateHandoffStatus(dbPath, "h-001", "completed"); err != nil {
		t.Fatalf("updateHandoffStatus: %v", err)
	}
	handoffs2, _ := queryHandoffs(dbPath, "run-001")
	if len(handoffs2) != 1 || handoffs2[0].Status != "completed" {
		t.Errorf("status after update = %q, want completed", handoffs2[0].Status)
	}
}

func TestHandoffDBEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Query on empty DB should return nil, not error.
	handoffs, err := queryHandoffs(dbPath, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(handoffs) != 0 {
		t.Errorf("expected 0, got %d", len(handoffs))
	}

	// Empty dbPath.
	err = recordHandoff("", Handoff{ID: "x"})
	if err != nil {
		t.Errorf("expected nil for empty dbPath, got %v", err)
	}
}

func TestAgentMessageDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	msg := AgentMessage{
		WorkflowRunID: "run-001",
		FromAgent:      "翡翠",
		ToAgent:        "黒曜",
		Type:          "handoff",
		Content:       "Here are the research results for you to implement",
		RefID:         "h-001",
	}

	if err := sendAgentMessage(dbPath, msg); err != nil {
		t.Fatalf("sendAgentMessage: %v", err)
	}

	// Send a response.
	resp := AgentMessage{
		WorkflowRunID: "run-001",
		FromAgent:      "黒曜",
		ToAgent:        "翡翠",
		Type:          "response",
		Content:       "Implementation complete",
		RefID:         "h-001",
	}
	sendAgentMessage(dbPath, resp)

	// Query by workflow run.
	msgs, err := queryAgentMessages(dbPath, "run-001", "", 50)
	if err != nil {
		t.Fatalf("queryAgentMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Type != "handoff" {
		t.Errorf("first message type = %q, want handoff", msgs[0].Type)
	}
	if msgs[1].Type != "response" {
		t.Errorf("second message type = %q, want response", msgs[1].Type)
	}

	// Query by role.
	msgs2, err := queryAgentMessages(dbPath, "", "翡翠", 50)
	if err != nil {
		t.Fatalf("queryAgentMessages by role: %v", err)
	}
	if len(msgs2) != 2 {
		t.Errorf("expected 2 messages involving 翡翠, got %d", len(msgs2))
	}
}

func TestAgentMessageDBEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	msgs, err := queryAgentMessages(dbPath, "nope", "", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0, got %d", len(msgs))
	}

	// Empty dbPath.
	err = sendAgentMessage("", AgentMessage{ID: "x"})
	if err != nil {
		t.Errorf("expected nil for empty dbPath, got %v", err)
	}
}

func TestParseAutoDelegate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		role     string
	}{
		{
			name:     "single delegation",
			input:    `Here is my analysis. {"_delegate": {"role": "黒曜", "task": "implement the API endpoint", "reason": "requires coding"}}`,
			expected: 1,
			role:     "黒曜",
		},
		{
			name:     "no delegation markers",
			input:    `This is a normal response with no delegation.`,
			expected: 0,
		},
		{
			name: "multiple delegations",
			input: `Analysis complete.
{"_delegate": {"role": "黒曜", "task": "implement backend"}}
Also need:
{"_delegate": {"role": "琥珀", "task": "write documentation"}}`,
			expected: 2,
		},
		{
			name:     "malformed JSON",
			input:    `{"_delegate": {"role": "黒曜", "task": `,
			expected: 0,
		},
		{
			name:     "empty role",
			input:    `{"_delegate": {"role": "", "task": "do something"}}`,
			expected: 0,
		},
		{
			name:     "empty task",
			input:    `{"_delegate": {"role": "黒曜", "task": ""}}`,
			expected: 0,
		},
		{
			name:     "delegation in middle of text",
			input:    `First paragraph.\n\nI think we should delegate: {"_delegate": {"role": "翡翠", "task": "research this topic", "reason": "need more data"}}\n\nEnd of output.`,
			expected: 1,
			role:     "翡翠",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delegations := parseAutoDelegate(tt.input)
			if len(delegations) != tt.expected {
				t.Errorf("got %d delegations, want %d", len(delegations), tt.expected)
			}
			if tt.role != "" && len(delegations) > 0 {
				if delegations[0].Agent != tt.role {
					t.Errorf("role = %q, want %q", delegations[0].Agent, tt.role)
				}
			}
		})
	}
}

func TestParseAutoDelegateMaxLimit(t *testing.T) {
	// Build output with 5 delegations — should be capped at maxAutoDelegations (3).
	input := ""
	for i := 0; i < 5; i++ {
		input += `{"_delegate": {"role": "黒曜", "task": "task"}}` + "\n"
	}
	delegations := parseAutoDelegate(input)
	if len(delegations) > maxAutoDelegations {
		t.Errorf("got %d delegations, max should be %d", len(delegations), maxAutoDelegations)
	}
}

func TestFindMatchingBrace(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{`{"key": "value"}`, 15},
		{`{"nested": {"a": 1}}`, 19},
		{`{"str": "hello\"world"}`, 22},
		{`{`, -1},
		{`{}`, 1},
	}

	for _, tt := range tests {
		got := findMatchingBrace(tt.input)
		if got != tt.expected {
			t.Errorf("findMatchingBrace(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestBuildHandoffPrompt(t *testing.T) {
	// Both context and instruction.
	prompt := buildHandoffPrompt("research output here", "implement based on this")
	if !contains(prompt, "Handoff Context") {
		t.Error("expected Handoff Context header")
	}
	if !contains(prompt, "research output here") {
		t.Error("expected context content")
	}
	if !contains(prompt, "Instruction") {
		t.Error("expected Instruction header")
	}
	if !contains(prompt, "implement based on this") {
		t.Error("expected instruction content")
	}

	// Only instruction.
	prompt2 := buildHandoffPrompt("", "just do it")
	if !contains(prompt2, "Instruction") {
		t.Error("expected Instruction header for instruction-only case")
	}

	// Empty both.
	prompt3 := buildHandoffPrompt("", "")
	if prompt3 != "" {
		t.Errorf("expected empty prompt, got %q", prompt3)
	}
}

func TestWorkflowValidateHandoffStep(t *testing.T) {
	// Valid handoff.
	w := &Workflow{
		Name: "test-handoff",
		Steps: []WorkflowStep{
			{ID: "research", Agent: "翡翠", Prompt: "Research this"},
			{ID: "implement", Type: "handoff", HandoffFrom: "research", Agent: "黒曜",
				Prompt: "Implement based on research", DependsOn: []string{"research"}},
		},
	}
	errs := validateWorkflow(w)
	if len(errs) != 0 {
		t.Errorf("valid workflow got errors: %v", errs)
	}

	// Missing handoffFrom.
	w2 := &Workflow{
		Name: "test-handoff-bad",
		Steps: []WorkflowStep{
			{ID: "step1", Agent: "翡翠", Prompt: "Do something"},
			{ID: "step2", Type: "handoff", Agent: "黒曜"},
		},
	}
	errs2 := validateWorkflow(w2)
	found := false
	for _, e := range errs2 {
		if contains(e, "handoffFrom") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected handoffFrom error, got: %v", errs2)
	}

	// Missing agent.
	w3 := &Workflow{
		Name: "test-handoff-no-agent",
		Steps: []WorkflowStep{
			{ID: "step1", Agent: "翡翠", Prompt: "Do something"},
			{ID: "step2", Type: "handoff", HandoffFrom: "step1"},
		},
	}
	errs3 := validateWorkflow(w3)
	foundAgent := false
	for _, e := range errs3 {
		if contains(e, "target 'agent'") {
			foundAgent = true
		}
	}
	if !foundAgent {
		t.Errorf("expected agent error, got: %v", errs3)
	}

	// Unknown handoffFrom reference.
	w4 := &Workflow{
		Name: "test-handoff-bad-ref",
		Steps: []WorkflowStep{
			{ID: "step1", Agent: "翡翠", Prompt: "Do something"},
			{ID: "step2", Type: "handoff", HandoffFrom: "nonexistent", Agent: "黒曜"},
		},
	}
	errs4 := validateWorkflow(w4)
	foundRef := false
	for _, e := range errs4 {
		if contains(e, "unknown step") {
			foundRef = true
		}
	}
	if !foundRef {
		t.Errorf("expected unknown step error, got: %v", errs4)
	}
}

func TestRunHandoffStepSourceFailed(t *testing.T) {
	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: &Workflow{Name: "test", Steps: []WorkflowStep{{ID: "src", Agent: "翡翠"}}},
		run:      &WorkflowRun{ID: "run-1", StepResults: map[string]*StepRunResult{}},
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{
				"src": {Status: "error", Error: "provider error", Output: ""},
			},
			Env: map[string]string{},
		},
	}

	step := &WorkflowStep{
		ID:          "handoff-1",
		Type:        "handoff",
		HandoffFrom: "src",
		Agent:        "黒曜",
		Prompt:      "Implement this",
	}
	result := &StepRunResult{StepID: "handoff-1"}
	exec.runHandoffStep(context.Background(), step, result, exec.wCtx)

	if result.Status != "error" {
		t.Errorf("status = %q, want error (source failed)", result.Status)
	}
	if !contains(result.Error, "failed") {
		t.Errorf("error = %q, want contains 'failed'", result.Error)
	}
}

func TestRunHandoffStepSourceMissing(t *testing.T) {
	exec := &workflowExecutor{
		cfg:      &Config{},
		workflow: &Workflow{Name: "test"},
		run:      &WorkflowRun{ID: "run-1", StepResults: map[string]*StepRunResult{}},
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
	}

	step := &WorkflowStep{
		ID:          "handoff-1",
		Type:        "handoff",
		HandoffFrom: "nonexistent",
		Agent:        "黒曜",
	}
	result := &StepRunResult{StepID: "handoff-1"}
	exec.runHandoffStep(context.Background(), step, result, exec.wCtx)

	if result.Status != "error" {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !contains(result.Error, "no result") {
		t.Errorf("error = %q, want contains 'no result'", result.Error)
	}
}

func TestHandoffTablesIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Init multiple times should not panic.
	initHandoffTables(dbPath)
	initHandoffTables(dbPath)
	initHandoffTables(dbPath)

	// Empty path.
	initHandoffTables("")
}

func TestAgentMessageAutoID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	msg := AgentMessage{
		FromAgent: "翡翠",
		ToAgent:   "黒曜",
		Type:     "note",
		Content:  "test message",
	}

	err := sendAgentMessage(dbPath, msg)
	if err != nil {
		t.Fatalf("sendAgentMessage: %v", err)
	}

	// Should have auto-generated ID and timestamp.
	msgs, _ := queryAgentMessages(dbPath, "", "翡翠", 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ID == "" {
		t.Error("expected auto-generated ID")
	}
	if msgs[0].CreatedAt == "" {
		t.Error("expected auto-generated timestamp")
	}
}

// --- from workflow_dryrun_test.go ---

// TestDryRunMode verifies mode defaults to live when not specified.
func TestDryRunMode(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	wf := &Workflow{
		Name: "test-mode-default",
		Steps: []WorkflowStep{
			{ID: "s1", Type: "condition", If: "'yes' == 'yes'", Then: "s1"},
		},
	}

	state := newDispatchState()

	// No mode argument: should default to live.
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, nil)

	// Live mode: status should NOT have a prefix.
	if strings.HasPrefix(run.Status, "dry-run:") || strings.HasPrefix(run.Status, "shadow:") {
		t.Errorf("expected live mode (no prefix), got status=%q", run.Status)
	}
	if run.Status != "success" {
		t.Errorf("expected success, got status=%q", run.Status)
	}
}

// TestDryRunNoProviderCall verifies dry-run doesn't call provider for dispatch steps.
func TestDryRunNoProviderCall(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	wf := &Workflow{
		Name: "test-dry-dispatch",
		Steps: []WorkflowStep{
			{
				ID:     "analyze",
				Agent:   "翡翠",
				Prompt: "Analyze the Go codebase for potential improvements",
			},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, nil, WorkflowModeDryRun)

	// Should succeed without actually calling a provider.
	if !strings.HasPrefix(run.Status, "dry-run:") {
		t.Errorf("expected dry-run: prefix, got status=%q", run.Status)
	}

	sr := run.StepResults["analyze"]
	if sr == nil {
		t.Fatal("step result for 'analyze' is nil")
	}
	if sr.Status != "success" {
		t.Errorf("step status=%q, want success", sr.Status)
	}
	if !strings.Contains(sr.Output, "[DRY-RUN]") {
		t.Errorf("output should contain [DRY-RUN], got: %q", sr.Output)
	}
	if !strings.Contains(sr.Output, "step=analyze") {
		t.Errorf("output should contain step=analyze, got: %q", sr.Output)
	}
	if !strings.Contains(sr.Output, "role=翡翠") {
		t.Errorf("output should contain role=翡翠, got: %q", sr.Output)
	}
}

// TestDryRunEstimatedCost verifies cost estimation is populated in dry-run.
func TestDryRunEstimatedCost(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	wf := &Workflow{
		Name: "test-dry-cost",
		Steps: []WorkflowStep{
			{
				ID:     "step1",
				Prompt: "Write a comprehensive analysis of distributed systems",
			},
			{
				ID:        "step2",
				Prompt:    "Summarize the analysis from step1",
				DependsOn: []string{"step1"},
			},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, nil, WorkflowModeDryRun)

	// Both steps should have cost estimates.
	for _, stepID := range []string{"step1", "step2"} {
		sr := run.StepResults[stepID]
		if sr == nil {
			t.Fatalf("step result for %q is nil", stepID)
		}
		if sr.CostUSD <= 0 {
			t.Errorf("step %q: CostUSD=%f, want > 0", stepID, sr.CostUSD)
		}
		if !strings.Contains(sr.Output, "estimated_cost=$") {
			t.Errorf("step %q: output should contain estimated_cost, got: %q", stepID, sr.Output)
		}
	}

	// Total cost should be sum of step costs.
	if run.TotalCost <= 0 {
		t.Errorf("TotalCost=%f, want > 0", run.TotalCost)
	}
}

// TestDryRunConditionStep verifies conditions evaluate normally in dry-run.
func TestDryRunConditionStep(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	wf := &Workflow{
		Name:      "test-dry-condition",
		Variables: map[string]string{"env": "staging"},
		Steps: []WorkflowStep{
			{
				ID:   "check",
				Type: "condition",
				If:   "{{env}} == 'staging'",
				Then: "deploy",
				Else: "skip-deploy",
			},
			{
				ID:        "deploy",
				Prompt:    "Deploy to staging",
				DependsOn: []string{"check"},
			},
			{
				ID:        "skip-deploy",
				Prompt:    "Skip deployment",
				DependsOn: []string{"check"},
			},
		},
	}

	state := newDispatchState()
	vars := map[string]string{"env": "staging"}
	run := executeWorkflow(context.Background(), cfg, wf, vars, state, sem, nil, WorkflowModeDryRun)

	// Condition should evaluate correctly.
	condResult := run.StepResults["check"]
	if condResult == nil {
		t.Fatal("condition step result is nil")
	}
	if condResult.Status != "success" {
		t.Errorf("condition status=%q, want success", condResult.Status)
	}
	if condResult.Output != "deploy" {
		t.Errorf("condition output=%q, want 'deploy' (then branch)", condResult.Output)
	}

	// "skip-deploy" should be skipped (unchosen branch).
	skipResult := run.StepResults["skip-deploy"]
	if skipResult != nil && skipResult.Status != "skipped" && skipResult.Status != "pending" {
		// Note: the unchosen branch may be skipped by handleConditionResult.
		t.Logf("skip-deploy status=%q (expected skipped or pending)", skipResult.Status)
	}

	// "deploy" should have dry-run output.
	deployResult := run.StepResults["deploy"]
	if deployResult == nil {
		t.Fatal("deploy step result is nil")
	}
	if deployResult.Status != "success" {
		t.Errorf("deploy status=%q, want success", deployResult.Status)
	}
	if !strings.Contains(deployResult.Output, "[DRY-RUN]") {
		t.Errorf("deploy output should contain [DRY-RUN], got: %q", deployResult.Output)
	}
}

// TestDryRunSkillStep verifies skill steps return mock output in dry-run.
func TestDryRunSkillStep(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	wf := &Workflow{
		Name: "test-dry-skill",
		Steps: []WorkflowStep{
			{
				ID:    "run-lint",
				Type:  "skill",
				Skill: "golangci-lint",
			},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, nil, WorkflowModeDryRun)

	sr := run.StepResults["run-lint"]
	if sr == nil {
		t.Fatal("step result for 'run-lint' is nil")
	}
	if sr.Status != "success" {
		t.Errorf("status=%q, want success", sr.Status)
	}
	if !strings.Contains(sr.Output, "[DRY-RUN]") {
		t.Errorf("output should contain [DRY-RUN], got: %q", sr.Output)
	}
	if !strings.Contains(sr.Output, "golangci-lint") {
		t.Errorf("output should contain skill name, got: %q", sr.Output)
	}
}

// TestDryRunStatusPrefix verifies "dry-run:" prefix in recorded run status.
func TestDryRunStatusPrefix(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	cfg := &Config{
		BaseDir:               dir,
		HistoryDB:             dbPath,
		DefaultModel:          "sonnet",
		DefaultTimeout:        "5m",
		DefaultPermissionMode: "plan",
		DefaultWorkdir:        dir,
		DefaultProvider:       "claude",
	}
	sem := make(chan struct{}, 4)

	wf := &Workflow{
		Name: "test-prefix",
		Steps: []WorkflowStep{
			{ID: "s1", Prompt: "Hello"},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, nil, WorkflowModeDryRun)

	if !strings.HasPrefix(run.Status, "dry-run:") {
		t.Errorf("expected dry-run: prefix, got status=%q", run.Status)
	}

	// Verify DB record also has the prefix.
	dbRun, err := queryWorkflowRunByID(dbPath, run.ID)
	if err != nil {
		t.Fatalf("queryWorkflowRunByID: %v", err)
	}
	if !strings.HasPrefix(dbRun.Status, "dry-run:") {
		t.Errorf("DB record status=%q, want dry-run: prefix", dbRun.Status)
	}
}

// TestShadowStatusPrefix verifies "shadow:" prefix in recorded run status.
func TestShadowStatusPrefix(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	cfg := &Config{
		BaseDir:               dir,
		HistoryDB:             dbPath,
		DefaultModel:          "sonnet",
		DefaultTimeout:        "5m",
		DefaultPermissionMode: "plan",
		DefaultWorkdir:        dir,
		DefaultProvider:       "claude",
	}
	sem := make(chan struct{}, 4)

	// Use a condition step (which doesn't need a provider) to test shadow status prefix.
	wf := &Workflow{
		Name: "test-shadow-prefix",
		Steps: []WorkflowStep{
			{ID: "s1", Type: "condition", If: "'yes' == 'yes'", Then: "s1"},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, nil, WorkflowModeShadow)

	if !strings.HasPrefix(run.Status, "shadow:") {
		t.Errorf("expected shadow: prefix, got status=%q", run.Status)
	}

	// Verify DB record also has the prefix.
	dbRun, err := queryWorkflowRunByID(dbPath, run.ID)
	if err != nil {
		t.Fatalf("queryWorkflowRunByID: %v", err)
	}
	if !strings.HasPrefix(dbRun.Status, "shadow:") {
		t.Errorf("DB record status=%q, want shadow: prefix", dbRun.Status)
	}
}

// TestDryRunHandoffStep verifies handoff steps return estimated cost in dry-run.
func TestDryRunHandoffStep(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	exec := &workflowExecutor{
		cfg:      cfg,
		workflow: &Workflow{Name: "test-handoff-dry", Steps: []WorkflowStep{{ID: "src", Agent: "翡翠"}}},
		run:      &WorkflowRun{ID: "run-1", StepResults: map[string]*StepRunResult{}},
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{
				"src": {Status: "success", Output: "Research output from step 1"},
			},
			Env: map[string]string{},
		},
		mode: WorkflowModeDryRun,
		sem:  sem,
	}

	step := &WorkflowStep{
		ID:          "handoff-1",
		Type:        "handoff",
		HandoffFrom: "src",
		Agent:        "黒曜",
		Prompt:      "Implement based on research",
	}
	result := &StepRunResult{StepID: "handoff-1"}
	exec.runHandoffStepDryRun(step, result, exec.wCtx)

	if result.Status != "success" {
		t.Errorf("status=%q, want success", result.Status)
	}
	if !strings.Contains(result.Output, "[DRY-RUN]") {
		t.Errorf("output should contain [DRY-RUN], got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "handoff") {
		t.Errorf("output should contain 'handoff', got: %q", result.Output)
	}
	if result.CostUSD <= 0 {
		t.Errorf("CostUSD=%f, want > 0", result.CostUSD)
	}
}

// TestDryRunHandoffSourceFailed verifies handoff dry-run fails when source step failed.
func TestDryRunHandoffSourceFailed(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	exec := &workflowExecutor{
		cfg:      cfg,
		workflow: &Workflow{Name: "test"},
		run:      &WorkflowRun{ID: "run-1", StepResults: map[string]*StepRunResult{}},
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{
				"src": {Status: "error", Error: "provider error"},
			},
			Env: map[string]string{},
		},
		mode: WorkflowModeDryRun,
		sem:  sem,
	}

	step := &WorkflowStep{
		ID:          "handoff-1",
		Type:        "handoff",
		HandoffFrom: "src",
		Agent:        "黒曜",
	}
	result := &StepRunResult{StepID: "handoff-1"}
	exec.runHandoffStepDryRun(step, result, exec.wCtx)

	if result.Status != "error" {
		t.Errorf("status=%q, want error", result.Status)
	}
	if !strings.Contains(result.Error, "failed") {
		t.Errorf("error=%q, want contains 'failed'", result.Error)
	}
}

// TestDryRunMultiStepWorkflow verifies a multi-step workflow with dependencies.
func TestDryRunMultiStepWorkflow(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	wf := &Workflow{
		Name: "multi-step-dry",
		Steps: []WorkflowStep{
			{ID: "research", Prompt: "Research topic A", Agent: "翡翠"},
			{ID: "analyze", Prompt: "Analyze {{steps.research.output}}", Agent: "翡翠", DependsOn: []string{"research"}},
			{ID: "report", Prompt: "Write final report", Agent: "琥珀", DependsOn: []string{"analyze"}},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, nil, WorkflowModeDryRun)

	if !strings.HasPrefix(run.Status, "dry-run:success") {
		t.Errorf("expected dry-run:success, got %q", run.Status)
	}

	// All steps should complete successfully.
	for _, stepID := range []string{"research", "analyze", "report"} {
		sr := run.StepResults[stepID]
		if sr == nil {
			t.Fatalf("step %q result is nil", stepID)
		}
		if sr.Status != "success" {
			t.Errorf("step %q status=%q, want success", stepID, sr.Status)
		}
	}

	// Total cost should be sum of all steps.
	expectedMin := run.StepResults["research"].CostUSD +
		run.StepResults["analyze"].CostUSD +
		run.StepResults["report"].CostUSD
	if run.TotalCost < expectedMin*0.99 { // allow small floating point diff
		t.Errorf("TotalCost=%f, expected >= %f", run.TotalCost, expectedMin)
	}
}

// TestDryRunDuration verifies dry-run completes quickly (no provider wait).
func TestDryRunDuration(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	wf := &Workflow{
		Name: "test-dry-fast",
		Steps: []WorkflowStep{
			{ID: "s1", Prompt: "Step 1"},
			{ID: "s2", Prompt: "Step 2"},
			{ID: "s3", Prompt: "Step 3"},
		},
	}

	state := newDispatchState()
	start := time.Now()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, nil, WorkflowModeDryRun)
	elapsed := time.Since(start)

	// Dry-run should complete in well under 5 seconds (no provider calls).
	if elapsed > 5*time.Second {
		t.Errorf("dry-run took %v, expected < 5s", elapsed)
	}

	if !strings.HasPrefix(run.Status, "dry-run:") {
		t.Errorf("status=%q, expected dry-run: prefix", run.Status)
	}
}

// TestWorkflowRunModeConstants verifies the mode constants are correct.
func TestWorkflowRunModeConstants(t *testing.T) {
	if WorkflowModeLive != "live" {
		t.Errorf("WorkflowModeLive=%q, want 'live'", WorkflowModeLive)
	}
	if WorkflowModeDryRun != "dry-run" {
		t.Errorf("WorkflowModeDryRun=%q, want 'dry-run'", WorkflowModeDryRun)
	}
	if WorkflowModeShadow != "shadow" {
		t.Errorf("WorkflowModeShadow=%q, want 'shadow'", WorkflowModeShadow)
	}
}

// TestDryRunEmptyMode verifies empty mode defaults to live.
func TestDryRunEmptyMode(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)

	wf := &Workflow{
		Name: "test-empty-mode",
		Steps: []WorkflowStep{
			{ID: "s1", Type: "condition", If: "'yes' == 'yes'", Then: "s1"},
		},
	}

	state := newDispatchState()

	// Passing empty string should default to live.
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, nil, "")
	if strings.HasPrefix(run.Status, "dry-run:") || strings.HasPrefix(run.Status, "shadow:") {
		t.Errorf("empty mode should default to live, got status=%q", run.Status)
	}
}

// --- from workflow_external_test.go ---

// 1. TestExtractJSONPath — nested objects, array index, bool, float, missing key, invalid JSON
func TestExtractJSONPath(t *testing.T) {
	json := `{"name":"test","data":{"status":"ok","count":42,"active":true,"items":[{"id":"a"},{"id":"b"}]}}`

	tests := []struct {
		path string
		want string
	}{
		{"name", "test"},
		{"data.status", "ok"},
		{"data.count", "42"},
		{"data.active", "true"},
		{"data.items.0.id", "a"},
		{"data.items.1.id", "b"},
		{"data.items.2.id", ""},  // out of range
		{"missing", ""},
		{"data.missing.deep", ""},
	}

	for _, tt := range tests {
		got := extractJSONPath(json, tt.path)
		if got != tt.want {
			t.Errorf("extractJSONPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}

	// Invalid JSON.
	if got := extractJSONPath("not json", "key"); got != "" {
		t.Errorf("extractJSONPath(invalid) = %q, want empty", got)
	}

	// Empty inputs.
	if got := extractJSONPath("", "key"); got != "" {
		t.Errorf("extractJSONPath(empty json) = %q, want empty", got)
	}
	if got := extractJSONPath(`{"a":"b"}`, ""); got != "" {
		t.Errorf("extractJSONPath(empty path) = %q, want empty", got)
	}
}

// 2. TestApplyResponseMapping — with mapping, without, empty body
func TestApplyResponseMapping(t *testing.T) {
	body := `{"data":{"object":{"id":"ref_123","status":"succeeded"}}}`

	// With DataPath mapping.
	mapping := &ResponseMapping{DataPath: "data.object"}
	got := applyResponseMapping(body, mapping)
	if !strings.Contains(got, "ref_123") {
		t.Errorf("applyResponseMapping with DataPath: got %q, want to contain ref_123", got)
	}

	// Without mapping — returns full body.
	got = applyResponseMapping(body, nil)
	if got != body {
		t.Errorf("applyResponseMapping nil mapping: got %q, want full body", got)
	}

	// Empty body.
	got = applyResponseMapping("", mapping)
	if got != "" {
		t.Errorf("applyResponseMapping empty body: got %q, want empty", got)
	}

	// Mapping with empty DataPath — returns full body.
	got = applyResponseMapping(body, &ResponseMapping{})
	if got != body {
		t.Errorf("applyResponseMapping empty DataPath: got %q, want full body", got)
	}
}

// 3. TestCallbackManagerRegisterDeliver — basic register → deliver → channel receives
func TestCallbackManagerRegisterDeliver(t *testing.T) {
	cm := newCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := cm.Register("test-key", ctx, "single")
	if ch == nil {
		t.Fatal("Register returned nil")
	}

	if !cm.HasChannel("test-key") {
		t.Error("HasChannel should return true")
	}

	result := CallbackResult{Status: 200, Body: `{"ok":true}`, ContentType: "application/json"}
	if cm.Deliver("test-key", result) != DeliverOK {
		t.Error("Deliver should return DeliverOK")
	}

	select {
	case received := <-ch:
		if received.Body != `{"ok":true}` {
			t.Errorf("received body = %q, want {\"ok\":true}", received.Body)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for callback")
	}
}

// 4. TestCallbackManagerCollision — same key twice → nil
func TestCallbackManagerCollision(t *testing.T) {
	cm := newCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1 := cm.Register("dup-key", ctx, "single")
	if ch1 == nil {
		t.Fatal("first Register returned nil")
	}

	ch2 := cm.Register("dup-key", ctx, "single")
	if ch2 != nil {
		t.Error("second Register should return nil (collision)")
	}
}

// 5. TestCallbackManagerCapacity — 1001st → nil
func TestCallbackManagerCapacity(t *testing.T) {
	cm := newCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		ch := cm.Register(key, ctx, "single")
		if ch == nil {
			t.Fatalf("Register failed at key %d", i)
		}
	}

	ch := cm.Register("key-overflow", ctx, "single")
	if ch != nil {
		t.Error("1001st Register should return nil (capacity)")
	}
}

// 6. TestCallbackManagerUnregisterSafe — double unregister no panic
func TestCallbackManagerUnregisterSafe(t *testing.T) {
	cm := newCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cm.Register("safe-key", ctx, "single")

	// First unregister.
	cm.Unregister("safe-key")

	// Second unregister should not panic.
	cm.Unregister("safe-key")

	// Unregister non-existent key.
	cm.Unregister("nonexistent")

	if cm.HasChannel("safe-key") {
		t.Error("HasChannel should return false after unregister")
	}
}

// 7. TestCallbackManagerContextCleanup — cancel ctx → channel removed
func TestCallbackManagerContextCleanup(t *testing.T) {
	cm := newCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())

	cm.Register("ctx-key", ctx, "single")
	if !cm.HasChannel("ctx-key") {
		t.Fatal("channel should exist before cancel")
	}

	cancel()
	// Wait for cleanup goroutine.
	time.Sleep(50 * time.Millisecond)

	if cm.HasChannel("ctx-key") {
		t.Error("channel should be removed after context cancel")
	}
}

// 8. TestResolveTemplateWithFields — {{steps.id.output.field}} with JSON output
func TestResolveTemplateWithFields(t *testing.T) {
	exec := &workflowExecutor{
		wCtx: &WorkflowContext{
			Input: map[string]string{"name": "world"},
			Steps: map[string]*WorkflowStepResult{
				"step1": {Output: `{"result":"hello","count":5}`, Status: "success"},
			},
			Env: map[string]string{},
		},
	}

	tests := []struct {
		tmpl string
		want string
	}{
		{"Hello {{name}}", "Hello world"},
		{"Status: {{steps.step1.status}}", "Status: success"},
		{"Result: {{steps.step1.output.result}}", "Result: hello"},
		{"Count: {{steps.step1.output.count}}", "Count: 5"},
		{"Missing: {{steps.step1.output.missing}}", "Missing: "},
		{"No step: {{steps.nope.output.x}}", "No step: "},
	}

	for _, tt := range tests {
		got := exec.resolveTemplateWithFields(tt.tmpl)
		if got != tt.want {
			t.Errorf("resolveTemplateWithFields(%q) = %q, want %q", tt.tmpl, got, tt.want)
		}
	}
}

// 9. TestResolveTemplateXMLEscaped — entity escaping
func TestResolveTemplateXMLEscaped(t *testing.T) {
	exec := &workflowExecutor{
		wCtx: &WorkflowContext{
			Input: map[string]string{"val": "a<b&c"},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
	}

	got := exec.resolveTemplateXMLEscaped("Value: {{val}}")
	if !strings.Contains(got, "&lt;") || !strings.Contains(got, "&amp;") {
		t.Errorf("resolveTemplateXMLEscaped should escape XML entities, got %q", got)
	}
}

// 10. TestIsValidCallbackKey — valid/invalid formats
func TestIsValidCallbackKey(t *testing.T) {
	valid := []string{"abc", "test-key", "ocr-123_456", "a.b.c", "A1"}
	for _, k := range valid {
		if !isValidCallbackKey(k) {
			t.Errorf("isValidCallbackKey(%q) = false, want true", k)
		}
	}

	invalid := []string{"", "-starts-dash", ".starts-dot", "has space", "has/slash", strings.Repeat("a", 257)}
	for _, k := range invalid {
		if isValidCallbackKey(k) {
			t.Errorf("isValidCallbackKey(%q) = true, want false", k)
		}
	}
}

// 11. TestCallbackDBRoundTrip — record → query → markDelivered → isDelivered
func TestCallbackDBRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Init table.
	initCallbackTable(dbPath)

	// Record.
	recordPendingCallback(dbPath, "db-key-1", "run-1", "step-1", "single", "bearer",
		"https://example.com", `{"test":true}`, "2026-12-31 00:00:00")

	// Query.
	rec := queryPendingCallbackByKey(dbPath, "db-key-1")
	if rec == nil {
		t.Fatal("queryPendingCallbackByKey returned nil")
	}
	if rec.RunID != "run-1" || rec.StepID != "step-1" || rec.Mode != "single" {
		t.Errorf("unexpected record: %+v", rec)
	}

	// Query waiting.
	rec2 := queryPendingCallback(dbPath, "db-key-1")
	if rec2 == nil {
		t.Fatal("queryPendingCallback returned nil for waiting record")
	}

	// Mark delivered.
	markCallbackDelivered(dbPath, "db-key-1", 0, CallbackResult{Status: 200, Body: `{"done":true}`})

	// isDelivered.
	if !isCallbackDelivered(dbPath, "db-key-1", 0) {
		t.Error("isCallbackDelivered should return true after delivery")
	}

	// queryPendingCallback should return nil now (not waiting anymore).
	rec3 := queryPendingCallback(dbPath, "db-key-1")
	if rec3 != nil {
		t.Error("queryPendingCallback should return nil after delivery")
	}
}

// 12. TestValidateExternalStep — all validation rules
func TestValidateExternalStep(t *testing.T) {
	allIDs := map[string]bool{"s1": true}

	// Valid external step.
	valid := WorkflowStep{ID: "s1", Type: "external", ExternalURL: "https://example.com"}
	errs := validateStep(valid, allIDs)
	if len(errs) > 0 {
		t.Errorf("valid external step has errors: %v", errs)
	}

	// Mutual exclusion: both externalBody and externalRawBody.
	mutual := WorkflowStep{
		ID: "s1", Type: "external",
		ExternalBody:    map[string]string{"a": "b"},
		ExternalRawBody: "<xml/>",
	}
	errs = validateStep(mutual, allIDs)
	if len(errs) == 0 {
		t.Error("should error on both externalBody and externalRawBody")
	}

	// Invalid callbackMode.
	badMode := WorkflowStep{ID: "s1", Type: "external", CallbackMode: "invalid"}
	errs = validateStep(badMode, allIDs)
	if len(errs) == 0 {
		t.Error("should error on invalid callbackMode")
	}

	// Invalid callbackAuth.
	badAuth := WorkflowStep{ID: "s1", Type: "external", CallbackAuth: "invalid"}
	errs = validateStep(badAuth, allIDs)
	if len(errs) == 0 {
		t.Error("should error on invalid callbackAuth")
	}

	// Invalid onTimeout.
	badTimeout := WorkflowStep{ID: "s1", Type: "external", OnTimeout: "retry"}
	errs = validateStep(badTimeout, allIDs)
	if len(errs) == 0 {
		t.Error("should error on invalid onTimeout")
	}
}

// 13. TestHttpPostWithRetry — httptest.NewServer success/retry/fail
func TestHttpPostWithRetry(t *testing.T) {
	// Success case.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	resp, err := httpPostWithRetry(context.Background(), ts.URL, "application/json", nil, `{"test":true}`, 0)
	if err != nil {
		t.Fatalf("success case failed: %v", err)
	}
	resp.Body.Close()

	// Retry then succeed.
	attempts := 0
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(500)
			w.Write([]byte("server error"))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer ts2.Close()

	resp, err = httpPostWithRetry(context.Background(), ts2.URL, "application/json", nil, `{}`, 3)
	if err != nil {
		t.Fatalf("retry case failed: %v", err)
	}
	resp.Body.Close()
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}

	// All retries fail.
	ts3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("always fail"))
	}))
	defer ts3.Close()

	_, err = httpPostWithRetry(context.Background(), ts3.URL, "application/json", nil, `{}`, 1)
	if err == nil {
		t.Error("expected error when all retries fail")
	}
}

// 14. TestRunExternalStepDryRun — dry-run output format
func TestRunExternalStepDryRun(t *testing.T) {
	exec := &workflowExecutor{
		mode: WorkflowModeDryRun,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			StepResults: map[string]*StepRunResult{},
		},
	}

	step := &WorkflowStep{
		ID:           "ext1",
		Type:         "external",
		ExternalURL:  "https://example.com/api",
		CallbackMode: "single",
	}
	result := &StepRunResult{StepID: "ext1"}

	exec.runStepOnce(context.Background(), step, result)

	if result.Status != "success" {
		t.Errorf("dry-run status = %q, want success", result.Status)
	}
	if !strings.Contains(result.Output, "DRY-RUN") {
		t.Errorf("dry-run output should contain DRY-RUN, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "https://example.com/api") {
		t.Errorf("dry-run output should contain URL, got %q", result.Output)
	}
}

// 15. TestDeliverConcurrentUnregister — verify recover() prevents panic on concurrent close.
func TestDeliverConcurrentUnregister(t *testing.T) {
	cm := newCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := cm.Register("race-key", ctx, "single")
	if ch == nil {
		t.Fatal("Register returned nil")
	}

	// Unregister concurrently while delivering.
	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.Unregister("race-key")
	}()
	<-done

	// Deliver after channel is closed — should not panic, should return DeliverNoEntry.
	dr := cm.Deliver("race-key", CallbackResult{Body: "test"})
	if dr != DeliverNoEntry {
		t.Errorf("Deliver after Unregister = %d, want DeliverNoEntry", dr)
	}
}

// 16. TestDeliverAndSeqStreaming — verify atomic seq allocation for streaming.
func TestDeliverAndSeqStreaming(t *testing.T) {
	cm := newCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := cm.Register("stream-key", ctx, "streaming")
	if ch == nil {
		t.Fatal("Register returned nil")
	}

	// Deliver 3 streaming callbacks.
	for i := 0; i < 3; i++ {
		out := cm.DeliverAndSeq("stream-key", CallbackResult{Body: fmt.Sprintf("msg-%d", i)})
		if out.Result != DeliverOK {
			t.Fatalf("DeliverAndSeq %d: result = %d, want DeliverOK", i, out.Result)
		}
		if out.Seq != i {
			t.Errorf("DeliverAndSeq %d: seq = %d, want %d", i, out.Seq, i)
		}
	}

	// Drain channel.
	for i := 0; i < 3; i++ {
		select {
		case r := <-ch:
			if r.Body != fmt.Sprintf("msg-%d", i) {
				t.Errorf("received %q, want msg-%d", r.Body, i)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout draining channel")
		}
	}
}

// 17. TestSetSeqAfterReplay — verify seq counter updated after replay.
func TestSetSeqAfterReplay(t *testing.T) {
	cm := newCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := cm.Register("replay-key", ctx, "streaming")
	if ch == nil {
		t.Fatal("Register returned nil")
	}

	// Simulate replay of 3 accumulated results.
	replayed := []CallbackResult{
		{Body: "r0"}, {Body: "r1"}, {Body: "r2"},
	}
	cm.ReplayAccumulated("replay-key", replayed)
	cm.SetSeq("replay-key", len(replayed))

	// New delivery should start at seq=3.
	out := cm.DeliverAndSeq("replay-key", CallbackResult{Body: "new"})
	if out.Seq != 3 {
		t.Errorf("seq after replay = %d, want 3", out.Seq)
	}

	// Drain channel.
	for i := 0; i < 4; i++ {
		<-ch
	}
}

// 18. TestStreamingAccumulateNonJSON — verify non-JSON bodies are safely wrapped.
func TestStreamingAccumulateNonJSON(t *testing.T) {
	// Simulate accumulate with mixed JSON and non-JSON bodies.
	bodies := []string{`{"ok":true}`, "plain text", `{"count":42}`}

	var parts []string
	for _, b := range bodies {
		if !json.Valid([]byte(b)) {
			marshaled, _ := json.Marshal(b)
			b = string(marshaled)
		}
		parts = append(parts, b)
	}
	output := "[" + strings.Join(parts, ",") + "]"

	// Should be valid JSON.
	if !json.Valid([]byte(output)) {
		t.Errorf("accumulated output is not valid JSON: %s", output)
	}

	// Verify structure.
	var arr []any
	if err := json.Unmarshal([]byte(output), &arr); err != nil {
		t.Fatalf("failed to parse accumulated JSON: %v", err)
	}
	if len(arr) != 3 {
		t.Errorf("expected 3 elements, got %d", len(arr))
	}
	// Second element should be a string "plain text".
	if s, ok := arr[1].(string); !ok || s != "plain text" {
		t.Errorf("element 1 = %v, want string 'plain text'", arr[1])
	}
}

// TestParseDurationWithDays tests the day-suffix duration parser.
func TestParseDurationWithDays(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"5m", 5 * time.Minute, false},
		{"1h", time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"31d", 0, true}, // over 30d limit
		{"-1d", 0, true}, // negative
		{"abc", 0, true}, // invalid
	}

	for _, tt := range tests {
		got, err := parseDurationWithDays(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseDurationWithDays(%q) should error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDurationWithDays(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseDurationWithDays(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- from workflow_trigger_test.go ---

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

	if !expr.Matches(now) {
		t.Errorf("expected cron %q to match current time %v", cron, now)
	}
}

// TODO: TestTriggerCooldown removed — uses unexported WorkflowTriggerEngine fields


func TestTriggerEnabled(t *testing.T) {
	// nil Enabled -> should be enabled (default).
	t1 := WorkflowTriggerConfig{Name: "t1"}
	if !t1.IsEnabled() {
		t.Error("expected nil Enabled to default to true")
	}

	// Explicit true.
	boolTrue := true
	t2 := WorkflowTriggerConfig{Name: "t2", Enabled: &boolTrue}
	if !t2.IsEnabled() {
		t.Error("expected Enabled=true to be enabled")
	}

	// Explicit false.
	boolFalse := false
	t3 := WorkflowTriggerConfig{Name: "t3", Enabled: &boolFalse}
	if t3.IsEnabled() {
		t.Error("expected Enabled=false to be disabled")
	}
}

func TestToolCallStep(t *testing.T) {
	// Create a minimal config with a tool registry and a mock tool.
	cfg := &Config{}
	cfg.Runtime.ToolRegistry = newEmptyRegistry()

	// Register a mock tool.
	cfg.Runtime.ToolRegistry.(*ToolRegistry).Register(&ToolDef{
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
	cfg := &Config{}
	cfg.Runtime.ToolRegistry = newEmptyRegistry()

	cfg.Runtime.ToolRegistry.(*ToolRegistry).Register(&ToolDef{
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
		BaseDir: tmpDir,
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

// --- from workflow_viz_test.go ---

func TestBuildStepSummaries(t *testing.T) {
	steps := []WorkflowStep{
		{ID: "a", Agent: "翡翠", DependsOn: nil},
		{ID: "b", Type: "handoff", Agent: "黒曜", DependsOn: []string{"a"}},
	}
	summaries := buildStepSummaries(steps)
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	// Check first step.
	if summaries[0]["id"] != "a" {
		t.Errorf("expected id=a, got %v", summaries[0]["id"])
	}
	if summaries[0]["role"] != "翡翠" {
		t.Errorf("expected role=翡翠, got %v", summaries[0]["role"])
	}
	// Default type should be "dispatch".
	if summaries[0]["type"] != "dispatch" {
		t.Errorf("expected type=dispatch, got %v", summaries[0]["type"])
	}
	// No dependencies (nil or empty slice).
	deps0, _ := summaries[0]["dependsOn"].([]string)
	if len(deps0) != 0 {
		t.Errorf("expected empty dependsOn, got %v", summaries[0]["dependsOn"])
	}

	// Check second step has explicit type and dependency.
	if summaries[1]["id"] != "b" {
		t.Errorf("expected id=b, got %v", summaries[1]["id"])
	}
	if summaries[1]["type"] != "handoff" {
		t.Errorf("expected type=handoff, got %v", summaries[1]["type"])
	}
	if summaries[1]["role"] != "黒曜" {
		t.Errorf("expected role=黒曜, got %v", summaries[1]["role"])
	}
	deps, ok := summaries[1]["dependsOn"].([]string)
	if !ok || len(deps) != 1 || deps[0] != "a" {
		t.Errorf("expected dependsOn=[a], got %v", summaries[1]["dependsOn"])
	}
}

func TestBuildStepSummariesEmpty(t *testing.T) {
	summaries := buildStepSummaries(nil)
	if summaries != nil {
		t.Errorf("expected nil for empty input, got %v", summaries)
	}
}

func TestBuildStepSummariesCondition(t *testing.T) {
	steps := []WorkflowStep{
		{ID: "check", Type: "condition", If: "steps.a.status == 'success'", Then: "yes", Else: "no"},
	}
	summaries := buildStepSummaries(steps)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0]["type"] != "condition" {
		t.Errorf("expected type=condition, got %v", summaries[0]["type"])
	}
}

// =============================================================================
// Merged from workflow_test.go
// =============================================================================

func TestWorkflowLoadSave(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	w := &Workflow{
		Name: "test-pipeline",
		Steps: []WorkflowStep{
			{ID: "step1", Prompt: "hello"},
			{ID: "step2", Prompt: "world", DependsOn: []string{"step1"}},
		},
		Variables: map[string]string{"input": "test"},
		Timeout:   "10m",
	}

	// Save.
	if err := saveWorkflow(cfg, w); err != nil {
		t.Fatalf("saveWorkflow: %v", err)
	}

	// Check file exists.
	path := filepath.Join(dir, "workflows", "test-pipeline.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Load back.
	loaded, err := loadWorkflowByName(cfg, "test-pipeline")
	if err != nil {
		t.Fatalf("loadWorkflowByName: %v", err)
	}
	if loaded.Name != "test-pipeline" {
		t.Errorf("name = %q, want test-pipeline", loaded.Name)
	}
	if len(loaded.Steps) != 2 {
		t.Errorf("steps = %d, want 2", len(loaded.Steps))
	}
	if loaded.Variables["input"] != "test" {
		t.Errorf("variable input = %q, want test", loaded.Variables["input"])
	}
}

func TestWorkflowList(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	// Empty dir.
	wfs, err := listWorkflows(cfg)
	if err != nil {
		t.Fatalf("listWorkflows empty: %v", err)
	}
	if len(wfs) != 0 {
		t.Errorf("expected 0 workflows, got %d", len(wfs))
	}

	// Save two workflows.
	saveWorkflow(cfg, &Workflow{Name: "wf-a", Steps: []WorkflowStep{{ID: "s1", Prompt: "x"}}})
	saveWorkflow(cfg, &Workflow{Name: "wf-b", Steps: []WorkflowStep{{ID: "s1", Prompt: "y"}}})

	wfs, err = listWorkflows(cfg)
	if err != nil {
		t.Fatalf("listWorkflows: %v", err)
	}
	if len(wfs) != 2 {
		t.Errorf("expected 2 workflows, got %d", len(wfs))
	}
}

func TestWorkflowDelete(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{BaseDir: dir}

	saveWorkflow(cfg, &Workflow{Name: "to-delete", Steps: []WorkflowStep{{ID: "s1", Prompt: "x"}}})

	if err := deleteWorkflow(cfg, "to-delete"); err != nil {
		t.Fatalf("deleteWorkflow: %v", err)
	}

	if _, err := loadWorkflowByName(cfg, "to-delete"); err == nil {
		t.Error("expected error loading deleted workflow")
	}

	// Delete non-existent.
	if err := deleteWorkflow(cfg, "nope"); err == nil {
		t.Error("expected error deleting non-existent workflow")
	}
}

func TestValidateWorkflowBasic(t *testing.T) {
	// Valid workflow.
	w := &Workflow{
		Name: "valid",
		Steps: []WorkflowStep{
			{ID: "a", Prompt: "do A"},
			{ID: "b", Prompt: "do B", DependsOn: []string{"a"}},
		},
	}
	errs := validateWorkflow(w)
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidateWorkflowMissingName(t *testing.T) {
	w := &Workflow{Steps: []WorkflowStep{{ID: "a", Prompt: "x"}}}
	errs := validateWorkflow(w)
	if len(errs) == 0 {
		t.Error("expected error for missing name")
	}
}

func TestValidateWorkflowInvalidName(t *testing.T) {
	w := &Workflow{Name: "bad name!", Steps: []WorkflowStep{{ID: "a", Prompt: "x"}}}
	errs := validateWorkflow(w)
	hasNameErr := false
	for _, e := range errs {
		if contains(e, "invalid workflow name") {
			hasNameErr = true
		}
	}
	if !hasNameErr {
		t.Errorf("expected invalid name error, got: %v", errs)
	}
}

func TestValidateWorkflowNoSteps(t *testing.T) {
	w := &Workflow{Name: "empty"}
	errs := validateWorkflow(w)
	hasStepErr := false
	for _, e := range errs {
		if contains(e, "at least one step") {
			hasStepErr = true
		}
	}
	if !hasStepErr {
		t.Errorf("expected step error, got: %v", errs)
	}
}

func TestValidateWorkflowDuplicateIDs(t *testing.T) {
	w := &Workflow{
		Name: "dup",
		Steps: []WorkflowStep{
			{ID: "x", Prompt: "a"},
			{ID: "x", Prompt: "b"},
		},
	}
	errs := validateWorkflow(w)
	hasDupErr := false
	for _, e := range errs {
		if contains(e, "duplicate step ID") {
			hasDupErr = true
		}
	}
	if !hasDupErr {
		t.Errorf("expected duplicate ID error, got: %v", errs)
	}
}

func TestValidateWorkflowBadDependency(t *testing.T) {
	w := &Workflow{
		Name: "bad-dep",
		Steps: []WorkflowStep{
			{ID: "a", Prompt: "x", DependsOn: []string{"nonexistent"}},
		},
	}
	errs := validateWorkflow(w)
	hasDepErr := false
	for _, e := range errs {
		if contains(e, "unknown step") {
			hasDepErr = true
		}
	}
	if !hasDepErr {
		t.Errorf("expected bad dependency error, got: %v", errs)
	}
}

func TestValidateWorkflowSelfDep(t *testing.T) {
	w := &Workflow{
		Name: "self-dep",
		Steps: []WorkflowStep{
			{ID: "a", Prompt: "x", DependsOn: []string{"a"}},
		},
	}
	errs := validateWorkflow(w)
	hasSelfErr := false
	for _, e := range errs {
		if contains(e, "depend on itself") {
			hasSelfErr = true
		}
	}
	if !hasSelfErr {
		t.Errorf("expected self-dependency error, got: %v", errs)
	}
}

func TestDetectCycle(t *testing.T) {
	// No cycle.
	steps := []WorkflowStep{
		{ID: "a"},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"b"}},
	}
	if c := detectCycle(steps); c != "" {
		t.Errorf("expected no cycle, got: %s", c)
	}

	// Cycle: a -> b -> c -> a
	steps = []WorkflowStep{
		{ID: "a", DependsOn: []string{"c"}},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"b"}},
	}
	if c := detectCycle(steps); c == "" {
		t.Error("expected cycle detection")
	}
}

func TestTopologicalSort(t *testing.T) {
	steps := []WorkflowStep{
		{ID: "c", DependsOn: []string{"a", "b"}},
		{ID: "a"},
		{ID: "b", DependsOn: []string{"a"}},
	}
	order := topologicalSort(steps)
	if len(order) != 3 {
		t.Fatalf("expected 3, got %d", len(order))
	}
	// a must come before b and c; b must come before c.
	idx := make(map[string]int)
	for i, id := range order {
		idx[id] = i
	}
	if idx["a"] >= idx["b"] {
		t.Errorf("a should come before b: %v", order)
	}
	if idx["b"] >= idx["c"] {
		t.Errorf("b should come before c: %v", order)
	}
}

func TestResolveTemplateInput(t *testing.T) {
	wCtx := &WorkflowContext{
		Input: map[string]string{"repo": "github.com/test", "branch": "main"},
		Steps: make(map[string]*WorkflowStepResult),
		Env:   make(map[string]string),
	}

	result := resolveTemplate("Clone {{repo}} on branch {{branch}}", wCtx)
	if result != "Clone github.com/test on branch main" {
		t.Errorf("got %q", result)
	}
}

func TestResolveTemplateStepOutput(t *testing.T) {
	wCtx := &WorkflowContext{
		Input: make(map[string]string),
		Steps: map[string]*WorkflowStepResult{
			"analyze": {Output: "looks good", Status: "success"},
		},
		Env: make(map[string]string),
	}

	result := resolveTemplate("Report: {{steps.analyze.output}}, status={{steps.analyze.status}}", wCtx)
	if result != "Report: looks good, status=success" {
		t.Errorf("got %q", result)
	}
}

func TestResolveTemplateEnv(t *testing.T) {
	wCtx := &WorkflowContext{
		Input: make(map[string]string),
		Steps: make(map[string]*WorkflowStepResult),
		Env:   map[string]string{"HOME": "/home/test"},
	}

	result := resolveTemplate("Home is {{env.HOME}}", wCtx)
	if result != "Home is /home/test" {
		t.Errorf("got %q", result)
	}
}

func TestResolveTemplateMissing(t *testing.T) {
	wCtx := &WorkflowContext{
		Input: make(map[string]string),
		Steps: make(map[string]*WorkflowStepResult),
		Env:   make(map[string]string),
	}

	// Missing vars resolve to empty string.
	result := resolveTemplate("{{missing}} and {{steps.x.output}}", wCtx)
	if result != " and " {
		t.Errorf("got %q", result)
	}
}

func TestEvalConditionEquals(t *testing.T) {
	wCtx := &WorkflowContext{
		Input: make(map[string]string),
		Steps: map[string]*WorkflowStepResult{
			"check": {Status: "success"},
		},
		Env: make(map[string]string),
	}

	if !evalCondition("{{steps.check.status}} == 'success'", wCtx) {
		t.Error("expected true for success == success")
	}
	if evalCondition("{{steps.check.status}} == 'error'", wCtx) {
		t.Error("expected false for success == error")
	}
}

func TestEvalConditionNotEquals(t *testing.T) {
	wCtx := &WorkflowContext{
		Input: make(map[string]string),
		Steps: map[string]*WorkflowStepResult{
			"check": {Status: "error"},
		},
		Env: make(map[string]string),
	}

	if !evalCondition("{{steps.check.status}} != 'success'", wCtx) {
		t.Error("expected true for error != success")
	}
}

func TestEvalConditionTruthy(t *testing.T) {
	wCtx := &WorkflowContext{
		Input: map[string]string{"flag": "true"},
		Steps: make(map[string]*WorkflowStepResult),
		Env:   make(map[string]string),
	}

	if !evalCondition("{{flag}}", wCtx) {
		t.Error("expected truthy for 'true'")
	}

	wCtx.Input["flag"] = ""
	if evalCondition("{{flag}}", wCtx) {
		t.Error("expected falsy for empty string")
	}

	wCtx.Input["flag"] = "false"
	if evalCondition("{{flag}}", wCtx) {
		t.Error("expected falsy for 'false'")
	}
}

func TestNewWorkflowContext(t *testing.T) {
	w := &Workflow{
		Variables: map[string]string{"a": "default", "b": "val"},
	}
	overrides := map[string]string{"a": "override"}
	wCtx := newWorkflowContext(w, overrides)

	if wCtx.Input["a"] != "override" {
		t.Errorf("expected override, got %q", wCtx.Input["a"])
	}
	if wCtx.Input["b"] != "val" {
		t.Errorf("expected val, got %q", wCtx.Input["b"])
	}
}

func TestGetStepByID(t *testing.T) {
	w := &Workflow{
		Name: "test",
		Steps: []WorkflowStep{
			{ID: "first", Prompt: "one"},
			{ID: "second", Prompt: "two"},
		},
	}
	s := getStepByID(w, "second")
	if s == nil || s.Prompt != "two" {
		t.Error("expected to find step 'second'")
	}
	if getStepByID(w, "nope") != nil {
		t.Error("expected nil for unknown step")
	}
}

func TestBuildStepTask(t *testing.T) {
	wCtx := &WorkflowContext{
		Input: map[string]string{"file": "main.go"},
		Steps: make(map[string]*WorkflowStepResult),
		Env:   make(map[string]string),
	}

	step := &WorkflowStep{
		ID:     "review",
		Agent:   "黒曜",
		Prompt: "Review {{file}}",
		Model:  "sonnet",
	}

	task := buildStepTask(step, wCtx, "code-review")
	if task.Name != "code-review/review" {
		t.Errorf("name = %q", task.Name)
	}
	if task.Prompt != "Review main.go" {
		t.Errorf("prompt = %q", task.Prompt)
	}
	if task.Agent != "黒曜" {
		t.Errorf("role = %q", task.Agent)
	}
	if task.Source != "workflow:code-review" {
		t.Errorf("source = %q", task.Source)
	}
}

func TestValidateStepTypes(t *testing.T) {
	allIDs := map[string]bool{"a": true, "b": true, "c": true}

	// Dispatch without prompt.
	errs := validateStep(WorkflowStep{ID: "a"}, allIDs)
	if len(errs) == 0 {
		t.Error("expected error for dispatch step without prompt")
	}

	// Skill without name.
	errs = validateStep(WorkflowStep{ID: "b", Type: "skill"}, allIDs)
	if len(errs) == 0 {
		t.Error("expected error for skill step without skill name")
	}

	// Condition without if.
	errs = validateStep(WorkflowStep{ID: "c", Type: "condition", Then: "a"}, allIDs)
	if len(errs) == 0 {
		t.Error("expected error for condition step without if expression")
	}

	// Unknown type.
	errs = validateStep(WorkflowStep{ID: "a", Type: "unknown"}, allIDs)
	hasTypeErr := false
	for _, e := range errs {
		if contains(e, "unknown type") {
			hasTypeErr = true
		}
	}
	if !hasTypeErr {
		t.Errorf("expected unknown type error, got: %v", errs)
	}
}

func TestValidateConditionRefs(t *testing.T) {
	allIDs := map[string]bool{"a": true, "b": true}

	// Valid condition.
	errs := validateStep(WorkflowStep{
		ID: "a", Type: "condition", If: "{{x}}", Then: "b",
	}, allIDs)
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}

	// Bad then ref.
	errs = validateStep(WorkflowStep{
		ID: "a", Type: "condition", If: "{{x}}", Then: "nope",
	}, allIDs)
	hasBadRef := false
	for _, e := range errs {
		if contains(e, "unknown step") {
			hasBadRef = true
		}
	}
	if !hasBadRef {
		t.Errorf("expected unknown step error, got: %v", errs)
	}
}

func TestWorkflowJSONRoundTrip(t *testing.T) {
	w := &Workflow{
		Name:        "pipeline",
		Description: "Test pipeline",
		Steps: []WorkflowStep{
			{ID: "analyze", Agent: "黒曜", Prompt: "analyze {{input}}"},
			{ID: "security", Agent: "黒曜", Prompt: "audit", DependsOn: []string{"analyze"}},
			{ID: "report", Agent: "琥珀", Prompt: "write report", DependsOn: []string{"analyze", "security"}},
		},
		Variables: map[string]string{"input": ""},
		Timeout:   "30m",
	}

	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var loaded Workflow
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if loaded.Name != w.Name {
		t.Errorf("name = %q, want %q", loaded.Name, w.Name)
	}
	if len(loaded.Steps) != 3 {
		t.Errorf("steps = %d, want 3", len(loaded.Steps))
	}
	if loaded.Steps[1].DependsOn[0] != "analyze" {
		t.Errorf("step[1] depends on %q, want analyze", loaded.Steps[1].DependsOn[0])
	}
}

func TestValidateParallelStep(t *testing.T) {
	allIDs := map[string]bool{"p": true}

	// Valid parallel.
	errs := validateStep(WorkflowStep{
		ID:   "p",
		Type: "parallel",
		Parallel: []WorkflowStep{
			{ID: "p1", Prompt: "task 1"},
			{ID: "p2", Prompt: "task 2"},
		},
	}, allIDs)
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}

	// Empty parallel.
	errs = validateStep(WorkflowStep{
		ID:   "p",
		Type: "parallel",
	}, allIDs)
	if len(errs) == 0 {
		t.Error("expected error for empty parallel")
	}
}

func TestValidateInvalidTimeout(t *testing.T) {
	w := &Workflow{
		Name:    "bad-timeout",
		Timeout: "notaduration",
		Steps:   []WorkflowStep{{ID: "a", Prompt: "x"}},
	}
	errs := validateWorkflow(w)
	hasTimeoutErr := false
	for _, e := range errs {
		if contains(e, "invalid timeout") {
			hasTimeoutErr = true
		}
	}
	if !hasTimeoutErr {
		t.Errorf("expected timeout error, got: %v", errs)
	}
}

func TestStepTypeDefault(t *testing.T) {
	s := &WorkflowStep{ID: "x", Prompt: "y"}
	if stepType(s) != "dispatch" {
		t.Errorf("expected dispatch, got %q", stepType(s))
	}
	s.Type = "skill"
	if stepType(s) != "skill" {
		t.Errorf("expected skill, got %q", stepType(s))
	}
}

// =============================================================================
// Human Gate Step Tests
// =============================================================================

// TestRunHumanStepDryRun — dry-run output format.
func TestRunHumanStepDryRun(t *testing.T) {
	exec := &workflowExecutor{
		mode: WorkflowModeDryRun,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			StepResults: map[string]*StepRunResult{},
		},
	}

	step := &WorkflowStep{
		ID:            "hg1",
		Type:          "human",
		HumanSubtype:  "approval",
		HumanAssignee: "takuma",
	}
	result := &StepRunResult{StepID: "hg1"}

	exec.runStepOnce(context.Background(), step, result)

	if result.Status != "success" {
		t.Errorf("dry-run status = %q, want success", result.Status)
	}
	if !strings.Contains(result.Output, "DRY-RUN") {
		t.Errorf("dry-run output should contain DRY-RUN, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "approval") {
		t.Errorf("dry-run output should contain subtype, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "takuma") {
		t.Errorf("dry-run output should contain assignee, got %q", result.Output)
	}
}

// TestRunHumanStepDryRunDefaultSubtype — default subtype is "approval".
func TestRunHumanStepDryRunDefaultSubtype(t *testing.T) {
	exec := &workflowExecutor{
		mode: WorkflowModeDryRun,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			StepResults: map[string]*StepRunResult{},
		},
	}

	step := &WorkflowStep{
		ID:   "hg2",
		Type: "human",
	}
	result := &StepRunResult{StepID: "hg2"}

	exec.runStepOnce(context.Background(), step, result)

	if result.Status != "success" {
		t.Errorf("status = %q, want success", result.Status)
	}
	if !strings.Contains(result.Output, "approval") {
		t.Errorf("should default to approval, got %q", result.Output)
	}
}

// TestHumanGateApprovalFlow — Register → Deliver("approved") → step success.
func TestHumanGateApprovalFlow(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)
	initCallbackTable(cfg.HistoryDB)
	initHumanGateTable(cfg.HistoryDB)

	// Create a CallbackManager with the test DB.
	cm := newCallbackManager(cfg.HistoryDB)
	oldMgr := callbackMgr
	callbackMgr = cm
	defer func() { callbackMgr = oldMgr }()

	exec := &workflowExecutor{
		cfg:  cfg,
		mode: WorkflowModeLive,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			ID:          "run-hg-approval",
			Status:      "running",
			StepResults: map[string]*StepRunResult{},
		},
		sem:      sem,
		childSem: make(chan struct{}, 1),
	}

	step := &WorkflowStep{
		ID:            "approve1",
		Type:          "human",
		HumanSubtype:  "approval",
		HumanPrompt:   "Please approve this deployment",
		HumanAssignee: "takuma",
		HumanTimeout:  "10s",
	}
	result := &StepRunResult{StepID: "approve1"}

	// Simulate human approval via callback in a goroutine.
	go func() {
		time.Sleep(100 * time.Millisecond)
		hgKey := fmt.Sprintf("hg-%s-%s", exec.run.ID, step.ID)
		body := `{"action":"approved","response":"looks good"}`
		cm.Deliver(hgKey, CallbackResult{Body: body})
	}()

	exec.runHumanStep(context.Background(), step, result)

	if result.Status != "success" {
		t.Errorf("status = %q, want success", result.Status)
	}
	if !strings.Contains(result.Output, "approved") {
		t.Errorf("output should contain 'approved', got %q", result.Output)
	}
}

// TestHumanGateRejectionFlow — Deliver("rejected") → step error.
func TestHumanGateRejectionFlow(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)
	initCallbackTable(cfg.HistoryDB)
	initHumanGateTable(cfg.HistoryDB)

	cm := newCallbackManager(cfg.HistoryDB)
	oldMgr := callbackMgr
	callbackMgr = cm
	defer func() { callbackMgr = oldMgr }()

	exec := &workflowExecutor{
		cfg:  cfg,
		mode: WorkflowModeLive,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			ID:          "run-hg-reject",
			Status:      "running",
			StepResults: map[string]*StepRunResult{},
		},
		sem:      sem,
		childSem: make(chan struct{}, 1),
	}

	step := &WorkflowStep{
		ID:            "reject1",
		Type:          "human",
		HumanSubtype:  "approval",
		HumanPrompt:   "Approve?",
		HumanAssignee: "takuma",
		HumanTimeout:  "10s",
	}
	result := &StepRunResult{StepID: "reject1"}

	go func() {
		time.Sleep(100 * time.Millisecond)
		hgKey := fmt.Sprintf("hg-%s-%s", exec.run.ID, step.ID)
		body := `{"action":"rejected","response":"not ready"}`
		cm.Deliver(hgKey, CallbackResult{Body: body})
	}()

	exec.runHumanStep(context.Background(), step, result)

	if result.Status != "error" {
		t.Errorf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Error, "rejected") {
		t.Errorf("error should contain 'rejected', got %q", result.Error)
	}
}

// TestHumanGateInputFlow — Deliver response → step output contains inputKey.
func TestHumanGateInputFlow(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)
	initCallbackTable(cfg.HistoryDB)
	initHumanGateTable(cfg.HistoryDB)

	cm := newCallbackManager(cfg.HistoryDB)
	oldMgr := callbackMgr
	callbackMgr = cm
	defer func() { callbackMgr = oldMgr }()

	exec := &workflowExecutor{
		cfg:  cfg,
		mode: WorkflowModeLive,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			ID:          "run-hg-input",
			Status:      "running",
			StepResults: map[string]*StepRunResult{},
		},
		sem:      sem,
		childSem: make(chan struct{}, 1),
	}

	step := &WorkflowStep{
		ID:            "input1",
		Type:          "human",
		HumanSubtype:  "input",
		HumanPrompt:   "Enter the target version",
		HumanInputKey: "version",
		HumanTimeout:  "10s",
	}
	result := &StepRunResult{StepID: "input1"}

	go func() {
		time.Sleep(100 * time.Millisecond)
		hgKey := fmt.Sprintf("hg-%s-%s", exec.run.ID, step.ID)
		body := `{"response":"v2.3.1"}`
		cm.Deliver(hgKey, CallbackResult{Body: body})
	}()

	exec.runHumanStep(context.Background(), step, result)

	if result.Status != "success" {
		t.Errorf("status = %q, want success", result.Status)
	}
	if !strings.Contains(result.Output, "version") || !strings.Contains(result.Output, "v2.3.1") {
		t.Errorf("output should contain inputKey and response, got %q", result.Output)
	}
}

// TestHumanGateTimeoutStop — timeout with onTimeout="stop" → step timeout.
func TestHumanGateTimeoutStop(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)
	initCallbackTable(cfg.HistoryDB)
	initHumanGateTable(cfg.HistoryDB)

	cm := newCallbackManager(cfg.HistoryDB)
	oldMgr := callbackMgr
	callbackMgr = cm
	defer func() { callbackMgr = oldMgr }()

	exec := &workflowExecutor{
		cfg:  cfg,
		mode: WorkflowModeLive,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			ID:          "run-hg-timeout-stop",
			Status:      "running",
			StepResults: map[string]*StepRunResult{},
		},
		sem:      sem,
		childSem: make(chan struct{}, 1),
	}

	step := &WorkflowStep{
		ID:             "timeout-stop",
		Type:           "human",
		HumanSubtype:   "approval",
		HumanTimeout:   "200ms",
		HumanOnTimeout: "stop",
	}
	result := &StepRunResult{StepID: "timeout-stop"}

	exec.runHumanStep(context.Background(), step, result)

	if result.Status != "timeout" {
		t.Errorf("status = %q, want timeout", result.Status)
	}
}

// TestHumanGateTimeoutSkip — timeout with onTimeout="skip" → step skipped.
func TestHumanGateTimeoutSkip(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)
	initCallbackTable(cfg.HistoryDB)
	initHumanGateTable(cfg.HistoryDB)

	cm := newCallbackManager(cfg.HistoryDB)
	oldMgr := callbackMgr
	callbackMgr = cm
	defer func() { callbackMgr = oldMgr }()

	exec := &workflowExecutor{
		cfg:  cfg,
		mode: WorkflowModeLive,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			ID:          "run-hg-timeout-skip",
			Status:      "running",
			StepResults: map[string]*StepRunResult{},
		},
		sem:      sem,
		childSem: make(chan struct{}, 1),
	}

	step := &WorkflowStep{
		ID:             "timeout-skip",
		Type:           "human",
		HumanSubtype:   "approval",
		HumanTimeout:   "200ms",
		HumanOnTimeout: "skip",
	}
	result := &StepRunResult{StepID: "timeout-skip"}

	exec.runHumanStep(context.Background(), step, result)

	if result.Status != "skipped" {
		t.Errorf("status = %q, want skipped", result.Status)
	}
}

// TestHumanGateTimeoutApprove — timeout with onTimeout="approve" → step success.
func TestHumanGateTimeoutApprove(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)
	initCallbackTable(cfg.HistoryDB)
	initHumanGateTable(cfg.HistoryDB)

	cm := newCallbackManager(cfg.HistoryDB)
	oldMgr := callbackMgr
	callbackMgr = cm
	defer func() { callbackMgr = oldMgr }()

	exec := &workflowExecutor{
		cfg:  cfg,
		mode: WorkflowModeLive,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			ID:          "run-hg-timeout-approve",
			Status:      "running",
			StepResults: map[string]*StepRunResult{},
		},
		sem:      sem,
		childSem: make(chan struct{}, 1),
	}

	step := &WorkflowStep{
		ID:             "timeout-approve",
		Type:           "human",
		HumanSubtype:   "approval",
		HumanTimeout:   "200ms",
		HumanOnTimeout: "approve",
	}
	result := &StepRunResult{StepID: "timeout-approve"}

	exec.runHumanStep(context.Background(), step, result)

	if result.Status != "success" {
		t.Errorf("status = %q, want success", result.Status)
	}
	if !strings.Contains(result.Output, "auto-approved") {
		t.Errorf("output should contain 'auto-approved', got %q", result.Output)
	}
}

// TestHumanStepValidation — validate human step field checks.
func TestHumanStepValidation(t *testing.T) {
	ids := map[string]bool{"hg1": true}

	// Valid approval step.
	errs := validateStep(WorkflowStep{ID: "hg1", Type: "human", HumanSubtype: "approval"}, ids)
	if len(errs) != 0 {
		t.Errorf("valid approval step should have no errors, got %v", errs)
	}

	// Input subtype without inputKey.
	errs = validateStep(WorkflowStep{ID: "hg1", Type: "human", HumanSubtype: "input"}, ids)
	if len(errs) == 0 {
		t.Error("input subtype without humanInputKey should have error")
	}

	// Invalid subtype.
	errs = validateStep(WorkflowStep{ID: "hg1", Type: "human", HumanSubtype: "invalid"}, ids)
	if len(errs) == 0 {
		t.Error("invalid subtype should have error")
	}

	// Invalid timeout.
	errs = validateStep(WorkflowStep{ID: "hg1", Type: "human", HumanTimeout: "xyz"}, ids)
	if len(errs) == 0 {
		t.Error("invalid timeout should have error")
	}

	// Invalid onTimeout.
	errs = validateStep(WorkflowStep{ID: "hg1", Type: "human", HumanOnTimeout: "explode"}, ids)
	if len(errs) == 0 {
		t.Error("invalid onTimeout should have error")
	}
}

// TestHumanGateRespondedBy — respondedBy field is persisted to DB after approval.
func TestHumanGateRespondedBy(t *testing.T) {
	cfg, sem := testWorkflowCfg(t)
	initCallbackTable(cfg.HistoryDB)
	initHumanGateTable(cfg.HistoryDB)

	cm := newCallbackManager(cfg.HistoryDB)
	oldMgr := callbackMgr
	callbackMgr = cm
	defer func() { callbackMgr = oldMgr }()

	exec := &workflowExecutor{
		cfg:  cfg,
		mode: WorkflowModeLive,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			ID:          "run-hg-respondedby",
			Status:      "running",
			StepResults: map[string]*StepRunResult{},
		},
		sem:      sem,
		childSem: make(chan struct{}, 1),
	}

	step := &WorkflowStep{
		ID:            "approve-audit",
		Type:          "human",
		HumanSubtype:  "approval",
		HumanPrompt:   "Approve release?",
		HumanAssignee: "takuma",
		HumanTimeout:  "10s",
	}
	result := &StepRunResult{StepID: "approve-audit"}

	// Simulate human approval with respondedBy field.
	go func() {
		time.Sleep(100 * time.Millisecond)
		hgKey := fmt.Sprintf("hg-%s-%s", exec.run.ID, step.ID)
		body := `{"action":"approved","response":"ship it","respondedBy":"takuma"}`
		cm.Deliver(hgKey, CallbackResult{Body: body})
	}()

	exec.runHumanStep(context.Background(), step, result)

	if result.Status != "success" {
		t.Errorf("status = %q, want success", result.Status)
	}

	// Verify respondedBy was persisted to DB.
	hgKey := fmt.Sprintf("hg-%s-%s", exec.run.ID, step.ID)
	record := queryHumanGate(cfg.HistoryDB, hgKey)
	if record == nil {
		t.Fatal("human gate record not found in DB")
	}
	if record.RespondedBy != "takuma" {
		t.Errorf("RespondedBy = %q, want %q", record.RespondedBy, "takuma")
	}
}

// TODO: TestIsValidWorkflowName removed — isValidWorkflowName is internal-only

func TestResolveHumanAssigneeChannel(t *testing.T) {
	t.Run("Given mapping exists for assignee, Then returns mapped channel", func(t *testing.T) {
		m := map[string]string{"takuma": "ch-111"}
		got := resolveHumanAssigneeChannel(m, "takuma", "ch-default")
		if got != "ch-111" {
			t.Errorf("got %q, want %q", got, "ch-111")
		}
	})

	t.Run("Given no mapping for assignee, Then returns fallback", func(t *testing.T) {
		m := map[string]string{"alice": "ch-alice"}
		got := resolveHumanAssigneeChannel(m, "takuma", "ch-default")
		if got != "ch-default" {
			t.Errorf("got %q, want %q", got, "ch-default")
		}
	})

	t.Run("Given nil map, Then returns fallback", func(t *testing.T) {
		got := resolveHumanAssigneeChannel(nil, "takuma", "ch-default")
		if got != "ch-default" {
			t.Errorf("got %q, want %q", got, "ch-default")
		}
	})

	t.Run("Given empty assignee, Then returns fallback", func(t *testing.T) {
		m := map[string]string{"takuma": "ch-111"}
		got := resolveHumanAssigneeChannel(m, "", "ch-default")
		if got != "ch-default" {
			t.Errorf("got %q, want %q", got, "ch-default")
		}
	})

	t.Run("Given mapping with empty channel value, Then returns fallback", func(t *testing.T) {
		m := map[string]string{"takuma": ""}
		got := resolveHumanAssigneeChannel(m, "takuma", "ch-default")
		if got != "ch-default" {
			t.Errorf("got %q, want %q", got, "ch-default")
		}
	})

	t.Run("Given empty fallback and no mapping, Then returns empty string", func(t *testing.T) {
		got := resolveHumanAssigneeChannel(nil, "takuma", "")
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})
}
