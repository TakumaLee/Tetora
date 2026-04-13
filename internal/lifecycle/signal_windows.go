//go:build windows

package lifecycle

// SignalSelfReload is a no-op on Windows (SIGHUP not supported).
func SignalSelfReload() {}
