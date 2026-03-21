//go:build !windows

package cli

// windowsInstall, windowsUninstall, windowsStatus are stubs on non-Windows.
// They are never called (service.go dispatches via runtime.GOOS), but must
// exist so the package compiles on all platforms.
func windowsInstall()   {}
func windowsUninstall() {}
func windowsStatus()    {}
