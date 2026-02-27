package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// --- Agent Communication Tools ---
// These are registered as built-in tools in the tool registry.

// childSemConcurrentOrDefault returns the capacity for the child semaphore.
// Default: 2x maxConcurrent. Configurable via agentComm.childPoolMultiplier.
func childSemConcurrentOrDefault(cfg *Config) int {
	m := cfg.AgentComm.ChildSem
	if m <= 0 {
		m = 2
	}
	return cfg.MaxConcurrent * m
}

// --- P13.3: Nested Sub-Agents ---

// spawnTracker tracks the number of active child tasks per parent task ID.
// This enforces the maxChildrenPerTask limit to prevent unbounded spawning.
type spawnTracker struct {
	mu       sync.RWMutex
	children map[string]int // parentTaskID → active child count
}

// globalSpawnTracker is the package-level spawn tracker instance.
var globalSpawnTracker = &spawnTracker{
	children: make(map[string]int),
}

// trySpawn attempts to increment the child count for parentID.
// Returns true if the spawn is allowed (count < maxChildren), false otherwise.
func (st *spawnTracker) trySpawn(parentID string, maxChildren int) bool {
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

// release decrements the child count for parentID when a child task completes.
func (st *spawnTracker) release(parentID string) {
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

// count returns the number of active children for a parent task.
func (st *spawnTracker) count(parentID string) int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.children[parentID]
}

// maxDepthOrDefault returns the configured max nesting depth (default 3).
func maxDepthOrDefault(cfg *Config) int {
	if cfg.AgentComm.MaxDepth > 0 {
		return cfg.AgentComm.MaxDepth
	}
	return 3
}

// maxChildrenPerTaskOrDefault returns the configured max children per task (default 5).
func maxChildrenPerTaskOrDefault(cfg *Config) int {
	if cfg.AgentComm.MaxChildrenPerTask > 0 {
		return cfg.AgentComm.MaxChildrenPerTask
	}
	return 5
}

// toolAgentList lists all available agents/roles with their capabilities.
func toolAgentList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
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

// toolAgentDispatch dispatches a sub-task to another agent and waits for the result.
// This implementation calls the local HTTP API to avoid needing direct access to dispatchState.
// --- P13.3: Nested Sub-Agents --- Added depth tracking, max depth enforcement, and spawn control.
func toolAgentDispatch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Agent    string  `json:"agent"`
		Role     string  `json:"role"` // backward compat
		Prompt   string  `json:"prompt"`
		Timeout  float64 `json:"timeout"`
		Depth    int     `json:"depth"`    // --- P13.3: current depth (passed by parent)
		ParentID string  `json:"parentId"` // --- P13.3: parent task ID
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
	if args.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if args.Timeout <= 0 {
		if cfg.AgentComm.DefaultTimeout > 0 {
			args.Timeout = float64(cfg.AgentComm.DefaultTimeout)
		} else {
			// Use smart estimation from prompt; default 1h if no prompt.
			estimated, err := time.ParseDuration(estimateTimeout(args.Prompt))
			if err != nil {
				estimated = time.Hour
			}
			args.Timeout = estimated.Seconds()
		}
	}

	// --- P13.3: Enforce max nesting depth.
	childDepth := args.Depth + 1
	maxDepth := maxDepthOrDefault(cfg)
	if args.Depth >= maxDepth {
		return "", fmt.Errorf("max nesting depth exceeded: current depth %d >= maxDepth %d", args.Depth, maxDepth)
	}

	// --- P13.3: Enforce max children per parent task.
	maxChildren := maxChildrenPerTaskOrDefault(cfg)
	if args.ParentID != "" {
		if !globalSpawnTracker.trySpawn(args.ParentID, maxChildren) {
			return "", fmt.Errorf("max children per task exceeded: parent %s already has %d active children (limit %d)",
				args.ParentID, globalSpawnTracker.count(args.ParentID), maxChildren)
		}
		// Release when done (deferred).
		defer globalSpawnTracker.release(args.ParentID)
	}

	// Check if agent exists.
	if _, ok := cfg.Agents[args.Agent]; !ok {
		return "", fmt.Errorf("agent %q not found", args.Agent)
	}

	// Build task request.
	task := Task{
		Prompt:   args.Prompt,
		Agent:    args.Agent,
		Timeout:  fmt.Sprintf("%.0fs", args.Timeout),
		Source:   "agent_dispatch",
		Depth:    childDepth,  // --- P13.3: propagate depth
		ParentID: args.ParentID, // --- P13.3: propagate parent ID
	}
	fillDefaults(cfg, &task)

	logDebug("agent_dispatch", "agent", args.Agent, "depth", childDepth, "parentId", args.ParentID)

	// Call local HTTP API.
	requestBody, _ := json.Marshal([]Task{task})

	// Determine listen address.
	addr := cfg.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:7777"
	}

	url := fmt.Sprintf("http://%s/dispatch", addr)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tetora-Source", "agent_dispatch")

	// Add auth token if configured.
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}

	// Execute request with timeout.
	client := &http.Client{
		Timeout: time.Duration(args.Timeout+10) * time.Second, // add buffer
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dispatch failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Parse result.
	var dispatchResult DispatchResult
	if err := json.Unmarshal(body, &dispatchResult); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(dispatchResult.Tasks) == 0 {
		return "", fmt.Errorf("no task result returned")
	}

	taskResult := dispatchResult.Tasks[0]

	// Build result summary.
	result := map[string]any{
		"role":       args.Agent,
		"status":     taskResult.Status,
		"output":     taskResult.Output,
		"durationMs": taskResult.DurationMs,
		"costUsd":    taskResult.CostUSD,
	}
	if taskResult.Error != "" {
		result["error"] = taskResult.Error
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolAgentMessage sends an async message to another agent's session.
func toolAgentMessage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
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
	// TODO: extract from context if we store current agent there

	// Generate message ID.
	messageID := generateMessageID()

	// Store message in DB.
	sql := fmt.Sprintf(
		`INSERT INTO agent_messages (id, from_agent, to_agent, message, session_id, created_at)
		 VALUES ('%s', '%s', '%s', '%s', '%s', '%s')`,
		escapeSQLite(messageID),
		escapeSQLite(fromAgent),
		escapeSQLite(args.Agent),
		escapeSQLite(args.Message),
		escapeSQLite(args.SessionID),
		time.Now().Format(time.RFC3339),
	)

	if _, err := queryDB(cfg.HistoryDB, sql); err != nil {
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

// generateMessageID creates a random message ID.
func generateMessageID() string {
	var b [8]byte
	rand.Read(b[:])
	return "msg_" + hex.EncodeToString(b[:])
}

// initAgentCommDB initializes the agent_messages table.
func initAgentCommDB(dbPath string) error {
	// Migration: rename from_role/to_role -> from_agent/to_agent.
	for _, stmt := range []string{
		`ALTER TABLE agent_messages RENAME COLUMN from_role TO from_agent;`,
		`ALTER TABLE agent_messages RENAME COLUMN to_role TO to_agent;`,
	} {
		if err := execDB(dbPath, stmt); err != nil {
			// Ignore expected errors (column already renamed or table doesn't exist yet).
		}
	}

	sql := `
CREATE TABLE IF NOT EXISTS agent_messages (
    id TEXT PRIMARY KEY,
    from_agent TEXT NOT NULL,
    to_agent TEXT NOT NULL,
    message TEXT NOT NULL,
    session_id TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    read_at TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_agent_messages_to_agent ON agent_messages(to_agent, read_at);
CREATE INDEX IF NOT EXISTS idx_agent_messages_session ON agent_messages(session_id);
`
	_, err := queryDB(dbPath, sql)
	return err
}

// getAgentMessages retrieves pending messages for a role.
func getAgentMessages(dbPath, role string, markAsRead bool) ([]map[string]any, error) {
	sql := fmt.Sprintf(
		`SELECT id, from_agent, to_agent, message, session_id, created_at
		 FROM agent_messages
		 WHERE to_agent = '%s' AND read_at = ''
		 ORDER BY created_at ASC
		 LIMIT 50`,
		escapeSQLite(role),
	)

	rows, err := queryDB(dbPath, sql)
	if err != nil {
		return nil, err
	}

	// Mark as read if requested.
	if markAsRead && len(rows) > 0 {
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = fmt.Sprintf("'%s'", escapeSQLite(fmt.Sprintf("%v", row["id"])))
		}
		updateSQL := fmt.Sprintf(
			`UPDATE agent_messages SET read_at = '%s' WHERE id IN (%s)`,
			time.Now().Format(time.RFC3339),
			strings.Join(ids, ", "),
		)
		queryDB(dbPath, updateSQL) // ignore error
	}

	return rows, nil
}
