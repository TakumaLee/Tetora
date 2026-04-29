package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tetora/internal/skill"
)

// newSkillOnlyRegistry builds a registry with no builtins, then invokes
// registerSkillTools to exercise only the skill-as-tool path.
func newSkillOnlyRegistry(t *testing.T, skills []skill.SkillConfig) (*ToolRegistry, *Config) {
	t.Helper()
	cfg := &Config{
		DefaultProvider: "mock",
		Skills:          skills,
		// SkillStore empty → LoadFileSkills returns nothing, so only
		// config-based skills flow through listSkills.
	}
	r := newEmptyRegistry()
	registerSkillTools(r, cfg)
	return r, cfg
}

func TestRegisterSkillTools_GeneratesToolPerSkill(t *testing.T) {
	skills := []skill.SkillConfig{
		{
			Name:        "competitive-intel",
			Description: "Search news and social for a target company",
			Command:     "echo",
			Args:        []string{"target={{target}}", "count={{count}}"},
		},
	}
	r, _ := newSkillOnlyRegistry(t, skills)

	tool, ok := r.Get("competitive_intel")
	if !ok {
		t.Fatalf("expected tool competitive_intel to be registered")
	}
	if !strings.Contains(tool.Description, "Search news") {
		t.Errorf("description not carried over: %q", tool.Description)
	}

	var schema struct {
		Type       string                    `json:"type"`
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("schema invalid: %v", err)
	}
	if _, ok := schema.Properties["target"]; !ok {
		t.Errorf("expected target in schema, got %v", schema.Properties)
	}
	if _, ok := schema.Properties["count"]; !ok {
		t.Errorf("expected count in schema, got %v", schema.Properties)
	}
}

func TestSkillToolHandler_PassesVarsAndCoercesTypes(t *testing.T) {
	// Use a real sh script so ExecuteSkill actually runs and we can see
	// what vars reached the process.
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.txt")
	script := filepath.Join(dir, "dump.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf "target=%s count=%s flag=%s\n" "$1" "$2" "$3" > "$OUT"
`), 0o755); err != nil {
		t.Fatal(err)
	}

	skills := []skill.SkillConfig{{
		Name:    "dumper",
		Command: script,
		Args:    []string{"{{target}}", "{{count}}", "{{flag}}"},
		Env:     map[string]string{"OUT": outFile},
		Timeout: "5s",
	}}
	r, cfg := newSkillOnlyRegistry(t, skills)

	tool, ok := r.Get("dumper")
	if !ok {
		t.Fatal("dumper tool not registered")
	}

	input := json.RawMessage(`{"target":"acme","count":5,"flag":true}`)
	out, err := tool.Handler(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("handler failed: %v\noutput=%s", err, out)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading script output: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "target=acme count=5 flag=true"
	if got != want {
		t.Errorf("script saw %q, want %q", got, want)
	}
}

func TestSkillToolHandler_MissingSkillReturnsError(t *testing.T) {
	// Register with one skill, then drop it from cfg to simulate hot-removal.
	skills := []skill.SkillConfig{{
		Name:    "ghost",
		Command: "echo",
		Args:    []string{"{{x}}"},
	}}
	r, cfg := newSkillOnlyRegistry(t, skills)
	tool, _ := r.Get("ghost")
	cfg.Skills = nil

	_, err := tool.Handler(context.Background(), cfg, json.RawMessage(`{"x":"1"}`))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got %v", err)
	}
}

func TestRegisterSkillTools_CollisionPrefix(t *testing.T) {
	cfg := &Config{DefaultProvider: "mock", Skills: []skill.SkillConfig{
		{Name: "echo", Command: "echo", Args: []string{"{{msg}}"}},
	}}
	r := newEmptyRegistry()
	// Pre-register a tool named "echo" so the skill collides.
	r.Register(&ToolDef{
		Name:        "echo",
		Description: "pre-existing",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, _ *Config, _ json.RawMessage) (string, error) {
			return "pre", nil
		},
	})
	registerSkillTools(r, cfg)

	if t2, ok := r.Get("skill_echo"); !ok {
		t.Fatalf("expected skill_echo to be registered after collision")
	} else if t2.Description == "pre-existing" {
		t.Errorf("skill_echo should point at skill, not pre-existing tool")
	}
	if t1, _ := r.Get("echo"); t1 == nil || t1.Description != "pre-existing" {
		t.Errorf("original echo tool should still win")
	}
}

func TestDecodeSkillToolVars(t *testing.T) {
	got := decodeSkillToolVars(json.RawMessage(`{"s":"hi","n":3,"b":true,"o":{"k":1},"nul":null}`))
	if got["s"] != "hi" {
		t.Errorf("s: got %q", got["s"])
	}
	if got["n"] != "3" {
		t.Errorf("n: got %q", got["n"])
	}
	if got["b"] != "true" {
		t.Errorf("b: got %q", got["b"])
	}
	if !strings.Contains(got["o"], `"k":1`) {
		t.Errorf("o: got %q", got["o"])
	}
	if _, present := got["nul"]; present {
		t.Errorf("null should be skipped, got %q", got["nul"])
	}
}
