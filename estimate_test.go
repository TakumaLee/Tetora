package main

import (
	"testing"
)

func TestEstimateInputTokens(t *testing.T) {
	// ~25 chars => ~6 tokens (with min 10)
	tokens := estimateInputTokens("Hello, how are you today?", "")
	if tokens < 5 {
		t.Errorf("expected >=5, got %d", tokens)
	}
}

func TestEstimateInputTokensWithSystem(t *testing.T) {
	// Use longer strings to avoid the minimum threshold.
	prompt := "Please explain the theory of relativity in detail with examples"
	tokensNoSys := estimateInputTokens(prompt, "")
	tokensWithSys := estimateInputTokens(prompt, "You are a physics professor with 20 years of experience in theoretical physics.")
	if tokensWithSys <= tokensNoSys {
		t.Error("system prompt should increase token count")
	}
}

func TestEstimateInputTokensMinimum(t *testing.T) {
	tokens := estimateInputTokens("Hi", "")
	if tokens < 10 {
		t.Errorf("minimum should be 10, got %d", tokens)
	}
}

func TestEstimateInputTokensLong(t *testing.T) {
	long := make([]byte, 4000)
	for i := range long {
		long[i] = 'a'
	}
	tokens := estimateInputTokens(string(long), "")
	if tokens < 900 || tokens > 1100 {
		t.Errorf("expected ~1000 tokens for 4000 chars, got %d", tokens)
	}
}

func TestResolvePricingExact(t *testing.T) {
	cfg := &Config{
		Pricing: map[string]ModelPricing{
			"sonnet": {Model: "sonnet", InputPer1M: 3.0, OutputPer1M: 15.0},
		},
	}
	p := resolvePricing(cfg, "sonnet")
	if p.InputPer1M != 3.0 {
		t.Errorf("expected 3.0, got %f", p.InputPer1M)
	}
}

func TestResolvePricingDefault(t *testing.T) {
	cfg := &Config{}
	p := resolvePricing(cfg, "sonnet")
	if p.InputPer1M != 3.0 {
		t.Errorf("expected default 3.0, got %f", p.InputPer1M)
	}
}

func TestResolvePricingFallback(t *testing.T) {
	cfg := &Config{}
	p := resolvePricing(cfg, "unknown-model-xyz")
	if p.InputPer1M != 2.50 {
		t.Errorf("expected fallback 2.50, got %f", p.InputPer1M)
	}
}

func TestResolvePricingPrefixMatch(t *testing.T) {
	cfg := &Config{}
	p := resolvePricing(cfg, "claude-3-5-sonnet-20241022")
	if p.InputPer1M != 3.0 {
		t.Errorf("expected sonnet pricing 3.0, got %f", p.InputPer1M)
	}
}

func TestResolvePricingConfigOverride(t *testing.T) {
	cfg := &Config{
		Pricing: map[string]ModelPricing{
			"sonnet": {Model: "sonnet", InputPer1M: 5.0, OutputPer1M: 25.0},
		},
	}
	p := resolvePricing(cfg, "sonnet")
	if p.InputPer1M != 5.0 {
		t.Errorf("expected config override 5.0, got %f", p.InputPer1M)
	}
}

func TestEstimateTaskCostBasic(t *testing.T) {
	cfg := &Config{
		DefaultModel:    "sonnet",
		DefaultProvider: "claude",
		Providers: map[string]ProviderConfig{
			"claude": {Type: "claude-cli", Path: "claude"},
		},
		Estimate: EstimateConfig{DefaultOutputTokens: 500},
	}
	task := Task{
		Prompt: "Write a hello world program in Go",
	}
	fillDefaults(cfg, &task)
	est := estimateTaskCost(cfg, task, "")
	if est.EstimatedCostUSD <= 0 {
		t.Error("expected positive cost estimate")
	}
	if est.Model != "sonnet" {
		t.Errorf("expected model sonnet, got %s", est.Model)
	}
	if est.Provider != "claude" {
		t.Errorf("expected provider claude, got %s", est.Provider)
	}
	if est.EstimatedTokensIn <= 0 {
		t.Error("expected positive input tokens")
	}
	if est.EstimatedTokensOut != 500 {
		t.Errorf("expected 500 output tokens (default), got %d", est.EstimatedTokensOut)
	}
}

func TestEstimateTaskCostWithRole(t *testing.T) {
	cfg := &Config{
		DefaultModel:    "sonnet",
		DefaultProvider: "claude",
		Providers: map[string]ProviderConfig{
			"claude": {Type: "claude-cli", Path: "claude"},
		},
		Roles: map[string]RoleConfig{
			"黒曜": {Model: "opus", Provider: "claude"},
		},
		Estimate: EstimateConfig{DefaultOutputTokens: 500},
	}
	task := Task{Prompt: "Fix the bug"}
	fillDefaults(cfg, &task)
	est := estimateTaskCost(cfg, task, "黒曜")
	if est.Model != "opus" {
		t.Errorf("expected model opus from role, got %s", est.Model)
	}
}

func TestEstimateTasksWithSmartDispatch(t *testing.T) {
	cfg := &Config{
		DefaultModel:    "sonnet",
		DefaultProvider: "claude",
		Providers: map[string]ProviderConfig{
			"claude": {Type: "claude-cli", Path: "claude"},
		},
		SmartDispatch: SmartDispatchConfig{
			Enabled:     true,
			Coordinator: "琉璃",
			DefaultRole: "琉璃",
		},
		Roles: map[string]RoleConfig{
			"琉璃": {Model: "sonnet"},
		},
		Estimate: EstimateConfig{DefaultOutputTokens: 500},
	}
	tasks := []Task{{Prompt: "Analyze this code"}}
	result := estimateTasks(cfg, tasks)
	if result.ClassifyCost <= 0 {
		t.Error("expected classification cost when smart dispatch is enabled")
	}
	if result.TotalEstimatedCost <= 0 {
		t.Error("expected positive total estimate")
	}
	if len(result.Tasks) != 1 {
		t.Errorf("expected 1 task estimate, got %d", len(result.Tasks))
	}
}

func TestEstimateTasksWithExplicitRole(t *testing.T) {
	cfg := &Config{
		DefaultModel:    "sonnet",
		DefaultProvider: "claude",
		Providers: map[string]ProviderConfig{
			"claude": {Type: "claude-cli", Path: "claude"},
		},
		SmartDispatch: SmartDispatchConfig{Enabled: true, Coordinator: "琉璃", DefaultRole: "琉璃"},
		Roles: map[string]RoleConfig{
			"黒曜": {Model: "sonnet", Provider: "claude"},
		},
		Estimate: EstimateConfig{DefaultOutputTokens: 500},
	}
	tasks := []Task{{Prompt: "Fix the bug", Role: "黒曜"}}
	result := estimateTasks(cfg, tasks)
	if result.ClassifyCost > 0 {
		t.Error("expected no classification cost with explicit role")
	}
}

func TestEstimateMultipleTasks(t *testing.T) {
	cfg := &Config{
		DefaultModel:    "sonnet",
		DefaultProvider: "claude",
		Providers: map[string]ProviderConfig{
			"claude": {Type: "claude-cli", Path: "claude"},
		},
		Estimate: EstimateConfig{DefaultOutputTokens: 500},
	}
	tasks := []Task{
		{Prompt: "Task one"},
		{Prompt: "Task two with a longer prompt to increase tokens"},
	}
	result := estimateTasks(cfg, tasks)
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 task estimates, got %d", len(result.Tasks))
	}
	if result.TotalEstimatedCost <= 0 {
		t.Error("expected positive total estimate")
	}
	sum := 0.0
	for _, e := range result.Tasks {
		sum += e.EstimatedCostUSD
	}
	if abs(result.TotalEstimatedCost-sum) > 0.0001 {
		t.Errorf("total %.6f != sum of parts %.6f", result.TotalEstimatedCost, sum)
	}
}

func TestDefaultPricing(t *testing.T) {
	dp := defaultPricing()
	models := []string{"opus", "sonnet", "haiku", "gpt-4o", "gpt-4o-mini"}
	for _, m := range models {
		p, ok := dp[m]
		if !ok {
			t.Errorf("missing default pricing for %s", m)
			continue
		}
		if p.InputPer1M <= 0 || p.OutputPer1M <= 0 {
			t.Errorf("invalid pricing for %s: in=%.2f out=%.2f", m, p.InputPer1M, p.OutputPer1M)
		}
	}
}

func TestEstimateConfigDefaults(t *testing.T) {
	var ec EstimateConfig
	if ec.confirmThresholdOrDefault() != 1.0 {
		t.Errorf("expected default threshold 1.0, got %f", ec.confirmThresholdOrDefault())
	}
	if ec.defaultOutputTokensOrDefault() != 500 {
		t.Errorf("expected default output tokens 500, got %d", ec.defaultOutputTokensOrDefault())
	}

	ec2 := EstimateConfig{ConfirmThreshold: 2.5, DefaultOutputTokens: 1000}
	if ec2.confirmThresholdOrDefault() != 2.5 {
		t.Errorf("expected 2.5, got %f", ec2.confirmThresholdOrDefault())
	}
	if ec2.defaultOutputTokensOrDefault() != 1000 {
		t.Errorf("expected 1000, got %d", ec2.defaultOutputTokensOrDefault())
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
