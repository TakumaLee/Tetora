//go:build !windows

package main

import "tetora/internal/lifecycle"

// signalSelfReload is a thin shim that delegates to the lifecycle package.
func signalSelfReload() {
	lifecycle.SignalSelfReload()
}
