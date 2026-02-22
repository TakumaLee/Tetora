package main

import (
	"testing"
	"time"
)

func TestGroupChat_ShouldRespond_Mention(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "mention",
			MentionNames:  []string{"tetora", "テトラ"},
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 5,
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Test mention match (case insensitive).
	if !engine.ShouldRespond("slack", "C123", "U1", "Hey tetora, what's up?") {
		t.Error("Expected mention match for 'tetora'")
	}
	if !engine.ShouldRespond("slack", "C123", "U1", "TETORA help me!") {
		t.Error("Expected mention match for 'TETORA' (case insensitive)")
	}
	if !engine.ShouldRespond("discord", "G456", "U2", "テトラ、おはよう") {
		t.Error("Expected mention match for 'テトラ'")
	}

	// Test no match.
	if engine.ShouldRespond("slack", "C123", "U1", "Just chatting here") {
		t.Error("Expected no match for message without mention")
	}
}

func TestGroupChat_ShouldRespond_Keyword(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "keyword",
			Keywords:      []string{"help", "bug", "error"},
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 5,
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Test keyword match.
	if !engine.ShouldRespond("slack", "C123", "U1", "I need help with this") {
		t.Error("Expected keyword match for 'help'")
	}
	if !engine.ShouldRespond("slack", "C123", "U1", "Found a BUG in the code") {
		t.Error("Expected keyword match for 'BUG' (case insensitive)")
	}
	if !engine.ShouldRespond("discord", "G456", "U2", "Error: file not found") {
		t.Error("Expected keyword match for 'Error'")
	}

	// Test no match.
	if engine.ShouldRespond("slack", "C123", "U1", "Everything is working great") {
		t.Error("Expected no match for message without keywords")
	}
}

func TestGroupChat_ShouldRespond_All(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 5,
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Test all messages should match.
	if !engine.ShouldRespond("slack", "C123", "U1", "Random message") {
		t.Error("Expected match for 'all' mode")
	}
	if !engine.ShouldRespond("discord", "G456", "U2", "Another message") {
		t.Error("Expected match for 'all' mode")
	}
}

func TestGroupChat_RateLimit_PerGroup(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 3,
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Group 1: should allow 3 messages.
	for i := 0; i < 3; i++ {
		if !engine.ShouldRespond("slack", "C123", "U1", "msg") {
			t.Errorf("Expected message %d to be allowed", i+1)
		}
	}

	// Group 1: 4th message should be rate limited.
	if engine.ShouldRespond("slack", "C123", "U1", "msg") {
		t.Error("Expected 4th message to be rate limited")
	}

	// Group 2: should have separate rate limit.
	if !engine.ShouldRespond("slack", "C456", "U1", "msg") {
		t.Error("Expected message in different group to be allowed")
	}
}

func TestGroupChat_RateLimit_Global(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 3,
				PerGroup:  false, // Global rate limit.
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Group 1: 2 messages.
	engine.ShouldRespond("slack", "C123", "U1", "msg1")
	engine.ShouldRespond("slack", "C123", "U1", "msg2")

	// Group 2: 1 message (total 3).
	if !engine.ShouldRespond("slack", "C456", "U2", "msg3") {
		t.Error("Expected 3rd global message to be allowed")
	}

	// Group 2: 4th message should be rate limited (global).
	if engine.ShouldRespond("slack", "C456", "U2", "msg4") {
		t.Error("Expected 4th global message to be rate limited")
	}
}

func TestGroupChat_IsAllowedGroup(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 5,
				PerGroup:  true,
			},
			AllowedGroups: map[string][]string{
				"slack":   {"C123", "C456"},
				"discord": {"G789"},
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Test allowed groups.
	if !engine.IsAllowedGroup("slack", "C123") {
		t.Error("Expected C123 to be allowed")
	}
	if !engine.IsAllowedGroup("slack", "C456") {
		t.Error("Expected C456 to be allowed")
	}
	if !engine.IsAllowedGroup("discord", "G789") {
		t.Error("Expected G789 to be allowed")
	}

	// Test disallowed groups.
	if engine.IsAllowedGroup("slack", "C999") {
		t.Error("Expected C999 to be disallowed")
	}
	if engine.IsAllowedGroup("discord", "G999") {
		t.Error("Expected G999 to be disallowed")
	}
	if engine.IsAllowedGroup("telegram", "T123") {
		t.Error("Expected platform 'telegram' to be disallowed (not in whitelist)")
	}
}

func TestGroupChat_IsAllowedGroup_NoWhitelist(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 5,
				PerGroup:  true,
			},
			// No AllowedGroups configured = allow all.
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Test all groups allowed when no whitelist.
	if !engine.IsAllowedGroup("slack", "C123") {
		t.Error("Expected all groups to be allowed when no whitelist")
	}
	if !engine.IsAllowedGroup("discord", "G999") {
		t.Error("Expected all groups to be allowed when no whitelist")
	}
}

func TestGroupChat_ContextWindow_Record(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 3,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 100,
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Record messages.
	engine.RecordMessage("slack", "C123", "U1", "msg1")
	engine.RecordMessage("slack", "C123", "U2", "msg2")
	engine.RecordMessage("slack", "C123", "U3", "msg3")

	// Get context.
	messages := engine.GetContextMessages("slack", "C123", 10)
	if len(messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(messages))
	}
	if messages[0].Text != "msg1" || messages[1].Text != "msg2" || messages[2].Text != "msg3" {
		t.Error("Expected messages in order")
	}
}

func TestGroupChat_ContextWindow_RingBuffer(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 3, // Ring buffer size 3.
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 100,
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Record 5 messages (exceeds buffer size).
	engine.RecordMessage("slack", "C123", "U1", "msg1")
	engine.RecordMessage("slack", "C123", "U2", "msg2")
	engine.RecordMessage("slack", "C123", "U3", "msg3")
	engine.RecordMessage("slack", "C123", "U4", "msg4")
	engine.RecordMessage("slack", "C123", "U5", "msg5")

	// Get context: should only have last 3.
	messages := engine.GetContextMessages("slack", "C123", 10)
	if len(messages) != 3 {
		t.Errorf("Expected 3 messages (ring buffer), got %d", len(messages))
	}
	if messages[0].Text != "msg3" || messages[1].Text != "msg4" || messages[2].Text != "msg5" {
		t.Error("Expected last 3 messages (msg3, msg4, msg5)")
	}
}

func TestGroupChat_ContextWindow_Limit(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 100,
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Record 5 messages.
	for i := 1; i <= 5; i++ {
		engine.RecordMessage("slack", "C123", "U1", "msg")
	}

	// Get context with limit=2.
	messages := engine.GetContextMessages("slack", "C123", 2)
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages (limit), got %d", len(messages))
	}
}

func TestGroupChat_Status(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 5,
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Record messages in 2 groups.
	engine.RecordMessage("slack", "C123", "U1", "msg1")
	engine.RecordMessage("slack", "C123", "U2", "msg2")
	engine.RecordMessage("discord", "G456", "U3", "msg3")

	// Trigger rate limit tracking.
	engine.ShouldRespond("slack", "C123", "U1", "test")
	engine.ShouldRespond("discord", "G456", "U2", "test")

	// Get status.
	status := engine.Status()

	if status.ActiveGroups != 2 {
		t.Errorf("Expected 2 active groups, got %d", status.ActiveGroups)
	}
	if status.TotalMessages != 3 {
		t.Errorf("Expected 3 total messages, got %d", status.TotalMessages)
	}
	if len(status.RateLimits) == 0 {
		t.Error("Expected rate limit data")
	}
}

func TestGroupChat_Activation_CaseInsensitive(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "mention",
			MentionNames:  []string{"tetora"},
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 100, // High limit to avoid rate limiting in test.
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Test various cases.
	testCases := []string{
		"tetora help",
		"TETORA help",
		"Tetora help",
		"TeToRa help",
		"Hey tetora!",
		"tetora, are you there?",
	}

	for _, text := range testCases {
		if !engine.ShouldRespond("slack", "C123", "U1", text) {
			t.Errorf("Expected case-insensitive match for: %s", text)
		}
	}
}

func TestGroupChat_ConfigDefaults(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			// No explicit settings → should apply defaults.
		},
		Roles: map[string]RoleConfig{
			"琉璃": {},
			"翡翠": {},
		},
	}

	_ = newGroupChatEngine(cfg)

	// Check defaults applied.
	if cfg.GroupChat.ContextWindow != 10 {
		t.Errorf("Expected default ContextWindow=10, got %d", cfg.GroupChat.ContextWindow)
	}
	if cfg.GroupChat.RateLimit.MaxPerMin != 5 {
		t.Errorf("Expected default RateLimit.MaxPerMin=5, got %d", cfg.GroupChat.RateLimit.MaxPerMin)
	}
	if cfg.GroupChat.Activation != "mention" {
		t.Errorf("Expected default Activation=mention, got %s", cfg.GroupChat.Activation)
	}

	// Check default mention names include role names.
	hasRoleName := false
	for _, name := range cfg.GroupChat.MentionNames {
		if name == "琉璃" || name == "翡翠" {
			hasRoleName = true
			break
		}
	}
	if !hasRoleName {
		t.Error("Expected default MentionNames to include role names")
	}
}

func TestGroupChat_RateLimitSlidingWindow(t *testing.T) {
	cfg := &Config{
		GroupChat: GroupChatConfig{
			Activation:    "all",
			ContextWindow: 10,
			RateLimit: GroupChatRateLimitConfig{
				MaxPerMin: 2,
				PerGroup:  true,
			},
		},
		Roles: map[string]RoleConfig{},
	}

	engine := newGroupChatEngine(cfg)

	// Send 2 messages (max).
	engine.ShouldRespond("slack", "C123", "U1", "msg1")
	engine.ShouldRespond("slack", "C123", "U1", "msg2")

	// 3rd message should be rate limited.
	if engine.ShouldRespond("slack", "C123", "U1", "msg3") {
		t.Error("Expected 3rd message to be rate limited")
	}

	// Manually expire old timestamps (simulate 61s passing).
	engine.mu.Lock()
	key := "slack:C123"
	for i := range engine.rateLimitData[key] {
		engine.rateLimitData[key][i] = time.Now().Add(-61 * time.Second)
	}
	engine.mu.Unlock()

	// Now should be allowed again (sliding window expired).
	if !engine.ShouldRespond("slack", "C123", "U1", "msg4") {
		t.Error("Expected message to be allowed after sliding window expiry")
	}
}
