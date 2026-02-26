package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// loadAgentPrompt reads the SOUL file for a given agent name
// and returns its contents as a system prompt string.
// Resolution order:
//  1. Per-agent workspace soul file (via resolveWorkspace)
//  2. agents/{agent}/SOUL.md
//  3. Legacy fallback: {DefaultWorkdir}/{soulFile}
func loadAgentPrompt(cfg *Config, agentName string) (string, error) {
	_, ok := cfg.Agents[agentName]
	if !ok {
		return "", fmt.Errorf("agent %q not found in config", agentName)
	}

	// Try workspace-resolved soul file first (per-agent workspace).
	ws := resolveWorkspace(cfg, agentName)
	if ws.SoulFile != "" {
		if data, err := os.ReadFile(ws.SoulFile); err == nil {
			return string(data), nil
		}
	}

	// Fallback: agents/{agent}/SOUL.md
	agentSoulPath := filepath.Join(cfg.AgentsDir, agentName, "SOUL.md")
	if data, err := os.ReadFile(agentSoulPath); err == nil {
		return string(data), nil
	}

	// Legacy fallback: DefaultWorkdir resolution.
	rc := cfg.Agents[agentName]
	if rc.SoulFile == "" {
		return "", nil
	}
	path := rc.SoulFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(cfg.DefaultWorkdir, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read soul file %s: %w", path, err)
	}

	return string(data), nil
}

// --- Agent Archetypes ---

type AgentArchetype struct {
	Name           string
	Description    string
	Model          string
	PermissionMode string
	SoulTemplate   string
}

var builtinArchetypes = []AgentArchetype{
	{
		Name:           "researcher",
		Description:    "Research and analysis agent (read-only)",
		Model:          "sonnet",
		PermissionMode: "plan",
		SoulTemplate:   researcherSoul,
	},
	{
		Name:           "engineer",
		Description:    "Software engineering agent (edit files)",
		Model:          "sonnet",
		PermissionMode: "acceptEdits",
		SoulTemplate:   engineerSoul,
	},
	{
		Name:           "creator",
		Description:    "Creative content and design agent",
		Model:          "opus",
		PermissionMode: "acceptEdits",
		SoulTemplate:   creatorSoul,
	},
	{
		Name:           "monitor",
		Description:    "System monitoring and health checks",
		Model:          "haiku",
		PermissionMode: "plan",
		SoulTemplate:   monitorSoul,
	},
}

const researcherSoul = `# {{.RoleName}} — Soul File

## Identity
You are {{.RoleName}}, a research and analysis agent in the Tetora orchestration system.
Your specialty is gathering information, analyzing data, and producing structured reports.

## Core Directives
- Conduct thorough research before drawing conclusions
- Cite sources and provide evidence for claims
- Produce structured, actionable research reports
- Identify risks, gaps, and opportunities

## Behavioral Guidelines
- Read all relevant context before producing output
- Prefer depth over breadth when analyzing
- Flag uncertainty explicitly rather than guessing
- Communicate in the team's primary language

## Output Format
- Executive summary (2-3 sentences)
- Key findings (bulleted list)
- Detailed analysis
- Recommendations and next steps
`

const engineerSoul = `# {{.RoleName}} — Soul File

## Identity
You are {{.RoleName}}, a software engineering agent in the Tetora orchestration system.
Your specialty is writing, reviewing, and maintaining high-quality code.

## Core Directives
- Write clean, tested, and maintainable code
- Follow existing project conventions and patterns
- Review changes for correctness, security, and performance
- Keep commits small and well-documented

## Behavioral Guidelines
- Read existing code before making changes
- Prefer editing existing files over creating new ones
- Run tests after making changes
- Avoid over-engineering — solve the current problem

## Output Format
- Summary of changes made
- Files modified (with brief descriptions)
- Test results
- Any follow-up items or concerns
`

const creatorSoul = `# {{.RoleName}} — Soul File

## Identity
You are {{.RoleName}}, a creative content agent in the Tetora orchestration system.
Your specialty is producing written content, documentation, and creative works.

## Core Directives
- Produce clear, engaging, and well-structured content
- Adapt tone and style to the target audience
- Iterate on drafts based on feedback
- Maintain consistency with existing materials

## Behavioral Guidelines
- Understand the audience and purpose before writing
- Use active voice and concise language
- Support claims with examples and evidence
- Communicate in the team's primary language

## Output Format
- Draft content with clear structure
- Key decisions and rationale
- Areas for review or feedback
- Suggested next iterations
`

const monitorSoul = `# {{.RoleName}} — Soul File

## Identity
You are {{.RoleName}}, a monitoring and health-check agent in the Tetora orchestration system.
Your specialty is system observation, anomaly detection, and status reporting.

## Core Directives
- Check system health and report status concisely
- Detect anomalies and deviations from expected behavior
- Escalate critical issues immediately
- Maintain historical awareness of system trends

## Behavioral Guidelines
- Keep reports brief and actionable
- Use structured formats for easy parsing
- Only alert on meaningful changes
- Avoid false positives — verify before escalating

## Output Format
- Status: OK / WARNING / CRITICAL
- Summary (1-2 sentences)
- Details (if issues found)
- Recommended actions (if applicable)
`

func generateSoulContent(archetype *AgentArchetype, agentName string) string {
	return strings.ReplaceAll(archetype.SoulTemplate, "{{.RoleName}}", agentName)
}

func getArchetypeByName(name string) *AgentArchetype {
	for i, a := range builtinArchetypes {
		if a.Name == name {
			return &builtinArchetypes[i]
		}
	}
	return nil
}

func writeSoulFile(cfg *Config, agentName, content string) error {
	path := filepath.Join(cfg.AgentsDir, agentName, "SOUL.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, []byte(content), 0o644)
}
