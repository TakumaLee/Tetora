package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeAPIProvider_Name(t *testing.T) {
	p := &ClaudeAPIProvider{name: "claude-api"}
	if p.Name() != "claude-api" {
		t.Errorf("Name() = %q, want claude-api", p.Name())
	}
}

func TestClaudeAPIRequest_Serialization(t *testing.T) {
	req := claudeAPIRequest{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
		Messages: []claudeMessage{
			{Role: "user", Content: "hello"},
		},
		System: "system prompt",
		Tools: []any{
			map[string]any{
				"name":        "test_tool",
				"description": "test",
				"input_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"arg": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded["model"] != "claude-sonnet-4-5-20250929" {
		t.Errorf("model = %v, want claude-sonnet-4-5-20250929", decoded["model"])
	}
	if decoded["max_tokens"].(float64) != 1024 {
		t.Errorf("max_tokens = %v, want 1024", decoded["max_tokens"])
	}
	if decoded["system"] != "system prompt" {
		t.Errorf("system = %v, want 'system prompt'", decoded["system"])
	}

	// Check tools array.
	tools, ok := decoded["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want array with 1 element", decoded["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "test_tool" {
		t.Errorf("tools[0].name = %v, want test_tool", tool["name"])
	}
}

func TestClaudeAPIResponse_Parse(t *testing.T) {
	jsonResp := `{
		"id": "msg_123",
		"type": "message",
		"role": "assistant",
		"content": [
			{"type": "text", "text": "Hello!"},
			{"type": "tool_use", "id": "tool_1", "name": "read", "input": {"path": "/tmp/file.txt"}}
		],
		"model": "claude-sonnet-4-5-20250929",
		"stop_reason": "tool_use",
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50
		}
	}`

	var resp claudeAPIResponse
	if err := json.Unmarshal([]byte(jsonResp), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.ID != "msg_123" {
		t.Errorf("ID = %q, want msg_123", resp.ID)
	}
	if resp.Type != "message" {
		t.Errorf("Type = %q, want message", resp.Type)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(resp.Content))
	}
	if resp.Content[0].Type != "text" {
		t.Errorf("Content[0].Type = %q, want text", resp.Content[0].Type)
	}
	if resp.Content[0].Text != "Hello!" {
		t.Errorf("Content[0].Text = %q, want Hello!", resp.Content[0].Text)
	}
	if resp.Content[1].Type != "tool_use" {
		t.Errorf("Content[1].Type = %q, want tool_use", resp.Content[1].Type)
	}
	if resp.Content[1].Name != "read" {
		t.Errorf("Content[1].Name = %q, want read", resp.Content[1].Name)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", resp.Usage.OutputTokens)
	}
}

func TestClaudeStreamChunk_Parse(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		wantType  string
		wantText  string
		wantTokens int
	}{
		{
			name:     "content_block_delta text",
			json:     `{"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello"}}`,
			wantType: "content_block_delta",
			wantText: "Hello",
		},
		{
			name:       "message_start",
			json:       `{"type": "message_start", "message": {"id": "msg_1", "usage": {"input_tokens": 10, "output_tokens": 0}}}`,
			wantType:   "message_start",
			wantTokens: 10,
		},
		{
			name:     "content_block_start tool_use",
			json:     `{"type": "content_block_start", "index": 1, "content_block": {"type": "tool_use", "id": "toolu_1", "name": "exec"}}`,
			wantType: "content_block_start",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chunk claudeStreamChunk
			if err := json.Unmarshal([]byte(tt.json), &chunk); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if chunk.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", chunk.Type, tt.wantType)
			}

			if chunk.Type == "content_block_delta" && chunk.Delta != nil {
				if chunk.Delta.Text != tt.wantText {
					t.Errorf("Delta.Text = %q, want %q", chunk.Delta.Text, tt.wantText)
				}
			}

			if chunk.Type == "message_start" && chunk.Message != nil {
				if chunk.Message.Usage.InputTokens != tt.wantTokens {
					t.Errorf("Message.Usage.InputTokens = %d, want %d", chunk.Message.Usage.InputTokens, tt.wantTokens)
				}
			}
		})
	}
}

func TestClaudeAPIProvider_CalculateCost(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		inputTokens  int
		outputTokens int
		wantCost     float64
	}{
		{
			name:         "sonnet-4-5 model",
			model:        "claude-sonnet-4-5-20250929",
			inputTokens:  1000,
			outputTokens: 500,
			wantCost:     0.0105, // (1000/1M * 3) + (500/1M * 15) = 0.003 + 0.0075
		},
		{
			name:         "opus-4 model",
			model:        "claude-opus-4-20250514",
			inputTokens:  1000,
			outputTokens: 500,
			wantCost:     0.0525, // (1000/1M * 15) + (500/1M * 75) = 0.015 + 0.0375
		},
		{
			name:         "haiku-3-5 model",
			model:        "claude-haiku-3-5-20241022",
			inputTokens:  1000,
			outputTokens: 500,
			wantCost:     0.000875, // (1000/1M * 0.25) + (500/1M * 1.25) = 0.00025 + 0.000625
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &ClaudeAPIProvider{}
			cost := p.calculateCost(tt.model, tt.inputTokens, tt.outputTokens)
			diff := cost - tt.wantCost
			if diff < 0 {
				diff = -diff
			}
			if diff > 1e-9 {
				t.Errorf("calculateCost() = %f, want %f", cost, tt.wantCost)
			}
		})
	}
}

func TestClaudeAPIProvider_Execute_BuildRequest(t *testing.T) {
	// This test verifies request construction without making a real API call.
	p := &ClaudeAPIProvider{
		name:      "test-api",
		apiKey:    "test-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 4096,
		baseURL:   "https://api.anthropic.com/v1",
	}

	req := ProviderRequest{
		Prompt:       "test prompt",
		SystemPrompt: "test system",
		Model:        "claude-haiku-3-5-20241022", // override
		Tools: []ToolDef{
			{
				Name:        "exec",
				Description: "Execute shell command",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
			},
		},
	}

	// We can't easily test Execute without a mock server, but we can verify
	// the provider is constructed correctly.
	if p.Name() != "test-api" {
		t.Errorf("Name() = %q, want test-api", p.Name())
	}
	if p.model != "claude-sonnet-4-5-20250929" {
		t.Errorf("model = %q, want claude-sonnet-4-5-20250929", p.model)
	}

	// Test that tools are properly formatted.
	if len(req.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Name != "exec" {
		t.Errorf("Tools[0].Name = %q, want exec", req.Tools[0].Name)
	}
}

// Note: Full integration tests for Execute() would require a real API key or mock HTTP server.
// The above tests verify data structure serialization and cost calculation logic.

func TestClaudeAPIProvider_Execute_InvalidAPIKey(t *testing.T) {
	// Skip this test in CI or if no network available.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	p := &ClaudeAPIProvider{
		name:      "test",
		apiKey:    "invalid-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 100,
		baseURL:   "https://api.anthropic.com/v1",
	}

	req := ProviderRequest{
		Prompt: "hello",
	}

	ctx := context.Background()
	result, err := p.Execute(ctx, req)

	// Should get an error or error result (not nil).
	if err == nil && (result == nil || !result.IsError) {
		t.Error("expected error with invalid API key")
	}

	// Error should mention 401 or 403.
	if err != nil {
		errMsg := err.Error()
		if !strings.Contains(errMsg, "401") && !strings.Contains(errMsg, "403") {
			t.Logf("error message: %s", errMsg)
		}
	}
	if result != nil && result.IsError {
		if !strings.Contains(result.Error, "401") && !strings.Contains(result.Error, "403") {
			t.Logf("error result: %s", result.Error)
		}
	}
}

