package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"tetora/internal/audit"
	"tetora/internal/db"
	"tetora/internal/httputil"
	"tetora/internal/log"
)

// WorkflowDeps holds all dependencies for workflow HTTP handlers.
type WorkflowDeps struct {
	HistoryDB func() string
	APIToken  func() string

	// Workflow CRUD
	ListWorkflows func() (any, error)
	// SaveWorkflow decodes the request body, validates, and saves. Returns (name, stepCount, validationErrors, error).
	SaveWorkflow func(body json.RawMessage) (name string, stepCount int, validationErrs []string, err error)
	// LoadWorkflow loads a workflow by name. Returns the workflow value or error.
	LoadWorkflow func(name string) (any, error)
	// DeleteWorkflow deletes a workflow by name.
	DeleteWorkflow func(name string) error
	// ExportWorkflow loads and wraps a workflow in an export envelope.
	ExportWorkflow func(name string) (any, error)
	// ValidateWorkflow validates a loaded workflow by name. Returns (wfName, executionOrder, validationErrors, error).
	ValidateWorkflow func(name string) (wfName string, executionOrder any, errs []string, err error)

	// Workflow execution
	// RunWorkflow starts an async run. The context carries the trace ID.
	RunWorkflow func(ctx context.Context, name string, vars map[string]string)
	// DryRunWorkflow runs synchronously and returns the run result.
	DryRunWorkflow func(ctx context.Context, name string, vars map[string]string) (any, error)
	// ResumeWorkflow resumes an errored/cancelled run asynchronously.
	ResumeWorkflow func(ctx context.Context, runID string)
	// CancelRun cancels an in-progress run.
	CancelRun func(runID string)

	// Workflow versioning
	RestoreWorkflowVersion func(historyDB, versionID string) error

	// Workflow run queries.
	// QueryWorkflowRuns returns the run list as any (root-typed struct slice, JSON-safe).
	QueryWorkflowRuns func(historyDB string, limit int, name string) (any, error)
	// QueryWorkflowRunByID returns a single run as any (root-typed struct, JSON-safe).
	QueryWorkflowRunByID func(historyDB, runID string) (any, error)
	IsResumableStatus    func(status string) bool
	// QueryHandoffs returns handoffs as any (root-typed struct slice, JSON-safe).
	QueryHandoffs func(historyDB, runID string) (any, error)
	// QueryAgentMessages returns messages as any (root-typed struct slice, JSON-safe).
	QueryAgentMessages func(historyDB, runID string, limit int) (any, error)

	// Import (from export package)
	// ImportWorkflow validates and saves an import package. Returns (name, stepCount, validationErrors, error).
	ImportWorkflow func(body json.RawMessage) (name string, stepCount int, validationErrs []string, err error)

	// Store
	StoreBrowse func() ([]byte, error) // returns pre-marshalled JSON

	// Templates
	// ListTemplates returns the template list as any (root-typed struct slice, JSON-safe) + count.
	ListTemplates   func() (any, int)
	LoadTemplate    func(name string) (any, error)
	InstallTemplate func(name, newName string) error

	// Skills list (for editor dropdowns)
	ListSkillInfos func() []SkillInfo

	// Triggers
	TriggerEngineAvailable bool
	// ListTriggers returns the trigger info list as any (root-typed struct slice, JSON-safe).
	ListTriggers func() (any, int)
	HandleWebhookTrigger   func(webhookID string, payload map[string]string) error
	FireTrigger            func(name string) error
	GetCurrentTriggerNames func() []string // returns existing trigger names for duplicate check
	// ValidateTriggerConfig decodes body and validates. existingNames may be nil for updates.
	ValidateTriggerConfig func(body json.RawMessage, existingNames map[string]bool) []string
	MutateTriggerConfig   func(mutate func(raw map[string]any)) error
	// DecodeTriggerConfig extracts name/type/workflow from a trigger config body for audit logging.
	DecodeTriggerConfig func(body json.RawMessage) (name, typ, workflow string, err error)
	// QueryTriggerRuns returns []map[string]any (pure stdlib, no root types).
	QueryTriggerRuns func(historyDB, name string, limit int) ([]map[string]any, error)

	// Callbacks (external step)
	IsValidCallbackKey        func(key string) bool
	QueryPendingCallback      func(historyDB, key string) *PendingCallbackRecord
	QueryPendingCallbackByKey func(historyDB, key string) *PendingCallbackRecord
	CallbackSignatureSecret   func(apiToken, key string) string
	VerifyCallbackSignature   func(body []byte, secret, sig string) bool
	DeliverCallback           func(key string, result CallbackPayload) DeliverCallbackResult
	AppendStreamingCallback   func(historyDB, key string, seq int, result CallbackPayload)
	MarkCallbackDelivered     func(historyDB, key string, seq int, result CallbackPayload)
}

// SkillInfo is the shape returned by /api/skills.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// PendingCallbackRecord is the minimal shape needed for auth/status checks.
type PendingCallbackRecord struct {
	Status   string
	AuthMode string
}

// CallbackPayload carries the data of a single external-step callback POST.
type CallbackPayload struct {
	Status      int
	Body        string
	ContentType string
	RecvAt      string
}

// DeliverCallbackResult is the outcome of attempting an in-memory delivery.
type DeliverCallbackResult struct {
	// Outcome is one of: "ok", "dup", "full", "no_entry"
	Outcome string
	// Mode is "streaming" or "single"; set when Outcome == "ok".
	Mode string
	// Seq is the sequence number allocated on ok/full for streaming.
	Seq int
}

// RegisterWorkflowRoutes registers all workflow, template, skill, trigger,
// store, and external-step-callback HTTP routes.
func RegisterWorkflowRoutes(mux *http.ServeMux, d WorkflowDeps) {
	// --- Workflows ---
	mux.HandleFunc("/workflows", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			workflows, err := d.ListWorkflows()
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if workflows == nil {
				workflows = []any{}
			}
			json.NewEncoder(w).Encode(workflows)

		case http.MethodPost:
			body, err := readBody(r)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
				return
			}
			name, stepCount, validationErrs, serr := d.SaveWorkflow(body)
			if len(validationErrs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": validationErrs})
				return
			}
			if serr != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, serr), http.StatusInternalServerError)
				return
			}
			audit.Log(d.HistoryDB(), "workflow.create", "http",
				fmt.Sprintf("name=%s steps=%d", name, stepCount), httputil.ClientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "created", "name": name})

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

		body, err := readBody(r)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
			return
		}

		name, stepCount, validationErrs, ierr := d.ImportWorkflow(body)
		if len(validationErrs) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"errors": validationErrs, "valid": false})
			return
		}
		if ierr != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, ierr), http.StatusBadRequest)
			return
		}

		audit.Log(d.HistoryDB(), "workflow.import", "http",
			fmt.Sprintf("name=%s steps=%d", name, stepCount), httputil.ClientIP(r))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "imported", "name": name})
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
			wfName, executionOrder, errs, err := d.ValidateWorkflow(name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			valid := len(errs) == 0
			resp := map[string]any{"valid": valid, "name": wfName}
			if !valid {
				resp["errors"] = errs
			} else {
				resp["executionOrder"] = executionOrder
			}
			json.NewEncoder(w).Encode(resp)

		case action == "" && r.Method == http.MethodGet:
			wf, err := d.LoadWorkflow(name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(wf)

		case action == "export" && r.Method == http.MethodGet:
			pkg, err := d.ExportWorkflow(name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, name))
			json.NewEncoder(w).Encode(pkg)

		case action == "" && r.Method == http.MethodDelete:
			if err := d.DeleteWorkflow(name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			audit.Log(d.HistoryDB(), "workflow.delete", "http",
				fmt.Sprintf("name=%s", name), httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})

		case action == "run" && r.Method == http.MethodPost:
			// Validate before running.
			_, _, errs, verr := d.ValidateWorkflow(name)
			if verr != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, verr), http.StatusNotFound)
				return
			}
			if len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			var runBody struct {
				Variables map[string]string `json:"variables"`
			}
			json.NewDecoder(r.Body).Decode(&runBody)
			// Sanitize: strip internal-namespace variables to prevent injection.
			for k := range runBody.Variables {
				if strings.HasPrefix(k, "__") {
					delete(runBody.Variables, k)
				}
			}
			audit.Log(d.HistoryDB(), "workflow.run", "http",
				fmt.Sprintf("name=%s", name), httputil.ClientIP(r))
			// Run asynchronously — context carries trace ID.
			d.RunWorkflow(r.Context(), name, runBody.Variables)
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"status":   "accepted",
				"workflow": name,
			})

		case action == "dry-run" && r.Method == http.MethodPost:
			_, _, errs, verr := d.ValidateWorkflow(name)
			if verr != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, verr), http.StatusNotFound)
				return
			}
			if len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			var dryBody struct {
				Variables map[string]string `json:"variables"`
			}
			json.NewDecoder(r.Body).Decode(&dryBody)
			for k := range dryBody.Variables {
				if strings.HasPrefix(k, "__") {
					delete(dryBody.Variables, k)
				}
			}
			audit.Log(d.HistoryDB(), "workflow.dry-run", "http",
				fmt.Sprintf("name=%s", name), httputil.ClientIP(r))
			run, err := d.DryRunWorkflow(r.Context(), name, dryBody.Variables)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(run)

		case action == "restore" && r.Method == http.MethodPost:
			historyDB := d.HistoryDB()
			if historyDB == "" {
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
			if err := d.RestoreWorkflowVersion(historyDB, restoreBody.VersionID); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			audit.Log(d.HistoryDB(), "workflow.restore", "http",
				fmt.Sprintf("name=%s version=%s", name, restoreBody.VersionID), httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "restored", "workflow": name, "versionId": restoreBody.VersionID})

		case action == "runs" && r.Method == http.MethodGet:
			runs, err := d.QueryWorkflowRuns(d.HistoryDB(), 20, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if runs == nil {
				runs = []any{}
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
		runs, err := d.QueryWorkflowRuns(d.HistoryDB(), 20, name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if runs == nil {
			runs = []any{}
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
			d.CancelRun(runID)
			// Also update DB.
			if _, err := db.Query(d.HistoryDB(), fmt.Sprintf(
				`UPDATE workflow_runs SET status='cancelled', finished_at=datetime('now') WHERE id='%s' AND status IN ('running','waiting')`,
				db.Escape(runID),
			)); err != nil {
				log.Warn("cancel workflow run failed", "runID", runID, "error", err)
			}
			audit.Log(d.HistoryDB(), "workflow.cancel", "http",
				fmt.Sprintf("runID=%s", runID), httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "cancelled", "runId": runID})
			return
		}

		// POST /workflow-runs/{id}/resume
		if action == "resume" && r.Method == http.MethodPost {
			// Validate the run is resumable before accepting.
			origRun, err := d.QueryWorkflowRunByID(d.HistoryDB(), runID)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			// Extract status field for resumability check.
			status := ""
			if m, ok := origRun.(map[string]any); ok {
				status, _ = m["status"].(string)
			} else {
				// Encode/re-decode as map to extract status generically.
				var m map[string]any
				if b, merr := json.Marshal(origRun); merr == nil {
					json.Unmarshal(b, &m)
					status, _ = m["status"].(string)
				}
			}
			if !d.IsResumableStatus(status) {
				http.Error(w, fmt.Sprintf(`{"error":"run status %q is not resumable (must be error/cancelled/timeout)"}`, status), http.StatusBadRequest)
				return
			}

			audit.Log(d.HistoryDB(), "workflow.resume", "http",
				fmt.Sprintf("originalRunID=%s", runID), httputil.ClientIP(r))

			d.ResumeWorkflow(r.Context(), runID)

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

		run, err := d.QueryWorkflowRunByID(d.HistoryDB(), runID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
			return
		}
		// Enrich with handoffs, messages, and callbacks.
		handoffs, _ := d.QueryHandoffs(d.HistoryDB(), runID)
		messages, _ := d.QueryAgentMessages(d.HistoryDB(), runID, 100)
		if handoffs == nil {
			handoffs = []any{}
		}
		if messages == nil {
			messages = []any{}
		}
		// Query callbacks for this run.
		cbSQL := fmt.Sprintf(`SELECT key, step_id, mode, auth_mode, status, timeout_at, created_at
			FROM workflow_callbacks WHERE run_id='%s' ORDER BY created_at`, db.Escape(runID))
		callbacks, _ := db.Query(d.HistoryDB(), cbSQL)
		if callbacks == nil {
			callbacks = []map[string]any{}
		}
		// Query human gates for this run.
		hgSQL := fmt.Sprintf(`SELECT key, step_id, subtype, prompt, assignee, status, COALESCE(decision,'') as decision, COALESCE(response,'') as response, COALESCE(responded_by,'') as respondedBy, COALESCE(timeout_at,'') as timeoutAt, created_at as createdAt, COALESCE(completed_at,'') as completedAt
			FROM workflow_human_gates WHERE run_id='%s' ORDER BY created_at`, db.Escape(runID))
		humanGates, _ := db.Query(d.HistoryDB(), hgSQL)
		if humanGates == nil {
			humanGates = []map[string]any{}
		}
		result := map[string]any{
			"run":        run,
			"handoffs":   handoffs,
			"messages":   messages,
			"callbacks":  callbacks,
			"humanGates": humanGates,
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
		data, err := d.StoreBrowse()
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
		templates, count := d.ListTemplates()
		if templates == nil {
			templates = []any{}
		}
		json.NewEncoder(w).Encode(map[string]any{"templates": templates, "count": count})
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
			wf, err := d.LoadTemplate(name)
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
			if err := d.InstallTemplate(name, body.NewName); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			installedName := body.NewName
			if installedName == "" {
				installedName = name
			}
			audit.Log(d.HistoryDB(), "template.install", "http",
				fmt.Sprintf("template=%s installed_as=%s", name, installedName), httputil.ClientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "installed", "name": installedName})

		default:
			http.Error(w, `{"error":"GET or POST .../install"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Skill list for editor dropdowns ---
	mux.HandleFunc("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		skills := d.ListSkillInfos()
		if skills == nil {
			skills = []SkillInfo{}
		}
		sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
		json.NewEncoder(w).Encode(skills)
	})

	// /api/tools is registered in tools.go (RegisterToolRoutes)

	mux.HandleFunc("/api/triggers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if !d.TriggerEngineAvailable {
				json.NewEncoder(w).Encode(map[string]any{"triggers": []any{}, "count": 0})
				return
			}
			triggers, count := d.ListTriggers()
			if triggers == nil {
				triggers = []map[string]any{}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"triggers": triggers,
				"count":    count,
			})

		case http.MethodPost:
			body, err := readBody(r)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
				return
			}
			// Build existing names set.
			existing := make(map[string]bool)
			for _, n := range d.GetCurrentTriggerNames() {
				existing[n] = true
			}
			if errs := d.ValidateTriggerConfig(body, existing); len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			// Persist to config.json + SIGHUP.
			if err := d.MutateTriggerConfig(func(raw map[string]any) {
				triggers, _ := raw["workflowTriggers"].([]any)
				var m any
				json.Unmarshal(body, &m)
				raw["workflowTriggers"] = append(triggers, m)
			}); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"save failed: %v"}`, err), http.StatusInternalServerError)
				return
			}
			tName, tType, tWorkflow, _ := d.DecodeTriggerConfig(body)
			audit.Log(d.HistoryDB(), "trigger.create", "http",
				fmt.Sprintf("name=%s type=%s workflow=%s", tName, tType, tWorkflow), httputil.ClientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "created", "name": tName})

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
		rows, err := db.Query(d.HistoryDB(), sql)
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
		if !d.IsValidCallbackKey(key) {
			http.Error(w, `{"error":"invalid callback key format"}`, http.StatusBadRequest)
			return
		}

		// Look up callback record in DB for auth mode.
		record := d.QueryPendingCallback(d.HistoryDB(), key)
		if record == nil {
			// Check if it was already delivered or completed.
			existing := d.QueryPendingCallbackByKey(d.HistoryDB(), key)
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

		apiToken := d.APIToken()

		// Auth check based on callback auth mode.
		switch record.AuthMode {
		case "bearer":
			auth := r.Header.Get("Authorization")
			if auth == "" || auth != "Bearer "+apiToken {
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
			secret := d.CallbackSignatureSecret(apiToken, key)
			if !d.VerifyCallbackSignature(body, secret, sig) {
				http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
				return
			}
		case "open":
			// No auth required.
		default:
			// Default to bearer.
			auth := r.Header.Get("Authorization")
			if apiToken != "" && (auth == "" || auth != "Bearer "+apiToken) {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}

		cbResult := CallbackPayload{
			Status:      200,
			Body:        string(body),
			ContentType: r.Header.Get("Content-Type"),
			RecvAt:      time.Now().Format(time.RFC3339),
		}

		// Path A: try in-memory delivery first (skip HasChannel pre-check to avoid TOCTOU).
		out := d.DeliverCallback(key, cbResult)
		switch out.Outcome {
		case "ok":
			status := "delivered"
			if out.Mode == "streaming" {
				status = "accumulated"
				// Persist streaming callback immediately for crash recovery (#6).
				// Seq allocated atomically with Deliver to prevent race (#R2-1).
				d.AppendStreamingCallback(d.HistoryDB(), key, out.Seq, cbResult)
			}
			audit.Log(d.HistoryDB(), "callback."+status, "http",
				fmt.Sprintf("key=%s", key), httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": status})
			return
		case "dup":
			// Single mode: already delivered — idempotent.
			json.NewEncoder(w).Encode(map[string]string{"status": "already_delivered"})
			return
		case "full":
			// Streaming buffer full — store to DB with seq allocated atomically (#R2-2).
			d.AppendStreamingCallback(d.HistoryDB(), key, out.Seq, cbResult)
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "streaming buffer full, stored to DB"})
			return
		case "no_entry":
			// Channel not registered — fall through to Path B (DB-only).
		}

		// Path B: channel not alive — record to DB for recovery.
		// Re-check status to avoid overwriting completed/delivered records (#R2-8).
		current := d.QueryPendingCallbackByKey(d.HistoryDB(), key)
		if current != nil && (current.Status == "completed" || current.Status == "delivered") {
			json.NewEncoder(w).Encode(map[string]string{"status": "already_delivered"})
			return
		}
		d.MarkCallbackDelivered(d.HistoryDB(), key, 0, cbResult)
		audit.Log(d.HistoryDB(), "callback.stored", "http",
			fmt.Sprintf("key=%s (no active channel)", key), httputil.ClientIP(r))
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
			if !d.TriggerEngineAvailable {
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
			payload["_webhook_remote"] = httputil.ClientIP(r)

			if err := d.HandleWebhookTrigger(webhookID, payload); err != nil {
				status := http.StatusNotFound
				if strings.Contains(err.Error(), "cooldown") || strings.Contains(err.Error(), "disabled") {
					status = http.StatusTooManyRequests
				}
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), status)
				return
			}
			audit.Log(d.HistoryDB(), "trigger.webhook", "http",
				fmt.Sprintf("trigger=%s", webhookID), httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "accepted",
				"trigger": webhookID,
			})
			return
		}

		switch {
		case action == "fire" && r.Method == http.MethodPost:
			if !d.TriggerEngineAvailable {
				http.Error(w, `{"error":"no triggers configured"}`, http.StatusNotFound)
				return
			}
			if err := d.FireTrigger(name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
				return
			}
			audit.Log(d.HistoryDB(), "trigger.fire", "http",
				fmt.Sprintf("trigger=%s", name), httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "accepted",
				"trigger": name,
			})

		case action == "toggle" && r.Method == http.MethodPost:
			var newEnabled bool
			if err := d.MutateTriggerConfig(func(raw map[string]any) {
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
			audit.Log(d.HistoryDB(), "trigger.toggle", "http",
				fmt.Sprintf("trigger=%s enabled=%v", name, newEnabled), httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]any{"status": "toggled", "name": name, "enabled": newEnabled})

		case action == "" && r.Method == http.MethodPut:
			body, err := readBody(r)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
				return
			}
			if errs := d.ValidateTriggerConfig(body, nil); len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			found := false
			if err := d.MutateTriggerConfig(func(raw map[string]any) {
				triggers, _ := raw["workflowTriggers"].([]any)
				var m any
				json.Unmarshal(body, &m)
				// Force URL name into the decoded map.
				if mm, ok := m.(map[string]any); ok {
					mm["name"] = name
				}
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
			audit.Log(d.HistoryDB(), "trigger.update", "http",
				fmt.Sprintf("trigger=%s", name), httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "updated", "name": name})

		case action == "" && r.Method == http.MethodDelete:
			found := false
			if err := d.MutateTriggerConfig(func(raw map[string]any) {
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
			audit.Log(d.HistoryDB(), "trigger.delete", "http",
				fmt.Sprintf("trigger=%s", name), httputil.ClientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})

		case action == "runs" && r.Method == http.MethodGet:
			limit := 20
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
					limit = n
				}
			}
			triggerRuns, err := d.QueryTriggerRuns(d.HistoryDB(), name, limit)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if triggerRuns == nil {
				triggerRuns = []map[string]any{}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"runs":  triggerRuns,
				"count": len(triggerRuns),
			})

		default:
			http.Error(w, `{"error":"use POST .../fire|toggle, PUT, DELETE, or GET .../runs"}`, http.StatusMethodNotAllowed)
		}
	})
}
