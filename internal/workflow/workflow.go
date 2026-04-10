package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
	"tetora/internal/version"
)

// workflowDir returns the workflows directory under baseDir.
func WorkflowDir(cfg *config.Config) string {
	return filepath.Join(cfg.BaseDir, "workflows")
}

// EnsureWorkflowDir creates the workflows directory if missing.
func EnsureWorkflowDir(cfg *config.Config) error {
	return os.MkdirAll(WorkflowDir(cfg), 0o755)
}

// --- Load / Save ---

// LoadWorkflow reads a single workflow JSON file.
func LoadWorkflow(path string) (*Workflow, error) {
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

// LoadWorkflowByName loads a workflow by name from the workflows directory.
// Falls back to scanning all files if no {name}.json exists (handles filename != internal name).
func LoadWorkflowByName(cfg *config.Config, name string) (*Workflow, error) {
	path := filepath.Join(WorkflowDir(cfg), name+".json")
	wf, err := LoadWorkflow(path)
	if err == nil {
		return wf, nil
	}
	// Fallback: scan directory for a workflow with matching internal name.
	entries, dirErr := os.ReadDir(WorkflowDir(cfg))
	if dirErr != nil {
		return nil, err // return original error
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		w, loadErr := LoadWorkflow(filepath.Join(WorkflowDir(cfg), e.Name()))
		if loadErr == nil && w.Name == name {
			return w, nil
		}
	}
	return nil, err
}

// SaveWorkflow writes a workflow to the workflows directory.
func SaveWorkflow(cfg *config.Config, w *Workflow) error {
	if err := EnsureWorkflowDir(cfg); err != nil {
		return err
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workflow: %w", err)
	}
	path := filepath.Join(WorkflowDir(cfg), w.Name+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	// Auto-snapshot workflow version.
	version.SnapshotWorkflow(cfg.HistoryDB, w.Name, string(data), "system", "")
	return nil
}

// DeleteWorkflow removes a workflow file.
func DeleteWorkflow(cfg *config.Config, name string) error {
	path := filepath.Join(WorkflowDir(cfg), name+".json")
	// Snapshot before deletion.
	if data, err := os.ReadFile(path); err == nil {
		version.SnapshotWorkflow(cfg.HistoryDB, name, string(data), "system", "pre-delete")
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workflow %q not found", name)
		}
		return err
	}
	return nil
}

// ListWorkflows returns all workflow files from the workflows directory.
func ListWorkflows(cfg *config.Config) ([]*Workflow, error) {
	dir := WorkflowDir(cfg)
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
		w, err := LoadWorkflow(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip malformed files
		}
		workflows = append(workflows, w)
	}
	return workflows, nil
}

// RestoreWorkflowVersion restores a workflow to a saved version.
func RestoreWorkflowVersion(dbPath string, cfg *config.Config, versionID string) error {
	ver, err := version.QueryByID(dbPath, versionID)
	if err != nil {
		return err
	}
	if ver.EntityType != "workflow" {
		return fmt.Errorf("version %q is a %s, not a workflow", versionID, ver.EntityType)
	}

	// Validate the stored content.
	var wf Workflow
	if err := json.Unmarshal([]byte(ver.ContentJSON), &wf); err != nil {
		return fmt.Errorf("stored version has invalid workflow JSON: %w", err)
	}

	// Read current workflow for backup snapshot (if it exists).
	existing, err := LoadWorkflowByName(cfg, ver.EntityName)
	if err == nil && existing != nil {
		data, _ := json.MarshalIndent(existing, "", "  ")
		version.SnapshotEntity(dbPath, "workflow", ver.EntityName, string(data), "system", fmt.Sprintf("pre-rollback to %s", versionID))
	}

	// Save the restored workflow.
	if err := SaveWorkflow(cfg, &wf); err != nil {
		return fmt.Errorf("write restored workflow: %w", err)
	}

	// Snapshot the restored state.
	version.SnapshotEntity(dbPath, "workflow", ver.EntityName, ver.ContentJSON, "system", fmt.Sprintf("rollback to %s", versionID))

	return nil
}

// --- Validation ---

// ValidateWorkflow checks a workflow for structural correctness.
func ValidateWorkflow(w *Workflow) []string {
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
		errs = append(errs, ValidateStep(s, ids)...)
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
	if cycle := DetectCycle(w.Steps); cycle != "" {
		errs = append(errs, fmt.Sprintf("dependency cycle detected: %s", cycle))
	}

	// Template reference validation.
	errs = append(errs, ValidateTemplateRefs(w)...)

	return errs
}

// stepTemplateRefRe matches {{steps.STEP_ID.field}} expressions.
var stepTemplateRefRe = regexp.MustCompile(`\{\{steps\.([^.}]+)\.[^}]+\}\}`)

// ValidateTemplateRefs checks that {{steps.X.*}} template expressions reference known step IDs.
// It does not validate plain input variable references (e.g. {{taskId}}) since those are
// supplied at runtime and may not be declared in the workflow's variables map.
func ValidateTemplateRefs(w *Workflow) []string {
	// Collect all known step IDs: top-level and parallel sub-step IDs.
	stepIDs := make(map[string]bool)
	for _, s := range w.Steps {
		if s.ID != "" {
			stepIDs[s.ID] = true
		}
		for _, sub := range s.Parallel {
			if sub.ID != "" {
				stepIDs[sub.ID] = true
			}
		}
	}

	var errs []string
	for _, s := range w.Steps {
		errs = append(errs, checkStepTemplateRefs(s, stepIDs, "")...)
	}
	return errs
}

// checkStepTemplateRefs validates {{steps.X.*}} references in all string fields of a step.
func checkStepTemplateRefs(s WorkflowStep, stepIDs map[string]bool, parentID string) []string {
	var errs []string

	loc := s.ID
	if parentID != "" {
		loc = parentID + "/" + s.ID
	}

	checkField := func(fieldName, value string) {
		for _, ref := range extractStepRefs(value) {
			if !stepIDs[ref] {
				errs = append(errs, fmt.Sprintf("step %q field %q: template references unknown step %q", loc, fieldName, ref))
			}
		}
	}

	checkField("prompt", s.Prompt)
	checkField("if", s.If)
	checkField("notifyMsg", s.NotifyMsg)
	checkField("humanPrompt", s.HumanPrompt)
	checkField("humanAssignee", s.HumanAssignee)
	checkField("externalUrl", s.ExternalURL)
	checkField("externalRawBody", s.ExternalRawBody)
	checkField("callbackKey", s.CallbackKey)
	checkField("agent", s.Agent)
	checkField("model", s.Model)

	for i, arg := range s.SkillArgs {
		checkField(fmt.Sprintf("skillArgs[%d]", i), arg)
	}
	for k, v := range s.ExternalHeaders {
		checkField(fmt.Sprintf("externalHeaders[%q]", k), v)
	}
	for k, v := range s.ExternalBody {
		checkField(fmt.Sprintf("externalBody[%q]", k), v)
	}
	for k, v := range s.ToolInput {
		checkField(fmt.Sprintf("toolInput[%q]", k), v)
	}
	for k, v := range s.HumanContext {
		checkField(fmt.Sprintf("humanContext[%q]", k), v)
	}

	for _, sub := range s.Parallel {
		errs = append(errs, checkStepTemplateRefs(sub, stepIDs, s.ID)...)
	}

	return errs
}

// extractStepRefs returns all step IDs referenced via {{steps.STEP_ID.*}} expressions.
func extractStepRefs(s string) []string {
	matches := stepTemplateRefRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	refs := make([]string, 0, len(matches))
	for _, m := range matches {
		refs = append(refs, m[1])
	}
	return refs
}

// ValidateStep checks a single step for correctness.
func ValidateStep(s WorkflowStep, allIDs map[string]bool) []string {
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
			errs = append(errs, ValidateStep(sub, subIDs)...)
		}
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
	case "human":
		subtype := s.HumanSubtype
		if subtype == "" {
			subtype = "approval"
		}
		switch subtype {
		case "approval", "action", "input":
		default:
			errs = append(errs, fmt.Sprintf("step %q: humanSubtype must be \"approval\", \"action\", or \"input\"", s.ID))
		}
		if subtype == "input" && s.HumanInputKey == "" {
			errs = append(errs, fmt.Sprintf("step %q: input subtype requires humanInputKey", s.ID))
		}
		if s.HumanTimeout != "" {
			if _, err := ParseDurationWithDays(s.HumanTimeout); err != nil {
				errs = append(errs, fmt.Sprintf("step %q: invalid humanTimeout %q: %v", s.ID, s.HumanTimeout, err))
			}
		}
		if s.HumanOnTimeout != "" {
			switch s.HumanOnTimeout {
			case "stop", "skip", "approve":
			default:
				errs = append(errs, fmt.Sprintf("step %q: humanOnTimeout must be \"stop\", \"skip\", or \"approve\"", s.ID))
			}
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
			if d, err := ParseDurationWithDays(s.CallbackTimeout); err != nil {
				errs = append(errs, fmt.Sprintf("step %q: invalid callbackTimeout %q: %v", s.ID, s.CallbackTimeout, err))
			} else if d < time.Second {
				errs = append(errs, fmt.Sprintf("step %q: callbackTimeout must be at least 1s", s.ID))
			}
		}
		if s.CallbackKey != "" && !strings.Contains(s.CallbackKey, "{{") {
			if !IsValidCallbackKey(s.CallbackKey) {
				errs = append(errs, fmt.Sprintf("step %q: invalid callbackKey format %q", s.ID, s.CallbackKey))
			}
		}
	default:
		errs = append(errs, fmt.Sprintf("step %q: unknown type %q (use dispatch, skill, condition, parallel, handoff, tool_call, delay, notify, external, human)", s.ID, stepType))
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

// DetectCycle returns a description of any dependency cycle, or "" if none.
func DetectCycle(steps []WorkflowStep) string {
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

// TopologicalSort returns step IDs in execution order. Assumes no cycles.
func TopologicalSort(steps []WorkflowStep) []string {
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

// NewWorkflowContext creates a fresh context with merged variables.
func NewWorkflowContext(w *Workflow, inputOverrides map[string]string) *WorkflowContext {
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
var TemplateVarRe = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// ResolveTemplate replaces {{...}} placeholders in a string using the workflow context.
func ResolveTemplate(tmpl string, wCtx *WorkflowContext) string {
	return TemplateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		expr := strings.TrimSpace(match[2 : len(match)-2])
		return ResolveExpr(expr, wCtx)
	})
}

// ResolveExpr resolves a single template expression.
func ResolveExpr(expr string, wCtx *WorkflowContext) string {
	parts := strings.SplitN(expr, ".", 3)

	switch {
	case parts[0] == "steps" && len(parts) >= 3:
		stepID := parts[1]
		field := parts[2]
		result, ok := wCtx.Steps[stepID]
		if !ok {
			log.Warn("workflow template: step not found", "expr", expr)
			return ""
		}
		switch field {
		case "output":
			output := result.Output
			// Truncate to prevent context overflow.
			const defaultContextMax = 16000
			if len(output) > defaultContextMax {
				output = TruncateToChars(output, defaultContextMax)
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

// TruncateToChars truncates a string to maxChars Unicode code points.
func TruncateToChars(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars])
}

// --- Condition Evaluation ---

// EvalCondition evaluates a simple condition expression.
func EvalCondition(expr string, wCtx *WorkflowContext) bool {
	resolved := ResolveTemplate(expr, wCtx)

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

// GetStepByID finds a step in a workflow by ID.
func GetStepByID(w *Workflow, id string) *WorkflowStep {
	for i := range w.Steps {
		if w.Steps[i].ID == id {
			return &w.Steps[i]
		}
	}
	return nil
}

// StepType returns the effective type of a step (default "dispatch").
func StepType(s *WorkflowStep) string {
	if s.Type == "" {
		return "dispatch"
	}
	return s.Type
}

// BuildStepSummaries returns step metadata for DAG visualization.
func BuildStepSummaries(steps []WorkflowStep) []map[string]any {
	var out []map[string]any
	for _, s := range steps {
		out = append(out, map[string]any{
			"id":        s.ID,
			"type":      StepType(&s),
			"role":      s.Agent,
			"dependsOn": s.DependsOn,
		})
	}
	return out
}

// --- Validation helpers ---

var callbackKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// IsValidCallbackKey checks that a callback key is well-formed.
func IsValidCallbackKey(key string) bool {
	if len(key) == 0 || len(key) > 256 {
		return false
	}
	return callbackKeyRegex.MatchString(key)
}

// ParseDurationWithDays extends time.ParseDuration to support "d" suffix for days.
func ParseDurationWithDays(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		var days int
		if _, err := fmt.Sscanf(numStr, "%d", &days); err != nil {
			return 0, fmt.Errorf("invalid days: %s", s)
		}
		if days < 0 || days > 30 {
			return 0, fmt.Errorf("days out of range (0-30): %d", days)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// ValidateTriggerConfig checks a trigger config for errors.
func ValidateTriggerConfig(t config.WorkflowTriggerConfig, existingNames map[string]bool) []string {
	var errs []string
	if t.Name == "" {
		errs = append(errs, "name is required")
	}
	if existingNames != nil && existingNames[t.Name] {
		errs = append(errs, fmt.Sprintf("name %q already exists", t.Name))
	}
	if t.WorkflowName == "" {
		errs = append(errs, "workflowName is required")
	}
	switch t.Trigger.Type {
	case "cron":
		if t.Trigger.Cron == "" {
			errs = append(errs, "cron expression required for cron trigger")
		}
	case "event":
		if t.Trigger.Event == "" {
			errs = append(errs, "event pattern required for event trigger")
		}
	case "webhook":
		if t.Trigger.Webhook == "" {
			errs = append(errs, "webhook ID required for webhook trigger")
		}
	case "":
		errs = append(errs, "trigger type is required (cron, event, webhook)")
	default:
		errs = append(errs, fmt.Sprintf("unknown trigger type: %s", t.Trigger.Type))
	}
	return errs
}

// --- Variable Expansion for Tool Inputs ---

// ExpandVars replaces {{key}} with values from the vars map.
func ExpandVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// ExpandToolInput expands {{var}} in all tool input values.
func ExpandToolInput(input map[string]string, vars map[string]string) map[string]string {
	if len(input) == 0 {
		return input
	}
	result := make(map[string]string, len(input))
	for k, v := range input {
		result[k] = ExpandVars(v, vars)
	}
	return result
}
