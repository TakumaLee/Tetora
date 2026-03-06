package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Common binary search paths for macOS (launchd services have minimal PATH).
var darwinExtraPaths = []string{
	"/opt/homebrew/bin",
	"/usr/local/bin",
	"/opt/local/bin",
}

// findBinary looks up a binary by name, falling back to well-known paths
// if LookPath fails (common when running under launchd with minimal PATH).
func findBinary(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if runtime.GOOS == "darwin" {
		for _, dir := range darwinExtraPaths {
			p := dir + "/" + name
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// ensureTmux checks if tmux is installed, and auto-installs it if missing.
// Returns nil if tmux is available (already installed or successfully installed).
func ensureTmux() error {
	if findBinary("tmux") != "" {
		return nil // already installed
	}

	logInfo("tmux not found, attempting auto-install")

	switch runtime.GOOS {
	case "darwin":
		brew := findBinary("brew")
		if brew != "" {
			out, err := exec.Command(brew, "install", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("brew install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via Homebrew")
			return nil
		}
		port := findBinary("port")
		if port != "" {
			out, err := exec.Command("sudo", port, "install", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("port install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via MacPorts")
			return nil
		}
		return fmt.Errorf("tmux not found and no package manager (brew/port) available")

	case "linux":
		if p := findBinary("apt-get"); p != "" {
			out, err := exec.Command("sudo", p, "install", "-y", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("apt-get install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via apt")
			return nil
		}
		if p := findBinary("dnf"); p != "" {
			out, err := exec.Command("sudo", p, "install", "-y", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("dnf install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via dnf")
			return nil
		}
		if p := findBinary("yum"); p != "" {
			out, err := exec.Command("sudo", p, "install", "-y", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("yum install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via yum")
			return nil
		}
		if p := findBinary("apk"); p != "" {
			out, err := exec.Command("sudo", p, "add", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("apk add tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via apk")
			return nil
		}
		return fmt.Errorf("tmux not found; install manually with your package manager")

	default:
		return fmt.Errorf("tmux not found; auto-install not supported on %s", runtime.GOOS)
	}
}

// hasTmuxProvider returns true if any configured provider uses tmux.
func hasTmuxProvider(cfg *Config) bool {
	for _, pc := range cfg.Providers {
		t := strings.ToLower(pc.Type)
		if t == "claude-tmux" || t == "codex-tmux" {
			return true
		}
	}
	return false
}
