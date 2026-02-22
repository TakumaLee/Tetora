package main

import (
	"os"
	"path/filepath"
)

// --- Workspace Types ---

// WorkspaceConfig defines the isolated workspace for an agent role.
type WorkspaceConfig struct {
	Dir        string       `json:"dir,omitempty"`        // workspace directory path
	SoulFile   string       `json:"soulFile,omitempty"`   // agent personality file
	MCPServers []string     `json:"mcpServers,omitempty"` // allowed MCP server names
	Sandbox    *SandboxMode `json:"sandbox,omitempty"`    // sandbox mode override
}

// SandboxMode controls whether the agent runs in a sandboxed environment.
type SandboxMode struct {
	Mode string `json:"mode"` // "off", "on", "non-main"
}

// SessionScope defines trust and tool constraints per session type.
type SessionScope struct {
	SessionType string // "main", "dm", "group"
	TrustLevel  string // from role config or session-type default
	ToolProfile string // from role config or session-type default
	Sandbox     bool   // from role config + session type
}

// --- Workspace Resolution ---

// resolveWorkspace returns the effective workspace config for a role.
// Falls back to defaultWorkspace if the role is not found or workspace not configured.
func resolveWorkspace(cfg *Config, roleName string) WorkspaceConfig {
	role, ok := cfg.Roles[roleName]
	if !ok {
		return defaultWorkspace(cfg)
	}

	ws := role.Workspace

	// Set default workspace directory if not specified
	if ws.Dir == "" {
		ws.Dir = filepath.Join(cfg.baseDir, "workspaces", roleName)
	}

	// Set default soul file path if not specified
	if ws.SoulFile == "" {
		ws.SoulFile = filepath.Join(ws.Dir, "SOUL.md")
	}

	return ws
}

// defaultWorkspace returns the default workspace configuration.
func defaultWorkspace(cfg *Config) WorkspaceConfig {
	return WorkspaceConfig{
		Dir: cfg.DefaultWorkdir,
	}
}

// --- Workspace Initialization ---

// initWorkspaces ensures workspace directories exist for all configured roles.
// Creates workspace root directory and subdirectories (memory/, skills/).
func initWorkspaces(cfg *Config) error {
	for name := range cfg.Roles {
		ws := resolveWorkspace(cfg, name)
		if ws.Dir != "" {
			// Create main workspace directory
			if err := os.MkdirAll(ws.Dir, 0755); err != nil {
				return err
			}

			// Create subdirectories
			if err := os.MkdirAll(filepath.Join(ws.Dir, "memory"), 0755); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Join(ws.Dir, "skills"), 0755); err != nil {
				return err
			}

			logInfo("initialized workspace for role",
				"role", name,
				"dir", ws.Dir)
		}
	}
	return nil
}

// --- Session Scope Resolution ---

// resolveSessionScope determines the trust, tool, and sandbox settings for a session.
// Session types: "main" (dashboard/CLI), "dm" (DM), "group" (group chat).
func resolveSessionScope(cfg *Config, roleName string, sessionType string) SessionScope {
	scope := SessionScope{SessionType: sessionType}

	role, ok := cfg.Roles[roleName]
	if !ok {
		// Default scope for unknown roles
		scope.TrustLevel = "auto"
		scope.ToolProfile = defaultToolProfile(cfg)
		return scope
	}

	switch sessionType {
	case "main": // Dashboard/CLI - most trusted
		scope.TrustLevel = getRoleConfigTrustLevel(role)
		scope.ToolProfile = role.ToolPolicy.Profile
		if scope.ToolProfile == "" {
			scope.ToolProfile = defaultToolProfile(cfg)
		}
		// Sandbox based on role config
		if role.Workspace.Sandbox != nil {
			scope.Sandbox = role.Workspace.Sandbox.Mode == "on"
		}

	case "dm": // Direct message - moderate trust
		scope.TrustLevel = minTrust(getRoleConfigTrustLevel(role), "suggest")
		scope.ToolProfile = role.ToolPolicy.Profile
		if scope.ToolProfile == "" {
			scope.ToolProfile = "standard"
		}
		// DMs default to sandboxed unless explicitly disabled
		scope.Sandbox = true
		if role.Workspace.Sandbox != nil && role.Workspace.Sandbox.Mode == "off" {
			scope.Sandbox = false
		}

	case "group": // Group chat - least trusted
		scope.TrustLevel = "observe" // most restrictive
		scope.ToolProfile = "minimal"
		scope.Sandbox = true // always sandboxed
	}

	return scope
}

// getRoleConfigTrustLevel returns the trust level from role config, defaulting to "auto".
func getRoleConfigTrustLevel(role RoleConfig) string {
	if role.TrustLevel != "" {
		return role.TrustLevel
	}
	return "auto"
}

// defaultToolProfile returns the default tool profile from config.
func defaultToolProfile(cfg *Config) string {
	if cfg.Tools.DefaultProfile != "" {
		return cfg.Tools.DefaultProfile
	}
	return "standard"
}

// minTrust returns the more restrictive of two trust levels.
// Trust levels in order: observe (0) < suggest (1) < auto (2).
// If a level is invalid, the other level is returned.
// If both are invalid, "observe" is returned.
func minTrust(a, b string) string {
	levels := map[string]int{
		"observe": 0,
		"suggest": 1,
		"auto":    2,
	}

	levelA, okA := levels[a]
	levelB, okB := levels[b]

	// If both invalid, return observe
	if !okA && !okB {
		return "observe"
	}

	// If only one is invalid, return the valid one
	if !okA {
		return b
	}
	if !okB {
		return a
	}

	// Both valid, return the more restrictive
	if levelA < levelB {
		return a
	}
	return b
}

// --- MCP Server Scoping ---

// resolveMCPServers returns the MCP servers available to a role.
// If explicitly configured in workspace, use those.
// Otherwise, return all configured MCP servers.
func resolveMCPServers(cfg *Config, roleName string) []string {
	role, ok := cfg.Roles[roleName]
	if !ok {
		return nil // no role = no MCP servers
	}

	ws := role.Workspace
	if len(ws.MCPServers) > 0 {
		return ws.MCPServers // explicitly configured
	}

	// Default: all configured servers
	servers := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		servers = append(servers, name)
	}
	return servers
}

// --- Soul File Loading ---

// loadSoulFile reads the agent's soul/personality file from the workspace.
// Returns empty string if the file doesn't exist or can't be read.
func loadSoulFile(cfg *Config, roleName string) string {
	ws := resolveWorkspace(cfg, roleName)
	if ws.SoulFile == "" {
		return ""
	}

	data, err := os.ReadFile(ws.SoulFile)
	if err != nil {
		// No soul file is OK, just log debug
		logDebug("no soul file found",
			"role", roleName,
			"path", ws.SoulFile)
		return ""
	}

	logInfo("loaded soul file",
		"role", roleName,
		"path", ws.SoulFile,
		"size", len(data))

	return string(data)
}

// --- Workspace Memory Scope ---

// getWorkspaceMemoryPath returns the memory directory path for a role's workspace.
func getWorkspaceMemoryPath(cfg *Config, roleName string) string {
	ws := resolveWorkspace(cfg, roleName)
	if ws.Dir == "" {
		return ""
	}
	return filepath.Join(ws.Dir, "memory")
}

// getWorkspaceSkillsPath returns the skills directory path for a role's workspace.
func getWorkspaceSkillsPath(cfg *Config, roleName string) string {
	ws := resolveWorkspace(cfg, roleName)
	if ws.Dir == "" {
		return ""
	}
	return filepath.Join(ws.Dir, "skills")
}
