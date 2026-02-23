package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// securityMonitor
// ---------------------------------------------------------------------------

func newTestSecurityMonitor(threshold, windowMin int) (*securityMonitor, *[]string) {
	var alerts []string
	var mu sync.Mutex
	notifyFn := func(msg string) {
		mu.Lock()
		alerts = append(alerts, msg)
		mu.Unlock()
	}
	sm := &securityMonitor{
		events:        make(map[string][]time.Time),
		lastAlert:     make(map[string]time.Time),
		threshold:     threshold,
		windowMin:     windowMin,
		alertCooldown: 15 * time.Minute,
		notifyFn:      notifyFn,
	}
	return sm, &alerts
}

func TestSecurityMonitor_NilSafe(t *testing.T) {
	// Should not panic.
	var sm *securityMonitor
	sm.recordEvent("1.2.3.4", "test")
	sm.cleanup()
}

func TestSecurityMonitor_NoAlertBelowThreshold(t *testing.T) {
	sm, alerts := newTestSecurityMonitor(5, 5)

	for i := 0; i < 4; i++ {
		sm.recordEvent("1.2.3.4", "auth.fail")
	}

	// Give goroutine a moment if it were to fire (it shouldn't).
	time.Sleep(50 * time.Millisecond)
	if len(*alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(*alerts))
	}
}

func TestSecurityMonitor_AlertAtThreshold(t *testing.T) {
	sm, alerts := newTestSecurityMonitor(3, 5)

	for i := 0; i < 3; i++ {
		sm.recordEvent("1.2.3.4", "auth.fail")
	}

	// Wait for async notification.
	time.Sleep(100 * time.Millisecond)
	if len(*alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(*alerts))
	}
	if !strings.Contains((*alerts)[0], "1.2.3.4") {
		t.Errorf("alert should contain IP, got %q", (*alerts)[0])
	}
	if !strings.Contains((*alerts)[0], "[Security]") {
		t.Errorf("alert should contain [Security], got %q", (*alerts)[0])
	}
}

func TestSecurityMonitor_DedupSameIP(t *testing.T) {
	sm, alerts := newTestSecurityMonitor(2, 5)

	// First burst: 2 events -> alert.
	sm.recordEvent("1.2.3.4", "auth.fail")
	sm.recordEvent("1.2.3.4", "auth.fail")

	// Second burst: 2 more events -> should be deduped.
	sm.recordEvent("1.2.3.4", "auth.fail")
	sm.recordEvent("1.2.3.4", "auth.fail")

	time.Sleep(100 * time.Millisecond)
	if len(*alerts) != 1 {
		t.Errorf("expected 1 alert (dedup), got %d", len(*alerts))
	}
}

func TestSecurityMonitor_DifferentIPsSeparate(t *testing.T) {
	sm, alerts := newTestSecurityMonitor(2, 5)

	sm.recordEvent("1.1.1.1", "auth.fail")
	sm.recordEvent("1.1.1.1", "auth.fail")
	sm.recordEvent("2.2.2.2", "auth.fail")
	sm.recordEvent("2.2.2.2", "auth.fail")

	time.Sleep(100 * time.Millisecond)
	if len(*alerts) != 2 {
		t.Errorf("expected 2 alerts (different IPs), got %d", len(*alerts))
	}
}

func TestSecurityMonitor_Cleanup(t *testing.T) {
	sm, _ := newTestSecurityMonitor(10, 1) // 1 minute window

	// Add old events.
	sm.mu.Lock()
	sm.events["old-ip"] = []time.Time{time.Now().Add(-5 * time.Minute)}
	sm.lastAlert["old-ip"] = time.Now().Add(-20 * time.Minute)
	sm.mu.Unlock()

	sm.cleanup()

	sm.mu.Lock()
	_, eventsExist := sm.events["old-ip"]
	_, alertsExist := sm.lastAlert["old-ip"]
	sm.mu.Unlock()

	if eventsExist {
		t.Error("cleanup should remove expired events")
	}
	if alertsExist {
		t.Error("cleanup should remove expired alert dedup entries")
	}
}

func TestNewSecurityMonitor_Disabled(t *testing.T) {
	cfg := &Config{SecurityAlert: SecurityAlertConfig{Enabled: false}}
	sm := newSecurityMonitor(cfg, func(s string) {})
	if sm != nil {
		t.Error("expected nil when disabled")
	}
}

func TestNewSecurityMonitor_NilNotify(t *testing.T) {
	cfg := &Config{SecurityAlert: SecurityAlertConfig{Enabled: true}}
	sm := newSecurityMonitor(cfg, nil)
	if sm != nil {
		t.Error("expected nil when notifyFn is nil")
	}
}

func TestNewSecurityMonitor_Defaults(t *testing.T) {
	cfg := &Config{SecurityAlert: SecurityAlertConfig{Enabled: true}}
	sm := newSecurityMonitor(cfg, func(s string) {})
	if sm.threshold != 10 {
		t.Errorf("threshold = %d, want 10", sm.threshold)
	}
	if sm.windowMin != 5 {
		t.Errorf("windowMin = %d, want 5", sm.windowMin)
	}
}
