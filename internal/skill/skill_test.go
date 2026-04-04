package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExpandSkillVars_Empty(t *testing.T) {
	got := ExpandSkillVars("hello", nil)
	if got != "hello" {
		t.Errorf("ExpandSkillVars(%q, nil) = %q, want %q", "hello", got, "hello")
	}
}

func TestExpandSkillVars_SingleVar(t *testing.T) {
	vars := map[string]string{"name": "world"}
	got := ExpandSkillVars("hello {{name}}", vars)
	if got != "hello world" {
		t.Errorf("ExpandSkillVars = %q, want %q", got, "hello world")
	}
}

func TestExpandSkillVars_MultipleVars(t *testing.T) {
	vars := map[string]string{"a": "1", "b": "2"}
	got := ExpandSkillVars("{{a}} and {{b}}", vars)
	if got != "1 and 2" {
		t.Errorf("ExpandSkillVars = %q, want %q", got, "1 and 2")
	}
}

func TestExpandSkillVars_NoMatch(t *testing.T) {
	vars := map[string]string{"x": "y"}
	got := ExpandSkillVars("{{missing}}", vars)
	if got != "{{missing}}" {
		t.Errorf("ExpandSkillVars = %q, want %q", got, "{{missing}}")
	}
}

func TestExecuteSkill_Echo(t *testing.T) {
	s := SkillConfig{
		Name:    "test_echo",
		Command: "echo",
		Args:    []string{"hello", "world"},
		Timeout: "5s",
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
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
	s := SkillConfig{
		Name:    "test_vars",
		Command: "echo",
		Args:    []string{"{{greeting}}", "{{target}}"},
		Timeout: "5s",
	}
	vars := map[string]string{"greeting": "hi", "target": "world"}
	result, err := ExecuteSkill(context.Background(), s, vars)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
	if result.Output != "hi world\n" {
		t.Errorf("output = %q, want %q", result.Output, "hi world\n")
	}
}

func TestExecuteSkill_CommandNotFound(t *testing.T) {
	s := SkillConfig{
		Name:    "test_missing",
		Command: "tetora_nonexistent_command_12345",
		Timeout: "5s",
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
	if result.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestExecuteSkill_Timeout(t *testing.T) {
	s := SkillConfig{
		Name:    "test_timeout",
		Command: "sleep",
		Args:    []string{"10"},
		Timeout: "100ms",
	}
	start := time.Now()
	result, err := ExecuteSkill(context.Background(), s, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
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
	s := SkillConfig{
		Name:    "test_default_timeout",
		Command: "echo",
		Args:    []string{"ok"},
		Timeout: "invalid",
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
}

func TestExecuteSkill_ErrorCommand(t *testing.T) {
	s := SkillConfig{
		Name:    "test_error",
		Command: "ls",
		Args:    []string{"/nonexistent_dir_12345"},
		Timeout: "5s",
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
}

func TestListSkills_Empty(t *testing.T) {
	cfg := &AppConfig{}
	skills := ListSkills(cfg)
	if len(skills) != 0 {
		t.Errorf("ListSkills on empty config = %d, want 0", len(skills))
	}
}

func TestListSkills_WithSkills(t *testing.T) {
	cfg := &AppConfig{
		Skills: []SkillConfig{
			{Name: "a", Command: "echo"},
			{Name: "b", Command: "ls"},
		},
	}
	skills := ListSkills(cfg)
	if len(skills) != 2 {
		t.Errorf("ListSkills = %d, want 2", len(skills))
	}
}

func TestGetSkill_Found(t *testing.T) {
	cfg := &AppConfig{
		Skills: []SkillConfig{
			{Name: "alpha", Command: "echo"},
			{Name: "beta", Command: "ls"},
		},
	}
	s := GetSkill(cfg, "beta")
	if s == nil {
		t.Fatal("GetSkill returned nil for existing skill")
	}
	if s.Command != "ls" {
		t.Errorf("command = %q, want %q", s.Command, "ls")
	}
}

func TestGetSkill_NotFound(t *testing.T) {
	cfg := &AppConfig{
		Skills: []SkillConfig{
			{Name: "alpha", Command: "echo"},
		},
	}
	s := GetSkill(cfg, "missing")
	if s != nil {
		t.Errorf("GetSkill returned non-nil for missing skill: %v", s)
	}
}

func TestTestSkill_SetsShortTimeout(t *testing.T) {
	s := SkillConfig{
		Name:    "test_quick",
		Command: "echo",
		Args:    []string{"ok"},
		Timeout: "60s", // should be overridden to 5s
	}
	result, err := TestSkill(context.Background(), s)
	if err != nil {
		t.Fatalf("TestSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
}

func TestExecuteSkill_EnvVars(t *testing.T) {
	s := SkillConfig{
		Name:    "test_env",
		Command: "sh",
		Args:    []string{"-c", "echo $TETORA_SKILL_TEST_VAR"},
		Env:     map[string]string{"TETORA_SKILL_TEST_VAR": "hello_from_skill"},
		Timeout: "5s",
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q (error: %s)", result.Status, "success", result.Error)
	}
	if result.Output != "hello_from_skill\n" {
		t.Errorf("output = %q, want %q", result.Output, "hello_from_skill\n")
	}
}

func TestExecuteSkill_ValidationPass(t *testing.T) {
	// Create a temp validation script that exits 0.
	dir := t.TempDir()
	script := filepath.Join(dir, "validate.sh")
	os.WriteFile(script, []byte("#!/bin/bash\necho ok\nexit 0\n"), 0o755)

	s := SkillConfig{
		Name:             "test_val_pass",
		Command:          "echo",
		Args:             []string{"hello"},
		Timeout:          "5s",
		ValidationScript: script,
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
	if result.Validation == nil {
		t.Fatal("expected Validation to be non-nil")
	}
	if result.Validation.Status != "pass" {
		t.Errorf("validation status = %q, want %q", result.Validation.Status, "pass")
	}
}

func TestExecuteSkill_ValidationFail(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "validate.sh")
	os.WriteFile(script, []byte("#!/bin/bash\necho bad\nexit 1\n"), 0o755)

	s := SkillConfig{
		Name:             "test_val_fail",
		Command:          "echo",
		Args:             []string{"hello"},
		Timeout:          "5s",
		ValidationScript: script,
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	// Main status should still be success.
	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
	if result.Validation == nil {
		t.Fatal("expected Validation to be non-nil")
	}
	if result.Validation.Status != "fail" {
		t.Errorf("validation status = %q, want %q", result.Validation.Status, "fail")
	}
}

func TestExecuteSkill_NoValidation(t *testing.T) {
	s := SkillConfig{
		Name:    "test_no_val",
		Command: "echo",
		Args:    []string{"hello"},
		Timeout: "5s",
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	if result.Validation != nil {
		t.Errorf("expected Validation to be nil, got %+v", result.Validation)
	}
}

func TestExecuteSkill_SkipValidationOnError(t *testing.T) {
	// Validation script that would always pass — but should never run.
	dir := t.TempDir()
	script := filepath.Join(dir, "validate.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755)

	s := SkillConfig{
		Name:             "test_skip_val_error",
		Command:          "false", // exits non-zero → status "error"
		Timeout:          "5s",
		ValidationScript: script,
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
	if result.Validation != nil {
		t.Errorf("expected Validation to be nil when skill errored, got %+v", result.Validation)
	}
}

func TestExecuteSkill_SkipValidationOnTimeout(t *testing.T) {
	// Validation script that would always pass — but should never run.
	dir := t.TempDir()
	script := filepath.Join(dir, "validate.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755)

	s := SkillConfig{
		Name:             "test_skip_val_timeout",
		Command:          "sleep",
		Args:             []string{"10"},
		Timeout:          "100ms",
		ValidationScript: script,
	}
	result, err := ExecuteSkill(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ExecuteSkill returned error: %v", err)
	}
	if result.Status != "timeout" {
		t.Errorf("status = %q, want %q", result.Status, "timeout")
	}
	if result.Validation != nil {
		t.Errorf("expected Validation to be nil when skill timed out, got %+v", result.Validation)
	}
}
