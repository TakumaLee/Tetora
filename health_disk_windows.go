//go:build windows

package main

func rootDiskFreeBytes(_ string) uint64 { return 0 }
