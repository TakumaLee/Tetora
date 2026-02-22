package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SkillConfig defines a named skill (external command).
type SkillConfig struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Command     string            `json:"command"`             // shell command to execute
	Args        []string          `json:"args,omitempty"`      // command arguments
	Env         map[string]string `json:"env,omitempty"`       // additional env vars
	Workdir     string            `json:"workdir,omitempty"`   // working directory
	Timeout     string            `json:"timeout,omitempty"`   // default "30s"
	OutputAs    string            `json:"outputAs,omitempty"`  // "text" (default), "json"
}

// SkillResult is the output of a skill execution.
type SkillResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // "success", "error", "timeout"
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	Duration int64  `json:"durationMs"`
}

// executeSkill runs a skill command and returns the result.
func executeSkill(ctx context.Context, skill SkillConfig, vars map[string]string) (*SkillResult, error) {
	timeout, err := time.ParseDuration(skill.Timeout)
	if err != nil || timeout <= 0 {
		timeout = 30 * time.Second
	}

	skillCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Expand template vars in command and args.
	command := expandSkillVars(skill.Command, vars)
	args := make([]string, len(skill.Args))
	for i, a := range skill.Args {
		args[i] = expandSkillVars(a, vars)
	}

	// Validate command exists.
	if _, err := exec.LookPath(command); err != nil {
		return &SkillResult{
			Name:   skill.Name,
			Status: "error",
			Error:  fmt.Sprintf("command not found: %s", command),
		}, nil
	}

	cmd := exec.CommandContext(skillCtx, command, args...)
	if skill.Workdir != "" {
		cmd.Dir = expandSkillVars(skill.Workdir, vars)
	}

	// Set environment.
	cmd.Env = os.Environ()
	for k, v := range skill.Env {
		cmd.Env = append(cmd.Env, k+"="+expandSkillVars(v, vars))
	}

	start := time.Now()
	output, runErr := cmd.CombinedOutput()
	elapsed := time.Since(start)

	result := &SkillResult{
		Name:     skill.Name,
		Duration: elapsed.Milliseconds(),
		Output:   string(output),
	}

	if skillCtx.Err() == context.DeadlineExceeded {
		result.Status = "timeout"
		result.Error = fmt.Sprintf("timed out after %v", timeout)
	} else if runErr != nil {
		result.Status = "error"
		result.Error = runErr.Error()
	} else {
		result.Status = "success"
	}

	return result, nil
}

// expandSkillVars replaces {{key}} with values from the vars map.
func expandSkillVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// listSkills returns all configured skills.
func listSkills(cfg *Config) []SkillConfig {
	if cfg.Skills == nil {
		return []SkillConfig{}
	}
	return cfg.Skills
}

// getSkill returns a skill by name.
func getSkill(cfg *Config, name string) *SkillConfig {
	for i, s := range cfg.Skills {
		if s.Name == name {
			return &cfg.Skills[i]
		}
	}
	return nil
}

// testSkill runs a skill with a quick timeout to verify it works.
func testSkill(ctx context.Context, skill SkillConfig) (*SkillResult, error) {
	skill.Timeout = "5s"
	return executeSkill(ctx, skill, nil)
}
