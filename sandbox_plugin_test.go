package main

// --- P13.2: Sandbox Plugin ---

import (
	"encoding/json"
	"testing"
)

// --- SandboxConfig defaults ---

func TestSandboxConfig_DefaultImage(t *testing.T) {
	sc := SandboxConfig{}
	if sc.defaultImageOrDefault() != "ubuntu:22.04" {
		t.Errorf("expected ubuntu:22.04, got %s", sc.defaultImageOrDefault())
	}
}

func TestSandboxConfig_CustomImage(t *testing.T) {
	sc := SandboxConfig{DefaultImage: "alpine:3.19"}
	if sc.defaultImageOrDefault() != "alpine:3.19" {
		t.Errorf("expected alpine:3.19, got %s", sc.defaultImageOrDefault())
	}
}

func TestSandboxConfig_DefaultNetwork(t *testing.T) {
	sc := SandboxConfig{}
	if sc.networkOrDefault() != "none" {
		t.Errorf("expected none, got %s", sc.networkOrDefault())
	}
}

func TestSandboxConfig_CustomNetwork(t *testing.T) {
	sc := SandboxConfig{Network: "bridge"}
	if sc.networkOrDefault() != "bridge" {
		t.Errorf("expected bridge, got %s", sc.networkOrDefault())
	}
}

// --- SandboxManager nil safety ---

func TestSandboxManager_NilAvailable(t *testing.T) {
	var sm *SandboxManager
	if sm.Available() {
		t.Error("nil SandboxManager should return false for Available()")
	}
}

func TestSandboxManager_NilPluginName(t *testing.T) {
	var sm *SandboxManager
	if sm.PluginName() != "" {
		t.Error("nil SandboxManager should return empty PluginName()")
	}
}

func TestSandboxManager_NilEnsureSandbox(t *testing.T) {
	var sm *SandboxManager
	_, err := sm.EnsureSandbox("sess1", "/tmp")
	if err == nil {
		t.Error("expected error from nil SandboxManager")
	}
}

func TestSandboxManager_NilExecInSandbox(t *testing.T) {
	var sm *SandboxManager
	_, err := sm.ExecInSandbox("sb1", "echo hello")
	if err == nil {
		t.Error("expected error from nil SandboxManager")
	}
}

func TestSandboxManager_NilDestroySandbox(t *testing.T) {
	var sm *SandboxManager
	err := sm.DestroySandbox("sb1")
	if err == nil {
		t.Error("expected error from nil SandboxManager")
	}
}

// --- SandboxManager without plugin ---

func TestSandboxManager_NoPlugin(t *testing.T) {
	cfg := &Config{}
	sm := NewSandboxManager(cfg, nil)
	if sm.Available() {
		t.Error("SandboxManager with no plugin should not be available")
	}
	if sm.PluginName() != "" {
		t.Errorf("expected empty plugin name, got %q", sm.PluginName())
	}
}

// --- SandboxManager plugin resolution ---

func TestSandboxManager_ExplicitPlugin(t *testing.T) {
	cfg := &Config{
		Sandbox: SandboxConfig{Plugin: "my-sandbox"},
		Plugins: map[string]PluginConfig{
			"my-sandbox": {Type: "sandbox", Command: "sandbox-bin"},
		},
	}
	sm := NewSandboxManager(cfg, nil)
	if sm.PluginName() != "my-sandbox" {
		t.Errorf("expected my-sandbox, got %s", sm.PluginName())
	}
}

func TestSandboxManager_AutoDiscover(t *testing.T) {
	cfg := &Config{
		Plugins: map[string]PluginConfig{
			"tool-plugin": {Type: "tool", Command: "tool-bin"},
			"docker-sb":   {Type: "sandbox", Command: "docker-sb-bin"},
		},
	}
	sm := NewSandboxManager(cfg, nil)
	if sm.PluginName() != "docker-sb" {
		t.Errorf("expected docker-sb, got %s", sm.PluginName())
	}
}

// --- sandboxPolicyForAgent ---

func TestSandboxPolicyForRole_Default(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"test": {ToolPolicy: AgentToolPolicy{}},
		},
	}
	if sandboxPolicyForAgent(cfg, "test") != "never" {
		t.Errorf("expected never, got %s", sandboxPolicyForAgent(cfg, "test"))
	}
}

func TestSandboxPolicyForRole_Required(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"dev": {ToolPolicy: AgentToolPolicy{Sandbox: "required"}},
		},
	}
	if sandboxPolicyForAgent(cfg, "dev") != "required" {
		t.Errorf("expected required, got %s", sandboxPolicyForAgent(cfg, "dev"))
	}
}

func TestSandboxPolicyForRole_Optional(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"test": {ToolPolicy: AgentToolPolicy{Sandbox: "optional"}},
		},
	}
	if sandboxPolicyForAgent(cfg, "test") != "optional" {
		t.Errorf("expected optional, got %s", sandboxPolicyForAgent(cfg, "test"))
	}
}

func TestSandboxPolicyForRole_Unknown(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"test": {ToolPolicy: AgentToolPolicy{Sandbox: "bogus"}},
		},
	}
	if sandboxPolicyForAgent(cfg, "test") != "never" {
		t.Errorf("expected never for unknown value, got %s", sandboxPolicyForAgent(cfg, "test"))
	}
}

func TestSandboxPolicyForRole_EmptyRole(t *testing.T) {
	cfg := &Config{}
	if sandboxPolicyForAgent(cfg, "") != "never" {
		t.Errorf("expected never for empty role")
	}
}

func TestSandboxPolicyForRole_MissingRole(t *testing.T) {
	cfg := &Config{Agents: map[string]AgentConfig{}}
	if sandboxPolicyForAgent(cfg, "nonexistent") != "never" {
		t.Errorf("expected never for missing role")
	}
}

// --- sandboxImageForAgent ---

func TestSandboxImageForRole_Default(t *testing.T) {
	cfg := &Config{}
	if sandboxImageForAgent(cfg, "") != "ubuntu:22.04" {
		t.Errorf("expected ubuntu:22.04, got %s", sandboxImageForAgent(cfg, ""))
	}
}

func TestSandboxImageForRole_ConfigDefault(t *testing.T) {
	cfg := &Config{Sandbox: SandboxConfig{DefaultImage: "debian:12"}}
	if sandboxImageForAgent(cfg, "") != "debian:12" {
		t.Errorf("expected debian:12, got %s", sandboxImageForAgent(cfg, ""))
	}
}

func TestSandboxImageForRole_RoleOverride(t *testing.T) {
	cfg := &Config{
		Sandbox: SandboxConfig{DefaultImage: "debian:12"},
		Agents: map[string]AgentConfig{
			"dev": {ToolPolicy: AgentToolPolicy{SandboxImage: "node:20"}},
		},
	}
	if sandboxImageForAgent(cfg, "dev") != "node:20" {
		t.Errorf("expected node:20, got %s", sandboxImageForAgent(cfg, "dev"))
	}
}

// --- shouldUseSandbox ---

func TestShouldUseSandbox_Never(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"test": {ToolPolicy: AgentToolPolicy{}},
		},
	}
	use, err := shouldUseSandbox(cfg, "test", nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if use {
		t.Error("expected no sandbox for policy=never")
	}
}

func TestShouldUseSandbox_RequiredNoPlugin(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"dev": {ToolPolicy: AgentToolPolicy{Sandbox: "required"}},
		},
	}
	_, err := shouldUseSandbox(cfg, "dev", nil)
	if err == nil {
		t.Error("expected error when sandbox required but no manager")
	}
}

func TestShouldUseSandbox_OptionalNoPlugin(t *testing.T) {
	cfg := &Config{
		Agents: map[string]AgentConfig{
			"dev": {ToolPolicy: AgentToolPolicy{Sandbox: "optional"}},
		},
	}
	use, err := shouldUseSandbox(cfg, "dev", nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if use {
		t.Error("expected no sandbox when optional but no manager")
	}
}

// --- SandboxManager active session tracking ---

func TestSandboxManager_ActiveSandboxes(t *testing.T) {
	cfg := &Config{}
	sm := NewSandboxManager(cfg, nil)

	// Manually populate active map for unit test.
	sm.mu.Lock()
	sm.active["sess1"] = "sb-001"
	sm.active["sess2"] = "sb-002"
	sm.mu.Unlock()

	active := sm.ActiveSandboxes()
	if len(active) != 2 {
		t.Errorf("expected 2 active sandboxes, got %d", len(active))
	}
	if active["sess1"] != "sb-001" {
		t.Errorf("expected sb-001 for sess1, got %s", active["sess1"])
	}
	if active["sess2"] != "sb-002" {
		t.Errorf("expected sb-002 for sess2, got %s", active["sess2"])
	}
}

func TestSandboxManager_DestroyBySessionNotFound(t *testing.T) {
	cfg := &Config{}
	sm := NewSandboxManager(cfg, nil)
	// Should not error for non-existent session.
	err := sm.DestroyBySession("nonexistent")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- AgentToolPolicy JSON serialization ---

func TestAgentToolPolicy_SandboxJSON(t *testing.T) {
	policy := AgentToolPolicy{
		Profile:      "standard",
		Sandbox:      "required",
		SandboxImage: "node:20",
	}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var decoded AgentToolPolicy
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.Sandbox != "required" {
		t.Errorf("expected sandbox=required, got %s", decoded.Sandbox)
	}
	if decoded.SandboxImage != "node:20" {
		t.Errorf("expected sandboxImage=node:20, got %s", decoded.SandboxImage)
	}
}

// --- SandboxConfig JSON serialization ---

func TestSandboxConfig_JSON(t *testing.T) {
	sc := SandboxConfig{
		Plugin:       "docker-sandbox",
		DefaultImage: "alpine:3.19",
		MemLimit:     "512m",
		CPULimit:     "1.0",
		Network:      "bridge",
	}
	data, err := json.Marshal(sc)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var decoded SandboxConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.Plugin != "docker-sandbox" {
		t.Errorf("expected docker-sandbox, got %s", decoded.Plugin)
	}
	if decoded.DefaultImage != "alpine:3.19" {
		t.Errorf("expected alpine:3.19, got %s", decoded.DefaultImage)
	}
	if decoded.MemLimit != "512m" {
		t.Errorf("expected 512m, got %s", decoded.MemLimit)
	}
	if decoded.CPULimit != "1.0" {
		t.Errorf("expected 1.0, got %s", decoded.CPULimit)
	}
	if decoded.Network != "bridge" {
		t.Errorf("expected bridge, got %s", decoded.Network)
	}
}

// --- Concurrent sandbox session test ---

func TestSandboxManager_ConcurrentSessions(t *testing.T) {
	cfg := &Config{}
	sm := NewSandboxManager(cfg, nil)

	// Simulate concurrent access to active map.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			defer func() { done <- true }()
			key := "sess-" + string(rune('0'+idx))
			sm.mu.Lock()
			sm.active[key] = "sb-" + string(rune('0'+idx))
			sm.mu.Unlock()

			sm.mu.RLock()
			_ = sm.active[key]
			sm.mu.RUnlock()

			_ = sm.ActiveSandboxes()
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
