package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Priority Tests ---

func TestPriorityRank(t *testing.T) {
	tests := []struct {
		priority string
		rank     int
	}{
		{PriorityCritical, 4},
		{PriorityHigh, 3},
		{PriorityNormal, 2},
		{PriorityLow, 1},
		{"unknown", 2}, // defaults to normal
		{"", 2},
	}
	for _, tt := range tests {
		if got := priorityRank(tt.priority); got != tt.rank {
			t.Errorf("priorityRank(%q) = %d, want %d", tt.priority, got, tt.rank)
		}
	}
}

func TestPriorityFromRank(t *testing.T) {
	for _, p := range []string{PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow} {
		rank := priorityRank(p)
		got := priorityFromRank(rank)
		if got != p {
			t.Errorf("priorityFromRank(%d) = %q, want %q", rank, got, p)
		}
	}
}

func TestIsValidPriority(t *testing.T) {
	for _, p := range []string{PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow} {
		if !isValidPriority(p) {
			t.Errorf("isValidPriority(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"", "unknown", "CRITICAL", "Critical"} {
		if isValidPriority(p) {
			t.Errorf("isValidPriority(%q) = true, want false", p)
		}
	}
}

// --- Dedup Key Tests ---

func TestNotifyMessageDedupKey(t *testing.T) {
	m1 := NotifyMessage{EventType: "task.complete", Role: "琉璃"}
	m2 := NotifyMessage{EventType: "task.complete", Role: "琉璃"}
	m3 := NotifyMessage{EventType: "task.complete", Role: "黒曜"}

	if m1.dedupKey() != m2.dedupKey() {
		t.Error("same event+role should have same dedup key")
	}
	if m1.dedupKey() == m3.dedupKey() {
		t.Error("different role should have different dedup key")
	}
}

// --- Mock Notifier ---

type mockIntelNotifier struct {
	mu       sync.Mutex
	name     string
	messages []string
}

func (m *mockIntelNotifier) Send(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, text)
	return nil
}

func (m *mockIntelNotifier) Name() string { return m.name }

func (m *mockIntelNotifier) messageCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

func (m *mockIntelNotifier) lastMessage() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return ""
	}
	return m.messages[len(m.messages)-1]
}

// --- Engine Tests ---

func TestNotificationEngine_ImmediateCritical(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack", MinPriority: ""},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{
		Priority:  PriorityCritical,
		EventType: "sla.violation",
		Text:      "SLA violation on 琉璃",
	})

	// Critical should be delivered immediately.
	if n.messageCount() != 1 {
		t.Fatalf("expected 1 message, got %d", n.messageCount())
	}
	if !strings.Contains(n.lastMessage(), "CRITICAL") {
		t.Errorf("expected [CRITICAL] prefix, got %q", n.lastMessage())
	}
	if !strings.Contains(n.lastMessage(), "SLA violation") {
		t.Errorf("expected message text, got %q", n.lastMessage())
	}
}

func TestNotificationEngine_ImmediateHigh(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{
		Priority: PriorityHigh,
		Text:     "Task failed",
	})

	if n.messageCount() != 1 {
		t.Fatalf("expected 1 immediate message, got %d", n.messageCount())
	}
}

func TestNotificationEngine_BufferNormal(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"}, // long interval to avoid auto-flush
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{
		Priority: PriorityNormal,
		Text:     "Job completed successfully",
	})

	// Normal priority should be buffered, not sent immediately.
	if n.messageCount() != 0 {
		t.Errorf("expected 0 immediate messages for normal priority, got %d", n.messageCount())
	}
	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered message, got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_BufferLow(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{
		Priority: PriorityLow,
		Text:     "Debug info",
	})

	if n.messageCount() != 0 {
		t.Errorf("expected 0 immediate messages for low priority, got %d", n.messageCount())
	}
	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered message, got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_Dedup(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)

	// Send same event+role twice.
	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "task.complete",
		Role:      "琉璃",
		Text:      "First",
	})
	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "task.complete",
		Role:      "琉璃",
		Text:      "Second (should be deduped)",
	})

	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered (deduped), got %d", ne.BufferedCount())
	}

	// Different role should not be deduped.
	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "task.complete",
		Role:      "黒曜",
		Text:      "Different role",
	})
	if ne.BufferedCount() != 2 {
		t.Errorf("expected 2 buffered, got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_DedupDifferentEvent(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)

	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "task.complete",
		Role:      "琉璃",
		Text:      "Task done",
	})
	ne.Notify(NotifyMessage{
		Priority:  PriorityNormal,
		EventType: "job.complete",
		Role:      "琉璃",
		Text:      "Job done",
	})

	// Different event types should not dedup.
	if ne.BufferedCount() != 2 {
		t.Errorf("expected 2 buffered (different events), got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_FlushBatch(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.Notify(NotifyMessage{Priority: PriorityNormal, Text: "Msg 1"})
	ne.Notify(NotifyMessage{Priority: PriorityLow, EventType: "low1", Text: "Msg 2"})

	// Manually flush.
	ne.flushBatch()

	if n.messageCount() != 1 {
		t.Fatalf("expected 1 batch message, got %d", n.messageCount())
	}
	msg := n.lastMessage()
	if !strings.Contains(msg, "Digest") {
		t.Errorf("expected digest format, got %q", msg)
	}
	if !strings.Contains(msg, "2 notifications") {
		t.Errorf("expected '2 notifications' in digest, got %q", msg)
	}
	if ne.BufferedCount() != 0 {
		t.Errorf("expected 0 buffered after flush, got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_FlushBatchEmpty(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	// Flush with no buffered messages.
	ne.flushBatch()
	if n.messageCount() != 0 {
		t.Errorf("expected no messages for empty flush, got %d", n.messageCount())
	}
}

func TestNotificationEngine_PerChannelFilter(t *testing.T) {
	nAll := &mockIntelNotifier{name: "all"}
	nHigh := &mockIntelNotifier{name: "high-only"}

	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack", MinPriority: ""},     // accept all
			{Type: "discord", MinPriority: "high"}, // only high+critical
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{nAll, nHigh}, nil)

	// Send a high-priority message.
	ne.Notify(NotifyMessage{Priority: PriorityHigh, Text: "Important"})

	if nAll.messageCount() != 1 {
		t.Errorf("all-channel: expected 1, got %d", nAll.messageCount())
	}
	if nHigh.messageCount() != 1 {
		t.Errorf("high-channel: expected 1, got %d", nHigh.messageCount())
	}

	// Send a critical message.
	ne.Notify(NotifyMessage{Priority: PriorityCritical, Text: "Urgent"})
	if nAll.messageCount() != 2 {
		t.Errorf("all-channel: expected 2, got %d", nAll.messageCount())
	}
	if nHigh.messageCount() != 2 {
		t.Errorf("high-channel: expected 2, got %d", nHigh.messageCount())
	}
}

func TestNotificationEngine_PerChannelFilter_BatchFlush(t *testing.T) {
	nAll := &mockIntelNotifier{name: "all"}
	nHigh := &mockIntelNotifier{name: "high-only"}

	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack", MinPriority: ""},
			{Type: "discord", MinPriority: "high"},
		},
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, []Notifier{nAll, nHigh}, nil)

	// Buffer a normal message.
	ne.Notify(NotifyMessage{Priority: PriorityNormal, Text: "Routine"})
	ne.flushBatch()

	// All-channel should get the batch, high-only should not.
	if nAll.messageCount() != 1 {
		t.Errorf("all-channel: expected 1 batch message, got %d", nAll.messageCount())
	}
	if nHigh.messageCount() != 0 {
		t.Errorf("high-channel: expected 0 (filtered), got %d", nHigh.messageCount())
	}
}

func TestNotificationEngine_FallbackFn(t *testing.T) {
	var received []string
	fallback := func(text string) {
		received = append(received, text)
	}

	cfg := &Config{}
	ne := NewNotificationEngine(cfg, nil, fallback)

	ne.Notify(NotifyMessage{Priority: PriorityCritical, Text: "Alert!"})

	if len(received) != 1 {
		t.Fatalf("expected 1 fallback call, got %d", len(received))
	}
	if !strings.Contains(received[0], "Alert!") {
		t.Errorf("fallback message missing text, got %q", received[0])
	}
}

func TestNotificationEngine_FallbackOnFlush(t *testing.T) {
	var received []string
	fallback := func(text string) {
		received = append(received, text)
	}

	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, fallback)

	ne.Notify(NotifyMessage{Priority: PriorityNormal, Text: "Buffered"})
	ne.flushBatch()

	if len(received) != 1 {
		t.Fatalf("expected 1 fallback call on flush, got %d", len(received))
	}
	if !strings.Contains(received[0], "Digest") {
		t.Errorf("expected digest format in fallback, got %q", received[0])
	}
}

func TestNotificationEngine_DefaultPriority(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)

	// Empty priority should default to normal (buffered).
	ne.Notify(NotifyMessage{Text: "No priority set"})
	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered (default normal), got %d", ne.BufferedCount())
	}
}

func TestNotificationEngine_NotifyText(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)

	ne.NotifyText(PriorityCritical, "test.event", "琉璃", "Critical event")

	if n.messageCount() != 1 {
		t.Fatalf("expected 1 message, got %d", n.messageCount())
	}
}

func TestNotificationEngine_BatchInterval(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "30s"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)
	if ne.batchInterval != 30*time.Second {
		t.Errorf("expected 30s batch interval, got %v", ne.batchInterval)
	}
}

func TestNotificationEngine_BatchIntervalDefault(t *testing.T) {
	cfg := &Config{}
	ne := NewNotificationEngine(cfg, nil, nil)
	if ne.batchInterval != 5*time.Minute {
		t.Errorf("expected 5m default batch interval, got %v", ne.batchInterval)
	}
}

func TestNotificationEngine_BatchIntervalInvalid(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "invalid"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)
	if ne.batchInterval != 5*time.Minute {
		t.Errorf("expected 5m fallback for invalid interval, got %v", ne.batchInterval)
	}
}

func TestNotificationEngine_StopFlushes(t *testing.T) {
	var received []string
	fallback := func(text string) {
		received = append(received, text)
	}

	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, fallback)
	ne.Start()

	ne.Notify(NotifyMessage{Priority: PriorityNormal, Text: "Pending"})
	ne.Stop()

	// Give goroutine time to flush on stop.
	time.Sleep(50 * time.Millisecond)

	if len(received) != 1 {
		t.Errorf("expected 1 message flushed on stop, got %d", len(received))
	}
}

// --- Format Tests ---

func TestFormatNotifyMessage(t *testing.T) {
	tests := []struct {
		msg      NotifyMessage
		contains string
	}{
		{NotifyMessage{Priority: PriorityCritical, Text: "test"}, "[CRITICAL]"},
		{NotifyMessage{Priority: PriorityHigh, Text: "test"}, "[HIGH]"},
		{NotifyMessage{Priority: PriorityNormal, Text: "test"}, "[INFO]"},
		{NotifyMessage{Priority: PriorityLow, Text: "test"}, "[LOW]"},
	}
	for _, tt := range tests {
		result := formatNotifyMessage(tt.msg)
		if !strings.Contains(result, tt.contains) {
			t.Errorf("formatNotifyMessage(%q) = %q, want contains %q", tt.msg.Priority, result, tt.contains)
		}
		if !strings.Contains(result, "test") {
			t.Errorf("formatNotifyMessage missing text content")
		}
	}
}

func TestFormatBatchDigest(t *testing.T) {
	messages := []NotifyMessage{
		{Priority: PriorityNormal, Text: "Job A completed"},
		{Priority: PriorityLow, Text: "Debug log entry"},
		{Priority: PriorityNormal, Text: "Job B completed"},
	}

	digest := formatBatchDigest(messages)

	if !strings.Contains(digest, "3 notifications") {
		t.Errorf("digest missing count, got %q", digest)
	}
	if !strings.Contains(digest, "Job A completed") {
		t.Errorf("digest missing message A")
	}
	if !strings.Contains(digest, "Debug log entry") {
		t.Errorf("digest missing low-priority message")
	}
}

func TestFormatBatchDigest_Empty(t *testing.T) {
	if got := formatBatchDigest(nil); got != "" {
		t.Errorf("expected empty string for nil messages, got %q", got)
	}
}

func TestFormatBatchDigest_LongMessage(t *testing.T) {
	long := strings.Repeat("x", 300)
	messages := []NotifyMessage{
		{Priority: PriorityNormal, Text: long},
	}
	digest := formatBatchDigest(messages)
	if len(digest) > 300 {
		// Should be truncated.
		if !strings.Contains(digest, "...") {
			t.Error("expected truncation indicator")
		}
	}
}

// --- Infer Priority Tests ---

func TestInferPriority(t *testing.T) {
	tests := []struct {
		text     string
		expected string
	}{
		{"Budget CRITICAL: exceeded limit", PriorityCritical},
		{"Security alert: brute force detected", PriorityCritical},
		{"SLA violation on role 琉璃", PriorityCritical},
		{"Kill switch activated", PriorityCritical},
		{"IP blocked by security monitor", PriorityCritical},
		{"Budget Warning: 80% used", PriorityHigh},
		{"Task failed: timeout", PriorityHigh},
		{"Job auto-disabled after errors", PriorityHigh},
		{"Approve job execution", PriorityHigh},
		{"Offline queue: item expired", PriorityLow},
		{"Debug mode enabled", PriorityLow},
		{"Regular task completed", PriorityHigh}, // default
	}

	for _, tt := range tests {
		got := inferPriority(tt.text, PriorityHigh)
		if got != tt.expected {
			t.Errorf("inferPriority(%q) = %q, want %q", tt.text, got, tt.expected)
		}
	}
}

func TestInferEventType(t *testing.T) {
	tests := []struct {
		text     string
		expected string
	}{
		{"Budget warning: 80%", "budget"},
		{"SLA violation", "sla"},
		{"Security alert", "security"},
		{"Cron job completed", "cron"},
		{"Queue item expired", "queue"},
		{"Trust level changed", "trust"},
		{"Something else", "general"},
	}

	for _, tt := range tests {
		got := inferEventType(tt.text)
		if got != tt.expected {
			t.Errorf("inferEventType(%q) = %q, want %q", tt.text, got, tt.expected)
		}
	}
}

// --- Wrap NotifyFn Tests ---

func TestWrapNotifyFn_Nil(t *testing.T) {
	fn := wrapNotifyFn(nil, PriorityHigh)
	if fn != nil {
		t.Error("expected nil for nil engine")
	}
}

func TestWrapNotifyFn_Routes(t *testing.T) {
	n := &mockIntelNotifier{name: "test"}
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack"},
		},
	}
	ne := NewNotificationEngine(cfg, []Notifier{n}, nil)
	fn := wrapNotifyFn(ne, PriorityHigh)

	// Critical text should be delivered immediately.
	fn("Security alert: IP blocked")
	if n.messageCount() != 1 {
		t.Errorf("expected 1 immediate message for critical text, got %d", n.messageCount())
	}
}

func TestWrapNotifyFn_DefaultPriority(t *testing.T) {
	cfg := &Config{
		NotifyIntel: NotifyIntelConfig{BatchInterval: "1h"},
	}
	ne := NewNotificationEngine(cfg, nil, nil)
	fn := wrapNotifyFn(ne, PriorityNormal)

	// Non-matching text should use default priority (normal = buffered).
	fn("Some routine message")
	if ne.BufferedCount() != 1 {
		t.Errorf("expected 1 buffered, got %d", ne.BufferedCount())
	}
}
