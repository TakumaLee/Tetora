package skill

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestExtractVars_CoversAllTemplateSites(t *testing.T) {
	s := SkillConfig{
		Name:    "demo",
		Command: "curl",
		Args:    []string{"-X", "GET", "https://api.example.com/{{endpoint}}?q={{query}}"},
		Env:     map[string]string{"API_KEY": "{{api_key}}", "DEBUG": "1"},
		Workdir: "/tmp/{{session}}",
	}
	got := ExtractVars(s)
	want := []string{"api_key", "endpoint", "query", "session"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractVars_DeduplicatesAcrossSites(t *testing.T) {
	s := SkillConfig{
		Command: "{{target}}",
		Args:    []string{"{{target}}", "--count", "{{count}}"},
	}
	got := ExtractVars(s)
	want := []string{"count", "target"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractVars_NoVarsReturnsEmpty(t *testing.T) {
	s := SkillConfig{Command: "ls", Args: []string{"-la"}}
	got := ExtractVars(s)
	if len(got) != 0 {
		t.Fatalf("expected no vars, got %v", got)
	}
}

func TestBuildToolSchema_Shape(t *testing.T) {
	s := SkillConfig{
		Command: "echo",
		Args:    []string{"{{target}}"},
	}
	raw := BuildToolSchema(s)
	var parsed struct {
		Type       string                    `json:"type"`
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("schema not valid json: %v", err)
	}
	if parsed.Type != "object" {
		t.Fatalf("expected type=object, got %q", parsed.Type)
	}
	prop, ok := parsed.Properties["target"]
	if !ok {
		t.Fatalf("expected property 'target', got %v", parsed.Properties)
	}
	if prop["type"] != "string" {
		t.Fatalf("expected target.type=string, got %v", prop["type"])
	}
}

func TestBuildToolSchema_NoVars(t *testing.T) {
	s := SkillConfig{Command: "date"}
	raw := BuildToolSchema(s)
	var parsed struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("schema not valid json: %v", err)
	}
	if parsed.Type != "object" {
		t.Fatalf("expected object type, got %q", parsed.Type)
	}
	if len(parsed.Properties) != 0 {
		t.Fatalf("expected empty properties, got %v", parsed.Properties)
	}
}

func TestToolNameFor(t *testing.T) {
	cases := map[string]string{
		"competitive-intel": "competitive_intel",
		"My Skill":          "my_skill",
		"foo.bar":           "foo_bar",
		"already_good":      "already_good",
		"  spaced  ":        "spaced",
	}
	for in, want := range cases {
		got := ToolNameFor(SkillConfig{Name: in})
		if got != want {
			t.Errorf("ToolNameFor(%q) = %q, want %q", in, got, want)
		}
	}
}
