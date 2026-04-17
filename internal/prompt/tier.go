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
)

// skillExtractionSection is injected into every dispatched agent prompt.
// It mirrors workspace CLAUDE.md "Post-Task Skill Extraction" (authoritative source).
// Conditions mirror ShouldExtractSkill in internal/skill/skill.go.
const skillExtractionSection = "\n\n## Post-Task Skill Extraction\n" +
	"完成任務後，符合以下任一條件時，提取 skill 到 `skills/learned/{name}/`：\n" +
	"- 5+ tool calls、error recovery、無既有 skill 的新 workflow、user 糾正過\n" +
	"- 格式：SKILL.md + metadata.json，寫到 `skills/learned/{name}/`\n" +
	"- 一次性操作或已有類似 skill → 不提取"

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
	CollectSkillAllowedTools   func(cfg *config.Config, task dispatch.Task) []string
	InjectWorkspaceContent     func(cfg *config.Config, task *dispatch.Task, agentName string)
	EstimateDirSize            func(dir string) int
}

// BuildTieredPrompt constructs a system prompt based on request complexity.
// It replaces the inline assembly in runTask() and runSingleTask().
//
// Tiering strategy:
//
//	Simple:   soul (truncated 4KB) only — no reflection, style, citation, rules, knowledge
//	Standard: full soul + 1 reflection + citation + rules index + knowledge index
//	Complex:  full soul + 3 reflections + citation + writing style + full rules + full knowledge
func BuildTieredPrompt(cfg *config.Config, task *dispatch.Task, agentName string, complexity classify.Complexity, deps Deps) {
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

	// --- 1. Soul/Agent prompt (always loaded) ---
	if agentName != "" {
		soulPrompt := deps.LoadSoulFile(cfg, agentName)
		if soulPrompt == "" {
			if sp, err := deps.LoadAgentPrompt(cfg, agentName); err == nil {
				soulPrompt = sp
			}
		}
		if soulPrompt != "" {
			switch complexity {
			case classify.Simple:
				task.SystemPrompt = TruncateToChars(soulPrompt, 4000)
			default:
				task.SystemPrompt = TruncateToChars(soulPrompt, cfg.PromptBudget.SoulMaxOrDefault())
			}
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
				task.Prompt = fmt.Sprintf("⚠️ 任務開始前請先讀取 agents/%s/lessons.md，確認過去的經驗教訓。\n\n%s", agentName, task.Prompt)
			}
		} else {
			if content, err := os.ReadFile(lessonsPath); err == nil && len(content) > 0 {
				lessons := string(content)
				if len(lessons) > 4096 {
					lessons = TruncateLessonsToRecent(lessons, 10)
				}
				task.SystemPrompt += "\n\n## 經驗教訓 (lessons.md)\n" + lessons
			}
		}
	}

	// --- Preflight Header injection (agent-specific, hard injection) ---
	// Reads agents/{agentName}/preflight-header.md and prepends content to task.Prompt.
	// Hard-injected before provider split so it applies to both claude-code and API providers.
	if agentName != "" {
		preflightPath := filepath.Join(cfg.BaseDir, "agents", agentName, "preflight-header.md")
		if content, err := os.ReadFile(preflightPath); err == nil && len(content) > 0 {
			task.Prompt = string(content) + "\n\n" + task.Prompt
		}
	}

	// If provider is claude-code or codex-cli, append skill extraction hint to user prompt and return.
	// These providers read project files (CLAUDE.md, workspace) natively; system prompt is not used.
	if providerType == "claude-code" || providerType == "codex-cli" {
		task.Prompt += skillExtractionSection
		return
	}

	// --- 5. Knowledge dir ---
	// Simple: skip. Standard/Complex: inject if exists and < 50KB.
	if complexity != classify.Simple {
		if cfg.KnowledgeDir != "" && knowledge.HasFiles(cfg.KnowledgeDir) && deps.EstimateDirSize(cfg.KnowledgeDir) <= 50*1024 {
			task.AddDirs = append(task.AddDirs, cfg.KnowledgeDir)
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
			task.SystemPrompt += "\n\n" + refCtx
		}
	}

	// --- 7. Writing Style ---
	// Simple/Standard: skip. Complex: inject.
	if complexity == classify.Complex && cfg.WritingStyle.Enabled {
		style := deps.LoadWritingStyle(cfg)
		if style != "" {
			task.SystemPrompt += "\n\n## Writing Style\n\n" + style
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
		task.SystemPrompt += "\n\n## Citation Rules\n" + citationRule
	}

	// --- 8.5. Skills injection (with doc tier) ---
	if skillsPrompt := deps.BuildSkillsPrompt(cfg, *task, complexity); skillsPrompt != "" {
		task.SystemPrompt += skillsPrompt
	}

	// --- 8.6. Skill extraction instruction (Standard/Complex only) ---
	// Mirrors workspace CLAUDE.md "Post-Task Skill Extraction" (authoritative source).
	// Conditions align with ShouldExtractSkill in internal/skill/skill.go.
	if complexity != classify.Simple {
		task.SystemPrompt += skillExtractionSection
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
		deps.InjectWorkspaceContent(cfg, task, agentName)
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
