//go:build windows

package health

// DiskFreeBytes is not implemented on Windows — returns 0.
func DiskFreeBytes(path string) uint64 {
	return 0
}
