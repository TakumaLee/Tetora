package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// --- Mock ToolCapableProvider ---

// mockToolProvider is a scriptable ToolCapableProvider for integration tests.
// Each call to ExecuteWithTools pops the next result from the queue.
type mockToolProvider struct {
	name    string
	results []*ProviderResult
	calls   int
	mu      sync.Mutex
}

func (m *mockToolProvider) Name() string { return m.name }

func (m *mockToolProvider) Execute(_ context.Context, _ ProviderRequest) (*ProviderResult, error) {
	return &ProviderResult{Output: "mock-execute"}, nil
}

func (m *mockToolProvider) ExecuteWithTools(_ context.Context, req ProviderRequest) (*ProviderResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.calls
	m.calls++
	if idx >= len(m.results) {
		return &ProviderResult{Output: "exhausted", StopReason: "end_turn"}, nil
	}
	return m.results[idx], nil
}

// --- Helper to build a minimal Config with tool registry ---

func testConfigWithTools(tools ...*ToolDef) *Config {
	cfg := &Config{
		DefaultProvider: "mock",
	}
	r := &ToolRegistry{tools: make(map[string]*ToolDef)}
	for _, t := range tools {
		r.tools[t.Name] = t
	}
	cfg.toolRegistry = r
	return cfg
}

func testRegistry(p Provider) *providerRegistry {
	reg := newProviderRegistry()
	reg.register(p.Name(), p)
	return reg
}

// echoTool returns a simple tool that echoes its input.
func echoTool() *ToolDef {
	return &ToolDef{
		Name:        "echo",
		Description: "Echoes input",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`),
		Handler: func(_ context.Context, _ *Config, input json.RawMessage) (string, error) {
			var args struct{ Msg string }
			json.Unmarshal(input, &args)
			return "echo: " + args.Msg, nil
		},
		Builtin: true,
	}
}

// counterTool returns a tool that increments an atomic counter each call.
func counterTool(counter *atomic.Int64) *ToolDef {
	return &ToolDef{
		Name:        "counter",
		Description: "Increments counter",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ context.Context, _ *Config, _ json.RawMessage) (string, error) {
			n := counter.Add(1)
			return fmt.Sprintf("count=%d", n), nil
		},
		Builtin: true,
	}
}

// --- Integration Tests ---

func TestAgenticLoop_BasicToolCall(t *testing.T) {
	// Provider returns one tool_use, then end_turn.
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				Output:     "Let me echo that.",
				StopReason: "tool_use",
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "echo", Input: json.RawMessage(`{"msg":"hello"}`)},
				},
			},
			{
				Output:     "The echo returned: hello",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool())
	task := Task{ID: "t1", Prompt: "echo hello", Provider: "mock", Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output != "The echo returned: hello" {
		t.Errorf("unexpected output: %q", result.Output)
	}
	if provider.calls != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.calls)
	}
}

func TestAgenticLoop_MultipleIterations(t *testing.T) {
	var counter atomic.Int64
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				StopReason: "tool_use",
				ToolCalls:  []ToolCall{{ID: "tc1", Name: "counter", Input: json.RawMessage(`{}`)}},
			},
			{
				StopReason: "tool_use",
				ToolCalls:  []ToolCall{{ID: "tc2", Name: "counter", Input: json.RawMessage(`{}`)}},
			},
			{
				StopReason: "tool_use",
				ToolCalls:  []ToolCall{{ID: "tc3", Name: "counter", Input: json.RawMessage(`{}`)}},
			},
			{
				Output:     "done",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(counterTool(&counter))
	task := Task{ID: "t2", Prompt: "count 3 times", Provider: "mock", Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if counter.Load() != 3 {
		t.Errorf("expected counter=3, got %d", counter.Load())
	}
	if provider.calls != 4 {
		t.Errorf("expected 4 provider calls, got %d", provider.calls)
	}
}

func TestAgenticLoop_NoToolCalls(t *testing.T) {
	// Provider immediately returns end_turn, no tool calls.
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				Output:     "No tools needed.",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool())
	task := Task{ID: "t3", Prompt: "just answer", Provider: "mock", Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output != "No tools needed." {
		t.Errorf("unexpected output: %q", result.Output)
	}
	if provider.calls != 1 {
		t.Errorf("expected 1 provider call, got %d", provider.calls)
	}
}

func TestAgenticLoop_ToolNotFound(t *testing.T) {
	// Provider requests a tool that doesn't exist.
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				StopReason: "tool_use",
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "nonexistent_tool", Input: json.RawMessage(`{}`)},
				},
			},
			{
				Output:     "I see that tool wasn't found.",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool())
	task := Task{ID: "t4", Prompt: "use missing tool", Provider: "mock", Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	// The loop should continue after the tool-not-found error and reach the second response.
	if result.Output != "I see that tool wasn't found." {
		t.Errorf("unexpected output: %q", result.Output)
	}
}

func TestAgenticLoop_BudgetExceeded(t *testing.T) {
	// Per-task budget is a soft limit: it logs a warning but continues.
	// The loop should proceed past budget and finish normally.
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				Output:     "first call",
				StopReason: "tool_use",
				CostUSD:    0.50,
				ToolCalls:  []ToolCall{{ID: "tc1", Name: "echo", Input: json.RawMessage(`{"msg":"hi"}`)}},
			},
			{
				Output:     "completed despite budget",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool())
	task := Task{ID: "t5", Prompt: "expensive", Provider: "mock", Budget: 0.10, Source: "cron"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "",
		testRegistry(provider), nil, nil,
	)

	// Soft-limit: no hard error, loop continues past budget.
	if result.IsError {
		t.Fatalf("unexpected hard error: %s", result.Error)
	}
	// With soft-limit, the loop continues and the second provider call is reached.
	if result.Output != "completed despite budget" {
		t.Errorf("unexpected output: %q", result.Output)
	}
	// Provider should be called twice (budget is soft, loop continues).
	if provider.calls != 2 {
		t.Errorf("expected 2 provider calls (soft budget), got %d", provider.calls)
	}
}

func TestAgenticLoop_RoleFiltering(t *testing.T) {
	// Set up a role with limited tool access.
	var counter atomic.Int64
	provider := &mockToolProvider{
		name: "mock",
		results: []*ProviderResult{
			{
				StopReason: "tool_use",
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "echo", Input: json.RawMessage(`{"msg":"allowed"}`)},
					{ID: "tc2", Name: "counter", Input: json.RawMessage(`{}`)},
				},
			},
			{
				Output:     "done with role filtering",
				StopReason: "end_turn",
			},
		},
	}

	cfg := testConfigWithTools(echoTool(), counterTool(&counter))
	// Set up a role that only allows "echo", not "counter".
	cfg.Roles = map[string]RoleConfig{
		"limited": {
			ToolPolicy: RoleToolPolicy{
				Allow: []string{"echo"},
			},
		},
	}
	task := Task{ID: "t6", Prompt: "test role filtering", Provider: "mock", Role: "limited"}

	result := executeWithProviderAndTools(
		context.Background(), cfg, task, "limited",
		testRegistry(provider), nil, nil,
	)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	// Counter tool should NOT have been executed (blocked by policy).
	if counter.Load() != 0 {
		t.Errorf("counter tool should be blocked by role policy, got count=%d", counter.Load())
	}
}

func TestDispatchConcurrent_Race(t *testing.T) {
	// Run 5 concurrent executeWithProviderAndTools calls to detect data races.
	cfg := testConfigWithTools(echoTool())

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			provider := &mockToolProvider{
				name: "mock",
				results: []*ProviderResult{
					{
						StopReason: "tool_use",
						ToolCalls: []ToolCall{
							{ID: fmt.Sprintf("tc-%d", idx), Name: "echo", Input: json.RawMessage(`{"msg":"race"}`)},
						},
					},
					{
						Output:     fmt.Sprintf("done-%d", idx),
						StopReason: "end_turn",
					},
				},
			}
			task := Task{
				ID:       fmt.Sprintf("race-%d", idx),
				Prompt:   "race test",
				Provider: "mock",
			}
			result := executeWithProviderAndTools(
				context.Background(), cfg, task, "",
				testRegistry(provider), nil, nil,
			)
			if result.IsError {
				t.Errorf("goroutine %d got error: %s", idx, result.Error)
			}
		}(i)
	}
	wg.Wait()
}

func TestConfigReload_Race(t *testing.T) {
	// Simulate config reload during dispatch by mutating cfg.toolRegistry concurrently.
	echo := echoTool()
	cfg := testConfigWithTools(echo)

	// Goroutine that repeatedly re-registers the tool (simulating hot-reload).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var reloads atomic.Int64
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Simulate reload by re-registering.
				cfg.toolRegistry.Register(echo)
				reloads.Add(1)
			}
		}
	}()

	// Run dispatches concurrently with the reload goroutine.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			provider := &mockToolProvider{
				name: "mock",
				results: []*ProviderResult{
					{
						StopReason: "tool_use",
						ToolCalls: []ToolCall{
							{ID: fmt.Sprintf("tc-%d", idx), Name: "echo", Input: json.RawMessage(`{"msg":"reload"}`)},
						},
					},
					{
						Output:     "ok",
						StopReason: "end_turn",
					},
				},
			}
			task := Task{
				ID:       fmt.Sprintf("reload-%d", idx),
				Prompt:   "reload test",
				Provider: "mock",
			}
			result := executeWithProviderAndTools(
				context.Background(), cfg, task, "",
				testRegistry(provider), nil, nil,
			)
			if result.IsError {
				t.Errorf("goroutine %d got error: %s", idx, result.Error)
			}
		}(i)
	}
	wg.Wait()
	cancel()

	if reloads.Load() == 0 {
		t.Error("expected at least one reload to have occurred")
	}
}
