//go:build !windows

package main

import "syscall"

// diskFreeBytes returns free disk space in bytes for the given path.
func diskFreeBytes(path string) uint64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return stat.Bavail * uint64(stat.Bsize)
}
