package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// --- Incoming Webhook Types ---

// IncomingWebhookConfig defines an incoming webhook that triggers agent execution.
type IncomingWebhookConfig struct {
	Agent     string `json:"agent"`               // target agent for dispatch
	Template string `json:"template,omitempty"`  // prompt template with {{payload.xxx}} placeholders
	Secret   string `json:"secret,omitempty"`    // $ENV_VAR supported; HMAC-SHA256 signature verification
	Filter   string `json:"filter,omitempty"`    // simple condition: "payload.action == 'opened'"
	Workflow string `json:"workflow,omitempty"`  // workflow name to trigger instead of dispatch
	Enabled  *bool  `json:"enabled,omitempty"`   // default true
}

func (c IncomingWebhookConfig) isEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// IncomingWebhookResult is the response from processing an incoming webhook.
type IncomingWebhookResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"`   // "accepted", "filtered", "error", "disabled"
	TaskID   string `json:"taskId,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Message  string `json:"message,omitempty"`
}

// --- Signature Verification ---

// verifyWebhookSignature checks the request signature against the shared secret.
// Supports GitHub (X-Hub-Signature-256), GitLab (X-Gitlab-Token), and generic (X-Webhook-Signature).
func verifyWebhookSignature(r *http.Request, body []byte, secret string) bool {
	if secret == "" {
		return true // no secret = skip verification
	}

	// GitHub: X-Hub-Signature-256 = sha256=<hex>
	if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
		return verifyHMACSHA256(body, secret, strings.TrimPrefix(sig, "sha256="))
	}

	// GitLab: X-Gitlab-Token = <secret>
	if token := r.Header.Get("X-Gitlab-Token"); token != "" {
		return hmac.Equal([]byte(token), []byte(secret))
	}

	// Generic: X-Webhook-Signature = <hex hmac-sha256>
	if sig := r.Header.Get("X-Webhook-Signature"); sig != "" {
		return verifyHMACSHA256(body, secret, sig)
	}

	// No signature header found — reject if secret is configured.
	return false
}

// verifyHMACSHA256 checks HMAC-SHA256 signature.
func verifyHMACSHA256(body []byte, secret, signatureHex string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signatureHex))
}

// --- Payload Template Expansion ---

// expandPayloadTemplate replaces {{payload.xxx}} and {{payload.xxx.yyy}} placeholders
// with values from the parsed JSON payload.
func expandPayloadTemplate(template string, payload map[string]any) string {
	re := regexp.MustCompile(`\{\{payload\.([a-zA-Z0-9_.]+)\}\}`)
	return re.ReplaceAllStringFunc(template, func(match string) string {
		// Extract the path: "payload.pull_request.title" -> "pull_request.title"
		path := match[10 : len(match)-2] // strip "{{payload." and "}}"
		val := getNestedValue(payload, path)
		if val == nil {
			return match // keep original if not found
		}
		switch v := val.(type) {
		case string:
			return v
		case float64:
			if v == float64(int(v)) {
				return fmt.Sprintf("%d", int(v))
			}
			return fmt.Sprintf("%g", v)
		case bool:
			return fmt.Sprintf("%v", v)
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	})
}

// getNestedValue retrieves a value from a nested map using dot notation.
func getNestedValue(m map[string]any, path string) any {
	parts := strings.Split(path, ".")
	var current any = m
	for _, part := range parts {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = cm[part]
		if !ok {
			return nil
		}
	}
	return current
}

// --- Filter Evaluation ---

// evaluateFilter checks if a payload matches a simple filter expression.
// Supported: "payload.key == 'value'", "payload.key != 'value'", "payload.key" (truthy check).
func evaluateFilter(filter string, payload map[string]any) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true // no filter = accept all
	}

	// "payload.key == 'value'" or "payload.key != 'value'"
	for _, op := range []string{"==", "!="} {
		if parts := strings.SplitN(filter, op, 2); len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, "'\"")

			// Strip "payload." prefix.
			key = strings.TrimPrefix(key, "payload.")

			actual := getNestedValue(payload, key)
			actualStr := fmt.Sprintf("%v", actual)

			if op == "==" {
				return actualStr == val
			}
			return actualStr != val
		}
	}

	// Truthy check: "payload.key"
	key := strings.TrimPrefix(filter, "payload.")
	val := getNestedValue(payload, key)
	return isTruthy(val)
}

func isTruthy(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	default:
		return true
	}
}

// --- Webhook Handler ---

// handleIncomingWebhook processes an incoming webhook request.
func handleIncomingWebhook(ctx context.Context, cfg *Config, name string, r *http.Request,
	state *dispatchState, sem chan struct{}) IncomingWebhookResult {

	whCfg, ok := cfg.IncomingWebhooks[name]
	if !ok {
		return IncomingWebhookResult{
			Name: name, Status: "error",
			Message: fmt.Sprintf("webhook %q not found", name),
		}
	}

	if !whCfg.isEnabled() {
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
		logWarn("incoming webhook signature mismatch", "name", name)
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
		logDebugCtx(ctx, "incoming webhook filtered out", "name", name, "filter", whCfg.Filter)
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

	logInfoCtx(ctx, "incoming webhook accepted", "name", name, "agent", whCfg.Agent)
	auditLog(cfg.HistoryDB, "webhook.incoming", "http",
		fmt.Sprintf("name=%s agent=%s", name, whCfg.Agent), clientIP(r))

	// Trigger workflow or dispatch.
	if whCfg.Workflow != "" {
		return triggerWebhookWorkflow(ctx, cfg, name, whCfg, payload, prompt, state, sem)
	}
	return triggerWebhookDispatch(ctx, cfg, name, whCfg, prompt, state, sem)
}

// triggerWebhookDispatch dispatches a task to the specified agent.
func triggerWebhookDispatch(ctx context.Context, cfg *Config, name string, whCfg IncomingWebhookConfig,
	prompt string, state *dispatchState, sem chan struct{}) IncomingWebhookResult {

	task := Task{
		Prompt: prompt,
		Agent:   whCfg.Agent,
		Source: "webhook:" + name,
	}
	fillDefaults(cfg, &task)

	// Run async.
	go func() {
		result := runSingleTask(ctx, cfg, task, sem, whCfg.Agent)

		// Record history.
		start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, whCfg.Agent, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

		// Record session activity.
		recordSessionActivity(cfg.HistoryDB, task, result, whCfg.Agent)

		logInfoCtx(ctx, "incoming webhook task done", "name", name, "taskId", task.ID[:8],
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
	payload map[string]any, prompt string, state *dispatchState, sem chan struct{}) IncomingWebhookResult {

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
		run := executeWorkflow(ctx, cfg, wf, vars, state, sem)
		logInfoCtx(ctx, "incoming webhook workflow done", "name", name,
			"workflow", whCfg.Workflow, "status", run.Status, "cost", run.TotalCost)
	}()

	return IncomingWebhookResult{
		Name:     name,
		Status:   "accepted",
		Agent:     whCfg.Agent,
		Workflow: whCfg.Workflow,
	}
}
