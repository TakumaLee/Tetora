package dispatch

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"tetora/internal/config"
	"tetora/internal/log"
	"tetora/internal/trace"
)

// DefaultFallbackModel is the model used when no model is configured anywhere.
const DefaultFallbackModel = "claude-sonnet-4-6"

// FillDefaults populates empty Task fields with sensible defaults from config.
func FillDefaults(cfg *config.Config, t *Task) {
	if t.ID == "" {
		t.ID = trace.NewUUID()
	}
	if t.SessionID == "" {
		t.SessionID = trace.NewUUID()
	}
	if t.Model == "" {
		t.Model = cfg.DefaultModel
	}
	if t.Timeout == "" {
		if t.Prompt != "" {
			t.Timeout = EstimateTimeout(t.Prompt)
		} else {
			t.Timeout = cfg.DefaultTimeout
		}
	}
	if t.Budget == 0 {
		t.Budget = cfg.DefaultBudget
	}
	if t.PermissionMode == "" {
		t.PermissionMode = cfg.DefaultPermissionMode
	}
	if t.Workdir == "" {
		// Priority: agent's output dir (output-only agents only) > workspace dir > default workdir
		if t.Agent != "" && cfg.AgentOutputBase != "" {
			if rc, ok := cfg.Agents[t.Agent]; ok && rc.OutputOnly {
				t.Workdir = filepath.Join(cfg.AgentOutputBase, t.Agent, "outputs")
			} else if cfg.WorkspaceDir != "" {
				t.Workdir = cfg.WorkspaceDir
			} else {
				t.Workdir = cfg.DefaultWorkdir
			}
		} else if cfg.WorkspaceDir != "" {
			t.Workdir = cfg.WorkspaceDir
		} else {
			t.Workdir = cfg.DefaultWorkdir
		}
	}
	// Expand ~ in workdir.
	if strings.HasPrefix(t.Workdir, "~/") {
		home, _ := os.UserHomeDir()
		t.Workdir = filepath.Join(home, t.Workdir[2:])
	}
	if t.Name == "" {
		t.Name = fmt.Sprintf("task-%s", t.ID[:8])
	}
	// Sanitize prompt.
	if t.Prompt != "" {
		t.Prompt = SanitizePrompt(t.Prompt, cfg.MaxPromptLen)
	}
	// Resolve agent from system-wide default.
	if t.Agent == "" && cfg.DefaultAgent != "" {
		t.Agent = cfg.DefaultAgent
	}
	// Apply agent-specific overrides.
	ApplyAgentDefaults(cfg, t)

	// Final safety fallback: model must never be empty.
	if t.Model == "" {
		log.Warn("task model empty after defaults, falling back to sonnet", "agent", t.Agent)
		t.Model = DefaultFallbackModel
	}
}

// ApplyAgentDefaults applies agent-specific model and permission overrides to a task,
// but only if the task still has the global defaults (i.e. not explicitly set).
func ApplyAgentDefaults(cfg *config.Config, t *Task) {
	if t.Agent == "" {
		return
	}
	rc, ok := cfg.Agents[t.Agent]
	if !ok {
		return
	}
	// Agent model overrides: only apply if agent has explicit model (not "auto") AND task still uses global default.
	if rc.Model != "" && rc.Model != "auto" && t.Model == cfg.DefaultModel {
		t.Model = rc.Model
	}
	if rc.PermissionMode != "" && t.PermissionMode == cfg.DefaultPermissionMode {
		t.PermissionMode = rc.PermissionMode
	}
}

// EstimateTimeout infers an appropriate task timeout from the prompt content.
func EstimateTimeout(prompt string) string {
	p := strings.ToLower(prompt)

	heavyKeywords := []string{
		"refactor", "migrate", "migration", "全部", "整個", "整合", "架構",
		"rewrite", "overhaul", "all ", "entire", "全面",
	}
	for _, kw := range heavyKeywords {
		if strings.Contains(p, kw) {
			return "3h"
		}
	}

	buildKeywords := []string{
		"implement", "build", "create", "add ", "新增", "建立", "實作",
		"feature", "功能", "develop", "設計", "規劃",
	}
	for _, kw := range buildKeywords {
		if strings.Contains(p, kw) {
			return "2h"
		}
	}

	fixKeywords := []string{
		"fix", "bug", "修復", "update", "更新", "debug", "patch", "調整",
	}
	for _, kw := range fixKeywords {
		if strings.Contains(p, kw) {
			return "30m"
		}
	}

	queryKeywords := []string{
		"check", "查", "show", "list", "search", "analyze", "分析", "查看",
	}
	for _, kw := range queryKeywords {
		if strings.Contains(p, kw) {
			return "15m"
		}
	}

	return "2h"
}

// --- Prompt Sanitization ---

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// SanitizePrompt removes potentially dangerous content from prompt text.
func SanitizePrompt(input string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 102400
	}

	result := strings.ReplaceAll(input, "\x00", "")
	result = ansiEscapeRe.ReplaceAllString(result, "")

	if len(result) > maxLen {
		result = result[:maxLen]
		log.Warn("prompt truncated", "from", len(input), "to", maxLen)
	}

	if result != input && len(result) == len(input) {
		log.Warn("prompt sanitized, removed control characters")
	}

	return result
}
