package main

import (
	"sync"
	"time"
)

// threadParentCache caches the mapping from thread channel IDs to parent channel IDs.
// Discord threads have their own channel IDs that don't appear in config allowlists.
// This cache avoids repeated API calls to resolve thread→parent relationships.
// Bounded to threadParentCacheMaxSize entries with LRU-style eviction.
type threadParentCache struct {
	mu    sync.RWMutex
	items map[string]threadParentEntry
}

type threadParentEntry struct {
	ParentID  string    // empty string = negative cache (thread has no parent / API failed)
	ExpiresAt time.Time
}

const (
	threadParentCacheTTL     = 24 * time.Hour   // thread→parent is immutable; long TTL, bounded by max size
	threadParentNegativeTTL  = 5 * time.Minute  // shorter TTL for failed lookups (transient errors)
	threadParentCacheMaxSize = 1000
)

func newThreadParentCache() *threadParentCache {
	return &threadParentCache{
		items: make(map[string]threadParentEntry),
	}
}

// get returns the cached parent channel ID for a thread.
// Returns ("", false) if not cached or expired.
// Returns ("", true) if negative-cached (known non-thread or API failure).
// Returns (parentID, true) on cache hit.
func (c *threadParentCache) get(threadID string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.items[threadID]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return "", false
	}
	return entry.ParentID, true
}

// set caches a thread→parent mapping with TTL.
// parentID == "" caches a negative result (shorter TTL).
func (c *threadParentCache) set(threadID, parentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Evict expired entries if at capacity.
	if len(c.items) >= threadParentCacheMaxSize {
		c.evictExpiredLocked()
	}
	// If still at capacity after eviction, drop oldest entry.
	if len(c.items) >= threadParentCacheMaxSize {
		c.evictOldestLocked()
	}
	ttl := threadParentCacheTTL
	if parentID == "" {
		ttl = threadParentNegativeTTL
	}
	c.items[threadID] = threadParentEntry{
		ParentID:  parentID,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// cleanup removes all expired entries.
func (c *threadParentCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked()
}

// evictExpiredLocked removes expired entries. Caller must hold write lock.
func (c *threadParentCache) evictExpiredLocked() {
	now := time.Now()
	for k, v := range c.items {
		if now.After(v.ExpiresAt) {
			delete(c.items, k)
		}
	}
}

// evictOldestLocked removes the entry with the earliest expiration. Caller must hold write lock.
func (c *threadParentCache) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	for k, v := range c.items {
		if oldestKey == "" || v.ExpiresAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.ExpiresAt
		}
	}
	if oldestKey != "" {
		delete(c.items, oldestKey)
	}
}
