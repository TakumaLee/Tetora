package main

import "tetora/internal/webhook"

// WebhookPayload is the JSON body sent to webhook endpoints.
type WebhookPayload = webhook.Payload

// sendWebhooks posts the event payload to all matching webhooks in cfg.
func sendWebhooks(cfg *Config, event string, payload WebhookPayload) {
	whs := make([]webhook.Config, len(cfg.Webhooks))
	for i, w := range cfg.Webhooks {
		whs[i] = webhook.Config{URL: w.URL, Events: w.Events, Headers: w.Headers}
	}
	webhook.Send(whs, event, payload)
}

// webhookMatchesEvent checks if a webhook should fire for the given event.
func webhookMatchesEvent(wh WebhookConfig, event string) bool {
	return webhook.MatchesEvent(webhook.Config{URL: wh.URL, Events: wh.Events, Headers: wh.Headers}, event)
}
