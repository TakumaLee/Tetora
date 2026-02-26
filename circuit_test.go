package main

import (
	"sync"
	"testing"
	"time"
)

// --- CircuitBreaker State Machine ---

func TestCircuitBreaker_InitialState(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{})
	if cb.State() != CircuitClosed {
		t.Errorf("expected initial state Closed, got %s", cb.State())
	}
	if cb.failures != 0 {
		t.Errorf("expected 0 failures, got %d", cb.failures)
	}
	if cb.successes != 0 {
		t.Errorf("expected 0 successes, got %d", cb.successes)
	}
}

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{
		FailThreshold:    3,
		SuccessThreshold: 2,
		OpenTimeout:      "1m",
	})

	// Record failures below threshold — should stay Closed.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitClosed {
		t.Fatalf("expected Closed after 2 failures (threshold 3), got %s", cb.State())
	}

	// Third failure should trip to Open.
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected Open after 3 failures, got %s", cb.State())
	}

	// Allow should return false when Open (timeout is 1m, well in the future).
	if cb.Allow() {
		t.Error("expected Allow()=false when circuit is Open")
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{
		FailThreshold:    2,
		SuccessThreshold: 1,
		OpenTimeout:      "50ms",
	})

	// Trip the circuit to Open.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected Open, got %s", cb.State())
	}

	// Before timeout: Allow should return false.
	if cb.Allow() {
		t.Error("expected Allow()=false immediately after tripping to Open")
	}

	// Wait for the open timeout to elapse.
	time.Sleep(70 * time.Millisecond)

	// After timeout: Allow should return true and state should transition to HalfOpen.
	if !cb.Allow() {
		t.Error("expected Allow()=true after openTimeout elapsed")
	}
	if cb.State() != CircuitHalfOpen {
		t.Errorf("expected HalfOpen after timeout, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToClose(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{
		FailThreshold:    2,
		SuccessThreshold: 3,
		OpenTimeout:      "50ms",
	})

	// Trip to Open, wait for HalfOpen.
	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(70 * time.Millisecond)
	cb.Allow() // triggers Open -> HalfOpen transition

	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected HalfOpen, got %s", cb.State())
	}

	// Record successes below threshold — should stay HalfOpen.
	cb.RecordSuccess()
	cb.RecordSuccess()
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected HalfOpen after 2 successes (threshold 3), got %s", cb.State())
	}

	// Third success should close the circuit.
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatalf("expected Closed after 3 successes, got %s", cb.State())
	}

	// Failure count should be reset.
	cb.mu.Lock()
	f := cb.failures
	s := cb.successes
	cb.mu.Unlock()
	if f != 0 {
		t.Errorf("expected failures=0 after close, got %d", f)
	}
	if s != 0 {
		t.Errorf("expected successes=0 after close, got %d", s)
	}
}

func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{
		FailThreshold:    2,
		SuccessThreshold: 3,
		OpenTimeout:      "50ms",
	})

	// Trip to Open, wait for HalfOpen.
	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(70 * time.Millisecond)
	cb.Allow() // triggers HalfOpen

	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected HalfOpen, got %s", cb.State())
	}

	// One success, then a failure — should go back to Open.
	cb.RecordSuccess()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected Open after failure in HalfOpen, got %s", cb.State())
	}
}

func TestCircuitBreaker_Allow(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{
		FailThreshold:    2,
		SuccessThreshold: 1,
		OpenTimeout:      "10s", // long timeout so Open stays Open
	})

	// Closed: Allow returns true.
	if !cb.Allow() {
		t.Error("expected Allow()=true when Closed")
	}

	// Trip to Open.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.Allow() {
		t.Error("expected Allow()=false when Open (timeout not elapsed)")
	}

	// Manually set to HalfOpen for test.
	cb.mu.Lock()
	cb.state = CircuitHalfOpen
	cb.mu.Unlock()
	if !cb.Allow() {
		t.Error("expected Allow()=true when HalfOpen")
	}
}

func TestCircuitBreaker_SuccessResetsFailures(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{
		FailThreshold:    5,
		SuccessThreshold: 2,
		OpenTimeout:      "1m",
	})

	// Accumulate some failures (but not enough to trip).
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()
	cb.mu.Lock()
	if cb.failures != 3 {
		t.Errorf("expected 3 failures, got %d", cb.failures)
	}
	cb.mu.Unlock()

	// A success should reset the failure counter.
	cb.RecordSuccess()
	cb.mu.Lock()
	f := cb.failures
	cb.mu.Unlock()
	if f != 0 {
		t.Errorf("expected failures=0 after success in Closed state, got %d", f)
	}

	// After reset, it should take the full threshold to trip again.
	for i := 0; i < 4; i++ {
		cb.RecordFailure()
	}
	if cb.State() != CircuitClosed {
		t.Error("expected Closed after 4 failures (threshold 5)")
	}
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Error("expected Open after 5 failures")
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{
		FailThreshold:    2,
		SuccessThreshold: 1,
		OpenTimeout:      "10m",
	})

	// Trip to Open.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected Open, got %s", cb.State())
	}

	// Manual reset should go back to Closed.
	cb.Reset()
	if cb.State() != CircuitClosed {
		t.Errorf("expected Closed after Reset, got %s", cb.State())
	}

	cb.mu.Lock()
	f := cb.failures
	s := cb.successes
	cb.mu.Unlock()
	if f != 0 {
		t.Errorf("expected failures=0 after Reset, got %d", f)
	}
	if s != 0 {
		t.Errorf("expected successes=0 after Reset, got %d", s)
	}

	// Should be able to Allow() again.
	if !cb.Allow() {
		t.Error("expected Allow()=true after Reset")
	}
}

// --- CircuitBreaker Status ---

func TestCircuitBreaker_StatusInfo(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{
		FailThreshold:    3,
		SuccessThreshold: 2,
		OpenTimeout:      "50ms",
	})

	// Closed state: should have state and failures, no lastFailure.
	info := cb.statusInfo()
	if info["state"] != "closed" {
		t.Errorf("expected state 'closed', got %v", info["state"])
	}
	if info["failures"] != 0 {
		t.Errorf("expected failures=0, got %v", info["failures"])
	}
	if _, hasLF := info["lastFailure"]; hasLF {
		t.Error("expected no lastFailure when no failures recorded")
	}
	if _, hasS := info["successes"]; hasS {
		t.Error("expected no successes key when not HalfOpen")
	}

	// Record failures to trip and verify status.
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()
	info = cb.statusInfo()
	if info["state"] != "open" {
		t.Errorf("expected state 'open', got %v", info["state"])
	}
	if info["failures"].(int) != 3 {
		t.Errorf("expected failures=3, got %v", info["failures"])
	}
	if _, hasLF := info["lastFailure"]; !hasLF {
		t.Error("expected lastFailure to be set after failures")
	}

	// Transition to HalfOpen and check that successes appear.
	time.Sleep(70 * time.Millisecond)
	cb.Allow()
	cb.RecordSuccess()
	info = cb.statusInfo()
	if info["state"] != "half-open" {
		t.Errorf("expected state 'half-open', got %v", info["state"])
	}
	if info["successes"].(int) != 1 {
		t.Errorf("expected successes=1, got %v", info["successes"])
	}
}

// --- Circuit Breaker Default Config ---

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	// Empty config should use defaults: failThreshold=5, successThreshold=2, openTimeout=30s.
	cb := newCircuitBreaker(CircuitBreakerConfig{})
	if cb.failThreshold != 5 {
		t.Errorf("expected default failThreshold=5, got %d", cb.failThreshold)
	}
	if cb.successThreshold != 2 {
		t.Errorf("expected default successThreshold=2, got %d", cb.successThreshold)
	}
	if cb.openTimeout != 30*time.Second {
		t.Errorf("expected default openTimeout=30s, got %v", cb.openTimeout)
	}

	// Invalid openTimeout should fall back to 30s.
	cb2 := newCircuitBreaker(CircuitBreakerConfig{OpenTimeout: "not-a-duration"})
	if cb2.openTimeout != 30*time.Second {
		t.Errorf("expected 30s for invalid openTimeout, got %v", cb2.openTimeout)
	}

	// Negative values should use defaults.
	cb3 := newCircuitBreaker(CircuitBreakerConfig{FailThreshold: -1, SuccessThreshold: -1})
	if cb3.failThreshold != 5 {
		t.Errorf("expected 5 for negative failThreshold, got %d", cb3.failThreshold)
	}
	if cb3.successThreshold != 2 {
		t.Errorf("expected 2 for negative successThreshold, got %d", cb3.successThreshold)
	}
}

// --- circuitRegistry ---

func TestCircuitRegistry_LazyInit(t *testing.T) {
	cr := newCircuitRegistry(CircuitBreakerConfig{
		FailThreshold:    3,
		SuccessThreshold: 1,
		OpenTimeout:      "5s",
	})

	// First call should create a new breaker.
	cb1 := cr.get("openai")
	if cb1 == nil {
		t.Fatal("expected non-nil breaker from get()")
	}
	if cb1.State() != CircuitClosed {
		t.Errorf("expected Closed for new breaker, got %s", cb1.State())
	}
	if cb1.failThreshold != 3 {
		t.Errorf("expected failThreshold=3, got %d", cb1.failThreshold)
	}

	// Second call for the same provider should return the same instance.
	cb2 := cr.get("openai")
	if cb1 != cb2 {
		t.Error("expected same breaker instance for same provider")
	}

	// Different provider should get a different breaker.
	cb3 := cr.get("claude")
	if cb3 == cb1 {
		t.Error("expected different breaker for different provider")
	}
}

func TestCircuitRegistry_Status(t *testing.T) {
	cr := newCircuitRegistry(CircuitBreakerConfig{
		FailThreshold: 2,
		OpenTimeout:   "1m",
	})

	// No breakers yet — empty status.
	st := cr.status()
	if len(st) != 0 {
		t.Errorf("expected empty status, got %d entries", len(st))
	}

	// Create breakers and record some events.
	cr.get("openai").RecordFailure()
	cr.get("openai").RecordFailure() // trips to Open
	cr.get("claude").RecordSuccess()

	st = cr.status()
	if len(st) != 2 {
		t.Fatalf("expected 2 breakers in status, got %d", len(st))
	}

	openaiInfo, ok := st["openai"].(map[string]any)
	if !ok {
		t.Fatal("expected openai entry to be map[string]any")
	}
	if openaiInfo["state"] != "open" {
		t.Errorf("expected openai state 'open', got %v", openaiInfo["state"])
	}

	claudeInfo, ok := st["claude"].(map[string]any)
	if !ok {
		t.Fatal("expected claude entry to be map[string]any")
	}
	if claudeInfo["state"] != "closed" {
		t.Errorf("expected claude state 'closed', got %v", claudeInfo["state"])
	}
}

func TestCircuitRegistry_Reset(t *testing.T) {
	cr := newCircuitRegistry(CircuitBreakerConfig{
		FailThreshold: 2,
		OpenTimeout:   "1m",
	})

	// Reset non-existent provider returns false.
	if cr.reset("nonexistent") {
		t.Error("expected reset to return false for unknown provider")
	}

	// Trip a breaker and reset it.
	cr.get("openai").RecordFailure()
	cr.get("openai").RecordFailure()
	if cr.get("openai").State() != CircuitOpen {
		t.Fatal("expected openai to be Open")
	}

	if !cr.reset("openai") {
		t.Error("expected reset to return true for known provider")
	}
	if cr.get("openai").State() != CircuitClosed {
		t.Errorf("expected openai to be Closed after reset, got %s", cr.get("openai").State())
	}
}

func TestCircuitRegistry_Concurrent(t *testing.T) {
	cr := newCircuitRegistry(CircuitBreakerConfig{
		FailThreshold:    10,
		SuccessThreshold: 5,
		OpenTimeout:      "1s",
	})

	var wg sync.WaitGroup
	providers := []string{"openai", "claude", "gemini", "local"}

	// 100 goroutines hammering get/RecordSuccess/RecordFailure concurrently.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			p := providers[n%len(providers)]
			cb := cr.get(p)
			cb.Allow()
			if n%3 == 0 {
				cb.RecordFailure()
			} else {
				cb.RecordSuccess()
			}
			cb.State()
			_ = cb.statusInfo()
		}(i)
	}

	wg.Wait()

	// Just verify no panics and status is readable.
	st := cr.status()
	if len(st) != len(providers) {
		t.Errorf("expected %d providers in status, got %d", len(providers), len(st))
	}
}

// --- Provider Helpers ---

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		// Transient errors.
		{"connection refused", true},
		{"Connection Refused on port 443", true},
		{"request timed out", true},
		{"context deadline exceeded", true},
		{"unexpected EOF while reading response", true},
		{"broken pipe", true},
		{"connection reset by peer", true},
		{"HTTP 502 Bad Gateway", true},
		{"http 503 service unavailable", true},
		{"status 500: internal server error", true},
		{"temporarily unavailable, try again later", true},
		{"service unavailable", true},
		{"too many requests", true},
		{"rate limit exceeded", true},
		{"timeout waiting for response", true},

		// Non-transient errors (should NOT trigger failover).
		{"invalid API key", false},
		{"model not found", false},
		{"permission denied", false},
		{"bad request: prompt too long", false},
		{"authentication failed", false},
		{"", false},
		{"unknown error occurred", false},
		{"content policy violation", false},
	}

	for _, tc := range tests {
		got := isTransientError(tc.errMsg)
		if got != tc.want {
			t.Errorf("isTransientError(%q) = %v, want %v", tc.errMsg, got, tc.want)
		}
	}
}

func TestBuildProviderCandidates(t *testing.T) {
	t.Run("primary only", func(t *testing.T) {
		cfg := &Config{
			DefaultProvider: "claude",
			Agents:           map[string]AgentConfig{},
		}
		task := Task{Provider: ""}
		candidates := buildProviderCandidates(cfg, task, "")
		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate, got %d: %v", len(candidates), candidates)
		}
		if candidates[0] != "claude" {
			t.Errorf("expected primary 'claude', got %q", candidates[0])
		}
	})

	t.Run("task provider overrides default", func(t *testing.T) {
		cfg := &Config{
			DefaultProvider: "claude",
			Agents:           map[string]AgentConfig{},
		}
		task := Task{Provider: "openai"}
		candidates := buildProviderCandidates(cfg, task, "")
		if candidates[0] != "openai" {
			t.Errorf("expected primary 'openai', got %q", candidates[0])
		}
	})

	t.Run("role provider overrides config default", func(t *testing.T) {
		cfg := &Config{
			DefaultProvider: "claude",
			Agents: map[string]AgentConfig{
				"dev": {Provider: "gemini"},
			},
		}
		task := Task{}
		candidates := buildProviderCandidates(cfg, task, "dev")
		if candidates[0] != "gemini" {
			t.Errorf("expected primary 'gemini' from role, got %q", candidates[0])
		}
	})

	t.Run("role fallbacks appended", func(t *testing.T) {
		cfg := &Config{
			DefaultProvider: "claude",
			Agents: map[string]AgentConfig{
				"dev": {
					Provider:          "openai",
					FallbackProviders: []string{"gemini", "local"},
				},
			},
		}
		task := Task{}
		candidates := buildProviderCandidates(cfg, task, "dev")
		expected := []string{"openai", "gemini", "local"}
		if len(candidates) != len(expected) {
			t.Fatalf("expected %v, got %v", expected, candidates)
		}
		for i, want := range expected {
			if candidates[i] != want {
				t.Errorf("candidates[%d] = %q, want %q", i, candidates[i], want)
			}
		}
	})

	t.Run("config fallbacks appended", func(t *testing.T) {
		cfg := &Config{
			DefaultProvider:   "claude",
			FallbackProviders: []string{"openai", "gemini"},
			Agents:             map[string]AgentConfig{},
		}
		task := Task{}
		candidates := buildProviderCandidates(cfg, task, "")
		expected := []string{"claude", "openai", "gemini"}
		if len(candidates) != len(expected) {
			t.Fatalf("expected %v, got %v", expected, candidates)
		}
		for i, want := range expected {
			if candidates[i] != want {
				t.Errorf("candidates[%d] = %q, want %q", i, candidates[i], want)
			}
		}
	})

	t.Run("dedup across role and config fallbacks", func(t *testing.T) {
		cfg := &Config{
			DefaultProvider:   "claude",
			FallbackProviders: []string{"openai", "gemini", "claude"},
			Agents: map[string]AgentConfig{
				"dev": {
					Provider:          "openai",
					FallbackProviders: []string{"gemini", "local"},
				},
			},
		}
		task := Task{}
		candidates := buildProviderCandidates(cfg, task, "dev")
		// Primary: openai (from role)
		// Role fallbacks: gemini, local (openai already seen)
		// Config fallbacks: claude (openai, gemini already seen)
		expected := []string{"openai", "gemini", "local", "claude"}
		if len(candidates) != len(expected) {
			t.Fatalf("expected %v, got %v", expected, candidates)
		}
		for i, want := range expected {
			if candidates[i] != want {
				t.Errorf("candidates[%d] = %q, want %q", i, candidates[i], want)
			}
		}
	})

	t.Run("empty fallbacks", func(t *testing.T) {
		cfg := &Config{
			DefaultProvider:   "claude",
			FallbackProviders: []string{},
			Agents: map[string]AgentConfig{
				"dev": {
					FallbackProviders: []string{},
				},
			},
		}
		task := Task{}
		candidates := buildProviderCandidates(cfg, task, "dev")
		if len(candidates) != 1 || candidates[0] != "claude" {
			t.Errorf("expected [claude], got %v", candidates)
		}
	})
}

// --- CircuitState String ---

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		state CircuitState
		want  string
	}{
		{CircuitClosed, "closed"},
		{CircuitOpen, "open"},
		{CircuitHalfOpen, "half-open"},
		{CircuitState(99), "unknown"},
	}
	for _, tc := range tests {
		got := tc.state.String()
		if got != tc.want {
			t.Errorf("CircuitState(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// --- State() implicit transition ---

func TestCircuitBreaker_StateImplicitTransition(t *testing.T) {
	cb := newCircuitBreaker(CircuitBreakerConfig{
		FailThreshold: 2,
		OpenTimeout:   "50ms",
	})

	// Trip to Open.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected Open, got %s", cb.State())
	}

	// Wait for timeout.
	time.Sleep(70 * time.Millisecond)

	// State() should implicitly transition to HalfOpen (even without calling Allow).
	if cb.State() != CircuitHalfOpen {
		t.Errorf("expected HalfOpen via implicit transition in State(), got %s", cb.State())
	}
}
