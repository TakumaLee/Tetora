package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tetora/internal/version"
)

// --- Agent Prompt Loading ---

// LoadAgentPrompt reads the SOUL file for a given agent name.
// Resolution order:
//  1. Per-agent workspace soul file (via ResolveWorkspace)
//  2. agents/{agent}/SOUL.md
//  3. Legacy fallback: {DefaultWorkdir}/{soulFile}
func LoadAgentPrompt(cfg *CLIConfig, agentName string) (string, error) {
	_, ok := cfg.Agents[agentName]
	if !ok {
		return "", fmt.Errorf("agent %q not found in config", agentName)
	}

	// Try workspace-resolved soul file first.
	ws := ResolveWorkspace(cfg, agentName)
	if ws.SoulFile != "" {
		if data, err := os.ReadFile(ws.SoulFile); err == nil {
			return string(data), nil
		}
	}

	// Fallback: agents/{agent}/SOUL.local.md (per-machine override, not committed)
	agentSoulLocalPath := filepath.Join(cfg.AgentsDir, agentName, "SOUL.local.md")
	if data, err := os.ReadFile(agentSoulLocalPath); err == nil {
		return string(data), nil
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

// ResolveWorkspace returns the workspace config for an agent.
func ResolveWorkspace(cfg *CLIConfig, agentName string) WorkspaceInfo {
	agent, ok := cfg.Agents[agentName]
	if !ok {
		return WorkspaceInfo{Dir: cfg.WorkspaceDir}
	}

	ws := agent.Workspace

	if ws.Dir == "" {
		ws.Dir = cfg.WorkspaceDir
	}
	if ws.SoulFile == "" {
		ws.SoulFile = filepath.Join(cfg.AgentsDir, agentName, "SOUL.md")
	}

	return ws
}

// --- Agent Archetypes ---

// AgentArchetype defines a template for creating new agents.
type AgentArchetype struct {
	Name           string
	Description    string
	Model          string
	PermissionMode string
	SoulTemplate   string
}

// BuiltinArchetypes contains the default agent archetypes.
var BuiltinArchetypes = []AgentArchetype{
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

// GenerateSoulContent generates a soul file from an archetype template.
func GenerateSoulContent(archetype *AgentArchetype, agentName string) string {
	return strings.ReplaceAll(archetype.SoulTemplate, "{{.RoleName}}", agentName)
}

// --- Config Update Helpers ---

// UpdateConfigAgents updates a single agent in config.json.
// If agentJSON is nil, the agent is removed. Otherwise it is added/updated.
// Preserves all other config fields by reading/modifying/writing the raw JSON.
func UpdateConfigAgents(configPath, agentName string, agentJSON json.RawMessage) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Parse existing agents as raw map.
	var agents map[string]json.RawMessage
	if agentsRaw, ok := raw["agents"]; ok {
		json.Unmarshal(agentsRaw, &agents)
	} else if rolesRaw, ok := raw["roles"]; ok {
		json.Unmarshal(rolesRaw, &agents)
		delete(raw, "roles") // migrate to new key
	}
	if agents == nil {
		agents = make(map[string]json.RawMessage)
	}

	if agentJSON == nil {
		delete(agents, agentName)
	} else {
		agents[agentName] = agentJSON
	}

	agentsBytes, err := json.Marshal(agents)
	if err != nil {
		return err
	}
	raw["agents"] = agentsBytes

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o600); err != nil {
		return err
	}

	// Auto-snapshot config version after agent change.
	SnapshotAfterChange(configPath, "cli", fmt.Sprintf("agent %s", agentName))
	return nil
}

// UpdateConfigField sets a top-level field in config.json using raw JSON manipulation.
func UpdateConfigField(configPath, field string, value json.RawMessage) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	raw[field] = value

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}

// UpdateConfigSmartDispatchDefault sets smartDispatch.defaultAgent in the config file.
func UpdateConfigSmartDispatchDefault(configPath, agentName string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	sd := make(map[string]any)
	if sdRaw, ok := raw["smartDispatch"]; ok {
		json.Unmarshal(sdRaw, &sd)
	}
	sd["defaultAgent"] = agentName

	sdJSON, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	raw["smartDispatch"] = sdJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}

// SnapshotAfterChange snapshots config after a CLI change.
// Silently ignores errors (best-effort).
func SnapshotAfterChange(configPath, changedBy, reason string) {
	// Quick load to find historyDB.
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var cfg struct {
		HistoryDB string `json:"historyDB"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return
	}
	dbPath := cfg.HistoryDB
	if dbPath == "" {
		dbPath = "history.db"
	}
	baseDir := filepath.Dir(configPath)
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(baseDir, dbPath)
	}
	version.SnapshotConfig(dbPath, configPath, changedBy, reason)
}
