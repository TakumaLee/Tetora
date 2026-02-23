package main

import (
	"strings"
)

// --- P17.3c: Dynamic Skill Injection ---

// SkillMatcher defines conditions for when a skill should be injected into a prompt.
type SkillMatcher struct {
	Roles    []string `json:"roles,omitempty"`    // inject for these roles
	Keywords []string `json:"keywords,omitempty"` // inject when prompt matches
	Channels []string `json:"channels,omitempty"` // inject for these channels (telegram, slack, discord, etc.)
}

// selectSkills filters skills based on task context (role, keywords, channel).
// Returns only the skills that match the current task's context.
// This reduces token usage by avoiding injection of all skills into every prompt.
// Includes both config-based and learned file-based skills.
func selectSkills(cfg *Config, task Task) []SkillConfig {
	var selected []SkillConfig
	seen := make(map[string]bool)

	// Config-based skills.
	for _, skill := range cfg.Skills {
		if shouldInjectSkill(skill, task) {
			selected = append(selected, skill)
			seen[skill.Name] = true
		}
	}

	// Also include learned skills from file store.
	learned := autoInjectLearnedSkills(cfg, task)
	for _, skill := range learned {
		if !seen[skill.Name] {
			selected = append(selected, skill)
			seen[skill.Name] = true
		}
	}

	return selected
}

// shouldInjectSkill determines if a skill should be injected for this task.
func shouldInjectSkill(skill SkillConfig, task Task) bool {
	// If no matcher is defined, always inject (backward compatible).
	if skill.Matcher == nil {
		return true
	}

	matcher := skill.Matcher

	// Check role match.
	if len(matcher.Roles) > 0 {
		roleMatch := false
		for _, role := range matcher.Roles {
			if role == task.Role {
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
		channel := extractChannelFromSource(task.Source)
		for _, ch := range matcher.Channels {
			if ch == channel {
				return true
			}
		}
	}

	// No match found, don't inject.
	return false
}

// extractChannelFromSource extracts the channel name from task.Source.
// Source format examples: "telegram", "slack:C123", "discord:456", "chat:telegram:789", "cron"
func extractChannelFromSource(source string) string {
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

// buildSkillsPrompt builds the skills section of the system prompt.
// Only includes skills that are relevant to this task.
func buildSkillsPrompt(cfg *Config, task Task) string {
	skills := selectSkills(cfg, task)
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Available Skills\n\n")
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
	return sb.String()
}

// skillMatchesContext is a helper for testing skill selection logic.
func skillMatchesContext(skill SkillConfig, role, prompt, source string) bool {
	task := Task{
		Role:   role,
		Prompt: prompt,
		Source: source,
	}
	return shouldInjectSkill(skill, task)
}
