package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"tetora/internal/db"
	"tetora/internal/log"
	iwf "tetora/internal/workflow"
)

// =============================================================================
// Type aliases — re-export from internal/workflow for root-package callers
// =============================================================================

type CallbackManager = iwf.CallbackManager
type CallbackResult = iwf.CallbackResult
type CallbackRecord = iwf.CallbackRecord
type DeliverResult = iwf.DeliverResult
type DeliverWithSeq = iwf.DeliverWithSeq

const (
	DeliverOK      = iwf.DeliverOK
	DeliverNoEntry = iwf.DeliverNoEntry
	DeliverDup     = iwf.DeliverDup
	DeliverFull    = iwf.DeliverFull
)

type TriggerInfo = iwf.TriggerInfo
type WorkflowTriggerEngine = iwf.WorkflowTriggerEngine

// =============================================================================
// Package-level singletons
// =============================================================================

// callbackMgr is the process-wide singleton CallbackManager.
var callbackMgr *CallbackManager

// runCancellers maps runID -> context.CancelFunc for the cancel API.
var runCancellers sync.Map

// =============================================================================
// Constructor wrappers
// =============================================================================

func newCallbackManager(dbPath string) *CallbackManager {
	return iwf.NewCallbackManager(dbPath)
}

func newWorkflowTriggerEngine(cfg *Config, state *dispatchState, sem, childSem chan struct{}, broker *sseBroker) *WorkflowTriggerEngine {
	deps := iwf.TriggerDeps{
		ExecuteWorkflow: func(ctx context.Context, c *Config, wf *Workflow, vars map[string]string) iwf.TriggerRunResult {
			run := executeWorkflow(ctx, c, wf, vars, state, sem, childSem)
			return iwf.TriggerRunResult{
				ID:         run.ID,
				Status:     run.Status,
				Error:      run.Error,
				DurationMs: run.DurationMs,
			}
		},
		LoadWorkflowByName: func(c *Config, name string) (*Workflow, error) {
			return loadWorkflowByName(c, name)
		},
	}
	return iwf.NewWorkflowTriggerEngine(cfg, deps, broker)
}

// =============================================================================
// DB helper wrappers (lowercase aliases used across the codebase)
// =============================================================================

func initCallbackTable(dbPath string)   { iwf.InitCallbackTable(dbPath) }
func initTriggerRunsTable(dbPath string) { iwf.InitTriggerRunsTable(dbPath) }

func recordPendingCallback(dbPath, key, runID, stepID, mode, authMode, url, body, timeoutAt string) {
	iwf.RecordPendingCallback(dbPath, key, runID, stepID, mode, authMode, url, body, timeoutAt)
}

func queryPendingCallbackByKey(dbPath, key string) *CallbackRecord {
	return iwf.QueryPendingCallbackByKey(dbPath, key)
}

func queryPendingCallback(dbPath, key string) *CallbackRecord {
	return iwf.QueryPendingCallback(dbPath, key)
}

func queryPendingCallbacksByRun(dbPath, runID string) []*CallbackRecord {
	return iwf.QueryPendingCallbacksByRun(dbPath, runID)
}

func markPostSent(dbPath, key string)          { iwf.MarkPostSent(dbPath, key) }
func resetCallbackRecord(dbPath, key string)   { iwf.ResetCallbackRecord(dbPath, key) }
func clearPendingCallback(dbPath, key string)  { iwf.ClearPendingCallback(dbPath, key) }
func updateCallbackRunID(dbPath, key, newRunID string) { iwf.UpdateCallbackRunID(dbPath, key, newRunID) }

func markCallbackDelivered(dbPath, key string, seq int, result CallbackResult) {
	iwf.MarkCallbackDelivered(dbPath, key, seq, result)
}

func isCallbackDelivered(dbPath, key string, seq int) bool {
	return iwf.IsCallbackDelivered(dbPath, key, seq)
}

func appendStreamingCallback(dbPath, key string, seq int, result CallbackResult) {
	iwf.AppendStreamingCallback(dbPath, key, seq, result)
}

func queryStreamingCallbacks(dbPath, key string) []CallbackResult {
	return iwf.QueryStreamingCallbacks(dbPath, key)
}

func cleanupExpiredCallbacks(dbPath string) { iwf.CleanupExpiredCallbacks(dbPath) }

func recordTriggerRun(dbPath, triggerName, workflowName, runID, status, startedAt, finishedAt, errMsg string) {
	iwf.RecordTriggerRun(dbPath, triggerName, workflowName, runID, status, startedAt, finishedAt, errMsg)
}

func queryTriggerRuns(dbPath, triggerName string, limit int) ([]map[string]any, error) {
	return iwf.QueryTriggerRuns(dbPath, triggerName, limit)
}

// =============================================================================
// HMAC helpers
// =============================================================================

func callbackSignatureSecret(serverSecret, callbackKey string) string {
	return iwf.CallbackSignatureSecret(serverSecret, callbackKey)
}

func verifyCallbackSignature(body []byte, secret, signatureHex string) bool {
	return iwf.VerifyCallbackSignature(body, secret, signatureHex)
}

// =============================================================================
// JSON helpers
// =============================================================================

func extractJSONPath(jsonStr, path string) string { return iwf.ExtractJSONPath(jsonStr, path) }

func applyResponseMapping(body string, mapping *ResponseMapping) string {
	return iwf.ApplyResponseMapping(body, mapping)
}

// =============================================================================
// Validation / utility helpers
// =============================================================================

func isValidCallbackKey(key string) bool { return iwf.IsValidCallbackKey(key) }

func parseDurationWithDays(s string) (time.Duration, error) { return iwf.ParseDurationWithDays(s) }

func matchEventType(eventType, pattern string) bool { return iwf.MatchEventType(eventType, pattern) }

func toolInputToJSON(input map[string]string) json.RawMessage { return iwf.ToolInputToJSON(input) }

// expandVars and expandToolInput are already wrapped in workflow.go via iwf.ExpandVars /
// iwf.ExpandToolInput. Keep thin wrappers here so workflow_exec.go callers compile.
func expandVars(s string, vars map[string]string) string { return iwf.ExpandVars(s, vars) }
func expandToolInput(input, vars map[string]string) map[string]string {
	return iwf.ExpandToolInput(input, vars)
}

// =============================================================================
// HTTP helper
// =============================================================================

func httpPostWithRetry(ctx context.Context, url, contentType string, headers map[string]string, body string, maxRetry int) (*http.Response, error) {
	return iwf.HTTPPostWithRetry(ctx, url, contentType, headers, body, maxRetry)
}

// =============================================================================
// Wait helpers
// =============================================================================

// waitSingleCallback waits for a single callback result or timeout.
func waitSingleCallback(ctx context.Context, ch chan CallbackResult, _ string, _ *WorkflowStep, timeout time.Duration) *CallbackResult {
	return iwf.WaitSingleCallback(ctx, ch, timeout)
}

// waitStreamingCallback waits for multiple callbacks until DonePath==DoneValue or timeout.
func waitStreamingCallback(ctx context.Context, ch chan CallbackResult, _ string, step *WorkflowStep, timeout time.Duration) (*CallbackResult, []CallbackResult) {
	return iwf.WaitStreamingCallback(ctx, ch, step.CallbackResponseMap, timeout)
}

// handleCallbackTimeout sets result fields for a timed-out callback.
func handleCallbackTimeout(step *WorkflowStep, result *StepRunResult, timeout time.Duration, ctx context.Context) {
	onTimeout := step.OnTimeout
	r := iwf.HandleCallbackTimeout(onTimeout, timeout, ctx.Err())
	result.Status = r.Status
	result.Error = r.Error
	if r.Output != "" {
		result.Output = r.Output
	}
}

// =============================================================================
// Template helpers (on workflowExecutor — root-only, accesses wCtx)
// =============================================================================

// resolveTemplateWithFields resolves {{...}} templates and also handles
// {{steps.id.output.field}} by extracting JSON fields from step outputs.
func (e *workflowExecutor) resolveTemplateWithFields(tmpl string) string {
	result := templateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		expr := strings.TrimSpace(match[2 : len(match)-2])
		parts := strings.SplitN(expr, ".", 4)

		// Handle {{steps.id.output.fieldPath}}
		if len(parts) >= 4 && parts[0] == "steps" && parts[2] == "output" {
			stepID := parts[1]
			fieldPath := strings.Join(parts[3:], ".")
			stepResult, ok := e.wCtx.Steps[stepID]
			if !ok {
				return ""
			}
			return extractJSONPath(stepResult.Output, fieldPath)
		}

		// Fallback to standard resolution.
		return resolveExpr(expr, e.wCtx)
	})
	return result
}

// resolveTemplateMapWithFields resolves all values in a map using resolveTemplateWithFields.
func (e *workflowExecutor) resolveTemplateMapWithFields(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = e.resolveTemplateWithFields(v)
	}
	return result
}

// resolveTemplateXMLEscaped resolves templates and XML-escapes the result.
func (e *workflowExecutor) resolveTemplateXMLEscaped(tmpl string) string {
	result := e.resolveTemplateWithFields(tmpl)
	result = strings.ReplaceAll(result, "&", "&amp;")
	result = strings.ReplaceAll(result, "<", "&lt;")
	result = strings.ReplaceAll(result, ">", "&gt;")
	result = strings.ReplaceAll(result, "\"", "&quot;")
	result = strings.ReplaceAll(result, "'", "&apos;")
	return result
}

// =============================================================================
// runExternalStep — root-only: accesses workflowExecutor, callbackMgr singleton
// =============================================================================

// runExternalStep executes an external step: POST to URL, wait for callback.
func (e *workflowExecutor) runExternalStep(ctx context.Context, step *WorkflowStep, result *StepRunResult) {
	if callbackMgr == nil {
		result.Status = "error"
		result.Error = "callback manager not initialized"
		return
	}

	// Resolve templates in all fields.
	url := e.resolveTemplateWithFields(step.ExternalURL)
	headers := e.resolveTemplateMapWithFields(step.ExternalHeaders)

	// Resolve auth mode early — needed before callbackKey for header injection.
	authMode := step.CallbackAuth
	if authMode == "" {
		authMode = "bearer"
	}

	callbackKey := e.resolveTemplateWithFields(step.CallbackKey)
	if callbackKey == "" {
		// Check for recovery-injected key (from recoverPendingWorkflows).
		if recoveredKey, ok := e.wCtx.Input["__cb_key_"+step.ID]; ok && recoveredKey != "" {
			callbackKey = recoveredKey
		} else {
			callbackKey = fmt.Sprintf("%s-%s-%s", e.run.ID, step.ID, newUUID()[:8])
		}
	}

	// For signature auth, include callback secret in outgoing headers.
	if authMode == "signature" {
		if url != "" && !strings.HasPrefix(url, "https://") {
			log.Warn("HMAC callback secret sent over non-HTTPS connection", "step", step.ID, "url", url)
		}
		cbSecret := callbackSignatureSecret(e.cfg.APIToken, callbackKey)
		if headers == nil {
			headers = make(map[string]string)
		}
		headers["X-Callback-Secret"] = cbSecret
	}

	// Build request body.
	contentType := step.ExternalContentType
	if contentType == "" {
		contentType = "application/json"
	}
	var bodyStr string
	if step.ExternalRawBody != "" {
		bodyStr = e.resolveTemplateWithFields(step.ExternalRawBody)
	} else if step.ExternalBody != nil {
		resolvedBody := e.resolveTemplateMapWithFields(step.ExternalBody)
		if contentType == "application/x-www-form-urlencoded" {
			vals := neturl.Values{}
			for k, v := range resolvedBody {
				vals.Set(k, v)
			}
			bodyStr = vals.Encode()
		} else {
			bodyBytes, _ := json.Marshal(resolvedBody)
			bodyStr = string(bodyBytes)
		}
	}

	// Callback mode and timeout.
	mode := step.CallbackMode
	if mode == "" {
		mode = "single"
	}
	timeout := 1 * time.Hour // default
	if step.CallbackTimeout != "" {
		if d, err := parseDurationWithDays(step.CallbackTimeout); err == nil {
			timeout = d
		}
	}

	// Check DB state for resume/retry.
	isResume := false
	existingRecord := queryPendingCallbackByKey(callbackMgr.DBPath(), callbackKey)
	if existingRecord != nil {
		switch existingRecord.Status {
		case "delivered":
			// Already completed — skip re-execution.
			result.Status = "success"
			output := existingRecord.ResultBody
			if output == "" {
				output = existingRecord.Body // fallback for legacy records
			}
			result.Output = output
			log.Info("external step already delivered, skipping", "step", step.ID, "key", callbackKey)
			return
		case "completed", "timeout":
			// Previous attempt finished — reset for retry.
			resetCallbackRecord(callbackMgr.DBPath(), callbackKey)
			log.Info("external step retrying (reset old record)", "step", step.ID, "key", callbackKey, "oldStatus", existingRecord.Status)
		default:
			// "waiting" — check if POST was already sent (resume).
			if existingRecord.PostSent {
				isResume = true
				log.Info("external step resuming (POST already sent)", "step", step.ID, "key", callbackKey)
			}
		}
	}

	// If this is a recovered key, update the DB record to reference the new run ID.
	if _, ok := e.wCtx.Input["__cb_key_"+step.ID]; ok {
		updateCallbackRunID(callbackMgr.DBPath(), callbackKey, e.run.ID)
	}

	// Register channel BEFORE POST to prevent race condition.
	ch := callbackMgr.Register(callbackKey, ctx, mode)
	if ch == nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("failed to register callback channel (key collision or at capacity): %s", callbackKey)
		return
	}
	defer callbackMgr.Unregister(callbackKey)

	// Calculate timeout time.
	timeoutAt := time.Now().Add(timeout)

	// Write DB record.
	if !isResume {
		recordPendingCallback(callbackMgr.DBPath(), callbackKey, e.run.ID, step.ID,
			mode, authMode, url, bodyStr, timeoutAt.UTC().Format("2006-01-02 15:04:05"))
	}

	// Replay accumulated streaming callbacks on resume.
	if isResume && mode == "streaming" {
		accumulated := queryStreamingCallbacks(callbackMgr.DBPath(), callbackKey)
		if len(accumulated) > 0 {
			callbackMgr.ReplayAccumulated(callbackKey, accumulated)
			callbackMgr.SetSeq(callbackKey, len(accumulated))
			log.Info("replayed accumulated streaming callbacks", "step", step.ID, "key", callbackKey, "count", len(accumulated))
		}
	}

	// HTTP POST (skip if resuming).
	if !isResume && url != "" {
		markPostSent(callbackMgr.DBPath(), callbackKey)

		retryMax := step.RetryMax
		if retryMax <= 0 {
			retryMax = 2
		}
		resp, err := httpPostWithRetry(ctx, url, contentType, headers, bodyStr, retryMax)
		if err != nil {
			result.Status = "error"
			result.Error = fmt.Sprintf("external POST failed: %v", err)
			resetCallbackRecord(callbackMgr.DBPath(), callbackKey)
			return
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	} else if !isResume {
		// No URL — callback-only mode (e.g. manual approval).
		markPostSent(callbackMgr.DBPath(), callbackKey)
	}

	// Publish waiting event.
	e.publishEvent("step_waiting", map[string]any{
		"runId":       e.run.ID,
		"stepId":      step.ID,
		"callbackKey": callbackKey,
		"timeout":     timeout.String(),
	})

	log.Info("external step waiting for callback", "step", step.ID, "key", callbackKey, "timeout", timeout.String())

	// Wait for callback(s).
	if mode == "streaming" {
		lastResult, accumulated := waitStreamingCallback(ctx, ch, callbackKey, step, timeout)

		if lastResult == nil {
			handleCallbackTimeout(step, result, timeout, ctx)
			return
		}

		// Build output based on accumulate setting.
		var output string
		if step.CallbackAccumulate && len(accumulated) > 0 {
			var parts []string
			for _, a := range accumulated {
				mapped := applyResponseMapping(a.Body, step.CallbackResponseMap)
				if !json.Valid([]byte(mapped)) {
					b, _ := json.Marshal(mapped)
					mapped = string(b)
				}
				parts = append(parts, mapped)
			}
			output = "[" + strings.Join(parts, ",") + "]"
		} else {
			output = applyResponseMapping(lastResult.Body, step.CallbackResponseMap)
		}

		// Check if done or timed out.
		isDone := false
		if step.CallbackResponseMap != nil && step.CallbackResponseMap.DonePath != "" {
			doneVal := extractJSONPath(lastResult.Body, step.CallbackResponseMap.DonePath)
			isDone = doneVal == step.CallbackResponseMap.DoneValue
		}

		if isDone {
			result.Status = "success"
			result.Output = output
		} else if ctx.Err() != nil {
			result.Status = "cancelled"
			result.Error = "workflow cancelled while waiting for callback"
			result.Output = output
		} else {
			// Timeout with partial results.
			onTimeout := step.OnTimeout
			if onTimeout == "" {
				onTimeout = "stop"
			}
			if onTimeout == "skip" {
				result.Status = "skipped"
				result.Error = "streaming timeout (partial)"
				result.Output = output
			} else {
				result.Status = "timeout"
				result.Error = "streaming timeout (partial)"
				result.Output = output
			}
		}
		clearPendingCallback(callbackMgr.DBPath(), callbackKey)
		log.Info("external step completed (streaming)", "step", step.ID, "key", callbackKey, "callbacks", len(accumulated))
	} else {
		// Single mode.
		cbResult := waitSingleCallback(ctx, ch, callbackKey, step, timeout)
		if cbResult == nil {
			handleCallbackTimeout(step, result, timeout, ctx)
			return
		}

		markCallbackDelivered(callbackMgr.DBPath(), callbackKey, 0, *cbResult)

		output := cbResult.Body
		if step.CallbackResponseMap != nil {
			output = applyResponseMapping(output, step.CallbackResponseMap)
		}

		result.Status = "success"
		result.Output = output
		clearPendingCallback(callbackMgr.DBPath(), callbackKey)
		log.Info("external step completed", "step", step.ID, "key", callbackKey)
	}
}

// =============================================================================
// Recovery helpers — root-only (use dispatchState + executeWorkflow)
// =============================================================================

// recoverPendingWorkflows scans for workflows with pending external steps and resumes them.
func recoverPendingWorkflows(cfg *Config, state *dispatchState, sem, childSem chan struct{}) {
	if cfg.HistoryDB == "" || callbackMgr == nil {
		return
	}

	// Find all unique run IDs with waiting callbacks.
	sql := `SELECT DISTINCT run_id FROM workflow_callbacks WHERE status='waiting'`
	rows, err := db.Query(cfg.HistoryDB, sql)
	if err != nil || len(rows) == 0 {
		return
	}

	for _, row := range rows {
		runID := fmt.Sprintf("%v", row["run_id"])

		// Load the workflow run.
		run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
		if err != nil || run == nil {
			log.Warn("recovery: cannot load workflow run", "runID", runID, "error", err)
			continue
		}

		// Load the workflow definition.
		wf, err := loadWorkflowByName(cfg, run.WorkflowName)
		if err != nil {
			log.Warn("recovery: cannot load workflow", "workflow", run.WorkflowName, "error", err)
			continue
		}

		log.Info("recovering pending workflow", "workflow", run.WorkflowName, "runID", runID[:8])

		// Collect pending callback keys for this run so the new execution can reuse them.
		pendingCallbacks := queryPendingCallbacksByRun(cfg.HistoryDB, runID)
		recoveryVars := make(map[string]string)
		for k, v := range run.Variables {
			recoveryVars[k] = v
		}
		for _, cb := range pendingCallbacks {
			recoveryVars["__cb_key_"+cb.StepID] = cb.Key
		}

		// Mark old run as superseded so it's not left orphaned.
		markRunSuperseded := func(oldRunID string) {
			sql := fmt.Sprintf(
				`UPDATE workflow_runs SET status='recovered', finished_at=datetime('now') WHERE id='%s' AND status IN ('running','waiting')`,
				db.Escape(oldRunID),
			)
			db.Query(cfg.HistoryDB, sql)
		}
		markRunSuperseded(runID)

		// Re-execute the workflow in background.
		go executeWorkflow(context.Background(), cfg, wf, recoveryVars, state, sem, childSem)
	}
}

// checkpointRun saves current workflow run state to DB.
func checkpointRun(e *workflowExecutor) {
	recordWorkflowRun(e.cfg.HistoryDB, e.run)
}

// hasWaitingExternalStep checks if any step result indicates a waiting external step.
func hasWaitingExternalStep(results map[string]*StepRunResult) bool {
	for _, r := range results {
		if r.Status == "waiting" {
			return true
		}
	}
	return false
}

// validateTriggerConfig wraps the internal version (which also validates cron expressions).
func validateTriggerConfig(t WorkflowTriggerConfig, existingNames map[string]bool) []string {
	return iwf.ValidateTriggerConfig(t, existingNames)
}
