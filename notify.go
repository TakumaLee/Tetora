package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// buildDiscordNotifierByName returns a DiscordNotifier for the named channel (from cfg.Notifications), or nil.
func buildDiscordNotifierByName(cfg *Config, name string) *DiscordNotifier {
	client := &http.Client{Timeout: 5 * time.Second}
	for _, ch := range cfg.Notifications {
		if ch.Type == "discord" && ch.Name == name && ch.WebhookURL != "" {
			return &DiscordNotifier{WebhookURL: ch.WebhookURL, client: client}
		}
	}
	return nil
}

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
		case "matrix": // --- P15.2: Matrix Channel ---
			// For Matrix, WebhookURL should contain the target room ID
			if ch.WebhookURL != "" && cfg.Matrix.Enabled {
				notifiers = append(notifiers, &MatrixNotifier{
					Config: cfg.Matrix,
					RoomID: ch.WebhookURL, // use webhookUrl field for Matrix room ID
				})
			}
		case "teams": // --- P15.3: Teams Channel ---
			// For Teams, WebhookURL is used as "serviceUrl|conversationId" format
			if ch.WebhookURL != "" && cfg.Teams.Enabled {
				parts := strings.SplitN(ch.WebhookURL, "|", 2)
				if len(parts) == 2 {
					teamsBot := newTeamsBot(cfg, nil, nil, nil)
					notifiers = append(notifiers, &TeamsNotifier{
						Bot:            teamsBot,
						ServiceURL:     parts[0],
						ConversationID: parts[1],
					})
				}
			}
		case "signal": // --- P15.4: Signal Channel ---
			// For Signal, WebhookURL format: "phoneNumber" or "group:groupId"
			if ch.WebhookURL != "" && cfg.Signal.Enabled {
				isGroup := strings.HasPrefix(ch.WebhookURL, "group:")
				recipient := ch.WebhookURL
				if isGroup {
					recipient = strings.TrimPrefix(recipient, "group:")
				}
				notifiers = append(notifiers, &SignalNotifier{
					Config:    cfg.Signal,
					Recipient: recipient,
					IsGroup:   isGroup,
				})
			}
		case "gchat", "googlechat": // --- P15.5: Google Chat Channel ---
			// For Google Chat, WebhookURL should contain the space name (spaces/{space_id})
			if ch.WebhookURL != "" && cfg.GoogleChat.Enabled {
				// Note: GoogleChatNotifier requires a bot instance which is created in main.go
				// This is a placeholder - actual initialization happens in main.go
				logWarn("gchat notifier requires bot initialization in main.go", "space", ch.WebhookURL)
			}
		case "imessage": // --- P20.2: iMessage via BlueBubbles ---
			// For iMessage, WebhookURL field holds the target chat GUID.
			if ch.WebhookURL != "" && cfg.IMessage.Enabled {
				notifiers = append(notifiers, &IMessageNotifier{
					Config:   cfg.IMessage,
					ChatGUID: ch.WebhookURL,
				})
			}
		default:
			logWarn("unknown notification type", "type", ch.Type)
		}
	}
	return notifiers
}
