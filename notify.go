package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Notifier sends text notifications to a channel.
type Notifier interface {
	Send(text string) error
	Name() string
}

// SlackNotifier sends via Slack incoming webhook.
type SlackNotifier struct {
	WebhookURL string
	client     *http.Client
}

func (s *SlackNotifier) Send(text string) error {
	payload, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequest("POST", s.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("slack: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("slack: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *SlackNotifier) Name() string { return "slack" }

// DiscordNotifier sends via Discord webhook.
type DiscordNotifier struct {
	WebhookURL string
	client     *http.Client
}

func (d *DiscordNotifier) Send(text string) error {
	// Discord limits content to 2000 chars.
	if len(text) > 2000 {
		text = text[:1997] + "..."
	}
	payload, _ := json.Marshal(map[string]string{"content": text})
	req, err := http.NewRequest("POST", d.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("discord: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (d *DiscordNotifier) Name() string { return "discord" }

// MultiNotifier fans out to multiple notifiers. Failures are logged, not fatal.
type MultiNotifier struct {
	notifiers []Notifier
}

func (m *MultiNotifier) Send(text string) {
	for _, n := range m.notifiers {
		if err := n.Send(text); err != nil {
			logError("notification send failed", "channel", n.Name(), "error", err)
		}
	}
}

// WhatsAppNotifier sends via WhatsApp Cloud API.
type WhatsAppNotifier struct {
	Config    WhatsAppConfig
	Recipient string // phone number to send to
}

func (w *WhatsAppNotifier) Send(text string) error {
	if err := sendWhatsAppMessage(w.Config, w.Recipient, text); err != nil {
		return fmt.Errorf("whatsapp: %w", err)
	}
	return nil
}

func (w *WhatsAppNotifier) Name() string { return "whatsapp" }

// buildNotifiers creates Notifier instances from config.
func buildNotifiers(cfg *Config) []Notifier {
	var notifiers []Notifier
	client := &http.Client{Timeout: 5 * time.Second}
	for _, ch := range cfg.Notifications {
		switch ch.Type {
		case "slack":
			if ch.WebhookURL != "" {
				notifiers = append(notifiers, &SlackNotifier{WebhookURL: ch.WebhookURL, client: client})
			}
		case "discord":
			if ch.WebhookURL != "" {
				notifiers = append(notifiers, &DiscordNotifier{WebhookURL: ch.WebhookURL, client: client})
			}
		case "whatsapp":
			// For WhatsApp, WebhookURL should contain the recipient phone number
			if ch.WebhookURL != "" && cfg.WhatsApp.Enabled {
				notifiers = append(notifiers, &WhatsAppNotifier{
					Config:    cfg.WhatsApp,
					Recipient: ch.WebhookURL, // use webhookUrl field for phone number
				})
			}
		case "line": // --- P15.1: LINE Channel ---
			// For LINE, WebhookURL should contain the target user/group ID
			if ch.WebhookURL != "" && cfg.LINE.Enabled {
				notifiers = append(notifiers, &LINENotifier{
					Config: cfg.LINE,
					ChatID: ch.WebhookURL, // use webhookUrl field for LINE user/group ID
				})
			}
		default:
			logWarn("unknown notification type", "type", ch.Type)
		}
	}
	return notifiers
}
