package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowLoadSave(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{baseDir: dir}

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
	cfg := &Config{baseDir: dir}

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
	cfg := &Config{baseDir: dir}

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

func TestIsValidWorkflowName(t *testing.T) {
	valid := []string{"my-workflow", "test_123", "a", "Code-Review"}
	for _, n := range valid {
		if !isValidWorkflowName(n) {
			t.Errorf("expected %q to be valid", n)
		}
	}
	invalid := []string{"", "has space", "special!", "../escape", "-start"}
	for _, n := range invalid {
		if isValidWorkflowName(n) {
			t.Errorf("expected %q to be invalid", n)
		}
	}
}

