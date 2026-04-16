package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tetora/internal/classify"
	"tetora/internal/config"
	"tetora/internal/dispatch"
	"tetora/internal/knowledge"
	"tetora/internal/log"
)

// validScopeBoundaries enumerates the allowed values for task.ScopeBoundary.
// Empty is treated as "unset" (warning emitted, no injection).
var validScopeBoundaries = map[string]bool{
	"diagnostic_only":   true,
	"implement_allowed": true,
	"test_only":         true,
	"review_only":       true,
}

// skillExtractionSection is injected into every dispatched agent prompt.
// It mirrors workspace CLAUDE.md "Post-Task Skill Extraction" (authoritative source).
// Conditions mirror ShouldExtractSkill in internal/skill/skill.go.
const skillExtractionSection = "\n\n<!-- Post-Task Skill Extraction\n" +
	"完成任務後，符合以下任一條件時，提取 skill 到 `skills/learned/{name}/`：\n" +
	"- 5+ tool calls、error recovery、無既有 skill 的新 workflow、user 糾正過\n" +
	"- 格式：SKILL.md + metadata.json，寫到 `skills/learned/{name}/`\n" +
	"- 一次性操作或已有類似 skill → 不提取\n" +
	"-->"

// Deps holds root-level function callbacks required by BuildTieredPrompt.
// All fields are required; BuildTieredPrompt panics if any are nil.
type Deps struct {
	ResolveProviderName    func(cfg *config.Config, task dispatch.Task, agentName string) string
	LoadSoulFile           func(cfg *config.Config, agentName string) string
	LoadAgentPrompt        func(cfg *config.Config, agentName string) (string, error)
	ResolveWorkspace       func(cfg *config.Config, agentName string) config.WorkspaceConfig
	BuildReflectionContext func(dbPath, role string, limit int) string
	LoadWritingStyle       func(cfg *config.Config) string
	BuildSkillsPrompt          func(cfg *config.Config, task dispatch.Task, complexity classify.Complexity) string
	// BuildSkillsPromptWithMeta is optional. If non-nil, used instead of BuildSkillsPrompt
	// so the manifest can record matched skill names.
	BuildSkillsPromptWithMeta func(cfg *config.Config, task dispatch.Task, complexity classify.Complexity) (string, []string)
	CollectSkillAllowedTools   func(cfg *config.Config, task dispatch.Task) []string
	InjectWorkspaceContent     func(cfg *config.Config, task *dispatch.Task, agentName string)
	EstimateDirSize            func(dir string) int
}

// BuildScopeBlock returns the SCOPE HEADER text for a given scope_boundary value.
// Empty string = no scope declared; returns "" so caller can no-op.
// The header is designed to be prepended to the user prompt so it applies to all
// providers (claude-code, codex-cli, API-based) uniformly, since those providers
// differ in whether system prompt is honored.
func BuildScopeBlock(scope string) string {
	switch scope {
	case "diagnostic_only":
		return "⛔ SCOPE: diagnostic_only\n" +
			"本任務禁止任何 production 檔案寫入。\n" +
			"允許：Read、Grep、Glob、Bash（唯讀指令）、DB 查詢。\n" +
			"禁止：Edit、Write、git commit、git push、任何 production 檔案修改。\n" +
			"若發現可改善之處 → 記錄至 task comment，開新票，不在本任務實作。\n" +
			"違反此範疇會被標記為 DONE_WITH_CONCERNS。\n"
	case "test_only":
		return "⚠️ SCOPE: test_only\n" +
			"本任務僅允許測試檔案寫入。\n" +
			"允許：寫測試檔案（*.test.*、*.spec.*、tests/、__tests__/）、執行測試、更新 test fixtures。\n" +
			"禁止：修改 production 程式碼（非測試檔案）。\n" +
			"違反此範疇會被標記為 DONE_WITH_CONCERNS。\n"
	case "review_only":
		return "🔍 SCOPE: review_only\n" +
			"本任務僅允許讀取與審閱。\n" +
			"允許：Read、Grep、Glob、產出 review 報告至 task comment。\n" +
			"禁止：任何寫入操作（Edit、Write、git commit 等）。\n" +
			"輸出：純文字 review report，附建議開票的 action items。\n" +
			"違反此範疇會被標記為 DONE_WITH_CONCERNS。\n"
	case "implement_allowed":
		return "⚠️ SCOPE: implement_allowed\n" +
			"本任務允許實作，但限於 spec 中 critical_files 指定範圍。\n" +
			"允許：Edit、Write、git commit（限 critical_files）。\n" +
			"禁止：超出 critical_files 範圍的大規模重構、非必要的週邊修改。\n" +
			"仍需遵守 OUT OF SCOPE 清單。\n"
	}
	return ""
}

// BuildTieredPrompt constructs a system prompt based on request complexity.
// It replaces the inline assembly in runTask() and runSingleTask().
//
// Tiering strategy:
//
//	Simple:   soul (truncated 4KB) only — no reflection, style, citation, rules, knowledge
//	Standard: full soul + 1 reflection + citation + rules index + knowledge index
//	Complex:  full soul + 3 reflections + citation + writing style + full rules + full knowledge
//
// Returns a Manifest describing the sections that were injected, for post-hoc
// debugging and token accounting. The returned manifest is never nil.
func BuildTieredPrompt(cfg *config.Config, task *dispatch.Task, agentName string, complexity classify.Complexity, deps Deps) *Manifest {
	// --- Scope Boundary validation (warn-only; never fail) ---
	// Empty value is tolerated for backward compatibility with pre-existing jobs,
	// but emits a warning so operators can see unconfigured tasks. Unknown values
	// are also warned and treated as empty (no injection).
	effectiveScope := task.ScopeBoundary
	if effectiveScope == "" {
		log.Debug("task missing scope_boundary", "taskId", task.ID, "name", task.Name, "agent", agentName, "source", task.Source)
	} else if !validScopeBoundaries[effectiveScope] {
		log.Warn("task has unknown scope_boundary", "taskId", task.ID, "name", task.Name, "scope", effectiveScope)
		effectiveScope = ""
	}

	// --- Provider type check ---
	// If the provider is "claude-code", only inject soul prompt and skip everything else.
	// Claude Code reads project files (CLAUDE.md, workspace) natively — double injection causes confusion.
	providerType := ""
	pName := deps.ResolveProviderName(cfg, *task, agentName)
	if pc, ok := cfg.Providers[pName]; ok {
		providerType = pc.Type
	}
	// Also match by provider name (auto-registered providers have no ProviderConfig entry).
	if providerType == "" && pName == "claude-code" {
		providerType = "claude-code"
	}
	if providerType == "" && pName == "codex" {
		providerType = "codex-cli"
	}

	manifest := NewManifest(task, complexity.String(), pName, providerType, agentName)

	// --- 1. Soul/Agent prompt (always loaded) ---
	if agentName != "" {
		soulPrompt := deps.LoadSoulFile(cfg, agentName)
		soulPath := ""
		if soulPrompt != "" {
			soulPath = filepath.Join(cfg.BaseDir, "agents", agentName, "SOUL.md")
		}
		if soulPrompt == "" {
			if sp, err := deps.LoadAgentPrompt(cfg, agentName); err == nil {
				soulPrompt = sp
			}
		}
		if soulPrompt != "" {
			var injected string
			switch complexity {
			case classify.Simple:
				injected = TruncateToChars(soulPrompt, 4000)
			default:
				injected = TruncateToChars(soulPrompt, cfg.PromptBudget.SoulMaxOrDefault())
			}
			task.SystemPrompt = injected
			manifest.Record("soul", "system_prompt", len(injected),
				Path(soulPath),
				Truncated(len(injected) < len(soulPrompt)),
				HashOf(injected),
			)
		}
	}

	// --- 2. Workspace directory setup (always) ---
	// Only set Workdir if not already specified (e.g. by taskboard project-specific workdir).
	if agentName != "" {
		ws := deps.ResolveWorkspace(cfg, agentName)
		if task.Workdir == "" && ws.Dir != "" {
			task.Workdir = ws.Dir
		}
		task.AddDirs = append(task.AddDirs, cfg.BaseDir)

		// Inject workspace rules into system prompt for non-CLI providers.
		if providerType != "claude-code" && providerType != "codex-cli" {
			workspaceRule := buildWorkspaceRule(cfg, agentName)
			if workspaceRule != "" {
				if task.SystemPrompt != "" {
					task.SystemPrompt += "\n\n" + workspaceRule
				} else {
					task.SystemPrompt = workspaceRule
				}
			}
		}
	}

	// --- 3. Agent config overrides (always) ---
	if agentName != "" {
		if rc, ok := cfg.Agents[agentName]; ok {
			if task.Model == cfg.DefaultModel && rc.Model != "" {
				task.Model = rc.Model
			}
			if task.PermissionMode == cfg.DefaultPermissionMode && rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	// --- 4. Inject global defaultAddDirs (always) ---
	for _, d := range cfg.DefaultAddDirs {
		if strings.HasPrefix(d, "~/") {
			home, _ := os.UserHomeDir()
			d = filepath.Join(home, d[2:])
		} else if d == "~" {
			home, _ := os.UserHomeDir()
			d = home
		}
		task.AddDirs = append(task.AddDirs, d)
	}

	// --- Lessons injection (always, provider-aware) ---
	if agentName != "" {
		lessonsPath := filepath.Join(cfg.BaseDir, "agents", agentName, "lessons.md")
		if providerType == "claude-code" || providerType == "codex-cli" {
			if _, err := os.Stat(lessonsPath); err == nil {
				preLen := len(task.Prompt)
				task.Prompt = fmt.Sprintf("⚠️ 任務開始前請先讀取 agents/%s/lessons.md，確認過去的經驗教訓。\n\n%s", agentName, task.Prompt)
				manifest.Record("lessons", "user_prompt", len(task.Prompt)-preLen, Path(lessonsPath))
			}
		} else {
			if content, err := os.ReadFile(lessonsPath); err == nil && len(content) > 0 {
				lessons := string(content)
				origLen := len(lessons)
				if len(lessons) > 4096 {
					lessons = TruncateLessonsToRecent(lessons, 10)
				}
				block := "\n\n## 經驗教訓 (lessons.md)\n" + lessons
				task.SystemPrompt += block
				manifest.Record("lessons", "system_prompt", len(block),
					Path(lessonsPath),
					Truncated(len(lessons) < origLen),
				)
			}
		}
	}

	// --- Preflight Header injection (agent-specific, hard injection) ---
	// Reads agents/{agentName}/preflight-header.md and prepends content to task.Prompt.
	// Hard-injected before provider split so it applies to both claude-code and API providers.
	if agentName != "" {
		preflightPath := filepath.Join(cfg.BaseDir, "agents", agentName, "preflight-header.md")
		if content, err := os.ReadFile(preflightPath); err == nil && len(content) > 0 {
			preLen := len(task.Prompt)
			task.Prompt = string(content) + "\n\n" + task.Prompt
			manifest.Record("preflight", "user_prompt", len(task.Prompt)-preLen, Path(preflightPath))
		}
	}

	// If provider is claude-code or codex-cli, append skill extraction hint to user prompt and return.
	// These providers read project files (CLAUDE.md, workspace) natively; system prompt is not used.
	if providerType == "claude-code" || providerType == "codex-cli" {
		preLen := len(task.Prompt)
		task.Prompt += skillExtractionSection
		manifest.Record("skill_extraction", "user_prompt", len(task.Prompt)-preLen)
		recordScopeBoundary(task, effectiveScope, manifest)
		manifest.Finalize(task)
		return manifest
	}

	// --- 5. Knowledge dir ---
	// Simple: skip. Standard/Complex: inject if exists and < 50KB.
	if complexity != classify.Simple {
		if cfg.KnowledgeDir != "" && knowledge.HasFiles(cfg.KnowledgeDir) && deps.EstimateDirSize(cfg.KnowledgeDir) <= 50*1024 {
			task.AddDirs = append(task.AddDirs, cfg.KnowledgeDir)
			manifest.Record("knowledge_dir", "add_dirs", deps.EstimateDirSize(cfg.KnowledgeDir), Path(cfg.KnowledgeDir))
		}
	}

	// --- 6. Reflection ---
	// Simple: skip. Standard: limit 1. Complex: limit 3.
	if complexity != classify.Simple && cfg.Reflection.Enabled && agentName != "" && cfg.HistoryDB != "" {
		limit := 1
		if complexity == classify.Complex {
			limit = 3
		}
		if refCtx := deps.BuildReflectionContext(cfg.HistoryDB, agentName, limit); refCtx != "" {
			block := "\n\n" + refCtx
			task.SystemPrompt += block
			// Estimate number of entries from "## " headings (coarse but avoids a schema change).
			itemCount := strings.Count(refCtx, "\n## ")
			if itemCount == 0 && strings.HasPrefix(refCtx, "## ") {
				itemCount = 1
			}
			manifest.Record("reflection", "system_prompt", len(block), ItemCount(itemCount))
		}
	}

	// --- 7. Writing Style ---
	// Simple/Standard: skip. Complex: inject.
	if complexity == classify.Complex && cfg.WritingStyle.Enabled {
		style := deps.LoadWritingStyle(cfg)
		if style != "" {
			block := "\n\n## Writing Style\n\n" + style
			task.SystemPrompt += block
			manifest.Record("writing_style", "system_prompt", len(block))
		}
	}

	// --- 8. Citation Rules ---
	// Simple: skip. Standard/Complex: inject.
	if complexity != classify.Simple && cfg.Citation.Enabled {
		citationFmt := cfg.Citation.Format
		if citationFmt == "" {
			citationFmt = "bracket"
		}
		var citationRule string
		switch citationFmt {
		case "footnote":
			citationRule = "When using information from knowledge_search, note_search, or web_search results, " +
				"add numbered footnotes at the end of your response. Format: [1] source_name"
		case "inline":
			citationRule = "When using information from knowledge_search, note_search, or web_search results, " +
				"cite sources inline immediately after the relevant information. Format: (source: source_name)"
		default: // "bracket"
			citationRule = "When using information from knowledge_search, note_search, or web_search results, " +
				"cite the source at the end of your response. Format: [source_name]"
		}
		block := "\n\n## Citation Rules\n" + citationRule
		task.SystemPrompt += block
		manifest.Record("citation", "system_prompt", len(block))
	}

	// --- 8.5. Skills injection (with doc tier) ---
	var skillsPrompt string
	var matchedSkills []string
	if deps.BuildSkillsPromptWithMeta != nil {
		skillsPrompt, matchedSkills = deps.BuildSkillsPromptWithMeta(cfg, *task, complexity)
	} else if deps.BuildSkillsPrompt != nil {
		skillsPrompt = deps.BuildSkillsPrompt(cfg, *task, complexity)
	}
	if skillsPrompt != "" {
		task.SystemPrompt += skillsPrompt
		manifest.Record("skills", "system_prompt", len(skillsPrompt), Items(matchedSkills))
	}

	// --- 8.6. Skill extraction instruction (Standard/Complex only) ---
	// Mirrors workspace CLAUDE.md "Post-Task Skill Extraction" (authoritative source).
	// Conditions align with ShouldExtractSkill in internal/skill/skill.go.
	if complexity != classify.Simple {
		task.SystemPrompt += skillExtractionSection
		manifest.Record("skill_extraction", "system_prompt", len(skillExtractionSection))
	}

	// --- 8.7. Skill-derived AllowedTools ---
	if deps.CollectSkillAllowedTools != nil {
		if collected := deps.CollectSkillAllowedTools(cfg, *task); len(collected) > 0 {
			task.AllowedTools = mergeDedup(task.AllowedTools, collected)
		}
	}

	// --- 9. Workspace Content Injection ---
	// Simple: skip entirely. Standard/Complex: call InjectWorkspaceContent.
	if complexity != classify.Simple {
		preLenSys := len(task.SystemPrompt)
		preLenUser := len(task.Prompt)
		deps.InjectWorkspaceContent(cfg, task, agentName)
		sysDelta := len(task.SystemPrompt) - preLenSys
		userDelta := len(task.Prompt) - preLenUser
		if sysDelta > 0 {
			manifest.Record("workspace_content", "system_prompt", sysDelta)
		}
		if userDelta > 0 {
			manifest.Record("workspace_content", "user_prompt", userDelta)
		}
	}

	// --- 10. AddDirs control ---
	// Simple: clear AddDirs, only keep baseDir.
	// Standard/Complex: keep baseDir, workspace dir, and task workdir.
	// Never include the bare home directory — agents scanning $HOME causes
	// extreme I/O load (find over millions of files).
	home, _ := os.UserHomeDir()
	if complexity == classify.Simple {
		task.AddDirs = []string{cfg.BaseDir}
	} else {
		var kept []string
		ws := deps.ResolveWorkspace(cfg, agentName)
		seen := map[string]bool{}
		for _, d := range task.AddDirs {
			// Block bare home directory — too broad for any task.
			if d == home {
				continue
			}
			if !seen[d] && (d == cfg.BaseDir || d == ws.Dir || d == task.Workdir) {
				seen[d] = true
				kept = append(kept, d)
			}
		}
		// For complex tasks, also keep project-specific dirs (not home).
		if complexity == classify.Complex {
			for _, d := range task.AddDirs {
				if d != home && !seen[d] {
					seen[d] = true
					kept = append(kept, d)
				}
			}
		}
		task.AddDirs = kept
	}

	// --- 12. Enforce total budget ---
	totalMax := cfg.PromptBudget.TotalMaxOrDefault()
	if len(task.SystemPrompt) > totalMax {
		task.SystemPrompt = TruncateToChars(task.SystemPrompt, totalMax)
	}

	// --- 13. Scope Boundary (last, highest priority) ---
	// Injected last so the SCOPE HEADER ends up at the top of both user prompt
	// and system prompt, overriding any earlier guidance.
	recordScopeBoundary(task, effectiveScope, manifest)

	manifest.Finalize(task)
	return manifest
}

// recordScopeBoundary prepends the SCOPE HEADER and records it in the manifest.
func recordScopeBoundary(task *dispatch.Task, scope string, manifest *Manifest) {
	block := BuildScopeBlock(scope)
	if block == "" {
		return
	}
	userDelta := 0
	sysDelta := 0
	preLenUser := len(task.Prompt)
	preLenSys := len(task.SystemPrompt)
	task.Prompt = block + "\n" + task.Prompt
	userDelta = len(task.Prompt) - preLenUser
	if task.SystemPrompt != "" {
		task.SystemPrompt = block + "\n" + task.SystemPrompt
		sysDelta = len(task.SystemPrompt) - preLenSys
	}
	if userDelta > 0 {
		manifest.Record("scope_boundary", "user_prompt", userDelta, Items([]string{scope}))
	}
	if sysDelta > 0 {
		manifest.Record("scope_boundary", "system_prompt", sysDelta, Items([]string{scope}))
	}
}

// TruncateLessonsToRecent keeps only the last N entries from a lessons.md file.
// Entries are separated by "---" or "##" headings.
func TruncateLessonsToRecent(content string, n int) string {
	var entries []string
	var current strings.Builder
	for _, line := range strings.Split(content, "\n") {
		if (line == "---" || strings.HasPrefix(line, "## ")) && current.Len() > 0 {
			entries = append(entries, current.String())
			current.Reset()
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	if current.Len() > 0 {
		entries = append(entries, current.String())
	}

	if len(entries) <= n {
		return content
	}

	recent := entries[len(entries)-n:]
	result := fmt.Sprintf("[... %d older entries omitted ...]\n\n", len(entries)-n)
	result += strings.Join(recent, "---\n")
	return result
}

// TruncateToChars truncates a string to maxChars, trying to cut at a newline boundary.
func TruncateToChars(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	cut := s[:maxChars]
	if idx := strings.LastIndex(cut, "\n"); idx > maxChars*3/4 {
		cut = cut[:idx]
	}
	return cut + "\n\n[... truncated ...]"
}

// mergeDedup appends extra items to base, skipping duplicates.
func mergeDedup(base, extra []string) []string {
	seen := make(map[string]bool, len(base))
	for _, s := range base {
		seen[s] = true
	}
	result := append([]string{}, base...)
	for _, s := range extra {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// buildWorkspaceRule generates a workspace rule for the given agent.
// Rule: "Save outputs to ~/.tetora/workspace/agents/{name}/outputs/"
func buildWorkspaceRule(cfg *config.Config, agentName string) string {
	if cfg.AgentOutputBase == "" {
		return ""
	}

	outputDir := filepath.Join(cfg.AgentOutputBase, agentName, "outputs")

	return fmt.Sprintf(`## Working Directory Rules
1. **Code Edits**: Modify files in-place within the project structure.
2. **New Artifacts**: Save all generated documents (reports, docs, plans, analysis) to:
   **%s**
3. **Cross-Project Review**: When reviewing external projects:
   - READ from external directories is allowed.
   - WRITE all notes, reviews, and reports to your output directory above.
   - NEVER create loose files in external project directories.
4. **NEVER** create temporary files in the project root directory.`, outputDir)
}
