package discord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tetora/internal/log"
)

const (
	GatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"
	APIBase    = "https://discord.com/api/v10"

	// Gateway opcodes.
	OpDispatch       = 0
	OpHeartbeat      = 1
	OpIdentify       = 2
	OpResume         = 6
	OpReconnect      = 7
	OpInvalidSession = 9
	OpHello          = 10
	OpHeartbeatAck   = 11

	// Gateway intents.
	IntentGuildMessages  = 1 << 9
	IntentDirectMessages = 1 << 12
	IntentMessageContent = 1 << 15
)

// Client wraps Discord REST API calls.
type Client struct {
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a Client with sensible defaults.
func NewClient(token string) *Client {
	return &Client{
		Token:      token,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Post sends a POST request to the Discord API (no response body).
// Retries up to 3 times on network errors, 429 rate-limit, or 5xx server errors.
// Errors are only logged — callers that need to distinguish success vs failure
// should use postWithError.
func (c *Client) Post(path string, payload any) {
	if err := c.postWithError(path, payload); err != nil {
		log.Error("discord api post failed", "path", path, "error", err)
	}
}

// postWithError is Post's inner implementation; returns the final error so
// callers can distinguish success from retried-then-given-up failures.
func (c *Client) postWithError(path string, payload any) error {
	const maxAttempts = 3
	body, _ := json.Marshal(payload)
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		req, err := http.NewRequest("POST", APIBase+path, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bot "+c.Token)
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			log.Warn("discord api send failed, retrying", "attempt", attempt+1, "error", err)
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			delay := time.Duration(attempt+1) * 500 * time.Millisecond
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				var secs float64
				if _, err := fmt.Sscanf(ra, "%f", &secs); err == nil {
					delay = time.Duration(secs*float64(time.Second)) + 100*time.Millisecond
				}
			}
			log.Warn("discord rate limited, retrying", "attempt", attempt+1, "delay", delay)
			time.Sleep(delay)
			lastErr = fmt.Errorf("rate limited: %s", string(b))
			continue
		}
		if resp.StatusCode >= 500 {
			log.Warn("discord api server error, retrying", "attempt", attempt+1, "status", resp.StatusCode, "body", string(b))
			lastErr = fmt.Errorf("server %d: %s", resp.StatusCode, string(b))
			continue
		}
		if resp.StatusCode >= 400 {
			// 4xx errors (except 429) are not retried.
			return fmt.Errorf("client %d: %s", resp.StatusCode, string(b))
		}
		return nil // success
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("exhausted %d attempts", maxAttempts)
	}
	return lastErr
}

// Request sends a Discord API request and returns the response body.
func (c *Client) Request(method, path string, payload any) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		body, _ := json.Marshal(payload)
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, APIBase+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+c.Token)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 400 {
		return respBody, fmt.Errorf("discord api %s %s: %d %s", method, path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// RequestRaw sends a Discord API request and returns status code + body.
// Unlike Request, it does not return an error for non-2xx status codes.
func (c *Client) RequestRaw(method, path string, payload any) (int, []byte) {
	if c == nil || c.HTTPClient == nil {
		return 0, nil
	}
	var bodyStr string
	if payload != nil {
		b, _ := json.Marshal(payload)
		bodyStr = string(b)
	}
	reqBody := strings.NewReader(bodyStr)
	req, err := http.NewRequest(method, APIBase+path, reqBody)
	if err != nil {
		log.Error("discord api request error", "method", method, "path", path, "error", err)
		return 0, nil
	}
	if bodyStr != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bot "+c.Token)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		log.Error("discord api send failed", "method", method, "path", path, "error", err)
		return 0, nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode >= 400 {
		log.Warn("discord api error", "method", method, "path", path,
			"status", resp.StatusCode, "body", string(respBody))
	}
	return resp.StatusCode, respBody
}

// --- Message Helpers ---

// SendMessage sends a text message to a channel.
func (c *Client) SendMessage(channelID, content string) {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	c.Post(fmt.Sprintf("/channels/%s/messages", channelID), map[string]string{"content": content})
}

// SendLongMessage sends content that may exceed Discord's 2000-byte limit by
// splitting at natural boundaries (paragraph > line > word) and posting each
// chunk as a separate message prefixed with `(i/N) `. Runes are never broken
// mid-sequence. For content under the limit, a single unadorned message is
// posted. Returns the first chunk send error (if any) so callers can gate
// downstream side-effects (e.g. dedup marking) on successful delivery.
func (c *Client) SendLongMessage(channelID, content string) error {
	if content == "" {
		return nil
	}
	chunks := splitForDiscord(content, 1990)
	path := fmt.Sprintf("/channels/%s/messages", channelID)
	for i, chunk := range chunks {
		msg := chunk
		if len(chunks) > 1 {
			msg = fmt.Sprintf("(%d/%d) %s", i+1, len(chunks), chunk)
		}
		if err := c.postWithError(path, map[string]string{"content": msg}); err != nil {
			return fmt.Errorf("chunk %d/%d: %w", i+1, len(chunks), err)
		}
		if i < len(chunks)-1 {
			time.Sleep(400 * time.Millisecond)
		}
	}
	return nil
}

// SendEmbed sends an embed message to a channel.
func (c *Client) SendEmbed(channelID string, embed Embed) {
	c.Post(fmt.Sprintf("/channels/%s/messages", channelID), map[string]any{"embeds": []Embed{embed}})
}

// SendEmbedReply sends an embed as a reply to a specific message.
func (c *Client) SendEmbedReply(channelID, replyToID string, embed Embed) {
	payload := map[string]any{"embeds": []Embed{embed}}
	if replyToID != "" {
		payload["message_reference"] = MessageRef{MessageID: replyToID, FailIfNotExists: false}
	}
	path := fmt.Sprintf("/channels/%s/messages", channelID)
	if replyToID == "" {
		c.Post(path, payload)
		return
	}
	// Use RequestRaw so we can detect REPLIES_CANNOT_REPLY_TO_SYSTEM_MESSAGE (50035)
	// and fall back to sending without a reply reference.
	statusCode, body := c.RequestRaw("POST", path, payload)
	if statusCode >= 200 && statusCode < 300 {
		return
	}
	if statusCode == http.StatusBadRequest && strings.Contains(string(body), "50035") {
		// Discord system message (e.g. thread-creation message) — retry without reply ref.
		log.Debug("discord embed reply: falling back to plain send (system message)", "channel", channelID)
		delete(payload, "message_reference")
		c.Post(path, payload)
		return
	}
	// For other errors, log and give up (consistent with Post behaviour).
	log.Warn("discord api error", "status", statusCode, "body", string(body))
}

// SendTyping sends a typing indicator to a channel.
func (c *Client) SendTyping(channelID string) {
	url := APIBase + fmt.Sprintf("/channels/%s/typing", channelID)
	req, _ := http.NewRequest("POST", url, nil)
	if req != nil {
		req.Header.Set("Authorization", "Bot "+c.Token)
		c.HTTPClient.Do(req)
	}
}

// SendMessageReturningID sends a message and returns its ID.
func (c *Client) SendMessageReturningID(channelID, content string) (string, error) {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	body, err := c.Request("POST",
		fmt.Sprintf("/channels/%s/messages", channelID),
		map[string]string{"content": content})
	if err != nil {
		return "", err
	}
	var msg struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return "", err
	}
	return msg.ID, nil
}

// EditMessage edits an existing message's text content.
func (c *Client) EditMessage(channelID, messageID, content string) error {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	_, err := c.Request("PATCH",
		fmt.Sprintf("/channels/%s/messages/%s", channelID, messageID),
		map[string]string{"content": content})
	return err
}

// EditMessageWithComponents edits a message with new content and components.
func (c *Client) EditMessageWithComponents(channelID, messageID, content string, components []Component) error {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	payload := map[string]any{"content": content}
	if components != nil {
		payload["components"] = components
	} else {
		payload["components"] = []Component{}
	}
	_, err := c.Request("PATCH",
		fmt.Sprintf("/channels/%s/messages/%s", channelID, messageID), payload)
	return err
}

// DeleteMessage deletes a message.
func (c *Client) DeleteMessage(channelID, messageID string) {
	_, err := c.Request("DELETE",
		fmt.Sprintf("/channels/%s/messages/%s", channelID, messageID), nil)
	if err != nil {
		log.Warn("discord delete message failed", "error", err)
	}
}

// SendMessageWithComponents sends a message with interactive components.
func (c *Client) SendMessageWithComponents(channelID, content string, components []Component) {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	c.Post(fmt.Sprintf("/channels/%s/messages", channelID), map[string]any{
		"content":    content,
		"components": components,
	})
}

// SendMessageWithComponentsReturningID sends a message with components and returns its ID.
func (c *Client) SendMessageWithComponentsReturningID(channelID, content string, components []Component) (string, error) {
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}
	body, err := c.Request("POST",
		fmt.Sprintf("/channels/%s/messages", channelID),
		map[string]any{"content": content, "components": components})
	if err != nil {
		return "", err
	}
	var msg struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return "", err
	}
	return msg.ID, nil
}

// SendEmbedWithComponents sends an embed message with interactive components.
func (c *Client) SendEmbedWithComponents(channelID string, embed Embed, components []Component) {
	c.Post(fmt.Sprintf("/channels/%s/messages", channelID), map[string]any{
		"embeds":     []Embed{embed},
		"components": components,
	})
}

// --- Utility Functions ---

// IsMentioned checks if the given bot user ID is in the mentions list.
func IsMentioned(mentions []User, botID string) bool {
	for _, u := range mentions {
		if u.ID == botID {
			return true
		}
	}
	return false
}

// StripMention removes the bot mention from message content.
func StripMention(content, botID string) string {
	content = strings.ReplaceAll(content, "<@"+botID+">", "")
	content = strings.ReplaceAll(content, "<@!"+botID+">", "")
	return strings.TrimSpace(content)
}
