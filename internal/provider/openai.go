package provider

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
	Name_        string
	BaseURL      string
	APIKey       string
	DefaultModel string
	IsLocal      bool // true for localhost endpoints (Ollama, LM Studio) — cost is $0
}

func (p *OpenAIProvider) Name() string { return p.Name_ }

// --- OpenAI request/response types ---

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

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
			ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
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

func (p *OpenAIProvider) Execute(ctx context.Context, req Request) (*Result, error) {
	return p.executeInternal(ctx, req)
}

// ExecuteWithTools implements ToolCapableProvider for multi-turn tool conversations.
func (p *OpenAIProvider) ExecuteWithTools(ctx context.Context, req Request) (*Result, error) {
	return p.executeInternal(ctx, req)
}

func (p *OpenAIProvider) executeInternal(ctx context.Context, req Request) (*Result, error) {
	model := req.Model
	if model == "" {
		model = p.DefaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("no model specified for provider %q", p.Name_)
	}

	var messages []openAIMessage
	if req.SystemPrompt != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: req.Prompt})

	for _, m := range req.Messages {
		converted := ConvertToOpenAIMessages(m)
		messages = append(messages, converted...)
	}

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
		Stream:   req.OnEvent != nil,
		Tools:    tools,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := p.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
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
		return &Result{
			IsError:    true,
			Error:      fmt.Sprintf("HTTP %d: %s", resp.StatusCode, TruncateBytes(respBody, 500)),
			DurationMs: elapsed.Milliseconds(),
		}, nil
	}

	var result *Result
	if req.OnEvent != nil {
		result = p.readStreamResponse(resp.Body, req, start)
	} else {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		elapsed := time.Since(start)
		result = ParseOpenAIResponse(respBody, elapsed.Milliseconds())
	}

	// Local endpoints (Ollama, LM Studio) are free — zero out estimated cost.
	if p.IsLocal {
		result.CostUSD = 0
	}

	return result, nil
}

// ConvertToOpenAIMessages converts a provider Message to one or more openAIMessages.
func ConvertToOpenAIMessages(m Message) []openAIMessage {
	var blocks []ContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err == nil && len(blocks) > 0 {
		if m.Role == "assistant" {
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

	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return []openAIMessage{{Role: m.Role, Content: s}}
	}

	return []openAIMessage{{Role: m.Role, Content: string(m.Content)}}
}

func (p *OpenAIProvider) readStreamResponse(body io.Reader, req Request, start time.Time) *Result {
	scanner := bufio.NewScanner(body)
	var fullContent strings.Builder
	var sessionID string
	var tokensIn, tokensOut int
	var finishReason string

	type toolCallAccumulator struct {
		id      string
		name    string
		argsBuf strings.Builder
	}
	var toolAccumulators []toolCallAccumulator

	for scanner.Scan() {
		line := scanner.Text()

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

			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}

			delta := choice.Delta.Content
			if delta != "" {
				fullContent.WriteString(delta)

				if req.OnEvent != nil {
					req.OnEvent(Event{
						Type:      EventOutputChunk,
						SessionID: req.SessionID,
						Data: map[string]string{
							"chunk": delta,
						},
						Timestamp: time.Now().Format(time.RFC3339),
					})
				}
			}

			for _, tcDelta := range choice.Delta.ToolCalls {
				idx := tcDelta.Index
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

	stopReason := MapOpenAIFinishReason(finishReason)

	result := &Result{
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
		result.CostUSD = EstimateOpenAICost(tokensIn, tokensOut)
	}

	return result
}

// ParseOpenAIResponse parses an OpenAI-compatible API response.
func ParseOpenAIResponse(data []byte, durationMs int64) *Result {
	var resp openAIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return &Result{
			IsError:    true,
			Error:      fmt.Sprintf("parse response: %v", err),
			DurationMs: durationMs,
		}
	}

	if resp.Error != nil {
		return &Result{
			IsError:    true,
			Error:      resp.Error.Message,
			DurationMs: durationMs,
		}
	}

	result := &Result{
		DurationMs: durationMs,
		SessionID:  resp.ID,
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		result.Output = choice.Message.Content

		for _, tc := range choice.Message.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}

		result.StopReason = MapOpenAIFinishReason(choice.FinishReason)
	}

	if resp.Usage != nil {
		result.TokensIn = resp.Usage.PromptTokens
		result.TokensOut = resp.Usage.CompletionTokens
		result.CostUSD = EstimateOpenAICost(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}

	result.ProviderMs = durationMs

	return result
}

// MapOpenAIFinishReason converts OpenAI finish_reason to normalized stop reason.
func MapOpenAIFinishReason(reason string) string {
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

// EstimateOpenAICost provides a rough cost estimate based on token counts.
func EstimateOpenAICost(promptTokens, completionTokens int) float64 {
	inputCost := float64(promptTokens) * 2.50 / 1_000_000
	outputCost := float64(completionTokens) * 10.00 / 1_000_000
	return inputCost + outputCost
}

// IsLocalEndpoint returns true if the base URL points to a local server (Ollama, LM Studio, etc.).
func IsLocalEndpoint(baseURL string) bool {
	return strings.Contains(baseURL, "localhost") || strings.Contains(baseURL, "127.0.0.1")
}

// TruncateBytes truncates bytes to max length as string.
func TruncateBytes(b []byte, maxLen int) string {
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
