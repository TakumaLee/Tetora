//go:build windows

package main

// signalSelfReload is a no-op on Windows (SIGHUP not supported).
func signalSelfReload() {}
