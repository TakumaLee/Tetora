package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEstimateRequestTokens(t *testing.T) {
	req := ProviderRequest{
		Prompt:       "Hello world",
		SystemPrompt: "You are a helpful assistant",
	}
	tokens := estimateRequestTokens(req)
	raw := (len("Hello world") + len("You are a helpful assistant")) / 4
	expected := raw
	if expected < 10 {
		expected = 10 // minimum floor
	}
	if tokens != expected {
		t.Errorf("got %d, want %d", tokens, expected)
	}
}

func TestEstimateRequestTokensWithMessages(t *testing.T) {
	msg := Message{Role: "user", Content: json.RawMessage(`"a long message here"`)}
	req := ProviderRequest{
		Prompt:   "test",
		Messages: []Message{msg},
	}
	tokens := estimateRequestTokens(req)
	if tokens <= 0 {
		t.Error("expected positive token count")
	}
}

func TestEstimateRequestTokensWithTools(t *testing.T) {
	req := ProviderRequest{
		Prompt: "test",
		Tools: []ToolDef{
			{Name: "web_search", Description: "Search the web", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	tokens := estimateRequestTokens(req)
	if tokens <= 1 {
		t.Error("should include tool definition tokens")
	}
}

func TestEstimateRequestTokensMinimum(t *testing.T) {
	req := ProviderRequest{}
	tokens := estimateRequestTokens(req)
	if tokens < 10 {
		t.Errorf("minimum should be 10, got %d", tokens)
	}
}

func TestContextWindowForModel(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"opus", 200000},
		{"claude-sonnet-4-5-20250929", 200000},
		{"haiku", 200000},
		{"gpt-4o", 128000},
		{"gpt-4o-mini", 128000},
		{"unknown-model", 200000},
	}
	for _, tt := range tests {
		got := contextWindowForModel(tt.model)
		if got != tt.want {
			t.Errorf("contextWindowForModel(%q) = %d, want %d", tt.model, got, tt.want)
		}
	}
}

func TestCompressMessages(t *testing.T) {
	// Create messages with some large content.
	msgs := make([]Message, 8)
	for i := range msgs {
		if i%2 == 0 {
			msgs[i] = Message{Role: "assistant", Content: json.RawMessage(`[{"type":"text","text":"` + strings.Repeat("x", 500) + `"}]`)}
		} else {
			msgs[i] = Message{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","content":"` + strings.Repeat("y", 500) + `"}]`)}
		}
	}

	compressed := compressMessages(msgs, 2)
	if len(compressed) != len(msgs) {
		t.Errorf("should preserve message count, got %d want %d", len(compressed), len(msgs))
	}

	// First 4 messages should be compressed (smaller).
	for i := 0; i < 4; i++ {
		if len(compressed[i].Content) >= len(msgs[i].Content) {
			t.Errorf("message %d should be compressed", i)
		}
	}

	// Last 4 should be unchanged.
	for i := 4; i < 8; i++ {
		if string(compressed[i].Content) != string(msgs[i].Content) {
			t.Errorf("message %d should be unchanged", i)
		}
	}
}

func TestCompressMessagesShortList(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: json.RawMessage(`"hello"`)},
		{Role: "user", Content: json.RawMessage(`"world"`)},
	}
	compressed := compressMessages(msgs, 3)
	// Should return same messages since fewer than keepRecent*2.
	if len(compressed) != 2 {
		t.Error("short list should be unchanged")
	}
}
