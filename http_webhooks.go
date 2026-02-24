package main

import (
	"net/http"
)

func (s *Server) registerWebhookRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// Register Slack events endpoint (uses its own auth via signing secret).
	if s.slackBot != nil {
		mux.HandleFunc("/slack/events", s.slackBot.slackEventHandler)
	}

	// Register WhatsApp webhook endpoint (uses its own auth via signature verification).
	if s.whatsappBot != nil {
		mux.HandleFunc("/api/whatsapp/webhook", s.whatsappBot.whatsAppWebhookHandler)
	}

	// --- P14.1: Discord Components v2 ---
	if s.state.discordBot != nil && cfg.Discord.PublicKey != "" {
		discordBot := s.state.discordBot
		mux.HandleFunc("/api/discord/interactions", func(w http.ResponseWriter, r *http.Request) {
			handleDiscordInteraction(discordBot, w, r)
		})
		logInfo("discord interactions endpoint enabled", "endpoint", "/api/discord/interactions")
	}

	// --- P15.1: LINE Channel --- Register LINE webhook endpoint.
	if s.lineBot != nil {
		webhookPath := cfg.LINE.webhookPathOrDefault()
		mux.HandleFunc(webhookPath, s.lineBot.HandleWebhook)
	}

	// --- P15.3: Teams Channel --- Register Teams webhook endpoint.
	if s.teamsBot != nil {
		mux.HandleFunc("/api/teams/webhook", s.teamsBot.HandleWebhook)
	}

	// --- P15.4: Signal Channel --- Register Signal webhook endpoint.
	if s.signalBot != nil {
		webhookPath := cfg.Signal.webhookPathOrDefault()
		mux.HandleFunc(webhookPath, s.signalBot.HandleWebhook)
	}

	// --- P15.5: Google Chat Channel --- Register Google Chat webhook endpoint.
	if s.gchatBot != nil {
		webhookPath := cfg.GoogleChat.webhookPathOrDefault()
		mux.HandleFunc(webhookPath, s.gchatBot.HandleWebhook)
	}

	// --- P20.2: iMessage --- BlueBubbles webhook.
	if s.imessageBot != nil {
		mux.HandleFunc(cfg.IMessage.webhookPathOrDefault(), s.imessageBot.webhookHandler)
	}
}
