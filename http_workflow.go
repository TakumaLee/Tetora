package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) registerWorkflowRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	state := s.state
	sem := s.sem

	// --- Workflows ---
	mux.HandleFunc("/workflows", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			workflows, err := listWorkflows(cfg)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if workflows == nil {
				workflows = []*Workflow{}
			}
			json.NewEncoder(w).Encode(workflows)

		case http.MethodPost:
			var wf Workflow
			if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
				return
			}
			errs := validateWorkflow(&wf)
			if len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			if err := saveWorkflow(cfg, &wf); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "workflow.create", "http",
				fmt.Sprintf("name=%s steps=%d", wf.Name, len(wf.Steps)), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "created", "name": wf.Name})

		default:
			http.Error(w, `{"error":"GET or POST"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/workflows/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		name := strings.TrimPrefix(r.URL.Path, "/workflows/")
		if name == "" {
			http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
			return
		}

		// Strip sub-paths (e.g. /workflows/name/validate).
		parts := strings.SplitN(name, "/", 2)
		name = parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		case action == "validate" && r.Method == http.MethodPost:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			errs := validateWorkflow(wf)
			valid := len(errs) == 0
			resp := map[string]any{"valid": valid, "name": wf.Name}
			if !valid {
				resp["errors"] = errs
			} else {
				resp["executionOrder"] = topologicalSort(wf.Steps)
			}
			json.NewEncoder(w).Encode(resp)

		case action == "" && r.Method == http.MethodGet:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(wf)

		case action == "" && r.Method == http.MethodDelete:
			if err := deleteWorkflow(cfg, name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "workflow.delete", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})

		case action == "run" && r.Method == http.MethodPost:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			if errs := validateWorkflow(wf); len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			var body struct {
				Variables map[string]string `json:"variables"`
			}
			json.NewDecoder(r.Body).Decode(&body)

			auditLog(cfg.HistoryDB, "workflow.run", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))

			// Run asynchronously.
			wfTraceID := traceIDFromContext(r.Context())
			go executeWorkflow(withTraceID(context.Background(), wfTraceID), cfg, wf, body.Variables, state, sem)

			// Return immediately with run acknowledgment.
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"status":   "accepted",
				"workflow": name,
			})

		case action == "runs" && r.Method == http.MethodGet:
			runs, err := queryWorkflowRuns(cfg.HistoryDB, 20, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if runs == nil {
				runs = []WorkflowRun{}
			}
			json.NewEncoder(w).Encode(runs)

		default:
			http.Error(w, `{"error":"GET, DELETE, or POST .../validate|run"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Workflow Runs ---
	mux.HandleFunc("/workflow-runs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("workflow")
		runs, err := queryWorkflowRuns(cfg.HistoryDB, 20, name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if runs == nil {
			runs = []WorkflowRun{}
		}
		json.NewEncoder(w).Encode(runs)
	})

	mux.HandleFunc("/workflow-runs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		runID := strings.TrimPrefix(r.URL.Path, "/workflow-runs/")
		if runID == "" {
			http.Error(w, `{"error":"run ID required"}`, http.StatusBadRequest)
			return
		}
		run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
			return
		}
		// Enrich with handoffs and messages.
		handoffs, _ := queryHandoffs(cfg.HistoryDB, run.ID)
		messages, _ := queryAgentMessages(cfg.HistoryDB, run.ID, "", 100)
		if handoffs == nil {
			handoffs = []Handoff{}
		}
		if messages == nil {
			messages = []AgentMessage{}
		}
		result := map[string]any{
			"run":      run,
			"handoffs": handoffs,
			"messages": messages,
		}
		json.NewEncoder(w).Encode(result)
	})

	// --- P18.3: Workflow Triggers ---
	// Build trigger engine reference for HTTP handlers.
	var triggerEngine *WorkflowTriggerEngine
	if len(cfg.WorkflowTriggers) > 0 {
		triggerEngine = newWorkflowTriggerEngine(cfg, state, sem, state.broker)
	}

	mux.HandleFunc("/api/triggers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if triggerEngine == nil {
			json.NewEncoder(w).Encode(map[string]any{"triggers": []any{}, "count": 0})
			return
		}
		infos := triggerEngine.ListTriggers()
		if infos == nil {
			infos = []TriggerInfo{}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"triggers": infos,
			"count":    len(infos),
		})
	})

	mux.HandleFunc("/api/triggers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/api/triggers/")
		parts := strings.SplitN(path, "/", 2)
		name := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		if name == "" {
			http.Error(w, `{"error":"trigger name required"}`, http.StatusBadRequest)
			return
		}

		// Handle webhook trigger: POST /api/triggers/webhook/{id}
		if name == "webhook" && action != "" && r.Method == http.MethodPost {
			webhookID := action
			if triggerEngine == nil {
				http.Error(w, `{"error":"no triggers configured"}`, http.StatusNotFound)
				return
			}
			// Parse JSON payload into vars.
			var payload map[string]string
			if r.Body != nil {
				json.NewDecoder(r.Body).Decode(&payload)
			}
			if payload == nil {
				payload = make(map[string]string)
			}
			payload["_webhook_remote"] = clientIP(r)

			if err := triggerEngine.HandleWebhookTrigger(webhookID, payload); err != nil {
				status := http.StatusNotFound
				if strings.Contains(err.Error(), "cooldown") || strings.Contains(err.Error(), "disabled") {
					status = http.StatusTooManyRequests
				}
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), status)
				return
			}
			auditLog(cfg.HistoryDB, "trigger.webhook", "http",
				fmt.Sprintf("trigger=%s", webhookID), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "accepted",
				"trigger": webhookID,
			})
			return
		}

		switch {
		case action == "fire" && r.Method == http.MethodPost:
			if triggerEngine == nil {
				http.Error(w, `{"error":"no triggers configured"}`, http.StatusNotFound)
				return
			}
			if err := triggerEngine.FireTrigger(name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "trigger.fire", "http",
				fmt.Sprintf("trigger=%s", name), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "accepted",
				"trigger": name,
			})

		case action == "runs" && r.Method == http.MethodGet:
			limit := 20
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
					limit = n
				}
			}
			runs, err := queryTriggerRuns(cfg.HistoryDB, name, limit)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if runs == nil {
				runs = []map[string]any{}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"runs":  runs,
				"count": len(runs),
			})

		default:
			http.Error(w, `{"error":"use POST .../fire or GET .../runs"}`, http.StatusMethodNotAllowed)
		}
	})
}
