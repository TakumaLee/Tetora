package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestBuildDockerCmd_BasicArgs(t *testing.T) {
	dcfg := DockerConfig{
		Image:   "tetora/sandbox:latest",
		Network: "none",
		Memory:  "512m",
		CPUs:    "1.0",
	}
	claudeArgs := []string{"--print", "--model", "opus", "hello"}
	cmd := buildDockerCmd(context.Background(), dcfg, "/tmp/work", "claude", claudeArgs, nil, "", nil)

	args := cmd.Args
	// Should start with "docker run --rm -i"
	if args[0] != "docker" || args[1] != "run" || args[2] != "--rm" || args[3] != "-i" {
		t.Errorf("expected docker run --rm -i prefix, got %v", args[:4])
	}

	// Check network
	assertContainsSequence(t, args, "--network", "none")
	// Check memory
	assertContainsSequence(t, args, "--memory", "512m")
	// Check cpus
	assertContainsSequence(t, args, "--cpus", "1.0")
	// Check workdir mount
	assertContainsSequence(t, args, "--workdir", "/workspace")
	assertContainsSequence(t, args, "-v", "/tmp/work:/workspace")
	// Check image
	assertContains(t, args, "tetora/sandbox:latest")
	// Check claude args
	assertContains(t, args, "claude")
	assertContains(t, args, "--print")
	assertContains(t, args, "hello")
}

func TestBuildDockerCmd_DefaultNetwork(t *testing.T) {
	dcfg := DockerConfig{Image: "test:latest"}
	cmd := buildDockerCmd(context.Background(), dcfg, "/work", "claude", []string{"hi"}, nil, "", nil)
	assertContainsSequence(t, cmd.Args, "--network", "none")
}

func TestBuildDockerCmd_ReadOnly(t *testing.T) {
	dcfg := DockerConfig{Image: "test:latest", ReadOnly: true}
	cmd := buildDockerCmd(context.Background(), dcfg, "/work", "claude", []string{"hi"}, nil, "", nil)
	assertContains(t, cmd.Args, "--read-only")
}

func TestBuildDockerCmd_NoReadOnly(t *testing.T) {
	dcfg := DockerConfig{Image: "test:latest", ReadOnly: false}
	cmd := buildDockerCmd(context.Background(), dcfg, "/work", "claude", []string{"hi"}, nil, "", nil)
	for _, a := range cmd.Args {
		if a == "--read-only" {
			t.Error("should not have --read-only when ReadOnly=false")
		}
	}
}

func TestBuildDockerCmd_AddDirs(t *testing.T) {
	dcfg := DockerConfig{Image: "test:latest"}
	addDirs := []string{"/home/user/project", "/opt/tools"}
	cmd := buildDockerCmd(context.Background(), dcfg, "/work", "claude", []string{"hi"}, addDirs, "", nil)
	assertContainsSequence(t, cmd.Args, "-v", "/home/user/project:/mnt/project")
	assertContainsSequence(t, cmd.Args, "-v", "/opt/tools:/mnt/tools")
}

func TestBuildDockerCmd_MCPMount(t *testing.T) {
	dcfg := DockerConfig{Image: "test:latest"}
	cmd := buildDockerCmd(context.Background(), dcfg, "/work", "claude", []string{"hi"}, nil, "/tmp/mcp-config.json", nil)
	assertContainsSequence(t, cmd.Args, "-v", "/tmp/mcp-config.json:/tmp/mcp.json:ro")
}

func TestBuildDockerCmd_EnvVars(t *testing.T) {
	dcfg := DockerConfig{Image: "test:latest"}
	envVars := []string{"ANTHROPIC_API_KEY=sk-123", "HOME=/home/user"}
	cmd := buildDockerCmd(context.Background(), dcfg, "/work", "claude", []string{"hi"}, nil, "", envVars)
	assertContainsSequence(t, cmd.Args, "-e", "ANTHROPIC_API_KEY=sk-123")
	assertContainsSequence(t, cmd.Args, "-e", "HOME=/home/user")
}

func TestBuildDockerCmd_NoWorkdir(t *testing.T) {
	dcfg := DockerConfig{Image: "test:latest"}
	cmd := buildDockerCmd(context.Background(), dcfg, "", "claude", []string{"hi"}, nil, "", nil)
	for _, a := range cmd.Args {
		if a == "--workdir" {
			t.Error("should not have --workdir when workdir is empty")
		}
	}
}

func TestBuildDockerCmd_HostNetwork(t *testing.T) {
	dcfg := DockerConfig{Image: "test:latest", Network: "host"}
	cmd := buildDockerCmd(context.Background(), dcfg, "/work", "claude", []string{"hi"}, nil, "", nil)
	assertContainsSequence(t, cmd.Args, "--network", "host")
}

// --- dockerEnvFilter tests ---

func TestDockerEnvFilter_DefaultWhitelist(t *testing.T) {
	// Set a known env var for testing.
	os.Setenv("ANTHROPIC_API_KEY", "test-key-123")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	dcfg := DockerConfig{}
	result := dockerEnvFilter(dcfg)

	foundAPIKey := false
	for _, env := range result {
		if strings.HasPrefix(env, "ANTHROPIC_API_KEY=") {
			foundAPIKey = true
		}
		// Should not include random env vars.
		key, _, _ := strings.Cut(env, "=")
		switch key {
		case "ANTHROPIC_API_KEY", "HOME", "PATH":
			// OK — whitelisted
		default:
			t.Errorf("unexpected env var in whitelist: %s", key)
		}
	}
	if !foundAPIKey {
		t.Error("expected ANTHROPIC_API_KEY in filtered env")
	}
}

func TestDockerEnvFilter_CustomEnvPass(t *testing.T) {
	os.Setenv("MY_CUSTOM_VAR", "myval")
	defer os.Unsetenv("MY_CUSTOM_VAR")

	dcfg := DockerConfig{EnvPass: []string{"MY_CUSTOM_VAR"}}
	result := dockerEnvFilter(dcfg)

	found := false
	for _, env := range result {
		if env == "MY_CUSTOM_VAR=myval" {
			found = true
		}
	}
	if !found {
		t.Error("expected MY_CUSTOM_VAR in filtered env with custom envPass")
	}
}

// --- rewriteDockerArgs tests ---

func TestRewriteDockerArgs_AddDirs(t *testing.T) {
	args := []string{"--print", "--add-dir", "/home/user/project", "--add-dir", "/opt/tools", "hello"}
	addDirs := []string{"/home/user/project", "/opt/tools"}
	result := rewriteDockerArgs(args, addDirs, "")

	expected := []string{"--print", "--add-dir", "/mnt/project", "--add-dir", "/mnt/tools", "hello"}
	for i, a := range result {
		if a != expected[i] {
			t.Errorf("arg[%d]: expected %q, got %q", i, expected[i], a)
		}
	}
}

func TestRewriteDockerArgs_MCPPath(t *testing.T) {
	args := []string{"--mcp-config", "/tmp/tetora-mcp-123.json", "hello"}
	result := rewriteDockerArgs(args, nil, "/tmp/tetora-mcp-123.json")

	if result[1] != "/tmp/mcp.json" {
		t.Errorf("expected /tmp/mcp.json, got %s", result[1])
	}
}

func TestRewriteDockerArgs_NoRewrite(t *testing.T) {
	args := []string{"--print", "--model", "opus", "hello"}
	result := rewriteDockerArgs(args, nil, "")
	for i, a := range result {
		if a != args[i] {
			t.Errorf("arg[%d] should be unchanged: expected %q, got %q", i, args[i], a)
		}
	}
}

// --- shouldUseDocker tests ---

func TestShouldUseDocker_TaskOverrideTrue(t *testing.T) {
	p := &ClaudeProvider{cfg: &Config{}}
	v := true
	req := ProviderRequest{Docker: &v}
	if !p.shouldUseDocker(req) {
		t.Error("expected true when task Docker=true")
	}
}

func TestShouldUseDocker_TaskOverrideFalse(t *testing.T) {
	p := &ClaudeProvider{cfg: &Config{Docker: DockerConfig{Enabled: true}}}
	v := false
	req := ProviderRequest{Docker: &v}
	if p.shouldUseDocker(req) {
		t.Error("expected false when task Docker=false overrides config")
	}
}

func TestShouldUseDocker_ConfigEnabled(t *testing.T) {
	p := &ClaudeProvider{cfg: &Config{Docker: DockerConfig{Enabled: true}}}
	req := ProviderRequest{}
	if !p.shouldUseDocker(req) {
		t.Error("expected true when config Docker.Enabled=true and no override")
	}
}

func TestShouldUseDocker_ConfigDisabled(t *testing.T) {
	p := &ClaudeProvider{cfg: &Config{}}
	req := ProviderRequest{}
	if p.shouldUseDocker(req) {
		t.Error("expected false when Docker not configured")
	}
}

// --- buildClaudeArgs tests ---

func TestBuildClaudeArgs_Basic(t *testing.T) {
	req := ProviderRequest{
		Model:          "opus",
		SessionID:      "s123",
		PermissionMode: "acceptEdits",
		Prompt:         "hello world",
	}
	args := buildClaudeArgs(req)
	assertContainsSequence(t, args, "--model", "opus")
	assertContainsSequence(t, args, "--session-id", "s123")
	assertContainsSequence(t, args, "--permission-mode", "acceptEdits")
	assertContains(t, args, "--print")
	assertContains(t, args, "--no-session-persistence")
	// Prompt should be last
	if args[len(args)-1] != "hello world" {
		t.Errorf("expected prompt as last arg, got %s", args[len(args)-1])
	}
}

func TestBuildClaudeArgs_WithBudget(t *testing.T) {
	req := ProviderRequest{
		Model:          "opus",
		SessionID:      "s",
		PermissionMode: "plan",
		Budget:         5.50,
		Prompt:         "hi",
	}
	args := buildClaudeArgs(req)
	assertContainsSequence(t, args, "--max-budget-usd", "5.50")
}

func TestBuildClaudeArgs_WithAddDirs(t *testing.T) {
	req := ProviderRequest{
		Model:          "opus",
		SessionID:      "s",
		PermissionMode: "plan",
		AddDirs:        []string{"/dir1", "/dir2"},
		Prompt:         "hi",
	}
	args := buildClaudeArgs(req)
	assertContainsSequence(t, args, "--add-dir", "/dir1")
	assertContainsSequence(t, args, "--add-dir", "/dir2")
}

func TestBuildClaudeArgs_WithMCP(t *testing.T) {
	req := ProviderRequest{
		Model:          "opus",
		SessionID:      "s",
		PermissionMode: "plan",
		MCPPath:        "/tmp/mcp.json",
		Prompt:         "hi",
	}
	args := buildClaudeArgs(req)
	assertContainsSequence(t, args, "--mcp-config", "/tmp/mcp.json")
}

func TestBuildClaudeArgs_WithSystemPrompt(t *testing.T) {
	req := ProviderRequest{
		Model:          "opus",
		SessionID:      "s",
		PermissionMode: "plan",
		SystemPrompt:   "You are a helper",
		Prompt:         "hi",
	}
	args := buildClaudeArgs(req)
	assertContainsSequence(t, args, "--append-system-prompt", "You are a helper")
}

// --- Test helpers ---

func assertContains(t *testing.T, args []string, val string) {
	t.Helper()
	for _, a := range args {
		if a == val {
			return
		}
	}
	t.Errorf("expected args to contain %q, got %v", val, args)
}

func assertContainsSequence(t *testing.T, args []string, key, val string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == val {
			return
		}
	}
	t.Errorf("expected args to contain %q %q sequence, got %v", key, val, args)
}
