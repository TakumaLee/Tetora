package main

import (
	"encoding/json"
	"fmt"
)

// formatDurationMs converts milliseconds to a human-readable string (e.g. "11.9s", "320ms").
func formatDurationMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, sec)
	}
	return fmt.Sprintf("%dm %ds", m, sec)
}

// formatTokenField renders the "今日 Token" embed value.
// When limit > 0 it shows a progress bar; otherwise plain numbers.
func formatTokenField(in, out, limit int) string {
	counts := fmt.Sprintf("%s in / %s out", formatTokenCount(in), formatTokenCount(out))
	if limit <= 0 {
		return counts
	}
	total := in + out
	pct := float64(total) / float64(limit) * 100
	if pct > 100 {
		pct = 100
	}
	const barWidth = 10
	filled := int(pct / 100 * barWidth)
	bar := ""
	for i := 0; i < barWidth; i++ {
		if i < filled {
			bar += "▓"
		} else {
			bar += "░"
		}
	}
	return fmt.Sprintf("%s %.0f%%\n%s", bar, pct, counts)
}

// formatTokenCount formats a token count with K/M suffix for readability.
func formatTokenCount(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// sliceContainsStr checks if a string slice contains a value.
// Also exported as discord.ContainsStr in internal/discord.
func sliceContainsStr(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// jsonUnmarshalBytes is a small helper to unmarshal JSON from bytes.
func jsonUnmarshalBytes(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
