package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// --- Smart Dispatch Types ---

// RouteRequest is the input for the routing engine.
type RouteRequest struct {
	Prompt    string `json:"prompt"`
	Source    string `json:"source,omitempty"`    // "telegram", "http", "cli", "slack", "discord"
	UserID    string `json:"userId,omitempty"`    // user ID (telegram, discord, etc.)
	ChannelID string `json:"channelId,omitempty"` // channel/chat ID (slack, telegram group, etc.)
	GuildID   string `json:"guildId,omitempty"`   // guild/server ID (discord)
}

// RouteResult represents the outcome of smart dispatch routing.
type RouteResult struct {
	Role       string `json:"role"`              // selected role
	Method     string `json:"method"`            // "keyword", "llm", "default"
	Confidence string `json:"confidence"`        // "high", "medium", "low"
	Reason     string `json:"reason,omitempty"`  // why this role was selected
}

// SmartDispatchResult is the full result of a routed task.
type SmartDispatchResult struct {
	Route    RouteResult `json:"route"`
	Task     TaskResult  `json:"task"`
	ReviewOK *bool       `json:"reviewOk,omitempty"` // nil if no review
	Review   string      `json:"review,omitempty"`   // review comment from coordinator
}

// --- Binding Classification (Highest Priority) ---

// checkBindings checks if the request matches any channel/user binding rules.
// Returns nil if no binding match is found.
func checkBindings(cfg *Config, req RouteRequest) *RouteResult {
	for _, binding := range cfg.SmartDispatch.Bindings {
		// Channel must match.
		if binding.Channel != "" && binding.Channel != req.Source {
			continue
		}

		// Check if any of the ID fields match.
		matched := false
		if binding.UserID != "" && binding.UserID == req.UserID {
			matched = true
		}
		if binding.ChannelID != "" && binding.ChannelID == req.ChannelID {
			matched = true
		}
		if binding.GuildID != "" && binding.GuildID == req.GuildID {
			matched = true
		}

		// If channel matches and at least one ID matches, return this binding.
		if matched {
			return &RouteResult{
				Role:       binding.Role,
				Method:     "binding",
				Confidence: "high",
				Reason:     fmt.Sprintf("matched binding rule for channel=%s", binding.Channel),
			}
		}
	}

	return nil
}

// --- Keyword Classification (Fast Path) ---

// classifyByKeywords checks routing rules and role keywords for a match.
// Returns nil if no keyword match is found.
func classifyByKeywords(cfg *Config, prompt string) *RouteResult {
	lower := strings.ToLower(prompt)

	// Check explicit routing rules first (higher priority).
	for _, rule := range cfg.SmartDispatch.Rules {
		for _, kw := range rule.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return &RouteResult{
					Role:       rule.Role,
					Method:     "keyword",
					Confidence: "high",
					Reason:     fmt.Sprintf("matched rule keyword %q", kw),
				}
			}
		}
		for _, pat := range rule.Patterns {
			re, err := regexp.Compile("(?i)" + pat)
			if err != nil {
				continue
			}
			if re.MatchString(prompt) {
				return &RouteResult{
					Role:       rule.Role,
					Method:     "keyword",
					Confidence: "high",
					Reason:     fmt.Sprintf("matched rule pattern %q", pat),
				}
			}
		}
	}

	// Check role-level keywords (lower priority).
	for roleName, rc := range cfg.Roles {
		for _, kw := range rc.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return &RouteResult{
					Role:       roleName,
					Method:     "keyword",
					Confidence: "medium",
					Reason:     fmt.Sprintf("matched role keyword %q", kw),
				}
			}
		}
	}

	return nil
}

// --- LLM Classification (Slow Path) ---

// routeSem is a dedicated semaphore for routing LLM calls.
// Routing should never compete with task execution for slots,
// otherwise new messages block until running tasks complete.
var routeSem = make(chan struct{}, 5)

// classifyByLLM asks the coordinator role to classify the task.
func classifyByLLM(ctx context.Context, cfg *Config, prompt string) (*RouteResult, error) {
	coordinator := cfg.SmartDispatch.Coordinator

	// Build role list for the classification prompt.
	var roleLines []string
	for name, rc := range cfg.Roles {
		desc := rc.Description
		if desc == "" {
			desc = "(no description)"
		}
		kws := ""
		if len(rc.Keywords) > 0 {
			kws = " [keywords: " + strings.Join(rc.Keywords, ", ") + "]"
		}
		roleLines = append(roleLines, fmt.Sprintf("- %s: %s%s", name, desc, kws))
	}
	// Sort for deterministic output.
	sort.Strings(roleLines)

	// Build valid keys list for explicit constraint.
	var validKeys []string
	for name := range cfg.Roles {
		validKeys = append(validKeys, name)
	}
	sort.Strings(validKeys)

	classifyPrompt := fmt.Sprintf(
		`You are a task router. Given a user request, decide which team member should handle it.

Available roles:
%s

IMPORTANT: The "role" field in your response MUST be one of these exact keys: %s
Do NOT use translated names, functional titles, or any other values.

User request: %s

Reply with ONLY a JSON object (no markdown, no explanation):
{"role":"<exact_role_key>","confidence":"high|medium|low","reason":"<brief reason>"}

If no role is clearly appropriate, use %q as the default.`,
		strings.Join(roleLines, "\n"),
		strings.Join(validKeys, ", "),
		prompt,
		cfg.SmartDispatch.DefaultRole,
	)

	task := Task{
		Prompt:  classifyPrompt,
		Timeout: cfg.SmartDispatch.ClassifyTimeout,
		Budget:  cfg.SmartDispatch.ClassifyBudget,
		Source:  "route-classify",
	}
	fillDefaults(cfg, &task)

	// Step 1: Try with sonnet for cost efficiency.
	task.Model = "sonnet"

	result := runSingleTask(ctx, cfg, task, routeSem, coordinator)
	if result.Status != "success" {
		return nil, fmt.Errorf("classification failed: %s", result.Error)
	}

	parsed, err := parseLLMRouteResult(result.Output, cfg.SmartDispatch.DefaultRole)
	if err != nil {
		return nil, err
	}

	// Step 2: If low confidence, escalate to opus.
	if parsed.Confidence == "low" {
		logInfo("route: sonnet confidence low, escalating to opus", "reason", parsed.Reason)
		task.Model = "opus"
		result2 := runSingleTask(ctx, cfg, task, routeSem, coordinator)
		if result2.Status == "success" {
			parsed2, err2 := parseLLMRouteResult(result2.Output, cfg.SmartDispatch.DefaultRole)
			if err2 == nil {
				parsed2.Method = "llm-escalated"
				return parsed2, nil
			}
		}
		// If opus also fails, return the sonnet result.
	}

	return parsed, nil
}

// parseLLMRouteResult extracts RouteResult from LLM output.
func parseLLMRouteResult(output, defaultRole string) (*RouteResult, error) {
	// Try to find JSON in the output (LLM may wrap it in text).
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return &RouteResult{
			Role: defaultRole, Method: "llm", Confidence: "low",
			Reason: "could not parse LLM response",
		}, nil
	}

	var result RouteResult
	if err := json.Unmarshal([]byte(output[start:end+1]), &result); err != nil {
		return &RouteResult{
			Role: defaultRole, Method: "llm", Confidence: "low",
			Reason: "JSON parse error: " + err.Error(),
		}, nil
	}
	result.Method = "llm"

	if result.Role == "" {
		result.Role = defaultRole
	}
	if result.Confidence == "" {
		result.Confidence = "medium"
	}

	return &result, nil
}

// --- Multi-Tier Route ---

// routeTask determines which role should handle the given prompt.
// Priority: bindings → keywords → LLM/coordinator fallback.
func routeTask(ctx context.Context, cfg *Config, req RouteRequest) *RouteResult {
	// Tier 1: Check bindings (highest priority).
	if result := checkBindings(cfg, req); result != nil {
		if _, ok := cfg.Roles[result.Role]; ok {
			return result
		}
		logWarnCtx(ctx, "binding matched role not in config, falling through", "role", result.Role)
	}

	// Tier 2: Keyword matching.
	if result := classifyByKeywords(cfg, req.Prompt); result != nil {
		if _, ok := cfg.Roles[result.Role]; ok {
			return result
		}
		logWarnCtx(ctx, "keyword matched role not in config, falling through", "role", result.Role)
	}

	// Tier 3: Fallback mode.
	fallbackMode := cfg.SmartDispatch.Fallback
	if fallbackMode == "" {
		fallbackMode = "smart" // default to smart routing
	}

	if fallbackMode == "coordinator" {
		// Direct fallback to coordinator (no LLM call).
		return &RouteResult{
			Role:       cfg.SmartDispatch.DefaultRole,
			Method:     "coordinator",
			Confidence: "high",
			Reason:     "fallback mode set to coordinator",
		}
	}

	// Smart fallback: LLM classification.
	result, err := classifyByLLM(ctx, cfg, req.Prompt)
	if err != nil {
		logWarnCtx(ctx, "LLM classify error, using default", "error", err)
		return &RouteResult{
			Role:       cfg.SmartDispatch.DefaultRole,
			Method:     "default",
			Confidence: "low",
			Reason:     "LLM classification failed: " + err.Error(),
		}
	}

	// Validate role exists.
	if _, ok := cfg.Roles[result.Role]; !ok {
		result.Role = cfg.SmartDispatch.DefaultRole
		result.Confidence = "low"
		result.Reason += " (role not found, using default)"
	}

	return result
}

// --- Full Smart Dispatch Pipeline ---

// smartDispatch is the full pipeline: route → dispatch → memory → review → audit.
func smartDispatch(ctx context.Context, cfg *Config, prompt string, source string,
	state *dispatchState, sem chan struct{}) *SmartDispatchResult {

	// Publish task_received to dashboard.
	if state != nil && state.broker != nil {
		state.broker.Publish(SSEDashboardKey, SSEEvent{
			Type: SSETaskReceived,
			Data: map[string]any{
				"source": source,
				"prompt": truncate(prompt, 200),
			},
		})
	}

	// Step 1: Route.
	route := routeTask(ctx, cfg, RouteRequest{Prompt: prompt, Source: source})

	logInfoCtx(ctx, "route decision",
		"prompt", truncate(prompt, 60), "role", route.Role,
		"method", route.Method, "confidence", route.Confidence)

	// Publish task_routing to dashboard.
	if state != nil && state.broker != nil {
		state.broker.Publish(SSEDashboardKey, SSEEvent{
			Type: SSETaskRouting,
			Data: map[string]any{
				"source":     source,
				"role":       route.Role,
				"method":     route.Method,
				"confidence": route.Confidence,
			},
		})
	}

	// Step 2: Build and run task with the selected role.
	task := Task{
		Prompt: prompt,
		Role:   route.Role,
		Source: "route:" + source,
	}
	fillDefaults(cfg, &task)

	// Inject role soul prompt + model + permission mode.
	if route.Role != "" {
		if soulPrompt, err := loadRolePrompt(cfg, route.Role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := cfg.Roles[route.Role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	// Expand template variables.
	task.Prompt = expandPrompt(task.Prompt, "", cfg.HistoryDB, route.Role, cfg.KnowledgeDir, cfg)

	taskStart := time.Now()
	result := runSingleTask(ctx, cfg, task, sem, route.Role)

	// Record to history.
	recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, route.Role, task, result,
		taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity.
	recordSessionActivity(cfg.HistoryDB, task, result, route.Role)

	// Step 3: Store output summary in agent memory.
	if result.Status == "success" {
		setMemory(cfg, route.Role, "last_route_output", truncate(result.Output, 500))
		setMemory(cfg, route.Role, "last_route_prompt", truncate(prompt, 200))
		setMemory(cfg, route.Role, "last_route_time", time.Now().Format(time.RFC3339))
	}

	sdr := &SmartDispatchResult{
		Route: *route,
		Task:  result,
	}

	// Step 4: Optional coordinator review (conditional trigger).
	if shouldReview(cfg, route, result.CostUSD) && result.Status == "success" {
		reviewOK, reviewComment := reviewOutput(ctx, cfg, prompt, result.Output, route.Role, sem)
		sdr.ReviewOK = &reviewOK
		sdr.Review = reviewComment
	}

	// Step 5: Audit log.
	auditLog(cfg.HistoryDB, "route.dispatch", source,
		fmt.Sprintf("role=%s method=%s confidence=%s prompt=%s",
			route.Role, route.Method, route.Confidence, truncate(prompt, 100)), "")

	// Webhook notifications.
	sendWebhooks(cfg, result.Status, WebhookPayload{
		JobID:    task.ID,
		Name:     task.Name,
		Source:   task.Source,
		Status:   result.Status,
		Cost:     result.CostUSD,
		Duration: result.DurationMs,
		Model:    result.Model,
		Output:   truncate(result.Output, 500),
		Error:    truncate(result.Error, 300),
	})

	return sdr
}

// --- Coordinator Review ---

// reviewOutput asks the coordinator to review the agent's output.
func reviewOutput(ctx context.Context, cfg *Config, originalPrompt, output, agentRole string, sem chan struct{}) (bool, string) {
	coordinator := cfg.SmartDispatch.Coordinator

	reviewPrompt := fmt.Sprintf(
		`Review this agent output for quality and correctness.

Original request: %s

Agent (%s) output:
%s

Is this output satisfactory? Reply with ONLY a JSON object:
{"ok":true,"comment":"brief comment"} or {"ok":false,"comment":"what's wrong"}`,
		truncate(originalPrompt, 300),
		agentRole,
		truncate(output, 2000),
	)

	task := Task{
		Prompt:  reviewPrompt,
		Timeout: cfg.SmartDispatch.ClassifyTimeout,
		Budget:  cfg.SmartDispatch.ReviewBudget,
		Source:  "route-review",
	}
	fillDefaults(cfg, &task)

	// Use coordinator's model.
	if rc, ok := cfg.Roles[coordinator]; ok && rc.Model != "" {
		task.Model = rc.Model
	}

	result := runSingleTask(ctx, cfg, task, sem, coordinator)
	if result.Status != "success" {
		return true, "review skipped (error)"
	}

	// Parse review JSON.
	start := strings.Index(result.Output, "{")
	end := strings.LastIndex(result.Output, "}")
	if start >= 0 && end > start {
		var review struct {
			OK      bool   `json:"ok"`
			Comment string `json:"comment"`
		}
		if json.Unmarshal([]byte(result.Output[start:end+1]), &review) == nil {
			return review.OK, review.Comment
		}
	}

	return true, "review parse error"
}

// --- Conditional Review Trigger ---

// shouldReview determines if a task result should be reviewed by the coordinator.
// Reviews are triggered by: low routing confidence, high task cost, or explicit priority.
func shouldReview(cfg *Config, routeResult *RouteResult, taskCost float64) bool {
	if !cfg.SmartDispatch.Review {
		return false
	}
	// Condition 1: routing confidence was low.
	if routeResult != nil && routeResult.Confidence == "low" {
		return true
	}
	// Condition 2: task cost exceeded threshold ($0.10).
	if taskCost > 0.10 {
		return true
	}
	return false
}
