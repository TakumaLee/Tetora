package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// --- Priority Levels ---

const (
	PriorityCritical = "critical" // SLA violation, security alert, budget exceeded
	PriorityHigh     = "high"     // task complete, approval needed
	PriorityNormal   = "normal"   // job success, routine report
	PriorityLow      = "low"      // info, debug
)

// priorityRank returns numeric rank for sorting (higher = more important).
func priorityRank(p string) int {
	switch p {
	case PriorityCritical:
		return 4
	case PriorityHigh:
		return 3
	case PriorityNormal:
		return 2
	case PriorityLow:
		return 1
	default:
		return 2 // default to normal
	}
}

// priorityFromRank converts numeric rank back to priority string.
func priorityFromRank(rank int) string {
	switch rank {
	case 4:
		return PriorityCritical
	case 3:
		return PriorityHigh
	case 2:
		return PriorityNormal
	case 1:
		return PriorityLow
	default:
		return PriorityNormal
	}
}

// isValidPriority checks if a priority string is valid.
func isValidPriority(p string) bool {
	return p == PriorityCritical || p == PriorityHigh || p == PriorityNormal || p == PriorityLow
}

// --- Notification Message ---

// NotifyMessage represents a prioritized notification.
type NotifyMessage struct {
	Priority  string    // "critical", "high", "normal", "low"
	EventType string    // e.g. "task.complete", "sla.violation", "budget.warning"
	Role      string    // agent role (for dedup)
	Text      string    // notification text
	Timestamp time.Time // when the event occurred
}

// dedupKey returns a key for deduplication within a batch window.
func (m NotifyMessage) dedupKey() string {
	return m.EventType + ":" + m.Role
}

// --- Notification Engine ---

// NotificationEngine manages prioritized notification delivery with batching and dedup.
type NotificationEngine struct {
	mu            sync.Mutex
	channels      []notifyChannel
	batchInterval time.Duration
	buffer        []NotifyMessage
	dedupSeen     map[string]time.Time // dedupKey -> last seen timestamp
	stopCh        chan struct{}
	stopped       bool
	fallbackFn    func(string) // fallback for backward compat (e.g. Telegram bot)
}

// notifyChannel wraps a Notifier with per-channel priority filtering.
type notifyChannel struct {
	notifier    Notifier
	minPriority int // minimum priority rank to accept
}

// NotifyIntelConfig holds notification intelligence configuration.
type NotifyIntelConfig struct {
	BatchInterval string `json:"notifyBatch,omitempty"` // e.g. "5m", default "5m"
}

// NewNotificationEngine creates a new notification engine.
func NewNotificationEngine(cfg *Config, notifiers []Notifier, fallbackFn func(string)) *NotificationEngine {
	ne := &NotificationEngine{
		dedupSeen:  make(map[string]time.Time),
		stopCh:     make(chan struct{}),
		fallbackFn: fallbackFn,
	}

	// Parse batch interval.
	ne.batchInterval = 5 * time.Minute // default
	if cfg.NotifyIntel.BatchInterval != "" {
		if d, err := time.ParseDuration(cfg.NotifyIntel.BatchInterval); err == nil && d > 0 {
			ne.batchInterval = d
		}
	}

	// Build channels with per-channel priority filtering.
	for i, ch := range cfg.Notifications {
		if i >= len(notifiers) {
			break
		}
		minRank := 1 // default: accept all (low and above)
		if ch.MinPriority != "" {
			minRank = priorityRank(ch.MinPriority)
		}
		ne.channels = append(ne.channels, notifyChannel{
			notifier:    notifiers[i],
			minPriority: minRank,
		})
	}

	return ne
}

// Start begins the batch flush ticker.
func (ne *NotificationEngine) Start() {
	go ne.batchLoop()
}

// Stop signals the batch loop to stop and flushes remaining messages.
func (ne *NotificationEngine) Stop() {
	ne.mu.Lock()
	if ne.stopped {
		ne.mu.Unlock()
		return
	}
	ne.stopped = true
	ne.mu.Unlock()
	close(ne.stopCh)
}

// Notify sends a prioritized notification.
// Critical and high priority messages are delivered immediately.
// Normal and low priority messages are buffered for batch delivery.
func (ne *NotificationEngine) Notify(msg NotifyMessage) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	if msg.Priority == "" {
		msg.Priority = PriorityNormal
	}

	rank := priorityRank(msg.Priority)

	// Critical/High: deliver immediately to eligible channels.
	if rank >= priorityRank(PriorityHigh) {
		ne.deliverImmediate(msg)
		return
	}

	// Normal/Low: buffer for batch delivery with dedup.
	ne.mu.Lock()
	defer ne.mu.Unlock()

	// Dedup check: same event_type+role within batch window.
	key := msg.dedupKey()
	if lastSeen, exists := ne.dedupSeen[key]; exists {
		if time.Since(lastSeen) < ne.batchInterval {
			logDebug("notification deduped", "key", key, "priority", msg.Priority)
			return
		}
	}
	ne.dedupSeen[key] = msg.Timestamp
	ne.buffer = append(ne.buffer, msg)
}

// NotifyText is a convenience method for backward compatibility.
// Sends a notification with the given priority and text.
func (ne *NotificationEngine) NotifyText(priority, eventType, role, text string) {
	ne.Notify(NotifyMessage{
		Priority:  priority,
		EventType: eventType,
		Role:      role,
		Text:      text,
		Timestamp: time.Now(),
	})
}

// deliverImmediate sends a message to all eligible channels right away.
func (ne *NotificationEngine) deliverImmediate(msg NotifyMessage) {
	text := formatNotifyMessage(msg)

	// Send to fallback (Telegram bot).
	if ne.fallbackFn != nil {
		ne.fallbackFn(text)
	}

	// Send to configured channels.
	rank := priorityRank(msg.Priority)
	for _, ch := range ne.channels {
		if rank >= ch.minPriority {
			if err := ch.notifier.Send(text); err != nil {
				logError("notification send failed", "channel", ch.notifier.Name(),
					"priority", msg.Priority, "error", err)
			}
		}
	}
}

// batchLoop runs the periodic batch flush.
func (ne *NotificationEngine) batchLoop() {
	ticker := time.NewTicker(ne.batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ne.flushBatch()
		case <-ne.stopCh:
			ne.flushBatch() // final flush
			return
		}
	}
}

// flushBatch sends all buffered messages as a digest.
func (ne *NotificationEngine) flushBatch() {
	ne.mu.Lock()
	if len(ne.buffer) == 0 {
		// Clean up old dedup entries.
		ne.cleanupDedup()
		ne.mu.Unlock()
		return
	}
	batch := ne.buffer
	ne.buffer = nil
	ne.cleanupDedup()
	ne.mu.Unlock()

	digest := formatBatchDigest(batch)

	// Send to fallback.
	if ne.fallbackFn != nil {
		ne.fallbackFn(digest)
	}

	// Send to channels that accept normal/low priority.
	for _, ch := range ne.channels {
		if priorityRank(PriorityNormal) >= ch.minPriority {
			if err := ch.notifier.Send(digest); err != nil {
				logError("batch notification send failed", "channel", ch.notifier.Name(), "error", err)
			}
		}
	}

	logInfo("notification batch flushed", "count", len(batch))
}

// cleanupDedup removes dedup entries older than 2x batch interval.
func (ne *NotificationEngine) cleanupDedup() {
	cutoff := time.Now().Add(-2 * ne.batchInterval)
	for key, ts := range ne.dedupSeen {
		if ts.Before(cutoff) {
			delete(ne.dedupSeen, key)
		}
	}
}

// BufferedCount returns the number of buffered messages (for testing/monitoring).
func (ne *NotificationEngine) BufferedCount() int {
	ne.mu.Lock()
	defer ne.mu.Unlock()
	return len(ne.buffer)
}

// --- Formatting ---

// formatNotifyMessage formats a single notification message with priority prefix.
func formatNotifyMessage(msg NotifyMessage) string {
	prefix := priorityEmoji(msg.Priority)
	return fmt.Sprintf("%s %s", prefix, msg.Text)
}

// priorityEmoji returns a text indicator for the priority level.
func priorityEmoji(p string) string {
	switch p {
	case PriorityCritical:
		return "[CRITICAL]"
	case PriorityHigh:
		return "[HIGH]"
	case PriorityNormal:
		return "[INFO]"
	case PriorityLow:
		return "[LOW]"
	default:
		return "[INFO]"
	}
}

// formatBatchDigest formats buffered messages into a digest notification.
func formatBatchDigest(messages []NotifyMessage) string {
	if len(messages) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Tetora Digest (%d notifications)\n\n", len(messages)))

	// Group by priority.
	groups := map[string][]NotifyMessage{}
	for _, m := range messages {
		groups[m.Priority] = append(groups[m.Priority], m)
	}

	// Output in priority order.
	for _, p := range []string{PriorityNormal, PriorityLow} {
		msgs, ok := groups[p]
		if !ok {
			continue
		}
		for _, m := range msgs {
			text := m.Text
			if len(text) > 200 {
				text = text[:197] + "..."
			}
			b.WriteString(fmt.Sprintf("%s %s\n", priorityEmoji(p), text))
		}
	}

	return strings.TrimSpace(b.String())
}

// --- Backward Compatibility ---

// wrapNotifyFn creates a backward-compatible notifyFn that routes through the engine.
// This allows existing callers (cron, security, etc.) to continue using notifyFn(string)
// while getting priority routing.
func wrapNotifyFn(ne *NotificationEngine, defaultPriority string) func(string) {
	if ne == nil {
		return nil
	}
	return func(text string) {
		// Infer priority from text content.
		priority := inferPriority(text, defaultPriority)
		eventType := inferEventType(text)
		ne.NotifyText(priority, eventType, "", text)
	}
}

// inferPriority guesses the priority from the notification text.
func inferPriority(text, defaultPriority string) string {
	lower := strings.ToLower(text)

	// Critical indicators.
	if strings.Contains(lower, "critical") ||
		strings.Contains(lower, "kill switch") ||
		strings.Contains(lower, "security alert") ||
		strings.Contains(lower, "blocked") ||
		strings.Contains(lower, "sla violation") {
		return PriorityCritical
	}

	// High indicators.
	if strings.Contains(lower, "budget") ||
		strings.Contains(lower, "warning") ||
		strings.Contains(lower, "failed") ||
		strings.Contains(lower, "error") ||
		strings.Contains(lower, "auto-disabled") ||
		strings.Contains(lower, "approval") ||
		strings.Contains(lower, "approve") {
		return PriorityHigh
	}

	// Low indicators.
	if strings.Contains(lower, "debug") ||
		strings.Contains(lower, "queue") {
		return PriorityLow
	}

	return defaultPriority
}

// inferEventType guesses the event type from the notification text.
func inferEventType(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "budget"):
		return "budget"
	case strings.Contains(lower, "sla"):
		return "sla"
	case strings.Contains(lower, "security"):
		return "security"
	case strings.Contains(lower, "cron") || strings.Contains(lower, "job"):
		return "cron"
	case strings.Contains(lower, "queue"):
		return "queue"
	case strings.Contains(lower, "trust"):
		return "trust"
	default:
		return "general"
	}
}
