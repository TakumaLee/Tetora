package main

// --- P14.2: Thread-Bound Sessions Tests ---

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- Session Key Derivation ---

func TestThreadSessionKey(t *testing.T) {
	tests := []struct {
		role, guildID, threadID string
		expected                string
	}{
		{"ruri", "G123", "T456", "agent:ruri:discord:thread:G123:T456"},
		{"hisui", "G123", "T789", "agent:hisui:discord:thread:G123:T789"},
		{"kokuyou", "G999", "T012", "agent:kokuyou:discord:thread:G999:T012"},
		{"kohaku", "", "T111", "agent:kohaku:discord:thread::T111"},
	}
	for _, tt := range tests {
		got := threadSessionKey(tt.role, tt.guildID, tt.threadID)
		if got != tt.expected {
			t.Errorf("threadSessionKey(%q, %q, %q) = %q, want %q",
				tt.role, tt.guildID, tt.threadID, got, tt.expected)
		}
	}
}

// --- Thread Binding: Bind, Get, Unbind ---

func TestThreadBindingStore_BindAndGet(t *testing.T) {
	store := newThreadBindingStore()

	sessionID := store.bind("G123", "T456", "ruri", 24*time.Hour)
	if sessionID != "agent:ruri:discord:thread:G123:T456" {
		t.Errorf("unexpected session ID: %s", sessionID)
	}

	b := store.get("G123", "T456")
	if b == nil {
		t.Fatal("expected binding, got nil")
	}
	if b.Agent != "ruri" {
		t.Errorf("expected role ruri, got %q", b.Agent)
	}
	if b.GuildID != "G123" {
		t.Errorf("expected guildID G123, got %q", b.GuildID)
	}
	if b.ThreadID != "T456" {
		t.Errorf("expected threadID T456, got %q", b.ThreadID)
	}
	if b.SessionID != sessionID {
		t.Errorf("expected sessionID %q, got %q", sessionID, b.SessionID)
	}
}

func TestThreadBindingStore_GetNotFound(t *testing.T) {
	store := newThreadBindingStore()

	b := store.get("G999", "T999")
	if b != nil {
		t.Errorf("expected nil for unbound thread, got %+v", b)
	}
}

func TestThreadBindingStore_Unbind(t *testing.T) {
	store := newThreadBindingStore()

	store.bind("G123", "T456", "hisui", 24*time.Hour)
	if store.get("G123", "T456") == nil {
		t.Fatal("expected binding after bind")
	}

	store.unbind("G123", "T456")
	if store.get("G123", "T456") != nil {
		t.Error("expected nil after unbind")
	}
}

func TestThreadBindingStore_UnbindNonExistent(t *testing.T) {
	store := newThreadBindingStore()
	// Should not panic.
	store.unbind("G999", "T999")
}

// --- TTL Expiration ---

func TestThreadBindingStore_TTLExpiration(t *testing.T) {
	store := newThreadBindingStore()

	// Bind with a very short TTL.
	store.bind("G123", "T456", "kokuyou", 1*time.Millisecond)

	// Wait for expiration.
	time.Sleep(5 * time.Millisecond)

	b := store.get("G123", "T456")
	if b != nil {
		t.Errorf("expected nil for expired binding, got %+v", b)
	}
}

func TestThreadBindingStore_TTLNotYetExpired(t *testing.T) {
	store := newThreadBindingStore()

	store.bind("G123", "T456", "ruri", 1*time.Hour)

	b := store.get("G123", "T456")
	if b == nil {
		t.Fatal("expected binding before TTL expires")
	}
	if b.Agent != "ruri" {
		t.Errorf("expected role ruri, got %q", b.Agent)
	}
}

// --- Cleanup ---

func TestThreadBindingStore_Cleanup(t *testing.T) {
	store := newThreadBindingStore()

	// Bind two: one expired, one active.
	store.bind("G1", "T1", "ruri", 1*time.Millisecond)
	store.bind("G2", "T2", "hisui", 1*time.Hour)

	time.Sleep(5 * time.Millisecond)

	store.cleanup()

	if store.get("G1", "T1") != nil {
		t.Error("expected expired binding to be cleaned up")
	}
	if store.get("G2", "T2") == nil {
		t.Error("expected active binding to survive cleanup")
	}
	if store.count() != 1 {
		t.Errorf("expected 1 active binding, got %d", store.count())
	}
}

func TestThreadBindingStore_CleanupEmpty(t *testing.T) {
	store := newThreadBindingStore()
	// Should not panic on empty store.
	store.cleanup()
	if store.count() != 0 {
		t.Errorf("expected 0, got %d", store.count())
	}
}

// --- Concurrent Access ---

func TestThreadBindingStore_Concurrent(t *testing.T) {
	store := newThreadBindingStore()
	var wg sync.WaitGroup

	// Concurrent binds.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			gid := "G1"
			tid := fmt.Sprintf("T%d", n)
			store.bind(gid, tid, "ruri", 1*time.Hour)
		}(i)
	}

	// Concurrent reads.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			store.get("G1", fmt.Sprintf("T%d", n))
		}(i)
	}

	// Concurrent cleanup.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.cleanup()
		}()
	}

	wg.Wait()

	// All 50 should exist (none expired).
	if store.count() != 50 {
		t.Errorf("expected 50 bindings, got %d", store.count())
	}
}

// --- Channel Type Detection ---

func TestIsThreadChannel(t *testing.T) {
	tests := []struct {
		channelType int
		expected    bool
	}{
		{discordChannelTypePublicThread, true},
		{discordChannelTypePrivateThread, true},
		{discordChannelTypeForum, true},
		{0, false},  // guild text
		{1, false},  // DM
		{2, false},  // guild voice
		{5, false},  // guild announcement
		{13, false}, // guild stage voice
	}
	for _, tt := range tests {
		got := isThreadChannel(tt.channelType)
		if got != tt.expected {
			t.Errorf("isThreadChannel(%d) = %v, want %v", tt.channelType, got, tt.expected)
		}
	}
}

// --- Forum Auto-Thread Detection ---

func TestForumChannelDetection(t *testing.T) {
	// Forum channels (type 15) should be treated as threads.
	if !isThreadChannel(discordChannelTypeForum) {
		t.Error("expected forum channel type 15 to be detected as thread")
	}
}

// --- Binding Key ---

func TestThreadBindingKey(t *testing.T) {
	tests := []struct {
		guildID, threadID, expected string
	}{
		{"G123", "T456", "G123:T456"},
		{"", "T456", ":T456"},
		{"G123", "", "G123:"},
	}
	for _, tt := range tests {
		got := threadBindingKey(tt.guildID, tt.threadID)
		if got != tt.expected {
			t.Errorf("threadBindingKey(%q, %q) = %q, want %q",
				tt.guildID, tt.threadID, got, tt.expected)
		}
	}
}

// --- Override Binding ---

func TestThreadBindingStore_OverrideBind(t *testing.T) {
	store := newThreadBindingStore()

	store.bind("G1", "T1", "ruri", 1*time.Hour)
	b := store.get("G1", "T1")
	if b == nil || b.Agent != "ruri" {
		t.Fatal("expected ruri binding")
	}

	// Override with a different role.
	store.bind("G1", "T1", "hisui", 2*time.Hour)
	b = store.get("G1", "T1")
	if b == nil || b.Agent != "hisui" {
		t.Fatal("expected hisui binding after override")
	}
	if b.SessionID != "agent:hisui:discord:thread:G1:T1" {
		t.Errorf("expected updated session ID, got %q", b.SessionID)
	}
}

// --- TTL Config Default ---

func TestThreadBindingsConfigTTL(t *testing.T) {
	// Default (zero value).
	cfg := DiscordThreadBindingsConfig{}
	if cfg.threadBindingsTTL() != 24*time.Hour {
		t.Errorf("expected 24h default, got %v", cfg.threadBindingsTTL())
	}

	// Custom value.
	cfg = DiscordThreadBindingsConfig{TTLHours: 48}
	if cfg.threadBindingsTTL() != 48*time.Hour {
		t.Errorf("expected 48h, got %v", cfg.threadBindingsTTL())
	}

	// Negative value defaults to 24h.
	cfg = DiscordThreadBindingsConfig{TTLHours: -1}
	if cfg.threadBindingsTTL() != 24*time.Hour {
		t.Errorf("expected 24h for negative, got %v", cfg.threadBindingsTTL())
	}
}

// --- Binding Expired Method ---

func TestThreadBinding_Expired(t *testing.T) {
	b := &threadBinding{
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	if !b.expired() {
		t.Error("expected expired for past expiration time")
	}

	b = &threadBinding{
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if b.expired() {
		t.Error("expected not expired for future expiration time")
	}
}

// --- Count ---

func TestThreadBindingStore_Count(t *testing.T) {
	store := newThreadBindingStore()

	if store.count() != 0 {
		t.Errorf("expected 0, got %d", store.count())
	}

	store.bind("G1", "T1", "ruri", 1*time.Hour)
	store.bind("G2", "T2", "hisui", 1*time.Hour)
	store.bind("G3", "T3", "kokuyou", 1*time.Millisecond)

	time.Sleep(5 * time.Millisecond)

	// T3 is expired, so count should be 2.
	if store.count() != 2 {
		t.Errorf("expected 2 active bindings, got %d", store.count())
	}
}
