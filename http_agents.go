package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) registerAgentRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	state := s.state
	sem := s.sem

	// --- Agent Messages ---
	mux.HandleFunc("/agent-messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			workflowRun := r.URL.Query().Get("workflowRun")
			role := r.URL.Query().Get("role")
			limit := 50
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 {
					limit = n
				}
			}
			msgs, err := queryAgentMessages(cfg.HistoryDB, workflowRun, role, limit)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if msgs == nil {
				msgs = []AgentMessage{}
			}
			json.NewEncoder(w).Encode(msgs)

		case http.MethodPost:
			var msg AgentMessage
			if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if msg.FromAgent == "" || msg.ToAgent == "" || msg.Content == "" {
				http.Error(w, `{"error":"fromAgent, toAgent, and content are required"}`, http.StatusBadRequest)
				return
			}
			if msg.Type == "" {
				msg.Type = "note"
			}
			if err := sendAgentMessage(cfg.HistoryDB, msg); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "agent.message", "http",
				fmt.Sprintf("%s→%s type=%s", msg.FromAgent, msg.ToAgent, msg.Type), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "sent", "id": msg.ID})

		default:
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/handoffs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		workflowRun := r.URL.Query().Get("workflowRun")
		handoffs, err := queryHandoffs(cfg.HistoryDB, workflowRun)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if handoffs == nil {
			handoffs = []Handoff{}
		}
		json.NewEncoder(w).Encode(handoffs)
	})

	// --- P14.6: Task Board ---
	var taskBoardEngine *TaskBoardEngine
	var taskBoardDispatcher *TaskBoardDispatcher
	if cfg.TaskBoard.Enabled {
		taskBoardEngine = newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
		if err := taskBoardEngine.initTaskBoardSchema(); err != nil {
			logError("init task board schema failed", "error", err)
		}

		// Start auto-dispatcher if enabled (singleton — one per server).
		if cfg.TaskBoard.AutoDispatch.Enabled {
			taskBoardDispatcher = newTaskBoardDispatcher(taskBoardEngine, cfg, sem, state)
			taskBoardDispatcher.Start()
		}
	}
	_ = taskBoardDispatcher // available for graceful shutdown

	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		if taskBoardEngine == nil {
			http.Error(w, `{"error":"task board not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			status := r.URL.Query().Get("status")
			assignee := r.URL.Query().Get("assignee")
			project := r.URL.Query().Get("project")
			tasks, err := taskBoardEngine.ListTasks(status, assignee, project)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if tasks == nil {
				tasks = []TaskBoard{}
			}
			json.NewEncoder(w).Encode(map[string]any{"tasks": tasks})

		case http.MethodPost:
			var task TaskBoard
			if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
				return
			}
			created, err := taskBoardEngine.CreateTask(task)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(created)

		default:
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/tasks/board", func(w http.ResponseWriter, r *http.Request) {
		if taskBoardEngine == nil {
			http.Error(w, `{"error":"task board not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		project := r.URL.Query().Get("project")
		assignee := r.URL.Query().Get("assignee")
		priority := r.URL.Query().Get("priority")
		board, err := taskBoardEngine.GetBoardView(project, assignee, priority)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(board)
	})

	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		if taskBoardEngine == nil {
			http.Error(w, `{"error":"task board not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		parts := strings.Split(path, "/")
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
			return
		}

		taskID := parts[0]

		// GET /api/tasks/{id} → get single task.
		if r.Method == http.MethodGet && len(parts) == 1 {
			task, err := taskBoardEngine.GetTask(taskID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(task)
			return
		}

		// PATCH /api/tasks/{id} → update task.
		if r.Method == http.MethodPatch && len(parts) == 1 {
			var updates map[string]any
			if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
				return
			}
			task, err := taskBoardEngine.UpdateTask(taskID, updates)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(task)
			return
		}

		// POST /api/tasks/{id}/move → move task.
		if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "move" {
			var req struct {
				Status string `json:"status"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
				return
			}
			task, err := taskBoardEngine.MoveTask(taskID, req.Status)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(task)
			return
		}

		// POST /api/tasks/{id}/assign → assign task.
		if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "assign" {
			var req struct {
				Assignee string `json:"assignee"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
				return
			}
			task, err := taskBoardEngine.AssignTask(taskID, req.Assignee)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(task)
			return
		}

		// POST /api/tasks/{id}/comment → add comment.
		if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "comment" {
			var req struct {
				Author  string `json:"author"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
				return
			}
			comment, err := taskBoardEngine.AddComment(taskID, req.Author, req.Content)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(comment)
			return
		}

		// GET /api/tasks/{id}/thread → get comments.
		if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "thread" {
			comments, err := taskBoardEngine.GetThread(taskID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if comments == nil {
				comments = []TaskComment{}
			}
			json.NewEncoder(w).Encode(map[string]any{"comments": comments})
			return
		}

		http.Error(w, `{"error":"invalid path or method"}`, http.StatusBadRequest)
	})

	// --- Quick Actions ---
	quickActionEngine := newQuickActionEngine(cfg)

	mux.HandleFunc("/api/quick/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		actions := quickActionEngine.List()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(actions)
	})

	mux.HandleFunc("/api/quick/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()

		var req struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid request: %v"}`, err), http.StatusBadRequest)
			return
		}

		// Build prompt from action.
		prompt, role, err := quickActionEngine.BuildPrompt(req.Name, req.Params)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		// Create task.
		task := Task{
			Name:   "quick:" + req.Name,
			Prompt: prompt,
			Agent:   role,
			Source: "quick:" + req.Name,
		}
		fillDefaults(cfg, &task)

		// Dispatch task.
		tasks := []Task{task}
		result := dispatch(ctx, cfg, tasks, state, sem)

		if len(result.Tasks) == 0 {
			http.Error(w, `{"error":"no result"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result.Tasks[0])
	})

	mux.HandleFunc("/api/quick/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		query := r.URL.Query().Get("q")
		actions := quickActionEngine.Search(query)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(actions)
	})

	// --- Agent Communication ---
	mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		result, err := toolAgentList(r.Context(), cfg, json.RawMessage(`{}`))
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(result))
	})

	mux.HandleFunc("/api/agents/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		role := r.URL.Query().Get("role")
		if role == "" {
			http.Error(w, `{"error":"role parameter required"}`, http.StatusBadRequest)
			return
		}

		markAsRead := r.URL.Query().Get("markAsRead") == "true"

		messages, err := getAgentMessages(cfg.HistoryDB, role, markAsRead)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"messages": messages,
			"count":    len(messages),
		})
	})

	mux.HandleFunc("/api/agents/message", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Agent      string `json:"agent"`
			Message   string `json:"message"`
			SessionID string `json:"sessionId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}

		input, _ := json.Marshal(req)
		result, err := toolAgentMessage(r.Context(), cfg, input)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(result))
	})

	// --- P0.3: Running Agents ---
	mux.HandleFunc("/api/agents/running", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		type runningTask struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Agent     string `json:"agent,omitempty"`
			Source   string `json:"source,omitempty"`
			Prompt   string `json:"prompt,omitempty"`
			Elapsed  string `json:"elapsed"`
			ParentID string `json:"parentId,omitempty"`
			Depth    int    `json:"depth,omitempty"`
		}

		var tasks []runningTask
		if state != nil {
			state.mu.Lock()
			for _, ts := range state.running {
				prompt := ts.task.Prompt
				if len(prompt) > 100 {
					prompt = prompt[:100] + "..."
				}
				tasks = append(tasks, runningTask{
					ID:       ts.task.ID,
					Name:     ts.task.Name,
					Agent:     ts.task.Agent,
					Source:   ts.task.Source,
					Prompt:   prompt,
					Elapsed:  time.Since(ts.startAt).Round(time.Second).String(),
					ParentID: ts.task.ParentID,
					Depth:    ts.task.Depth,
				})
			}
			state.mu.Unlock()
		}

		if tasks == nil {
			tasks = []runningTask{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"running": tasks,
			"count":   len(tasks),
		})
	})

	// --- Trust Gradient ---
	mux.HandleFunc("/trust", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		statuses := getAllTrustStatuses(cfg)
		json.NewEncoder(w).Encode(statuses)
	})

	mux.HandleFunc("/trust/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		agentName := strings.TrimPrefix(r.URL.Path, "/trust/")
		agentName = strings.TrimSuffix(agentName, "/")
		if agentName == "" {
			http.Error(w, `{"error":"agent name required"}`, http.StatusBadRequest)
			return
		}

		// Check if agent exists.
		if _, ok := cfg.Agents[agentName]; !ok {
			http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
			return
		}

		switch r.Method {
		case http.MethodGet:
			status := getTrustStatus(cfg, agentName)
			json.NewEncoder(w).Encode(status)

		case http.MethodPost, http.MethodPut:
			var body struct {
				Level string `json:"level"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if !isValidTrustLevel(body.Level) {
				http.Error(w, fmt.Sprintf(`{"error":"invalid level, valid: %s"}`,
					strings.Join(validTrustLevels, ", ")), http.StatusBadRequest)
				return
			}

			oldLevel := resolveTrustLevel(cfg, agentName)
			if err := updateAgentTrustLevel(cfg, agentName, body.Level); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}

			// Persist to config.json.
			configPath := filepath.Join(cfg.baseDir, "config.json")
			if err := saveAgentTrustLevel(configPath, agentName, body.Level); err != nil {
				logWarn("persist trust level failed", "agent", agentName, "error", err)
			}

			// Record trust event.
			recordTrustEvent(cfg.HistoryDB, agentName, "set", oldLevel, body.Level, 0,
				"set via API")

			auditLog(cfg.HistoryDB, "trust.set", "http",
				fmt.Sprintf("agent=%s from=%s to=%s", agentName, oldLevel, body.Level), clientIP(r))

			json.NewEncoder(w).Encode(getTrustStatus(cfg, agentName))

		default:
			http.Error(w, `{"error":"GET or POST"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/trust-events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		role := r.URL.Query().Get("role")
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		events, err := queryTrustEvents(cfg.HistoryDB, role, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if events == nil {
			events = []map[string]any{}
		}
		json.NewEncoder(w).Encode(events)
	})
}
