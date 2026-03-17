package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"tetora/internal/log"
	"tetora/internal/messaging/webhook"
)

// --- Incoming Webhook Types ---

// IncomingWebhookResult is the response from processing an incoming webhook.
type IncomingWebhookResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"`   // "accepted", "filtered", "error", "disabled"
	TaskID   string `json:"taskId,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Message  string `json:"message,omitempty"`
}

// --- Signature Verification (delegated to internal/messaging/webhook) ---

// verifyWebhookSignature checks the request signature against the shared secret.
func verifyWebhookSignature(r *http.Request, body []byte, secret string) bool {
	return webhook.VerifySignature(r, body, secret)
}

// verifyHMACSHA256 checks HMAC-SHA256 signature.
func verifyHMACSHA256(body []byte, secret, signatureHex string) bool {
	return webhook.VerifyHMACSHA256(body, secret, signatureHex)
}

// --- Payload Template Expansion (delegated to internal/messaging/webhook) ---

// expandPayloadTemplate replaces {{payload.xxx}} placeholders with payload values.
func expandPayloadTemplate(tmpl string, payload map[string]any) string {
	return webhook.ExpandTemplate(tmpl, payload)
}

// getNestedValue retrieves a value from a nested map using dot notation.
func getNestedValue(m map[string]any, path string) any {
	return webhook.GetNestedValue(m, path)
}

// --- Filter Evaluation (delegated to internal/messaging/webhook) ---

// evaluateFilter checks if a payload matches a simple filter expression.
func evaluateFilter(filter string, payload map[string]any) bool {
	return webhook.EvaluateFilter(filter, payload)
}

func isTruthy(val any) bool {
	return webhook.IsTruthy(val)
}

// --- Webhook Handler ---

// handleIncomingWebhook processes an incoming webhook request.
func handleIncomingWebhook(ctx context.Context, cfg *Config, name string, r *http.Request,
	state *dispatchState, sem, childSem chan struct{}) IncomingWebhookResult {

	whCfg, ok := cfg.IncomingWebhooks[name]
	if !ok {
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: fmt.Sprintf("webhook %q not found", name),
		}
	}

	if !whCfg.IsEnabled() {
		return IncomingWebhookResult{Name: name, Status: "disabled"}
	}

	// Read body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: fmt.Sprintf("read body: %v", err),
		}
	}

	// Verify signature.
	if !verifyWebhookSignature(r, body, whCfg.Secret) {
		log.Warn("incoming webhook signature mismatch", "name", name)
		auditLog(cfg.HistoryDB, "webhook.incoming.auth_fail", "http", name, clientIP(r))
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: "signature verification failed",
		}
	}

	// Parse payload.
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: fmt.Sprintf("parse payload: %v", err),
		}
	}

	// Apply filter.
	if !evaluateFilter(whCfg.Filter, payload) {
		log.DebugCtx(ctx, "incoming webhook filtered out", "name", name, "filter", whCfg.Filter)
		return IncomingWebhookResult{Name: name, Status: "filtered"}
	}

	// Build prompt from template.
	prompt := whCfg.Template
	if prompt != "" {
		prompt = expandPayloadTemplate(prompt, payload)
	} else {
		// Default: pretty-print the entire payload.
		b, _ := json.MarshalIndent(payload, "", "  ")
		prompt = fmt.Sprintf("Process this webhook event (%s):\n\n%s", name, string(b))
	}

	log.InfoCtx(ctx, "incoming webhook accepted", "name", name, "agent", whCfg.Agent)
	auditLog(cfg.HistoryDB, "webhook.incoming", "http",
		fmt.Sprintf("name=%s agent=%s", name, whCfg.Agent), clientIP(r))

	// Trigger workflow or dispatch.
	if whCfg.Workflow != "" {
		return triggerWebhookWorkflow(ctx, cfg, name, whCfg, payload, prompt, state, sem, childSem)
	}
	return triggerWebhookDispatch(ctx, cfg, name, whCfg, prompt, state, sem, childSem)
}

// triggerWebhookDispatch dispatches a task to the specified agent.
func triggerWebhookDispatch(ctx context.Context, cfg *Config, name string, whCfg IncomingWebhookConfig,
	prompt string, state *dispatchState, sem, childSem chan struct{}) IncomingWebhookResult {

	task := Task{
		Prompt: prompt,
		Agent:   whCfg.Agent,
		Source: "webhook:" + name,
	}
	fillDefaults(cfg, &task)

	// Run async.
	go func() {
		result := runSingleTask(ctx, cfg, task, sem, childSem, whCfg.Agent)

		// Record history.
		start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, whCfg.Agent, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

		// Record session activity.
		recordSessionActivity(cfg.HistoryDB, task, result, whCfg.Agent)

		log.InfoCtx(ctx, "incoming webhook task done", "name", name, "taskId", task.ID[:8],
			"status", result.Status, "cost", result.CostUSD)
	}()

	return IncomingWebhookResult{
		Name:   name,
		Status: "accepted",
		TaskID: task.ID,
		Agent:   whCfg.Agent,
	}
}

// triggerWebhookWorkflow loads and executes a workflow.
func triggerWebhookWorkflow(ctx context.Context, cfg *Config, name string, whCfg IncomingWebhookConfig,
	payload map[string]any, prompt string, state *dispatchState, sem, childSem chan struct{}) IncomingWebhookResult {

	wf, err := loadWorkflowByName(cfg, whCfg.Workflow)
	if err != nil {
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: fmt.Sprintf("load workflow %q: %v", whCfg.Workflow, err),
		}
	}

	// Build workflow variables from payload.
	vars := map[string]string{
		"input":        prompt,
		"webhook_name": name,
	}
	// Flatten top-level payload keys as variables.
	for k, v := range payload {
		switch val := v.(type) {
		case string:
			vars["payload_"+k] = val
		case float64:
			if val == float64(int(val)) {
				vars["payload_"+k] = fmt.Sprintf("%d", int(val))
			} else {
				vars["payload_"+k] = fmt.Sprintf("%g", val)
			}
		case bool:
			vars["payload_"+k] = fmt.Sprintf("%v", val)
		}
	}

	// Run async.
	go func() {
		run := executeWorkflow(ctx, cfg, wf, vars, state, sem, childSem)
		log.InfoCtx(ctx, "incoming webhook workflow done", "name", name,
			"workflow", whCfg.Workflow, "status", run.Status, "cost", run.TotalCost)
	}()

	return IncomingWebhookResult{
		Name:     name,
		Status:   "accepted",
		Agent:     whCfg.Agent,
		Workflow: whCfg.Workflow,
	}
}
