// quiet_inline.go — inlined from archived internal/quiet package.
// Only the Config type and IsQuietHours function are needed by the CLI.
package cli

import (
	"strings"
	"time"
)

// quietConfig holds the quiet hours configuration fields.
type quietConfig struct {
	Enabled bool
	Start   string
	End     string
	TZ      string
	Digest  bool
}

// isQuietHours returns true if the current time is within the configured quiet period.
func isQuietHours(cfg quietConfig) bool {
	if !cfg.Enabled {
		return false
	}
	if cfg.Start == "" || cfg.End == "" {
		return false
	}

	loc := time.Local
	if cfg.TZ != "" {
		if l, err := time.LoadLocation(cfg.TZ); err == nil {
			loc = l
		}
	}

	now := time.Now().In(loc)
	startH, startM := parseHHMM(cfg.Start)
	endH, endM := parseHHMM(cfg.End)

	if startH < 0 || endH < 0 {
		return false
	}

	nowMin := now.Hour()*60 + now.Minute()
	startMin := startH*60 + startM
	endMin := endH*60 + endM

	if startMin <= endMin {
		// Same day: e.g. 09:00 - 17:00
		return nowMin >= startMin && nowMin < endMin
	}
	// Overnight: e.g. 23:00 - 08:00
	return nowMin >= startMin || nowMin < endMin
}

// parseHHMM parses "HH:MM" format. Returns -1,-1 on error.
func parseHHMM(s string) (int, int) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return -1, -1
	}
	h, m := 0, 0
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return -1, -1
		}
		h = h*10 + int(c-'0')
	}
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return -1, -1
		}
		m = m*10 + int(c-'0')
	}
	if h > 23 || m > 59 {
		return -1, -1
	}
	return h, m
}
