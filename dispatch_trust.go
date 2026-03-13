package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// --- UUID ---

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// --- Task Defaults ---

// estimateTimeout infers an appropriate task timeout from the prompt content.
// This prevents long-running development tasks from being killed prematurely.
// The caller can always override by setting Task.Timeout explicitly.
func estimateTimeout(prompt string) string {
	p := strings.ToLower(prompt)

	// Heaviest tasks — large-scale refactors, migrations, multi-phase builds.
	heavyKeywords := []string{
		"refactor", "migrate", "migration", "全部", "整個", "整合", "架構",
		"rewrite", "overhaul", "all ", "entire", "全面",
	}
	for _, kw := range heavyKeywords {
		if strings.Contains(p, kw) {
			return "3h"
		}
	}

	// Medium-heavy — implementation, multi-file changes, feature builds.
	buildKeywords := []string{
		"implement", "build", "create", "add ", "新增", "建立", "實作",
		"feature", "功能", "develop", "設計", "規劃",
	}
	for _, kw := range buildKeywords {
		if strings.Contains(p, kw) {
			return "2h"
		}
	}

	// Light fixes — targeted bug fixes and updates.
	fixKeywords := []string{
		"fix", "bug", "修復", "update", "更新", "debug", "patch", "調整",
	}
	for _, kw := range fixKeywords {
		if strings.Contains(p, kw) {
			return "30m"
		}
	}

	// Read-only / query tasks.
	queryKeywords := []string{
		"check", "查", "show", "list", "search", "analyze", "分析", "查看",
	}
	for _, kw := range queryKeywords {
		if strings.Contains(p, kw) {
			return "15m"
		}
	}

	// Default: 2h — increased from 1h because complex tasks often exceed 1h.
	return "2h"
}

func fillDefaults(cfg *Config, t *Task) {
	if t.ID == "" {
		t.ID = newUUID()
	}
	if t.SessionID == "" {
		t.SessionID = newUUID()
	}
	if t.Model == "" {
		t.Model = cfg.DefaultModel
	}
	if t.Timeout == "" {
		// Use smart estimation from prompt; fall back to config default.
		if t.Prompt != "" {
			t.Timeout = estimateTimeout(t.Prompt)
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
		t.Workdir = cfg.DefaultWorkdir
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
		t.Prompt = sanitizePrompt(t.Prompt, cfg.MaxPromptLen)
	}
	// Resolve agent from system-wide default (not SmartDispatch — that's handled by the routing engine).
	if t.Agent == "" && cfg.DefaultAgent != "" {
		t.Agent = cfg.DefaultAgent
	}
	// Apply agent-specific overrides.
	applyAgentDefaults(cfg, t)
}

// applyAgentDefaults applies agent-specific model and permission overrides to a task,
// but only if the task still has the global defaults (i.e. not explicitly set).
func applyAgentDefaults(cfg *Config, t *Task) {
	if t.Agent == "" {
		return
	}
	rc, ok := cfg.Agents[t.Agent]
	if !ok {
		return
	}
	if rc.Model != "" && t.Model == cfg.DefaultModel {
		t.Model = rc.Model
	}
	if rc.PermissionMode != "" && t.PermissionMode == cfg.DefaultPermissionMode {
		t.PermissionMode = rc.PermissionMode
	}
}

// --- Prompt Sanitization ---

// ansiEscapeRe matches ANSI escape sequences.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// sanitizePrompt removes potentially dangerous content from prompt text.
// This performs structural sanitization only (null bytes, ANSI escapes, length).
// Content filtering is the LLM's responsibility.
func sanitizePrompt(input string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 102400
	}

	// Strip null bytes.
	result := strings.ReplaceAll(input, "\x00", "")

	// Strip ANSI escape sequences.
	result = ansiEscapeRe.ReplaceAllString(result, "")

	// Enforce max length.
	if len(result) > maxLen {
		result = result[:maxLen]
		logWarn("prompt truncated", "from", len(input), "to", maxLen)
	}

	if result != input && len(result) == len(input) {
		logWarn("prompt sanitized, removed control characters")
	}

	return result
}

// --- P21.2: Writing Style ---

// loadWritingStyle resolves writing style guidelines from config.
func loadWritingStyle(cfg *Config) string {
	if cfg.WritingStyle.FilePath != "" {
		data, err := os.ReadFile(cfg.WritingStyle.FilePath)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
		logWarn("failed to load writing style file", "path", cfg.WritingStyle.FilePath, "error", err)
	}
	return cfg.WritingStyle.Guidelines
}

// --- Directory Validation ---

// validateDirs checks that the task's workdir and addDirs are within allowed directories.
// If allowedDirs is empty, no restriction is applied (backward compatible).
// Agent-level allowedDirs takes precedence over config-level.
func validateDirs(cfg *Config, task Task, agentName string) error {
	// Determine which allowedDirs to use.
	var allowed []string
	if agentName != "" {
		if rc, ok := cfg.Agents[agentName]; ok && len(rc.AllowedDirs) > 0 {
			allowed = rc.AllowedDirs
		}
	}
	if len(allowed) == 0 {
		allowed = cfg.AllowedDirs
	}
	if len(allowed) == 0 {
		return nil // no restriction
	}

	// Normalize allowed dirs.
	normalized := make([]string, 0, len(allowed))
	for _, d := range allowed {
		if strings.HasPrefix(d, "~/") {
			home, _ := os.UserHomeDir()
			d = filepath.Join(home, d[2:])
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		normalized = append(normalized, abs+string(filepath.Separator))
	}

	check := func(dir, label string) error {
		if dir == "" {
			return nil
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("%s: cannot resolve path %q: %w", label, dir, err)
		}
		absWithSep := abs + string(filepath.Separator)
		for _, a := range normalized {
			if strings.HasPrefix(absWithSep, a) || abs == strings.TrimSuffix(a, string(filepath.Separator)) {
				return nil
			}
		}
		return fmt.Errorf("%s %q is not within allowedDirs", label, dir)
	}

	if err := check(task.Workdir, "workdir"); err != nil {
		return err
	}
	for _, d := range task.AddDirs {
		if err := check(d, "addDir"); err != nil {
			return err
		}
	}
	return nil
}

// --- Output Storage ---

// saveTaskOutput saves the raw claude output to a file in the outputs directory.
// Returns the filename (not full path) for storage in the history DB.
func saveTaskOutput(baseDir string, jobID string, stdout []byte) string {
	if len(stdout) == 0 || baseDir == "" {
		return ""
	}
	outputDir := filepath.Join(baseDir, "outputs")
	os.MkdirAll(outputDir, 0o755)

	ts := time.Now().Format("20060102-150405")
	// Use first 8 chars of jobID for readability.
	shortID := jobID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	filename := fmt.Sprintf("%s_%s.json", shortID, ts)
	filePath := filepath.Join(outputDir, filename)

	if err := os.WriteFile(filePath, stdout, 0o644); err != nil {
		logWarn("save output failed", "error", err)
		return ""
	}
	return filename
}

// cleanupOutputs removes output files older than the given number of days.
func cleanupOutputs(baseDir string, days int) {
	outputDir := filepath.Join(baseDir, "outputs")
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(outputDir, e.Name()))
		}
	}
}
