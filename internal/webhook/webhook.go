// Package webhook provides outgoing webhook delivery for job lifecycle events.
package webhook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	tlog "tetora/internal/log"
)

// Config defines a single outgoing webhook endpoint.
type Config struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Events  []string          `json:"events,omitempty"` // "success", "error", "timeout", "all"; empty = all
}

// Payload is the JSON body sent to webhook endpoints.
type Payload struct {
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

// Send posts the event payload to all matching webhook endpoints.
// Non-blocking: each delivery runs in a goroutine with a 5s timeout.
// Failures are logged but never returned to the caller.
func Send(webhooks []Config, event string, payload Payload) {
	if len(webhooks) == 0 {
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

	for _, wh := range webhooks {
		if !MatchesEvent(wh, event) {
			continue
		}

		go func(wh Config, body []byte) {
			req, err := http.NewRequest("POST", wh.URL, bytes.NewReader(body))
			if err != nil {
				tlog.Error("webhook request creation failed", "url", wh.URL, "error", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			for k, v := range wh.Headers {
				req.Header.Set(k, v)
			}

			resp, err := client.Do(req)
			if err != nil {
				tlog.Error("webhook POST failed", "url", wh.URL, "error", err)
				return
			}
			resp.Body.Close()

			if resp.StatusCode >= 400 {
				tlog.Warn("webhook POST returned error status", "url", wh.URL, "status", resp.StatusCode)
			}
		}(wh, body)
	}
}

// MatchesEvent reports whether the webhook should fire for the given event.
func MatchesEvent(wh Config, event string) bool {
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
