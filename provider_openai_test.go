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

func TestOpenAIProvider_ImplementsToolCapableProvider(t *testing.T) {
	var _ ToolCapableProvider = (*OpenAIProvider)(nil)
}

func TestOpenAIProvider_ToolCallParsing_NonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "chatcmpl-tool1",
			"choices": [{
				"message": {
					"content": "Let me check that.",
					"tool_calls": [{
						"id": "call_1",
						"type": "function",
						"function": {
							"name": "read_file",
							"arguments": "{\"path\":\"/etc/hosts\"}"
						}
					}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150}
		}`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		name:         "test-openai",
		baseURL:      srv.URL,
		apiKey:       "test-key",
		defaultModel: "gpt-4o",
	}

	result, err := p.Execute(context.Background(), ProviderRequest{Prompt: "read /etc/hosts"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "tool_use")
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_1" {
		t.Errorf("ToolCalls[0].ID = %q, want call_1", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want read_file", result.ToolCalls[0].Name)
	}
	if result.Output != "Let me check that." {
		t.Errorf("Output = %q, want %q", result.Output, "Let me check that.")
	}
}

func TestOpenAIProvider_FinishReasonMapping(t *testing.T) {
	tests := []struct {
		openAIReason string
		wantStop     string
	}{
		{"tool_calls", "tool_use"},
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"content_filter", "content_filter"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.openAIReason, func(t *testing.T) {
			got := mapOpenAIFinishReason(tt.openAIReason)
			if got != tt.wantStop {
				t.Errorf("mapOpenAIFinishReason(%q) = %q, want %q", tt.openAIReason, got, tt.wantStop)
			}
		})
	}
}

func TestOpenAIToolFormat(t *testing.T) {
	tools := []ToolDef{
		{
			Name:        "read_file",
			Description: "Read a file from disk",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
		{
			Name:        "exec",
			Description: "Execute a shell command",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
		},
	}

	var converted []openAITool
	for _, t := range tools {
		converted = append(converted, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	if len(converted) != 2 {
		t.Fatalf("len(converted) = %d, want 2", len(converted))
	}

	// Verify serialization.
	b, err := json.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded[0]["type"] != "function" {
		t.Errorf("type = %v, want function", decoded[0]["type"])
	}
	fn := decoded[0]["function"].(map[string]any)
	if fn["name"] != "read_file" {
		t.Errorf("name = %v, want read_file", fn["name"])
	}
	if fn["description"] != "Read a file from disk" {
		t.Errorf("description = %v, want 'Read a file from disk'", fn["description"])
	}
}

func TestOpenAIProvider_MultiTurnMessages(t *testing.T) {
	var capturedBody openAIRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "chatcmpl-mt",
			"choices": [{
				"message": {"content": "The file contains hosts."},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 200, "completion_tokens": 30, "total_tokens": 230}
		}`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		name:         "test-openai",
		baseURL:      srv.URL,
		apiKey:       "test-key",
		defaultModel: "gpt-4o",
	}

	// Build multi-turn messages (Claude format).
	assistantContent, _ := json.Marshal([]ContentBlock{
		{Type: "text", Text: "Let me check."},
		{Type: "tool_use", ID: "call_1", Name: "read_file", Input: json.RawMessage(`{"path":"/etc/hosts"}`)},
	})
	userContent, _ := json.Marshal([]ContentBlock{
		{Type: "tool_result", ToolUseID: "call_1", Content: "127.0.0.1 localhost"},
	})

	result, err := p.ExecuteWithTools(context.Background(), ProviderRequest{
		Prompt:       "read /etc/hosts",
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
		t.Fatalf("unexpected error: %s", result.Error)
	}

	// Verify messages: system + user prompt + assistant + tool result = 4 messages.
	if len(capturedBody.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4", len(capturedBody.Messages))
	}

	// system
	if capturedBody.Messages[0].Role != "system" {
		t.Errorf("Messages[0].Role = %q, want system", capturedBody.Messages[0].Role)
	}
	// user prompt
	if capturedBody.Messages[1].Role != "user" {
		t.Errorf("Messages[1].Role = %q, want user", capturedBody.Messages[1].Role)
	}
	if capturedBody.Messages[1].Content != "read /etc/hosts" {
		t.Errorf("Messages[1].Content = %q, want 'read /etc/hosts'", capturedBody.Messages[1].Content)
	}
	// assistant with tool_calls
	if capturedBody.Messages[2].Role != "assistant" {
		t.Errorf("Messages[2].Role = %q, want assistant", capturedBody.Messages[2].Role)
	}
	if len(capturedBody.Messages[2].ToolCalls) != 1 {
		t.Fatalf("Messages[2].ToolCalls = %d, want 1", len(capturedBody.Messages[2].ToolCalls))
	}
	if capturedBody.Messages[2].ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("ToolCalls[0].Function.Name = %q, want read_file", capturedBody.Messages[2].ToolCalls[0].Function.Name)
	}
	// tool result
	if capturedBody.Messages[3].Role != "tool" {
		t.Errorf("Messages[3].Role = %q, want tool", capturedBody.Messages[3].Role)
	}
	if capturedBody.Messages[3].ToolCallID != "call_1" {
		t.Errorf("Messages[3].ToolCallID = %q, want call_1", capturedBody.Messages[3].ToolCallID)
	}
	if capturedBody.Messages[3].Content != "127.0.0.1 localhost" {
		t.Errorf("Messages[3].Content = %q, want '127.0.0.1 localhost'", capturedBody.Messages[3].Content)
	}

	// Verify tools were sent.
	if len(capturedBody.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(capturedBody.Tools))
	}
	if capturedBody.Tools[0].Function.Name != "read_file" {
		t.Errorf("Tools[0].Function.Name = %q, want read_file", capturedBody.Tools[0].Function.Name)
	}

	// Verify result.
	if result.Output != "The file contains hosts." {
		t.Errorf("Output = %q, want %q", result.Output, "The file contains hosts.")
	}
	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", result.StopReason)
	}
}

func TestOpenAIProvider_MultipleToolResults(t *testing.T) {
	var capturedBody openAIRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "chatcmpl-mr",
			"choices": [{"message": {"content": "Done."}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 300, "completion_tokens": 5, "total_tokens": 305}
		}`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		name:         "test",
		baseURL:      srv.URL,
		apiKey:       "key",
		defaultModel: "gpt-4o",
	}

	// Two tool calls in one assistant message, two tool results in one user message.
	assistantContent, _ := json.Marshal([]ContentBlock{
		{Type: "tool_use", ID: "call_a", Name: "read_file", Input: json.RawMessage(`{"path":"/a"}`)},
		{Type: "tool_use", ID: "call_b", Name: "read_file", Input: json.RawMessage(`{"path":"/b"}`)},
	})
	userContent, _ := json.Marshal([]ContentBlock{
		{Type: "tool_result", ToolUseID: "call_a", Content: "content of a"},
		{Type: "tool_result", ToolUseID: "call_b", Content: "content of b"},
	})

	_, err := p.ExecuteWithTools(context.Background(), ProviderRequest{
		Prompt: "read both",
		Messages: []Message{
			{Role: "assistant", Content: assistantContent},
			{Role: "user", Content: userContent},
		},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Should have: user prompt + assistant (with 2 tool_calls) + 2 tool messages = 4.
	if len(capturedBody.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4", len(capturedBody.Messages))
	}

	// Messages[2] should be assistant with 2 tool calls.
	if len(capturedBody.Messages[1].ToolCalls) != 2 {
		t.Errorf("Messages[1].ToolCalls = %d, want 2", len(capturedBody.Messages[1].ToolCalls))
	}

	// Messages[2] and [3] should be tool results.
	if capturedBody.Messages[2].Role != "tool" {
		t.Errorf("Messages[2].Role = %q, want tool", capturedBody.Messages[2].Role)
	}
	if capturedBody.Messages[2].ToolCallID != "call_a" {
		t.Errorf("Messages[2].ToolCallID = %q, want call_a", capturedBody.Messages[2].ToolCallID)
	}
	if capturedBody.Messages[3].Role != "tool" {
		t.Errorf("Messages[3].Role = %q, want tool", capturedBody.Messages[3].Role)
	}
	if capturedBody.Messages[3].ToolCallID != "call_b" {
		t.Errorf("Messages[3].ToolCallID = %q, want call_b", capturedBody.Messages[3].ToolCallID)
	}
}

func TestOpenAIProvider_StreamingToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			// Text content first.
			`{"id":"chatcmpl-s1","choices":[{"delta":{"content":"Checking"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-s1","choices":[{"delta":{"content":"..."},"finish_reason":null}]}`,
			// Tool call starts.
			`{"id":"chatcmpl-s1","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_s1","type":"function","function":{"name":"exec","arguments":""}}]},"finish_reason":null}]}`,
			// Tool call arguments come in chunks.
			`{"id":"chatcmpl-s1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-s1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}`,
			// Finish reason.
			`{"id":"chatcmpl-s1","choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":50,"completion_tokens":30,"total_tokens":80}}`,
		}

		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		name:         "test",
		baseURL:      srv.URL,
		apiKey:       "key",
		defaultModel: "gpt-4o",
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
		t.Errorf("StopReason = %q, want tool_use", result.StopReason)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_s1" {
		t.Errorf("ToolCalls[0].ID = %q, want call_s1", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[0].Name != "exec" {
		t.Errorf("ToolCalls[0].Name = %q, want exec", result.ToolCalls[0].Name)
	}
	// Verify accumulated arguments.
	if string(result.ToolCalls[0].Input) != `{"cmd":"ls"}` {
		t.Errorf("ToolCalls[0].Input = %q, want %q", string(result.ToolCalls[0].Input), `{"cmd":"ls"}`)
	}
	if result.Output != "Checking..." {
		t.Errorf("Output = %q, want %q", result.Output, "Checking...")
	}
}

func TestOpenAIProvider_StreamingEndTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`{"id":"chatcmpl-e1","choices":[{"delta":{"content":"Hello!"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-e1","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		}

		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		name:         "test",
		baseURL:      srv.URL,
		apiKey:       "key",
		defaultModel: "gpt-4o",
	}

	eventCh := make(chan SSEEvent, 100)
	result, err := p.Execute(context.Background(), ProviderRequest{
		Prompt:  "hello",
		EventCh: eventCh,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", result.StopReason)
	}
	if result.Output != "Hello!" {
		t.Errorf("Output = %q, want Hello!", result.Output)
	}
}

func TestOpenAIProvider_ToolsIncludedInRequest(t *testing.T) {
	var capturedBody openAIRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "chatcmpl-tools",
			"choices": [{"message": {"content": "ok"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 50, "completion_tokens": 5, "total_tokens": 55}
		}`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		name:         "test",
		baseURL:      srv.URL,
		apiKey:       "key",
		defaultModel: "gpt-4o",
	}

	_, err := p.ExecuteWithTools(context.Background(), ProviderRequest{
		Prompt: "test",
		Tools: []ToolDef{
			{
				Name:        "read_file",
				Description: "Read a file from disk",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			},
			{
				Name:        "exec",
				Description: "Execute a command",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if len(capturedBody.Tools) != 2 {
		t.Fatalf("len(Tools) = %d, want 2", len(capturedBody.Tools))
	}
	if capturedBody.Tools[0].Type != "function" {
		t.Errorf("Tools[0].Type = %q, want function", capturedBody.Tools[0].Type)
	}
	if capturedBody.Tools[0].Function.Name != "read_file" {
		t.Errorf("Tools[0].Function.Name = %q, want read_file", capturedBody.Tools[0].Function.Name)
	}
	if capturedBody.Tools[1].Function.Name != "exec" {
		t.Errorf("Tools[1].Function.Name = %q, want exec", capturedBody.Tools[1].Function.Name)
	}
}

func TestOpenAIProvider_NoToolsNoToolsInRequest(t *testing.T) {
	var capturedBody openAIRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "chatcmpl-nt",
			"choices": [{"message": {"content": "hello"}, "finish_reason": "stop"}]
		}`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		name:         "test",
		baseURL:      srv.URL,
		apiKey:       "key",
		defaultModel: "gpt-4o",
	}

	_, err := p.Execute(context.Background(), ProviderRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if len(capturedBody.Tools) != 0 {
		t.Errorf("len(Tools) = %d, want 0 (no tools should be sent)", len(capturedBody.Tools))
	}
}

func TestConvertToOpenAIMessages_AssistantWithToolCalls(t *testing.T) {
	content, _ := json.Marshal([]ContentBlock{
		{Type: "text", Text: "I will check."},
		{Type: "tool_use", ID: "call_1", Name: "exec", Input: json.RawMessage(`{"cmd":"ls"}`)},
	})

	msgs := convertToOpenAIMessages(Message{Role: "assistant", Content: content})
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("Role = %q, want assistant", msgs[0].Role)
	}
	if msgs[0].Content != "I will check." {
		t.Errorf("Content = %q, want 'I will check.'", msgs[0].Content)
	}
	if len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(msgs[0].ToolCalls))
	}
	if msgs[0].ToolCalls[0].ID != "call_1" {
		t.Errorf("ToolCalls[0].ID = %q, want call_1", msgs[0].ToolCalls[0].ID)
	}
	if msgs[0].ToolCalls[0].Function.Name != "exec" {
		t.Errorf("ToolCalls[0].Function.Name = %q, want exec", msgs[0].ToolCalls[0].Function.Name)
	}
}

func TestConvertToOpenAIMessages_UserWithToolResults(t *testing.T) {
	content, _ := json.Marshal([]ContentBlock{
		{Type: "tool_result", ToolUseID: "call_1", Content: "result 1"},
		{Type: "tool_result", ToolUseID: "call_2", Content: "result 2"},
	})

	msgs := convertToOpenAIMessages(Message{Role: "user", Content: content})
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].Role != "tool" {
		t.Errorf("msgs[0].Role = %q, want tool", msgs[0].Role)
	}
	if msgs[0].ToolCallID != "call_1" {
		t.Errorf("msgs[0].ToolCallID = %q, want call_1", msgs[0].ToolCallID)
	}
	if msgs[0].Content != "result 1" {
		t.Errorf("msgs[0].Content = %q, want 'result 1'", msgs[0].Content)
	}
	if msgs[1].Role != "tool" {
		t.Errorf("msgs[1].Role = %q, want tool", msgs[1].Role)
	}
	if msgs[1].ToolCallID != "call_2" {
		t.Errorf("msgs[1].ToolCallID = %q, want call_2", msgs[1].ToolCallID)
	}
}

func TestConvertToOpenAIMessages_PlainString(t *testing.T) {
	content, _ := json.Marshal("plain message")
	msgs := convertToOpenAIMessages(Message{Role: "user", Content: content})
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
	if msgs[0].Content != "plain message" {
		t.Errorf("Content = %q, want 'plain message'", msgs[0].Content)
	}
}

func TestOpenAIProvider_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`service unavailable`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		name:         "test",
		baseURL:      srv.URL,
		apiKey:       "key",
		defaultModel: "gpt-4o",
	}

	result, err := p.Execute(context.Background(), ProviderRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
	if !strings.Contains(result.Error, "503") {
		t.Errorf("error should contain 503, got: %s", result.Error)
	}
}

func TestOpenAIProvider_MultipleToolCallsStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			// First tool call.
			`{"id":"chatcmpl-m2","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-m2","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"/a\"}"}}]},"finish_reason":null}]}`,
			// Second tool call.
			`{"id":"chatcmpl-m2","choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-m2","choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"path\":\"/b\"}"}}]},"finish_reason":null}]}`,
			// Done.
			`{"id":"chatcmpl-m2","choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		}

		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		name:         "test",
		baseURL:      srv.URL,
		apiKey:       "key",
		defaultModel: "gpt-4o",
	}

	eventCh := make(chan SSEEvent, 100)
	result, err := p.Execute(context.Background(), ProviderRequest{
		Prompt:  "read both",
		EventCh: eventCh,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if result.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", result.StopReason)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_a" {
		t.Errorf("ToolCalls[0].ID = %q, want call_a", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want read_file", result.ToolCalls[0].Name)
	}
	if string(result.ToolCalls[0].Input) != `{"path":"/a"}` {
		t.Errorf("ToolCalls[0].Input = %q, want %q", string(result.ToolCalls[0].Input), `{"path":"/a"}`)
	}
	if result.ToolCalls[1].ID != "call_b" {
		t.Errorf("ToolCalls[1].ID = %q, want call_b", result.ToolCalls[1].ID)
	}
	if string(result.ToolCalls[1].Input) != `{"path":"/b"}` {
		t.Errorf("ToolCalls[1].Input = %q, want %q", string(result.ToolCalls[1].Input), `{"path":"/b"}`)
	}
}
