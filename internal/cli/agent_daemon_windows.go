//go:build windows

package cli

import (
	"fmt"
	"os/exec"
	"time"
)

func buildDaemonCmd(executable string, interval time.Duration) *exec.Cmd {
	return exec.Command(executable, "agent", "watch",
		fmt.Sprintf("--interval=%s", interval))
}
