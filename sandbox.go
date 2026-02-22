package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DockerConfig holds Docker sandbox settings for isolated execution.
type DockerConfig struct {
	Enabled  bool     `json:"enabled"`
	Image    string   `json:"image,omitempty"`      // e.g. "tetora/sandbox:latest"
	Network  string   `json:"network,omitempty"`    // "none" (default) | "host" | "bridge"
	Memory   string   `json:"memory,omitempty"`     // e.g. "512m", "2g"
	CPUs     string   `json:"cpus,omitempty"`       // e.g. "1.0", "2.0"
	ReadOnly bool     `json:"readOnly,omitempty"`   // --read-only root filesystem
	EnvPass  []string `json:"envPass,omitempty"`    // env var names to pass through
}

// buildDockerCmd wraps a claude CLI invocation in `docker run`.
// The workdir is mounted at /workspace inside the container.
// addDirs get unique mount points under /mnt/.
// MCPPath is mounted at /tmp/mcp.json if set.
func buildDockerCmd(ctx context.Context, dcfg DockerConfig, workdir string, claudePath string, claudeArgs []string, addDirs []string, mcpPath string, envVars []string) *exec.Cmd {
	args := []string{"run", "--rm", "-i"}

	// Resource limits.
	network := dcfg.Network
	if network == "" {
		network = "none"
	}
	args = append(args, "--network", network)

	if dcfg.Memory != "" {
		args = append(args, "--memory", dcfg.Memory)
	}
	if dcfg.CPUs != "" {
		args = append(args, "--cpus", dcfg.CPUs)
	}
	if dcfg.ReadOnly {
		args = append(args, "--read-only")
	}

	// Mount workdir.
	if workdir != "" {
		args = append(args, "--workdir", "/workspace")
		args = append(args, "-v", workdir+":/workspace")
	}

	// Mount addDirs (each gets /mnt/<basename>).
	for _, dir := range addDirs {
		base := filepath.Base(dir)
		mountPoint := "/mnt/" + base
		args = append(args, "-v", dir+":"+mountPoint)
	}

	// Mount MCP config if set.
	if mcpPath != "" {
		args = append(args, "-v", mcpPath+":/tmp/mcp.json:ro")
	}

	// Pass whitelisted environment variables.
	for _, env := range envVars {
		args = append(args, "-e", env)
	}

	// Image.
	args = append(args, dcfg.Image)

	// Claude command + args inside container.
	args = append(args, claudePath)
	args = append(args, claudeArgs...)

	return exec.CommandContext(ctx, "docker", args...)
}

// dockerEnvFilter returns KEY=VALUE pairs for whitelisted environment variables.
// Default whitelist: ANTHROPIC_API_KEY, HOME, PATH.
// Additional vars from DockerConfig.EnvPass are included.
func dockerEnvFilter(dcfg DockerConfig) []string {
	whitelist := map[string]bool{
		"ANTHROPIC_API_KEY": true,
		"HOME":              true,
		"PATH":              true,
	}
	for _, key := range dcfg.EnvPass {
		whitelist[key] = true
	}

	var result []string
	for _, env := range os.Environ() {
		key, _, ok := strings.Cut(env, "=")
		if ok && whitelist[key] {
			result = append(result, env)
		}
	}
	return result
}

// rewriteDockerArgs adjusts claude CLI arguments for Docker context.
// - Rewrites --add-dir paths to /mnt/<basename>
// - Rewrites --mcp-config path to /tmp/mcp.json
// - Rewrites workdir-relative references
func rewriteDockerArgs(claudeArgs []string, addDirs []string, mcpPath string) []string {
	rewritten := make([]string, len(claudeArgs))
	copy(rewritten, claudeArgs)

	// Build mapping of host addDir → container path.
	dirMap := make(map[string]string)
	for _, dir := range addDirs {
		base := filepath.Base(dir)
		dirMap[dir] = "/mnt/" + base
	}

	for i := 0; i < len(rewritten); i++ {
		if rewritten[i] == "--add-dir" && i+1 < len(rewritten) {
			if mapped, ok := dirMap[rewritten[i+1]]; ok {
				rewritten[i+1] = mapped
			}
		}
		if rewritten[i] == "--mcp-config" && i+1 < len(rewritten) && mcpPath != "" {
			rewritten[i+1] = "/tmp/mcp.json"
		}
	}

	return rewritten
}

// checkDockerAvailable verifies that docker CLI is in PATH and the daemon is accessible.
func checkDockerAvailable() error {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker not found in PATH")
	}

	out, err := exec.Command(dockerPath, "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return fmt.Errorf("docker daemon not accessible: %v", err)
	}

	version := strings.TrimSpace(string(out))
	if version == "" {
		return fmt.Errorf("docker daemon returned empty version")
	}

	return nil
}

// checkDockerImage verifies that a Docker image exists locally.
func checkDockerImage(image string) error {
	err := exec.Command("docker", "image", "inspect", image).Run()
	if err != nil {
		return fmt.Errorf("image %q not found locally (docker pull %s)", image, image)
	}
	return nil
}
