package main

// --- P14.2: Thread-Bound Sessions ---

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// --- Thread Parent Cache ---

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

// --- Config ---

// DiscordThreadBindingsConfig configures per-thread agent session isolation.
type DiscordThreadBindingsConfig struct {
	Enabled               bool `json:"enabled,omitempty"`
	TTLHours              int  `json:"ttlHours,omitempty"`              // default 24
	SpawnSubagentSessions bool `json:"spawnSubagentSessions,omitempty"` // allow threads to spawn sub-sessions
}

// threadBindingsTTL returns the configured TTL duration, defaulting to 24 hours.
func (c DiscordThreadBindingsConfig) threadBindingsTTL() time.Duration {
	if c.TTLHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(c.TTLHours) * time.Hour
}

// --- Discord Channel Types ---

const (
	discordChannelTypePublicThread  = 11
	discordChannelTypePrivateThread = 12
	discordChannelTypeForum         = 15
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
			logDebug("discord thread cleanup complete", "bindings", store.count())
		}
	}
}

// --- Channel Type Detection ---

// discordMessageWithType extends discordMessage with channel type info
// used for thread detection during MESSAGE_CREATE dispatch.
type discordMessageWithType struct {
	discordMessage
	ChannelType int `json:"channel_type,omitempty"`
}

// isThreadChannel returns true if the channel type represents a thread or forum.
func isThreadChannel(channelType int) bool {
	return channelType == discordChannelTypePublicThread ||
		channelType == discordChannelTypePrivateThread ||
		channelType == discordChannelTypeForum
}

// --- /focus and /unfocus Command Handlers ---

// availableRoleNames returns sorted agent names from config.
func (db *DiscordBot) availableRoleNames() []string {
	if db == nil || db.cfg == nil || db.cfg.Agents == nil {
		return nil
	}
	names := make([]string, 0, len(db.cfg.Agents))
	for r := range db.cfg.Agents {
		names = append(names, r)
	}
	sort.Strings(names)
	return names
}

// handleFocusCommand processes the /focus <agent> command to bind a thread to an agent.
func (db *DiscordBot) handleFocusCommand(msg discordMessage, args string, channelType int) bool {
	if !isThreadChannel(channelType) {
		db.sendMessage(msg.ChannelID, "The `/focus` command can only be used inside a thread.")
		return true
	}

	role := strings.TrimSpace(strings.ToLower(args))
	if role == "" {
		available := db.availableRoleNames()
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Usage: `/focus <agent>` — Available agents: %s", strings.Join(available, ", ")))
		return true
	}

	// Validate agent exists in config.
	_, roleExists := db.cfg.Agents[role]
	if db.cfg.Agents == nil || !roleExists {
		available := db.availableRoleNames()
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Unknown agent `%s`. Available: %s", role, strings.Join(available, ", ")))
		return true
	}

	guildID := msg.GuildID
	threadID := msg.ChannelID // in a thread, channel_id IS the thread ID
	ttl := db.cfg.Discord.ThreadBindings.threadBindingsTTL()

	sessionID := db.threads.bind(guildID, threadID, role, ttl)
	logInfo("discord thread bound", "guild", guildID, "thread", threadID, "agent", role, "session", sessionID)

	db.sendEmbed(msg.ChannelID, discordEmbed{
		Title:       fmt.Sprintf("Thread focused on %s", role),
		Description: fmt.Sprintf("This thread is now bound to agent **%s**.\nSession: `%s`\nExpires in %d hours.", role, sessionID, int(ttl.Hours())),
		Color:       0x57F287,
		Timestamp:   time.Now().Format(time.RFC3339),
	})
	return true
}

// handleUnfocusCommand processes the /unfocus command to unbind a thread.
func (db *DiscordBot) handleUnfocusCommand(msg discordMessage, channelType int) bool {
	if !isThreadChannel(channelType) {
		db.sendMessage(msg.ChannelID, "The `/unfocus` command can only be used inside a thread.")
		return true
	}

	guildID := msg.GuildID
	threadID := msg.ChannelID

	existing := db.threads.get(guildID, threadID)
	if existing == nil {
		db.sendMessage(msg.ChannelID, "This thread is not currently focused on any agent.")
		return true
	}

	db.threads.unbind(guildID, threadID)
	logInfo("discord thread unbound", "guild", guildID, "thread", threadID, "wasRole", existing.Agent)

	db.sendEmbed(msg.ChannelID, discordEmbed{
		Title:       "Thread unfocused",
		Description: fmt.Sprintf("Agent **%s** has been unbound from this thread.", existing.Agent),
		Color:       0xFEE75C,
		Timestamp:   time.Now().Format(time.RFC3339),
	})
	return true
}

// --- Thread-Aware Message Routing ---

// handleThreadMessage checks if a message is in a bound thread and routes accordingly.
// Returns true if the message was handled (bound thread routing), false for normal routing.
func (db *DiscordBot) handleThreadMessage(msg discordMessage, channelType int) bool {
	if db.threads == nil || !db.cfg.Discord.ThreadBindings.Enabled {
		return false
	}

	// channelType may be 0 when Discord omits it from the payload.
	// If it's explicitly a non-thread type (1-10), skip. If 0 or thread type, check binding.
	if channelType > 0 && !isThreadChannel(channelType) {
		return false
	}

	// For channelType == 0 (unknown), check if we have a binding as a fallback signal.
	// This handles cases where Discord doesn't include channel_type in MESSAGE_CREATE.
	binding := db.threads.get(msg.GuildID, msg.ChannelID)
	isThread := isThreadChannel(channelType)

	// Check for /focus and /unfocus commands (only in confirmed threads).
	text := discordStripMention(msg.Content, db.botUserID)
	text = strings.TrimSpace(text)

	if isThread {
		if strings.HasPrefix(text, "/focus") {
			args := strings.TrimPrefix(text, "/focus")
			return db.handleFocusCommand(msg, args, channelType)
		}
		if text == "/unfocus" {
			return db.handleUnfocusCommand(msg, channelType)
		}
	}

	if binding == nil {
		return false // not bound, use normal routing
	}

	// Thread is bound — route to the bound agent.
	db.handleThreadRoute(msg, text, binding)
	return true
}

// handleThreadRoute dispatches a message in a bound thread to the bound agent.
func (db *DiscordBot) handleThreadRoute(msg discordMessage, prompt string, binding *threadBinding) {
	if prompt == "" {
		return
	}

	db.sendTyping(msg.ChannelID)

	ctx := withTraceID(context.Background(), newTraceID("discord-thread"))
	dbPath := db.cfg.HistoryDB
	role := binding.Agent
	sessionID := binding.SessionID

	logInfoCtx(ctx, "discord thread dispatch",
		"thread", msg.ChannelID, "agent", role, "session", sessionID, "prompt", truncate(prompt, 60))

	// Get or create session using the thread binding's session ID as channel key.
	sess, err := getOrCreateChannelSession(dbPath, "discord", sessionID, role, "")
	if err != nil {
		logErrorCtx(ctx, "discord thread session error", "error", err)
	}

	// Context-aware prompt.
	// Skip text injection for providers with native session support (e.g. claude-code).
	contextPrompt := prompt
	if sess != nil {
		providerName := resolveProviderName(db.cfg, Task{Agent: role}, role)
		if !providerHasNativeSession(providerName) {
			sessionCtx := buildSessionContext(dbPath, sess.ID, db.cfg.Session.contextMessagesOrDefault())
			contextPrompt = wrapWithContext(sessionCtx, prompt)
		}
		now := time.Now().Format(time.RFC3339)
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID, Role: "user", Content: truncateStr(prompt, 5000), CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)
		title := fmt.Sprintf("[thread:%s] %s", role, prompt)
		if len(title) > 100 {
			title = title[:100]
		}
		updateSessionTitle(dbPath, sess.ID, title)
	}

	// Build and run task.
	task := Task{Prompt: contextPrompt, Agent: role, Source: "route:discord:thread"}
	fillDefaults(db.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}
	if role != "" {
		if soulPrompt, err := loadAgentPrompt(db.cfg, role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := db.cfg.Agents[role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}
	task.Prompt = expandPrompt(task.Prompt, "", db.cfg.HistoryDB, role, db.cfg.KnowledgeDir, db.cfg)

	taskStart := time.Now()
	result := runSingleTask(ctx, db.cfg, task, db.sem, db.childSem, role)

	recordHistory(db.cfg.HistoryDB, task.ID, task.Name, task.Source, role, task, result,
		taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record to session.
	if sess != nil {
		now := time.Now().Format(time.RFC3339)
		msgRole := "assistant"
		content := truncateStr(result.Output, 5000)
		if result.Status != "success" {
			msgRole = "system"
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			content = fmt.Sprintf("[%s] %s", result.Status, truncateStr(errMsg, 2000))
		}
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID, Role: msgRole, Content: content,
			CostUSD: result.CostUSD, TokensIn: result.TokensIn, TokensOut: result.TokensOut,
			Model: result.Model, TaskID: task.ID, CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, result.CostUSD, result.TokensIn, result.TokensOut, 1)
		maybeCompactSession(db.cfg, dbPath, sess.ID, sess.MessageCount+2, db.sem, db.childSem)
	}

	if result.Status == "success" {
		setMemory(db.cfg, role, "last_thread_output", truncate(result.Output, 500))
		setMemory(db.cfg, role, "last_thread_prompt", truncate(prompt, 200))
		setMemory(db.cfg, role, "last_thread_time", time.Now().Format(time.RFC3339))
	}

	auditLog(dbPath, "thread.dispatch", "discord",
		fmt.Sprintf("agent=%s thread=%s session=%s", role, msg.ChannelID, task.SessionID), "")

	// Send response embed.
	route := &RouteResult{Agent: role, Method: "thread-binding"}
	db.sendRouteResponse(msg.ChannelID, route, result, task, false)
}
