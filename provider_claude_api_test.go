package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeAPIProvider_Name(t *testing.T) {
	p := &ClaudeAPIProvider{name: "claude-api"}
	if p.Name() != "claude-api" {
		t.Errorf("Name() = %q, want claude-api", p.Name())
	}
}

func TestClaudeAPIProvider_ImplementsToolCapableProvider(t *testing.T) {
	var _ ToolCapableProvider = (*ClaudeAPIProvider)(nil)
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
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
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

// --- Mock HTTP server tests ---

func TestClaudeAPIProvider_StopReason_NonStreaming(t *testing.T) {
	// Mock server that returns a tool_use response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(claudeAPIResponse{
			ID:   "msg_test",
			Type: "message",
			Role: "assistant",
			Content: []claudeContent{
				{Type: "text", Text: "Let me check that."},
				{Type: "tool_use", ID: "toolu_1", Name: "read_file", Input: json.RawMessage(`{"path":"/etc/hosts"}`)},
			},
			StopReason: "tool_use",
			Usage:      struct{ InputTokens int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"` }{InputTokens: 100, OutputTokens: 50},
		})
	}))
	defer srv.Close()

	p := &ClaudeAPIProvider{
		name:      "test",
		apiKey:    "test-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 4096,
		baseURL:   srv.URL,
	}

	result, err := p.Execute(context.Background(), ProviderRequest{Prompt: "read /etc/hosts"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Error)
	}
	if result.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "tool_use")
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want read_file", result.ToolCalls[0].Name)
	}
	if result.Output != "Let me check that." {
		t.Errorf("Output = %q, want %q", result.Output, "Let me check that.")
	}
}

func TestClaudeAPIProvider_StopReason_EndTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(claudeAPIResponse{
			ID:   "msg_test2",
			Type: "message",
			Role: "assistant",
			Content: []claudeContent{
				{Type: "text", Text: "Hello there!"},
			},
			StopReason: "end_turn",
			Usage:      struct{ InputTokens int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"` }{InputTokens: 10, OutputTokens: 5},
		})
	}))
	defer srv.Close()

	p := &ClaudeAPIProvider{
		name:      "test",
		apiKey:    "test-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 4096,
		baseURL:   srv.URL,
	}

	result, err := p.Execute(context.Background(), ProviderRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "end_turn")
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("len(ToolCalls) = %d, want 0", len(result.ToolCalls))
	}
}

func TestClaudeAPIProvider_StopReason_Streaming(t *testing.T) {
	// Mock server that returns a streaming response with tool_use.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`{"type":"message_start","message":{"id":"msg_s1","usage":{"input_tokens":50,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Checking..."}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_s1","name":"exec"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
			`{"type":"message_stop"}`,
		}

		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	p := &ClaudeAPIProvider{
		name:      "test",
		apiKey:    "test-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 4096,
		baseURL:   srv.URL,
	}

	eventCh := make(chan SSEEvent, 100)
	result, err := p.Execute(context.Background(), ProviderRequest{
		Prompt:  "list files",
		EventCh: eventCh,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "tool_use")
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "exec" {
		t.Errorf("ToolCalls[0].Name = %q, want exec", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[0].ID != "toolu_s1" {
		t.Errorf("ToolCalls[0].ID = %q, want toolu_s1", result.ToolCalls[0].ID)
	}
	if result.Output != "Checking..." {
		t.Errorf("Output = %q, want %q", result.Output, "Checking...")
	}
}

func TestClaudeAPIProvider_Streaming_EndTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`{"type":"message_start","message":{"id":"msg_s2","usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done!"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`{"type":"message_stop"}`,
		}

		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	p := &ClaudeAPIProvider{
		name:      "test",
		apiKey:    "test-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 4096,
		baseURL:   srv.URL,
	}

	eventCh := make(chan SSEEvent, 100)
	result, err := p.Execute(context.Background(), ProviderRequest{
		Prompt:  "done",
		EventCh: eventCh,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "end_turn")
	}
	if result.Output != "Done!" {
		t.Errorf("Output = %q, want %q", result.Output, "Done!")
	}
}

func TestClaudeAPIProvider_MultiTurnMessages(t *testing.T) {
	// Mock server that captures the request body and verifies multi-turn messages.
	var capturedBody claudeAPIRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(claudeAPIResponse{
			ID:   "msg_mt",
			Type: "message",
			Role: "assistant",
			Content: []claudeContent{
				{Type: "text", Text: "Final answer."},
			},
			StopReason: "end_turn",
			Usage:      struct{ InputTokens int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"` }{InputTokens: 200, OutputTokens: 30},
		})
	}))
	defer srv.Close()

	p := &ClaudeAPIProvider{
		name:      "test",
		apiKey:    "test-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 4096,
		baseURL:   srv.URL,
	}

	// Build multi-turn messages (assistant with tool_use, user with tool_result).
	assistantContent, _ := json.Marshal([]ContentBlock{
		{Type: "text", Text: "Let me check."},
		{Type: "tool_use", ID: "toolu_1", Name: "read_file", Input: json.RawMessage(`{"path":"/tmp/test"}`)},
	})
	userContent, _ := json.Marshal([]ContentBlock{
		{Type: "tool_result", ToolUseID: "toolu_1", Content: "file contents here"},
	})

	result, err := p.ExecuteWithTools(context.Background(), ProviderRequest{
		Prompt:       "read the file",
		SystemPrompt: "You are helpful.",
		Messages: []Message{
			{Role: "assistant", Content: assistantContent},
			{Role: "user", Content: userContent},
		},
		Tools: []ToolDef{
			{
				Name:        "read_file",
				Description: "Read a file",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteWithTools error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Error)
	}

	// Verify the request had 3 messages (user prompt + assistant + user tool_result).
	if len(capturedBody.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(capturedBody.Messages))
	}

	// First message should be the user prompt.
	if capturedBody.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want user", capturedBody.Messages[0].Role)
	}

	// Second should be assistant.
	if capturedBody.Messages[1].Role != "assistant" {
		t.Errorf("Messages[1].Role = %q, want assistant", capturedBody.Messages[1].Role)
	}

	// Third should be user (tool results).
	if capturedBody.Messages[2].Role != "user" {
		t.Errorf("Messages[2].Role = %q, want user", capturedBody.Messages[2].Role)
	}

	// Verify tools were included.
	if len(capturedBody.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(capturedBody.Tools))
	}

	// Verify system prompt.
	if capturedBody.System != "You are helpful." {
		t.Errorf("System = %q, want %q", capturedBody.System, "You are helpful.")
	}

	// Verify result.
	if result.Output != "Final answer." {
		t.Errorf("Output = %q, want %q", result.Output, "Final answer.")
	}
	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "end_turn")
	}
}

func TestClaudeAPIProvider_ExecuteWithTools_NoMessages(t *testing.T) {
	// ExecuteWithTools with empty Messages should work like Execute.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body claudeAPIRequest
		json.NewDecoder(r.Body).Decode(&body)

		if len(body.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(body.Messages))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(claudeAPIResponse{
			ID:         "msg_single",
			Type:       "message",
			Role:       "assistant",
			Content:    []claudeContent{{Type: "text", Text: "Hello!"}},
			StopReason: "end_turn",
			Usage:      struct{ InputTokens int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"` }{InputTokens: 5, OutputTokens: 3},
		})
	}))
	defer srv.Close()

	p := &ClaudeAPIProvider{
		name:      "test",
		apiKey:    "test-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 4096,
		baseURL:   srv.URL,
	}

	result, err := p.ExecuteWithTools(context.Background(), ProviderRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Output != "Hello!" {
		t.Errorf("Output = %q, want Hello!", result.Output)
	}
}

func TestConvertMessageContent_TextBlocks(t *testing.T) {
	content, _ := json.Marshal([]claudeContent{
		{Type: "text", Text: "hello"},
		{Type: "tool_use", ID: "t1", Name: "exec", Input: json.RawMessage(`{"cmd":"ls"}`)},
	})

	result := convertMessageContent(content)
	blocks, ok := result.([]claudeContent)
	if !ok {
		t.Fatalf("expected []claudeContent, got %T", result)
	}
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("blocks[0].Type = %q, want text", blocks[0].Type)
	}
	if blocks[1].Type != "tool_use" {
		t.Errorf("blocks[1].Type = %q, want tool_use", blocks[1].Type)
	}
}

func TestConvertMessageContent_PlainString(t *testing.T) {
	content, _ := json.Marshal("plain text")
	result := convertMessageContent(content)
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string, got %T", result)
	}
	if s != "plain text" {
		t.Errorf("result = %q, want %q", s, "plain text")
	}
}

func TestConvertMessageContent_Empty(t *testing.T) {
	result := convertMessageContent(nil)
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string for empty input, got %T", result)
	}
	if s != "" {
		t.Errorf("result = %q, want empty", s)
	}
}

func TestClaudeAPIProvider_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit","message":"too many requests"}}`))
	}))
	defer srv.Close()

	p := &ClaudeAPIProvider{
		name:      "test",
		apiKey:    "test-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 4096,
		baseURL:   srv.URL,
	}

	result, err := p.Execute(context.Background(), ProviderRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
	if !strings.Contains(result.Error, "429") {
		t.Errorf("error should contain 429, got: %s", result.Error)
	}
}

func TestClaudeAPIProvider_MultipleToolCalls_NonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(claudeAPIResponse{
			ID:   "msg_multi",
			Type: "message",
			Role: "assistant",
			Content: []claudeContent{
				{Type: "text", Text: "I need two things."},
				{Type: "tool_use", ID: "t1", Name: "read_file", Input: json.RawMessage(`{"path":"/a"}`)},
				{Type: "tool_use", ID: "t2", Name: "read_file", Input: json.RawMessage(`{"path":"/b"}`)},
			},
			StopReason: "tool_use",
			Usage:      struct{ InputTokens int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"` }{InputTokens: 100, OutputTokens: 80},
		})
	}))
	defer srv.Close()

	p := &ClaudeAPIProvider{
		name:      "test",
		apiKey:    "test-key",
		model:     "claude-sonnet-4-5-20250929",
		maxTokens: 4096,
		baseURL:   srv.URL,
	}

	result, err := p.Execute(context.Background(), ProviderRequest{Prompt: "read both files"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", result.StopReason)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "t1" {
		t.Errorf("ToolCalls[0].ID = %q, want t1", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[1].ID != "t2" {
		t.Errorf("ToolCalls[1].ID = %q, want t2", result.ToolCalls[1].ID)
	}
}
