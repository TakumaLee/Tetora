package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tetora/internal/integration/oauthif"
)

// Config holds Gmail integration settings.
type Config struct {
	Enabled       bool     `json:"enabled"`
	MaxResults    int      `json:"maxResults,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	AutoClassify  bool     `json:"autoClassify,omitempty"`
	DefaultSender string   `json:"defaultSender,omitempty"`
}

// Service provides Gmail API operations via OAuth.
type Service struct {
	cfg   Config
	oauth oauthif.Requester
}

// BaseURL is the Gmail API v1 base URL (overridable for tests).
var BaseURL = "https://gmail.googleapis.com/gmail/v1/users/me"

// New creates a new Gmail Service.
func New(cfg Config, oauth oauthif.Requester) *Service {
	return &Service{cfg: cfg, oauth: oauth}
}

// --- Types ---

// Message represents a full email message.
type Message struct {
	ID       string   `json:"id"`
	ThreadID string   `json:"threadId"`
	Subject  string   `json:"subject"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Date     string   `json:"date"`
	Snippet  string   `json:"snippet"`
	Body     string   `json:"body"`
	Labels   []string `json:"labels"`
}

// MessageSummary is a lightweight message summary.
type MessageSummary struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
	Snippet  string `json:"snippet"`
	Subject  string `json:"subject,omitempty"`
	From     string `json:"from,omitempty"`
	Date     string `json:"date,omitempty"`
}

// --- Helper Functions ---

// Base64URLEncode encodes data using base64url (no padding) as required by Gmail API.
func Base64URLEncode(data []byte) string {
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data)
}

// DecodeBase64URL decodes a base64url-encoded string (with or without padding).
func DecodeBase64URL(s string) (string, error) {
	b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(s)
	if err != nil {
		b, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			b, err = base64.StdEncoding.DecodeString(s)
			if err != nil {
				return "", fmt.Errorf("base64 decode: %w", err)
			}
		}
	}
	return string(b), nil
}

// BuildRFC2822 constructs an RFC 2822 formatted email message.
func BuildRFC2822(from, to, subject, body string, cc, bcc []string) string {
	var sb strings.Builder

	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	sb.WriteString(fmt.Sprintf("From: %s\r\n", from))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if len(cc) > 0 {
		sb.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(cc, ", ")))
	}
	if len(bcc) > 0 {
		sb.WriteString(fmt.Sprintf("Bcc: %s\r\n", strings.Join(bcc, ", ")))
	}
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	sb.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z)))
	sb.WriteString("\r\n")
	sb.WriteString(body)

	return sb.String()
}

// ParsePayload extracts subject, from, to, date, and body text from a Gmail API message payload.
func ParsePayload(payload map[string]any) (subject, from, to, date, body string) {
	if headers, ok := payload["headers"].([]any); ok {
		for _, h := range headers {
			hdr, ok := h.(map[string]any)
			if !ok {
				continue
			}
			name := strings.ToLower(fmt.Sprint(hdr["name"]))
			value := fmt.Sprint(hdr["value"])
			switch name {
			case "subject":
				subject = value
			case "from":
				from = value
			case "to":
				to = value
			case "date":
				date = value
			}
		}
	}

	body = ExtractBody(payload, "text/plain")
	if body == "" {
		htmlBody := ExtractBody(payload, "text/html")
		if htmlBody != "" {
			body = StripHTMLTags(htmlBody)
		}
	}

	return
}

// ExtractBody recursively finds a body part with the given MIME type in a Gmail payload.
func ExtractBody(payload map[string]any, mimeType string) string {
	if mt, ok := payload["mimeType"].(string); ok && mt == mimeType {
		if bodyMap, ok := payload["body"].(map[string]any); ok {
			if data, ok := bodyMap["data"].(string); ok && data != "" {
				decoded, err := DecodeBase64URL(data)
				if err == nil {
					return decoded
				}
			}
		}
	}

	if parts, ok := payload["parts"].([]any); ok {
		for _, p := range parts {
			if part, ok := p.(map[string]any); ok {
				result := ExtractBody(part, mimeType)
				if result != "" {
					return result
				}
			}
		}
	}

	return ""
}

// StripHTMLTags removes HTML tags from a string.
// This is a simple implementation — for the Gmail use case it's sufficient.
func StripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// --- Gmail API Methods ---

// ListMessages lists Gmail messages matching a query string.
func (g *Service) ListMessages(ctx context.Context, query string, maxResults int) ([]MessageSummary, error) {
	return g.searchMessages(ctx, query, maxResults)
}

// SearchMessages searches Gmail messages using advanced Gmail search syntax.
func (g *Service) SearchMessages(ctx context.Context, query string, maxResults int) ([]MessageSummary, error) {
	return g.searchMessages(ctx, query, maxResults)
}

// searchMessages is the shared implementation for ListMessages and SearchMessages.
func (g *Service) searchMessages(ctx context.Context, query string, maxResults int) ([]MessageSummary, error) {
	if g.oauth == nil {
		return nil, fmt.Errorf("oauth manager not initialized")
	}

	if maxResults <= 0 {
		maxResults = g.cfg.MaxResults
		if maxResults <= 0 {
			maxResults = 20
		}
	}

	params := url.Values{}
	if query != "" {
		params.Set("q", query)
	}
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))

	reqURL := fmt.Sprintf("%s/messages?%s", BaseURL, params.Encode())
	resp, err := g.oauth.Request(ctx, "google", http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gmail list messages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gmail API error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var listResp struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
		NextPageToken string `json:"nextPageToken"`
		ResultSizeEst int    `json:"resultSizeEstimate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}

	if len(listResp.Messages) == 0 {
		return []MessageSummary{}, nil
	}

	summaries := make([]MessageSummary, 0, len(listResp.Messages))
	for _, msg := range listResp.Messages {
		summary, err := g.fetchMessageSummary(ctx, msg.ID, msg.ThreadID)
		if err != nil {
			summaries = append(summaries, MessageSummary{
				ID:       msg.ID,
				ThreadID: msg.ThreadID,
			})
			continue
		}
		summaries = append(summaries, *summary)
	}

	return summaries, nil
}

// fetchMessageSummary fetches minimal metadata for a message.
func (g *Service) fetchMessageSummary(ctx context.Context, messageID, threadID string) (*MessageSummary, error) {
	reqURL := fmt.Sprintf("%s/messages/%s?format=metadata&metadataHeaders=Subject&metadataHeaders=From&metadataHeaders=Date",
		BaseURL, url.PathEscape(messageID))

	resp, err := g.oauth.Request(ctx, "google", http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gmail API error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var msgResp struct {
		ID       string         `json:"id"`
		ThreadID string         `json:"threadId"`
		Snippet  string         `json:"snippet"`
		Payload  map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&msgResp); err != nil {
		return nil, fmt.Errorf("decode message: %w", err)
	}

	summary := &MessageSummary{
		ID:       msgResp.ID,
		ThreadID: msgResp.ThreadID,
		Snippet:  msgResp.Snippet,
	}

	if msgResp.Payload != nil {
		if headers, ok := msgResp.Payload["headers"].([]any); ok {
			for _, h := range headers {
				hdr, ok := h.(map[string]any)
				if !ok {
					continue
				}
				name := strings.ToLower(fmt.Sprint(hdr["name"]))
				value := fmt.Sprint(hdr["value"])
				switch name {
				case "subject":
					summary.Subject = value
				case "from":
					summary.From = value
				case "date":
					summary.Date = value
				}
			}
		}
	}

	return summary, nil
}

// GetMessage fetches a full email message by ID.
func (g *Service) GetMessage(ctx context.Context, messageID string) (*Message, error) {
	if g.oauth == nil {
		return nil, fmt.Errorf("oauth manager not initialized")
	}

	reqURL := fmt.Sprintf("%s/messages/%s?format=full", BaseURL, url.PathEscape(messageID))
	resp, err := g.oauth.Request(ctx, "google", http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gmail get message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gmail API error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var raw struct {
		ID       string         `json:"id"`
		ThreadID string         `json:"threadId"`
		Snippet  string         `json:"snippet"`
		LabelIDs []string       `json:"labelIds"`
		Payload  map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode message: %w", err)
	}

	subject, from, to, date, body := ParsePayload(raw.Payload)

	const maxBodyLen = 50000
	if len(body) > maxBodyLen {
		body = body[:maxBodyLen] + "\n[truncated]"
	}

	return &Message{
		ID:       raw.ID,
		ThreadID: raw.ThreadID,
		Subject:  subject,
		From:     from,
		To:       to,
		Date:     date,
		Snippet:  raw.Snippet,
		Body:     body,
		Labels:   raw.LabelIDs,
	}, nil
}

// SendMessage sends an email and returns the message ID.
func (g *Service) SendMessage(ctx context.Context, to, subject, body string, cc, bcc []string) (string, error) {
	if g.oauth == nil {
		return "", fmt.Errorf("oauth manager not initialized")
	}

	from := g.cfg.DefaultSender
	if from == "" {
		from = "me"
	}

	raw := BuildRFC2822(from, to, subject, body, cc, bcc)
	encoded := Base64URLEncode([]byte(raw))

	payload := map[string]any{
		"raw": encoded,
	}
	payloadBytes, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("%s/messages/send", BaseURL)
	resp, err := g.oauth.Request(ctx, "google", http.MethodPost, reqURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "", fmt.Errorf("gmail send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gmail send error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode send response: %w", err)
	}

	return result.ID, nil
}

// CreateDraft creates a draft email and returns the draft ID.
func (g *Service) CreateDraft(ctx context.Context, to, subject, body string) (string, error) {
	if g.oauth == nil {
		return "", fmt.Errorf("oauth manager not initialized")
	}

	from := g.cfg.DefaultSender
	if from == "" {
		from = "me"
	}

	raw := BuildRFC2822(from, to, subject, body, nil, nil)
	encoded := Base64URLEncode([]byte(raw))

	payload := map[string]any{
		"message": map[string]any{
			"raw": encoded,
		},
	}
	payloadBytes, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("%s/drafts", BaseURL)
	resp, err := g.oauth.Request(ctx, "google", http.MethodPost, reqURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "", fmt.Errorf("gmail create draft: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gmail draft error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		ID      string `json:"id"`
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode draft response: %w", err)
	}

	return result.ID, nil
}

// ModifyLabels adds or removes labels from a message.
func (g *Service) ModifyLabels(ctx context.Context, messageID string, addLabels, removeLabels []string) error {
	if g.oauth == nil {
		return fmt.Errorf("oauth manager not initialized")
	}

	payload := map[string]any{
		"addLabelIds":    addLabels,
		"removeLabelIds": removeLabels,
	}
	payloadBytes, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("%s/messages/%s/modify", BaseURL, url.PathEscape(messageID))
	resp, err := g.oauth.Request(ctx, "google", http.MethodPost, reqURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return fmt.Errorf("gmail modify labels: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gmail modify error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
