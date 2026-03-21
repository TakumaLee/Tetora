package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/handoff"
)

// ChildSemConcurrentOrDefault returns the capacity for the child semaphore.
// Default: 2x maxConcurrent. Configurable via agentComm.childPoolMultiplier.
func ChildSemConcurrentOrDefault(cfg *config.Config) int {
	m := cfg.AgentComm.ChildSem
	if m <= 0 {
		m = 2
	}
	return cfg.MaxConcurrent * m
}

// MaxDepthOrDefault returns the configured max nesting depth (default 3).
func MaxDepthOrDefault(cfg *config.Config) int {
	if cfg.AgentComm.MaxDepth > 0 {
		return cfg.AgentComm.MaxDepth
	}
	return 3
}

// MaxChildrenPerTaskOrDefault returns the configured max children per task (default 5).
func MaxChildrenPerTaskOrDefault(cfg *config.Config) int {
	if cfg.AgentComm.MaxChildrenPerTask > 0 {
		return cfg.AgentComm.MaxChildrenPerTask
	}
	return 5
}

// SpawnTracker tracks the number of active child tasks per parent task ID.
// This enforces the MaxChildrenPerTask limit to prevent unbounded spawning.
type SpawnTracker struct {
	mu       sync.RWMutex
	children map[string]int // parentTaskID → active child count
}

// NewSpawnTracker creates a new SpawnTracker.
func NewSpawnTracker() *SpawnTracker {
	return &SpawnTracker{
		children: make(map[string]int),
	}
}

// TrySpawn attempts to increment the child count for parentID.
// Returns true if the spawn is allowed (count < maxChildren), false otherwise.
func (st *SpawnTracker) TrySpawn(parentID string, maxChildren int) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if parentID == "" {
		return true // no parent tracking for top-level tasks
	}
	if maxChildren <= 0 {
		maxChildren = 5 // default
	}
	current := st.children[parentID]
	if current >= maxChildren {
		return false
	}
	st.children[parentID] = current + 1
	return true
}

// Release decrements the child count for parentID when a child task completes.
func (st *SpawnTracker) Release(parentID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if parentID == "" {
		return
	}
	if st.children[parentID] > 0 {
		st.children[parentID]--
	}
	if st.children[parentID] == 0 {
		delete(st.children, parentID)
	}
}

// Count returns the number of active children for a parent task.
func (st *SpawnTracker) Count(parentID string) int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.children[parentID]
}

// ToolAgentList lists all available agents/roles with their capabilities.
func ToolAgentList(ctx context.Context, cfg *config.Config, input json.RawMessage) (string, error) {
	var agents []map[string]any

	for name, role := range cfg.Agents {
		agent := map[string]any{
			"name":        name,
			"description": role.Description,
		}

		// Add keywords if present.
		if len(role.Keywords) > 0 {
			agent["capabilities"] = role.Keywords
		}

		// Add provider info.
		provider := role.Provider
		if provider == "" {
			provider = cfg.DefaultProvider
		}
		agent["provider"] = provider

		// Add model info.
		model := role.Model
		if model == "" {
			model = cfg.DefaultModel
		}
		agent["model"] = model

		agents = append(agents, agent)
	}

	b, _ := json.Marshal(agents)
	return string(b), nil
}

// ToolAgentDispatchArgs holds all dependencies for ToolAgentDispatch that
// would otherwise require access to root-package state.
type ToolAgentDispatchArgs struct {
	// Tracker is used to enforce the max children per task limit.
	// If nil, the global fallback tracker is used.
	Tracker *SpawnTracker
	// FillDefaults populates Task fields with config-derived defaults.
	FillDefaults func(task *Task)
	// Dispatch sends the task to the local HTTP API.
	// addr is the listen address (e.g. "127.0.0.1:7777").
	Addr string
	// APIToken is used for authorization.
	APIToken string
}

// ToolAgentMessage sends an async message to another agent's session.
func ToolAgentMessage(ctx context.Context, cfg *config.Config, input json.RawMessage) (string, error) {
	var args struct {
		Agent     string `json:"agent"`
		Role      string `json:"role"` // backward compat
		Message   string `json:"message"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Agent == "" {
		args.Agent = args.Role
	}
	if args.Agent == "" {
		return "", fmt.Errorf("agent is required")
	}
	if args.Message == "" {
		return "", fmt.Errorf("message is required")
	}

	// Check if agent exists.
	if _, ok := cfg.Agents[args.Agent]; !ok {
		return "", fmt.Errorf("agent %q not found", args.Agent)
	}

	// Determine sender agent from context (if available).
	fromAgent := "system"

	// Generate message ID.
	messageID := GenerateMessageID()

	// Store message in DB.
	sql := fmt.Sprintf(
		`INSERT INTO agent_messages (id, from_agent, to_agent, message, session_id, created_at)
		 VALUES ('%s', '%s', '%s', '%s', '%s', '%s')`,
		db.Escape(messageID),
		db.Escape(fromAgent),
		db.Escape(args.Agent),
		db.Escape(args.Message),
		db.Escape(args.SessionID),
		time.Now().Format(time.RFC3339),
	)

	if _, err := db.Query(cfg.HistoryDB, sql); err != nil {
		return "", fmt.Errorf("store message: %w", err)
	}

	result := map[string]any{
		"status":    "sent",
		"messageId": messageID,
		"to":        args.Agent,
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// GenerateMessageID creates a random message ID.
func GenerateMessageID() string {
	var b [8]byte
	rand.Read(b[:])
	return "msg_" + hex.EncodeToString(b[:])
}

// InitAgentCommDB initializes the agent_messages table.
// Delegates to handoff.InitTables which owns the canonical unified DDL.
func InitAgentCommDB(dbPath string) error {
	handoff.InitTables(dbPath)
	return nil
}

// GetAgentMessages retrieves pending messages for a role.
func GetAgentMessages(dbPath, role string, markAsRead bool) ([]map[string]any, error) {
	sql := fmt.Sprintf(
		`SELECT id, from_agent, to_agent, message, session_id, created_at
		 FROM agent_messages
		 WHERE to_agent = '%s' AND read_at = ''
		 ORDER BY created_at ASC
		 LIMIT 50`,
		db.Escape(role),
	)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}

	// Mark as read if requested.
	if markAsRead && len(rows) > 0 {
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = fmt.Sprintf("'%s'", db.Escape(fmt.Sprintf("%v", row["id"])))
		}
		updateSQL := fmt.Sprintf(
			`UPDATE agent_messages SET read_at = '%s' WHERE id IN (%s)`,
			time.Now().Format(time.RFC3339),
			strings.Join(ids, ", "),
		)
		db.Query(dbPath, updateSQL) // ignore error
	}

	return rows, nil
}

