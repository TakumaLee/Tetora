package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestJSONRPCRequest(t *testing.T) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded jsonRPCRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %s", decoded.JSONRPC)
	}

	if decoded.ID != 1 {
		t.Errorf("expected id 1, got %d", decoded.ID)
	}

	if decoded.Method != "initialize" {
		t.Errorf("expected method initialize, got %s", decoded.Method)
	}
}

func TestJSONRPCResponse(t *testing.T) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  json.RawMessage(`{"status":"ok"}`),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded jsonRPCResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %s", decoded.JSONRPC)
	}

	var result map[string]string
	if err := json.Unmarshal(decoded.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result["status"] != "ok" {
		t.Errorf("expected status ok, got %s", result["status"])
	}
}

func TestJSONRPCError(t *testing.T) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Error: &jsonRPCError{
			Code:    -32601,
			Message: "Method not found",
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded jsonRPCResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Error == nil {
		t.Fatal("expected error, got nil")
	}

	if decoded.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", decoded.Error.Code)
	}

	if decoded.Error.Message != "Method not found" {
		t.Errorf("expected message 'Method not found', got %s", decoded.Error.Message)
	}
}

func TestMCPServerStatus(t *testing.T) {
	status := MCPServerStatus{
		Name:      "test-server",
		Status:    "running",
		Tools:     []string{"mcp:test:tool1", "mcp:test:tool2"},
		Restarts:  0,
		LastError: "",
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded MCPServerStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Name != "test-server" {
		t.Errorf("expected name test-server, got %s", decoded.Name)
	}

	if decoded.Status != "running" {
		t.Errorf("expected status running, got %s", decoded.Status)
	}

	if len(decoded.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(decoded.Tools))
	}
}

func TestToolNamePrefixing(t *testing.T) {
	serverName := "test-server"
	toolName := "read_file"
	expected := "mcp:test-server:read_file"

	prefixed := fmt.Sprintf("mcp:%s:%s", serverName, toolName)

	if prefixed != expected {
		t.Errorf("expected %s, got %s", expected, prefixed)
	}
}

func TestInitializeParams(t *testing.T) {
	params := initializeParams{
		ProtocolVersion: "2025-03-26",
		Capabilities: map[string]interface{}{
			"roots": map[string]interface{}{
				"listChanged": true,
			},
		},
	}
	params.ClientInfo.Name = "tetora"
	params.ClientInfo.Version = "2.0"

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded initializeParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.ProtocolVersion != "2025-03-26" {
		t.Errorf("expected protocol version 2025-03-26, got %s", decoded.ProtocolVersion)
	}

	if decoded.ClientInfo.Name != "tetora" {
		t.Errorf("expected client name tetora, got %s", decoded.ClientInfo.Name)
	}
}

func TestToolsListResult(t *testing.T) {
	resultJSON := `{
		"tools": [
			{
				"name": "read_file",
				"description": "Read a file",
				"inputSchema": {"type":"object","properties":{"path":{"type":"string"}}}
			}
		]
	}`

	var result toolsListResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}

	tool := result.Tools[0]
	if tool.Name != "read_file" {
		t.Errorf("expected tool name read_file, got %s", tool.Name)
	}

	if tool.Description != "Read a file" {
		t.Errorf("expected description 'Read a file', got %s", tool.Description)
	}
}

func TestToolsCallParams(t *testing.T) {
	params := toolsCallParams{
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"/tmp/test.txt"}`),
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded toolsCallParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Name != "read_file" {
		t.Errorf("expected name read_file, got %s", decoded.Name)
	}

	var args map[string]string
	if err := json.Unmarshal(decoded.Arguments, &args); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}

	if args["path"] != "/tmp/test.txt" {
		t.Errorf("expected path /tmp/test.txt, got %s", args["path"])
	}
}

func TestToolsCallResult(t *testing.T) {
	resultJSON := `{
		"content": [
			{"type": "text", "text": "file contents here"}
		]
	}`

	var result toolsCallResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}

	content := result.Content[0]
	if content.Type != "text" {
		t.Errorf("expected type text, got %s", content.Type)
	}

	if content.Text != "file contents here" {
		t.Errorf("expected text 'file contents here', got %s", content.Text)
	}
}

func TestMCPHostCreation(t *testing.T) {
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"test": {
				Command: "echo",
				Args:    []string{"hello"},
			},
		},
	}

	toolReg := NewToolRegistry(cfg)
	host := newMCPHost(cfg, toolReg)

	if host == nil {
		t.Fatal("expected host, got nil")
	}

	if host.cfg != cfg {
		t.Error("host config not set correctly")
	}

	if host.toolReg != toolReg {
		t.Error("host tool registry not set correctly")
	}

	if len(host.servers) != 0 {
		t.Errorf("expected 0 servers initially, got %d", len(host.servers))
	}
}

func TestMCPServerStatusGeneration(t *testing.T) {
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{},
	}
	toolReg := NewToolRegistry(cfg)
	host := newMCPHost(cfg, toolReg)

	// Manually add a mock server.
	server := &MCPServer{
		Name:   "test-server",
		status: "running",
		Tools: []ToolDef{
			{Name: "mcp:test:tool1"},
			{Name: "mcp:test:tool2"},
		},
		restarts: 1,
	}

	host.mu.Lock()
	host.servers["test-server"] = server
	host.mu.Unlock()

	statuses := host.ServerStatus()

	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}

	status := statuses[0]
	if status.Name != "test-server" {
		t.Errorf("expected name test-server, got %s", status.Name)
	}

	if status.Status != "running" {
		t.Errorf("expected status running, got %s", status.Status)
	}

	if len(status.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(status.Tools))
	}

	if status.Restarts != 1 {
		t.Errorf("expected 1 restart, got %d", status.Restarts)
	}
}

func TestMCPServerDisabled(t *testing.T) {
	disabled := false
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"disabled-server": {
				Command: "echo",
				Enabled: &disabled,
			},
		},
	}

	toolReg := NewToolRegistry(cfg)
	host := newMCPHost(cfg, toolReg)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := host.Start(ctx); err != nil {
		t.Fatalf("start error: %v", err)
	}

	// Wait a bit for servers to start (or not).
	time.Sleep(50 * time.Millisecond)

	statuses := host.ServerStatus()
	if len(statuses) != 0 {
		t.Errorf("expected 0 servers (disabled), got %d", len(statuses))
	}
}
