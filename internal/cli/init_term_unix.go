//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"strings"
)

func menuSetRawMode() (string, error) {
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	saved := strings.TrimSpace(string(out))
	raw := exec.Command("stty", "raw", "-echo")
	raw.Stdin = os.Stdin
	if err := raw.Run(); err != nil {
		return "", err
	}
	return saved, nil
}

func menuRestoreMode(saved string) {
	cmd := exec.Command("stty", saved)
	cmd.Stdin = os.Stdin
	cmd.Run()
}
