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

// openAI request/response types.
type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
}

type openAIResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
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
			Content string `json:"content"`
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

	body := openAIRequest{
		Model:    model,
		Messages: messages,
		Stream:   req.EventCh != nil,
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

// readStreamResponse reads an OpenAI SSE stream response and emits chunks.
func (p *OpenAIProvider) readStreamResponse(body io.Reader, req ProviderRequest, start time.Time) *ProviderResult {
	scanner := bufio.NewScanner(body)
	var fullContent strings.Builder
	var sessionID string
	var tokensIn, tokensOut int

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
			delta := chunk.Choices[0].Delta.Content
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
		}
	}

	elapsed := time.Since(start)
	result := &ProviderResult{
		Output:     fullContent.String(),
		DurationMs: elapsed.Milliseconds(),
		SessionID:  sessionID,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		ProviderMs: elapsed.Milliseconds(),
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
		result.Output = resp.Choices[0].Message.Content
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
