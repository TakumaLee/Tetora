package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) registerSessionRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	state := s.state
	sem := s.sem
	childSem := s.childSem

	// --- Sessions ---
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			q := SessionQuery{
				Agent:  r.URL.Query().Get("role"),
				Status: r.URL.Query().Get("status"),
				Source: r.URL.Query().Get("source"),
				Limit:  20,
			}
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
					q.Limit = n
				}
			}
			if p := r.URL.Query().Get("page"); p != "" {
				if n, err := strconv.Atoi(p); err == nil && n > 1 {
					q.Offset = (n - 1) * q.Limit
				}
			}

			sessions, total, err := querySessions(cfg.HistoryDB, q)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if sessions == nil {
				sessions = []Session{}
			}
			page := (q.Offset / q.Limit) + 1
			json.NewEncoder(w).Encode(map[string]any{
				"sessions": sessions,
				"total":    total,
				"page":     page,
				"limit":    q.Limit,
			})

		case http.MethodPost:
			var body struct {
				Agent string `json:"agent"`
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Agent == "" {
				http.Error(w, `{"error":"agent is required"}`, http.StatusBadRequest)
				return
			}
			if _, ok := cfg.Agents[body.Agent]; !ok {
				http.Error(w, `{"error":"agent not found"}`, http.StatusBadRequest)
				return
			}
			now := time.Now().Format(time.RFC3339)
			sess := Session{
				ID:        newUUID(),
				Agent:     body.Agent,
				Source:    "chat",
				Status:    "active",
				Title:     body.Title,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if sess.Title == "" {
				sess.Title = "New chat with " + body.Agent
			}
			if err := createSession(cfg.HistoryDB, sess); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "session.create", "http",
				fmt.Sprintf("session=%s role=%s", sess.ID, sess.Agent), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(sess)

		default:
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if cfg.HistoryDB == "" {
			http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/sessions/")
		if path == "" {
			http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
			return
		}

		parts := strings.SplitN(path, "/", 2)
		sessionID := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		// GET /sessions/{id}/stream — SSE stream for session events.
		case action == "stream" && r.Method == http.MethodGet:
			if state.broker == nil {
				http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
				return
			}
			serveSSE(w, r, state.broker, sessionID)
			return

		// GET /sessions/{id}/watch — persistent SSE stream (stays open across tasks).
		case action == "watch" && r.Method == http.MethodGet:
			if state.broker == nil {
				http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
				return
			}
			serveSSEPersistent(w, r, state.broker, sessionID)
			return

		// GET /sessions/{id} — get session with messages.
		case action == "" && r.Method == http.MethodGet:
			detail, err := querySessionDetail(cfg.HistoryDB, sessionID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if detail == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(detail)

		// DELETE /sessions/{id} — archive session.
		case action == "" && r.Method == http.MethodDelete:
			if err := updateSessionStatus(cfg.HistoryDB, sessionID, "archived"); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "session.archive", "http",
				fmt.Sprintf("session=%s", sessionID), clientIP(r))
			w.Write([]byte(`{"status":"archived"}`))

		// POST /sessions/{id}/message — continue a session.
		case action == "message" && r.Method == http.MethodPost:
			var body struct {
				Prompt string `json:"prompt"`
				Async  bool   `json:"async"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
				http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
				return
			}

			sess, err := querySessionByID(cfg.HistoryDB, sessionID)
			if err != nil || sess == nil {
				http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
				return
			}

			// Pre-record user message immediately.
			now := time.Now().Format(time.RFC3339)
			if err := addSessionMessage(cfg.HistoryDB, SessionMessage{
				SessionID: sessionID,
				Role:      "user",
				Content:   truncateStr(body.Prompt, 5000),
				CreatedAt: now,
			}); err != nil {
				logWarn("add user message failed", "session", sessionID, "error", err)
			}
			if err := updateSessionStats(cfg.HistoryDB, sessionID, 0, 0, 0, 1); err != nil {
				logWarn("update session stats failed", "session", sessionID, "error", err)
			}

			// Update session title on first message.
			title := body.Prompt
			if len(title) > 100 {
				title = title[:100]
			}
			if err := updateSessionTitle(cfg.HistoryDB, sessionID, title); err != nil {
				logWarn("update session title failed", "session", sessionID, "error", err)
			}

			// Re-activate session if it was completed.
			if sess.Status == "completed" {
				if err := updateSessionStatus(cfg.HistoryDB, sessionID, "active"); err != nil {
					logWarn("reactivate session failed", "session", sessionID, "error", err)
				}
			}

			task := Task{
				Prompt:    body.Prompt,
				Agent:     sess.Agent,
				SessionID: sessionID,
				Source:    "chat",
			}
			fillDefaults(cfg, &task)
			task.SessionID = sessionID // Override fillDefaults' new UUID.

			if body.Async {
				// Async mode: return task ID immediately, stream via SSE.
				taskID := task.ID
				traceID := traceIDFromContext(r.Context())

				go func() {
					asyncCtx := withTraceID(context.Background(), traceID)
					result := runTask(asyncCtx, cfg, task, state)

					// Record assistant message to session.
					nowDone := time.Now().Format(time.RFC3339)
					msgRole := "assistant"
					content := truncateStr(result.Output, 5000)
					if result.Status != "success" {
						msgRole = "system"
						errMsg := result.Error
						if errMsg == "" {
							errMsg = result.Status
						}
						content = fmt.Sprintf("[%s] %s", result.Status, truncateStr(errMsg, 2000))
					}
					addSessionMessage(cfg.HistoryDB, SessionMessage{
						SessionID: sessionID,
						Role:      msgRole,
						Content:   content,
						CostUSD:   result.CostUSD,
						TokensIn:  result.TokensIn,
						TokensOut: result.TokensOut,
						Model:     result.Model,
						TaskID:    task.ID,
						CreatedAt: nowDone,
					})
					updateSessionStats(cfg.HistoryDB, sessionID, result.CostUSD, result.TokensIn, result.TokensOut, 1)
				}()

				auditLog(cfg.HistoryDB, "session.message.async", "http",
					fmt.Sprintf("session=%s role=%s task=%s", sessionID, sess.Agent, taskID), clientIP(r))
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(map[string]any{
					"taskId":    taskID,
					"sessionId": sessionID,
					"status":    "running",
				})
				return
			}

			// Sync mode (existing behavior for API consumers).
			result := runSingleTask(r.Context(), cfg, task, sem, childSem, sess.Agent)
			taskStart := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
			recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, sess.Agent, task, result,
				taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

			// Record assistant message (user message already pre-recorded above).
			nowDone := time.Now().Format(time.RFC3339)
			msgRole := "assistant"
			content := truncateStr(result.Output, 5000)
			if result.Status != "success" {
				msgRole = "system"
				errMsg := result.Error
				if errMsg == "" {
					errMsg = result.Status
				}
				content = fmt.Sprintf("[%s] %s", result.Status, truncateStr(errMsg, 2000))
			}
			addSessionMessage(cfg.HistoryDB, SessionMessage{
				SessionID: sessionID,
				Role:      msgRole,
				Content:   content,
				CostUSD:   result.CostUSD,
				TokensIn:  result.TokensIn,
				TokensOut: result.TokensOut,
				Model:     result.Model,
				TaskID:    task.ID,
				CreatedAt: nowDone,
			})
			updateSessionStats(cfg.HistoryDB, sessionID, result.CostUSD, result.TokensIn, result.TokensOut, 1)

			auditLog(cfg.HistoryDB, "session.message", "http",
				fmt.Sprintf("session=%s role=%s", sessionID, sess.Agent), clientIP(r))
			json.NewEncoder(w).Encode(result)

		// POST /sessions/{id}/mirror — record external message (no task execution).
		case action == "mirror" && r.Method == http.MethodPost:
			var body struct {
				Role           string  `json:"role"`    // "user" or "assistant"
				Content        string  `json:"content"`
				Model          string  `json:"model"`
				Cost           float64 `json:"cost"`
				TokensIn       int     `json:"tokensIn"`
				TokensOut      int     `json:"tokensOut"`
				DiscordChannel string  `json:"discordChannel"` // optional: forward to this Discord channel ID
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Role == "" || body.Content == "" {
				http.Error(w, `{"error":"role and content required"}`, http.StatusBadRequest)
				return
			}
			if body.Role != "user" && body.Role != "assistant" && body.Role != "system" {
				http.Error(w, `{"error":"role must be user, assistant, or system"}`, http.StatusBadRequest)
				return
			}

			// Check if session exists; create it if not (source=mirror).
			sess, err := querySessionByID(cfg.HistoryDB, sessionID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			now := time.Now().Format(time.RFC3339)
			if sess == nil {
				sess = &Session{
					ID:        sessionID,
					Agent:     "mirror",
					Source:    "mirror",
					Status:    "active",
					Title:     "Mirror session",
					CreatedAt: now,
					UpdatedAt: now,
				}
				if err := createSession(cfg.HistoryDB, *sess); err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
					return
				}
			}

			// Add message to DB.
			if err := addSessionMessage(cfg.HistoryDB, SessionMessage{
				SessionID: sessionID,
				Role:      body.Role,
				Content:   truncateStr(body.Content, 10000),
				CostUSD:   body.Cost,
				TokensIn:  body.TokensIn,
				TokensOut: body.TokensOut,
				Model:     body.Model,
				CreatedAt: now,
			}); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			updateSessionStats(cfg.HistoryDB, sessionID, body.Cost, body.TokensIn, body.TokensOut, 1)

			// Publish SSE event so Dashboard chat sees it in real-time.
			if state.broker != nil {
				event := SSEEvent{
					Type:      SSEOutputChunk,
					SessionID: sessionID,
					Data: map[string]any{
						"role":    body.Role,
						"content": body.Content,
					},
					Timestamp: now,
				}
				publishToSSEBroker(state.broker, event)
			}

			// Forward to Discord if channel specified and bot is configured.
			if body.DiscordChannel != "" && cfg.Discord.Enabled && cfg.Discord.BotToken != "" {
				go func() {
					content := body.Content
					if len(content) > 1900 {
						content = content[:1900] + "\n..."
					}
					prefix := "💬"
					if body.Role == "assistant" {
						prefix = "🤖"
					}
					msg := fmt.Sprintf("%s **[mirror:%s]**\n%s", prefix, body.Role, content)
					if err := cronDiscordSendBotChannel(cfg.Discord.BotToken, body.DiscordChannel, msg); err != nil {
						logWarn("mirror discord forward failed", "session", sessionID, "error", err)
					}
				}()
			}

			auditLog(cfg.HistoryDB, "session.mirror", "http",
				fmt.Sprintf("session=%s role=%s len=%d", sessionID, body.Role, len(body.Content)), clientIP(r))
			json.NewEncoder(w).Encode(map[string]any{
				"status":    "ok",
				"sessionId": sessionID,
			})

		// POST /sessions/{id}/compact — trigger context compaction.
		case action == "compact" && r.Method == http.MethodPost:
			go func() {
				compactCtx, compactCancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer compactCancel()
				if err := compactSession(compactCtx, cfg, cfg.HistoryDB, sessionID, sem, childSem); err != nil {
					logError("compact session error", "session", sessionID, "error", err)
				}
			}()
			auditLog(cfg.HistoryDB, "session.compact", "http",
				fmt.Sprintf("session=%s", sessionID), clientIP(r))
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "compacting"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Skills ---
	mux.HandleFunc("/skills", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		skills := listSkills(cfg)
		json.NewEncoder(w).Encode(skills)
	})

	mux.HandleFunc("/skills/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Parse /skills/<name>/<action>
		path := strings.TrimPrefix(r.URL.Path, "/skills/")
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

		skill := getSkill(cfg, name)
		if skill == nil {
			http.Error(w, fmt.Sprintf(`{"error":"skill %q not found"}`, name), http.StatusNotFound)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		switch action {
		case "run":
			var body struct {
				Vars map[string]string `json:"vars"`
			}
			json.NewDecoder(r.Body).Decode(&body)

			auditLog(cfg.HistoryDB, "skill.run", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))

			result, err := executeSkill(r.Context(), *skill, body.Vars)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(result)

		case "test":
			auditLog(cfg.HistoryDB, "skill.test", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))

			result, err := testSkill(r.Context(), *skill)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(result)

		default:
			http.Error(w, `{"error":"unknown action, use run or test"}`, http.StatusBadRequest)
		}
	})
}
