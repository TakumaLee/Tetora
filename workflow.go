package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:embed examples/templates/*.json
var templateFS embed.FS

// --- Workflow Types ---

// Workflow defines a multi-step orchestration pipeline.
type Workflow struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Steps       []WorkflowStep    `json:"steps"`
	Variables   map[string]string `json:"variables,omitempty"` // input variables with defaults
	Timeout     string            `json:"timeout,omitempty"`   // overall workflow timeout, e.g. "30m"
	OnSuccess   string            `json:"onSuccess,omitempty"` // notification template
	OnFailure   string            `json:"onFailure,omitempty"` // notification template
}

// WorkflowStep is a single step in a workflow.
type WorkflowStep struct {
	ID        string   `json:"id"`
	Type      string   `json:"type,omitempty"`      // "dispatch" (default), "skill", "condition", "parallel"
	Agent      string   `json:"agent,omitempty"`       // agent role for dispatch steps
	Prompt    string   `json:"prompt,omitempty"`     // for dispatch steps
	Skill     string   `json:"skill,omitempty"`      // skill name for skill steps
	SkillArgs []string `json:"skillArgs,omitempty"`  // skill arguments
	DependsOn []string `json:"dependsOn,omitempty"`  // step IDs that must complete first

	// Dispatch options.
	Model          string  `json:"model,omitempty"`
	Provider       string  `json:"provider,omitempty"`
	Timeout        string  `json:"timeout,omitempty"` // per-step timeout
	Budget         float64 `json:"budget,omitempty"`
	PermissionMode string  `json:"permissionMode,omitempty"`

	// Condition step fields.
	If   string `json:"if,omitempty"`   // condition expression
	Then string `json:"then,omitempty"` // step ID to jump to on true
	Else string `json:"else,omitempty"` // step ID to jump to on false

	// Handoff step fields.
	HandoffFrom string `json:"handoffFrom,omitempty"` // source step ID whose output becomes context

	// Parallel step fields.
	Parallel []WorkflowStep `json:"parallel,omitempty"` // sub-steps to run in parallel

	// Failure handling.
	RetryMax   int    `json:"retryMax,omitempty"`   // max retries on failure
	RetryDelay string `json:"retryDelay,omitempty"` // delay between retries
	OnError    string `json:"onError,omitempty"`    // "stop" (default), "skip", "retry"

	// --- P18.3: Workflow Triggers --- New step types.
	ToolName  string            `json:"toolName,omitempty"`  // for type="tool_call"
	ToolInput map[string]string `json:"toolInput,omitempty"` // tool input params (supports {{var}} expansion)
	Delay     string            `json:"delay,omitempty"`     // for type="delay" (e.g. "30s", "5m")
	NotifyMsg string            `json:"notifyMsg,omitempty"` // for type="notify"
	NotifyTo  string            `json:"notifyTo,omitempty"`  // notification channel hint

	// External step fields (type="external").
	ExternalURL         string            `json:"externalUrl,omitempty"`         // POST target URL
	ExternalHeaders     map[string]string `json:"externalHeaders,omitempty"`     // custom headers (supports template vars)
	ExternalBody        map[string]string `json:"externalBody,omitempty"`        // request body KV (supports template vars)
	ExternalRawBody     string            `json:"externalRawBody,omitempty"`     // raw body (XML / custom, mutually exclusive with ExternalBody)
	ExternalContentType string            `json:"externalContentType,omitempty"` // default: application/json
	CallbackKey         string            `json:"callbackKey,omitempty"`         // callback matching key (supports template vars)
	CallbackTimeout     string            `json:"callbackTimeout,omitempty"`     // wait timeout (default 1h, max 30d)
	CallbackMode        string            `json:"callbackMode,omitempty"`        // "single" (default) or "streaming"
	CallbackAccumulate  bool              `json:"callbackAccumulate,omitempty"`  // streaming: true=accumulate all results as JSON array
	CallbackAuth        string            `json:"callbackAuth,omitempty"`        // "bearer" (default), "open", "signature"
	CallbackContentType string            `json:"callbackContentType,omitempty"` // callback content type, default application/json
	CallbackResponseMap *ResponseMapping  `json:"callbackResponseMap,omitempty"` // extract status/data from webhook body
	OnTimeout           string            `json:"onTimeout,omitempty"`           // timeout behavior: stop / skip
}

// ResponseMapping extracts structured data from arbitrary webhook bodies.
type ResponseMapping struct {
	StatusPath string `json:"statusPath,omitempty"` // JSONPath-like: "data.object.status"
	DataPath   string `json:"dataPath,omitempty"`   // JSONPath-like: "data.object"
	DonePath   string `json:"donePath,omitempty"`   // streaming: field path to check for completion
	DoneValue  string `json:"doneValue,omitempty"`  // streaming: DonePath value that indicates done
}

// workflowDir returns the workflows directory under baseDir.
func workflowDir(cfg *Config) string {
	return filepath.Join(cfg.baseDir, "workflows")
}

// ensureWorkflowDir creates the workflows directory if missing.
func ensureWorkflowDir(cfg *Config) error {
	return os.MkdirAll(workflowDir(cfg), 0o755)
}

// --- Load / Save ---

// loadWorkflow reads a single workflow JSON file.
func loadWorkflow(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workflow: %w", err)
	}
	var w Workflow
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	return &w, nil
}

// loadWorkflowByName loads a workflow by name from the workflows directory.
func loadWorkflowByName(cfg *Config, name string) (*Workflow, error) {
	path := filepath.Join(workflowDir(cfg), name+".json")
	return loadWorkflow(path)
}

// saveWorkflow writes a workflow to the workflows directory.
func saveWorkflow(cfg *Config, w *Workflow) error {
	if err := ensureWorkflowDir(cfg); err != nil {
		return err
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workflow: %w", err)
	}
	path := filepath.Join(workflowDir(cfg), w.Name+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	// Auto-snapshot workflow version.
	snapshotWorkflow(cfg.HistoryDB, w.Name, string(data), "system", "")
	return nil
}

// deleteWorkflow removes a workflow file.
func deleteWorkflow(cfg *Config, name string) error {
	path := filepath.Join(workflowDir(cfg), name+".json")
	// Snapshot before deletion.
	if data, err := os.ReadFile(path); err == nil {
		snapshotWorkflow(cfg.HistoryDB, name, string(data), "system", "pre-delete")
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workflow %q not found", name)
		}
		return err
	}
	return nil
}

// listWorkflows returns all workflow files from the workflows directory.
func listWorkflows(cfg *Config) ([]*Workflow, error) {
	dir := workflowDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var workflows []*Workflow
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		w, err := loadWorkflow(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip malformed files
		}
		workflows = append(workflows, w)
	}
	return workflows, nil
}

// --- Validation ---

// ValidateWorkflow checks a workflow for structural correctness.
func validateWorkflow(w *Workflow) []string {
	var errs []string

	if w.Name == "" {
		errs = append(errs, "workflow name is required")
	}
	if !isValidWorkflowName(w.Name) {
		errs = append(errs, fmt.Sprintf("invalid workflow name %q: use alphanumeric, hyphens, underscores", w.Name))
	}
	if len(w.Steps) == 0 {
		errs = append(errs, "workflow must have at least one step")
	}
	if w.Timeout != "" {
		if _, err := time.ParseDuration(w.Timeout); err != nil {
			errs = append(errs, fmt.Sprintf("invalid timeout %q: %v", w.Timeout, err))
		}
	}

	// Build step ID set and check uniqueness.
	ids := make(map[string]bool)
	for _, s := range w.Steps {
		if s.ID == "" {
			errs = append(errs, "step ID is required")
			continue
		}
		if ids[s.ID] {
			errs = append(errs, fmt.Sprintf("duplicate step ID %q", s.ID))
		}
		ids[s.ID] = true
	}

	// Validate each step.
	for _, s := range w.Steps {
		errs = append(errs, validateStep(s, ids)...)
	}

	// Check for duplicate static callbackKey across external steps.
	cbKeys := make(map[string]string) // key -> stepID
	for _, s := range w.Steps {
		if s.Type == "external" && s.CallbackKey != "" && !strings.Contains(s.CallbackKey, "{{") {
			if prev, dup := cbKeys[s.CallbackKey]; dup {
				errs = append(errs, fmt.Sprintf("steps %q and %q share the same callbackKey %q", prev, s.ID, s.CallbackKey))
			}
			cbKeys[s.CallbackKey] = s.ID
		}
	}

	// DAG cycle detection.
	if cycle := detectCycle(w.Steps); cycle != "" {
		errs = append(errs, fmt.Sprintf("dependency cycle detected: %s", cycle))
	}

	return errs
}

// validateStep checks a single step for correctness.
func validateStep(s WorkflowStep, allIDs map[string]bool) []string {
	var errs []string

	stepType := s.Type
	if stepType == "" {
		stepType = "dispatch"
	}

	switch stepType {
	case "dispatch":
		if s.Prompt == "" {
			errs = append(errs, fmt.Sprintf("step %q: dispatch step requires a prompt", s.ID))
		}
	case "skill":
		if s.Skill == "" {
			errs = append(errs, fmt.Sprintf("step %q: skill step requires a skill name", s.ID))
		}
	case "condition":
		if s.If == "" {
			errs = append(errs, fmt.Sprintf("step %q: condition step requires an 'if' expression", s.ID))
		}
		if s.Then == "" {
			errs = append(errs, fmt.Sprintf("step %q: condition step requires a 'then' target", s.ID))
		}
		if s.Then != "" && !allIDs[s.Then] {
			errs = append(errs, fmt.Sprintf("step %q: 'then' references unknown step %q", s.ID, s.Then))
		}
		if s.Else != "" && !allIDs[s.Else] {
			errs = append(errs, fmt.Sprintf("step %q: 'else' references unknown step %q", s.ID, s.Else))
		}
	case "handoff":
		if s.HandoffFrom == "" {
			errs = append(errs, fmt.Sprintf("step %q: handoff step requires 'handoffFrom' source step", s.ID))
		} else if !allIDs[s.HandoffFrom] {
			errs = append(errs, fmt.Sprintf("step %q: handoffFrom references unknown step %q", s.ID, s.HandoffFrom))
		}
		if s.Agent == "" {
			errs = append(errs, fmt.Sprintf("step %q: handoff step requires a target 'agent'", s.ID))
		}
	case "parallel":
		if len(s.Parallel) == 0 {
			errs = append(errs, fmt.Sprintf("step %q: parallel step requires sub-steps", s.ID))
		}
		subIDs := make(map[string]bool)
		for k := range allIDs {
			subIDs[k] = true
		}
		for _, sub := range s.Parallel {
			if sub.ID != "" {
				subIDs[sub.ID] = true
			}
		}
		for _, sub := range s.Parallel {
			errs = append(errs, validateStep(sub, subIDs)...)
		}
	// --- P18.3: Workflow Triggers --- New step types.
	case "tool_call":
		if s.ToolName == "" {
			errs = append(errs, fmt.Sprintf("step %q: tool_call step requires a toolName", s.ID))
		}
	case "delay":
		if s.Delay == "" {
			errs = append(errs, fmt.Sprintf("step %q: delay step requires a delay duration", s.ID))
		} else if _, err := time.ParseDuration(s.Delay); err != nil {
			errs = append(errs, fmt.Sprintf("step %q: invalid delay %q: %v", s.ID, s.Delay, err))
		}
	case "notify":
		if s.NotifyMsg == "" {
			errs = append(errs, fmt.Sprintf("step %q: notify step requires a notifyMsg", s.ID))
		}
	case "external":
		if s.ExternalBody != nil && s.ExternalRawBody != "" {
			errs = append(errs, fmt.Sprintf("step %q: externalBody and externalRawBody are mutually exclusive", s.ID))
		}
		if s.CallbackMode != "" && s.CallbackMode != "single" && s.CallbackMode != "streaming" {
			errs = append(errs, fmt.Sprintf("step %q: callbackMode must be \"single\" or \"streaming\"", s.ID))
		}
		if s.CallbackAuth != "" && s.CallbackAuth != "bearer" && s.CallbackAuth != "open" && s.CallbackAuth != "signature" {
			errs = append(errs, fmt.Sprintf("step %q: callbackAuth must be \"bearer\", \"open\", or \"signature\"", s.ID))
		}
		if s.OnTimeout != "" && s.OnTimeout != "stop" && s.OnTimeout != "skip" {
			errs = append(errs, fmt.Sprintf("step %q: onTimeout must be \"stop\" or \"skip\"", s.ID))
		}
		if s.CallbackTimeout != "" {
			if d, err := parseDurationWithDays(s.CallbackTimeout); err != nil {
				errs = append(errs, fmt.Sprintf("step %q: invalid callbackTimeout %q: %v", s.ID, s.CallbackTimeout, err))
			} else if d < time.Second {
				errs = append(errs, fmt.Sprintf("step %q: callbackTimeout must be at least 1s", s.ID))
			}
		}
		if s.CallbackKey != "" && !strings.Contains(s.CallbackKey, "{{") {
			// Only validate static keys (not template expressions).
			if !isValidCallbackKey(s.CallbackKey) {
				errs = append(errs, fmt.Sprintf("step %q: invalid callbackKey format %q", s.ID, s.CallbackKey))
			}
		}
	default:
		errs = append(errs, fmt.Sprintf("step %q: unknown type %q (use dispatch, skill, condition, parallel, handoff, tool_call, delay, notify, external)", s.ID, stepType))
	}

	// Validate dependency references.
	for _, dep := range s.DependsOn {
		if !allIDs[dep] {
			errs = append(errs, fmt.Sprintf("step %q: dependsOn references unknown step %q", s.ID, dep))
		}
		if dep == s.ID {
			errs = append(errs, fmt.Sprintf("step %q: step cannot depend on itself", s.ID))
		}
	}

	// Validate timeout.
	if s.Timeout != "" {
		if _, err := time.ParseDuration(s.Timeout); err != nil {
			errs = append(errs, fmt.Sprintf("step %q: invalid timeout %q", s.ID, s.Timeout))
		}
	}
	if s.RetryDelay != "" {
		if _, err := time.ParseDuration(s.RetryDelay); err != nil {
			errs = append(errs, fmt.Sprintf("step %q: invalid retryDelay %q", s.ID, s.RetryDelay))
		}
	}

	// Validate onError.
	if s.OnError != "" {
		switch s.OnError {
		case "stop", "skip", "retry":
		default:
			errs = append(errs, fmt.Sprintf("step %q: invalid onError %q (use stop, skip, retry)", s.ID, s.OnError))
		}
	}

	return errs
}

var workflowNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func isValidWorkflowName(name string) bool {
	return workflowNameRe.MatchString(name)
}

// --- DAG Cycle Detection (Kahn's algorithm) ---

// detectCycle returns a description of any dependency cycle, or "" if none.
func detectCycle(steps []WorkflowStep) string {
	// Build adjacency list and in-degree count.
	adj := make(map[string][]string)
	inDeg := make(map[string]int)
	for _, s := range steps {
		if _, ok := inDeg[s.ID]; !ok {
			inDeg[s.ID] = 0
		}
		for _, dep := range s.DependsOn {
			adj[dep] = append(adj[dep], s.ID)
			inDeg[s.ID]++
		}
		if _, ok := adj[s.ID]; !ok {
			adj[s.ID] = nil
		}
	}

	// Kahn's algorithm: process nodes with in-degree 0.
	var queue []string
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adj[node] {
			inDeg[next]--
			if inDeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if visited < len(inDeg) {
		// Collect cycle participants.
		var cycleNodes []string
		for id, deg := range inDeg {
			if deg > 0 {
				cycleNodes = append(cycleNodes, id)
			}
		}
		return strings.Join(cycleNodes, " → ")
	}

	return ""
}

// topologicalSort returns step IDs in execution order. Assumes no cycles.
func topologicalSort(steps []WorkflowStep) []string {
	adj := make(map[string][]string)
	inDeg := make(map[string]int)
	for _, s := range steps {
		if _, ok := inDeg[s.ID]; !ok {
			inDeg[s.ID] = 0
		}
		for _, dep := range s.DependsOn {
			adj[dep] = append(adj[dep], s.ID)
			inDeg[s.ID]++
		}
		if _, ok := adj[s.ID]; !ok {
			adj[s.ID] = nil
		}
	}

	var queue []string
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	var order []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)
		for _, next := range adj[node] {
			inDeg[next]--
			if inDeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	return order
}

// --- Variable Template System ---

// WorkflowContext holds runtime state for variable resolution.
type WorkflowContext struct {
	Input   map[string]string            // workflow input variables
	Steps   map[string]*WorkflowStepResult // completed step results
	Env     map[string]string            // environment snapshot
}

// WorkflowStepResult stores the output of a completed step.
type WorkflowStepResult struct {
	Output string `json:"output"`
	Status string `json:"status"` // "success", "error", "skipped", "timeout"
	Error  string `json:"error,omitempty"`
}

// newWorkflowContext creates a fresh context with merged variables.
func newWorkflowContext(w *Workflow, inputOverrides map[string]string) *WorkflowContext {
	ctx := &WorkflowContext{
		Input: make(map[string]string),
		Steps: make(map[string]*WorkflowStepResult),
		Env:   make(map[string]string),
	}
	// Copy workflow defaults.
	for k, v := range w.Variables {
		ctx.Input[k] = v
	}
	// Apply overrides.
	for k, v := range inputOverrides {
		ctx.Input[k] = v
	}
	// Snapshot relevant env vars (avoid leaking full env).
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i >= 0 {
			ctx.Env[e[:i]] = e[i+1:]
		}
	}
	return ctx
}

// templateVarRe matches {{...}} template expressions.
var templateVarRe = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// resolveTemplate replaces {{...}} placeholders in a string using the workflow context.
// Supported patterns:
//   - {{input}}                — value of input variable "input" (if only one, treat as shortcut)
//   - {{varName}}              — workflow input variable
//   - {{steps.ID.output}}      — step output
//   - {{steps.ID.status}}      — step status
//   - {{steps.ID.error}}       — step error
//   - {{env.KEY}}              — environment variable
func resolveTemplate(tmpl string, wCtx *WorkflowContext) string {
	return templateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		expr := strings.TrimSpace(match[2 : len(match)-2])
		return resolveExpr(expr, wCtx)
	})
}

// resolveExpr resolves a single template expression.
func resolveExpr(expr string, wCtx *WorkflowContext) string {
	parts := strings.SplitN(expr, ".", 3)

	switch {
	case parts[0] == "steps" && len(parts) >= 3:
		stepID := parts[1]
		field := parts[2]
		result, ok := wCtx.Steps[stepID]
		if !ok {
			logWarn("workflow template: step not found", "expr", expr)
			return ""
		}
		switch field {
		case "output":
			output := result.Output
			// Truncate to prevent context overflow.
			const defaultContextMax = 16000
			if len(output) > defaultContextMax {
				output = truncateToChars(output, defaultContextMax)
			}
			return output
		case "status":
			return result.Status
		case "error":
			return result.Error
		default:
			return ""
		}

	case parts[0] == "env" && len(parts) >= 2:
		key := strings.Join(parts[1:], ".")
		return wCtx.Env[key]

	default:
		// Simple input variable lookup.
		if v, ok := wCtx.Input[expr]; ok {
			return v
		}
		return ""
	}
}

// --- Condition Evaluation ---

// evalCondition evaluates a simple condition expression.
// Supported: "expr == 'value'", "expr != 'value'", "expr" (truthy check).
func evalCondition(expr string, wCtx *WorkflowContext) bool {
	resolved := resolveTemplate(expr, wCtx)

	// Check for == operator.
	if i := strings.Index(resolved, "=="); i >= 0 {
		left := strings.TrimSpace(resolved[:i])
		right := strings.TrimSpace(resolved[i+2:])
		right = strings.Trim(right, "'\"")
		return left == right
	}

	// Check for != operator.
	if i := strings.Index(resolved, "!="); i >= 0 {
		left := strings.TrimSpace(resolved[:i])
		right := strings.TrimSpace(resolved[i+2:])
		right = strings.Trim(right, "'\"")
		return left != right
	}

	// Truthy check: non-empty and not "false"/"0" means true.
	resolved = strings.TrimSpace(resolved)
	return resolved != "" && resolved != "false" && resolved != "0"
}

// --- Utility ---

// getStepByID finds a step in a workflow by ID.
func getStepByID(w *Workflow, id string) *WorkflowStep {
	for i := range w.Steps {
		if w.Steps[i].ID == id {
			return &w.Steps[i]
		}
	}
	return nil
}

// stepType returns the effective type of a step (default "dispatch").
func stepType(s *WorkflowStep) string {
	if s.Type == "" {
		return "dispatch"
	}
	return s.Type
}

// --- Template Gallery ---

// TemplateSummary provides a brief summary of a workflow template.
type TemplateSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	StepCount   int      `json:"stepCount"`
	Variables   []string `json:"variables"`
	Category    string   `json:"category"`
}

// cachedTemplates holds the pre-computed template summaries (static from embed.FS).
var cachedTemplates []TemplateSummary

// listTemplates returns summaries of all embedded workflow templates (cached after first call).
func listTemplates() []TemplateSummary {
	if cachedTemplates != nil {
		return cachedTemplates
	}
	entries, err := templateFS.ReadDir("examples/templates")
	if err != nil {
		return nil
	}
	var templates []TemplateSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := templateFS.ReadFile("examples/templates/" + e.Name())
		if err != nil {
			continue
		}
		var wf Workflow
		if err := json.Unmarshal(data, &wf); err != nil {
			continue
		}
		var varNames []string
		for k := range wf.Variables {
			varNames = append(varNames, k)
		}
		category := ""
		name := strings.TrimPrefix(wf.Name, "tpl-")
		if idx := strings.Index(name, "-"); idx > 0 {
			category = name[:idx]
		}
		templates = append(templates, TemplateSummary{
			Name:        wf.Name,
			Description: wf.Description,
			StepCount:   len(wf.Steps),
			Variables:   varNames,
			Category:    category,
		})
	}
	cachedTemplates = templates
	return templates
}

// loadTemplate loads a full workflow template by name.
func loadTemplate(name string) (*Workflow, error) {
	// Sanitize to prevent path traversal.
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("invalid template name")
	}
	fileName := name + ".json"
	if !strings.HasPrefix(name, "tpl-") {
		fileName = "tpl-" + fileName
	}
	data, err := templateFS.ReadFile("examples/templates/" + fileName)
	if err != nil {
		return nil, fmt.Errorf("template %q not found", name)
	}
	var wf Workflow
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	return &wf, nil
}

// installTemplate copies a template to the user's workflows directory with an optional new name.
func installTemplate(cfg *Config, templateName, newName string) error {
	wf, err := loadTemplate(templateName)
	if err != nil {
		return err
	}
	if newName != "" {
		wf.Name = newName
		// Also update step IDs prefix if it starts with old template name
	}
	return saveWorkflow(cfg, wf)
}

// buildStepTask converts a workflow dispatch step into a Task for execution.
func buildStepTask(s *WorkflowStep, wCtx *WorkflowContext, workflowName string) Task {
	return Task{
		ID:             newUUID(),
		Name:           fmt.Sprintf("%s/%s", workflowName, s.ID),
		Prompt:         resolveTemplate(s.Prompt, wCtx),
		Agent:          resolveTemplate(s.Agent, wCtx),
		Model:          resolveTemplate(s.Model, wCtx),
		Provider:       resolveTemplate(s.Provider, wCtx),
		Timeout:        resolveTemplate(s.Timeout, wCtx),
		Budget:         s.Budget,
		PermissionMode: resolveTemplate(s.PermissionMode, wCtx),
		Source:         "workflow:" + workflowName,
	}
}
