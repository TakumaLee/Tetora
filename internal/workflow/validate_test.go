package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Unit tests for ValidateWorkflow ---

func TestValidateWorkflow_valid(t *testing.T) {
	w := &Workflow{
		Name: "test-workflow",
		Steps: []WorkflowStep{
			{ID: "step-a", Prompt: "do something"},
			{
				ID:        "step-b",
				Prompt:    "use {{steps.step-a.output}}",
				DependsOn: []string{"step-a"},
			},
		},
	}
	errs := ValidateWorkflow(w)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidateWorkflow_onErrorTypo(t *testing.T) {
	w := &Workflow{
		Name: "test-workflow",
		Steps: []WorkflowStep{
			{ID: "step-a", Prompt: "do something", OnError: "contniue"}, // typo: should be stop/skip/retry
		},
	}
	errs := ValidateWorkflow(w)
	if len(errs) == 0 {
		t.Fatal("expected error for invalid onError value, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "onError") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error mentioning 'onError', got: %v", errs)
	}
}

func TestValidateWorkflow_brokenTemplateRef(t *testing.T) {
	w := &Workflow{
		Name: "test-workflow",
		Steps: []WorkflowStep{
			{ID: "step-a", Prompt: "do something"},
			{
				ID:        "step-b",
				Prompt:    "use {{steps.typo-step.output}}",
				DependsOn: []string{"step-a"},
			},
		},
	}
	errs := ValidateWorkflow(w)
	if len(errs) == 0 {
		t.Fatal("expected error for broken template reference, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "typo-step") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error mentioning 'typo-step', got: %v", errs)
	}
}

func TestValidateWorkflow_brokenTemplateRef_inCondition(t *testing.T) {
	w := &Workflow{
		Name: "test-workflow",
		Steps: []WorkflowStep{
			{ID: "plan", Prompt: "make a plan"},
			{
				ID:        "check",
				Type:      "condition",
				If:        "{{steps.ghost.output}} == 'high'",
				Then:      "plan",
				DependsOn: []string{"plan"},
			},
		},
	}
	errs := ValidateWorkflow(w)
	if len(errs) == 0 {
		t.Fatal("expected error for broken template ref in 'if' field, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "ghost") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error mentioning 'ghost', got: %v", errs)
	}
}

func TestValidateWorkflow_brokenDependsOn(t *testing.T) {
	w := &Workflow{
		Name: "test-workflow",
		Steps: []WorkflowStep{
			{ID: "step-a", Prompt: "do something", DependsOn: []string{"nonexistent"}},
		},
	}
	errs := ValidateWorkflow(w)
	if len(errs) == 0 {
		t.Fatal("expected error for unknown dependsOn step, got none")
	}
}

func TestValidateWorkflow_cycleDetection(t *testing.T) {
	w := &Workflow{
		Name: "test-workflow",
		Steps: []WorkflowStep{
			{ID: "step-a", Prompt: "do something", DependsOn: []string{"step-b"}},
			{ID: "step-b", Prompt: "do something", DependsOn: []string{"step-a"}},
		},
	}
	errs := ValidateWorkflow(w)
	if len(errs) == 0 {
		t.Fatal("expected error for dependency cycle, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "cycle") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error mentioning 'cycle', got: %v", errs)
	}
}

func TestValidateWorkflow_missingName(t *testing.T) {
	w := &Workflow{
		Steps: []WorkflowStep{
			{ID: "step-a", Prompt: "do something"},
		},
	}
	errs := ValidateWorkflow(w)
	if len(errs) == 0 {
		t.Fatal("expected error for missing workflow name, got none")
	}
}

func TestValidateWorkflow_duplicateStepID(t *testing.T) {
	w := &Workflow{
		Name: "test-workflow",
		Steps: []WorkflowStep{
			{ID: "step-a", Prompt: "first"},
			{ID: "step-a", Prompt: "duplicate"},
		},
	}
	errs := ValidateWorkflow(w)
	if len(errs) == 0 {
		t.Fatal("expected error for duplicate step ID, got none")
	}
}

func TestValidateWorkflow_validOnErrorValues(t *testing.T) {
	for _, val := range []string{"stop", "skip", "retry"} {
		w := &Workflow{
			Name: "test-workflow",
			Steps: []WorkflowStep{
				{ID: "step-a", Prompt: "do something", OnError: val},
			},
		}
		errs := ValidateWorkflow(w)
		if len(errs) != 0 {
			t.Errorf("onError=%q should be valid, got: %v", val, errs)
		}
	}
}

// --- Integration test: validate all workflow JSON files ---

// TestWorkflowFiles loads every .json file from ~/.tetora/workflows/ and runs
// ValidateWorkflow on each. A missing workflow directory skips the test rather
// than failing (keeps CI green on machines without a Tetora installation).
func TestWorkflowFiles(t *testing.T) {
	dir := filepath.Join(os.Getenv("HOME"), ".tetora", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("workflow dir not accessible: %v", err)
	}

	tested := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			w, err := LoadWorkflow(path)
			if err != nil {
				t.Fatalf("failed to load %s: %v", path, err)
			}
			errs := ValidateWorkflow(w)
			for _, e := range errs {
				t.Errorf("%s", e)
			}
		})
		tested++
	}

	if tested == 0 {
		t.Skip("no workflow JSON files found")
	}
	t.Logf("validated %d workflow file(s)", tested)
}
