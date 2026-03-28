package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// NotifChannel matches config.NotificationChannel for discord operations.
type NotifChannel struct {
	Name       string   `json:"name,omitempty"`
	Type       string   `json:"type"`
	WebhookURL string   `json:"webhookUrl"`
	Events     []string `json:"events,omitempty"`
}

// DiscordDeps holds dependencies for Discord HTTP handlers.
type DiscordDeps struct {
	GetConfig         func() any // returns *Config or similar
	GetNotifications  func() []NotifChannel
	FindConfigPath    func() string
	ChannelSessionKey func(source string, parts ...string) string
	FindSession       func(dbPath, chKey string) (any, error)
	HistoryDB         func() string
}

// RegisterDiscordRoutes registers Discord webhook channel management API endpoints.
func RegisterDiscordRoutes(mux *http.ServeMux, d DiscordDeps) {
	mux.HandleFunc("/api/discord/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			channels := d.GetNotifications()
			type channelInfo struct {
				Name       string   `json:"name"`
				WebhookURL string   `json:"webhookUrl"`
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

			if !ValidChannelName(body.Name) {
				http.Error(w, `{"error":"invalid channel name"}`, http.StatusBadRequest)
				return
			}
			if !strings.HasPrefix(body.WebhookURL, "https://discord.com/api/webhooks/") &&
				!strings.HasPrefix(body.WebhookURL, "https://discordapp.com/api/webhooks/") {
				http.Error(w, `{"error":"invalid webhook URL"}`, http.StatusBadRequest)
				return
			}

			existing := d.GetNotifications()
			for _, ch := range existing {
				if ch.Name == body.Name {
					http.Error(w, `{"error":"channel already exists"}`, http.StatusConflict)
					return
				}
			}

			if len(body.Events) == 0 {
				body.Events = []string{"all"}
			}

			newCh := NotifChannel{
				Name:       body.Name,
				Type:       "discord",
				WebhookURL: body.WebhookURL,
				Events:     body.Events,
			}

			configPath := d.FindConfigPath()
			if err := UpdateNotificationsConfig(configPath, body.Name, &newCh); err != nil {
				http.Error(w, `{"error":"failed to save config"}`, http.StatusInternalServerError)
				return
			}

			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": body.Name})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/discord/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		channelID := r.URL.Query().Get("channel")
		if channelID == "" {
			http.Error(w, `{"error":"channel query parameter required"}`, http.StatusBadRequest)
			return
		}
		dbPath := d.HistoryDB()
		if dbPath == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}

		chKey := d.ChannelSessionKey("discord", channelID)
		sess, err := d.FindSession(dbPath, chKey)
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

	mux.HandleFunc("/api/discord/channels/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		rest := strings.TrimPrefix(r.URL.Path, "/api/discord/channels/")
		rest = strings.Trim(rest, "/")

		isTest := strings.HasSuffix(rest, "/test")
		name := rest
		if isTest {
			name = strings.TrimSuffix(rest, "/test")
		}

		if name == "" {
			http.Error(w, `{"error":"channel name required"}`, http.StatusBadRequest)
			return
		}

		channels := d.GetNotifications()
		var found *NotifChannel
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
			if r.Method != http.MethodPost {
				http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
				return
			}
			if err := SendTestWebhook(found.WebhookURL, name); err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		if r.Method != http.MethodDelete {
			http.Error(w, `{"error":"DELETE only"}`, http.StatusMethodNotAllowed)
			return
		}
		configPath := d.FindConfigPath()
		if err := UpdateNotificationsConfig(configPath, name, nil); err != nil {
			http.Error(w, `{"error":"failed to update config"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

// ValidChannelName validates a Discord notification channel name.
func ValidChannelName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// UpdateNotificationsConfig adds, updates, or removes a notification channel in config.
// Pass ch=nil to remove.
func UpdateNotificationsConfig(configPath, name string, ch *NotifChannel) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var channels []NotifChannel
	if notifRaw, ok := raw["notifications"]; ok {
		_ = json.Unmarshal(notifRaw, &channels)
	}

	if ch == nil {
		filtered := channels[:0]
		for _, c := range channels {
			if c.Name != name {
				filtered = append(filtered, c)
			}
		}
		channels = filtered
	} else {
		found := false
		for i, c := range channels {
			if c.Name == name {
				channels[i] = *ch
				found = true
				break
			}
		}
		if !found {
			channels = append(channels, *ch)
		}
	}

	b, _ := json.Marshal(channels)
	raw["notifications"] = b
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}

// SendTestWebhook sends a test message to a Discord webhook URL.
func SendTestWebhook(webhookURL, channelName string) error {
	payload := fmt.Sprintf(`{"content":"🔔 Test notification from Tetora — channel: %s"}`, channelName)
	resp, err := http.Post(webhookURL, "application/json", strings.NewReader(payload))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Discord returned HTTP %d", resp.StatusCode)
	}
	return nil
}
