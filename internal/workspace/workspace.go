package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tetora/internal/config"
	"tetora/internal/log"
	"tetora/internal/rule"
	"tetora/internal/sprite"
)

// SessionScope defines trust and tool constraints per session type.
type SessionScope struct {
	SessionType string // "main", "dm", "group"
	TrustLevel  string // from agent config or session-type default
	ToolProfile string // from agent config or session-type default
	Sandbox     bool   // from agent config + session type
}

// ResolveWorkspace returns the effective workspace config for an agent.
// Falls back to DefaultWorkspace if the agent is not found or workspace not configured.
func ResolveWorkspace(cfg *config.Config, agentName string) config.WorkspaceConfig {
	role, ok := cfg.Agents[agentName]
	if !ok {
		return DefaultWorkspace(cfg)
	}

	ws := role.Workspace

	// Set default workspace directory if not specified.
	if ws.Dir == "" {
		ws.Dir = cfg.WorkspaceDir
	}

	// Set default soul file path if not specified.
	if ws.SoulFile == "" {
		ws.SoulFile = filepath.Join(cfg.AgentsDir, agentName, "SOUL.md")
	}

	return ws
}

// DefaultWorkspace returns the default workspace configuration.
func DefaultWorkspace(cfg *config.Config) config.WorkspaceConfig {
	return config.WorkspaceConfig{
		Dir: cfg.WorkspaceDir,
	}
}

// InitDirectories ensures all required directories exist for agents, workspace, and runtime.
// v1.3.0 directory layout:
//
//	~/.tetora/
//	  agents/{name}/          — agent identity (SOUL.md)
//	  workspace/              — shared workspace
//	    lore/                 — world-building & story context (injected into system prompt)
//	    rules/                — governance rules (injected into system prompt)
//	    memory/               — shared memory (.md files)
//	    team/                 — team governance
//	    knowledge/            — knowledge base
//	    drafts/               — content drafts
//	    intel/                — intelligence center
//	    products/             — product portfolio
//	    projects/             — project references
//	    content-queue/        — publishing schedule
//	    research/             — research documents
//	    skills/               — skills/integrations
//	  runtime/                — ephemeral (deletable)
//	    sessions/ outputs/ logs/ cache/ security/ cron-runs/
//	  dbs/                    — databases
//	  vault/                  — import snapshots
//	  media/                  — media assets
//	    sprites/              — character sprite PNGs
func InitDirectories(cfg *config.Config) error {
	dirs := []string{
		// Agents
		cfg.AgentsDir,
		// Workspace sub-directories
		cfg.WorkspaceDir,
		filepath.Join(cfg.WorkspaceDir, "lore"),
		filepath.Join(cfg.WorkspaceDir, "rules"),
		filepath.Join(cfg.WorkspaceDir, "memory"),
		filepath.Join(cfg.WorkspaceDir, "team"),
		filepath.Join(cfg.WorkspaceDir, "knowledge"),
		filepath.Join(cfg.WorkspaceDir, "drafts"),
		filepath.Join(cfg.WorkspaceDir, "intel"),
		filepath.Join(cfg.WorkspaceDir, "products"),
		filepath.Join(cfg.WorkspaceDir, "projects"),
		filepath.Join(cfg.WorkspaceDir, "content-queue"),
		filepath.Join(cfg.WorkspaceDir, "research"),
		filepath.Join(cfg.WorkspaceDir, "skills"),
		// Runtime sub-directories
		cfg.RuntimeDir,
		filepath.Join(cfg.RuntimeDir, "sessions"),
		filepath.Join(cfg.RuntimeDir, "outputs"),
		filepath.Join(cfg.RuntimeDir, "logs"),
		filepath.Join(cfg.RuntimeDir, "cache"),
		filepath.Join(cfg.RuntimeDir, "security"),
		filepath.Join(cfg.RuntimeDir, "cron-runs"),
		// Databases
		filepath.Join(cfg.BaseDir, "dbs"),
		// Vault (import snapshots)
		cfg.VaultDir,
		// Media assets
		filepath.Join(cfg.BaseDir, "media"),
		filepath.Join(cfg.BaseDir, "media", "sprites"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// Create agent directories for configured roles.
	for name := range cfg.Agents {
		agentDir := filepath.Join(cfg.AgentsDir, name)
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			return err
		}
	}
	// Write default sprite config if not present.
	if err := sprite.InitConfig(filepath.Join(cfg.BaseDir, "media", "sprites")); err != nil {
		log.Warn("sprite config init failed", "error", err)
	}

	log.Info("initialized directories", "agents", cfg.AgentsDir, "workspace", cfg.WorkspaceDir, "runtime", cfg.RuntimeDir)
	return nil
}

// ResolveSessionScope determines the trust, tool, and sandbox settings for a session.
// Session types: "main" (dashboard/CLI), "dm" (DM), "group" (group chat).
func ResolveSessionScope(cfg *config.Config, agentName string, sessionType string) SessionScope {
	scope := SessionScope{SessionType: sessionType}

	role, ok := cfg.Agents[agentName]
	if !ok {
		// Default scope for unknown agents.
		scope.TrustLevel = "auto"
		scope.ToolProfile = DefaultToolProfile(cfg)
		return scope
	}

	switch sessionType {
	case "main": // Dashboard/CLI - most trusted
		scope.TrustLevel = agentConfigTrustLevel(role)
		scope.ToolProfile = role.ToolPolicy.Profile
		if scope.ToolProfile == "" {
			scope.ToolProfile = DefaultToolProfile(cfg)
		}
		// Sandbox based on agent config.
		if role.Workspace.Sandbox != nil {
			scope.Sandbox = role.Workspace.Sandbox.Mode == "on"
		}

	case "dm": // Direct message - moderate trust
		scope.TrustLevel = MinTrust(agentConfigTrustLevel(role), "suggest")
		scope.ToolProfile = role.ToolPolicy.DMProfile
		if scope.ToolProfile == "" {
			scope.ToolProfile = role.ToolPolicy.Profile
		}
		if scope.ToolProfile == "" {
			scope.ToolProfile = "standard"
		}
		// DMs default to sandboxed unless explicitly disabled.
		scope.Sandbox = true
		if role.Workspace.Sandbox != nil && role.Workspace.Sandbox.Mode == "off" {
			scope.Sandbox = false
		}

	case "group": // Group chat - least trusted
		scope.TrustLevel = "observe" // most restrictive
		// Use groupProfile override if set, then fall back to profile; cap at standard.
		scope.ToolProfile = role.ToolPolicy.GroupProfile
		if scope.ToolProfile == "" {
			scope.ToolProfile = role.ToolPolicy.Profile
		}
		if scope.ToolProfile == "" || scope.ToolProfile == "full" {
			scope.ToolProfile = "standard"
		}
		scope.Sandbox = true // always sandboxed
	}

	return scope
}

// agentConfigTrustLevel returns the trust level from agent config, defaulting to "auto".
func agentConfigTrustLevel(role config.AgentConfig) string {
	if role.TrustLevel != "" {
		return role.TrustLevel
	}
	return "auto"
}

// DefaultToolProfile returns the default tool profile from config.
func DefaultToolProfile(cfg *config.Config) string {
	if cfg.Tools.DefaultProfile != "" {
		return cfg.Tools.DefaultProfile
	}
	return "standard"
}

// MinTrust returns the more restrictive of two trust levels.
// Trust levels in order: observe (0) < suggest (1) < auto (2).
// If a level is invalid, the other level is returned.
// If both are invalid, "observe" is returned.
func MinTrust(a, b string) string {
	levels := map[string]int{
		"observe": 0,
		"suggest": 1,
		"auto":    2,
	}

	levelA, okA := levels[a]
	levelB, okB := levels[b]

	// If both invalid, return observe.
	if !okA && !okB {
		return "observe"
	}

	// If only one is invalid, return the valid one.
	if !okA {
		return b
	}
	if !okB {
		return a
	}

	// Both valid, return the more restrictive.
	if levelA < levelB {
		return a
	}
	return b
}

// ResolveMCPServers returns the MCP servers available to an agent.
// If explicitly configured in workspace, use those.
// Otherwise, return all configured MCP servers.
func ResolveMCPServers(cfg *config.Config, agentName string) []string {
	role, ok := cfg.Agents[agentName]
	if !ok {
		return nil // no agent = no MCP servers
	}

	ws := role.Workspace
	if len(ws.MCPServers) > 0 {
		return ws.MCPServers // explicitly configured
	}

	// Default: all configured servers.
	servers := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		servers = append(servers, name)
	}
	return servers
}

// LoadSoulFile reads the agent's soul/personality file from the workspace.
// Returns empty string if the file doesn't exist or can't be read.
func LoadSoulFile(cfg *config.Config, agentName string) string {
	ws := ResolveWorkspace(cfg, agentName)
	if ws.SoulFile == "" {
		return ""
	}

	data, err := os.ReadFile(ws.SoulFile)
	if err != nil {
		// No soul file is OK, just log debug.
		log.Debug("no soul file found",
			"agent", agentName,
			"path", ws.SoulFile)
		return ""
	}

	log.Info("loaded soul file",
		"agent", agentName,
		"path", ws.SoulFile,
		"size", len(data))

	return string(data)
}

// GetWorkspaceMemoryPath returns the shared workspace memory directory path.
func GetWorkspaceMemoryPath(cfg *config.Config) string {
	return filepath.Join(cfg.WorkspaceDir, "memory")
}

// GetWorkspaceSkillsPath returns the shared workspace skills directory path.
func GetWorkspaceSkillsPath(cfg *config.Config) string {
	return filepath.Join(cfg.WorkspaceDir, "skills")
}

// InjectContent applies the workspace injection into a task's system prompt
// and addDirs. Rules are injected dynamically: INDEX.md is parsed to select
// always-on + keyword-matched rules for the given task prompt, and their full
// content is written to systemPrompt. When INDEX parsing fails, falls back to
// the legacy whole-directory DirIndex/addDirs behaviour. Knowledge/ uses the
// legacy behaviour unchanged. Agent-specific rules (files containing agentName)
// are appended unconditionally.
func InjectContent(cfg *config.Config, systemPrompt *string, addDirs *[]string, agentName, taskPrompt string) {
	if cfg.WorkspaceDir == "" {
		return
	}

	// Two-tier fallback injection for dirs without a dynamic matcher:
	//   ≤ indexThreshold : add dir to addDirs (provider --add-dir filesystem access)
	//   > indexThreshold : inject DirIndex (filename + first line per file) into systemPrompt
	//
	// There is no upper cliff. DirIndex output is bounded by file count, not
	// source size, so it degrades gracefully for arbitrarily large dirs.
	const indexThreshold = 20 * 1024 // 20KB
	const warnSize = 200 * 1024      // log-only warning; does not skip injection

	injectDir := func(dir string) {
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			return
		}
		size := DirSize(dir)
		if size > warnSize {
			log.Warn("workspace dir is very large; consider pruning",
				"dir", dir, "size", size)
		}
		if size > indexThreshold {
			idx := DirIndex(dir)
			if idx != "" {
				*systemPrompt += "\n\n" + idx
			}
			return
		}
		for _, d := range *addDirs {
			if d == dir {
				return
			}
		}
		*addDirs = append(*addDirs, dir)
	}

	// Inject lore first — world-building context is foundational, must precede rules.
	loreDir := filepath.Join(cfg.WorkspaceDir, "lore")
	if entries, err := os.ReadDir(loreDir); err == nil {
		var loreBlock strings.Builder
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, _ := os.ReadFile(filepath.Join(loreDir, e.Name()))
			if len(data) > 0 {
				loreBlock.Write(data)
				loreBlock.WriteString("\n\n")
			}
		}
		if loreBlock.Len() > 0 {
			*systemPrompt = strings.TrimSpace(loreBlock.String()) + "\n\n" + *systemPrompt
		}
	}

	rulesDir := filepath.Join(cfg.WorkspaceDir, "rules")
	if rulesBlock := rule.BuildPromptForAgent(cfg, rulesDir, taskPrompt, agentName); rulesBlock != "" {
		*systemPrompt += "\n\n" + rulesBlock
	} else {
		// No frontmatter or INDEX entries → fall back to legacy whole-dir injection.
		injectDir(rulesDir)
	}
	injectDir(filepath.Join(cfg.WorkspaceDir, "knowledge"))

	if agentName != "" {
		roleRules := AgentRules(rulesDir, agentName)
		if roleRules != "" {
			*systemPrompt += "\n\n" + roleRules
		}
	}
}

// DirIndex generates a compact markdown index of a directory.
// Each file is summarized by its first non-empty line (up to 100 chars).
func DirIndex(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	dirName := filepath.Base(dir)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Directory Index: %s\n\nUse `{{rules.FILENAME}}` to load a specific file on demand.\n\n", dirName))
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		summary := strings.TrimSpace(string(data))
		if idx := strings.IndexByte(summary, '\n'); idx >= 0 {
			summary = summary[:idx]
		}
		if len(summary) > 100 {
			summary = summary[:100] + "..."
		}
		summary = strings.TrimLeft(summary, "# ")
		b.WriteString(fmt.Sprintf("- **%s**: %s\n", e.Name(), summary))
		count++
	}
	if count == 0 {
		return ""
	}
	return b.String()
}

// AgentRules reads files in rulesDir whose name contains agentName (case-insensitive).
func AgentRules(rulesDir, agentName string) string {
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return ""
	}
	var parts []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.Contains(strings.ToLower(e.Name()), strings.ToLower(agentName)) {
			data, err := os.ReadFile(filepath.Join(rulesDir, e.Name()))
			if err == nil {
				parts = append(parts, string(data))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// DirSize returns the total size of all files (non-recursive) in a directory.
func DirSize(dir string) int {
	total := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			total += int(info.Size())
		}
	}
	return total
}
