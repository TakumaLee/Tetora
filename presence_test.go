package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockPresenceSetter is a test double for PresenceSetter.
type mockPresenceSetter struct {
	name     string
	calls    atomic.Int64
	lastRef  string
	mu       sync.Mutex
}

func (m *mockPresenceSetter) SetTyping(ctx context.Context, channelRef string) error {
	m.calls.Add(1)
	m.mu.Lock()
	m.lastRef = channelRef
	m.mu.Unlock()
	return nil
}

func (m *mockPresenceSetter) PresenceName() string { return m.name }

func (m *mockPresenceSetter) callCount() int64 { return m.calls.Load() }

func (m *mockPresenceSetter) getLastRef() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastRef
}

// --- parseSourceChannel Tests ---

func TestParseSourceChannel(t *testing.T) {
	tests := []struct {
		source  string
		wantCh  string
		wantRef string
	}{
		{"", "", ""},
		{"telegram", "telegram", ""},
		{"telegram:12345", "telegram", "12345"},
		{"slack:C123", "slack", "C123"},
		{"discord:456789", "discord", "456789"},
		{"whatsapp:123", "whatsapp", "123"},
		{"chat:telegram:789", "telegram", "789"},
		{"route:slack:C456", "slack", "C456"},
		{"chat:discord:chan:extra", "discord", "chan:extra"},
	}

	for _, tt := range tests {
		ch, ref := parseSourceChannel(tt.source)
		if ch != tt.wantCh || ref != tt.wantRef {
			t.Errorf("parseSourceChannel(%q) = (%q, %q), want (%q, %q)",
				tt.source, ch, ref, tt.wantCh, tt.wantRef)
		}
	}
}

// --- presenceManager Lifecycle Tests ---

func TestPresenceManagerStartStop(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "telegram"}
	pm.RegisterSetter("telegram", mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pm.StartTyping(ctx, "telegram:12345")

	// Wait a bit for the first typing call.
	time.Sleep(100 * time.Millisecond)

	if mock.callCount() < 1 {
		t.Fatalf("expected at least 1 typing call, got %d", mock.callCount())
	}
	if mock.getLastRef() != "12345" {
		t.Errorf("expected lastRef=12345, got %q", mock.getLastRef())
	}

	pm.StopTyping("telegram:12345")

	// Verify the loop stopped by checking call count doesn't increase.
	countAfterStop := mock.callCount()
	time.Sleep(150 * time.Millisecond)
	if mock.callCount() > countAfterStop+1 {
		t.Errorf("typing loop did not stop: calls went from %d to %d",
			countAfterStop, mock.callCount())
	}
}

func TestPresenceManagerUnknownChannel(t *testing.T) {
	pm := newPresenceManager()

	ctx := context.Background()

	// Should not panic for unknown channels.
	pm.StartTyping(ctx, "unknown:123")
	pm.StopTyping("unknown:123")
}

func TestPresenceManagerEmptySource(t *testing.T) {
	pm := newPresenceManager()

	ctx := context.Background()

	// Should not panic for empty source.
	pm.StartTyping(ctx, "")
	pm.StopTyping("")
}

func TestPresenceManagerNoRef(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "telegram"}
	pm.RegisterSetter("telegram", mock)

	ctx := context.Background()

	// Source without ref should not start typing.
	pm.StartTyping(ctx, "telegram")
	time.Sleep(50 * time.Millisecond)

	if mock.callCount() != 0 {
		t.Errorf("expected 0 typing calls for source without ref, got %d", mock.callCount())
	}
}

func TestPresenceManagerChatPrefix(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "discord"}
	pm.RegisterSetter("discord", mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pm.StartTyping(ctx, "chat:discord:789")

	time.Sleep(100 * time.Millisecond)

	if mock.callCount() < 1 {
		t.Fatalf("expected at least 1 typing call for chat:discord:789, got %d", mock.callCount())
	}
	if mock.getLastRef() != "789" {
		t.Errorf("expected lastRef=789, got %q", mock.getLastRef())
	}

	pm.StopTyping("chat:discord:789")
}

func TestPresenceManagerConcurrentSessions(t *testing.T) {
	pm := newPresenceManager()
	mockTG := &mockPresenceSetter{name: "telegram"}
	mockDC := &mockPresenceSetter{name: "discord"}
	pm.RegisterSetter("telegram", mockTG)
	pm.RegisterSetter("discord", mockDC)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start typing in both channels simultaneously.
	pm.StartTyping(ctx, "telegram:111")
	pm.StartTyping(ctx, "discord:222")

	time.Sleep(100 * time.Millisecond)

	if mockTG.callCount() < 1 {
		t.Errorf("expected telegram typing calls, got %d", mockTG.callCount())
	}
	if mockDC.callCount() < 1 {
		t.Errorf("expected discord typing calls, got %d", mockDC.callCount())
	}

	// Stop both.
	pm.StopTyping("telegram:111")
	pm.StopTyping("discord:222")

	time.Sleep(100 * time.Millisecond)

	// Verify both active maps are clean.
	pm.mu.RLock()
	activeCount := len(pm.active)
	pm.mu.RUnlock()

	if activeCount != 0 {
		t.Errorf("expected 0 active entries after stop, got %d", activeCount)
	}
}

func TestPresenceManagerDoubleStart(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "slack"}
	pm.RegisterSetter("slack", mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Starting typing twice for the same source should cancel the first loop.
	pm.StartTyping(ctx, "slack:C123")
	time.Sleep(50 * time.Millisecond)
	pm.StartTyping(ctx, "slack:C123")
	time.Sleep(50 * time.Millisecond)

	pm.StopTyping("slack:C123")

	// Should have exactly one active entry removed.
	pm.mu.RLock()
	activeCount := len(pm.active)
	pm.mu.RUnlock()

	if activeCount != 0 {
		t.Errorf("expected 0 active entries, got %d", activeCount)
	}
}

func TestPresenceManagerContextCancellation(t *testing.T) {
	pm := newPresenceManager()
	mock := &mockPresenceSetter{name: "telegram"}
	pm.RegisterSetter("telegram", mock)

	ctx, cancel := context.WithCancel(context.Background())

	pm.StartTyping(ctx, "telegram:999")
	time.Sleep(50 * time.Millisecond)

	// Cancel context should stop the loop.
	cancel()
	time.Sleep(100 * time.Millisecond)

	countAfterCancel := mock.callCount()
	time.Sleep(150 * time.Millisecond)

	if mock.callCount() > countAfterCancel+1 {
		t.Errorf("typing loop did not stop after context cancel")
	}
}
