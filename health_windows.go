//go:build windows

package main

// diskFreeBytes is not implemented on Windows — returns 0.
func diskFreeBytes(path string) uint64 {
	return 0
}
