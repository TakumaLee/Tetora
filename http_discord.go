package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// registerDiscordRoutes registers Discord webhook channel management API endpoints.
//
//	GET  /api/discord/channels           - list all Discord notification channels
//	POST /api/discord/channels           - add a new Discord notification channel
//	DELETE /api/discord/channels/:name   - remove a Discord notification channel
//	POST /api/discord/channels/:name/test - send a test message to a channel
func (s *Server) registerDiscordRoutes(mux *http.ServeMux) {
	// Collection endpoint: list + create.
	mux.HandleFunc("/api/discord/channels", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			channels := discordGetWebhookChannels(cfg)
			type channelInfo struct {
				Name       string   `json:"name"`
				WebhookURL string   `json:"webhookUrl"` // preview only (first 60 chars + "…")
				Events     []string `json:"events"`
			}
			result := make([]channelInfo, 0, len(channels))
			for _, ch := range channels {
				preview := ch.WebhookURL
				if len(preview) > 60 {
					preview = preview[:57] + "..."
				}
				events := ch.Events
				if len(events) == 0 {
					events = []string{"all"}
				}
				result = append(result, channelInfo{
					Name:       ch.Name,
					WebhookURL: preview,
					Events:     events,
				})
			}
			json.NewEncoder(w).Encode(result)

		case http.MethodPost:
			var body struct {
				Name       string   `json:"name"`
				WebhookURL string   `json:"webhookUrl"`
				Events     []string `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}

			// Validate.
			if !discordValidChannelName(body.Name) {
				http.Error(w, `{"error":"invalid channel name"}`, http.StatusBadRequest)
				return
			}
			if !strings.HasPrefix(body.WebhookURL, "https://discord.com/api/webhooks/") &&
				!strings.HasPrefix(body.WebhookURL, "https://discordapp.com/api/webhooks/") {
				http.Error(w, `{"error":"invalid webhook URL"}`, http.StatusBadRequest)
				return
			}

			// Duplicate check.
			existing := discordGetWebhookChannels(cfg)
			for _, ch := range existing {
				if ch.Name == body.Name {
					http.Error(w, `{"error":"channel already exists"}`, http.StatusConflict)
					return
				}
			}

			if len(body.Events) == 0 {
				body.Events = []string{"all"}
			}

			newCh := NotificationChannel{
				Name:       body.Name,
				Type:       "discord",
				WebhookURL: body.WebhookURL,
				Events:     body.Events,
			}

			configPath := findConfigPath()
			if err := discordUpdateNotificationsConfig(configPath, body.Name, &newCh); err != nil {
				http.Error(w, `{"error":"failed to save config"}`, http.StatusInternalServerError)
				return
			}

			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": body.Name})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// GET /api/discord/session?channel=<channel_id> — resolve Discord channel to session.
	mux.HandleFunc("/api/discord/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		cfg := s.Cfg()
		w.Header().Set("Content-Type", "application/json")

		channelID := r.URL.Query().Get("channel")
		if channelID == "" {
			http.Error(w, `{"error":"channel query parameter required"}`, http.StatusBadRequest)
			return
		}
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}

		chKey := channelSessionKey("discord", channelID)
		sess, err := findChannelSession(cfg.HistoryDB, chKey)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if sess == nil {
			http.Error(w, `{"error":"no active session for this channel"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(sess)
	})

	// Item endpoint: delete + test — match /api/discord/channels/{name} and /api/discord/channels/{name}/test
	mux.HandleFunc("/api/discord/channels/", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Cfg()
		w.Header().Set("Content-Type", "application/json")

		// Parse the path after /api/discord/channels/
		rest := strings.TrimPrefix(r.URL.Path, "/api/discord/channels/")
		rest = strings.Trim(rest, "/")

		// Distinguish /{name}/test from /{name}
		isTest := strings.HasSuffix(rest, "/test")
		name := rest
		if isTest {
			name = strings.TrimSuffix(rest, "/test")
		}

		if name == "" {
			http.Error(w, `{"error":"channel name required"}`, http.StatusBadRequest)
			return
		}

		// Verify channel exists.
		channels := discordGetWebhookChannels(cfg)
		var found *NotificationChannel
		for i := range channels {
			if channels[i].Name == name {
				found = &channels[i]
				break
			}
		}
		if found == nil {
			http.Error(w, `{"error":"channel not found"}`, http.StatusNotFound)
			return
		}

		if isTest {
			// POST /api/discord/channels/:name/test
			if r.Method != http.MethodPost {
				http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
				return
			}
			if err := discordSendTestWebhook(found.WebhookURL, name); err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		// DELETE /api/discord/channels/:name
		if r.Method != http.MethodDelete {
			http.Error(w, `{"error":"DELETE only"}`, http.StatusMethodNotAllowed)
			return
		}
		configPath := findConfigPath()
		if err := discordUpdateNotificationsConfig(configPath, name, nil); err != nil {
			http.Error(w, `{"error":"failed to update config"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}
