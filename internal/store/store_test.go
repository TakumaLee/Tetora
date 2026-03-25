package store

import (
	"testing"
)

func TestBrowse_WorkflowSkills(t *testing.T) {
	deps := Deps{
		ListWorkflows: func() ([]WorkflowInfo, error) { return nil, nil },
		ListTemplates: func() []TemplateInfo { return nil },
		ListWorkflowSkills: func() []SkillInfo {
			return []SkillInfo{
				{Name: "dev-cycle", Description: "Full dev cycle", Type: "workflow", WorkflowName: "dev-wf"},
				{Name: "cmd-skill", Description: "A command skill", Type: "command"},
			}
		},
	}

	items, cats := Browse(deps)

	// Only the workflow-type skill should be included.
	if len(items) != 1 {
		t.Fatalf("Browse returned %d items, want 1", len(items))
	}
	if items[0].Name != "dev-cycle" {
		t.Errorf("item name = %q, want %q", items[0].Name, "dev-cycle")
	}
	if items[0].Source != "skill-workflow" {
		t.Errorf("item source = %q, want %q", items[0].Source, "skill-workflow")
	}
	if items[0].Type != "skill-workflow" {
		t.Errorf("item type = %q, want %q", items[0].Type, "skill-workflow")
	}
	if !items[0].Installed {
		t.Error("item installed = false, want true")
	}
	if len(cats) == 0 {
		t.Error("expected at least 1 category")
	}
}

func TestBrowse_WorkflowSkills_Dedup(t *testing.T) {
	deps := Deps{
		ListWorkflows: func() ([]WorkflowInfo, error) {
			return []WorkflowInfo{{Name: "dev-cycle", Description: "Existing wf", StepCount: 3}}, nil
		},
		ListTemplates: func() []TemplateInfo { return nil },
		ListWorkflowSkills: func() []SkillInfo {
			return []SkillInfo{
				{Name: "dev-cycle", Description: "Skill version", Type: "workflow", WorkflowName: "dev-cycle"},
			}
		},
	}

	items, _ := Browse(deps)

	// Should not duplicate — workflow listing comes first, skill-workflow should be skipped.
	count := 0
	for _, item := range items {
		if item.Name == "dev-cycle" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("dev-cycle appeared %d times, want 1", count)
	}
}

func TestBrowse_NilWorkflowSkills(t *testing.T) {
	deps := Deps{
		ListWorkflows: func() ([]WorkflowInfo, error) { return nil, nil },
		ListTemplates: func() []TemplateInfo { return nil },
		// ListWorkflowSkills is nil — should not panic.
	}

	items, _ := Browse(deps)
	if len(items) != 0 {
		t.Errorf("Browse returned %d items, want 0", len(items))
	}
}
