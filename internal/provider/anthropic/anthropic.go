// Package anthropic implements a provider that calls the native Anthropic Messages API.
// Auth: x-api-key header (NOT Authorization: Bearer).
// Endpoint: POST /messages (NOT /chat/completions).
package anthropic

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

	"tetora/internal/provider"
)

// Version is the Anthropic API version header value.
const Version = "2023-06-01"

// Provider calls the native Anthropic Messages API.
type Provider struct {
	name         string
	baseURL      string
	apiKey       string
	defaultModel string
}

// New creates a new Anthropic provider.
func New(name, baseURL, apiKey, defaultModel string) *Provider {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return &Provider{
		name:         name,
		baseURL:      baseURL,
		apiKey:       apiKey,
		defaultModel: defaultModel,
	}
}

func (p *Provider) Name() string { return p.name }

// --- Anthropic request/response types ---

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []contentBlock
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
	Tools     []tool    `json:"tools,omitempty"`
	Stream    bool      `json:"stream,omitempty"`
}

type response struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Execute implements provider.Provider.
func (p *Provider) Execute(ctx context.Context, req provider.Request) (*provider.Result, error) {
	return p.executeInternal(ctx, req)
}

// ExecuteWithTools implements provider.ToolCapableProvider.
func (p *Provider) ExecuteWithTools(ctx context.Context, req provider.Request) (*provider.Result, error) {
	return p.executeInternal(ctx, req)
}

func (p *Provider) executeInternal(ctx context.Context, req provider.Request) (*provider.Result, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("no model specified for provider %q", p.name)
	}

	messages, err := buildMessages(req)
	if err != nil {
		return nil, fmt.Errorf("build messages: %w", err)
	}

	var tools []tool
	for _, t := range req.Tools {
		if t.DeferLoading {
			continue // exclude deferred tools from the Anthropic request
		}
		tools = append(tools, tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	body := request{
		Model:     model,
		MaxTokens: 8192,
		System:    req.SystemPrompt,
		Messages:  messages,
		Tools:     tools,
		Stream:    req.EventCh != nil,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := p.baseURL + "/messages"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", Version)
	if p.apiKey != "" {
		httpReq.Header.Set("x-api-key", p.apiKey)
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
		return &provider.Result{
			IsError:    true,
			Error:      fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(respBody, 500)),
			DurationMs: elapsed.Milliseconds(),
		}, nil
	}

	if req.EventCh != nil {
		return p.readStream(resp.Body, req, start), nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	elapsed := time.Since(start)

	return parseResponse(respBody, elapsed.Milliseconds()), nil
}

// buildMessages converts provider.Request into Anthropic message format.
func buildMessages(req provider.Request) ([]message, error) {
	var msgs []message

	// Seed with the prompt unless Messages already contains context.
	if req.Prompt != "" {
		msgs = append(msgs, message{Role: "user", Content: req.Prompt})
	}

	for _, m := range req.Messages {
		var blocks []provider.ContentBlock
		if err := json.Unmarshal(m.Content, &blocks); err == nil && len(blocks) > 0 {
			var cb []contentBlock
			for _, b := range blocks {
				switch b.Type {
				case "text":
					cb = append(cb, contentBlock{Type: "text", Text: b.Text})
				case "tool_use":
					cb = append(cb, contentBlock{
						Type:  "tool_use",
						ID:    b.ID,
						Name:  b.Name,
						Input: b.Input,
					})
				case "tool_result":
					cb = append(cb, contentBlock{
						Type:      "tool_result",
						ToolUseID: b.ToolUseID,
						Content:   b.Content,
					})
				}
			}
			msgs = append(msgs, message{Role: m.Role, Content: cb})
			continue
		}

		var s string
		if err := json.Unmarshal(m.Content, &s); err == nil {
			msgs = append(msgs, message{Role: m.Role, Content: s})
			continue
		}

		msgs = append(msgs, message{Role: m.Role, Content: string(m.Content)})
	}

	return msgs, nil
}

// parseResponse parses a non-streaming Anthropic /messages response.
func parseResponse(data []byte, durationMs int64) *provider.Result {
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return &provider.Result{
			IsError:    true,
			Error:      fmt.Sprintf("parse response: %v", err),
			DurationMs: durationMs,
		}
	}

	if resp.Error != nil {
		return &provider.Result{
			IsError:    true,
			Error:      resp.Error.Message,
			DurationMs: durationMs,
		}
	}

	result := &provider.Result{
		DurationMs: durationMs,
		SessionID:  resp.ID,
		StopReason: resp.StopReason,
		ProviderMs: durationMs,
		TokensIn:   resp.Usage.InputTokens,
		TokensOut:  resp.Usage.OutputTokens,
	}

	var textParts []string
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
		case "tool_use":
			result.ToolCalls = append(result.ToolCalls, provider.ToolCall{
				ID:    b.ID,
				Name:  b.Name,
				Input: b.Input,
			})
		}
	}
	result.Output = strings.Join(textParts, "\n")

	if resp.StopReason == "tool_use" {
		result.StopReason = "tool_use"
	}

	return result
}

// --- Streaming ---

// readStream reads Anthropic SSE events and returns a Result.
func (p *Provider) readStream(body io.Reader, req provider.Request, start time.Time) *provider.Result {
	scanner := bufio.NewScanner(body)
	var fullText strings.Builder
	var sessionID string
	var tokensIn, tokensOut int
	var stopReason string

	type toolAccumulator struct {
		id      string
		name    string
		argsBuf strings.Builder
	}
	var toolAccs []toolAccumulator

	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "message_start":
			var ev struct {
				Message struct {
					ID    string `json:"id"`
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				sessionID = ev.Message.ID
				tokensIn = ev.Message.Usage.InputTokens
			}

		case "content_block_start":
			var ev struct {
				Index        int          `json:"index"`
				ContentBlock contentBlock `json:"content_block"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.ContentBlock.Type == "tool_use" {
				for len(toolAccs) <= ev.Index {
					toolAccs = append(toolAccs, toolAccumulator{})
				}
				toolAccs[ev.Index].id = ev.ContentBlock.ID
				toolAccs[ev.Index].name = ev.ContentBlock.Name
			}

		case "content_block_delta":
			var ev struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				switch ev.Delta.Type {
				case "text_delta":
					fullText.WriteString(ev.Delta.Text)
					if req.EventCh != nil {
						req.EventCh <- provider.Event{
							Type:      provider.EventOutputChunk,
							SessionID: req.SessionID,
							Data:      map[string]string{"chunk": ev.Delta.Text},
							Timestamp: time.Now().Format(time.RFC3339),
						}
					}
				case "input_json_delta":
					for len(toolAccs) <= ev.Index {
						toolAccs = append(toolAccs, toolAccumulator{})
					}
					toolAccs[ev.Index].argsBuf.WriteString(ev.Delta.PartialJSON)
				}
			}

		case "message_delta":
			var ev struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				if ev.Delta.StopReason != "" {
					stopReason = ev.Delta.StopReason
				}
				tokensOut = ev.Usage.OutputTokens
			}
		}
	}

	elapsed := time.Since(start)

	var toolCalls []provider.ToolCall
	for _, acc := range toolAccs {
		if acc.id != "" {
			toolCalls = append(toolCalls, provider.ToolCall{
				ID:    acc.id,
				Name:  acc.name,
				Input: json.RawMessage(acc.argsBuf.String()),
			})
		}
	}

	return &provider.Result{
		Output:     fullText.String(),
		DurationMs: elapsed.Milliseconds(),
		ProviderMs: elapsed.Milliseconds(),
		SessionID:  sessionID,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		StopReason: stopReason,
		ToolCalls:  toolCalls,
	}
}

func truncate(b []byte, maxLen int) string {
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
