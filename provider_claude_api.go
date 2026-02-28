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

// claudeHTTPClient is a dedicated HTTP client for Claude API calls.
// Using a dedicated client avoids sharing state with http.DefaultClient
// and allows us to set transport-level timeouts independently of ctx.
//
// Timeout layering:
//   - ResponseHeaderTimeout (90s): transport-level guard; ensures the server sends
//     response headers within 90s. This fires before any streaming body is read.
//     It is intentionally shorter than the caller ctx deadline so we don't wait
//     for a hung server beyond 90s waiting for headers alone.
//   - firstTokenTimeout (60s default): application-level guard applied in handleStreaming;
//     waits for the first SSE event after headers arrive. Since headers must arrive
//     within 90s and then the first event within 60s, the worst-case latency before
//     we give up is 90s + 60s = 150s. The ctx deadline set by the caller provides the
//     overall ceiling for the entire request lifetime.
//
// Interaction note: ResponseHeaderTimeout fires independently of ctx. If ctx is
// cancelled before ResponseHeaderTimeout, the transport respects ctx.Done first.
// If ResponseHeaderTimeout fires first, claudeHTTPClient.Do returns an error that
// wraps net.Error with Timeout() == true, which isTransientError() treats as transient.
var claudeHTTPClient = &http.Client{
	Transport: &http.Transport{
		ResponseHeaderTimeout: 90 * time.Second, // header must arrive within 90s (ctx handles body)
	},
}

// ClaudeAPIProvider executes tasks using Anthropic Messages API directly.
type ClaudeAPIProvider struct {
	name              string
	apiKey            string
	model             string
	maxTokens         int
	baseURL           string
	cfg               *Config
	firstTokenTimeout time.Duration // 0 means use the package-level default
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
	return p.executeInternal(ctx, req)
}

// ExecuteWithTools implements ToolCapableProvider for multi-turn tool conversations.
func (p *ClaudeAPIProvider) ExecuteWithTools(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	return p.executeInternal(ctx, req)
}

// executeInternal is the shared implementation for Execute and ExecuteWithTools.
// It builds the message list from req.Prompt and req.Messages, includes tools,
// and dispatches the HTTP call.
func (p *ClaudeAPIProvider) executeInternal(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
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

	// Build messages: initial prompt + multi-turn conversation history.
	var messages []claudeMessage
	if len(req.Messages) == 0 {
		// Simple single-turn: just the user prompt.
		messages = []claudeMessage{
			{Role: "user", Content: req.Prompt},
		}
	} else {
		// Multi-turn: user prompt first, then accumulated tool conversation messages.
		messages = []claudeMessage{
			{Role: "user", Content: req.Prompt},
		}
		for _, m := range req.Messages {
			content := convertMessageContent(m.Content)
			messages = append(messages, claudeMessage{Role: m.Role, Content: content})
		}
	}

	// Build tools array if provided.
	var tools []any
	if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			var schema map[string]any
			if len(t.InputSchema) > 0 {
				if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
					return nil, fmt.Errorf("unmarshal tool input schema: %w", err)
				}
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
	resp, err := claudeHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Limit error body read to 10KB to prevent OOM on malformed/large responses.
		// This executes on the goroutine that called claudeHTTPClient.Do, so there is
		// no concurrent access to resp.Body — safe without additional synchronisation.
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 10*1024))
		if readErr != nil {
			logWarn("claude-api: failed to read error body", "status", resp.StatusCode, "error", readErr)
		}
		return errResult("HTTP %d: %s", resp.StatusCode, string(bodyBytes)), nil
	}

	// Handle streaming vs non-streaming.
	if req.EventCh != nil {
		streamCtx, streamCancel := context.WithCancel(ctx)
		defer streamCancel()
		return p.handleStreaming(streamCtx, streamCancel, resp, req.EventCh, start, model)
	}

	return p.handleNonStreaming(ctx, resp, start, model)
}

// convertMessageContent converts json.RawMessage content to a suitable type
// for the Claude API. It tries to parse as an array of content blocks first,
// then falls back to a plain string.
func convertMessageContent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return ""
	}

	// Try as array of content blocks (tool_use, tool_result, text blocks).
	var blocks []claudeContent
	if err := json.Unmarshal(raw, &blocks); err == nil && len(blocks) > 0 {
		return blocks
	}

	// Try as plain string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Fallback: use raw bytes as string.
	return string(raw)
}

// handleNonStreaming processes a non-streaming response.
func (p *ClaudeAPIProvider) handleNonStreaming(ctx context.Context, resp *http.Response, start time.Time, model string) (*ProviderResult, error) {
	var apiResp claudeAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if apiResp.Error != nil {
		return errResult("%s: %s", apiResp.Error.Type, apiResp.Error.Message), nil
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
		StopReason: apiResp.StopReason,
	}, nil
}

// defaultFirstTokenTimeout is the fallback used when ProviderConfig.FirstTokenTimeout
// is not set. Normal Anthropic API sends message_start within 2-10s; 60s is very
// conservative and gives enough headroom for occasionally slow API responses.
const defaultFirstTokenTimeout = 60 * time.Second

// resolveFirstTokenTimeout returns the provider-level override if configured,
// otherwise the package default.
func (p *ClaudeAPIProvider) resolveFirstTokenTimeout() time.Duration {
	if p.firstTokenTimeout > 0 {
		return p.firstTokenTimeout
	}
	return defaultFirstTokenTimeout
}

// streamLine is a result from the scanner goroutine.
type streamLine struct {
	line string
	err  error
	done bool
}

// handleStreaming processes a streaming SSE response.
//
// Caller contract:
//   - cancel must be provided; handleStreaming calls cancel() on first-token timeout
//     so that the body-closer goroutine's ctx.Done fires and resp.Body.Close() is
//     called, which unblocks the blocked scanner.Scan() immediately.
//   - handleStreaming also calls cancel() internally (via defer) so that the
//     body-closer goroutine always exits — even on normal completion where ctx is
//     never cancelled by the outer caller.
//   - eventCh sends are guarded by a ctx-aware select, so cancelling ctx (from any
//     source) causes handleStreaming to return before the caller closes eventCh.
//     The caller must not close eventCh until handleStreaming (and therefore
//     executeWithProvider) returns.
func (p *ClaudeAPIProvider) handleStreaming(ctx context.Context, cancel context.CancelFunc, resp *http.Response, eventCh chan<- SSEEvent, start time.Time, model string) (*ProviderResult, error) {
	// Always cancel the child context on return so the body-closer goroutine exits
	// promptly on the normal completion path (where the outer ctx is never cancelled).
	defer cancel()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 1MB max line

	// Run scanner in a goroutine so we can apply a first-event timeout via select.
	// scanner.Scan() is blocking I/O; a ctx check before Scan() is not sufficient.
	// We close resp.Body when ctx is cancelled to force Scan() to return immediately.
	//
	// The inner goroutine that closes resp.Body uses a separate bodyCloseDone channel
	// so it can exit as soon as the outer scanner goroutine finishes — even when ctx
	// is never cancelled (normal completion path).  Without this, the inner goroutine
	// would leak until the caller's ctx is eventually cancelled.
	lineCh := make(chan streamLine, 32)
	go func() {
		// scannerDone is closed when the scanner goroutine exits, allowing the
		// body-closer goroutine to exit on the normal (non-cancel) path as well.
		// Declare it before the inner goroutine so both goroutines share the same
		// channel; defer order is LIFO so scannerDone is closed after lineCh.
		scannerDone := make(chan struct{})
		defer close(scannerDone)
		defer close(lineCh)

		// Close the response body when ctx is cancelled so that scanner.Scan()
		// unblocks immediately instead of waiting for the next read deadline.
		// scannerDone lets the inner goroutine exit on the normal (non-cancel) path
		// so it never leaks — even when ctx is a long-lived parent context.
		go func() {
			select {
			case <-ctx.Done():
				resp.Body.Close()
			case <-scannerDone:
				// Scanner finished (EOF or early exit); resp.Body will be closed by
				// the defer in executeInternal. Nothing more to do here.
			}
		}()

		for scanner.Scan() {
			select {
			case lineCh <- streamLine{line: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}

		// scanner.Err() after ctx cancel returns a "use of closed network connection"
		// error because resp.Body.Close() was called by the body-closer goroutine above.
		// That error is expected; log it only when ctx is still alive (genuine error).
		scanErr := scanner.Err()
		if scanErr != nil && ctx.Err() == nil {
			logWarn("claude-api: scanner error", "error", scanErr)
		}

		// Send done sentinel before defer close(lineCh) runs, so the receiver
		// always sees a done=true entry rather than a closed channel on natural EOF.
		// Carry the scanner error only when ctx is still alive; on ctx cancel the
		// error is an artefact of resp.Body.Close() and should not surface to the caller.
		var sentinelErr error
		if ctx.Err() == nil {
			sentinelErr = scanErr
		}
		select {
		case lineCh <- streamLine{err: sentinelErr, done: true}:
		case <-ctx.Done():
		}
	}()

	var textParts []string
	var toolCalls []ToolCall
	var inputTokens, outputTokens int
	var currentToolCall ToolCall
	var hasCurrentToolCall bool
	var toolInputBuffer strings.Builder
	var stopReason string

	// firstEventTimer guards against Anthropic API hanging after headers arrive.
	// We manage Stop/drain explicitly on every return path rather than via defer
	// to avoid the double-stop/double-drain confusion that arises when both a
	// defer Stop and an inline Stop+drain coexist.
	firstEventTimer := time.NewTimer(p.resolveFirstTokenTimeout())
	// stopFirstEventTimer safely stops the timer and drains its channel if needed.
	// Calling this multiple times is safe; subsequent calls are no-ops because the
	// timer is already stopped and the channel is empty.
	stopFirstEventTimer := func() {
		if !firstEventTimer.Stop() {
			select {
			case <-firstEventTimer.C:
			default:
			}
		}
	}
	firstEventSeen := false

	for {
		var sl streamLine
		var ok bool
		if !firstEventSeen {
			// Before the first SSE event: apply first-token timeout.
			select {
			case sl, ok = <-lineCh:
				if !ok {
					// goroutine exited early (ctx cancel or unexpected close)
					stopFirstEventTimer()
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					return nil, fmt.Errorf("stream closed unexpectedly")
				}
			case <-firstEventTimer.C:
				// Timer already fired; channel is now empty — no drain needed.
				// cancel() causes the body-closer goroutine's ctx.Done to fire,
				// which closes resp.Body and unblocks the scanner immediately.
				// (defer cancel() at function entry also guarantees this on all
				// other return paths.)
				return nil, fmt.Errorf("first token timeout: Anthropic API did not respond within %v", p.resolveFirstTokenTimeout())
			case <-ctx.Done():
				stopFirstEventTimer()
				return nil, ctx.Err()
			}
		} else {
			select {
			case sl, ok = <-lineCh:
				if !ok {
					// goroutine exited early (ctx cancel or unexpected close)
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					return nil, fmt.Errorf("stream closed unexpectedly")
				}
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		if sl.done {
			if sl.err != nil {
				return nil, fmt.Errorf("read stream: %w", sl.err)
			}
			break
		}

		line := sl.line
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		// We received the first real SSE event — API is alive, stop the first-token
		// timer. Stop() + drain is the correct pattern: if Stop() returns false the
		// timer already fired and its channel holds a value that we must drain to
		// prevent a spurious select in a subsequent iteration.
		if !firstEventSeen {
			firstEventSeen = true
			stopFirstEventTimer()
		}

		var chunk claudeStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			logWarn("claude-api: failed to parse chunk", "error", err, "data", data)
			continue
		}

		switch chunk.Type {
		case "message_start":
			if chunk.Message != nil {
				inputTokens = chunk.Message.Usage.InputTokens
			}

		case "content_block_start":
			if chunk.ContentBlock != nil && chunk.ContentBlock.Type == "tool_use" {
				// If a previous tool call was still open (nested blocks), flush it first
				// so we don't lose its accumulated input.
				if hasCurrentToolCall {
					currentToolCall.Input = json.RawMessage(toolInputBuffer.String())
					toolCalls = append(toolCalls, currentToolCall)
					hasCurrentToolCall = false
				}
				// Start new tool call.
				currentToolCall = ToolCall{
					ID:   chunk.ContentBlock.ID,
					Name: chunk.ContentBlock.Name,
				}
				hasCurrentToolCall = true
				toolInputBuffer.Reset()
			}

		case "content_block_delta":
			if chunk.Delta != nil {
				if chunk.Delta.Type == "text_delta" && chunk.Delta.Text != "" {
					textParts = append(textParts, chunk.Delta.Text)
					// Publish chunk event.
					// Guard with ctx.Done() so we never send to eventCh after the
					// caller has cancelled ctx (and potentially closed eventCh).
					// recover() is intentionally absent: the select ensures we only
					// send when ctx is still live, eliminating the send-vs-close race.
					if eventCh != nil {
						select {
						case eventCh <- SSEEvent{
							Type: "output_chunk",
							Data: map[string]any{
								"text": chunk.Delta.Text,
							},
						}:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
				} else if chunk.Delta.Type == "input_json_delta" && chunk.Delta.PartialJSON != "" {
					// Tool input accumulation.
					toolInputBuffer.WriteString(chunk.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			// Content block finished.
			if hasCurrentToolCall {
				// Finalize tool call input.
				currentToolCall.Input = json.RawMessage(toolInputBuffer.String())
				toolCalls = append(toolCalls, currentToolCall)
				hasCurrentToolCall = false
			}

		case "message_delta":
			if chunk.Delta != nil && chunk.Delta.StopReason != "" {
				stopReason = chunk.Delta.StopReason
				logDebug("claude-api: stop reason", "reason", chunk.Delta.StopReason)
			}
			if chunk.Message != nil {
				outputTokens = chunk.Message.Usage.OutputTokens
			}

		case "message_stop":
			// Stream finished.
		}
	}

	output := strings.Join(textParts, "\n")
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
		StopReason: stopReason,
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
