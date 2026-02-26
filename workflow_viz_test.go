package main

import "testing"

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
