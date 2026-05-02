//go:build windows

package cron

func diskFreeBytes(_ string) uint64 { return 0 }
