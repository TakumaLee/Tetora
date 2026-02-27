package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestIsInteractiveSource(t *testing.T) {
	tests := []struct {
		source      string
		interactive bool
	}{
		// Interactive sources.
		{"route:discord", true},
		{"route:discord:guild123", true},
		{"route:telegram", true},
		{"route:telegram:private", true},
		{"route:slack", true},
		{"route:line", true},
		{"route:imessage", true},
		{"route:matrix", true},
		{"route:signal", true},
		{"route:teams", true},
		{"route:whatsapp", true},
		{"route:googlechat", true},
		{"ask", true},
		{"chat", true},

		// Non-interactive sources.
		{"cron", false},
		{"dispatch", false},
		{"queue", false},
		{"agent_dispatch", false},
		{"workflow:daily_review", false},
		{"reflection", false},
		{"taskboard", false},
		{"route-classify", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got := isInteractiveSource(tt.source)
			if got != tt.interactive {
				t.Errorf("isInteractiveSource(%q) = %v, want %v", tt.source, got, tt.interactive)
			}
		})
	}
}

func newTestGuard(semCap int, cfg SlotPressureConfig) (*SlotPressureGuard, chan struct{}) {
	sem := make(chan struct{}, semCap)
	g := &SlotPressureGuard{
		cfg:    cfg,
		sem:    sem,
		semCap: semCap,
	}
	return g, sem
}

func TestAcquireSlot_InteractiveNoWarning(t *testing.T) {
	g, sem := newTestGuard(8, SlotPressureConfig{Enabled: true, WarnThreshold: 3})
	ctx := context.Background()

	ar, err := g.AcquireSlot(ctx, sem, "route:discord")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ar.Warning != "" {
		t.Errorf("expected no warning, got %q", ar.Warning)
	}
	if g.active.Load() != 1 {
		t.Errorf("expected active=1, got %d", g.active.Load())
	}

	// Release and verify.
	g.ReleaseSlot()
	<-sem
	if g.active.Load() != 0 {
		t.Errorf("expected active=0 after release, got %d", g.active.Load())
	}
}

func TestAcquireSlot_InteractiveWithWarning(t *testing.T) {
	g, sem := newTestGuard(8, SlotPressureConfig{Enabled: true, WarnThreshold: 3})
	ctx := context.Background()

	// Fill 6 slots to leave only 2 available (<= warnThreshold of 3).
	for i := 0; i < 6; i++ {
		sem <- struct{}{}
		g.active.Add(1)
	}

	ar, err := g.AcquireSlot(ctx, sem, "route:telegram")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ar.Warning == "" {
		t.Error("expected warning when pressure is high, got empty")
	}

	// Cleanup.
	g.ReleaseSlot()
	<-sem
	for i := 0; i < 6; i++ {
		g.active.Add(-1)
		<-sem
	}
}

func TestAcquireSlot_NonInteractiveImmediate(t *testing.T) {
	g, sem := newTestGuard(8, SlotPressureConfig{Enabled: true, ReservedSlots: 2})
	ctx := context.Background()

	// Available = 8, reserved = 2 → 8 > 2, should acquire immediately.
	ar, err := g.AcquireSlot(ctx, sem, "cron")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ar.Warning != "" {
		t.Errorf("expected no warning for non-interactive, got %q", ar.Warning)
	}

	g.ReleaseSlot()
	<-sem
}

func TestAcquireSlot_NonInteractiveWaits(t *testing.T) {
	g, sem := newTestGuard(4, SlotPressureConfig{
		Enabled:               true,
		ReservedSlots:         2,
		PollInterval:          "50ms",
		NonInteractiveTimeout: "500ms",
	})
	ctx := context.Background()

	// Fill 2 slots → available=2, reserved=2 → 2 <= 2 → must wait.
	for i := 0; i < 2; i++ {
		sem <- struct{}{}
		g.active.Add(1)
	}

	done := make(chan error, 1)
	go func() {
		_, err := g.AcquireSlot(ctx, sem, "cron")
		done <- err
	}()

	// Should not complete immediately.
	select {
	case <-done:
		t.Fatal("non-interactive task should be waiting, not completed")
	case <-time.After(100 * time.Millisecond):
		// Good — it's waiting.
	}

	// Now release a slot.
	g.active.Add(-1)
	<-sem

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("non-interactive task should have acquired after slot release")
	}

	g.ReleaseSlot()
	<-sem
}

func TestAcquireSlot_NonInteractiveTimeout(t *testing.T) {
	g, sem := newTestGuard(4, SlotPressureConfig{
		Enabled:               true,
		ReservedSlots:         2,
		PollInterval:          "50ms",
		NonInteractiveTimeout: "200ms",
	})
	ctx := context.Background()

	// Fill 2 slots → available=2 == reserved=2 → must wait → timeout → force acquire.
	for i := 0; i < 2; i++ {
		sem <- struct{}{}
		g.active.Add(1)
	}

	start := time.Now()
	ar, err := g.AcquireSlot(ctx, sem, "dispatch")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ar.Warning != "" {
		t.Errorf("expected no warning for non-interactive, got %q", ar.Warning)
	}
	// Should have waited ~200ms (the timeout) before force-acquiring.
	if elapsed < 150*time.Millisecond {
		t.Errorf("expected to wait ~200ms, only waited %v", elapsed)
	}

	g.ReleaseSlot()
	<-sem
	for i := 0; i < 2; i++ {
		g.active.Add(-1)
		<-sem
	}
}

func TestAcquireSlot_NonInteractiveReleaseDuringWait(t *testing.T) {
	g, sem := newTestGuard(4, SlotPressureConfig{
		Enabled:               true,
		ReservedSlots:         2,
		PollInterval:          "50ms",
		NonInteractiveTimeout: "5s",
	})
	ctx := context.Background()

	// Fill 2 slots.
	for i := 0; i < 2; i++ {
		sem <- struct{}{}
		g.active.Add(1)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var acquireErr error
	go func() {
		defer wg.Done()
		_, acquireErr = g.AcquireSlot(ctx, sem, "queue")
	}()

	// Wait a bit then release a slot.
	time.Sleep(100 * time.Millisecond)
	g.active.Add(-1)
	<-sem

	wg.Wait()
	if acquireErr != nil {
		t.Fatalf("unexpected error: %v", acquireErr)
	}

	g.ReleaseSlot()
	<-sem
}

func TestAcquireSlot_ContextCancelled(t *testing.T) {
	g, sem := newTestGuard(4, SlotPressureConfig{
		Enabled:               true,
		ReservedSlots:         2,
		PollInterval:          "50ms",
		NonInteractiveTimeout: "5s",
	})
	ctx, cancel := context.WithCancel(context.Background())

	// Fill 2 slots → non-interactive will wait.
	for i := 0; i < 2; i++ {
		sem <- struct{}{}
		g.active.Add(1)
	}

	done := make(chan error, 1)
	go func() {
		_, err := g.AcquireSlot(ctx, sem, "cron")
		done <- err
	}()

	// Cancel context.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context cancellation error, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for context cancellation")
	}

	// Cleanup.
	for i := 0; i < 2; i++ {
		g.active.Add(-1)
		<-sem
	}
}

func TestAcquireSlot_GuardDisabled(t *testing.T) {
	// When guard is nil, callers should fall through to bare channel send.
	// This test verifies the pattern: check guard != nil before calling AcquireSlot.
	var g *SlotPressureGuard
	if g != nil {
		t.Fatal("nil guard should not reach AcquireSlot")
	}

	// Simulate the fallthrough: bare channel send works.
	sem := make(chan struct{}, 4)
	sem <- struct{}{}
	<-sem
}

func TestRunMonitor_AlertAndCooldown(t *testing.T) {
	g, sem := newTestGuard(4, SlotPressureConfig{
		Enabled:         true,
		WarnThreshold:   2,
		MonitorEnabled:  true,
		MonitorInterval: "50ms",
	})

	var mu sync.Mutex
	var alerts []string
	g.notifyFn = func(msg string) {
		mu.Lock()
		alerts = append(alerts, msg)
		mu.Unlock()
	}

	// Fill 3 slots → available=1 <= threshold=2 → should trigger alert.
	for i := 0; i < 3; i++ {
		sem <- struct{}{}
		g.active.Add(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go g.RunMonitor(ctx)

	// Wait for multiple monitor ticks.
	time.Sleep(300 * time.Millisecond)
	cancel()

	mu.Lock()
	alertCount := len(alerts)
	mu.Unlock()

	if alertCount == 0 {
		t.Error("expected at least one alert, got none")
	}
	// Due to 60s cooldown, we should only get 1 alert even with multiple ticks.
	if alertCount > 1 {
		t.Errorf("expected 1 alert due to cooldown, got %d", alertCount)
	}

	// Cleanup.
	for i := 0; i < 3; i++ {
		g.active.Add(-1)
		<-sem
	}
}

func TestSlotPressureGuard_Defaults(t *testing.T) {
	g, _ := newTestGuard(8, SlotPressureConfig{Enabled: true})

	if g.reservedSlots() != 2 {
		t.Errorf("default reservedSlots = %d, want 2", g.reservedSlots())
	}
	if g.warnThreshold() != 3 {
		t.Errorf("default warnThreshold = %d, want 3", g.warnThreshold())
	}
	if g.nonInteractiveTimeout() != 5*time.Minute {
		t.Errorf("default nonInteractiveTimeout = %v, want 5m", g.nonInteractiveTimeout())
	}
	if g.pollInterval() != 2*time.Second {
		t.Errorf("default pollInterval = %v, want 2s", g.pollInterval())
	}
	if g.monitorInterval() != 30*time.Second {
		t.Errorf("default monitorInterval = %v, want 30s", g.monitorInterval())
	}
}
