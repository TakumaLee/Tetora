package skill

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
	Env         map[string]string `json:"env,omitempty"`       // additional env vars (supports $ENV_VAR)
	Matcher     *SkillMatcher     `json:"matcher,omitempty"`   // when to inject this skill (default: always)
	Example     string            `json:"example,omitempty"`   // usage example for LLM
	Workdir     string            `json:"workdir,omitempty"`   // working directory
	Timeout     string            `json:"timeout,omitempty"`   // default "30s"
	OutputAs         string            `json:"outputAs,omitempty"`     // "text" (default), "json"
	AllowedTools     []string          `json:"allowedTools,omitempty"` // tools this skill needs (e.g. ["Bash","Read"])
	DocPath          string            `json:"-"`                      // runtime: SKILL.md full path (not serialized)
	DocSize          int               `json:"-"`                      // runtime: SKILL.md byte size (not serialized)
	Learned          bool              `json:"-"`                      // runtime: true if loaded from skills/learned/
	ValidationScript string            `json:"-"`                      // runtime: scripts/validate.* path (not serialized)
}

// ValidationResult is the output of a post-execution validation script.
type ValidationResult struct {
	Status string `json:"status"` // "pass", "fail", "error"
	Output string `json:"output"`
}

// SkillResult is the output of a skill execution.
type SkillResult struct {
	Name       string            `json:"name"`
	Status     string            `json:"status"` // "success", "error", "timeout"
	Output     string            `json:"output"`
	Error      string            `json:"error,omitempty"`
	Duration   int64             `json:"durationMs"`
	Validation *ValidationResult `json:"validation,omitempty"`
}

// RunWorkflow is an optional callback set by the root package (wire.go) to
// bridge skill execution into the workflow engine. When nil, workflow-type
// skills are not supported.
var RunWorkflow func(ctx context.Context, workflowName string, vars map[string]string, callStack []string) (*SkillResult, error)

// ExecuteSkill runs a skill command and returns the result.
func ExecuteSkill(ctx context.Context, skill SkillConfig, vars map[string]string) (*SkillResult, error) {
	timeout, err := time.ParseDuration(skill.Timeout)
	if err != nil || timeout <= 0 {
		timeout = 30 * time.Second
	}

	skillCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Expand template vars in command and args.
	command := ExpandSkillVars(skill.Command, vars)
	args := make([]string, len(skill.Args))
	for i, a := range skill.Args {
		args[i] = ExpandSkillVars(a, vars)
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
		cmd.Dir = ExpandSkillVars(skill.Workdir, vars)
	}

	// Set environment.
	cmd.Env = os.Environ()
	for k, v := range skill.Env {
		// Resolve $ENV_VAR references first.
		resolved := resolveEnvRef(v, fmt.Sprintf("skill.%s.env.%s", skill.Name, k))
		// Then expand template vars.
		expanded := ExpandSkillVars(resolved, vars)
		cmd.Env = append(cmd.Env, k+"="+expanded)
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

	// Run validation script if configured and skill succeeded.
	// Validation is meaningless when the skill itself timed out or errored.
	if skill.ValidationScript != "" && result.Status == "success" {
		result.Validation = runValidationScript(ctx, skill.ValidationScript, cmd.Dir)
	}

	return result, nil
}

// runValidationScript executes a validation script with a fixed 10s timeout.
// Exit 0 → "pass", exit != 0 → "fail", exec error → "error".
// The main skill result.Status is never altered by validation outcome.
func runValidationScript(ctx context.Context, scriptPath, workDir string) *ValidationResult {
	valCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(valCtx, scriptPath, workDir)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()

	vr := &ValidationResult{Output: string(output)}
	if valCtx.Err() == context.DeadlineExceeded {
		vr.Status = "error"
		vr.Output = fmt.Sprintf("validation timed out after 10s\n%s", vr.Output)
	} else if err != nil {
		vr.Status = "fail"
	} else {
		vr.Status = "pass"
	}
	return vr
}

// ExpandSkillVars replaces {{key}} with values from the vars map.
func ExpandSkillVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// ListSkills returns all configured skills (config-based + file-based, merged).
func ListSkills(cfg *AppConfig) []SkillConfig {
	configSkills := cfg.Skills
	if configSkills == nil {
		configSkills = []SkillConfig{}
	}
	fileSkills := LoadFileSkills(cfg)
	return MergeSkills(configSkills, fileSkills)
}

// GetSkill returns a skill by name, searching both config and file-based skills.
func GetSkill(cfg *AppConfig, name string) *SkillConfig {
	for i, s := range cfg.Skills {
		if s.Name == name {
			return &cfg.Skills[i]
		}
	}
	// Also search file-based skills.
	fileSkills := LoadFileSkills(cfg)
	for i, s := range fileSkills {
		if s.Name == name {
			return &fileSkills[i]
		}
	}
	return nil
}

// TestSkill runs a skill with a quick timeout to verify it works.
func TestSkill(ctx context.Context, skill SkillConfig) (*SkillResult, error) {
	skill.Timeout = "5s"
	return ExecuteSkill(ctx, skill, nil)
}

// resolveEnvRef resolves a value that may reference an environment variable.
// If the value looks like $VARNAME or ${VARNAME}, it is expanded from the OS env.
// The fieldName is used for logging purposes.
func resolveEnvRef(value, fieldName string) string {
	return os.ExpandEnv(value)
}

// TaskSignals holds observable signals from a completed agent task.
// Used by ShouldExtractSkill to decide if a learned skill should be extracted.
//
// Current population status (dispatch/shouldExtractSkill):
//   - ToolCallCount: populated via regex count of `"type":"tool_use"` markers.
//   - TaskPrompt / AgentRole: populated directly from the task.
//   - ErrorRecovery / UserCorrection: reserved; not yet populated. Kept as
//     real gate conditions so future providers (or an error-pattern classifier
//     over result.Output) can opt in without a skill.go API change. Tests
//     assert the gate still fires on these flags to prevent regressions.
type TaskSignals struct {
	ToolCallCount  int    // total tool calls made during the task
	ErrorRecovery  bool   // reserved — true if agent recovered from ≥1 error
	UserCorrection bool   // reserved — true if user corrected the agent during the task
	TaskPrompt     string // original task prompt (used for duplicate detection)
	AgentRole      string // agent that completed the task
}

// ShouldExtractSkill returns true if the task signals warrant auto-extracting
// a learned skill. Any one condition is sufficient:
//   - 5+ tool calls (complex multi-step workflow)
//   - error recovery occurred
//   - user corrected the agent
//
// Returns false if an existing file skill already covers this workflow
// (checked via historical prompt overlap) to avoid redundant extractions.
func ShouldExtractSkill(cfg *AppConfig, signals TaskSignals) bool {
	triggered := signals.ToolCallCount >= 5 || signals.ErrorRecovery || signals.UserCorrection
	if !triggered {
		return false
	}
	// Skip if an existing skill was already created for a similar workflow.
	if signals.TaskPrompt != "" && cfg.HistoryDB != "" {
		existing := SuggestSkillsForPrompt(cfg.HistoryDB, signals.TaskPrompt, 1)
		if len(existing) > 0 {
			return false
		}
	}
	return true
}
