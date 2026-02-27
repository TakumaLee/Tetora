package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// --- P18.4: Self-Improving Skills ---

// SkillMetadata is stored as metadata.json in each skill directory.
type SkillMetadata struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Matcher     *SkillMatcher     `json:"matcher,omitempty"`
	Example     string            `json:"example,omitempty"`
	CreatedBy   string            `json:"createdBy,omitempty"`
	Approved    bool              `json:"approved"`
	Sandbox     bool              `json:"sandbox,omitempty"`
	CreatedAt   string            `json:"createdAt"`
	UsageCount  int               `json:"usageCount,omitempty"`
	LastUsedAt  string            `json:"lastUsedAt,omitempty"`
}

// SkillStoreConfig configures the self-improving skill store.
type SkillStoreConfig struct {
	AutoApprove bool `json:"autoApprove,omitempty"` // skip approval for agent-created skills
	Sandbox     bool `json:"sandbox,omitempty"`     // default to sandbox execution for created skills
	MaxSkills   int  `json:"maxSkills,omitempty"`   // max file-based skills (default 50)
}

// maxSkillsOrDefault returns the configured max skills limit (default 50).
func (c SkillStoreConfig) maxSkillsOrDefault() int {
	if c.MaxSkills > 0 {
		return c.MaxSkills
	}
	return 50
}

// skillNameRegex validates skill names: alphanumeric and hyphens only.
var skillNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// isValidSkillName checks that a skill name is safe and valid.
// Must be alphanumeric + hyphens only, no path traversal, max 64 chars.
func isValidSkillName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	// Reject path traversal.
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	return skillNameRegex.MatchString(name)
}

// skillsDir returns the path to the file-based skills directory.
// Uses WorkspaceDir/skills/ (consistent with memory/rules/knowledge),
// falling back to baseDir/skills/ for tests that only set baseDir.
func skillsDir(cfg *Config) string {
	if cfg.WorkspaceDir != "" {
		return filepath.Join(cfg.WorkspaceDir, "skills")
	}
	return filepath.Join(cfg.baseDir, "skills")
}

// createSkill creates a new file-based skill with metadata and script.
func createSkill(cfg *Config, meta SkillMetadata, script string) error {
	if !isValidSkillName(meta.Name) {
		return fmt.Errorf("invalid skill name %q: must be alphanumeric+hyphens, max 64 chars", meta.Name)
	}

	// Check max skills limit.
	existing := loadFileSkills(cfg)
	if len(existing) >= cfg.SkillStore.maxSkillsOrDefault() {
		return fmt.Errorf("max skills limit reached (%d)", cfg.SkillStore.maxSkillsOrDefault())
	}

	// Check for duplicates among file skills.
	for _, s := range existing {
		if s.Name == meta.Name {
			return fmt.Errorf("skill %q already exists", meta.Name)
		}
	}

	dir := filepath.Join(skillsDir(cfg), meta.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
	}

	// Set creation timestamp.
	if meta.CreatedAt == "" {
		meta.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	// Write script file.
	scriptPath := filepath.Join(dir, scriptFilename(meta.Command))
	perm := os.FileMode(0o644)
	if meta.Approved {
		perm = 0o755
	}
	if err := os.WriteFile(scriptPath, []byte(script), perm); err != nil {
		os.RemoveAll(dir) // cleanup on failure
		return fmt.Errorf("write script: %w", err)
	}

	// Write metadata.
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), metaData, 0o644); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("write metadata: %w", err)
	}

	logInfo("skill created", "name", meta.Name, "approved", meta.Approved, "createdBy", meta.CreatedBy)
	return nil
}

// scriptFilename returns the script filename based on the command.
func scriptFilename(command string) string {
	if strings.Contains(command, "python") {
		return "run.py"
	}
	return "run.sh"
}

// loadFileSkills scans the skills directory and returns all file-based skills.
// Only approved skills are returned as usable SkillConfigs.
func loadFileSkills(cfg *Config) []SkillConfig {
	dir := skillsDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var skills []SkillConfig
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(dir, entry.Name(), "metadata.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var meta SkillMetadata
		if json.Unmarshal(data, &meta) != nil {
			continue
		}

		// Only include approved skills.
		if !meta.Approved {
			continue
		}

		skillDir := filepath.Join(dir, entry.Name())
		sc := SkillConfig{
			Name:        meta.Name,
			Description: meta.Description,
			Command:     meta.Command,
			Args:        meta.Args,
			Env:         meta.Env,
			Matcher:     meta.Matcher,
			Example:     meta.Example,
			Workdir:     skillDir,
		}

		// Detect SKILL.md for Tier 2 doc injection.
		skillMDPath := filepath.Join(skillDir, "SKILL.md")
		if info, err := os.Stat(skillMDPath); err == nil {
			sc.DocPath = skillMDPath
			sc.DocSize = int(info.Size())
		}

		skills = append(skills, sc)
	}
	return skills
}

// loadAllFileSkillMetas scans the skills directory and returns all metadata (including unapproved).
func loadAllFileSkillMetas(cfg *Config) []SkillMetadata {
	dir := skillsDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var metas []SkillMetadata
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(dir, entry.Name(), "metadata.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var meta SkillMetadata
		if json.Unmarshal(data, &meta) != nil {
			continue
		}
		metas = append(metas, meta)
	}
	return metas
}

// mergeSkills merges config-based skills and file-based skills.
// Config-based skills take priority on name collision.
func mergeSkills(configSkills, fileSkills []SkillConfig) []SkillConfig {
	seen := make(map[string]bool, len(configSkills))
	result := make([]SkillConfig, 0, len(configSkills)+len(fileSkills))

	for _, s := range configSkills {
		seen[s.Name] = true
		result = append(result, s)
	}
	for _, s := range fileSkills {
		if !seen[s.Name] {
			result = append(result, s)
		}
	}
	return result
}

// approveSkill sets Approved=true for a file-based skill and makes its script executable.
func approveSkill(cfg *Config, name string) error {
	if !isValidSkillName(name) {
		return fmt.Errorf("invalid skill name %q", name)
	}

	metaPath := filepath.Join(skillsDir(cfg), name, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("skill %q not found: %w", name, err)
	}

	var meta SkillMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("parse metadata: %w", err)
	}

	if meta.Approved {
		return fmt.Errorf("skill %q is already approved", name)
	}

	meta.Approved = true

	// Make script executable.
	scriptPath := filepath.Join(skillsDir(cfg), name, scriptFilename(meta.Command))
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		logWarn("chmod script failed", "path", scriptPath, "error", err)
	}

	newData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, newData, 0o644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	logInfo("skill approved", "name", name)
	return nil
}

// rejectSkill deletes a file-based skill directory (rejection removes it entirely).
func rejectSkill(cfg *Config, name string) error {
	return deleteFileSkill(cfg, name)
}

// deleteFileSkill removes a file-based skill directory.
func deleteFileSkill(cfg *Config, name string) error {
	if !isValidSkillName(name) {
		return fmt.Errorf("invalid skill name %q", name)
	}

	dir := filepath.Join(skillsDir(cfg), name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("skill %q not found", name)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}

	logInfo("skill deleted", "name", name)
	return nil
}

// recordSkillUsage updates UsageCount and LastUsedAt in a file-based skill's metadata.
func recordSkillUsage(cfg *Config, name string) {
	metaPath := filepath.Join(skillsDir(cfg), name, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return // not a file-based skill, ignore
	}

	var meta SkillMetadata
	if json.Unmarshal(data, &meta) != nil {
		return
	}

	meta.UsageCount++
	meta.LastUsedAt = time.Now().UTC().Format(time.RFC3339)

	newData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(metaPath, newData, 0o644)
}

// listPendingSkills returns skills that have not been approved.
func listPendingSkills(cfg *Config) []SkillMetadata {
	all := loadAllFileSkillMetas(cfg)
	var pending []SkillMetadata
	for _, m := range all {
		if !m.Approved {
			pending = append(pending, m)
		}
	}
	return pending
}

// createSkillToolHandler is the tool handler for the create_skill built-in tool.
func createSkillToolHandler(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name        string        `json:"name"`
		Description string        `json:"description"`
		Script      string        `json:"script"`
		Language    string        `json:"language"`
		Matcher     *SkillMatcher `json:"matcher"`
		Doc         string        `json:"doc"` // optional SKILL.md content
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if args.Description == "" {
		return "", fmt.Errorf("description is required")
	}
	if args.Script == "" {
		return "", fmt.Errorf("script is required")
	}
	if !isValidSkillName(args.Name) {
		return "", fmt.Errorf("invalid skill name %q: alphanumeric and hyphens only, max 64 chars", args.Name)
	}

	// Default language to bash.
	if args.Language == "" {
		args.Language = "bash"
	}
	if args.Language != "bash" && args.Language != "python" {
		return "", fmt.Errorf("language must be 'bash' or 'python', got %q", args.Language)
	}

	// Build metadata.
	meta := SkillMetadata{
		Name:        args.Name,
		Description: args.Description,
		Matcher:     args.Matcher,
		Approved:    cfg.SkillStore.AutoApprove,
		Sandbox:     cfg.SkillStore.Sandbox,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	// Extract createdBy from context if available.
	if role, ok := ctx.Value(ctxKeyRole).(string); ok {
		meta.CreatedBy = role
	}

	switch args.Language {
	case "python":
		meta.Command = "python3"
		meta.Args = []string{"run.py"}
	default:
		meta.Command = "./run.sh"
	}

	if err := createSkill(cfg, meta, args.Script); err != nil {
		return "", err
	}

	// Write SKILL.md if doc content was provided.
	if args.Doc != "" {
		skillMDPath := filepath.Join(skillsDir(cfg), args.Name, "SKILL.md")
		if err := os.WriteFile(skillMDPath, []byte(args.Doc), 0o644); err != nil {
			logWarn("failed to write SKILL.md", "skill", args.Name, "error", err)
		}
	}

	// Record the creation event.
	if cfg.HistoryDB != "" {
		prompt := ""
		if p, ok := ctx.Value(ctxKeyPrompt).(string); ok {
			prompt = p
		}
		recordSkillEvent(cfg.HistoryDB, args.Name, "created", prompt, meta.CreatedBy)
	}

	status := "created (pending approval)"
	if meta.Approved {
		status = "created (auto-approved)"
	}

	result := map[string]any{
		"name":   args.Name,
		"status": status,
		"path":   filepath.Join(skillsDir(cfg), args.Name),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// Context keys for passing task info to tool handlers.
type ctxKey string

const (
	ctxKeyRole   ctxKey = "role"
	ctxKeyPrompt ctxKey = "prompt"
)
