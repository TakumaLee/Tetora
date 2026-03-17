package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	dtypes "tetora/internal/dispatch"
	"tetora/internal/log"
)

// --- Smart Dispatch Types (aliases to internal/dispatch) ---

type RouteRequest = dtypes.RouteRequest
type RouteResult = dtypes.RouteResult
type SmartDispatchResult = dtypes.SmartDispatchResult

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
				Agent:       binding.Agent,
				Method:     "binding",
				Confidence: "high",
				Reason:     fmt.Sprintf("matched binding rule for channel=%s", binding.Channel),
			}
		}
	}

	return nil
}

// --- Keyword Classification (Fast Path) ---

// classifyByKeywords checks routing rules and agent keywords for a match.
// Returns nil if no keyword match is found.
func classifyByKeywords(cfg *Config, prompt string) *RouteResult {
	lower := strings.ToLower(prompt)

	// Check explicit routing rules first (higher priority).
	for _, rule := range cfg.SmartDispatch.Rules {
		for _, kw := range rule.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return &RouteResult{
					Agent:       rule.Agent,
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
					Agent:       rule.Agent,
					Method:     "keyword",
					Confidence: "high",
					Reason:     fmt.Sprintf("matched rule pattern %q", pat),
				}
			}
		}
	}

	// Check agent-level keywords (lower priority).
	for agentName, rc := range cfg.Agents {
		for _, kw := range rc.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return &RouteResult{
					Agent:       agentName,
					Method:     "keyword",
					Confidence: "medium",
					Reason:     fmt.Sprintf("matched agent keyword %q", kw),
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

// classifyByLLM asks the coordinator agent to classify the task.
func classifyByLLM(ctx context.Context, cfg *Config, prompt string) (*RouteResult, error) {
	coordinator := cfg.SmartDispatch.Coordinator

	// Build agent list for the classification prompt.
	var roleLines []string
	for name, rc := range cfg.Agents {
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
	for name := range cfg.Agents {
		validKeys = append(validKeys, name)
	}
	sort.Strings(validKeys)

	classifyPrompt := fmt.Sprintf(
		`You are a task router. Given a user request, decide which team member should handle it.

Available agents:
%s

IMPORTANT: The "role" field in your response MUST be one of these exact keys: %s
Do NOT use translated names, functional titles, or any other values.

User request: %s

Reply with ONLY a JSON object (no markdown, no explanation):
{"role":"<exact_role_key>","confidence":"high|medium|low","reason":"<brief reason>"}

If no agent is clearly appropriate, use %q as the default.`,
		strings.Join(roleLines, "\n"),
		strings.Join(validKeys, ", "),
		prompt,
		cfg.SmartDispatch.DefaultAgent,
	)

	task := Task{
		Prompt:  classifyPrompt,
		Timeout: cfg.SmartDispatch.ClassifyTimeout,
		Budget:  cfg.SmartDispatch.ClassifyBudget,
		Source:  "route-classify",
	}
	fillDefaults(cfg, &task)

	// Step 1: Try with haiku for cost efficiency.
	task.Model = "haiku"

	result := runSingleTask(ctx, cfg, task, routeSem, nil, coordinator)
	if result.Status != "success" {
		return nil, fmt.Errorf("classification failed: %s", result.Error)
	}

	parsed, err := parseLLMRouteResult(result.Output, cfg.SmartDispatch.DefaultAgent)
	if err != nil {
		return nil, err
	}

	// Step 2: If low confidence, escalate to sonnet.
	if parsed.Confidence == "low" {
		log.Info("route: haiku confidence low, escalating to sonnet", "reason", parsed.Reason)
		task.Model = "sonnet"
		result2 := runSingleTask(ctx, cfg, task, routeSem, nil, coordinator)
		if result2.Status == "success" {
			parsed2, err2 := parseLLMRouteResult(result2.Output, cfg.SmartDispatch.DefaultAgent)
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
func parseLLMRouteResult(output, defaultAgent string) (*RouteResult, error) {
	// Try to find JSON in the output (LLM may wrap it in text).
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return &RouteResult{
			Agent: defaultAgent, Method: "llm", Confidence: "low",
			Reason: "could not parse LLM response",
		}, nil
	}

	var result RouteResult
	if err := json.Unmarshal([]byte(output[start:end+1]), &result); err != nil {
		return &RouteResult{
			Agent: defaultAgent, Method: "llm", Confidence: "low",
			Reason: "JSON parse error: " + err.Error(),
		}, nil
	}
	result.Method = "llm"

	if result.Agent == "" {
		result.Agent = defaultAgent
	}
	if result.Confidence == "" {
		result.Confidence = "medium"
	}

	return &result, nil
}

// --- Multi-Tier Route ---

// routeTask determines which agent should handle the given prompt.
// Priority: bindings → keywords → LLM/coordinator fallback.
func routeTask(ctx context.Context, cfg *Config, req RouteRequest) *RouteResult {
	// Tier 1: Check bindings (highest priority).
	if result := checkBindings(cfg, req); result != nil {
		if _, ok := cfg.Agents[result.Agent]; ok {
			return result
		}
		log.WarnCtx(ctx, "binding matched agent not in config, falling through", "agent", result.Agent)
	}

	// Tier 2: Keyword matching.
	if result := classifyByKeywords(cfg, req.Prompt); result != nil {
		if _, ok := cfg.Agents[result.Agent]; ok {
			return result
		}
		log.WarnCtx(ctx, "keyword matched agent not in config, falling through", "agent", result.Agent)
	}

	// Tier 3: Fallback mode.
	fallbackMode := cfg.SmartDispatch.Fallback
	if fallbackMode == "" {
		fallbackMode = "smart" // default to smart routing
	}

	if fallbackMode == "coordinator" {
		// Direct fallback to coordinator (no LLM call).
		return &RouteResult{
			Agent:       cfg.SmartDispatch.DefaultAgent,
			Method:     "coordinator",
			Confidence: "high",
			Reason:     "fallback mode set to coordinator",
		}
	}

	// Smart fallback: LLM classification.
	result, err := classifyByLLM(ctx, cfg, req.Prompt)
	if err != nil {
		log.WarnCtx(ctx, "LLM classify error, using default", "error", err)
		return &RouteResult{
			Agent:       cfg.SmartDispatch.DefaultAgent,
			Method:     "default",
			Confidence: "low",
			Reason:     "LLM classification failed: " + err.Error(),
		}
	}

	// Validate agent exists.
	if _, ok := cfg.Agents[result.Agent]; !ok {
		result.Agent = cfg.SmartDispatch.DefaultAgent
		result.Confidence = "low"
		result.Reason += " (agent not found, using default)"
	}

	return result
}

// --- Full Smart Dispatch Pipeline ---

// smartDispatch is the full pipeline: route → dispatch → memory → review → audit.
func smartDispatch(ctx context.Context, cfg *Config, prompt string, source string,
	state *dispatchState, sem, childSem chan struct{}) *SmartDispatchResult {

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

	log.InfoCtx(ctx, "route decision",
		"prompt", truncate(prompt, 60), "role", route.Agent,
		"method", route.Method, "confidence", route.Confidence)

	// Publish task_routing to dashboard.
	if state != nil && state.broker != nil {
		state.broker.Publish(SSEDashboardKey, SSEEvent{
			Type: SSETaskRouting,
			Data: map[string]any{
				"source":     source,
				"role":       route.Agent,
				"method":     route.Method,
				"confidence": route.Confidence,
			},
		})
	}

	// Step 2: Build and run task with the selected agent.
	task := Task{
		Prompt: prompt,
		Agent:   route.Agent,
		Source: "route:" + source,
	}
	fillDefaults(cfg, &task)

	// Inject agent soul prompt + model + permission mode.
	if route.Agent != "" {
		if soulPrompt, err := loadAgentPrompt(cfg, route.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := cfg.Agents[route.Agent]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	// Expand template variables.
	task.Prompt = expandPrompt(task.Prompt, "", cfg.HistoryDB, route.Agent, cfg.KnowledgeDir, cfg)

	// Step 3: Execute with optional Dev↔QA retry loop.
	taskStart := time.Now()
	var result TaskResult
	var totalCost float64
	var qaApproved bool
	var attempts int

	if cfg.SmartDispatch.ReviewLoop {
		// Dev↔QA retry loop: execute → review → retry with feedback (max N retries).
		loopResult := routeDevQALoop(ctx, cfg, task, prompt, route.Agent, sem, childSem)
		result = loopResult.Result
		totalCost = loopResult.TotalCost
		qaApproved = loopResult.QAApproved
		attempts = loopResult.Attempts
	} else {
		result = runSingleTask(ctx, cfg, task, sem, childSem, route.Agent)
		totalCost = result.CostUSD
		attempts = 1
	}

	// Record to history.
	recordHistory(cfg.HistoryDB, task.ID, task.Name, task.Source, route.Agent, task, result,
		taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record session activity.
	recordSessionActivity(cfg.HistoryDB, task, result, route.Agent)

	// Step 4: Store output summary in agent memory.
	if result.Status == "success" {
		setMemory(cfg, route.Agent, "last_route_output", truncate(result.Output, 500))
		setMemory(cfg, route.Agent, "last_route_prompt", truncate(prompt, 200))
		setMemory(cfg, route.Agent, "last_route_time", time.Now().Format(time.RFC3339))
	}

	sdr := &SmartDispatchResult{
		Route:    *route,
		Task:     result,
		Attempts: attempts,
	}

	// Use accumulated cost from all attempts.
	if totalCost > result.CostUSD {
		sdr.Task.CostUSD = totalCost
	}

	// Step 5: Review gate.
	if cfg.SmartDispatch.ReviewLoop {
		// Dev↔QA loop already handled review — propagate the result.
		sdr.ReviewOK = &qaApproved
		if !qaApproved && attempts > 1 {
			sdr.Review = fmt.Sprintf("Dev↔QA loop exhausted (%d attempts)", attempts)
		}
	} else if shouldReview(cfg, route, result.CostUSD) && result.Status == "success" {
		// Single-pass review (original behavior).
		reviewOK, reviewComment := reviewOutput(ctx, cfg, prompt, result.Output, route.Agent, sem, childSem)
		sdr.ReviewOK = &reviewOK
		sdr.Review = reviewComment
	}

	// Step 6: Audit log.
	auditLog(cfg.HistoryDB, "route.dispatch", source,
		fmt.Sprintf("role=%s method=%s confidence=%s attempts=%d prompt=%s",
			route.Agent, route.Method, route.Confidence, attempts, truncate(prompt, 100)), "")

	// Webhook notifications.
	sendWebhooks(cfg, result.Status, WebhookPayload{
		JobID:    task.ID,
		Name:     task.Name,
		Source:   task.Source,
		Status:   result.Status,
		Cost:     totalCost,
		Duration: result.DurationMs,
		Model:    result.Model,
		Output:   truncate(result.Output, 500),
		Error:    truncate(result.Error, 300),
	})

	return sdr
}

// --- Route Dev↔QA Loop ---

// routeDevQALoop runs the Dev↔QA retry loop for smart dtypes.
// Unlike the taskboard version, this operates without a TaskBoard record.
//
// Flow: Dev execute → QA review → (pass → done) | (fail → record failure → inject feedback → retry)
func routeDevQALoop(ctx context.Context, cfg *Config, task Task, originalPrompt, agentName string, sem, childSem chan struct{}) devQALoopResult {
	maxRetries := cfg.SmartDispatch.MaxRetriesOrDefault() // default 3

	reviewer := cfg.SmartDispatch.ReviewAgent
	if reviewer == "" {
		reviewer = cfg.SmartDispatch.Coordinator
	}

	var accumulated float64

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Step 1: Dev execution.
		result := runSingleTask(ctx, cfg, task, sem, childSem, agentName)
		accumulated += result.CostUSD

		// If execution itself failed, exit loop immediately.
		if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// Step 2: QA review.
		reviewOK, reviewComment := reviewOutput(ctx, cfg, originalPrompt, result.Output, agentName, sem, childSem)
		if reviewOK {
			log.InfoCtx(ctx, "routeDevQA: review passed", "agent", agentName, "attempt", attempt+1)
			return devQALoopResult{Result: result, QAApproved: true, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// QA failed.
		log.InfoCtx(ctx, "routeDevQA: review failed, injecting feedback",
			"agent", agentName, "attempt", attempt+1, "maxAttempts", maxRetries+1,
			"comment", truncate(reviewComment, 200))

		// Record QA rejection as skill failure for future context injection.
		qaFailMsg := fmt.Sprintf("[QA rejection attempt %d] %s", attempt+1, reviewComment)
		skills := selectSkills(cfg, task)
		for _, s := range skills {
			appendSkillFailure(cfg, s.Name, task.Name, agentName, qaFailMsg)
		}

		if attempt == maxRetries {
			log.WarnCtx(ctx, "routeDevQA: max retries exhausted, escalating",
				"agent", agentName, "attempts", maxRetries+1)
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// Step 3: Rebuild prompt with failure context + QA feedback for retry.
		task.Prompt = originalPrompt

		// Inject accumulated skill failures.
		for _, s := range skills {
			failures := loadSkillFailuresByName(cfg, s.Name)
			if failures != "" {
				task.Prompt += fmt.Sprintf("\n\n<skill-failures name=\"%s\">\n%s\n</skill-failures>", s.Name, failures)
			}
		}

		// Inject QA reviewer's specific feedback.
		task.Prompt += fmt.Sprintf("\n\n## QA Review Feedback (Attempt %d)\n", attempt+1)
		task.Prompt += "The QA reviewer rejected the output. Issues found:\n"
		task.Prompt += reviewComment
		task.Prompt += fmt.Sprintf("\n\nAddress ALL issues above. This is retry %d of %d.\n", attempt+2, maxRetries+1)

		// Fresh session for retry.
		task.ID = newUUID()
		task.SessionID = newUUID()
	}

	return devQALoopResult{}
}

// --- Coordinator Review ---

// reviewOutput asks the review agent (or coordinator) to review the agent's output.
func reviewOutput(ctx context.Context, cfg *Config, originalPrompt, output, agentRole string, sem, childSem chan struct{}) (bool, string) {
	// Use dedicated review agent if configured, otherwise fall back to coordinator.
	reviewer := cfg.SmartDispatch.Coordinator
	if cfg.SmartDispatch.ReviewAgent != "" {
		reviewer = cfg.SmartDispatch.ReviewAgent
	}

	reviewPrompt := fmt.Sprintf(
		`Review this agent output for quality and correctness.

Original request: %s

Agent (%s) output:
%s

Reply with ONLY a JSON object:
{"ok":true,"comment":"brief comment"} or {"ok":false,"comment":"what's wrong and what evidence is missing"}`,
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

	// Inject review agent's SOUL prompt and model.
	if soulPrompt, err := loadAgentPrompt(cfg, reviewer); err == nil && soulPrompt != "" {
		task.SystemPrompt = soulPrompt
	}
	if rc, ok := cfg.Agents[reviewer]; ok {
		if rc.Model != "" {
			task.Model = rc.Model
		}
		if rc.PermissionMode != "" {
			task.PermissionMode = rc.PermissionMode
		}
	}

	result := runSingleTask(ctx, cfg, task, sem, childSem, reviewer)
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
