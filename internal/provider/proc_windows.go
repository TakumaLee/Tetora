//go:build windows

package provider

import (
	"os"
	"os/exec"
)

// SetProcessGroup is a no-op on Windows (Setpgid / Kill(-pgid) not available).
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return os.ErrProcessDone
	}
}
