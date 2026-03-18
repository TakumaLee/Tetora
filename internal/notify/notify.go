package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
	imessagebot "tetora/internal/messaging/imessage"
	"tetora/internal/messaging/line"
	"tetora/internal/messaging/matrix"
	signalbot "tetora/internal/messaging/signal"
	"tetora/internal/messaging/teams"
	"tetora/internal/messaging/whatsapp"
)

// Notifier sends text notifications to a channel.
type Notifier interface {
	Send(text string) error
	Name() string
}

// SlackNotifier sends via Slack incoming webhook.
type SlackNotifier struct {
	WebhookURL string
	client     *http.Client
}

func (s *SlackNotifier) Send(text string) error {
	payload, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequest("POST", s.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("slack: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("slack: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *SlackNotifier) Name() string { return "slack" }

// DiscordNotifier sends via Discord webhook.
type DiscordNotifier struct {
	WebhookURL string
	client     *http.Client
}

func (d *DiscordNotifier) Send(text string) error {
	// Discord limits content to 2000 chars.
	if len(text) > 2000 {
		text = text[:1997] + "..."
	}
	payload, _ := json.Marshal(map[string]string{"content": text})
	req, err := http.NewRequest("POST", d.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("discord: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (d *DiscordNotifier) Name() string { return "discord" }

// NewDiscordNotifier creates a DiscordNotifier with the given webhook URL and HTTP timeout.
func NewDiscordNotifier(webhookURL string, timeout time.Duration) *DiscordNotifier {
	return &DiscordNotifier{WebhookURL: webhookURL, client: &http.Client{Timeout: timeout}}
}

// MultiNotifier fans out to multiple notifiers. Failures are logged, not fatal.
type MultiNotifier struct {
	Notifiers []Notifier
}

func (m *MultiNotifier) Send(text string) {
	for _, n := range m.Notifiers {
		if err := n.Send(text); err != nil {
			log.Error("notification send failed", "channel", n.Name(), "error", err)
		}
	}
}

// WhatsAppNotifier is an alias for the internal whatsapp.Notifier.
type WhatsAppNotifier = whatsapp.Notifier

// BuildDiscordNotifierByName returns a DiscordNotifier for the named channel (from cfg.Notifications), or nil.
func BuildDiscordNotifierByName(cfg *config.Config, name string) *DiscordNotifier {
	client := &http.Client{Timeout: 5 * time.Second}
	for _, ch := range cfg.Notifications {
		if ch.Type == "discord" && ch.Name == name && ch.WebhookURL != "" {
			return &DiscordNotifier{WebhookURL: ch.WebhookURL, client: client}
		}
	}
	return nil
}

// BuildNotifiers creates Notifier instances from config.
func BuildNotifiers(cfg *config.Config) []Notifier {
	var notifiers []Notifier
	client := &http.Client{Timeout: 5 * time.Second}
	for _, ch := range cfg.Notifications {
		switch ch.Type {
		case "slack":
			if ch.WebhookURL != "" {
				notifiers = append(notifiers, &SlackNotifier{WebhookURL: ch.WebhookURL, client: client})
			}
		case "discord":
			if ch.WebhookURL != "" {
				notifiers = append(notifiers, &DiscordNotifier{WebhookURL: ch.WebhookURL, client: client})
			}
		case "whatsapp":
			// For WhatsApp, WebhookURL should contain the recipient phone number
			if ch.WebhookURL != "" && cfg.WhatsApp.Enabled {
				notifiers = append(notifiers, &whatsapp.Notifier{
					Cfg:       cfg.WhatsApp,
					Recipient: ch.WebhookURL, // use webhookUrl field for phone number
				})
			}
		case "line": // --- P15.1: LINE Channel ---
			// For LINE, WebhookURL should contain the target user/group ID
			if ch.WebhookURL != "" && cfg.LINE.Enabled {
				notifiers = append(notifiers, &line.Notifier{
					Config: cfg.LINE,
					ChatID: ch.WebhookURL, // use webhookUrl field for LINE user/group ID
				})
			}
		case "matrix": // --- P15.2: Matrix Channel ---
			// For Matrix, WebhookURL should contain the target room ID
			if ch.WebhookURL != "" && cfg.Matrix.Enabled {
				notifiers = append(notifiers, &matrix.MatrixNotifier{
					Config: cfg.Matrix,
					RoomID: ch.WebhookURL, // use webhookUrl field for Matrix room ID
				})
			}
		case "teams": // --- P15.3: Teams Channel ---
			// For Teams, WebhookURL is used as "serviceUrl|conversationId" format
			if ch.WebhookURL != "" && cfg.Teams.Enabled {
				parts := strings.SplitN(ch.WebhookURL, "|", 2)
				if len(parts) == 2 {
					teamsBot := teams.NewBot(cfg.Teams, nil) // nil runtime OK — only used for proactive send
					notifiers = append(notifiers, &teams.Notifier{
						Bot:            teamsBot,
						ServiceURL:     parts[0],
						ConversationID: parts[1],
					})
				}
			}
		case "signal": // --- P15.4: Signal Channel ---
			// For Signal, WebhookURL format: "phoneNumber" or "group:groupId"
			if ch.WebhookURL != "" && cfg.Signal.Enabled {
				isGroup := strings.HasPrefix(ch.WebhookURL, "group:")
				recipient := ch.WebhookURL
				if isGroup {
					recipient = strings.TrimPrefix(recipient, "group:")
				}
				notifiers = append(notifiers, &signalbot.Notifier{
					Config:    cfg.Signal,
					Recipient: recipient,
					IsGroup:   isGroup,
				})
			}
		case "gchat", "googlechat": // --- P15.5: Google Chat Channel ---
			// For Google Chat, WebhookURL should contain the space name (spaces/{space_id})
			if ch.WebhookURL != "" && cfg.GoogleChat.Enabled {
				// Note: GoogleChatNotifier requires a bot instance which is created in main.go
				// This is a placeholder - actual initialization happens in main.go
				log.Warn("gchat notifier requires bot initialization in main.go", "space", ch.WebhookURL)
			}
		case "imessage": // --- P20.2: iMessage via BlueBubbles ---
			// For iMessage, WebhookURL field holds the target chat GUID.
			if ch.WebhookURL != "" && cfg.IMessage.Enabled {
				notifiers = append(notifiers, &imessagebot.Notifier{
					Config:   cfg.IMessage,
					ChatGUID: ch.WebhookURL,
				})
			}
		default:
			log.Warn("unknown notification type", "type", ch.Type)
		}
	}
	return notifiers
}

// --- Priority Levels ---

const (
	PriorityCritical = "critical" // SLA violation, security alert, budget exceeded
	PriorityHigh     = "high"     // task complete, approval needed
	PriorityNormal   = "normal"   // job success, routine report
	PriorityLow      = "low"      // info, debug
)

// PriorityRank returns numeric rank for sorting (higher = more important).
func PriorityRank(p string) int {
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

// PriorityFromRank converts numeric rank back to priority string.
func PriorityFromRank(rank int) string {
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

// IsValidPriority checks if a priority string is valid.
func IsValidPriority(p string) bool {
	return p == PriorityCritical || p == PriorityHigh || p == PriorityNormal || p == PriorityLow
}

// --- Notification Message ---

// Message represents a prioritized notification.
type Message struct {
	Priority  string    // "critical", "high", "normal", "low"
	EventType string    // e.g. "task.complete", "sla.violation", "budget.warning"
	Agent     string    // agent name (for dedup)
	Text      string    // notification text
	Timestamp time.Time // when the event occurred
}

// DedupKey returns a key for deduplication within a batch window.
func (m Message) DedupKey() string {
	return m.EventType + ":" + m.Agent
}

// --- Notification Engine ---

// Engine manages prioritized notification delivery with batching and dedup.
type Engine struct {
	mu            sync.Mutex
	channels      []notifyChannel
	batchInterval time.Duration
	buffer        []Message
	dedupSeen     map[string]time.Time // dedupKey -> last seen timestamp
	stopCh        chan struct{}
	stopped       bool
	fallbackFn    func(string) // fallback for backward compat (e.g. Telegram bot)
}

// BatchInterval returns the configured batch interval (for logging/monitoring).
func (ne *Engine) BatchInterval() time.Duration {
	return ne.batchInterval
}

// notifyChannel wraps a Notifier with per-channel priority filtering.
type notifyChannel struct {
	notifier    Notifier
	minPriority int // minimum priority rank to accept
}

// NewEngine creates a new notification engine.
func NewEngine(cfg *config.Config, notifiers []Notifier, fallbackFn func(string)) *Engine {
	ne := &Engine{
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
			minRank = PriorityRank(ch.MinPriority)
		}
		ne.channels = append(ne.channels, notifyChannel{
			notifier:    notifiers[i],
			minPriority: minRank,
		})
	}

	return ne
}

// Start begins the batch flush ticker.
func (ne *Engine) Start() {
	go ne.batchLoop()
}

// Stop signals the batch loop to stop and flushes remaining messages.
func (ne *Engine) Stop() {
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
func (ne *Engine) Notify(msg Message) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	if msg.Priority == "" {
		msg.Priority = PriorityNormal
	}

	rank := PriorityRank(msg.Priority)

	// Critical/High: deliver immediately to eligible channels.
	if rank >= PriorityRank(PriorityHigh) {
		ne.deliverImmediate(msg)
		return
	}

	// Normal/Low: buffer for batch delivery with dedup.
	ne.mu.Lock()
	defer ne.mu.Unlock()

	// Dedup check: same event_type+agent within batch window.
	key := msg.DedupKey()
	if lastSeen, exists := ne.dedupSeen[key]; exists {
		if time.Since(lastSeen) < ne.batchInterval {
			log.Debug("notification deduped", "key", key, "priority", msg.Priority)
			return
		}
	}
	ne.dedupSeen[key] = msg.Timestamp
	ne.buffer = append(ne.buffer, msg)
}

// NotifyText is a convenience method for backward compatibility.
// Sends a notification with the given priority and text.
func (ne *Engine) NotifyText(priority, eventType, role, text string) {
	ne.Notify(Message{
		Priority:  priority,
		EventType: eventType,
		Agent:     role,
		Text:      text,
		Timestamp: time.Now(),
	})
}

// deliverImmediate sends a message to all eligible channels right away.
func (ne *Engine) deliverImmediate(msg Message) {
	text := formatMessage(msg)

	// Send to fallback (Telegram bot).
	if ne.fallbackFn != nil {
		ne.fallbackFn(text)
	}

	// Send to configured channels.
	rank := PriorityRank(msg.Priority)
	for _, ch := range ne.channels {
		if rank >= ch.minPriority {
			if err := ch.notifier.Send(text); err != nil {
				log.Error("notification send failed", "channel", ch.notifier.Name(),
					"priority", msg.Priority, "error", err)
			}
		}
	}
}

// batchLoop runs the periodic batch flush.
func (ne *Engine) batchLoop() {
	ticker := time.NewTicker(ne.batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ne.FlushBatch()
		case <-ne.stopCh:
			ne.FlushBatch() // final flush
			return
		}
	}
}

// FlushBatch sends all buffered messages as a digest.
func (ne *Engine) FlushBatch() {
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
		if PriorityRank(PriorityNormal) >= ch.minPriority {
			if err := ch.notifier.Send(digest); err != nil {
				log.Error("batch notification send failed", "channel", ch.notifier.Name(), "error", err)
			}
		}
	}

	log.Info("notification batch flushed", "count", len(batch))
}

// cleanupDedup removes dedup entries older than 2x batch interval.
func (ne *Engine) cleanupDedup() {
	cutoff := time.Now().Add(-2 * ne.batchInterval)
	for key, ts := range ne.dedupSeen {
		if ts.Before(cutoff) {
			delete(ne.dedupSeen, key)
		}
	}
}

// BufferedCount returns the number of buffered messages (for testing/monitoring).
func (ne *Engine) BufferedCount() int {
	ne.mu.Lock()
	defer ne.mu.Unlock()
	return len(ne.buffer)
}

// --- Formatting ---

// formatMessage formats a single notification message with priority prefix.
func formatMessage(msg Message) string {
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
func formatBatchDigest(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Tetora Digest (%d notifications)\n\n", len(messages)))

	// Group by priority.
	groups := map[string][]Message{}
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

// WrapNotifyFn creates a backward-compatible notifyFn that routes through the engine.
// This allows existing callers (cron, security, etc.) to continue using notifyFn(string)
// while getting priority routing.
func WrapNotifyFn(ne *Engine, defaultPriority string) func(string) {
	if ne == nil {
		return nil
	}
	return func(text string) {
		// Infer priority from text content.
		priority := InferPriority(text, defaultPriority)
		eventType := InferEventType(text)
		ne.NotifyText(priority, eventType, "", text)
	}
}

// InferPriority guesses the priority from the notification text.
func InferPriority(text, defaultPriority string) string {
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

// InferEventType guesses the event type from the notification text.
func InferEventType(text string) string {
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
