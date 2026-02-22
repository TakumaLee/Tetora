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

// ClaudeAPIProvider executes tasks using Anthropic Messages API directly.
type ClaudeAPIProvider struct {
	name      string
	apiKey    string
	model     string
	maxTokens int
	baseURL   string
	cfg       *Config
}

func (p *ClaudeAPIProvider) Name() string { return p.name }

// --- Claude API Request/Response Types ---

// claudeMessage represents a message in the conversation.
type claudeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []claudeContent
}

// claudeContent represents a content block in a message.
type claudeContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// claudeAPIRequest is the request body for /v1/messages.
type claudeAPIRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	Messages    []claudeMessage `json:"messages"`
	System      string          `json:"system,omitempty"`
	Tools       []any           `json:"tools,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
}

// claudeAPIResponse is the response body from /v1/messages.
type claudeAPIResponse struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Content      []claudeContent `json:"content"`
	Model        string          `json:"model"`
	StopReason   string          `json:"stop_reason"`
	StopSequence string          `json:"stop_sequence,omitempty"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// claudeStreamChunk represents a chunk from the SSE stream.
type claudeStreamChunk struct {
	Type         string `json:"type"`
	Index        int    `json:"index,omitempty"`
	Delta        *struct {
		Type        string          `json:"type"`
		Text        string          `json:"text,omitempty"`
		StopReason  string          `json:"stop_reason,omitempty"`
		PartialJSON string          `json:"partial_json,omitempty"`
	} `json:"delta,omitempty"`
	ContentBlock *claudeContent `json:"content_block,omitempty"`
	Message      *struct {
		ID    string `json:"id"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message,omitempty"`
}

// --- Provider Execute ---

func (p *ClaudeAPIProvider) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	if model == "" {
		model = "claude-sonnet-4-5-20250929"
	}

	maxTokens := p.maxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}

	// Build messages from prompt.
	messages := []claudeMessage{
		{Role: "user", Content: req.Prompt},
	}

	// Build tools array if provided.
	var tools []any
	if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			var schema map[string]any
			if len(t.InputSchema) > 0 {
				json.Unmarshal(t.InputSchema, &schema)
			}
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": schema,
			})
		}
	}

	body := claudeAPIRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  messages,
		System:    req.SystemPrompt,
		Tools:     tools,
		Stream:    req.EventCh != nil,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := p.baseURL + "/messages"

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	start := time.Now()
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return &ProviderResult{
			IsError: true,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(bodyBytes)),
		}, nil
	}

	// Handle streaming vs non-streaming.
	if req.EventCh != nil {
		return p.handleStreaming(ctx, resp, req.EventCh, start, model)
	}

	return p.handleNonStreaming(ctx, resp, start, model)
}

// handleNonStreaming processes a non-streaming response.
func (p *ClaudeAPIProvider) handleNonStreaming(ctx context.Context, resp *http.Response, start time.Time, model string) (*ProviderResult, error) {
	var apiResp claudeAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if apiResp.Error != nil {
		return &ProviderResult{
			IsError: true,
			Error:   fmt.Sprintf("%s: %s", apiResp.Error.Type, apiResp.Error.Message),
		}, nil
	}

	// Extract text and tool calls.
	var textParts []string
	var toolCalls []ToolCall
	for _, c := range apiResp.Content {
		if c.Type == "text" {
			textParts = append(textParts, c.Text)
		} else if c.Type == "tool_use" {
			toolCalls = append(toolCalls, ToolCall{
				ID:    c.ID,
				Name:  c.Name,
				Input: c.Input,
			})
		}
	}

	output := strings.Join(textParts, "\n")
	elapsed := time.Since(start)

	// Calculate cost.
	costUSD := p.calculateCost(model, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens)

	return &ProviderResult{
		Output:     output,
		CostUSD:    costUSD,
		DurationMs: elapsed.Milliseconds(),
		TokensIn:   apiResp.Usage.InputTokens,
		TokensOut:  apiResp.Usage.OutputTokens,
		ToolCalls:  toolCalls,
	}, nil
}

// handleStreaming processes a streaming SSE response.
func (p *ClaudeAPIProvider) handleStreaming(ctx context.Context, resp *http.Response, eventCh chan<- SSEEvent, start time.Time, model string) (*ProviderResult, error) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 1MB max line

	var textParts []string
	var toolCalls []ToolCall
	var inputTokens, outputTokens int
	var currentToolCall *ToolCall
	var toolInputBuffer strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk claudeStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			logDebug("claude-api: failed to parse chunk", "error", err, "data", data)
			continue
		}

		switch chunk.Type {
		case "message_start":
			if chunk.Message != nil {
				inputTokens = chunk.Message.Usage.InputTokens
			}

		case "content_block_start":
			if chunk.ContentBlock != nil && chunk.ContentBlock.Type == "tool_use" {
				// Tool use started.
				currentToolCall = &ToolCall{
					ID:   chunk.ContentBlock.ID,
					Name: chunk.ContentBlock.Name,
				}
				toolInputBuffer.Reset()
			}

		case "content_block_delta":
			if chunk.Delta != nil {
				if chunk.Delta.Type == "text_delta" && chunk.Delta.Text != "" {
					textParts = append(textParts, chunk.Delta.Text)
					// Publish chunk event.
					if eventCh != nil {
						eventCh <- SSEEvent{
							Type: "output_chunk",
							Data: map[string]any{
								"text": chunk.Delta.Text,
							},
						}
					}
				} else if chunk.Delta.Type == "input_json_delta" && chunk.Delta.PartialJSON != "" {
					// Tool input accumulation.
					toolInputBuffer.WriteString(chunk.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			// Content block finished.
			if currentToolCall != nil {
				// Finalize tool call input.
				currentToolCall.Input = json.RawMessage(toolInputBuffer.String())
				toolCalls = append(toolCalls, *currentToolCall)
				currentToolCall = nil
			}

		case "message_delta":
			if chunk.Delta != nil && chunk.Delta.StopReason != "" {
				logDebug("claude-api: stop reason", "reason", chunk.Delta.StopReason)
			}
			if chunk.Message != nil {
				outputTokens = chunk.Message.Usage.OutputTokens
			}

		case "message_stop":
			// Stream finished.
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	output := strings.Join(textParts, "")
	elapsed := time.Since(start)

	// Calculate cost.
	costUSD := p.calculateCost(model, inputTokens, outputTokens)

	return &ProviderResult{
		Output:     output,
		CostUSD:    costUSD,
		DurationMs: elapsed.Milliseconds(),
		TokensIn:   inputTokens,
		TokensOut:  outputTokens,
		ToolCalls:  toolCalls,
	}, nil
}

// calculateCost calculates the cost based on model pricing.
func (p *ClaudeAPIProvider) calculateCost(model string, inputTokens, outputTokens int) float64 {
	// Check config pricing first.
	if p.cfg != nil && p.cfg.Pricing != nil {
		if pricing, ok := p.cfg.Pricing[model]; ok {
			return float64(inputTokens)/1e6*pricing.InputPer1M + float64(outputTokens)/1e6*pricing.OutputPer1M
		}
	}

	// Fallback to hardcoded defaults (as of 2025-02).
	var inputPer1M, outputPer1M float64
	switch {
	case strings.Contains(model, "opus-4"):
		inputPer1M = 15.0
		outputPer1M = 75.0
	case strings.Contains(model, "sonnet-4-5"):
		inputPer1M = 3.0
		outputPer1M = 15.0
	case strings.Contains(model, "haiku-3-5"):
		inputPer1M = 0.25
		outputPer1M = 1.25
	default:
		// Default to sonnet pricing.
		inputPer1M = 3.0
		outputPer1M = 15.0
	}

	return (float64(inputTokens)/1000000)*inputPer1M + (float64(outputTokens)/1000000)*outputPer1M
}
