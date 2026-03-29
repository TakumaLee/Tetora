package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"tetora/internal/config"
	"tetora/internal/log"
)

// routeSem is a dedicated semaphore for routing LLM calls.
// Routing should never compete with task execution for slots,
// otherwise new messages block until running tasks complete.
var routeSem = make(chan struct{}, 5)

// CheckBindings checks if the request matches any channel/user binding rules.
// Returns nil if no binding match is found.
func CheckBindings(cfg *config.Config, req RouteRequest) *RouteResult {
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
				Agent:      binding.Agent,
				Method:     "binding",
				Confidence: "high",
				Reason:     fmt.Sprintf("matched binding rule for channel=%s", binding.Channel),
			}
		}
	}

	return nil
}

// inAllowed returns true if agent is in the allowed list, or the list is empty/nil.
func inAllowed(agent string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == agent {
			return true
		}
	}
	return false
}

// ClassifyByKeywords checks routing rules and agent keywords for a match.
// allowedAgents restricts which agents may be returned; nil/empty means no restriction.
// Returns nil if no keyword match is found.
func ClassifyByKeywords(cfg *config.Config, prompt string, allowedAgents []string) *RouteResult {
	lower := strings.ToLower(prompt)

	// Check explicit routing rules first (higher priority).
	for _, rule := range cfg.SmartDispatch.Rules {
		if !inAllowed(rule.Agent, allowedAgents) {
			continue
		}
		for _, kw := range rule.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return &RouteResult{
					Agent:      rule.Agent,
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
					Agent:      rule.Agent,
					Method:     "keyword",
					Confidence: "high",
					Reason:     fmt.Sprintf("matched rule pattern %q", pat),
				}
			}
		}
	}

	// Check agent-level keywords (lower priority).
	for agentName, rc := range cfg.Agents {
		if !inAllowed(agentName, allowedAgents) {
			continue
		}
		for _, kw := range rc.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return &RouteResult{
					Agent:      agentName,
					Method:     "keyword",
					Confidence: "medium",
					Reason:     fmt.Sprintf("matched agent keyword %q", kw),
				}
			}
		}
	}

	return nil
}

// ParseLLMRouteResult extracts RouteResult from LLM output.
func ParseLLMRouteResult(output, defaultAgent string) (*RouteResult, error) {
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

// ShouldReview determines if a task result should be reviewed by the coordinator.
// Reviews are triggered by: low routing confidence, high task cost, or explicit priority.
func ShouldReview(cfg *config.Config, routeResult *RouteResult, taskCost float64) bool {
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

// ClassifyByLLM asks the coordinator agent to classify the task.
// It uses the provided TaskExecutor to invoke the LLM without pulling
// root-package semaphores into this package.
func ClassifyByLLM(ctx context.Context, cfg *config.Config, prompt string, exec TaskExecutor) (*RouteResult, error) {
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
		Agent:   coordinator,
	}
	FillDefaults(cfg, &task)

	// Step 1: Try with haiku for cost efficiency.
	task.Model = "haiku"

	result := exec.RunTask(ctx, task, coordinator)
	if result.Status != "success" {
		return nil, fmt.Errorf("classification failed: %s", result.Error)
	}

	parsed, err := ParseLLMRouteResult(result.Output, cfg.SmartDispatch.DefaultAgent)
	if err != nil {
		return nil, err
	}

	// Step 2: If low confidence, escalate to sonnet.
	if parsed.Confidence == "low" {
		log.Info("route: haiku confidence low, escalating to sonnet", "reason", parsed.Reason)
		task.Model = "sonnet"
		result2 := exec.RunTask(ctx, task, coordinator)
		if result2.Status == "success" {
			parsed2, err2 := ParseLLMRouteResult(result2.Output, cfg.SmartDispatch.DefaultAgent)
			if err2 == nil {
				parsed2.Method = "llm-escalated"
				return parsed2, nil
			}
		}
		// If escalation also fails, return the haiku result.
	}

	return parsed, nil
}

// RouteTask determines which agent should handle the given prompt.
// Priority: bindings → keywords → LLM/coordinator fallback.
// The TaskExecutor is used only for the LLM classification path.
func RouteTask(ctx context.Context, cfg *config.Config, req RouteRequest, exec TaskExecutor) *RouteResult {
	// Tier 1: Check bindings (highest priority).
	if result := CheckBindings(cfg, req); result != nil {
		if _, ok := cfg.Agents[result.Agent]; ok {
			return result
		}
		log.WarnCtx(ctx, "binding matched agent not in config, falling through", "agent", result.Agent)
	}

	// Tier 2: Keyword matching.
	if result := ClassifyByKeywords(cfg, req.Prompt, nil); result != nil {
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
			Agent:      cfg.SmartDispatch.DefaultAgent,
			Method:     "coordinator",
			Confidence: "high",
			Reason:     "fallback mode set to coordinator",
		}
	}

	// Smart fallback: LLM classification.
	result, err := ClassifyByLLM(ctx, cfg, req.Prompt, exec)
	if err != nil {
		log.WarnCtx(ctx, "LLM classify error, using default", "error", err)
		return &RouteResult{
			Agent:      cfg.SmartDispatch.DefaultAgent,
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
