//go:build !windows

package lifecycle

import "syscall"

// SignalSelfReload sends SIGHUP to the current process to trigger a graceful reload.
func SignalSelfReload() {
	syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
}
