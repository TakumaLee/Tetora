package rule

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tetora/internal/config"
)

// BuildPrompt assembles the dynamic "Active Rules" block to be appended to a
// task's system prompt. It returns an empty string when no rule entries can be
// resolved — the caller is expected to fall back to the legacy whole-directory
// injection in that case.
//
// Entry resolution order:
//  1. Per-file YAML frontmatter (preferred; scan rulesDir/*.md).
//  2. Legacy INDEX.md markdown table (fallback when frontmatter scan is empty).
//
// Budget enforcement:
//   - Matched entries are capped at cfg.PromptBudget.MaxRulesPerTaskOrDefault().
//   - Total injected content (always + matched) is capped at
//     cfg.PromptBudget.RulesMaxOrDefault() bytes. Entries exceeding the
//     remaining budget are skipped (with a short note) rather than truncated
//     mid-rule, to avoid emitting half-rules that might mislead the agent.
func BuildPrompt(cfg *config.Config, rulesDir, prompt string) string {
	return BuildPromptForAgent(cfg, rulesDir, prompt, "")
}

// BuildPromptForAgent is BuildPrompt with an optional agentName for per-agent
// always-on rules declared via frontmatter `agents: [...]`.
func BuildPromptForAgent(cfg *config.Config, rulesDir, prompt, agentName string) string {
	if cfg == nil || rulesDir == "" {
		return ""
	}
	entries, err := ScanDir(rulesDir)
	if err != nil || len(entries) == 0 {
		indexPath := filepath.Join(rulesDir, "INDEX.md")
		entries, err = ParseIndex(indexPath)
		if err != nil {
			return ""
		}
	}

	always, matched := MatchForAgent(entries, prompt, agentName)

	maxMatched := cfg.PromptBudget.MaxRulesPerTaskOrDefault()
	if len(matched) > maxMatched {
		matched = matched[:maxMatched]
	}

	budget := cfg.PromptBudget.RulesMaxOrDefault()
	var (
		b       strings.Builder
		used    int
		skipped []string
	)
	b.WriteString("## Active Rules (always-on + matched for this task)\n\n")

	emit := func(e Entry, tag string) {
		path := filepath.Join(rulesDir, e.Path)
		data, err := os.ReadFile(path)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (read error)", e.Path))
			return
		}
		_, body := ParseFrontmatter(string(data))
		content := strings.TrimSpace(body)
		header := fmt.Sprintf("### [%s] %s\n\n", tag, e.Path)
		cost := len(header) + len(content) + 2 // +2 for trailing \n\n
		if used+cost > budget && used > 0 {
			skipped = append(skipped, e.Path)
			return
		}
		b.WriteString(header)
		b.WriteString(content)
		b.WriteString("\n\n")
		used += cost
	}

	for _, e := range always {
		emit(e, "always")
	}
	for _, e := range matched {
		keywordHit := firstHit(e.Keywords, prompt)
		tag := "matched"
		if keywordHit != "" {
			tag = fmt.Sprintf("matched: %s", keywordHit)
		}
		emit(e, tag)
	}

	// Footer hint so the agent knows further rules are addressable via the
	// {{rules.FILENAME}} template or the INDEX.
	b.WriteString("> Other rules available via `{{rules.FILENAME}}` template or `rules/INDEX.md`.")
	if len(skipped) > 0 {
		b.WriteString(fmt.Sprintf(" Skipped for budget: %s.", strings.Join(skipped, ", ")))
	}
	b.WriteString("\n")

	return b.String()
}

func firstHit(keywords []string, prompt string) string {
	promptLower := strings.ToLower(prompt)
	for _, kw := range keywords {
		if kw != "" && strings.Contains(promptLower, kw) {
			return kw
		}
	}
	return ""
}
