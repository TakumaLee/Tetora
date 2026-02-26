package main

import (
	"fmt"
	"sync"
	"time"
)

// --- Security Monitor ---

// securityMonitor tracks security-related events and sends alerts
// when suspicious patterns are detected (e.g. auth failure bursts).
type securityMonitor struct {
	mu            sync.Mutex
	events        map[string][]time.Time // ip -> event timestamps
	lastAlert     map[string]time.Time   // ip -> last alert time (dedup)
	threshold     int                    // number of failures to trigger alert
	windowMin     int                    // window in minutes
	alertCooldown time.Duration          // min time between alerts for same IP
	notifyFn      func(string)           // notification callback
}

func newSecurityMonitor(cfg *Config, notifyFn func(string)) *securityMonitor {
	if !cfg.SecurityAlert.Enabled || notifyFn == nil {
		return nil
	}
	threshold := cfg.SecurityAlert.FailThreshold
	if threshold <= 0 {
		threshold = 10
	}
	windowMin := cfg.SecurityAlert.FailWindowMin
	if windowMin <= 0 {
		windowMin = 5
	}
	return &securityMonitor{
		events:        make(map[string][]time.Time),
		lastAlert:     make(map[string]time.Time),
		threshold:     threshold,
		windowMin:     windowMin,
		alertCooldown: 15 * time.Minute,
		notifyFn:      notifyFn,
	}
}

// recordEvent records a security event for the given IP.
// If the event count exceeds the threshold within the window, an alert is sent.
func (sm *securityMonitor) recordEvent(ip, eventType string) {
	if sm == nil {
		return
	}

	sm.mu.Lock()

	now := time.Now()
	cutoff := now.Add(-time.Duration(sm.windowMin) * time.Minute)

	key := ip

	// Get or create event list.
	events := sm.events[key]

	// Trim old events outside window.
	start := 0
	for start < len(events) && events[start].Before(cutoff) {
		start++
	}
	events = events[start:]

	// Add new event.
	events = append(events, now)
	sm.events[key] = events

	// Check threshold.
	var alertMsg string
	if len(events) >= sm.threshold {
		// Dedup: don't alert same IP more than once per cooldown.
		if last, ok := sm.lastAlert[key]; !ok || now.Sub(last) >= sm.alertCooldown {
			sm.lastAlert[key] = now
			alertMsg = fmt.Sprintf("[Security] Suspicious activity from %s: %d %s events in %dm",
				ip, len(events), eventType, sm.windowMin)
		}
	}
	sm.mu.Unlock()

	// Send notification outside of the lock to avoid holding it during I/O.
	if alertMsg != "" {
		sm.notifyFn(alertMsg)
	}
}

// cleanup removes expired entries to prevent memory leak.
func (sm *securityMonitor) cleanup() {
	if sm == nil {
		return
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	cutoff := time.Now().Add(-time.Duration(sm.windowMin) * time.Minute)
	for ip, events := range sm.events {
		if len(events) == 0 || events[len(events)-1].Before(cutoff) {
			delete(sm.events, ip)
		}
	}

	// Clean up old alert dedup entries.
	alertCutoff := time.Now().Add(-sm.alertCooldown)
	for ip, last := range sm.lastAlert {
		if last.Before(alertCutoff) {
			delete(sm.lastAlert, ip)
		}
	}
}
