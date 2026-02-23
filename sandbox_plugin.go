package main

// --- P13.2: Sandbox Plugin ---

import (
	"encoding/json"
	"fmt"
	"sync"
)

// --- Sandbox Config ---

// SandboxConfig configures the sandbox plugin system.
type SandboxConfig struct {
	Plugin       string `json:"plugin,omitempty"`       // plugin name (default: first "sandbox" type plugin)
	DefaultImage string `json:"defaultImage,omitempty"` // default Docker image (default "ubuntu:22.04")
	MemLimit     string `json:"memLimit,omitempty"`     // default mem limit (e.g. "512m")
	CPULimit     string `json:"cpuLimit,omitempty"`     // default CPU limit (e.g. "1.0")
	Network      string `json:"network,omitempty"`      // "none", "bridge" (default "none")
}

// defaultImageOrDefault returns the configured default image or "ubuntu:22.04".
func (c SandboxConfig) defaultImageOrDefault() string {
	if c.DefaultImage != "" {
		return c.DefaultImage
	}
	return "ubuntu:22.04"
}

// networkOrDefault returns the configured network or "none".
func (c SandboxConfig) networkOrDefault() string {
	if c.Network != "" {
		return c.Network
	}
	return "none"
}

// --- Sandbox Manager ---

// SandboxManager bridges the core dispatch system with the sandbox plugin.
// It manages per-session sandboxes via JSON-RPC calls to the plugin.
type SandboxManager struct {
	host   *PluginHost
	cfg    *Config
	plugin string            // resolved plugin name
	active map[string]string // sessionID -> sandboxID
	mu     sync.RWMutex
}

// NewSandboxManager creates a SandboxManager. It resolves the sandbox plugin
// name from config or auto-discovers the first "sandbox" type plugin.
func NewSandboxManager(cfg *Config, host *PluginHost) *SandboxManager {
	sm := &SandboxManager{
		host:   host,
		cfg:    cfg,
		active: make(map[string]string),
	}

	// Resolve plugin name.
	if cfg.Sandbox.Plugin != "" {
		sm.plugin = cfg.Sandbox.Plugin
	} else {
		// Auto-discover first sandbox-type plugin.
		for name, pcfg := range cfg.Plugins {
			if pcfg.Type == "sandbox" {
				sm.plugin = name
				break
			}
		}
	}

	return sm
}

// Available returns true if the sandbox plugin is configured and responsive.
func (sm *SandboxManager) Available() bool {
	if sm == nil || sm.host == nil || sm.plugin == "" {
		return false
	}

	health := sm.host.Health(sm.plugin)
	if healthy, ok := health["healthy"].(bool); ok {
		return healthy
	}
	return false
}

// PluginName returns the resolved sandbox plugin name.
func (sm *SandboxManager) PluginName() string {
	if sm == nil {
		return ""
	}
	return sm.plugin
}

// EnsureSandbox creates or returns an existing sandbox for the given session.
// workspace is the host directory to mount inside the sandbox.
func (sm *SandboxManager) EnsureSandbox(sessionID, workspace string) (string, error) {
	if sm == nil || sm.host == nil {
		return "", fmt.Errorf("sandbox manager not initialized")
	}
	if sm.plugin == "" {
		return "", fmt.Errorf("no sandbox plugin configured")
	}

	// Check if we already have a sandbox for this session.
	sm.mu.RLock()
	if sandboxID, ok := sm.active[sessionID]; ok {
		sm.mu.RUnlock()
		return sandboxID, nil
	}
	sm.mu.RUnlock()

	// Build create params from config defaults.
	params := map[string]any{
		"sessionId": sessionID,
		"workspace": workspace,
		"image":     sm.cfg.Sandbox.defaultImageOrDefault(),
		"network":   sm.cfg.Sandbox.networkOrDefault(),
	}
	if sm.cfg.Sandbox.MemLimit != "" {
		params["memLimit"] = sm.cfg.Sandbox.MemLimit
	}
	if sm.cfg.Sandbox.CPULimit != "" {
		params["cpuLimit"] = sm.cfg.Sandbox.CPULimit
	}

	result, err := sm.host.Call(sm.plugin, "sandbox/create", params)
	if err != nil {
		return "", fmt.Errorf("sandbox/create failed: %w", err)
	}

	// Parse response.
	var resp struct {
		SandboxID string `json:"sandboxId"`
		IsError   bool   `json:"isError"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", fmt.Errorf("parse sandbox/create response: %w", err)
	}
	if resp.IsError {
		return "", fmt.Errorf("sandbox/create error: %s", resp.Error)
	}
	if resp.SandboxID == "" {
		return "", fmt.Errorf("sandbox/create returned empty sandboxId")
	}

	sm.mu.Lock()
	sm.active[sessionID] = resp.SandboxID
	sm.mu.Unlock()

	logInfo("sandbox created", "sessionId", sessionID, "sandboxId", resp.SandboxID)
	return resp.SandboxID, nil
}

// EnsureSandboxWithImage creates a sandbox with a custom image override.
func (sm *SandboxManager) EnsureSandboxWithImage(sessionID, workspace, image string) (string, error) {
	if sm == nil || sm.host == nil {
		return "", fmt.Errorf("sandbox manager not initialized")
	}
	if sm.plugin == "" {
		return "", fmt.Errorf("no sandbox plugin configured")
	}

	// Check if we already have a sandbox for this session.
	sm.mu.RLock()
	if sandboxID, ok := sm.active[sessionID]; ok {
		sm.mu.RUnlock()
		return sandboxID, nil
	}
	sm.mu.RUnlock()

	if image == "" {
		image = sm.cfg.Sandbox.defaultImageOrDefault()
	}

	params := map[string]any{
		"sessionId": sessionID,
		"workspace": workspace,
		"image":     image,
		"network":   sm.cfg.Sandbox.networkOrDefault(),
	}
	if sm.cfg.Sandbox.MemLimit != "" {
		params["memLimit"] = sm.cfg.Sandbox.MemLimit
	}
	if sm.cfg.Sandbox.CPULimit != "" {
		params["cpuLimit"] = sm.cfg.Sandbox.CPULimit
	}

	result, err := sm.host.Call(sm.plugin, "sandbox/create", params)
	if err != nil {
		return "", fmt.Errorf("sandbox/create failed: %w", err)
	}

	var resp struct {
		SandboxID string `json:"sandboxId"`
		IsError   bool   `json:"isError"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", fmt.Errorf("parse sandbox/create response: %w", err)
	}
	if resp.IsError {
		return "", fmt.Errorf("sandbox/create error: %s", resp.Error)
	}
	if resp.SandboxID == "" {
		return "", fmt.Errorf("sandbox/create returned empty sandboxId")
	}

	sm.mu.Lock()
	sm.active[sessionID] = resp.SandboxID
	sm.mu.Unlock()

	logInfo("sandbox created", "sessionId", sessionID, "sandboxId", resp.SandboxID, "image", image)
	return resp.SandboxID, nil
}

// ExecInSandbox executes a command inside a sandbox container.
func (sm *SandboxManager) ExecInSandbox(sandboxID, command string) (string, error) {
	if sm == nil || sm.host == nil {
		return "", fmt.Errorf("sandbox manager not initialized")
	}
	if sm.plugin == "" {
		return "", fmt.Errorf("no sandbox plugin configured")
	}

	result, err := sm.host.Call(sm.plugin, "sandbox/exec", map[string]any{
		"sandboxId": sandboxID,
		"command":   command,
		"timeout":   120, // default 2 minute timeout
	})
	if err != nil {
		return "", fmt.Errorf("sandbox/exec failed: %w", err)
	}

	var resp struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exitCode"`
		IsError  bool   `json:"isError"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", fmt.Errorf("parse sandbox/exec response: %w", err)
	}
	if resp.IsError {
		return "", fmt.Errorf("sandbox/exec error: %s", resp.Error)
	}

	output := resp.Stdout
	if resp.Stderr != "" {
		output += "\n[stderr]\n" + resp.Stderr
	}
	if resp.ExitCode != 0 {
		return output, fmt.Errorf("exit code %d", resp.ExitCode)
	}

	return output, nil
}

// DestroySandbox removes a sandbox container and cleans up the session mapping.
func (sm *SandboxManager) DestroySandbox(sandboxID string) error {
	if sm == nil || sm.host == nil {
		return fmt.Errorf("sandbox manager not initialized")
	}
	if sm.plugin == "" {
		return fmt.Errorf("no sandbox plugin configured")
	}

	_, err := sm.host.Call(sm.plugin, "sandbox/destroy", map[string]any{
		"sandboxId": sandboxID,
	})

	// Remove from active map regardless of error (container may already be gone).
	sm.mu.Lock()
	for sid, sbid := range sm.active {
		if sbid == sandboxID {
			delete(sm.active, sid)
			break
		}
	}
	sm.mu.Unlock()

	if err != nil {
		return fmt.Errorf("sandbox/destroy failed: %w", err)
	}

	logInfo("sandbox destroyed", "sandboxId", sandboxID)
	return nil
}

// DestroyBySession destroys the sandbox associated with a session ID.
func (sm *SandboxManager) DestroyBySession(sessionID string) error {
	sm.mu.RLock()
	sandboxID, ok := sm.active[sessionID]
	sm.mu.RUnlock()

	if !ok {
		return nil // no sandbox for this session
	}

	return sm.DestroySandbox(sandboxID)
}

// ActiveSandboxes returns a copy of the active session-to-sandbox mapping.
func (sm *SandboxManager) ActiveSandboxes() map[string]string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	result := make(map[string]string, len(sm.active))
	for k, v := range sm.active {
		result[k] = v
	}
	return result
}

// DestroyAll destroys all active sandboxes (used during shutdown).
func (sm *SandboxManager) DestroyAll() {
	sm.mu.RLock()
	sandboxIDs := make([]string, 0, len(sm.active))
	for _, sbid := range sm.active {
		sandboxIDs = append(sandboxIDs, sbid)
	}
	sm.mu.RUnlock()

	for _, sbid := range sandboxIDs {
		if err := sm.DestroySandbox(sbid); err != nil {
			logWarn("destroy sandbox failed during shutdown", "sandboxId", sbid, "error", err)
		}
	}
}

// --- Sandbox Dispatch Helpers ---

// sandboxPolicyForRole returns the sandbox policy setting for a role.
// Returns "required", "optional", or "never" (default).
func sandboxPolicyForRole(cfg *Config, roleName string) string {
	if roleName == "" {
		return "never"
	}
	rc, ok := cfg.Roles[roleName]
	if !ok {
		return "never"
	}
	switch rc.ToolPolicy.Sandbox {
	case "required", "optional":
		return rc.ToolPolicy.Sandbox
	default:
		return "never"
	}
}

// sandboxImageForRole returns the sandbox image for a role.
// Priority: role SandboxImage -> config Sandbox.DefaultImage -> "ubuntu:22.04"
func sandboxImageForRole(cfg *Config, roleName string) string {
	if roleName != "" {
		if rc, ok := cfg.Roles[roleName]; ok {
			if rc.ToolPolicy.SandboxImage != "" {
				return rc.ToolPolicy.SandboxImage
			}
		}
	}
	return cfg.Sandbox.defaultImageOrDefault()
}

// shouldUseSandbox determines whether a task should use a sandbox,
// given the role policy and sandbox availability.
// Returns (useSandbox bool, err error).
// err is non-nil only when sandbox is required but unavailable.
func shouldUseSandbox(cfg *Config, roleName string, sm *SandboxManager) (bool, error) {
	policy := sandboxPolicyForRole(cfg, roleName)

	switch policy {
	case "required":
		if sm == nil || !sm.Available() {
			return false, fmt.Errorf("sandbox required for role %q but sandbox plugin is unavailable", roleName)
		}
		return true, nil
	case "optional":
		if sm != nil && sm.Available() {
			return true, nil
		}
		return false, nil // fallback to local execution
	default:
		return false, nil
	}
}
