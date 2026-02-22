package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspace_Defaults(t *testing.T) {
	cfg := &Config{
		DefaultWorkdir: "/tmp/tetora",
		baseDir:        "/home/user/.tetora",
		Roles: map[string]RoleConfig{
			"琉璃": {Model: "opus"},
		},
	}

	ws := resolveWorkspace(cfg, "琉璃")

	// Should use generated default path
	expectedDir := filepath.Join(cfg.baseDir, "workspaces", "琉璃")
	if ws.Dir != expectedDir {
		t.Errorf("Dir = %q, want %q", ws.Dir, expectedDir)
	}

	expectedSoulFile := filepath.Join(expectedDir, "SOUL.md")
	if ws.SoulFile != expectedSoulFile {
		t.Errorf("SoulFile = %q, want %q", ws.SoulFile, expectedSoulFile)
	}
}

func TestResolveWorkspace_CustomConfig(t *testing.T) {
	cfg := &Config{
		DefaultWorkdir: "/tmp/tetora",
		baseDir:        "/home/user/.tetora",
		Roles: map[string]RoleConfig{
			"琉璃": {
				Model: "opus",
				Workspace: WorkspaceConfig{
					Dir:        "/custom/workspace",
					SoulFile:   "/custom/soul.md",
					MCPServers: []string{"server1", "server2"},
				},
			},
		},
	}

	ws := resolveWorkspace(cfg, "琉璃")

	if ws.Dir != "/custom/workspace" {
		t.Errorf("Dir = %q, want /custom/workspace", ws.Dir)
	}
	if ws.SoulFile != "/custom/soul.md" {
		t.Errorf("SoulFile = %q, want /custom/soul.md", ws.SoulFile)
	}
	if len(ws.MCPServers) != 2 {
		t.Errorf("MCPServers len = %d, want 2", len(ws.MCPServers))
	}
}

func TestResolveWorkspace_UnknownRole(t *testing.T) {
	cfg := &Config{
		DefaultWorkdir: "/tmp/tetora",
		Roles:          map[string]RoleConfig{},
	}

	ws := resolveWorkspace(cfg, "unknown")

	if ws.Dir != cfg.DefaultWorkdir {
		t.Errorf("Dir = %q, want %q", ws.Dir, cfg.DefaultWorkdir)
	}
}

func TestResolveSessionScope_Main(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			DefaultProfile: "standard",
		},
		Roles: map[string]RoleConfig{
			"琉璃": {
				Model:      "opus",
				TrustLevel: "auto",
				ToolPolicy: RoleToolPolicy{
					Profile: "full",
				},
				Workspace: WorkspaceConfig{
					Sandbox: &SandboxMode{Mode: "off"},
				},
			},
		},
	}

	scope := resolveSessionScope(cfg, "琉璃", "main")

	if scope.SessionType != "main" {
		t.Errorf("SessionType = %q, want main", scope.SessionType)
	}
	if scope.TrustLevel != "auto" {
		t.Errorf("TrustLevel = %q, want auto", scope.TrustLevel)
	}
	if scope.ToolProfile != "full" {
		t.Errorf("ToolProfile = %q, want full", scope.ToolProfile)
	}
	if scope.Sandbox {
		t.Error("Sandbox = true, want false")
	}
}

func TestResolveSessionScope_DM(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			DefaultProfile: "standard",
		},
		Roles: map[string]RoleConfig{
			"琉璃": {
				Model:      "opus",
				TrustLevel: "auto",
				ToolPolicy: RoleToolPolicy{
					Profile: "standard",
				},
			},
		},
	}

	scope := resolveSessionScope(cfg, "琉璃", "dm")

	if scope.SessionType != "dm" {
		t.Errorf("SessionType = %q, want dm", scope.SessionType)
	}
	// DM should cap trust at "suggest" even if role is "auto"
	if scope.TrustLevel != "suggest" {
		t.Errorf("TrustLevel = %q, want suggest", scope.TrustLevel)
	}
	if scope.ToolProfile != "standard" {
		t.Errorf("ToolProfile = %q, want standard", scope.ToolProfile)
	}
	// DM should default to sandboxed
	if !scope.Sandbox {
		t.Error("Sandbox = false, want true")
	}
}

func TestResolveSessionScope_Group(t *testing.T) {
	cfg := &Config{
		Roles: map[string]RoleConfig{
			"琉璃": {
				Model:      "opus",
				TrustLevel: "auto",
				ToolPolicy: RoleToolPolicy{
					Profile: "full",
				},
			},
		},
	}

	scope := resolveSessionScope(cfg, "琉璃", "group")

	if scope.SessionType != "group" {
		t.Errorf("SessionType = %q, want group", scope.SessionType)
	}
	// Group should always be "observe" regardless of role config
	if scope.TrustLevel != "observe" {
		t.Errorf("TrustLevel = %q, want observe", scope.TrustLevel)
	}
	// Group should always use minimal tools
	if scope.ToolProfile != "minimal" {
		t.Errorf("ToolProfile = %q, want minimal", scope.ToolProfile)
	}
	// Group should always be sandboxed
	if !scope.Sandbox {
		t.Error("Sandbox = false, want true")
	}
}

func TestMinTrust(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want string
	}{
		{"observe", "suggest", "observe"},
		{"suggest", "observe", "observe"},
		{"auto", "suggest", "suggest"},
		{"suggest", "auto", "suggest"},
		{"auto", "observe", "observe"},
		{"observe", "auto", "observe"},
		{"invalid", "suggest", "suggest"},
		{"auto", "invalid", "auto"},
	}

	for _, tt := range tests {
		got := minTrust(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("minTrust(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestResolveMCPServers_Explicit(t *testing.T) {
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"server1": {},
			"server2": {},
			"server3": {},
		},
		Roles: map[string]RoleConfig{
			"琉璃": {
				Workspace: WorkspaceConfig{
					MCPServers: []string{"server1", "server2"},
				},
			},
		},
	}

	servers := resolveMCPServers(cfg, "琉璃")

	if len(servers) != 2 {
		t.Fatalf("len(servers) = %d, want 2", len(servers))
	}

	// Check servers are the explicitly configured ones
	found := make(map[string]bool)
	for _, s := range servers {
		found[s] = true
	}
	if !found["server1"] || !found["server2"] {
		t.Errorf("servers = %v, want [server1, server2]", servers)
	}
}

func TestResolveMCPServers_Default(t *testing.T) {
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"server1": {},
			"server2": {},
			"server3": {},
		},
		Roles: map[string]RoleConfig{
			"琉璃": {}, // No explicit MCP servers
		},
	}

	servers := resolveMCPServers(cfg, "琉璃")

	// Should return all configured servers
	if len(servers) != 3 {
		t.Errorf("len(servers) = %d, want 3", len(servers))
	}
}

func TestResolveMCPServers_UnknownRole(t *testing.T) {
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"server1": {},
		},
	}

	servers := resolveMCPServers(cfg, "unknown")

	if servers != nil {
		t.Errorf("servers = %v, want nil", servers)
	}
}

func TestInitWorkspaces(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		baseDir: tmpDir,
		Roles: map[string]RoleConfig{
			"琉璃": {Model: "opus"},
			"翡翠": {Model: "sonnet"},
		},
	}

	err := initWorkspaces(cfg)
	if err != nil {
		t.Fatalf("initWorkspaces failed: %v", err)
	}

	// Check workspace directories were created
	for _, role := range []string{"琉璃", "翡翠"} {
		ws := resolveWorkspace(cfg, role)

		// Check main workspace dir
		if _, err := os.Stat(ws.Dir); os.IsNotExist(err) {
			t.Errorf("workspace dir not created: %s", ws.Dir)
		}

		// Check memory subdir
		memoryDir := filepath.Join(ws.Dir, "memory")
		if _, err := os.Stat(memoryDir); os.IsNotExist(err) {
			t.Errorf("memory dir not created: %s", memoryDir)
		}

		// Check skills subdir
		skillsDir := filepath.Join(ws.Dir, "skills")
		if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
			t.Errorf("skills dir not created: %s", skillsDir)
		}
	}
}

func TestLoadSoulFile(t *testing.T) {
	tmpDir := t.TempDir()
	soulFile := filepath.Join(tmpDir, "SOUL.md")
	soulContent := "I am 琉璃, the coordinator agent."

	// Create soul file
	if err := os.WriteFile(soulFile, []byte(soulContent), 0644); err != nil {
		t.Fatalf("failed to create test soul file: %v", err)
	}

	cfg := &Config{
		Roles: map[string]RoleConfig{
			"琉璃": {
				Workspace: WorkspaceConfig{
					SoulFile: soulFile,
				},
			},
		},
	}

	content := loadSoulFile(cfg, "琉璃")
	if content != soulContent {
		t.Errorf("loadSoulFile = %q, want %q", content, soulContent)
	}
}

func TestLoadSoulFile_NotExist(t *testing.T) {
	cfg := &Config{
		Roles: map[string]RoleConfig{
			"琉璃": {
				Workspace: WorkspaceConfig{
					SoulFile: "/nonexistent/soul.md",
				},
			},
		},
	}

	content := loadSoulFile(cfg, "琉璃")
	if content != "" {
		t.Errorf("loadSoulFile = %q, want empty string", content)
	}
}

func TestGetWorkspaceMemoryPath(t *testing.T) {
	cfg := &Config{
		baseDir: "/home/user/.tetora",
		Roles: map[string]RoleConfig{
			"琉璃": {},
		},
	}

	path := getWorkspaceMemoryPath(cfg, "琉璃")
	expected := filepath.Join("/home/user/.tetora", "workspaces", "琉璃", "memory")

	if path != expected {
		t.Errorf("getWorkspaceMemoryPath = %q, want %q", path, expected)
	}
}

func TestGetWorkspaceSkillsPath(t *testing.T) {
	cfg := &Config{
		baseDir: "/home/user/.tetora",
		Roles: map[string]RoleConfig{
			"琉璃": {},
		},
	}

	path := getWorkspaceSkillsPath(cfg, "琉璃")
	expected := filepath.Join("/home/user/.tetora", "workspaces", "琉璃", "skills")

	if path != expected {
		t.Errorf("getWorkspaceSkillsPath = %q, want %q", path, expected)
	}
}
