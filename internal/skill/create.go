package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// --- P18.4: Self-Improving Skills ---

// skillsFileCache caches the result of LoadFileSkills per skills directory.
// Invalidated explicitly by any function that mutates the skill store.
var (
	skillsCacheMu sync.RWMutex
	skillsCacheMap = make(map[string][]SkillConfig)
)

// invalidateSkillsCache removes the cached result for the given config's skills dir.
// Must be called after any operation that adds, removes, or modifies file-based skills.
func invalidateSkillsCache(cfg *AppConfig) {
	dir := SkillsDir(cfg)
	skillsCacheMu.Lock()
	delete(skillsCacheMap, dir)
	skillsCacheMu.Unlock()
}

// SkillMetadata is stored as metadata.json in each skill directory.
type SkillMetadata struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Matcher     *SkillMatcher     `json:"matcher,omitempty"`
	Example      string            `json:"example,omitempty"`
	AllowedTools []string          `json:"allowedTools,omitempty"`
	CreatedBy    string            `json:"createdBy,omitempty"`
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

// IsValidSkillName checks that a skill name is safe and valid.
// Must be alphanumeric + hyphens only, no path traversal, max 64 chars.
func IsValidSkillName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	// Reject path traversal.
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	return skillNameRegex.MatchString(name)
}

// SkillsDir returns the path to the file-based skills directory.
// Uses WorkspaceDir/skills/ (consistent with memory/rules/knowledge),
// falling back to BaseDir/skills/ for tests that only set BaseDir.
func SkillsDir(cfg *AppConfig) string {
	if cfg.WorkspaceDir != "" {
		return filepath.Join(cfg.WorkspaceDir, "skills")
	}
	return filepath.Join(cfg.BaseDir, "skills")
}

// CreateSkill creates a new file-based skill with metadata and script.
func CreateSkill(cfg *AppConfig, meta SkillMetadata, script string) error {
	if !IsValidSkillName(meta.Name) {
		return fmt.Errorf("invalid skill name %q: must be alphanumeric+hyphens, max 64 chars", meta.Name)
	}

	// Check max skills limit.
	existing := LoadFileSkills(cfg)
	if len(existing) >= cfg.SkillStore.maxSkillsOrDefault() {
		return fmt.Errorf("max skills limit reached (%d)", cfg.SkillStore.maxSkillsOrDefault())
	}

	// Check for duplicates among file skills.
	for _, s := range existing {
		if s.Name == meta.Name {
			return fmt.Errorf("skill %q already exists", meta.Name)
		}
	}

	dir := filepath.Join(SkillsDir(cfg), meta.Name)
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

	invalidateSkillsCache(cfg)
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

// LoadFileSkills scans the skills directory and returns all file-based skills.
// Skills with metadata.json (approved=true) are loaded first. Skills that only
// have a SKILL.md with YAML frontmatter are also loaded as doc-only skills.
// metadata.json takes priority: if a directory has both, metadata.json wins.
//
// Results are cached per skills directory and invalidated by CreateSkill,
// ApproveSkill, and DeleteFileSkill. Callers outside this package that mutate
// the skills directory directly should call InvalidateSkillsCache.
func LoadFileSkills(cfg *AppConfig) []SkillConfig {
	dir := SkillsDir(cfg)

	// Fast path: return cached result if available.
	skillsCacheMu.RLock()
	if cached, ok := skillsCacheMap[dir]; ok {
		result := make([]SkillConfig, len(cached))
		copy(result, cached)
		skillsCacheMu.RUnlock()
		return result
	}
	skillsCacheMu.RUnlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var skills []SkillConfig
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(dir, entry.Name())

		// --- metadata.json path (existing behaviour, takes priority) ---
		metaPath := filepath.Join(skillDir, "metadata.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta SkillMetadata
			if json.Unmarshal(data, &meta) != nil {
				continue
			}
			// Only include approved skills.
			if !meta.Approved {
				continue
			}
			sc := SkillConfig{
				Name:         meta.Name,
				Description:  meta.Description,
				Command:      meta.Command,
				Args:         meta.Args,
				Env:          meta.Env,
				Matcher:      meta.Matcher,
				Example:      meta.Example,
				AllowedTools: meta.AllowedTools,
				Workdir:      skillDir,
			}
			// Detect SKILL.md for Tier 2 doc injection.
			skillMDPath := filepath.Join(skillDir, "SKILL.md")
			if info, err := os.Stat(skillMDPath); err == nil {
				sc.DocPath = skillMDPath
				sc.DocSize = int(info.Size())
			}
			// Detect validation script.
			scriptGlob := filepath.Join(skillDir, "scripts", "validate.*")
			if matches, _ := filepath.Glob(scriptGlob); len(matches) > 0 {
				sc.ValidationScript = matches[0]
			}
			// Fallback: parse allowed-tools from SKILL.md frontmatter if not set in metadata.json.
			if sc.DocPath != "" && len(sc.AllowedTools) == 0 {
				if tools := parseAllowedToolsFromFrontmatter(sc.DocPath); len(tools) > 0 {
					sc.AllowedTools = tools
				}
			}
			skills = append(skills, sc)
			continue
		}

		// --- SKILL.md-only path (doc-only skill from frontmatter) ---
		if sc := loadSkillFromFrontmatter(skillDir); sc != nil {
			skills = append(skills, *sc)
		}
	}
	// --- Also scan skills/learned/ directory for agent-extracted skills pending review ---
	//
	// Security gate: learned/ is populated by LLM extraction (CreateLearnedSkill)
	// and every entry starts with approved=false. Only explicitly-approved
	// entries may be surfaced to AutoInjectLearnedSkills / downstream prompts.
	// metadata.json is the canonical approval record; a missing or unparseable
	// metadata.json is treated as unapproved (fail-closed).
	learnedDir := filepath.Join(dir, "learned")
	learnedEntries, err := os.ReadDir(learnedDir)
	if err == nil {
		for _, entry := range learnedEntries {
			if !entry.IsDir() {
				continue
			}
			skillDir := filepath.Join(learnedDir, entry.Name())
			// Fail-closed: require metadata.json with approved=true. Frontmatter
			// alone is not sufficient (it carries no approval field).
			metaPath := filepath.Join(skillDir, "metadata.json")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta SkillMetadata
			if json.Unmarshal(data, &meta) != nil {
				continue
			}
			if !meta.Approved {
				continue
			}
			// Prefer frontmatter-derived SkillConfig for learned skills (it
			// captures triggers, allowed-tools, etc.); fall back to metadata.json.
			if sc := loadSkillFromFrontmatter(skillDir); sc != nil {
				sc.Learned = true
				skills = append(skills, *sc)
				continue
			}
			sc := SkillConfig{
				Name:        meta.Name,
				Description: meta.Description,
				Command:     meta.Command,
				Args:        meta.Args,
				Env:         meta.Env,
				Matcher:     meta.Matcher,
				Example:     meta.Example,
				Workdir:     skillDir,
				Learned:     true,
			}
			skillMDPath := filepath.Join(skillDir, "SKILL.md")
			if info, err := os.Stat(skillMDPath); err == nil {
				sc.DocPath = skillMDPath
				sc.DocSize = int(info.Size())
			}
			skills = append(skills, sc)
		}
	}

	// Populate cache.
	skillsCacheMu.Lock()
	skillsCacheMap[dir] = skills
	skillsCacheMu.Unlock()

	return skills
}

// InvalidateSkillsCache clears the LoadFileSkills cache for the given config's
// skills directory. Useful for callers that modify the skills directory outside
// of CreateSkill / ApproveSkill / DeleteFileSkill.
func InvalidateSkillsCache(cfg *AppConfig) {
	invalidateSkillsCache(cfg)
}

// loadSkillFromFrontmatter reads a SKILL.md file and parses its YAML frontmatter
// to produce a doc-only SkillConfig. Returns nil if no valid frontmatter is found.
// Parsing is manual (no yaml import) to preserve zero-dependency constraint.
func loadSkillFromFrontmatter(skillDir string) *SkillConfig {
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(skillMDPath)
	if err != nil {
		return nil
	}

	content := string(data)

	// Frontmatter must start at the very beginning with "---".
	if !strings.HasPrefix(content, "---") {
		return nil
	}

	// Find the closing "---" delimiter.
	rest := content[3:]
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return nil
	}
	frontmatter := rest[:end]

	// Parse key: value lines. Multi-line values and anchors are not supported —
	// SKILL.md frontmatter is intentionally simple.
	parsed := parseFrontmatterKV(frontmatter)

	name := strings.TrimSpace(parsed["name"])
	if name == "" {
		// Fall back to directory name so the skill is still registered.
		name = filepath.Base(skillDir)
	}

	description := strings.TrimSpace(parsed["description"])

	// Parse triggers array: [a, b, c] or multiline list.
	var keywords []string
	if raw, ok := parsed["triggers"]; ok {
		keywords = parseFrontmatterList(raw)
	}

	var matcher *SkillMatcher
	if len(keywords) > 0 {
		matcher = &SkillMatcher{Keywords: keywords}
	}

	info, err := os.Stat(skillMDPath)
	if err != nil {
		return nil
	}

	return &SkillConfig{
		Name:        name,
		Description: description,
		Matcher:     matcher,
		// No Command — doc-only skill; agent reads SKILL.md directly.
		DocPath: skillMDPath,
		DocSize: int(info.Size()),
	}
}

// parseFrontmatterKV parses a simple YAML block into a string map.
// Supports single-line scalar values only (sufficient for SKILL.md frontmatter).
func parseFrontmatterKV(block string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(block, "\n") {
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if key != "" {
			result[key] = val
		}
	}
	return result
}

// parseFrontmatterList parses a YAML inline sequence "[a, b, c]" or a bare
// comma-separated string into a slice of trimmed strings.
func parseFrontmatterList(raw string) []string {
	raw = strings.TrimSpace(raw)
	// Strip surrounding brackets if present.
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = raw[1 : len(raw)-1]
	}
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Strip surrounding quotes if any.
		p = strings.Trim(p, `"'`)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseAllowedToolsFromFrontmatter extracts allowed-tools from SKILL.md YAML frontmatter.
// Supports both inline format: `allowed-tools: [Bash, Read, Grep]`
// and multi-line list format:
//
//	allowed-tools:
//	  - Bash
//	  - Read
func parseAllowedToolsFromFrontmatter(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return nil
	}
	parts := strings.SplitN(text, "---", 3)
	if len(parts) < 3 {
		return nil
	}

	frontmatter := strings.TrimSpace(parts[1])
	var tools []string
	inAllowedTools := false

	for _, line := range strings.Split(frontmatter, "\n") {
		stripped := strings.TrimSpace(line)
		if strings.HasPrefix(stripped, "- ") && inAllowedTools {
			item := strings.TrimSpace(strings.TrimPrefix(stripped, "- "))
			item = strings.Trim(item, `"'`)
			if item != "" {
				tools = append(tools, item)
			}
			continue
		}
		if idx := strings.Index(stripped, ":"); idx > 0 {
			key := strings.TrimSpace(stripped[:idx])
			val := strings.TrimSpace(stripped[idx+1:])
			if key == "allowed-tools" {
				inAllowedTools = true
				// Inline format: [Bash, Read, Grep]
				if strings.HasPrefix(val, "[") {
					val = strings.Trim(val, "[]")
					for _, item := range strings.Split(val, ",") {
						item = strings.TrimSpace(item)
						item = strings.Trim(item, `"'`)
						if item != "" {
							tools = append(tools, item)
						}
					}
					return tools
				}
			} else {
				inAllowedTools = false
			}
		}
	}
	return tools
}

// LoadAllFileSkillMetas scans the skills directory and returns all metadata (including unapproved).
func LoadAllFileSkillMetas(cfg *AppConfig) []SkillMetadata {
	dir := SkillsDir(cfg)
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

// MergeSkills merges config-based skills and file-based skills.
// Config-based skills take priority on name collision.
func MergeSkills(configSkills, fileSkills []SkillConfig) []SkillConfig {
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

// ApproveSkill sets Approved=true for a file-based skill and makes its script executable.
func ApproveSkill(cfg *AppConfig, name string) error {
	if !IsValidSkillName(name) {
		return fmt.Errorf("invalid skill name %q", name)
	}

	metaPath := filepath.Join(SkillsDir(cfg), name, "metadata.json")
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
	scriptPath := filepath.Join(SkillsDir(cfg), name, scriptFilename(meta.Command))
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

	invalidateSkillsCache(cfg)
	logInfo("skill approved", "name", name)
	return nil
}

// RejectSkill deletes a file-based skill directory (rejection removes it entirely).
func RejectSkill(cfg *AppConfig, name string) error {
	return DeleteFileSkill(cfg, name)
}

// DeleteFileSkill removes a file-based skill directory.
func DeleteFileSkill(cfg *AppConfig, name string) error {
	if !IsValidSkillName(name) {
		return fmt.Errorf("invalid skill name %q", name)
	}

	dir := filepath.Join(SkillsDir(cfg), name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("skill %q not found", name)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}

	invalidateSkillsCache(cfg)
	logInfo("skill deleted", "name", name)
	return nil
}

// RecordSkillUsage updates UsageCount and LastUsedAt in a file-based skill's metadata.
func RecordSkillUsage(cfg *AppConfig, name string) {
	metaPath := filepath.Join(SkillsDir(cfg), name, "metadata.json")
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

// ListPendingSkills returns skills that have not been approved.
func ListPendingSkills(cfg *AppConfig) []SkillMetadata {
	all := LoadAllFileSkillMetas(cfg)
	var pending []SkillMetadata
	for _, m := range all {
		if !m.Approved {
			pending = append(pending, m)
		}
	}
	return pending
}

// Context keys for passing task info to tool handlers.
type ctxKey string

const (
	ctxKeyRole   ctxKey = "role"
	ctxKeyPrompt ctxKey = "prompt"
)

// CreateSkillToolHandler is the tool handler for the create_skill built-in tool.
func CreateSkillToolHandler(ctx context.Context, cfg *AppConfig, input json.RawMessage) (string, error) {
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
	if !IsValidSkillName(args.Name) {
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

	if err := CreateSkill(cfg, meta, args.Script); err != nil {
		return "", err
	}

	// Write SKILL.md if doc content was provided.
	if args.Doc != "" {
		skillMDPath := filepath.Join(SkillsDir(cfg), args.Name, "SKILL.md")
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
		RecordSkillEvent(cfg.HistoryDB, args.Name, "created", prompt, meta.CreatedBy)
	}

	status := "created (pending approval)"
	if meta.Approved {
		status = "created (auto-approved)"
	}

	result := map[string]any{
		"name":   args.Name,
		"status": status,
		"path":   filepath.Join(SkillsDir(cfg), args.Name),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// LearnedSkillSpec is the input for creating an auto-extracted learned skill.
type LearnedSkillSpec struct {
	Name        string   // skill name (alphanumeric+hyphens, max 64 chars)
	Description string   // one-line description
	Triggers    []string // keywords that trigger injection
	Doc         string   // SKILL.md body content (below frontmatter)
	CreatedBy   string   // agent role that extracted the skill
}

// CreateLearnedSkill writes a SKILL.md + metadata.json to skills/learned/{name}/.
// The skill is created with approved=false (pending human review).
// Format matches workspace/skills/learned/char-pose-gen/ as the canonical example.
func CreateLearnedSkill(cfg *AppConfig, spec LearnedSkillSpec) error {
	if !IsValidSkillName(spec.Name) {
		return fmt.Errorf("invalid skill name %q: must be alphanumeric+hyphens, max 64 chars", spec.Name)
	}

	learnedDir := filepath.Join(SkillsDir(cfg), "learned", spec.Name)
	if _, err := os.Stat(learnedDir); err == nil {
		return fmt.Errorf("learned skill %q already exists", spec.Name)
	}

	if err := os.MkdirAll(learnedDir, 0o755); err != nil {
		return fmt.Errorf("create learned skill dir: %w", err)
	}

	// Build SKILL.md with frontmatter matching char-pose-gen format.
	// The source of Description/Triggers is LLM output (Haiku); sanitize
	// aggressively before writing to prevent YAML-frontmatter injection:
	//   - Description: collapse any newline/CR into a single space so the
	//     scalar stays on one line and can't terminate the frontmatter block.
	//   - Triggers: accept only [A-Za-z0-9_-]; drop anything else so a
	//     rogue comma/`]`/quote cannot corrupt the inline array. Also dedup
	//     and cap length to keep the file compact.
	desc := sanitizeFrontmatterScalar(spec.Description)
	triggers := sanitizeFrontmatterTriggers(spec.Triggers)

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + spec.Name + "\n")
	if desc != "" {
		sb.WriteString("description: " + desc + "\n")
	}
	if len(triggers) > 0 {
		sb.WriteString("triggers: [" + strings.Join(triggers, ", ") + "]\n")
	}
	// CreatedBy is normally an internal agent name, but defense-in-depth:
	// apply the same newline-collapse as Description so a hypothetical
	// future caller feeding external input cannot break the frontmatter.
	if createdBy := sanitizeFrontmatterScalar(spec.CreatedBy); createdBy != "" {
		sb.WriteString("maintainer: " + createdBy + "\n")
	}
	sb.WriteString("---\n")
	if spec.Doc != "" {
		sb.WriteString("\n")
		sb.WriteString(strings.TrimSpace(spec.Doc))
		sb.WriteString("\n")
	}

	skillMDPath := filepath.Join(learnedDir, "SKILL.md")
	if err := os.WriteFile(skillMDPath, []byte(sb.String()), 0o644); err != nil {
		os.RemoveAll(learnedDir)
		return fmt.Errorf("write SKILL.md: %w", err)
	}

	// Build metadata.json (approved=false — requires human review before use).
	meta := SkillMetadata{
		Name:        spec.Name,
		Description: spec.Description,
		CreatedBy:   spec.CreatedBy,
		Approved:    false,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if len(spec.Triggers) > 0 {
		meta.Matcher = &SkillMatcher{Keywords: spec.Triggers}
	}

	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		os.RemoveAll(learnedDir)
		return fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(learnedDir, "metadata.json"), metaData, 0o644); err != nil {
		os.RemoveAll(learnedDir)
		return fmt.Errorf("write metadata.json: %w", err)
	}

	invalidateSkillsCache(cfg)
	logInfo("learned skill extracted", "name", spec.Name, "createdBy", spec.CreatedBy)
	return nil
}

// sanitizeFrontmatterScalar collapses any embedded newline/CR in a
// frontmatter scalar value (description, etc.) to a single space, preventing
// untrusted input (LLM output) from terminating the YAML frontmatter block.
func sanitizeFrontmatterScalar(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

// sanitizeFrontmatterTriggers restricts each trigger to a safe charset
// (alphanumeric + `_-`), drops empty/duplicate entries, and caps the total
// count. This protects the inline-array frontmatter form against injection
// from LLM-supplied values containing `,`, `]`, `'`, or `"`.
func sanitizeFrontmatterTriggers(in []string) []string {
	const maxTriggers = 16
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		clean := strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z':
				return r
			case r >= 'A' && r <= 'Z':
				return r
			case r >= '0' && r <= '9':
				return r
			case r == '-' || r == '_':
				return r
			default:
				return -1
			}
		}, t)
		if clean == "" {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
		if len(out) >= maxTriggers {
			break
		}
	}
	return out
}

