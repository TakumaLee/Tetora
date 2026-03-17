package main

// --- P19.5: Unified Presence/Typing Indicators ---

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"tetora/internal/log"
	"tetora/internal/messaging"
)

// Ensure root PresenceSetter is compatible with messaging.PresenceSetter.
var _ messaging.PresenceSetter = (PresenceSetter)(nil)

// PresenceState represents the current activity state of the bot in a channel.
type PresenceState int

const (
	PresenceIdle       PresenceState = iota
	PresenceThinking                         // processing user request
	PresenceToolUse                          // executing a tool call
	PresenceResponding                       // generating response
)

// presenceTickInterval is how often the typing indicator is refreshed.
// Most chat APIs expire typing after 5 seconds, so we refresh every 4s.
const presenceTickInterval = 4 * time.Second

// PresenceSetter is implemented by channel bots that support typing indicators.
type PresenceSetter interface {
	// SetTyping sends a typing indicator to the specified channel reference.
	// channelRef is the channel-specific identifier (chat ID, channel ID, etc.).
	SetTyping(ctx context.Context, channelRef string) error
	// PresenceName returns the channel name (e.g., "telegram", "slack").
	PresenceName() string
}

// presenceManager coordinates typing indicators across all channel bots.
type presenceManager struct {
	mu      sync.RWMutex
	setters map[string]PresenceSetter        // keyed by channel name
	active  map[string]context.CancelFunc    // active typing loops keyed by "channel:ref"
}

// globalPresence is the package-level presence manager, initialized in daemon mode.
var globalPresence *presenceManager

// newPresenceManager creates a new presenceManager.
func newPresenceManager() *presenceManager {
	return &presenceManager{
		setters: make(map[string]PresenceSetter),
		active:  make(map[string]context.CancelFunc),
	}
}

// RegisterSetter registers a channel bot as a presence setter.
func (pm *presenceManager) RegisterSetter(name string, setter PresenceSetter) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.setters[name] = setter
	log.Debug("presence: registered setter", "channel", name)
}

// StartTyping starts a typing indicator loop for the given task source.
// The loop repeats every presenceTickInterval until StopTyping is called
// or the context is cancelled.
func (pm *presenceManager) StartTyping(ctx context.Context, source string) {
	if source == "" {
		return
	}

	channel, ref := parseSourceChannel(source)
	if channel == "" || ref == "" {
		return
	}

	pm.mu.RLock()
	setter, ok := pm.setters[channel]
	pm.mu.RUnlock()
	if !ok {
		return // no setter registered for this channel
	}

	key := channel + ":" + ref

	// Cancel any existing typing loop for this key.
	pm.mu.Lock()
	if cancel, exists := pm.active[key]; exists {
		cancel()
	}
	loopCtx, loopCancel := context.WithCancel(ctx)
	pm.active[key] = loopCancel
	pm.mu.Unlock()

	// Start typing loop in background.
	go pm.typingLoop(loopCtx, setter, ref, key)
}

// StopTyping cancels the typing indicator loop for the given task source.
func (pm *presenceManager) StopTyping(source string) {
	if source == "" {
		return
	}

	channel, ref := parseSourceChannel(source)
	if channel == "" || ref == "" {
		return
	}

	key := channel + ":" + ref

	pm.mu.Lock()
	if cancel, exists := pm.active[key]; exists {
		cancel()
		delete(pm.active, key)
	}
	pm.mu.Unlock()
}

// typingLoop repeatedly sends typing indicators until the context is cancelled.
func (pm *presenceManager) typingLoop(ctx context.Context, setter PresenceSetter, ref, key string) {
	// Send the first typing indicator immediately.
	if err := setter.SetTyping(ctx, ref); err != nil {
		log.Debug("presence: typing error", "channel", setter.PresenceName(), "ref", ref, "error", err)
	}

	ticker := time.NewTicker(presenceTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Clean up the active entry if it still references us.
			pm.mu.Lock()
			if _, exists := pm.active[key]; exists {
				delete(pm.active, key)
			}
			pm.mu.Unlock()
			return
		case <-ticker.C:
			if err := setter.SetTyping(ctx, ref); err != nil {
				log.Debug("presence: typing error", "channel", setter.PresenceName(), "ref", ref, "error", err)
			}
		}
	}
}

// parseSourceChannel extracts the channel name and channel reference from a task source.
//
// Source formats:
//   - "telegram"          -> ("telegram", "") — no ref, won't start typing
//   - "telegram:12345"    -> ("telegram", "12345")
//   - "slack:C123"        -> ("slack", "C123")
//   - "discord:456"       -> ("discord", "456")
//   - "chat:telegram:789" -> ("telegram", "789")
//   - "route:slack:C123"  -> ("slack", "C123")
//   - "whatsapp:123"      -> ("whatsapp", "123")
func parseSourceChannel(source string) (channel, ref string) {
	if source == "" {
		return "", ""
	}

	parts := strings.Split(source, ":")

	switch len(parts) {
	case 1:
		// "telegram" — channel only, no ref
		return parts[0], ""
	case 2:
		// "telegram:12345" or "slack:C123"
		return parts[0], parts[1]
	default:
		// "chat:telegram:789" or "route:slack:C123" — skip prefix
		// The channel name is parts[1], ref is everything after
		return parts[1], strings.Join(parts[2:], ":")
	}
}

// --- PresenceSetter Implementations ---

// Telegram and Slack PresenceSetter implementations are in their
// respective internal/messaging/ packages.

// Discord Bot — POST /channels/{channelRef}/typing
func (db *DiscordBot) SetTyping(ctx context.Context, channelRef string) error {
	if channelRef == "" {
		return nil
	}
	url := discordAPIBase + fmt.Sprintf("/channels/%s/typing", channelRef)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+db.cfg.Discord.BotToken)
	resp, err := db.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (db *DiscordBot) PresenceName() string { return "discord" }

// LINE, Teams, iMessage PresenceSetter implementations are in their
// respective internal/messaging/ packages.
