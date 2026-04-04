package skill

import (
	"fmt"
	"os"
	"strings"

	"tetora/internal/classify"
)

// --- P17.3c: Dynamic Skill Injection ---

// SkillMatcher defines conditions for when a skill should be injected into a prompt.
type SkillMatcher struct {
	Agents   []string `json:"agents,omitempty"`   // inject for these agents
	Keywords []string `json:"keywords,omitempty"` // inject when prompt matches
	Channels []string `json:"channels,omitempty"` // inject for these channels (telegram, slack, discord, etc.)
}

// SelectSkills filters skills based on task context (role, keywords, channel).
// Returns only the skills that match the current task's context.
// This reduces token usage by avoiding injection of all skills into every prompt.
// Includes both config-based and learned file-based skills.
func SelectSkills(cfg *AppConfig, task TaskContext) []SkillConfig {
	var selected []SkillConfig
	seen := make(map[string]bool)

	// Config-based skills.
	for _, skill := range cfg.Skills {
		if ShouldInjectSkill(skill, task) {
			selected = append(selected, skill)
			seen[skill.Name] = true
		}
	}

	// Also include learned skills from file store.
	learned := AutoInjectLearnedSkills(cfg, task)
	for _, skill := range learned {
		if !seen[skill.Name] {
			selected = append(selected, skill)
			seen[skill.Name] = true
		}
	}

	return selected
}

// ShouldInjectSkill determines if a skill should be injected for this task.
func ShouldInjectSkill(skill SkillConfig, task TaskContext) bool {
	// If no matcher is defined, always inject (backward compatible).
	if skill.Matcher == nil {
		return true
	}

	matcher := skill.Matcher

	// Check role match.
	if len(matcher.Agents) > 0 {
		roleMatch := false
		for _, role := range matcher.Agents {
			if role == task.Agent {
				roleMatch = true
				break
			}
		}
		if roleMatch {
			return true
		}
	}

	// Check keyword match in prompt.
	if len(matcher.Keywords) > 0 {
		promptLower := strings.ToLower(task.Prompt)
		for _, kw := range matcher.Keywords {
			if strings.Contains(promptLower, strings.ToLower(kw)) {
				return true
			}
		}
	}

	// Check channel match (extract from task.Source).
	if len(matcher.Channels) > 0 {
		channel := ExtractChannelFromSource(task.Source)
		for _, ch := range matcher.Channels {
			if ch == channel {
				return true
			}
		}
	}

	// No match found, don't inject.
	return false
}

// ExtractChannelFromSource extracts the channel name from task.Source.
// Source format examples: "telegram", "slack:C123", "discord:456", "chat:telegram:789", "cron"
func ExtractChannelFromSource(source string) string {
	if source == "" {
		return ""
	}

	// Handle chat: prefix.
	if strings.HasPrefix(source, "chat:") {
		parts := strings.Split(source, ":")
		if len(parts) >= 2 {
			return parts[1]
		}
	}

	// Handle direct channel name (telegram, slack, discord, etc.)
	parts := strings.Split(source, ":")
	return parts[0]
}

// CollectSkillAllowedTools aggregates AllowedTools from all matching skills (deduped).
func CollectSkillAllowedTools(cfg *AppConfig, task TaskContext) []string {
	skills := SelectSkills(cfg, task)
	seen := make(map[string]bool)
	var result []string
	for _, s := range skills {
		for _, t := range s.AllowedTools {
			if !seen[t] {
				seen[t] = true
				result = append(result, t)
			}
		}
	}
	return result
}

// BuildSkillCatalog returns a compact markdown listing of ALL skills in the skill
// store, grouped into executable vs doc-only (reference) categories. This gives
// agents a complete map of available skills without injecting full documentation.
// Max ~60 chars per description line; longer descriptions are truncated with "...".
func BuildSkillCatalog(cfg *AppConfig) string {
	all := LoadFileSkills(cfg)
	if len(all) == 0 {
		return ""
	}

	var executable, docOnly []SkillConfig
	for _, s := range all {
		if s.Command != "" {
			executable = append(executable, s)
		} else {
			docOnly = append(docOnly, s)
		}
	}

	if len(executable) == 0 && len(docOnly) == 0 {
		return ""
	}

	const maxDescLen = 60

	truncate := func(s string) string {
		// Use rune-aware truncation to avoid splitting multibyte characters.
		runes := []rune(s)
		if len(runes) > maxDescLen {
			return string(runes[:maxDescLen-3]) + "..."
		}
		return s
	}

	var sb strings.Builder
	sb.WriteString("## Skill Catalog (all available)\n\n")

	if len(executable) > 0 {
		sb.WriteString("Executable:\n")
		for _, s := range executable {
			sb.WriteString("- **")
			sb.WriteString(s.Name)
			sb.WriteString("**")
			if s.Description != "" {
				sb.WriteString(": ")
				sb.WriteString(truncate(s.Description))
			}
			sb.WriteString("\n")
		}
	}

	if len(docOnly) > 0 {
		if len(executable) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("Reference (read SKILL.md for details):\n")
		for _, s := range docOnly {
			sb.WriteString("- **")
			sb.WriteString(s.Name)
			sb.WriteString("**")
			if s.Description != "" {
				sb.WriteString(": ")
				sb.WriteString(truncate(s.Description))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\nTo use a reference skill, read its SKILL.md at ")
		sb.WriteString("~/.tetora/workspace/skills/{name}/SKILL.md\n")
	}

	// --- Learned (pending review) section ---
	var pendingLearned []SkillConfig
	for _, s := range all {
		if s.Learned {
			pendingLearned = append(pendingLearned, s)
		}
	}
	if len(pendingLearned) > 0 {
		sb.WriteString("\nLearned (pending review):\n")
		for _, s := range pendingLearned {
			sb.WriteString("- **")
			sb.WriteString(s.Name)
			sb.WriteString("**")
			if s.Description != "" {
				sb.WriteString(": ")
				sb.WriteString(truncate(s.Description))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// BuildSkillsPrompt builds the skills section of the system prompt.
// Tier 0 (always): full skill catalog (all skills, compact listing).
// Tier 1 (matched): one-line summaries for context-matched skills.
// Tier 2 (Standard/Complex only): SKILL.md doc injection when available.
func BuildSkillsPrompt(cfg *AppConfig, task TaskContext, complexity classify.Complexity) string {
	catalog := BuildSkillCatalog(cfg)

	skills := SelectSkills(cfg, task)

	// Limit number of injected skills per task (SkillsBench: 2-3 curated > many).
	// The maxSkillsPerTask cap applies only to matched/active skills, not the catalog.
	maxN := cfg.maxSkillsPerTaskOrDefault()
	if len(skills) > maxN {
		skills = skills[:maxN]
	}

	// If there's nothing at all, return empty.
	if catalog == "" && len(skills) == 0 {
		return ""
	}

	// Track which skills were injected for this task.
	for _, s := range skills {
		RecordSkillEventEx(cfg.HistoryDB, s.Name, "injected", task.Prompt, task.Agent, SkillEventOpts{
			SessionID: task.SessionID,
		})
	}

	var sb strings.Builder
	sb.WriteString("\n\n")

	// --- Tier 0: Full skill catalog (always present) ---
	if catalog != "" {
		sb.WriteString(catalog)
	}

	// --- Tier 1: Active matched skills ---
	if len(skills) > 0 {
		if catalog != "" {
			sb.WriteString("\n---\n\n")
		}
		sb.WriteString("## Active Skills (matched for this task)\n\n")
		sb.WriteString("You have access to the following external commands/skills:\n\n")

		for _, skill := range skills {
			sb.WriteString("- **")
			sb.WriteString(skill.Name)
			sb.WriteString("**")
			if skill.Description != "" {
				sb.WriteString(": ")
				sb.WriteString(skill.Description)
			}
			sb.WriteString("\n")

			// Include usage example if available.
			if skill.Example != "" {
				sb.WriteString("  - Example: `")
				sb.WriteString(skill.Example)
				sb.WriteString("`\n")
			}
		}

		sb.WriteString("\nTo invoke a skill, use the `execute_skill` tool.\n")

		// --- Tier 2: Skill documentation (Standard/Complex only) ---
		if complexity != classify.Simple {
			docBudget := cfg.skillsMaxOrDefault()
			docUsed := 0
			for _, skill := range skills {
				if skill.DocPath == "" {
					continue
				}
				if skill.DocSize > 4096 {
					// Too large — hint the path for agent to read manually.
					sb.WriteString(fmt.Sprintf(
						"\n- **%s** detailed docs: `%s` (read with file tool)\n",
						skill.Name, skill.DocPath))
					continue
				}
				if docUsed+skill.DocSize > docBudget {
					sb.WriteString(fmt.Sprintf(
						"\n- **%s** detailed docs: `%s` (budget exceeded, read with file tool)\n",
						skill.Name, skill.DocPath))
					continue
				}
				doc, err := os.ReadFile(skill.DocPath)
				if err != nil {
					logDebug("skill doc read failed", "skill", skill.Name, "path", skill.DocPath, "error", err)
					continue
				}
				sb.WriteString(fmt.Sprintf("\n<skill-doc name=\"%s\">\n%s\n</skill-doc>\n",
					skill.Name, strings.TrimSpace(string(doc))))
				docUsed += len(doc)
			}

			// --- Tier 3: Skill failure context injection ---
			for _, skill := range skills {
				failures := LoadSkillFailuresByName(cfg, skill.Name)
				if failures == "" {
					continue
				}
				sb.WriteString(fmt.Sprintf("\n<skill-failures name=\"%s\">\n%s\n</skill-failures>\n",
					skill.Name, failures))
			}
		}
	}

	return sb.String()
}

// SkillMatchesContext is a helper for testing skill selection logic.
func SkillMatchesContext(skill SkillConfig, role, prompt, source string) bool {
	task := TaskContext{
		Agent:  role,
		Prompt: prompt,
		Source: source,
	}
	return ShouldInjectSkill(skill, task)
}
