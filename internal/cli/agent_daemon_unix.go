//go:build !windows

package cli

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func buildDaemonCmd(executable string, interval time.Duration) *exec.Cmd {
	cmd := exec.Command(executable, "agent", "watch",
		fmt.Sprintf("--interval=%s", interval))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
