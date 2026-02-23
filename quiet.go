package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// quietState manages quiet hours notification queue.
type quietState struct {
	mu       sync.Mutex
	queue    []quietEntry
	wasQuiet bool // was in quiet hours on last check
}

type quietEntry struct {
	message   string
	timestamp time.Time
}

var quiet = &quietState{}

// isQuietHours returns true if the current time is within the configured quiet period.
func isQuietHours(cfg *Config) bool {
	if !cfg.QuietHours.Enabled {
		return false
	}
	if cfg.QuietHours.Start == "" || cfg.QuietHours.End == "" {
		return false
	}

	loc := time.Local
	if cfg.QuietHours.TZ != "" {
		if l, err := time.LoadLocation(cfg.QuietHours.TZ); err == nil {
			loc = l
		}
	}

	now := time.Now().In(loc)
	startH, startM := parseHHMM(cfg.QuietHours.Start)
	endH, endM := parseHHMM(cfg.QuietHours.End)

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

// enqueueQuietNotification adds a notification to the quiet hours queue.
func (qs *quietState) enqueue(msg string) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	qs.queue = append(qs.queue, quietEntry{
		message:   msg,
		timestamp: time.Now(),
	})
	logInfo("quiet hours notification queued", "queueSize", len(qs.queue))
}

// flushDigest sends accumulated notifications as a digest and clears the queue.
func (qs *quietState) flushDigest(cfg *Config, notifyFn func(string)) {
	qs.mu.Lock()
	entries := qs.queue
	qs.queue = nil
	qs.mu.Unlock()

	if len(entries) == 0 || notifyFn == nil {
		return
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Tetora Digest (%s - %s)\n",
		cfg.QuietHours.Start, cfg.QuietHours.End))
	lines = append(lines, fmt.Sprintf("%d notifications during quiet hours:\n", len(entries)))

	for _, e := range entries {
		// Truncate each entry to keep digest readable.
		msg := e.message
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		lines = append(lines, msg)
	}

	digest := strings.Join(lines, "\n")
	logInfo("quiet hours digest flushing", "entries", len(entries))
	notifyFn(digest)
}

// queuedCount returns the number of queued notifications.
func (qs *quietState) queuedCount() int {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	return len(qs.queue)
}

// checkQuietTransition checks if we just left quiet hours and flushes digest.
// Called from cron tick. Returns true if currently in quiet hours.
func (qs *quietState) checkQuietTransition(cfg *Config, notifyFn func(string)) bool {
	inQuiet := isQuietHours(cfg)

	qs.mu.Lock()
	wasQuiet := qs.wasQuiet
	qs.wasQuiet = inQuiet
	qs.mu.Unlock()

	// Just left quiet hours — flush digest.
	if wasQuiet && !inQuiet && cfg.QuietHours.Digest {
		qs.flushDigest(cfg, notifyFn)
	}

	return inQuiet
}
