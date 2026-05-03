//go:build !windows

package health

import "syscall"

// DiskFreeBytes returns free disk space in bytes for the given path.
func DiskFreeBytes(path string) uint64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return stat.Bavail * uint64(stat.Bsize)
}
