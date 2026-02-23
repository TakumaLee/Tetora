package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentList_ReturnsRoles(t *testing.T) {
	cfg := &Config{
		DefaultProvider: "claude",
		DefaultModel:    "sonnet",
		Roles: map[string]RoleConfig{
			"琉璃": {
				Description: "Coordinator agent",
				Keywords:    []string{"coordinate", "plan"},
				Model:       "opus",
			},
			"黒曜": {
				Description: "DevOps agent",
				Keywords:    []string{"deploy", "monitor"},
			},
		},
	}

	result, err := toolAgentList(context.Background(), cfg, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolAgentList failed: %v", err)
	}

	var agents []map[string]any
	if err := json.Unmarshal([]byte(result), &agents); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}

	// Check first agent has required fields.
	for _, agent := range agents {
		if agent["name"] == nil {
			t.Errorf("agent missing name field")
		}
		if agent["provider"] == nil {
			t.Errorf("agent missing provider field")
		}
		if agent["model"] == nil {
			t.Errorf("agent missing model field")
		}
	}
}

func TestAgentList_EmptyRoles(t *testing.T) {
	cfg := &Config{
		Roles: map[string]RoleConfig{},
	}

	result, err := toolAgentList(context.Background(), cfg, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("toolAgentList failed: %v", err)
	}

	var agents []map[string]any
	if err := json.Unmarshal([]byte(result), &agents); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestAgentDispatch_UnknownRole(t *testing.T) {
	cfg := &Config{
		Roles: map[string]RoleConfig{},
	}

	input := json.RawMessage(`{"role": "unknown", "prompt": "test"}`)
	_, err := toolAgentDispatch(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for unknown role, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestAgentDispatch_EmptyPrompt(t *testing.T) {
	cfg := &Config{
		Roles: map[string]RoleConfig{
			"test": {},
		},
	}

	input := json.RawMessage(`{"role": "test", "prompt": ""}`)
	_, err := toolAgentDispatch(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for empty prompt, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "required") {
		t.Errorf("expected 'required' error, got: %v", err)
	}
}

func TestAgentMessage_Store(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize DB.
	if err := initAgentCommDB(dbPath); err != nil {
		t.Fatalf("initAgentCommDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Roles: map[string]RoleConfig{
			"test": {},
		},
	}

	input := json.RawMessage(`{"role": "test", "message": "Hello test agent"}`)
	result, err := toolAgentMessage(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolAgentMessage failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	if res["status"] != "sent" {
		t.Errorf("expected status=sent, got %v", res["status"])
	}
	if res["messageId"] == nil {
		t.Error("expected messageId in result")
	}
}

func TestAgentMessage_Retrieve(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize DB.
	if err := initAgentCommDB(dbPath); err != nil {
		t.Fatalf("initAgentCommDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Roles: map[string]RoleConfig{
			"test": {},
		},
	}

	// Send a message.
	input := json.RawMessage(`{"role": "test", "message": "Test message"}`)
	_, err := toolAgentMessage(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolAgentMessage failed: %v", err)
	}

	// Retrieve messages.
	messages, err := getAgentMessages(dbPath, "test", false)
	if err != nil {
		t.Fatalf("getAgentMessages failed: %v", err)
	}

	if len(messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(messages))
	}

	if len(messages) > 0 {
		msg := messages[0]
		if msg["to_role"] != "test" {
			t.Errorf("expected to_role=test, got %v", msg["to_role"])
		}
		if msg["message"] != "Test message" {
			t.Errorf("expected message='Test message', got %v", msg["message"])
		}
	}
}

func TestAgentMessage_EmptyRole(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := initAgentCommDB(dbPath); err != nil {
		t.Fatalf("initAgentCommDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Roles:     map[string]RoleConfig{},
	}

	input := json.RawMessage(`{"role": "", "message": "test"}`)
	_, err := toolAgentMessage(context.Background(), cfg, input)
	if err == nil {
		t.Error("expected error for empty role, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "required") {
		t.Errorf("expected 'required' error, got: %v", err)
	}
}

func TestAgentMessage_WithSession(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := initAgentCommDB(dbPath); err != nil {
		t.Fatalf("initAgentCommDB failed: %v", err)
	}

	cfg := &Config{
		HistoryDB: dbPath,
		Roles: map[string]RoleConfig{
			"test": {},
		},
	}

	sessionID := "test-session-123"
	input := json.RawMessage(`{"role": "test", "message": "Session message", "sessionId": "` + sessionID + `"}`)
	result, err := toolAgentMessage(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolAgentMessage failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	// Verify stored in DB with session.
	messages, err := getAgentMessages(dbPath, "test", false)
	if err != nil {
		t.Fatalf("getAgentMessages failed: %v", err)
	}

	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	if messages[0]["session_id"] != sessionID {
		t.Errorf("expected session_id=%s, got %v", sessionID, messages[0]["session_id"])
	}
}

func TestAgentComm_ToolRegistration(t *testing.T) {
	cfg := &Config{
		Tools: ToolConfig{
			Builtin: map[string]bool{
				"agent_list":     true,
				"agent_dispatch": true,
				"agent_message":  true,
			},
		},
	}

	registry := NewToolRegistry(cfg)
	tools := registry.List()

	// Check that agent communication tools are registered.
	foundList := false
	foundDispatch := false
	foundMessage := false

	for _, tool := range tools {
		switch tool.Name {
		case "agent_list":
			foundList = true
		case "agent_dispatch":
			foundDispatch = true
		case "agent_message":
			foundMessage = true
		}
	}

	if !foundList {
		t.Error("agent_list tool not registered")
	}
	if !foundDispatch {
		t.Error("agent_dispatch tool not registered")
	}
	if !foundMessage {
		t.Error("agent_message tool not registered")
	}
}

func TestAgentCommDB_Init(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := initAgentCommDB(dbPath); err != nil {
		t.Fatalf("initAgentCommDB failed: %v", err)
	}

	// Verify table exists.
	sql := "SELECT name FROM sqlite_master WHERE type='table' AND name='agent_messages'"
	rows, err := queryDB(dbPath, sql)
	if err != nil {
		t.Fatalf("queryDB failed: %v", err)
	}

	if len(rows) != 1 {
		t.Errorf("expected agent_messages table to exist")
	}
}
