package main

import (
	"fmt"
	"strings"
)

// --- Cost Estimation Types ---

// ModelPricing defines per-model pricing rates.
type ModelPricing struct {
	Model           string  `json:"model"`
	InputPer1M      float64 `json:"inputPer1M"`               // USD per 1M input tokens
	OutputPer1M     float64 `json:"outputPer1M"`              // USD per 1M output tokens
	CacheReadPer1M  float64 `json:"cacheReadPer1M,omitempty"`  // USD per 1M cache read tokens
	CacheWritePer1M float64 `json:"cacheWritePer1M,omitempty"` // USD per 1M cache write tokens
}

// CostEstimate is the result for a single task estimation.
type CostEstimate struct {
	Name               string  `json:"name"`
	Provider           string  `json:"provider"`
	Model              string  `json:"model"`
	EstimatedCostUSD   float64 `json:"estimatedCostUsd"`
	EstimatedTokensIn  int     `json:"estimatedTokensIn"`
	EstimatedTokensOut int     `json:"estimatedTokensOut"`
	Breakdown          string  `json:"breakdown,omitempty"`
}

// EstimateResult is the full response for POST /dispatch/estimate.
type EstimateResult struct {
	Tasks              []CostEstimate `json:"tasks"`
	TotalEstimatedCost float64        `json:"totalEstimatedCostUsd"`
	ClassifyCost       float64        `json:"classifyCostUsd,omitempty"`
}

// --- Default Pricing ---

// defaultPricing returns built-in pricing for well-known models.
func defaultPricing() map[string]ModelPricing {
	return map[string]ModelPricing{
		// Claude models (cacheRead: 10% of input, cacheWrite: 125% of input)
		"opus":   {Model: "opus", InputPer1M: 15.00, OutputPer1M: 75.00, CacheReadPer1M: 1.50, CacheWritePer1M: 18.75},
		"sonnet": {Model: "sonnet", InputPer1M: 3.00, OutputPer1M: 15.00, CacheReadPer1M: 0.30, CacheWritePer1M: 3.75},
		"haiku":  {Model: "haiku", InputPer1M: 0.25, OutputPer1M: 1.25, CacheReadPer1M: 0.025, CacheWritePer1M: 0.3125},
		// OpenAI models
		"gpt-4o":      {Model: "gpt-4o", InputPer1M: 2.50, OutputPer1M: 10.00},
		"gpt-4o-mini": {Model: "gpt-4o-mini", InputPer1M: 0.15, OutputPer1M: 0.60},
		"gpt-4-turbo": {Model: "gpt-4-turbo", InputPer1M: 10.00, OutputPer1M: 30.00},
		"o1":          {Model: "o1", InputPer1M: 15.00, OutputPer1M: 60.00},
	}
}

// --- Token Estimation ---

// estimateInputTokens estimates input tokens using the len/4 heuristic.
// For mixed content (English, CJK, code), this is accurate within ~20%.
func estimateInputTokens(prompt, systemPrompt string) int {
	total := len(prompt) + len(systemPrompt)
	tokens := total / 4
	if tokens < 10 {
		tokens = 10
	}
	return tokens
}

// queryModelAvgOutput returns the average output tokens for a model from history DB.
// Uses the last 10 successful runs with that model that have tokens_out > 0.
func queryModelAvgOutput(dbPath, model string) int {
	if dbPath == "" || model == "" {
		return 0
	}
	sql := fmt.Sprintf(
		`SELECT COALESCE(AVG(tokens_out), 0) as avg_out
		 FROM (SELECT tokens_out FROM job_runs
		       WHERE model = '%s' AND status = 'success' AND tokens_out > 0
		       ORDER BY id DESC LIMIT 10)`,
		escapeSQLite(model))
	rows, err := queryDB(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return jsonInt(rows[0]["avg_out"])
}

// --- Pricing Resolution ---

// resolvePricing looks up pricing for a model.
// Chain: cfg.Pricing[exact] → cfg.Pricing[prefix] → defaults[exact] → defaults[prefix] → fallback.
func resolvePricing(cfg *Config, model string) ModelPricing {
	// Exact match in config.
	if cfg.Pricing != nil {
		if p, ok := cfg.Pricing[model]; ok {
			return p
		}
		// Prefix match in config.
		lm := strings.ToLower(model)
		for key, p := range cfg.Pricing {
			if strings.Contains(lm, strings.ToLower(key)) {
				return p
			}
		}
	}

	// Exact match in defaults.
	defaults := defaultPricing()
	if p, ok := defaults[model]; ok {
		return p
	}

	// Prefix match in defaults (e.g., "claude-3-5-sonnet-20241022" matches "sonnet").
	lm := strings.ToLower(model)
	for key, p := range defaults {
		if strings.Contains(lm, strings.ToLower(key)) {
			return p
		}
	}

	// Fallback: GPT-4o rates.
	return ModelPricing{Model: model, InputPer1M: 2.50, OutputPer1M: 10.00}
}

// --- Cost Estimation ---

// estimateTaskCost estimates the cost of a single task without executing it.
func estimateTaskCost(cfg *Config, task Task, roleName string) CostEstimate {
	providerName := resolveProviderName(cfg, task, roleName)

	model := task.Model
	if model == "" {
		if pc, ok := cfg.Providers[providerName]; ok && pc.Model != "" {
			model = pc.Model
		}
	}
	if model == "" {
		model = cfg.DefaultModel
	}

	// Inject role model if applicable.
	if roleName != "" {
		if rc, ok := cfg.Roles[roleName]; ok && rc.Model != "" {
			if task.Model == "" || task.Model == cfg.DefaultModel {
				model = rc.Model
			}
		}
	}

	// Estimate input tokens.
	tokensIn := estimateInputTokens(task.Prompt, task.SystemPrompt)

	// Estimate output tokens from history, fallback to config default.
	tokensOut := queryModelAvgOutput(cfg.HistoryDB, model)
	if tokensOut == 0 {
		tokensOut = cfg.Estimate.defaultOutputTokensOrDefault()
	}

	pricing := resolvePricing(cfg, model)

	costUSD := float64(tokensIn)*pricing.InputPer1M/1_000_000 +
		float64(tokensOut)*pricing.OutputPer1M/1_000_000

	return CostEstimate{
		Name:               task.Name,
		Provider:           providerName,
		Model:              model,
		EstimatedCostUSD:   costUSD,
		EstimatedTokensIn:  tokensIn,
		EstimatedTokensOut: tokensOut,
		Breakdown: fmt.Sprintf("~%d in + ~%d out @ $%.2f/$%.2f per 1M",
			tokensIn, tokensOut, pricing.InputPer1M, pricing.OutputPer1M),
	}
}

// estimateTasks estimates cost for multiple tasks.
// If smart dispatch is enabled and tasks have no explicit role, includes classification cost.
func estimateTasks(cfg *Config, tasks []Task) *EstimateResult {
	result := &EstimateResult{}

	for _, task := range tasks {
		fillDefaults(cfg, &task)
		roleName := task.Role

		// If no role and smart dispatch enabled, classification will happen.
		if roleName == "" && cfg.SmartDispatch.Enabled {
			// Estimate classification cost.
			classifyModel := cfg.DefaultModel
			if rc, ok := cfg.Roles[cfg.SmartDispatch.Coordinator]; ok && rc.Model != "" {
				classifyModel = rc.Model
			}
			classifyPricing := resolvePricing(cfg, classifyModel)
			// Classification prompt ~500 tokens in, ~50 tokens out.
			classifyCost := float64(500)*classifyPricing.InputPer1M/1_000_000 +
				float64(50)*classifyPricing.OutputPer1M/1_000_000
			result.ClassifyCost += classifyCost

			// Use keyword classification to guess likely role (no LLM call).
			if kr := classifyByKeywords(cfg, task.Prompt); kr != nil {
				roleName = kr.Role
			} else {
				roleName = cfg.SmartDispatch.DefaultRole
			}
		}

		est := estimateTaskCost(cfg, task, roleName)
		result.Tasks = append(result.Tasks, est)
		result.TotalEstimatedCost += est.EstimatedCostUSD
	}

	result.TotalEstimatedCost += result.ClassifyCost
	return result
}
