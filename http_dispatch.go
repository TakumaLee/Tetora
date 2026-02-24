package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (s *Server) registerDispatchRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	state := s.state
	sem := s.sem
	cron := s.cron

	// --- Dashboard SSE Stream ---
	mux.HandleFunc("/events/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if state.broker == nil {
			http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
			return
		}
		serveDashboardSSE(w, r, state.broker)
	})

	// --- Offline Queue ---
	mux.HandleFunc("/queue", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		status := r.URL.Query().Get("status")
		items := queryQueue(cfg.HistoryDB, status)
		if items == nil {
			items = []QueueItem{}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"items":   items,
			"count":   len(items),
			"pending": countPendingQueue(cfg.HistoryDB),
		})
	})

	mux.HandleFunc("/queue/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/queue/")

		// POST /queue/{id}/retry
		if strings.HasSuffix(path, "/retry") {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			idStr := strings.TrimSuffix(path, "/retry")
			id, err := strconv.Atoi(idStr)
			if err != nil {
				http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
				return
			}
			item := queryQueueItem(cfg.HistoryDB, id)
			if item == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			if item.Status != "pending" && item.Status != "failed" {
				http.Error(w, fmt.Sprintf(`{"error":"item status is %q, must be pending or failed"}`, item.Status), http.StatusConflict)
				return
			}

			// Deserialize and re-dispatch.
			var task Task
			if err := json.Unmarshal([]byte(item.TaskJSON), &task); err != nil {
				http.Error(w, `{"error":"invalid task in queue"}`, http.StatusInternalServerError)
				return
			}
			task.ID = newUUID()
			task.SessionID = newUUID()
			task.Source = "queue-retry:" + task.Source

			updateQueueStatus(cfg.HistoryDB, id, "processing", "")
			auditLog(cfg.HistoryDB, "queue.retry", "http", fmt.Sprintf("queueId=%d", id), clientIP(r))

			go func() {
				ctx := withTraceID(context.Background(), newTraceID("queue"))
				result := runSingleTask(ctx, cfg, task, sem, item.RoleName)
				if result.Status == "success" {
					updateQueueStatus(cfg.HistoryDB, id, "completed", "")
				} else {
					incrementQueueRetry(cfg.HistoryDB, id, "failed", result.Error)
				}
				startAt := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
				recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, item.RoleName, task, result,
					startAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
			}()

			w.Write([]byte(fmt.Sprintf(`{"status":"retrying","taskId":%q}`, task.ID)))
			return
		}

		// GET /queue/{id} or DELETE /queue/{id}
		id, err := strconv.Atoi(path)
		if err != nil {
			http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodGet:
			item := queryQueueItem(cfg.HistoryDB, id)
			if item == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(item)

		case http.MethodDelete:
			item := queryQueueItem(cfg.HistoryDB, id)
			if item == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			if err := deleteQueueItem(cfg.HistoryDB, id); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "queue.delete", "http", fmt.Sprintf("queueId=%d", id), clientIP(r))
			w.Write([]byte(`{"status":"deleted"}`))

		default:
			http.Error(w, "GET or DELETE only", http.StatusMethodNotAllowed)
		}
	})

	// --- Dispatch ---
	mux.HandleFunc("/dispatch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		state.mu.Lock()
		busy := state.active
		state.mu.Unlock()
		if busy {
			http.Error(w, `{"error":"dispatch already running"}`, http.StatusConflict)
			return
		}

		var tasks []Task
		if err := json.NewDecoder(r.Body).Decode(&tasks); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		for i := range tasks {
			fillDefaults(cfg, &tasks[i])
			tasks[i].Source = "http"
		}

		// Publish task_received to dashboard.
		if state.broker != nil {
			for _, t := range tasks {
				state.broker.Publish(SSEDashboardKey, SSEEvent{
					Type: SSETaskReceived,
					Data: map[string]any{
						"source": "http",
						"prompt": truncate(t.Prompt, 200),
					},
				})
			}
		}

		auditLog(cfg.HistoryDB, "dispatch", "http",
			fmt.Sprintf("%d tasks", len(tasks)), clientIP(r))

		result := dispatch(r.Context(), cfg, tasks, state, sem)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// --- Cancel ---
	mux.HandleFunc("/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		state.mu.Lock()
		cancelFn := state.cancel
		state.mu.Unlock()
		if cancelFn == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"nothing to cancel"}`))
			return
		}
		cancelFn()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"cancelling"}`))
	})

	// --- Cancel single task ---
	mux.HandleFunc("/cancel/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		id := strings.TrimPrefix(r.URL.Path, "/cancel/")
		if id == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}

		// Try dispatch state first.
		state.mu.Lock()
		if ts, ok := state.running[id]; ok && ts.cancelFn != nil {
			ts.cancelFn()
			state.mu.Unlock()
			auditLog(cfg.HistoryDB, "task.cancel", "http",
				fmt.Sprintf("id=%s (dispatch)", id), clientIP(r))
			w.Write([]byte(`{"status":"cancelling"}`))
			return
		}
		state.mu.Unlock()

		// Try cron engine.
		if cron != nil {
			if err := cron.CancelJob(id); err == nil {
				auditLog(cfg.HistoryDB, "job.cancel", "http",
					fmt.Sprintf("id=%s (cron)", id), clientIP(r))
				w.Write([]byte(`{"status":"cancelling"}`))
				return
			}
		}

		http.Error(w, `{"error":"task not found or not running"}`, http.StatusNotFound)
	})

	// --- Running Tasks ---
	mux.HandleFunc("/tasks/running", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		type runningTask struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Source   string `json:"source"`
			Model    string `json:"model"`
			Timeout  string `json:"timeout"`
			Elapsed  string `json:"elapsed"`
			Prompt   string `json:"prompt,omitempty"`
			PID      int    `json:"pid,omitempty"`
			PIDAlive bool   `json:"pidAlive"`
		}

		var tasks []runningTask

		// From dispatch state.
		state.mu.Lock()
		for _, ts := range state.running {
			prompt := ts.task.Prompt
			if len(prompt) > 100 {
				prompt = prompt[:100] + "..."
			}
			pid := 0
			pidAlive := false
			if ts.cmd != nil && ts.cmd.Process != nil {
				pid = ts.cmd.Process.Pid
				// On Unix, sending signal 0 checks if process exists.
				if ts.cmd.Process.Signal(syscall.Signal(0)) == nil {
					pidAlive = true
				}
			}
			tasks = append(tasks, runningTask{
				ID:       ts.task.ID,
				Name:     ts.task.Name,
				Source:   ts.task.Source,
				Model:    ts.task.Model,
				Timeout:  ts.task.Timeout,
				Elapsed:  time.Since(ts.startAt).Round(time.Second).String(),
				Prompt:   prompt,
				PID:      pid,
				PIDAlive: pidAlive,
			})
		}
		state.mu.Unlock()

		// From cron engine.
		if cron != nil {
			for _, j := range cron.ListJobs() {
				if !j.Running {
					continue
				}
				tasks = append(tasks, runningTask{
					ID:      j.ID,
					Name:    j.Name,
					Source:  "cron",
					Model:   j.RunModel,
					Timeout: j.RunTimeout,
					Elapsed: j.RunElapsed,
					Prompt:  j.RunPrompt,
				})
			}
		}

		if tasks == nil {
			tasks = []runningTask{}
		}
		json.NewEncoder(w).Encode(tasks)
	})

	// --- Tasks (Dashboard DB) ---
	mux.HandleFunc("/tasks", func(w http.ResponseWriter, r *http.Request) {
		if cfg.DashboardDB == "" {
			http.Error(w, `{"error":"dashboard DB not configured"}`, http.StatusServiceUnavailable)
			return
		}

		switch r.Method {
		case http.MethodGet:
			status := r.URL.Query().Get("status")
			if status != "" {
				tasks, err := getTasksByStatus(cfg.DashboardDB, status)
				if err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tasks)
			} else {
				stats, err := getTaskStats(cfg.DashboardDB)
				if err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(stats)
			}

		case http.MethodPatch:
			var body struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  string `json:"error"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			if err := updateTaskStatus(cfg.DashboardDB, body.ID, body.Status, body.Error); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))

		default:
			http.Error(w, "GET or PATCH only", http.StatusMethodNotAllowed)
		}
	})

	// --- Output files ---
	mux.HandleFunc("/outputs/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/outputs/")
		// Strict filename validation: only allow alphanumeric, dash, underscore, dot.
		if name == "" || !isValidOutputFilename(name) {
			http.Error(w, `{"error":"invalid filename"}`, http.StatusBadRequest)
			return
		}
		outputDir := filepath.Join(cfg.baseDir, "outputs")
		filePath := filepath.Join(outputDir, name)
		// Verify resolved path is still within outputs dir (prevent symlink escape).
		absPath, err := filepath.Abs(filePath)
		if err != nil || !strings.HasPrefix(absPath, filepath.Join(cfg.baseDir, "outputs")) {
			http.Error(w, `{"error":"invalid filename"}`, http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			} else {
				http.Error(w, `{"error":"read error"}`, http.StatusInternalServerError)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	// --- File Upload ---
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		// Parse multipart form (max 50MB).
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse form: %s"}`, err), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"no file: %s"}`, err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		uploadDir := initUploadDir(cfg.baseDir)
		uploaded, err := saveUpload(uploadDir, header.Filename, file, header.Size, "http")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}

		auditLog(cfg.HistoryDB, "file.upload", "http", uploaded.Name, clientIP(r))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(uploaded)
	})

	// --- Prompt Library ---
	mux.HandleFunc("/prompts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			prompts, err := listPrompts(cfg)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(prompts)

		case "POST":
			var body struct {
				Name    string `json:"name"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
				return
			}
			if body.Name == "" || body.Content == "" {
				http.Error(w, `{"error":"name and content are required"}`, http.StatusBadRequest)
				return
			}
			if err := writePrompt(cfg, body.Name, body.Content); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "prompt.create", "http", body.Name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": body.Name})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/prompts/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/prompts/")
		if name == "" {
			http.Error(w, `{"error":"prompt name required"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			content, err := readPrompt(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"name": name, "content": content})

		case "DELETE":
			if err := deletePrompt(cfg, name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "prompt.delete", "http", name, clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Cost Estimate ---
	mux.HandleFunc("/dispatch/estimate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		var tasks []Task
		if err := json.NewDecoder(r.Body).Decode(&tasks); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		result := estimateTasks(cfg, tasks)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	// --- Failed Tasks + Retry/Reroute ---
	mux.HandleFunc("/dispatch/failed", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		tasks := listFailedTasks(state)
		if tasks == nil {
			tasks = []failedTaskInfo{}
		}
		json.NewEncoder(w).Encode(tasks)
	})

	mux.HandleFunc("/dispatch/", func(w http.ResponseWriter, r *http.Request) {
		// Parse /dispatch/{id}/{action}
		path := strings.TrimPrefix(r.URL.Path, "/dispatch/")
		if path == "failed" || path == "estimate" {
			return // handled by dedicated handlers
		}
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, `{"error":"path must be /dispatch/{id}/{action}"}`, http.StatusBadRequest)
			return
		}
		taskID, action := parts[0], parts[1]

		// SSE stream endpoint: GET /dispatch/{id}/stream
		if action == "stream" && r.Method == http.MethodGet {
			if state.broker == nil {
				http.Error(w, `{"error":"streaming not available"}`, http.StatusServiceUnavailable)
				return
			}
			serveSSE(w, r, state.broker, taskID)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch action {
		case "retry":
			result, err := retryTask(r.Context(), cfg, taskID, state, sem)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "task.retry", "http",
				fmt.Sprintf("original=%s status=%s", taskID, result.Status), clientIP(r))
			json.NewEncoder(w).Encode(result)

		case "reroute":
			result, err := rerouteTask(r.Context(), cfg, taskID, state, sem)
			if err != nil {
				status := http.StatusNotFound
				if strings.Contains(err.Error(), "not enabled") {
					status = http.StatusBadRequest
				}
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), status)
				return
			}
			auditLog(cfg.HistoryDB, "task.reroute", "http",
				fmt.Sprintf("original=%s role=%s status=%s", taskID, result.Route.Role, result.Task.Status), clientIP(r))
			json.NewEncoder(w).Encode(result)

		default:
			http.Error(w, `{"error":"unknown action, use retry, reroute, or stream"}`, http.StatusBadRequest)
		}
	})

	// --- Smart Dispatch Route ---
	mux.HandleFunc("/route/classify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !cfg.SmartDispatch.Enabled {
			http.Error(w, `{"error":"smart dispatch not enabled"}`, http.StatusBadRequest)
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
			http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
			return
		}
		route := routeTask(r.Context(), cfg, RouteRequest{Prompt: body.Prompt, Source: "http"}, sem)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(route)
	})

	mux.HandleFunc("/route/", func(w http.ResponseWriter, r *http.Request) {
		// Handle /route/classify separately (already registered above, but paths
		// with trailing content after /route/ that aren't "classify" are async IDs).
		path := strings.TrimPrefix(r.URL.Path, "/route/")
		if path == "classify" {
			return // handled by /route/classify handler
		}

		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		// GET /route/{id} — check async route result.
		id := path
		if id == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}

		routeResultsMu.Lock()
		entry, ok := routeResults[id]
		routeResultsMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"id":        id,
			"status":    entry.Status,
			"error":     entry.Error,
			"result":    entry.Result,
			"createdAt": entry.CreatedAt.Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/route", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"enabled":     cfg.SmartDispatch.Enabled,
				"coordinator": cfg.SmartDispatch.Coordinator,
				"defaultRole": cfg.SmartDispatch.DefaultRole,
				"rules":       cfg.SmartDispatch.Rules,
			})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"GET or POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !cfg.SmartDispatch.Enabled {
			http.Error(w, `{"error":"smart dispatch not enabled"}`, http.StatusBadRequest)
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
			Async  bool   `json:"async"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
			http.Error(w, `{"error":"prompt is required"}`, http.StatusBadRequest)
			return
		}
		auditLog(cfg.HistoryDB, "route.request", "http",
			truncate(body.Prompt, 100), clientIP(r))

		if body.Async {
			// Async mode: start in goroutine, return ID immediately.
			id := newUUID()

			routeResultsMu.Lock()
			routeResults[id] = &routeResultEntry{
				Status:    "running",
				CreatedAt: time.Now(),
			}
			routeResultsMu.Unlock()

			routeTraceID := traceIDFromContext(r.Context())
			go func() {
				routeCtx := withTraceID(context.Background(), routeTraceID)
				result := smartDispatch(routeCtx, cfg, body.Prompt, "http", state, sem)
				routeResultsMu.Lock()
				entry := routeResults[id]
				if entry != nil {
					entry.Result = result
					entry.Status = "done"
					if result != nil && result.Task.Status != "success" {
						entry.Error = result.Task.Error
					}
				}
				routeResultsMu.Unlock()
			}()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]any{
				"id":     id,
				"status": "running",
			})
			return
		}

		// Sync mode: block until complete.
		result := smartDispatch(r.Context(), cfg, body.Prompt, "http", state, sem)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})
}
