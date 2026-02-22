package main

import (
	"context"
	"testing"
	"time"
)

func TestExpandSkillVars_Empty(t *testing.T) {
	got := expandSkillVars("hello", nil)
	if got != "hello" {
		t.Errorf("expandSkillVars(%q, nil) = %q, want %q", "hello", got, "hello")
	}
}

func TestExpandSkillVars_SingleVar(t *testing.T) {
	vars := map[string]string{"name": "world"}
	got := expandSkillVars("hello {{name}}", vars)
	if got != "hello world" {
		t.Errorf("expandSkillVars = %q, want %q", got, "hello world")
	}
}

func TestExpandSkillVars_MultipleVars(t *testing.T) {
	vars := map[string]string{"a": "1", "b": "2"}
	got := expandSkillVars("{{a}} and {{b}}", vars)
	if got != "1 and 2" {
		t.Errorf("expandSkillVars = %q, want %q", got, "1 and 2")
	}
}

func TestExpandSkillVars_NoMatch(t *testing.T) {
	vars := map[string]string{"x": "y"}
	got := expandSkillVars("{{missing}}", vars)
	if got != "{{missing}}" {
		t.Errorf("expandSkillVars = %q, want %q", got, "{{missing}}")
	}
}

func TestExecuteSkill_Echo(t *testing.T) {
	skill := SkillConfig{
		Name:    "test_echo",
		Command: "echo",
		Args:    []string{"hello", "world"},
		Timeout: "5s",
	}
	result, err := executeSkill(context.Background(), skill, nil)
	if err != nil {
		t.Fatalf("executeSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q (error: %s)", result.Status, "success", result.Error)
	}
	if result.Output != "hello world\n" {
		t.Errorf("output = %q, want %q", result.Output, "hello world\n")
	}
	if result.Duration < 0 {
		t.Errorf("duration = %d, want >= 0", result.Duration)
	}
}

func TestExecuteSkill_WithVars(t *testing.T) {
	skill := SkillConfig{
		Name:    "test_vars",
		Command: "echo",
		Args:    []string{"{{greeting}}", "{{target}}"},
		Timeout: "5s",
	}
	vars := map[string]string{"greeting": "hi", "target": "world"}
	result, err := executeSkill(context.Background(), skill, vars)
	if err != nil {
		t.Fatalf("executeSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
	if result.Output != "hi world\n" {
		t.Errorf("output = %q, want %q", result.Output, "hi world\n")
	}
}

func TestExecuteSkill_CommandNotFound(t *testing.T) {
	skill := SkillConfig{
		Name:    "test_missing",
		Command: "tetora_nonexistent_command_12345",
		Timeout: "5s",
	}
	result, err := executeSkill(context.Background(), skill, nil)
	if err != nil {
		t.Fatalf("executeSkill returned error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
	if result.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestExecuteSkill_Timeout(t *testing.T) {
	skill := SkillConfig{
		Name:    "test_timeout",
		Command: "sleep",
		Args:    []string{"10"},
		Timeout: "100ms",
	}
	start := time.Now()
	result, err := executeSkill(context.Background(), skill, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeSkill returned error: %v", err)
	}
	if result.Status != "timeout" {
		t.Errorf("status = %q, want %q", result.Status, "timeout")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took too long: %v (timeout should have been 100ms)", elapsed)
	}
}

func TestExecuteSkill_DefaultTimeout(t *testing.T) {
	// Invalid timeout string should default to 30s, but we test with a quick command.
	skill := SkillConfig{
		Name:    "test_default_timeout",
		Command: "echo",
		Args:    []string{"ok"},
		Timeout: "invalid",
	}
	result, err := executeSkill(context.Background(), skill, nil)
	if err != nil {
		t.Fatalf("executeSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
}

func TestExecuteSkill_ErrorCommand(t *testing.T) {
	skill := SkillConfig{
		Name:    "test_error",
		Command: "ls",
		Args:    []string{"/nonexistent_dir_12345"},
		Timeout: "5s",
	}
	result, err := executeSkill(context.Background(), skill, nil)
	if err != nil {
		t.Fatalf("executeSkill returned error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
}

func TestListSkills_Empty(t *testing.T) {
	cfg := &Config{}
	skills := listSkills(cfg)
	if len(skills) != 0 {
		t.Errorf("listSkills on empty config = %d, want 0", len(skills))
	}
}

func TestListSkills_WithSkills(t *testing.T) {
	cfg := &Config{
		Skills: []SkillConfig{
			{Name: "a", Command: "echo"},
			{Name: "b", Command: "ls"},
		},
	}
	skills := listSkills(cfg)
	if len(skills) != 2 {
		t.Errorf("listSkills = %d, want 2", len(skills))
	}
}

func TestGetSkill_Found(t *testing.T) {
	cfg := &Config{
		Skills: []SkillConfig{
			{Name: "alpha", Command: "echo"},
			{Name: "beta", Command: "ls"},
		},
	}
	s := getSkill(cfg, "beta")
	if s == nil {
		t.Fatal("getSkill returned nil for existing skill")
	}
	if s.Command != "ls" {
		t.Errorf("command = %q, want %q", s.Command, "ls")
	}
}

func TestGetSkill_NotFound(t *testing.T) {
	cfg := &Config{
		Skills: []SkillConfig{
			{Name: "alpha", Command: "echo"},
		},
	}
	s := getSkill(cfg, "missing")
	if s != nil {
		t.Errorf("getSkill returned non-nil for missing skill: %v", s)
	}
}

func TestTestSkill_SetsShortTimeout(t *testing.T) {
	skill := SkillConfig{
		Name:    "test_quick",
		Command: "echo",
		Args:    []string{"ok"},
		Timeout: "60s", // should be overridden to 5s
	}
	result, err := testSkill(context.Background(), skill)
	if err != nil {
		t.Fatalf("testSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
}

func TestExecuteSkill_EnvVars(t *testing.T) {
	skill := SkillConfig{
		Name:    "test_env",
		Command: "sh",
		Args:    []string{"-c", "echo $TETORA_SKILL_TEST_VAR"},
		Env:     map[string]string{"TETORA_SKILL_TEST_VAR": "hello_from_skill"},
		Timeout: "5s",
	}
	result, err := executeSkill(context.Background(), skill, nil)
	if err != nil {
		t.Fatalf("executeSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q (error: %s)", result.Status, "success", result.Error)
	}
	if result.Output != "hello_from_skill\n" {
		t.Errorf("output = %q, want %q", result.Output, "hello_from_skill\n")
	}
}
