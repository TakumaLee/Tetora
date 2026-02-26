package main

import (
	"strings"
	"sync"
	"time"
)

// --- Group Chat Types ---

type GroupMessage struct {
	Platform  string
	GroupID   string
	SenderID  string
	Text      string
	Timestamp time.Time
}

type GroupChatStatus struct {
	ActiveGroups  int            `json:"activeGroups"`
	TotalMessages int            `json:"totalMessages"`
	RateLimits    map[string]int `json:"rateLimits"` // groupID → remaining
}

// GroupChatEngine manages group chat state across platforms.
type GroupChatEngine struct {
	cfg *Config

	mu            sync.RWMutex
	messages      map[string][]GroupMessage // platform:groupID → messages (ring buffer)
	rateLimitData map[string][]time.Time    // platform:groupID or "global" → timestamps (sliding window)
}

// --- Group Chat Engine ---

func newGroupChatEngine(cfg *Config) *GroupChatEngine {
	if cfg == nil {
		return nil
	}

	// Apply defaults if not set.
	if cfg.GroupChat.ContextWindow <= 0 {
		cfg.GroupChat.ContextWindow = 10
	}
	if cfg.GroupChat.RateLimit.MaxPerMin <= 0 {
		cfg.GroupChat.RateLimit.MaxPerMin = 5
	}
	if cfg.GroupChat.Activation == "" {
		cfg.GroupChat.Activation = "mention"
	}

	// Default mention names = agent names.
	if len(cfg.GroupChat.MentionNames) == 0 {
		for name := range cfg.Agents {
			cfg.GroupChat.MentionNames = append(cfg.GroupChat.MentionNames, name)
		}
		// Also add "tetora" and "テトラ" as defaults.
		cfg.GroupChat.MentionNames = append(cfg.GroupChat.MentionNames, "tetora", "テトラ")
	}

	return &GroupChatEngine{
		cfg:           cfg,
		messages:      make(map[string][]GroupMessage),
		rateLimitData: make(map[string][]time.Time),
	}
}

// ShouldRespond decides whether to respond based on activation mode.
func (e *GroupChatEngine) ShouldRespond(platform, groupID, senderID, messageText string) bool {
	if e == nil || e.cfg == nil {
		return false
	}

	// Check if group is allowed (if whitelist configured).
	if !e.IsAllowedGroup(platform, groupID) {
		return false
	}

	// Check rate limit.
	if !e.CheckRateLimit(platform, groupID) {
		return false
	}

	// Check activation mode.
	mode := e.cfg.GroupChat.Activation
	switch mode {
	case "all":
		return true
	case "keyword":
		return e.matchesKeyword(messageText)
	case "mention":
		return e.matchesMention(messageText)
	default:
		// Unknown mode → default to mention.
		return e.matchesMention(messageText)
	}
}

// matchesMention checks if message contains any of the configured mention names (case insensitive).
func (e *GroupChatEngine) matchesMention(messageText string) bool {
	lowerText := strings.ToLower(messageText)
	for _, name := range e.cfg.GroupChat.MentionNames {
		if strings.Contains(lowerText, strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// matchesKeyword checks if message contains any of the configured keywords (case insensitive).
func (e *GroupChatEngine) matchesKeyword(messageText string) bool {
	lowerText := strings.ToLower(messageText)
	for _, kw := range e.cfg.GroupChat.Keywords {
		if strings.Contains(lowerText, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// CheckRateLimit checks if the rate limit allows a new message.
// Returns true if allowed, false if rate limited.
func (e *GroupChatEngine) CheckRateLimit(platform, groupID string) bool {
	if e == nil {
		return false
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	key := e.rateLimitKey(platform, groupID)
	now := time.Now()
	cutoff := now.Add(-60 * time.Second)

	// Remove timestamps older than 60s (sliding window).
	timestamps := e.rateLimitData[key]
	var valid []time.Time
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	e.rateLimitData[key] = valid

	// Check if under limit.
	maxPerMin := e.cfg.GroupChat.RateLimit.MaxPerMin
	if len(valid) >= maxPerMin {
		return false
	}

	// Record this message timestamp.
	e.rateLimitData[key] = append(valid, now)
	return true
}

// rateLimitKey returns the key for rate limit tracking.
// If PerGroup=true, key is platform:groupID, else "global".
func (e *GroupChatEngine) rateLimitKey(platform, groupID string) string {
	if e.cfg.GroupChat.RateLimit.PerGroup {
		return platform + ":" + groupID
	}
	return "global"
}

// IsAllowedGroup checks if a group is whitelisted (if whitelist configured).
func (e *GroupChatEngine) IsAllowedGroup(platform, groupID string) bool {
	if e == nil || e.cfg == nil {
		return false
	}

	// If no whitelist configured, allow all.
	if len(e.cfg.GroupChat.AllowedGroups) == 0 {
		return true
	}

	// Check if platform has whitelist.
	allowed, ok := e.cfg.GroupChat.AllowedGroups[platform]
	if !ok {
		return false
	}

	// Check if groupID is in the whitelist.
	for _, id := range allowed {
		if id == groupID {
			return true
		}
	}
	return false
}

// RecordMessage records a message for context window.
func (e *GroupChatEngine) RecordMessage(platform, groupID, senderID, messageText string) {
	if e == nil {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	key := platform + ":" + groupID
	msg := GroupMessage{
		Platform:  platform,
		GroupID:   groupID,
		SenderID:  senderID,
		Text:      messageText,
		Timestamp: time.Now(),
	}

	messages := e.messages[key]
	// Ring buffer: keep last N messages.
	maxSize := e.cfg.GroupChat.ContextWindow
	if len(messages) >= maxSize {
		// Shift left (remove oldest).
		messages = messages[1:]
	}
	messages = append(messages, msg)
	e.messages[key] = messages
}

// GetContextMessages returns recent messages for context (up to limit).
func (e *GroupChatEngine) GetContextMessages(platform, groupID string, limit int) []GroupMessage {
	if e == nil {
		return nil
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	key := platform + ":" + groupID
	messages := e.messages[key]

	if limit <= 0 || limit > len(messages) {
		limit = len(messages)
	}

	// Return last N messages.
	start := len(messages) - limit
	if start < 0 {
		start = 0
	}
	return messages[start:]
}

// Status returns the current group chat status for dashboard.
func (e *GroupChatEngine) Status() GroupChatStatus {
	if e == nil {
		return GroupChatStatus{
			ActiveGroups:  0,
			TotalMessages: 0,
			RateLimits:    make(map[string]int),
		}
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	status := GroupChatStatus{
		ActiveGroups:  len(e.messages),
		TotalMessages: 0,
		RateLimits:    make(map[string]int),
	}

	// Count total messages.
	for _, msgs := range e.messages {
		status.TotalMessages += len(msgs)
	}

	// Calculate rate limits remaining.
	now := time.Now()
	cutoff := now.Add(-60 * time.Second)
	maxPerMin := e.cfg.GroupChat.RateLimit.MaxPerMin

	for key, timestamps := range e.rateLimitData {
		count := 0
		for _, ts := range timestamps {
			if ts.After(cutoff) {
				count++
			}
		}
		remaining := maxPerMin - count
		if remaining < 0 {
			remaining = 0
		}
		status.RateLimits[key] = remaining
	}

	return status
}
