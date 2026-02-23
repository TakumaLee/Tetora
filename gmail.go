package main

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
)

// --- P19.1: Gmail Integration ---

// GmailConfig holds Gmail integration settings.
type GmailConfig struct {
	Enabled       bool     `json:"enabled"`
	MaxResults    int      `json:"maxResults,omitempty"`    // default 20
	Labels        []string `json:"labels,omitempty"`        // watched labels for notifications
	AutoClassify  bool     `json:"autoClassify,omitempty"`  // LLM auto-classification
	DefaultSender string   `json:"defaultSender,omitempty"` // from address override
}

// GmailService provides Gmail API operations via OAuth.
type GmailService struct {
	cfg *Config
}

// globalGmailService is the singleton Gmail service instance.
var globalGmailService *GmailService

// gmailBaseURL is the Gmail API v1 base URL.
const gmailBaseURL = "https://gmail.googleapis.com/gmail/v1/users/me"

// --- Types ---

// GmailMessage represents a full email message.
type GmailMessage struct {
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

// GmailMessageSummary is a lightweight message summary.
type GmailMessageSummary struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
	Snippet  string `json:"snippet"`
	Subject  string `json:"subject,omitempty"`
	From     string `json:"from,omitempty"`
	Date     string `json:"date,omitempty"`
}

// --- Helper Functions ---

// base64URLEncode encodes data using base64url (no padding) as required by Gmail API.
func base64URLEncode(data []byte) string {
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data)
}

// decodeBase64URL decodes a base64url-encoded string (with or without padding).
func decodeBase64URL(s string) (string, error) {
	// Gmail API may use standard base64url with or without padding.
	// Try no-padding first, then with padding.
	b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(s)
	if err != nil {
		b, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			// Try standard base64 as final fallback.
			b, err = base64.StdEncoding.DecodeString(s)
			if err != nil {
				return "", fmt.Errorf("base64 decode: %w", err)
			}
		}
	}
	return string(b), nil
}

// buildRFC2822 constructs an RFC 2822 formatted email message.
func buildRFC2822(from, to, subject, body string, cc, bcc []string) string {
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

// parseGmailPayload extracts subject, from, to, date, and body text from a Gmail API message payload.
func parseGmailPayload(payload map[string]any) (subject, from, to, date, body string) {
	// Extract headers.
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

	// Extract body — prefer text/plain, fallback to text/html.
	body = extractBody(payload, "text/plain")
	if body == "" {
		htmlBody := extractBody(payload, "text/html")
		if htmlBody != "" {
			body = stripHTMLTags(htmlBody)
		}
	}

	return
}

// extractBody recursively finds a body part with the given MIME type in a Gmail payload.
func extractBody(payload map[string]any, mimeType string) string {
	// Check this part's mimeType.
	if mt, ok := payload["mimeType"].(string); ok && mt == mimeType {
		if bodyMap, ok := payload["body"].(map[string]any); ok {
			if data, ok := bodyMap["data"].(string); ok && data != "" {
				decoded, err := decodeBase64URL(data)
				if err == nil {
					return decoded
				}
			}
		}
	}

	// Recurse into parts (multipart messages).
	if parts, ok := payload["parts"].([]any); ok {
		for _, p := range parts {
			if part, ok := p.(map[string]any); ok {
				result := extractBody(part, mimeType)
				if result != "" {
					return result
				}
			}
		}
	}

	return ""
}

// --- Gmail API Methods ---

// ListMessages lists Gmail messages matching a query string.
func (g *GmailService) ListMessages(ctx context.Context, query string, maxResults int) ([]GmailMessageSummary, error) {
	return g.searchMessages(ctx, query, maxResults)
}

// SearchMessages searches Gmail messages using advanced Gmail search syntax.
func (g *GmailService) SearchMessages(ctx context.Context, query string, maxResults int) ([]GmailMessageSummary, error) {
	return g.searchMessages(ctx, query, maxResults)
}

// searchMessages is the shared implementation for ListMessages and SearchMessages.
func (g *GmailService) searchMessages(ctx context.Context, query string, maxResults int) ([]GmailMessageSummary, error) {
	if globalOAuthManager == nil {
		return nil, fmt.Errorf("oauth manager not initialized")
	}

	if maxResults <= 0 {
		maxResults = g.cfg.Gmail.MaxResults
		if maxResults <= 0 {
			maxResults = 20
		}
	}

	// Build request URL.
	params := url.Values{}
	if query != "" {
		params.Set("q", query)
	}
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))

	reqURL := fmt.Sprintf("%s/messages?%s", gmailBaseURL, params.Encode())
	resp, err := globalOAuthManager.Request(ctx, "google", http.MethodGet, reqURL, nil)
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
		NextPageToken  string `json:"nextPageToken"`
		ResultSizeEst  int    `json:"resultSizeEstimate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decode list response: %w", err)
	}

	if len(listResp.Messages) == 0 {
		return []GmailMessageSummary{}, nil
	}

	// Fetch metadata for each message.
	summaries := make([]GmailMessageSummary, 0, len(listResp.Messages))
	for _, msg := range listResp.Messages {
		summary, err := g.fetchMessageSummary(ctx, msg.ID, msg.ThreadID)
		if err != nil {
			logWarn("gmail fetch summary failed", "messageId", msg.ID, "error", err)
			// Include basic info even if metadata fetch fails.
			summaries = append(summaries, GmailMessageSummary{
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
func (g *GmailService) fetchMessageSummary(ctx context.Context, messageID, threadID string) (*GmailMessageSummary, error) {
	reqURL := fmt.Sprintf("%s/messages/%s?format=metadata&metadataHeaders=Subject&metadataHeaders=From&metadataHeaders=Date",
		gmailBaseURL, url.PathEscape(messageID))

	resp, err := globalOAuthManager.Request(ctx, "google", http.MethodGet, reqURL, nil)
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

	summary := &GmailMessageSummary{
		ID:       msgResp.ID,
		ThreadID: msgResp.ThreadID,
		Snippet:  msgResp.Snippet,
	}

	// Extract headers from payload.
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
func (g *GmailService) GetMessage(ctx context.Context, messageID string) (*GmailMessage, error) {
	if globalOAuthManager == nil {
		return nil, fmt.Errorf("oauth manager not initialized")
	}

	reqURL := fmt.Sprintf("%s/messages/%s?format=full", gmailBaseURL, url.PathEscape(messageID))
	resp, err := globalOAuthManager.Request(ctx, "google", http.MethodGet, reqURL, nil)
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

	subject, from, to, date, body := parseGmailPayload(raw.Payload)

	// Truncate very long bodies for tool output.
	const maxBodyLen = 50000
	if len(body) > maxBodyLen {
		body = body[:maxBodyLen] + "\n[truncated]"
	}

	return &GmailMessage{
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
func (g *GmailService) SendMessage(ctx context.Context, to, subject, body string, cc, bcc []string) (string, error) {
	if globalOAuthManager == nil {
		return "", fmt.Errorf("oauth manager not initialized")
	}

	from := g.cfg.Gmail.DefaultSender
	if from == "" {
		from = "me"
	}

	raw := buildRFC2822(from, to, subject, body, cc, bcc)
	encoded := base64URLEncode([]byte(raw))

	payload := map[string]any{
		"raw": encoded,
	}
	payloadBytes, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("%s/messages/send", gmailBaseURL)
	resp, err := globalOAuthManager.Request(ctx, "google", http.MethodPost, reqURL, strings.NewReader(string(payloadBytes)))
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

	logInfo("gmail message sent", "to", to, "subject", subject, "messageId", result.ID)
	return result.ID, nil
}

// CreateDraft creates a draft email and returns the draft ID.
func (g *GmailService) CreateDraft(ctx context.Context, to, subject, body string) (string, error) {
	if globalOAuthManager == nil {
		return "", fmt.Errorf("oauth manager not initialized")
	}

	from := g.cfg.Gmail.DefaultSender
	if from == "" {
		from = "me"
	}

	raw := buildRFC2822(from, to, subject, body, nil, nil)
	encoded := base64URLEncode([]byte(raw))

	payload := map[string]any{
		"message": map[string]any{
			"raw": encoded,
		},
	}
	payloadBytes, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("%s/drafts", gmailBaseURL)
	resp, err := globalOAuthManager.Request(ctx, "google", http.MethodPost, reqURL, strings.NewReader(string(payloadBytes)))
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

	logInfo("gmail draft created", "to", to, "subject", subject, "draftId", result.ID)
	return result.ID, nil
}

// ModifyLabels adds or removes labels from a message.
func (g *GmailService) ModifyLabels(ctx context.Context, messageID string, addLabels, removeLabels []string) error {
	if globalOAuthManager == nil {
		return fmt.Errorf("oauth manager not initialized")
	}

	payload := map[string]any{
		"addLabelIds":    addLabels,
		"removeLabelIds": removeLabels,
	}
	payloadBytes, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("%s/messages/%s/modify", gmailBaseURL, url.PathEscape(messageID))
	resp, err := globalOAuthManager.Request(ctx, "google", http.MethodPost, reqURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return fmt.Errorf("gmail modify labels: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gmail modify error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	logInfo("gmail labels modified", "messageId", messageID, "add", addLabels, "remove", removeLabels)
	return nil
}

// --- Tool Handlers ---

// toolEmailList lists emails with optional query.
func toolEmailList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"maxResults"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if globalGmailService == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}

	messages, err := globalGmailService.ListMessages(ctx, args.Query, args.MaxResults)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"count":    len(messages),
		"messages": messages,
	})
	return string(b), nil
}

// toolEmailRead reads a specific email by ID.
func toolEmailRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.MessageID == "" {
		return "", fmt.Errorf("message_id is required")
	}

	if globalGmailService == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}

	msg, err := globalGmailService.GetMessage(ctx, args.MessageID)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(msg)
	return string(b), nil
}

// toolEmailSend sends an email (requires auth).
func toolEmailSend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		To      string   `json:"to"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
		Cc      []string `json:"cc"`
		Bcc     []string `json:"bcc"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.To == "" {
		return "", fmt.Errorf("to is required")
	}
	if args.Subject == "" {
		return "", fmt.Errorf("subject is required")
	}
	if args.Body == "" {
		return "", fmt.Errorf("body is required")
	}

	if globalGmailService == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}

	messageID, err := globalGmailService.SendMessage(ctx, args.To, args.Subject, args.Body, args.Cc, args.Bcc)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`{"status":"sent","messageId":"%s"}`, messageID), nil
}

// toolEmailDraft creates an email draft.
func toolEmailDraft(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.To == "" {
		return "", fmt.Errorf("to is required")
	}
	if args.Subject == "" {
		return "", fmt.Errorf("subject is required")
	}

	if globalGmailService == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}

	draftID, err := globalGmailService.CreateDraft(ctx, args.To, args.Subject, args.Body)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`{"status":"draft_created","draftId":"%s"}`, draftID), nil
}

// toolEmailSearch searches emails with advanced Gmail syntax.
func toolEmailSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"maxResults"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	if globalGmailService == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}

	messages, err := globalGmailService.SearchMessages(ctx, args.Query, args.MaxResults)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"count":    len(messages),
		"messages": messages,
	})
	return string(b), nil
}

// toolEmailLabel modifies labels on a message.
func toolEmailLabel(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		MessageID    string   `json:"message_id"`
		AddLabels    []string `json:"add_labels"`
		RemoveLabels []string `json:"remove_labels"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.MessageID == "" {
		return "", fmt.Errorf("message_id is required")
	}
	if len(args.AddLabels) == 0 && len(args.RemoveLabels) == 0 {
		return "", fmt.Errorf("at least one of add_labels or remove_labels is required")
	}

	if globalGmailService == nil {
		return "", fmt.Errorf("gmail not configured; enable gmail in config and connect via OAuth")
	}

	if err := globalGmailService.ModifyLabels(ctx, args.MessageID, args.AddLabels, args.RemoveLabels); err != nil {
		return "", err
	}

	return fmt.Sprintf(`{"status":"labels_modified","messageId":"%s"}`, args.MessageID), nil
}
