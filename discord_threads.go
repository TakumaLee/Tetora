package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"tetora/internal/log"
)

// --- Thread Binding ---

// threadBinding represents a Discord thread bound to a specific agent session.
type threadBinding struct {
	Agent      string
	GuildID   string
	ThreadID  string
	SessionID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// expired returns true if the binding has passed its expiration time.
func (b *threadBinding) expired() bool {
	return time.Now().After(b.ExpiresAt)
}

// --- Thread Binding Store ---

// threadBindingStore manages thread-to-agent bindings with TTL expiration.
type threadBindingStore struct {
	mu       sync.RWMutex
	bindings map[string]*threadBinding // key: "guildId:threadId"
}

// newThreadBindingStore creates a new empty thread binding store.
func newThreadBindingStore() *threadBindingStore {
	return &threadBindingStore{
		bindings: make(map[string]*threadBinding),
	}
}

// threadBindingKey generates the map key for a guild/thread pair.
func threadBindingKey(guildID, threadID string) string {
	return guildID + ":" + threadID
}

// bind creates or updates a thread binding. Returns the generated session ID.
func (s *threadBindingStore) bind(guildID, threadID, agent string, ttl time.Duration) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := threadBindingKey(guildID, threadID)
	now := time.Now()
	sessionID := threadSessionKey(agent, guildID, threadID)

	s.bindings[key] = &threadBinding{
		Agent:     agent,
		GuildID:   guildID,
		ThreadID:  threadID,
		SessionID: sessionID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	return sessionID
}

// unbind removes a thread binding.
func (s *threadBindingStore) unbind(guildID, threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bindings, threadBindingKey(guildID, threadID))
}

// get retrieves a thread binding, returning nil if not found or expired.
func (s *threadBindingStore) get(guildID, threadID string) *threadBinding {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.bindings[threadBindingKey(guildID, threadID)]
	if !ok {
		return nil
	}
	if b.expired() {
		return nil
	}
	return b
}

// cleanup removes all expired bindings.
func (s *threadBindingStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, b := range s.bindings {
		if b.expired() {
			delete(s.bindings, key)
		}
	}
}

// count returns the number of active (non-expired) bindings. Used for status/testing.
func (s *threadBindingStore) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := 0
	for _, b := range s.bindings {
		if !b.expired() {
			n++
		}
	}
	return n
}

// --- Session Key ---

// threadSessionKey generates a deterministic session key for a thread binding.
// Format: agent:{agent}:discord:thread:{guildId}:{threadId}
func threadSessionKey(agent, guildID, threadID string) string {
	return fmt.Sprintf("agent:%s:discord:thread:%s:%s", agent, guildID, threadID)
}

// --- Cleanup Goroutine ---

// startThreadCleanup runs periodic cleanup of expired thread bindings and parent cache entries.
func startThreadCleanup(ctx context.Context, store *threadBindingStore, parentCache *threadParentCache) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			store.cleanup()
			if parentCache != nil {
				parentCache.cleanup()
			}
			log.Debug("discord thread cleanup complete", "bindings", store.count())
		}
	}
}
