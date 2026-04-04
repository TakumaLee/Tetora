//go:build windows

package codex

import (
	"os"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return os.ErrProcessDone
	}
}
