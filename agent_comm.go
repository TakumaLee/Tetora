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
	"time"
)

// --- Agent Communication Tools ---
// These are registered as built-in tools in the tool registry.

// toolAgentList lists all available agents/roles with their capabilities.
func toolAgentList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var agents []map[string]any

	for name, role := range cfg.Roles {
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
func toolAgentDispatch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Role    string  `json:"role"`
		Prompt  string  `json:"prompt"`
		Timeout float64 `json:"timeout"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Role == "" {
		return "", fmt.Errorf("role is required")
	}
	if args.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if args.Timeout <= 0 {
		args.Timeout = 300 // default 5 minutes
	}

	// Check if role exists.
	if _, ok := cfg.Roles[args.Role]; !ok {
		return "", fmt.Errorf("role %q not found", args.Role)
	}

	// Build task request.
	task := Task{
		Prompt:  args.Prompt,
		Role:    args.Role,
		Timeout: fmt.Sprintf("%.0fs", args.Timeout),
		Source:  "agent_dispatch",
	}
	fillDefaults(cfg, &task)

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
		"role":       args.Role,
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
		Role      string `json:"role"`
		Message   string `json:"message"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Role == "" {
		return "", fmt.Errorf("role is required")
	}
	if args.Message == "" {
		return "", fmt.Errorf("message is required")
	}

	// Check if role exists.
	if _, ok := cfg.Roles[args.Role]; !ok {
		return "", fmt.Errorf("role %q not found", args.Role)
	}

	// Determine sender role from context (if available).
	fromRole := "system"
	// TODO: extract from context if we store current role there

	// Generate message ID.
	messageID := generateMessageID()

	// Store message in DB.
	sql := fmt.Sprintf(
		`INSERT INTO agent_messages (id, from_role, to_role, message, session_id, created_at)
		 VALUES ('%s', '%s', '%s', '%s', '%s', '%s')`,
		escapeSQLite(messageID),
		escapeSQLite(fromRole),
		escapeSQLite(args.Role),
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
		"to":        args.Role,
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
	sql := `
CREATE TABLE IF NOT EXISTS agent_messages (
    id TEXT PRIMARY KEY,
    from_role TEXT NOT NULL,
    to_role TEXT NOT NULL,
    message TEXT NOT NULL,
    session_id TEXT DEFAULT '',
    created_at TEXT NOT NULL,
    read_at TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_agent_messages_to_role ON agent_messages(to_role, read_at);
CREATE INDEX IF NOT EXISTS idx_agent_messages_session ON agent_messages(session_id);
`
	_, err := queryDB(dbPath, sql)
	return err
}

// getAgentMessages retrieves pending messages for a role.
func getAgentMessages(dbPath, role string, markAsRead bool) ([]map[string]any, error) {
	sql := fmt.Sprintf(
		`SELECT id, from_role, to_role, message, session_id, created_at
		 FROM agent_messages
		 WHERE to_role = '%s' AND read_at = ''
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
