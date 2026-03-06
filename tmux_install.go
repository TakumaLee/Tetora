package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ensureTmux checks if tmux is installed, and auto-installs it if missing.
// Returns nil if tmux is available (already installed or successfully installed).
func ensureTmux() error {
	if _, err := exec.LookPath("tmux"); err == nil {
		return nil // already installed
	}

	logInfo("tmux not found, attempting auto-install")

	switch runtime.GOOS {
	case "darwin":
		// Try Homebrew first, then MacPorts.
		if _, err := exec.LookPath("brew"); err == nil {
			out, err := exec.Command("brew", "install", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("brew install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via Homebrew")
			return nil
		}
		if _, err := exec.LookPath("port"); err == nil {
			out, err := exec.Command("sudo", "port", "install", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("port install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via MacPorts")
			return nil
		}
		return fmt.Errorf("tmux not found; install manually: brew install tmux")

	case "linux":
		// Try apt, then yum/dnf, then apk.
		if _, err := exec.LookPath("apt-get"); err == nil {
			out, err := exec.Command("sudo", "apt-get", "install", "-y", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("apt-get install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via apt")
			return nil
		}
		if _, err := exec.LookPath("dnf"); err == nil {
			out, err := exec.Command("sudo", "dnf", "install", "-y", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("dnf install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via dnf")
			return nil
		}
		if _, err := exec.LookPath("yum"); err == nil {
			out, err := exec.Command("sudo", "yum", "install", "-y", "tmux").CombinedOutput()
			if err != nil {
				return fmt.Errorf("yum install tmux failed: %v\n%s", err, string(out))
			}
			logInfo("tmux installed via yum")
			return nil
		}
		if _, err := exec.LookPath("apk"); err == nil {
			out, err := exec.Command("sudo", "apk", "add", "tmux").CombinedOutput()
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
