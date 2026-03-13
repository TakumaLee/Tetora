//go:build !windows

package main

import "syscall"

// signalSelfReload sends SIGHUP to the current process to trigger a graceful reload.
func signalSelfReload() {
	syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
}
