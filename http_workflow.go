package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// mutateTriggerConfig updates workflowTriggers in config.json and sends SIGHUP to reload.
func mutateTriggerConfig(mutate func(raw map[string]any)) error {
	configPath := findConfigPath()
	if configPath == "" {
		return fmt.Errorf("config path not found")
	}
	if err := updateConfigField(configPath, mutate); err != nil {
		return err
	}
	syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
	return nil
}

func (s *Server) registerWorkflowRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	state := s.state
	sem := s.sem
	childSem := s.childSem

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

	// Import workflow from export package.
	mux.HandleFunc("/api/workflows/import", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		var pkg struct {
			TetoraExport string   `json:"tetoraExport"`
			Workflow     Workflow `json:"workflow"`
		}
		if err := json.NewDecoder(r.Body).Decode(&pkg); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
			return
		}

		// Validate package format.
		if pkg.TetoraExport == "" {
			http.Error(w, `{"error":"not a valid Tetora export package (missing tetoraExport field)"}`, http.StatusBadRequest)
			return
		}

		wf := &pkg.Workflow
		if wf.Name == "" {
			http.Error(w, `{"error":"workflow name is required"}`, http.StatusBadRequest)
			return
		}

		// Validate workflow.
		errs := validateWorkflow(wf)
		if len(errs) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"errors": errs, "valid": false})
			return
		}

		// Save.
		if err := saveWorkflow(cfg, wf); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"save failed: %v"}`, err), http.StatusInternalServerError)
			return
		}

		auditLog(cfg.HistoryDB, "workflow.import", "http",
			fmt.Sprintf("name=%s steps=%d", wf.Name, len(wf.Steps)), clientIP(r))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "imported", "name": wf.Name})
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

		case action == "export" && r.Method == http.MethodGet:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			pkg := map[string]any{
				"tetoraExport": "workflow/v1",
				"exportedAt":   time.Now().UTC().Format(time.RFC3339),
				"workflow":     wf,
			}
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, name))
			json.NewEncoder(w).Encode(pkg)

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

			// Sanitize: strip internal-namespace variables to prevent injection.
			for k := range body.Variables {
				if strings.HasPrefix(k, "__") {
					delete(body.Variables, k)
				}
			}

			auditLog(cfg.HistoryDB, "workflow.run", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))

			// Run asynchronously.
			wfTraceID := traceIDFromContext(r.Context())
			go executeWorkflow(withTraceID(context.Background(), wfTraceID), cfg, wf, body.Variables, state, sem, childSem)

			// Return immediately with run acknowledgment.
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"status":   "accepted",
				"workflow": name,
			})

		case action == "dry-run" && r.Method == http.MethodPost:
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
			for k := range body.Variables {
				if strings.HasPrefix(k, "__") {
					delete(body.Variables, k)
				}
			}
			auditLog(cfg.HistoryDB, "workflow.dry-run", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			// Dry run is synchronous — no real provider calls.
			run := executeWorkflow(r.Context(), cfg, wf, body.Variables, state, sem, childSem, WorkflowModeDryRun)
			json.NewEncoder(w).Encode(run)

		case action == "restore" && r.Method == http.MethodPost:
			if cfg.HistoryDB == "" {
				http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
				return
			}
			var restoreBody struct {
				VersionID string `json:"versionId"`
			}
			json.NewDecoder(r.Body).Decode(&restoreBody)
			if restoreBody.VersionID == "" {
				http.Error(w, `{"error":"versionId required"}`, http.StatusBadRequest)
				return
			}
			if err := restoreWorkflowVersion(cfg.HistoryDB, cfg, restoreBody.VersionID); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			auditLog(cfg.HistoryDB, "workflow.restore", "http",
				fmt.Sprintf("name=%s version=%s", name, restoreBody.VersionID), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "restored", "workflow": name, "versionId": restoreBody.VersionID})

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
			http.Error(w, `{"error":"GET, DELETE, or POST .../validate|run|restore"}`, http.StatusMethodNotAllowed)
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

		path := strings.TrimPrefix(r.URL.Path, "/workflow-runs/")
		parts := strings.SplitN(path, "/", 2)
		runID := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		if runID == "" {
			http.Error(w, `{"error":"run ID required"}`, http.StatusBadRequest)
			return
		}

		// POST /workflow-runs/{id}/cancel
		if action == "cancel" && r.Method == http.MethodPost {
			if cancel, ok := runCancellers.Load(runID); ok {
				cancel.(context.CancelFunc)()
				runCancellers.Delete(runID)
			}
			// Also update DB.
			if _, err := queryDB(cfg.HistoryDB, fmt.Sprintf(
				`UPDATE workflow_runs SET status='cancelled', finished_at=datetime('now') WHERE id='%s' AND status IN ('running','waiting')`,
				escapeSQLite(runID),
			)); err != nil {
				logWarn("cancel workflow run failed", "runID", runID, "error", err)
			}
			auditLog(cfg.HistoryDB, "workflow.cancel", "http",
				fmt.Sprintf("runID=%s", runID), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "cancelled", "runId": runID})
			return
		}

		// POST /workflow-runs/{id}/resume
		if action == "resume" && r.Method == http.MethodPost {
			// Validate the run is resumable before accepting.
			origRun, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			if !isResumableStatus(origRun.Status) {
				http.Error(w, fmt.Sprintf(`{"error":"run status %q is not resumable (must be error/cancelled/timeout)"}`, origRun.Status), http.StatusBadRequest)
				return
			}

			auditLog(cfg.HistoryDB, "workflow.resume", "http",
				fmt.Sprintf("originalRunID=%s", runID), clientIP(r))

			wfTraceID := traceIDFromContext(r.Context())
			go func() {
				run, err := resumeWorkflow(withTraceID(context.Background(), wfTraceID), cfg, runID, state, sem, childSem)
				if err != nil {
					logWarn("workflow resume failed", "originalRunID", runID, "error", err)
				} else {
					logInfo("workflow resume dispatched", "originalRunID", runID, "newRunID", run.ID[:8])
				}
			}()

			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"status":      "accepted",
				"resumedFrom": runID,
			})
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET or POST .../cancel|resume"}`, http.StatusMethodNotAllowed)
			return
		}

		run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
			return
		}
		// Enrich with handoffs, messages, and callbacks.
		handoffs, _ := queryHandoffs(cfg.HistoryDB, run.ID)
		messages, _ := queryAgentMessages(cfg.HistoryDB, run.ID, "", 100)
		if handoffs == nil {
			handoffs = []Handoff{}
		}
		if messages == nil {
			messages = []AgentMessage{}
		}
		// Query callbacks for this run.
		var callbacks []map[string]any
		cbSQL := fmt.Sprintf(`SELECT key, step_id, mode, auth_mode, status, timeout_at, created_at
			FROM workflow_callbacks WHERE run_id='%s' ORDER BY created_at`, escapeSQLite(run.ID))
		cbRows, _ := queryDB(cfg.HistoryDB, cbSQL)
		if cbRows != nil {
			callbacks = cbRows
		} else {
			callbacks = []map[string]any{}
		}
		result := map[string]any{
			"run":       run,
			"handoffs":  handoffs,
			"messages":  messages,
			"callbacks": callbacks,
		}
		json.NewEncoder(w).Encode(result)
	})

	// --- Store Browse ---
	mux.HandleFunc("/api/store/browse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		items, cats := storeBrowse(cfg)
		data, err := storeItemsToJSON(items, cats)
		if err != nil {
			http.Error(w, `{"error":"marshal failed"}`, http.StatusInternalServerError)
			return
		}
		w.Write(data)
	})

	// --- Template Gallery ---
	mux.HandleFunc("/api/templates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		templates := listTemplates()
		if templates == nil {
			templates = []TemplateSummary{}
		}
		json.NewEncoder(w).Encode(map[string]any{"templates": templates, "count": len(templates)})
	})

	mux.HandleFunc("/api/templates/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/api/templates/")
		parts := strings.SplitN(path, "/", 2)
		name := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}
		if name == "" {
			http.Error(w, `{"error":"template name required"}`, http.StatusBadRequest)
			return
		}

		switch {
		case action == "" && r.Method == http.MethodGet:
			wf, err := loadTemplate(name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(wf)

		case action == "install" && r.Method == http.MethodPost:
			var body struct {
				NewName string `json:"newName"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if err := installTemplate(cfg, name, body.NewName); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			installedName := body.NewName
			if installedName == "" {
				installedName = name
			}
			auditLog(cfg.HistoryDB, "template.install", "http",
				fmt.Sprintf("template=%s installed_as=%s", name, installedName), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "installed", "name": installedName})

		default:
			http.Error(w, `{"error":"GET or POST .../install"}`, http.StatusMethodNotAllowed)
		}
	})

	// Use server's trigger engine (shared with main.go, supports hot-reload).
	triggerEngine := s.triggerEngine

	// --- Skill list for editor dropdowns ---
	mux.HandleFunc("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		type SkillInfo struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		dir := skillsDir(cfg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			json.NewEncoder(w).Encode([]SkillInfo{})
			return
		}
		var skills []SkillInfo
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			desc := ""
			// Try metadata.json first.
			metaPath := filepath.Join(dir, name, "metadata.json")
			if data, rerr := os.ReadFile(metaPath); rerr == nil {
				var meta struct {
					Description string `json:"description"`
				}
				if json.Unmarshal(data, &meta) == nil {
					desc = meta.Description
				}
			}
			// Fall back to SKILL.md frontmatter.
			if desc == "" {
				skillPath := filepath.Join(dir, name, "SKILL.md")
				if data, rerr := os.ReadFile(skillPath); rerr == nil {
					content := string(data)
					if strings.HasPrefix(content, "---\n") {
						if end := strings.Index(content[4:], "\n---"); end >= 0 {
							fm := content[4 : 4+end]
							for _, line := range strings.Split(fm, "\n") {
								line = strings.TrimSpace(line)
								if strings.HasPrefix(line, "description:") {
									desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
									desc = strings.Trim(desc, "\"'")
									break
								}
							}
						}
					}
				}
			}
			skills = append(skills, SkillInfo{Name: name, Description: desc})
		}
		if skills == nil {
			skills = []SkillInfo{}
		}
		sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
		json.NewEncoder(w).Encode(skills)
	})

	// /api/tools is registered in http_tools.go (registerToolRoutes)

	mux.HandleFunc("/api/triggers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
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

		case http.MethodPost:
			// Create a new trigger.
			var t WorkflowTriggerConfig
			if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
				return
			}
			// Build existing names set.
			existing := make(map[string]bool)
			currentCfg := s.Cfg()
			for _, et := range currentCfg.WorkflowTriggers {
				existing[et.Name] = true
			}
			if errs := validateTriggerConfig(t, existing); len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			// Persist to config.json + SIGHUP.
			if err := mutateTriggerConfig(func(raw map[string]any) {
				triggers, _ := raw["workflowTriggers"].([]any)
				b, _ := json.Marshal(t)
				var m any
				json.Unmarshal(b, &m)
				raw["workflowTriggers"] = append(triggers, m)
			}); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"save failed: %v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "trigger.create", "http",
				fmt.Sprintf("name=%s type=%s workflow=%s", t.Name, t.Trigger.Type, t.WorkflowName), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "created", "name": t.Name})

		default:
			http.Error(w, `{"error":"GET or POST"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- External Step: List pending callbacks ---
	mux.HandleFunc("/api/callbacks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		sql := `SELECT key, run_id, step_id, mode, auth_mode, status, timeout_at, post_sent, created_at
				FROM workflow_callbacks WHERE status='waiting' ORDER BY created_at DESC LIMIT 100`
		rows, err := queryDB(cfg.HistoryDB, sql)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		json.NewEncoder(w).Encode(map[string]any{"callbacks": rows, "count": len(rows)})
	})

	// --- External Step: Callback endpoint ---
	mux.HandleFunc("/api/callbacks/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		// Extract callback key from path.
		key := strings.TrimPrefix(r.URL.Path, "/api/callbacks/")
		if key == "" {
			http.Error(w, `{"error":"callback key required"}`, http.StatusBadRequest)
			return
		}

		// Validate key format to prevent path traversal and injection.
		if !isValidCallbackKey(key) {
			http.Error(w, `{"error":"invalid callback key format"}`, http.StatusBadRequest)
			return
		}

		// Look up callback record in DB for auth mode.
		record := queryPendingCallback(cfg.HistoryDB, key)
		if record == nil {
			// Check if it was already delivered or completed.
			existing := queryPendingCallbackByKey(cfg.HistoryDB, key)
			if existing != nil && (existing.Status == "delivered" || existing.Status == "completed") {
				json.NewEncoder(w).Encode(map[string]string{"status": "already_delivered"})
				return
			}
			http.Error(w, `{"error":"callback not found or expired"}`, http.StatusNotFound)
			return
		}

		// Read body upfront (1MB limit) — used by both auth verification and callback result.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		if err != nil {
			http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
			return
		}
		if len(body) > 1<<20 {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			json.NewEncoder(w).Encode(map[string]string{"error": "callback body exceeds 1MB"})
			return
		}

		// Auth check based on callback auth mode.
		switch record.AuthMode {
		case "bearer":
			auth := r.Header.Get("Authorization")
			if auth == "" || auth != "Bearer "+cfg.APIToken {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		case "signature":
			// HMAC-SHA256 signature verification.
			sig := r.Header.Get("X-Callback-Signature")
			if sig == "" {
				http.Error(w, `{"error":"missing X-Callback-Signature header"}`, http.StatusUnauthorized)
				return
			}
			secret := callbackSignatureSecret(cfg.APIToken, key)
			if !verifyCallbackSignature(body, secret, sig) {
				http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
				return
			}
		case "open":
			// No auth required.
		default:
			// Default to bearer.
			auth := r.Header.Get("Authorization")
			if cfg.APIToken != "" && (auth == "" || auth != "Bearer "+cfg.APIToken) {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}

		cbResult := CallbackResult{
			Status:      200,
			Body:        string(body),
			ContentType: r.Header.Get("Content-Type"),
			RecvAt:      time.Now().Format(time.RFC3339),
		}

		// Path A: try in-memory delivery first (skip HasChannel pre-check to avoid TOCTOU).
		if callbackMgr != nil {
			out := callbackMgr.DeliverAndSeq(key, cbResult)
			switch out.Result {
			case DeliverOK:
				status := "delivered"
				if out.Mode == "streaming" {
					status = "accumulated"
					// Persist streaming callback immediately for crash recovery (#6).
					// Seq allocated atomically with Deliver to prevent race (#R2-1).
					appendStreamingCallback(cfg.HistoryDB, key, out.Seq, cbResult)
				}
				auditLog(cfg.HistoryDB, "callback."+status, "http",
					fmt.Sprintf("key=%s", key), clientIP(r))
				json.NewEncoder(w).Encode(map[string]string{"status": status})
				return
			case DeliverDup:
				// Single mode: already delivered — idempotent.
				json.NewEncoder(w).Encode(map[string]string{"status": "already_delivered"})
				return
			case DeliverFull:
				// Streaming buffer full — store to DB with seq allocated atomically (#R2-2).
				appendStreamingCallback(cfg.HistoryDB, key, out.Seq, cbResult)
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{"error": "streaming buffer full, stored to DB"})
				return
			case DeliverNoEntry:
				// Channel not registered — fall through to Path B (DB-only).
			}
		}

		// Path B: channel not alive — record to DB for recovery.
		// Re-check status to avoid overwriting completed/delivered records (#R2-8).
		current := queryPendingCallbackByKey(cfg.HistoryDB, key)
		if current != nil && (current.Status == "completed" || current.Status == "delivered") {
			json.NewEncoder(w).Encode(map[string]string{"status": "already_delivered"})
			return
		}
		markCallbackDelivered(cfg.HistoryDB, key, 0, cbResult)
		auditLog(cfg.HistoryDB, "callback.stored", "http",
			fmt.Sprintf("key=%s (no active channel)", key), clientIP(r))
		json.NewEncoder(w).Encode(map[string]string{"status": "stored"})
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

		case action == "toggle" && r.Method == http.MethodPost:
			var newEnabled bool
			if err := mutateTriggerConfig(func(raw map[string]any) {
				triggers, _ := raw["workflowTriggers"].([]any)
				for _, t := range triggers {
					tm, _ := t.(map[string]any)
					if tm["name"] == name {
						cur, _ := tm["enabled"].(bool)
						if _, ok := tm["enabled"]; !ok {
							cur = true // default is enabled
						}
						newEnabled = !cur
						tm["enabled"] = newEnabled
						break
					}
				}
			}); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"toggle failed: %v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "trigger.toggle", "http",
				fmt.Sprintf("trigger=%s enabled=%v", name, newEnabled), clientIP(r))
			json.NewEncoder(w).Encode(map[string]any{"status": "toggled", "name": name, "enabled": newEnabled})

		case action == "" && r.Method == http.MethodPut:
			// Update trigger.
			var t WorkflowTriggerConfig
			if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
				return
			}
			t.Name = name // enforce URL name
			if errs := validateTriggerConfig(t, nil); len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			found := false
			if err := mutateTriggerConfig(func(raw map[string]any) {
				triggers, _ := raw["workflowTriggers"].([]any)
				b, _ := json.Marshal(t)
				var m any
				json.Unmarshal(b, &m)
				for i, tr := range triggers {
					tm, _ := tr.(map[string]any)
					if tm["name"] == name {
						triggers[i] = m
						found = true
						break
					}
				}
				raw["workflowTriggers"] = triggers
			}); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"update failed: %v"}`, err), http.StatusInternalServerError)
				return
			}
			if !found {
				http.Error(w, `{"error":"trigger not found"}`, http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "trigger.update", "http",
				fmt.Sprintf("trigger=%s", name), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "updated", "name": name})

		case action == "" && r.Method == http.MethodDelete:
			found := false
			if err := mutateTriggerConfig(func(raw map[string]any) {
				triggers, _ := raw["workflowTriggers"].([]any)
				for i, tr := range triggers {
					tm, _ := tr.(map[string]any)
					if tm["name"] == name {
						raw["workflowTriggers"] = append(triggers[:i], triggers[i+1:]...)
						found = true
						break
					}
				}
			}); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"delete failed: %v"}`, err), http.StatusInternalServerError)
				return
			}
			if !found {
				http.Error(w, `{"error":"trigger not found"}`, http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "trigger.delete", "http",
				fmt.Sprintf("trigger=%s", name), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})

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
			http.Error(w, `{"error":"use POST .../fire|toggle, PUT, DELETE, or GET .../runs"}`, http.StatusMethodNotAllowed)
		}
	})
}
