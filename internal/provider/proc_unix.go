//go:build !windows

package provider

import (
	"os"
	"os/exec"
	"syscall"
)

// SetProcessGroup configures the command to run in its own process group
// and sets a cancel function that kills the entire group.
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return os.ErrProcessDone
	}
}
