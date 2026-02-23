package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider executes tasks using OpenAI-compatible APIs.
// Supports OpenAI, Ollama, LM Studio, vLLM, and any compatible endpoint.
type OpenAIProvider struct {
	name         string
	baseURL      string
	apiKey       string
	defaultModel string
}

func (p *OpenAIProvider) Name() string { return p.name }

// --- OpenAI request/response types ---

// openAIToolCall represents a tool call in an assistant message.
type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openAIToolCallDelta represents a partial tool call in a streaming delta.
type openAIToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

// openAITool represents a tool definition in OpenAI format.
type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

// openAIFunction represents a function definition for OpenAI tools.
type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// openAIMessage represents a message in the conversation.
type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
	Tools    []openAITool    `json:"tools,omitempty"`
}

type openAIResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall  `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// openAIStreamChunk represents a chunk from the SSE stream.
type openAIStreamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Delta struct {
			Content   string                `json:"content"`
			ToolCalls []openAIToolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (p *OpenAIProvider) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	return p.executeInternal(ctx, req)
}

// ExecuteWithTools implements ToolCapableProvider for multi-turn tool conversations.
func (p *OpenAIProvider) ExecuteWithTools(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	return p.executeInternal(ctx, req)
}

// executeInternal is the shared implementation for Execute and ExecuteWithTools.
func (p *OpenAIProvider) executeInternal(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("no model specified for provider %q", p.name)
	}

	// Build messages.
	var messages []openAIMessage
	if req.SystemPrompt != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: req.Prompt})

	// Append multi-turn messages if present (tool conversation history).
	for _, m := range req.Messages {
		converted := convertToOpenAIMessages(m)
		messages = append(messages, converted...)
	}

	// Build tools array if provided.
	var tools []openAITool
	if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			tools = append(tools, openAITool{
				Type: "function",
				Function: openAIFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			})
		}
	}

	body := openAIRequest{
		Model:    model,
		Messages: messages,
		Stream:   req.EventCh != nil,
		Tools:    tools,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		elapsed := time.Since(start)
		return &ProviderResult{
			IsError:    true,
			Error:      fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateBytes(respBody, 500)),
			DurationMs: elapsed.Milliseconds(),
		}, nil
	}

	// Streaming mode.
	if req.EventCh != nil {
		return p.readStreamResponse(resp.Body, req, start), nil
	}

	// Non-streaming mode.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	elapsed := time.Since(start)

	return parseOpenAIResponse(respBody, elapsed.Milliseconds()), nil
}

// convertToOpenAIMessages converts a provider Message to one or more openAIMessages.
// Returns a slice because OpenAI requires each tool result to be a separate message
// with role "tool", whereas Claude bundles them in a single user message.
func convertToOpenAIMessages(m Message) []openAIMessage {
	// Try to parse as array of content blocks (Claude format).
	var blocks []ContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err == nil && len(blocks) > 0 {
		if m.Role == "assistant" {
			// Convert tool_use blocks to OpenAI tool_calls + text content.
			msg := openAIMessage{Role: "assistant"}
			var textParts []string
			for _, b := range blocks {
				switch b.Type {
				case "text":
					textParts = append(textParts, b.Text)
				case "tool_use":
					tc := openAIToolCall{
						ID:   b.ID,
						Type: "function",
					}
					tc.Function.Name = b.Name
					tc.Function.Arguments = string(b.Input)
					msg.ToolCalls = append(msg.ToolCalls, tc)
				}
			}
			msg.Content = strings.Join(textParts, "\n")
			return []openAIMessage{msg}
		}

		if m.Role == "user" {
			// tool_result blocks become individual "tool" role messages in OpenAI format.
			// Each tool result maps to a separate message with role "tool".
			var msgs []openAIMessage
			for _, b := range blocks {
				if b.Type == "tool_result" {
					msgs = append(msgs, openAIMessage{
						Role:       "tool",
						Content:    b.Content,
						ToolCallID: b.ToolUseID,
					})
				}
			}
			if len(msgs) > 0 {
				return msgs
			}
		}
	}

	// Fallback: try as plain string.
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return []openAIMessage{{Role: m.Role, Content: s}}
	}

	// Last resort: raw content as string.
	return []openAIMessage{{Role: m.Role, Content: string(m.Content)}}
}

// readStreamResponse reads an OpenAI SSE stream response and emits chunks.
func (p *OpenAIProvider) readStreamResponse(body io.Reader, req ProviderRequest, start time.Time) *ProviderResult {
	scanner := bufio.NewScanner(body)
	var fullContent strings.Builder
	var sessionID string
	var tokensIn, tokensOut int
	var finishReason string

	// Tool call accumulation for streaming.
	type toolCallAccumulator struct {
		id       string
		name     string
		argsBuf  strings.Builder
	}
	var toolAccumulators []toolCallAccumulator

	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: "data: {...}" or "data: [DONE]"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.ID != "" && sessionID == "" {
			sessionID = chunk.ID
		}

		if chunk.Usage != nil {
			tokensIn = chunk.Usage.PromptTokens
			tokensOut = chunk.Usage.CompletionTokens
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]

			// Capture finish reason.
			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}

			// Text content delta.
			delta := choice.Delta.Content
			if delta != "" {
				fullContent.WriteString(delta)

				if req.EventCh != nil {
					req.EventCh <- SSEEvent{
						Type:      SSEOutputChunk,
						SessionID: req.SessionID,
						Data: map[string]string{
							"chunk": delta,
						},
						Timestamp: time.Now().Format(time.RFC3339),
					}
				}
			}

			// Tool call deltas.
			for _, tcDelta := range choice.Delta.ToolCalls {
				idx := tcDelta.Index
				// Grow accumulators slice if needed.
				for len(toolAccumulators) <= idx {
					toolAccumulators = append(toolAccumulators, toolCallAccumulator{})
				}
				if tcDelta.ID != "" {
					toolAccumulators[idx].id = tcDelta.ID
				}
				if tcDelta.Function.Name != "" {
					toolAccumulators[idx].name = tcDelta.Function.Name
				}
				if tcDelta.Function.Arguments != "" {
					toolAccumulators[idx].argsBuf.WriteString(tcDelta.Function.Arguments)
				}
			}
		}
	}

	elapsed := time.Since(start)

	// Build tool calls from accumulators.
	var toolCalls []ToolCall
	for _, acc := range toolAccumulators {
		if acc.id != "" {
			toolCalls = append(toolCalls, ToolCall{
				ID:    acc.id,
				Name:  acc.name,
				Input: json.RawMessage(acc.argsBuf.String()),
			})
		}
	}

	// Map OpenAI finish_reason to normalized stop reason.
	stopReason := mapOpenAIFinishReason(finishReason)

	result := &ProviderResult{
		Output:     fullContent.String(),
		DurationMs: elapsed.Milliseconds(),
		SessionID:  sessionID,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		ProviderMs: elapsed.Milliseconds(),
		ToolCalls:  toolCalls,
		StopReason: stopReason,
	}

	if tokensIn > 0 || tokensOut > 0 {
		result.CostUSD = estimateOpenAICost(tokensIn, tokensOut)
	}

	return result
}

// parseOpenAIResponse parses an OpenAI-compatible API response.
func parseOpenAIResponse(data []byte, durationMs int64) *ProviderResult {
	var resp openAIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return &ProviderResult{
			IsError:    true,
			Error:      fmt.Sprintf("parse response: %v", err),
			DurationMs: durationMs,
		}
	}

	if resp.Error != nil {
		return &ProviderResult{
			IsError:    true,
			Error:      resp.Error.Message,
			DurationMs: durationMs,
		}
	}

	result := &ProviderResult{
		DurationMs: durationMs,
		SessionID:  resp.ID,
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		result.Output = choice.Message.Content

		// Extract tool calls.
		for _, tc := range choice.Message.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}

		// Map finish_reason to normalized stop reason.
		result.StopReason = mapOpenAIFinishReason(choice.FinishReason)
	}

	// Extract token usage and estimate cost if available.
	if resp.Usage != nil {
		result.TokensIn = resp.Usage.PromptTokens
		result.TokensOut = resp.Usage.CompletionTokens
		result.CostUSD = estimateOpenAICost(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}

	// OpenAI API doesn't report server-side latency separately; use wall-clock.
	result.ProviderMs = durationMs

	return result
}

// mapOpenAIFinishReason converts OpenAI finish_reason to the normalized stop reason
// used by the agentic loop ("end_turn", "tool_use").
func mapOpenAIFinishReason(reason string) string {
	switch reason {
	case "tool_calls":
		return "tool_use"
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "content_filter"
	default:
		if reason != "" {
			return reason
		}
		return ""
	}
}

// estimateOpenAICost provides a rough cost estimate based on token counts.
// Uses approximate GPT-4o pricing as a default baseline.
func estimateOpenAICost(promptTokens, completionTokens int) float64 {
	// GPT-4o approximate: $2.50/M input, $10.00/M output
	inputCost := float64(promptTokens) * 2.50 / 1_000_000
	outputCost := float64(completionTokens) * 10.00 / 1_000_000
	return inputCost + outputCost
}

func truncateBytes(b []byte, maxLen int) string {
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
