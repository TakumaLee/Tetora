package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testCfg creates a minimal config for workflow exec tests.
func testWorkflowCfg(t *testing.T) (*Config, chan struct{}) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	cfg := &Config{
		baseDir:               dir,
		HistoryDB:             dbPath,
		DefaultModel:          "sonnet",
		DefaultTimeout:        "5m",
		DefaultPermissionMode: "plan",
		DefaultWorkdir:        dir,
		DefaultProvider:       "claude",
		registry:              initProviders(&Config{}),
	}

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

func TestWorkflowDirCreation(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}

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
		FromRole:      "翡翠",
		ToRole:        "黒曜",
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
	if handoffs[0].FromRole != "翡翠" || handoffs[0].ToRole != "黒曜" {
		t.Errorf("roles = %s→%s", handoffs[0].FromRole, handoffs[0].ToRole)
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
		FromRole:      "翡翠",
		ToRole:        "黒曜",
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
		FromRole:      "黒曜",
		ToRole:        "翡翠",
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
				if delegations[0].Role != tt.role {
					t.Errorf("role = %q, want %q", delegations[0].Role, tt.role)
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
			{ID: "research", Role: "翡翠", Prompt: "Research this"},
			{ID: "implement", Type: "handoff", HandoffFrom: "research", Role: "黒曜",
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
			{ID: "step1", Role: "翡翠", Prompt: "Do something"},
			{ID: "step2", Type: "handoff", Role: "黒曜"},
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

	// Missing role.
	w3 := &Workflow{
		Name: "test-handoff-no-role",
		Steps: []WorkflowStep{
			{ID: "step1", Role: "翡翠", Prompt: "Do something"},
			{ID: "step2", Type: "handoff", HandoffFrom: "step1"},
		},
	}
	errs3 := validateWorkflow(w3)
	foundRole := false
	for _, e := range errs3 {
		if contains(e, "target 'role'") {
			foundRole = true
		}
	}
	if !foundRole {
		t.Errorf("expected role error, got: %v", errs3)
	}

	// Unknown handoffFrom reference.
	w4 := &Workflow{
		Name: "test-handoff-bad-ref",
		Steps: []WorkflowStep{
			{ID: "step1", Role: "翡翠", Prompt: "Do something"},
			{ID: "step2", Type: "handoff", HandoffFrom: "nonexistent", Role: "黒曜"},
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
		workflow: &Workflow{Name: "test", Steps: []WorkflowStep{{ID: "src", Role: "翡翠"}}},
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
		Role:        "黒曜",
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
		Role:        "黒曜",
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
		FromRole: "翡翠",
		ToRole:   "黒曜",
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
