package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

// WebhookPayload is the JSON body sent to webhook endpoints.
type WebhookPayload struct {
	Event     string  `json:"event"`     // "success", "error", "timeout", "cancelled"
	JobID     string  `json:"jobId"`
	Name      string  `json:"name"`
	Source    string  `json:"source"`
	Status    string  `json:"status"`
	Cost      float64 `json:"costUsd"`
	Duration  int64   `json:"durationMs"`
	Model     string  `json:"model"`
	Output    string  `json:"output,omitempty"`
	Error     string  `json:"error,omitempty"`
	Timestamp string  `json:"timestamp"`
}

// sendWebhooks posts the event payload to all matching webhooks.
// Non-blocking: runs in a goroutine, 5s timeout per request, failures only logged.
func sendWebhooks(cfg *Config, event string, payload WebhookPayload) {
	if len(cfg.Webhooks) == 0 {
		return
	}

	payload.Event = event
	if payload.Timestamp == "" {
		payload.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, wh := range cfg.Webhooks {
		if !webhookMatchesEvent(wh, event) {
			continue
		}

		go func(wh WebhookConfig, body []byte) {
			req, err := http.NewRequest("POST", wh.URL, bytes.NewReader(body))
			if err != nil {
				logError("webhook request creation failed", "url", wh.URL, "error", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			for k, v := range wh.Headers {
				req.Header.Set(k, v)
			}

			resp, err := client.Do(req)
			if err != nil {
				logError("webhook POST failed", "url", wh.URL, "error", err)
				return
			}
			resp.Body.Close()

			if resp.StatusCode >= 400 {
				logWarn("webhook POST returned error status", "url", wh.URL, "status", resp.StatusCode)
			}
		}(wh, body)
	}
}

// webhookMatchesEvent checks if a webhook should fire for the given event.
func webhookMatchesEvent(wh WebhookConfig, event string) bool {
	if len(wh.Events) == 0 {
		return true // no filter = all events
	}
	for _, e := range wh.Events {
		if e == "all" || e == event {
			return true
		}
	}
	return false
}
