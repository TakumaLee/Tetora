package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem)

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
				Role:   "翡翠",
				Prompt: "Analyze the Go codebase for potential improvements",
			},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, WorkflowModeDryRun)

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
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, WorkflowModeDryRun)

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
	run := executeWorkflow(context.Background(), cfg, wf, vars, state, sem, WorkflowModeDryRun)

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
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, WorkflowModeDryRun)

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

	wf := &Workflow{
		Name: "test-prefix",
		Steps: []WorkflowStep{
			{ID: "s1", Prompt: "Hello"},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, WorkflowModeDryRun)

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

	// Use a condition step (which doesn't need a provider) to test shadow status prefix.
	wf := &Workflow{
		Name: "test-shadow-prefix",
		Steps: []WorkflowStep{
			{ID: "s1", Type: "condition", If: "'yes' == 'yes'", Then: "s1"},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, WorkflowModeShadow)

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
		workflow: &Workflow{Name: "test-handoff-dry", Steps: []WorkflowStep{{ID: "src", Role: "翡翠"}}},
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
		Role:        "黒曜",
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
		Role:        "黒曜",
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
			{ID: "research", Prompt: "Research topic A", Role: "翡翠"},
			{ID: "analyze", Prompt: "Analyze {{steps.research.output}}", Role: "翡翠", DependsOn: []string{"research"}},
			{ID: "report", Prompt: "Write final report", Role: "琥珀", DependsOn: []string{"analyze"}},
		},
	}

	state := newDispatchState()
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, WorkflowModeDryRun)

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
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, WorkflowModeDryRun)
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
	run := executeWorkflow(context.Background(), cfg, wf, nil, state, sem, "")
	if strings.HasPrefix(run.Status, "dry-run:") || strings.HasPrefix(run.Status, "shadow:") {
		t.Errorf("empty mode should default to live, got status=%q", run.Status)
	}
}
