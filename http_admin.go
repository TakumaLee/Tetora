package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Server) registerAdminRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	state := s.state
	sem := s.sem

	// --- Incoming Webhooks ---
	mux.HandleFunc("/hooks/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/hooks/")
		if name == "" {
			http.Error(w, `{"error":"webhook name required"}`, http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		result := handleIncomingWebhook(ctx, cfg, name, r, state, sem)
		w.Header().Set("Content-Type", "application/json")
		switch result.Status {
		case "error":
			w.WriteHeader(http.StatusBadRequest)
		case "disabled":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("/webhooks/incoming", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		type webhookInfo struct {
			Name      string `json:"name"`
			Role      string `json:"role"`
			Enabled   bool   `json:"enabled"`
			Template  string `json:"template,omitempty"`
			Filter    string `json:"filter,omitempty"`
			Workflow  string `json:"workflow,omitempty"`
			HasSecret bool   `json:"hasSecret"`
		}
		var list []webhookInfo
		for name, wh := range cfg.IncomingWebhooks {
			list = append(list, webhookInfo{
				Name:      name,
				Role:      wh.Role,
				Enabled:   wh.isEnabled(),
				Template:  wh.Template,
				Filter:    wh.Filter,
				Workflow:  wh.Workflow,
				HasSecret: wh.Secret != "",
			})
		}
		if list == nil {
			list = []webhookInfo{}
		}
		json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("/audit", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		limit := 50
		offset := 0
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		if p := r.URL.Query().Get("page"); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n > 1 {
				offset = (n - 1) * limit
			}
		}

		entries, total, err := queryAuditLog(cfg.HistoryDB, limit, offset)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []AuditEntry{}
		}

		page := (offset / limit) + 1
		json.NewEncoder(w).Encode(map[string]any{
			"entries": entries,
			"total":   total,
			"page":    page,
			"limit":   limit,
		})
	})

	// --- Retention & Data ---
	mux.HandleFunc("/retention", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			stats := make(map[string]int)
			if cfg.HistoryDB != "" {
				stats = queryRetentionStats(cfg.HistoryDB)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"config": cfg.Retention,
				"defaults": map[string]int{
					"history":     retentionDays(cfg.Retention.History, 90),
					"sessions":    retentionDays(cfg.Retention.Sessions, 30),
					"auditLog":    retentionDays(cfg.Retention.AuditLog, 365),
					"logs":        retentionDays(cfg.Retention.Logs, 14),
					"workflows":   retentionDays(cfg.Retention.Workflows, 90),
					"reflections": retentionDays(cfg.Retention.Reflections, 60),
					"sla":         retentionDays(cfg.Retention.SLA, 90),
					"trustEvents": retentionDays(cfg.Retention.TrustEvents, 90),
					"handoffs":    retentionDays(cfg.Retention.Handoffs, 60),
					"queue":       retentionDays(cfg.Retention.Queue, 7),
					"versions":    retentionDays(cfg.Retention.Versions, 180),
					"outputs":     retentionDays(cfg.Retention.Outputs, 30),
					"uploads":     retentionDays(cfg.Retention.Uploads, 7),
				},
				"stats": stats,
			})
		default:
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/retention/cleanup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		auditLog(cfg.HistoryDB, "retention.cleanup", "http", "", clientIP(r))
		results := runRetention(cfg)
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})

	mux.HandleFunc("/data/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		auditLog(cfg.HistoryDB, "data.export", "http", "", clientIP(r))
		data, err := exportData(cfg)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		w.Write(data)
	})

	mux.HandleFunc("/data/purge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, `{"error":"DELETE only"}`, http.StatusMethodNotAllowed)
			return
		}
		before := r.URL.Query().Get("before")
		if before == "" {
			http.Error(w, `{"error":"before parameter required (YYYY-MM-DD)"}`, http.StatusBadRequest)
			return
		}
		confirm := r.Header.Get("X-Confirm-Purge")
		if confirm != "true" {
			http.Error(w, `{"error":"X-Confirm-Purge: true header required"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		auditLog(cfg.HistoryDB, "data.purge", "http", "before="+before, clientIP(r))
		results, err := purgeDataBefore(cfg, before)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})

	// --- Backup ---
	mux.HandleFunc("/backup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		auditLog(cfg.HistoryDB, "backup.download", "http", "", clientIP(r))

		// Create temp backup.
		tmpFile, err := os.CreateTemp("", "tetora-backup-*.tar.gz")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"create temp: %v"}`, err), http.StatusInternalServerError)
			return
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		if err := createBackup(cfg.baseDir, tmpPath); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"create backup: %v"}`, err), http.StatusInternalServerError)
			return
		}

		data, err := os.ReadFile(tmpPath)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"read backup: %v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", "attachment; filename=tetora-backup.tar.gz")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Write(data)
	})

	// Dashboard login.
	mux.HandleFunc("/dashboard/login", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.DashboardAuth.Enabled {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}

		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(dashboardLoginHTML))
			return
		}

		if r.Method == http.MethodPost {
			ip := clientIP(r)

			// Rate limit check.
			if s.limiter.isLocked(ip) {
				auditLog(cfg.HistoryDB, "dashboard.login.ratelimit", "http", "", ip)
				if s.secMon != nil {
					s.secMon.recordEvent(ip, "login.ratelimit")
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(dashboardLoginLockedHTML))
				return
			}

			r.ParseForm()
			password := r.FormValue("password")

			expected := cfg.DashboardAuth.Password
			if expected == "" {
				expected = cfg.DashboardAuth.Token
			}

			if password != expected {
				s.limiter.recordFailure(ip)
				auditLog(cfg.HistoryDB, "dashboard.login.fail", "http", "", ip)
				if s.secMon != nil {
					s.secMon.recordEvent(ip, "login.fail")
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(dashboardLoginFailHTML))
				return
			}

			// Success — clear rate limit.
			s.limiter.recordSuccess(ip)

			// Set session cookie.
			secret := expected
			cookieVal := dashboardAuthCookie(secret)
			cookie := &http.Cookie{
				Name:     "tetora_session",
				Value:    cookieVal,
				Path:     "/dashboard",
				MaxAge:   86400, // 24h
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			}
			if cfg.tlsEnabled {
				cookie.Secure = true
			}
			http.SetCookie(w, cookie)
			auditLog(cfg.HistoryDB, "dashboard.login", "http", "", ip)
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}

		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	})

	// --- Config & Workflow Versioning ---
	mux.HandleFunc("/config/versions", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			limit := 20
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 {
					limit = n
				}
			}
			versions, err := queryVersions(cfg.HistoryDB, "config", "config.json", limit)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if versions == nil {
				versions = []ConfigVersion{}
			}
			json.NewEncoder(w).Encode(versions)
		case http.MethodPost:
			// Manual snapshot.
			var req struct {
				Reason string `json:"reason"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			configPath := filepath.Join(cfg.baseDir, "config.json")
			if err := snapshotConfig(cfg.HistoryDB, configPath, "api", req.Reason); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"ok"}`))
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/config/versions/", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/config/versions/")

		// GET /config/versions/{id} — show version detail
		if r.Method == http.MethodGet && !strings.Contains(path, "/") {
			ver, err := queryVersionByID(cfg.HistoryDB, path)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(ver)
			return
		}

		// POST /config/versions/{id}/restore
		if r.Method == http.MethodPost && strings.HasSuffix(path, "/restore") {
			versionID := strings.TrimSuffix(path, "/restore")
			configPath := filepath.Join(cfg.baseDir, "config.json")
			if _, err := restoreConfigVersion(cfg.HistoryDB, configPath, versionID); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"status":"restored","note":"restart daemon for changes to take effect"}`))
			return
		}

		// GET /config/versions/{id}/diff/{id2}
		if r.Method == http.MethodGet && strings.Contains(path, "/diff/") {
			parts := strings.SplitN(path, "/diff/", 2)
			if len(parts) != 2 {
				http.Error(w, `{"error":"use GET /config/versions/{id}/diff/{id2}"}`, http.StatusBadRequest)
				return
			}
			result, err := versionDiffDetail(cfg.HistoryDB, parts[0], parts[1])
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(result)
			return
		}

		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	})

	mux.HandleFunc("/versions", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		entityType := r.URL.Query().Get("type")
		entityName := r.URL.Query().Get("name")
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		if entityType == "" {
			// List all versioned entities.
			entities, err := queryAllVersionedEntities(cfg.HistoryDB)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if entities == nil {
				entities = []ConfigVersion{}
			}
			json.NewEncoder(w).Encode(entities)
			return
		}
		versions, err := queryVersions(cfg.HistoryDB, entityType, entityName, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if versions == nil {
			versions = []ConfigVersion{}
		}
		json.NewEncoder(w).Encode(versions)
	})

	// --- P13.1: Plugin System --- Plugin API routes.
	mux.HandleFunc("/api/plugins", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if s.pluginHost == nil {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode(s.pluginHost.List())
	})

	mux.HandleFunc("/api/plugins/", func(w http.ResponseWriter, r *http.Request) {
		// Parse /api/plugins/{name}/{action}
		path := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, `{"error":"plugin name required"}`, http.StatusBadRequest)
			return
		}
		name := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		w.Header().Set("Content-Type", "application/json")

		if s.pluginHost == nil {
			http.Error(w, `{"error":"plugin system not initialized"}`, http.StatusServiceUnavailable)
			return
		}

		switch action {
		case "start":
			if r.Method != http.MethodPost {
				http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
				return
			}
			if err := s.pluginHost.Start(name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "started", "name": name})

		case "stop":
			if r.Method != http.MethodPost {
				http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
				return
			}
			if err := s.pluginHost.Stop(name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "stopped", "name": name})

		case "health":
			if r.Method != http.MethodGet {
				http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
				return
			}
			json.NewEncoder(w).Encode(s.pluginHost.Health(name))

		default:
			http.Error(w, `{"error":"unknown action, use start, stop, or health"}`, http.StatusBadRequest)
		}
	})

	// --- P18.4: Self-Improving Skills Store API ---
	mux.HandleFunc("/api/skills/store", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			allMetas := loadAllFileSkillMetas(cfg)
			pending := listPendingSkills(cfg)
			json.NewEncoder(w).Encode(map[string]any{
				"skills":  allMetas,
				"pending": pending,
				"total":   len(allMetas),
			})
		default:
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/skills/store/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Parse /api/skills/store/<name>/<action>
		path := strings.TrimPrefix(r.URL.Path, "/api/skills/store/")
		if path == "" {
			http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
			return
		}
		parts := strings.SplitN(path, "/", 2)
		name := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch r.Method {
		case http.MethodPost:
			switch action {
			case "approve":
				auditLog(cfg.HistoryDB, "skill.approve", "http",
					fmt.Sprintf("name=%s", name), clientIP(r))
				if err := approveSkill(cfg, name); err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
					return
				}
				if cfg.HistoryDB != "" {
					recordSkillEvent(cfg.HistoryDB, name, "approved", "", "http")
				}
				json.NewEncoder(w).Encode(map[string]string{"status": "approved", "name": name})

			case "reject":
				auditLog(cfg.HistoryDB, "skill.reject", "http",
					fmt.Sprintf("name=%s", name), clientIP(r))
				if err := rejectSkill(cfg, name); err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
					return
				}
				if cfg.HistoryDB != "" {
					recordSkillEvent(cfg.HistoryDB, name, "rejected", "", "http")
				}
				json.NewEncoder(w).Encode(map[string]string{"status": "rejected", "name": name})

			default:
				http.Error(w, `{"error":"unknown action, use approve or reject"}`, http.StatusBadRequest)
			}

		case http.MethodDelete:
			if action != "" {
				http.Error(w, `{"error":"DELETE /api/skills/store/<name>"}`, http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "skill.delete", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			if err := deleteFileSkill(cfg, name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})

		default:
			http.Error(w, `{"error":"POST or DELETE only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/skills/usage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}

		rows, err := queryDB(cfg.HistoryDB,
			fmt.Sprintf(`SELECT id, skill_name, event_type, task_prompt, role, created_at FROM skill_usage ORDER BY id DESC LIMIT %d`, limit))
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"events": rows,
			"count":  len(rows),
		})
	})

	// --- P22.4: Integration Status ---
	mux.HandleFunc("/api/integrations/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		type channelStatus struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"` // "connected", "not_configured", "error"
		}
		type integrationStatus struct {
			Channels      []channelStatus    `json:"channels"`
			OAuthServices []map[string]any   `json:"oauthServices,omitempty"`
			RemindersActive int              `json:"remindersActive"`
			TriggersActive  int              `json:"triggersActive"`
			KnowledgeDocs   int              `json:"knowledgeDocs"`
			BrowserRelay    string           `json:"browserRelay"`    // "connected", "not_configured", "no_clients"
			HomeAssistant   string           `json:"homeAssistant"`   // "connected", "not_configured"
		}

		status := integrationStatus{}

		// Channel statuses.
		channels := []struct {
			name    string
			enabled bool
			bot     any
		}{
			{"telegram", cfg.Telegram.Enabled && cfg.Telegram.BotToken != "", nil},
			{"slack", cfg.Slack.Enabled, s.slackBot},
			{"discord", cfg.Discord.Enabled, nil},
			{"whatsapp", cfg.WhatsApp.Enabled, s.whatsappBot},
			{"line", cfg.LINE.Enabled, s.lineBot},
			{"teams", cfg.Teams.Enabled, s.teamsBot},
			{"signal", cfg.Signal.Enabled, s.signalBot},
			{"gchat", cfg.GoogleChat.Enabled, s.gchatBot},
			{"imessage", cfg.IMessage.Enabled, s.imessageBot},
		}
		for _, ch := range channels {
			cs := channelStatus{Name: ch.name, Enabled: ch.enabled}
			if !ch.enabled {
				cs.Status = "not_configured"
			} else if ch.bot != nil {
				cs.Status = "connected"
			} else {
				cs.Status = "connected" // enabled = assumed connected for webhook-based channels
			}
			status.Channels = append(status.Channels, cs)
		}

		// OAuth services — use listOAuthTokenStatuses directly.
		if cfg.HistoryDB != "" {
			statuses, err := listOAuthTokenStatuses(cfg.HistoryDB, cfg.OAuth.EncryptionKey)
			if err == nil {
				for _, st := range statuses {
					svcMap := map[string]any{
						"name":      st.ServiceName,
						"connected": st.Connected,
					}
					if st.Scopes != "" {
						svcMap["scopes"] = st.Scopes
					}
					status.OAuthServices = append(status.OAuthServices, svcMap)
				}
			}
		}

		// Reminders count — query DB directly.
		if cfg.HistoryDB != "" {
			rows, err := queryDB(cfg.HistoryDB, "SELECT COUNT(*) as cnt FROM reminders WHERE status='pending'")
			if err == nil && len(rows) > 0 {
				if cnt, ok := rows[0]["cnt"]; ok {
					switch v := cnt.(type) {
					case float64:
						status.RemindersActive = int(v)
					case string:
						fmt.Sscanf(v, "%d", &status.RemindersActive)
					}
				}
			}
		}

		// Triggers count.
		triggers := cfg.WorkflowTriggers
		activeCount := 0
		for _, t := range triggers {
			if t.isEnabled() {
				activeCount++
			}
		}
		status.TriggersActive = activeCount

		// Knowledge docs count.
		if cfg.HistoryDB != "" {
			rows, err := queryDB(cfg.HistoryDB, "SELECT COUNT(*) as cnt FROM knowledge_docs")
			if err == nil && len(rows) > 0 {
				if cnt, ok := rows[0]["cnt"]; ok {
					switch v := cnt.(type) {
					case float64:
						status.KnowledgeDocs = int(v)
					case string:
						fmt.Sscanf(v, "%d", &status.KnowledgeDocs)
					}
				}
			}
		}

		// Browser relay status.
		if !cfg.BrowserRelay.Enabled {
			status.BrowserRelay = "not_configured"
		} else if globalBrowserRelay != nil && globalBrowserRelay.Connected() {
			status.BrowserRelay = "connected"
		} else {
			status.BrowserRelay = "no_clients"
		}

		// Home Assistant status.
		if !cfg.HomeAssistant.Enabled {
			status.HomeAssistant = "not_configured"
		} else if globalHAService != nil {
			status.HomeAssistant = "connected"
		} else {
			status.HomeAssistant = "not_configured"
		}

		json.NewEncoder(w).Encode(status)
	})

	// --- P22.5: Config Summary (sanitized) ---
	mux.HandleFunc("/api/config/summary", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		maskSecret := func(s string) string {
			if s == "" {
				return "not set"
			}
			return "***configured***"
		}

		toolCount := 0
		if cfg.toolRegistry != nil {
			toolCount = len(cfg.toolRegistry.List())
		}

		summary := map[string]any{
			"general": map[string]any{
				"listenAddr":     cfg.ListenAddr,
				"maxConcurrent":  cfg.MaxConcurrent,
				"defaultModel":   cfg.DefaultModel,
				"defaultTimeout": cfg.DefaultTimeout,
				"apiToken":       maskSecret(cfg.APIToken),
				"tlsEnabled":     cfg.tlsEnabled,
			},
			"channels": map[string]any{
				"telegram":  cfg.Telegram.Enabled,
				"slack":     cfg.Slack.Enabled,
				"discord":   cfg.Discord.Enabled,
				"whatsapp":  cfg.WhatsApp.Enabled,
				"line":      cfg.LINE.Enabled,
				"teams":     cfg.Teams.Enabled,
				"signal":    cfg.Signal.Enabled,
				"gchat":     cfg.GoogleChat.Enabled,
				"imessage":  cfg.IMessage.Enabled,
			},
			"integrations": map[string]any{
				"weather":       cfg.Weather.Enabled,
				"currency":      cfg.Currency.Enabled,
				"rss":           cfg.RSS.Enabled,
				"translate":     map[string]any{"enabled": cfg.Translate.Enabled, "provider": cfg.Translate.Provider, "apiKey": maskSecret(cfg.Translate.APIKey)},
				"imageGen":      map[string]any{"enabled": cfg.ImageGen.Enabled, "apiKey": maskSecret(cfg.ImageGen.APIKey), "model": cfg.ImageGen.Model, "dailyLimit": cfg.ImageGen.DailyLimit, "maxCostDay": cfg.ImageGen.MaxCostDay},
				"homeAssistant": map[string]any{"enabled": cfg.HomeAssistant.Enabled, "url": cfg.HomeAssistant.BaseURL},
				"gmail":         cfg.Gmail.Enabled,
				"calendar":      cfg.Calendar.Enabled,
				"twitter":       cfg.Twitter.Enabled,
				"browserRelay":  cfg.BrowserRelay.Enabled,
				"notebookLM":    cfg.NotebookLM.Enabled,
			},
			"tools": map[string]any{
				"totalRegistered": toolCount,
			},
			"budgets": map[string]any{
				"dailyLimit":  cfg.CostAlert.DailyLimit,
				"weeklyLimit": cfg.CostAlert.WeeklyLimit,
				"action":      cfg.CostAlert.Action,
			},
			"security": map[string]any{
				"tlsEnabled":    cfg.tlsEnabled,
				"rateLimit":     cfg.RateLimit.Enabled,
				"rateLimitMax":  cfg.RateLimit.MaxPerMin,
				"ipAllowlist":   len(cfg.AllowedIPs),
				"dashboardAuth": cfg.DashboardAuth.Enabled,
			},
		}

		json.NewEncoder(w).Encode(summary)
	})

	// --- P18.2: OAuth 2.0 Framework ---
	oauthMgr := newOAuthManager(cfg)
	globalOAuthManager = oauthMgr // expose for Gmail/Calendar tools
	mux.HandleFunc("/api/oauth/services", oauthMgr.handleOAuthServices)
	mux.HandleFunc("/api/oauth/", oauthMgr.handleOAuthRoute)

	// --- Drain: graceful shutdown (stop accepting new tasks, wait for running to finish) ---
	mux.HandleFunc("/api/admin/drain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		state.mu.Lock()
		alreadyDraining := state.draining
		state.draining = true
		active := len(state.running)
		state.mu.Unlock()

		auditLog(cfg.HistoryDB, "admin.drain", "http", fmt.Sprintf("active=%d", active), clientIP(r))
		logInfo("drain requested via API", "activeAgents", active)

		// Signal the main loop to begin draining (if channel is wired up).
		if s.drainCh != nil && !alreadyDraining {
			select {
			case s.drainCh <- struct{}{}:
			default:
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":   "draining",
			"active":   active,
			"message":  "daemon will stop accepting new tasks and exit after current tasks complete",
		})
	})
}
