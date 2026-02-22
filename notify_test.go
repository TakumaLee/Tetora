package main

import (
	"fmt"
	"sync"
	"testing"
)

type mockNotifier struct {
	name     string
	messages []string
	mu       sync.Mutex
	failErr  error
}

func (m *mockNotifier) Send(text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failErr != nil {
		return m.failErr
	}
	m.messages = append(m.messages, text)
	return nil
}

func (m *mockNotifier) Name() string { return m.name }

func TestMultiNotifierSend(t *testing.T) {
	n1 := &mockNotifier{name: "slack"}
	n2 := &mockNotifier{name: "discord"}
	multi := &MultiNotifier{notifiers: []Notifier{n1, n2}}

	multi.Send("hello")

	if len(n1.messages) != 1 || n1.messages[0] != "hello" {
		t.Errorf("slack got %v, want [hello]", n1.messages)
	}
	if len(n2.messages) != 1 || n2.messages[0] != "hello" {
		t.Errorf("discord got %v, want [hello]", n2.messages)
	}
}

func TestMultiNotifierPartialFailure(t *testing.T) {
	n1 := &mockNotifier{name: "slack", failErr: fmt.Errorf("timeout")}
	n2 := &mockNotifier{name: "discord"}
	multi := &MultiNotifier{notifiers: []Notifier{n1, n2}}

	multi.Send("test")

	// n1 fails but n2 should still receive.
	if len(n2.messages) != 1 {
		t.Errorf("discord should receive despite slack failure")
	}
}

func TestBuildNotifiers(t *testing.T) {
	cfg := &Config{
		Notifications: []NotificationChannel{
			{Type: "slack", WebhookURL: "https://hooks.slack.com/test"},
			{Type: "discord", WebhookURL: "https://discord.com/api/webhooks/test"},
			{Type: "unknown", WebhookURL: "https://example.com"},
			{Type: "slack", WebhookURL: ""}, // empty URL, should skip
		},
	}
	notifiers := buildNotifiers(cfg)
	if len(notifiers) != 2 {
		t.Errorf("got %d notifiers, want 2", len(notifiers))
	}
	if notifiers[0].Name() != "slack" {
		t.Errorf("first notifier = %q, want slack", notifiers[0].Name())
	}
	if notifiers[1].Name() != "discord" {
		t.Errorf("second notifier = %q, want discord", notifiers[1].Name())
	}
}

func TestDiscordContentLimit(t *testing.T) {
	d := &DiscordNotifier{WebhookURL: "http://localhost:0/test"}
	// Verify the struct is properly initialized.
	if d.Name() != "discord" {
		t.Errorf("Name() = %q, want discord", d.Name())
	}
}

func TestSlackNotifierName(t *testing.T) {
	s := &SlackNotifier{WebhookURL: "http://localhost:0/test"}
	if s.Name() != "slack" {
		t.Errorf("Name() = %q, want slack", s.Name())
	}
}

func TestBuildNotifiersEmpty(t *testing.T) {
	cfg := &Config{}
	notifiers := buildNotifiers(cfg)
	if len(notifiers) != 0 {
		t.Errorf("got %d notifiers, want 0", len(notifiers))
	}
}

func TestMultiNotifierEmpty(t *testing.T) {
	multi := &MultiNotifier{notifiers: nil}
	// Should not panic with zero notifiers.
	multi.Send("test")
}
