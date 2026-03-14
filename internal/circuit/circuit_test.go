package circuit

import (
	"sync"
	"testing"
	"time"
)

// --- CircuitBreaker State Machine ---

func TestCircuitBreaker_InitialState(t *testing.T) {
	cb := New(Config{})
	if cb.State() != Closed {
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
	cb := New(Config{
		FailThreshold:    3,
		SuccessThreshold: 2,
		OpenTimeout:      "1m",
	})

	// Record failures below threshold — should stay Closed.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != Closed {
		t.Fatalf("expected Closed after 2 failures (threshold 3), got %s", cb.State())
	}

	// Third failure should trip to Open.
	cb.RecordFailure()
	if cb.State() != Open {
		t.Fatalf("expected Open after 3 failures, got %s", cb.State())
	}

	// Allow should return false when Open (timeout is 1m, well in the future).
	if cb.Allow() {
		t.Error("expected Allow()=false when circuit is Open")
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := New(Config{
		FailThreshold:    2,
		SuccessThreshold: 1,
		OpenTimeout:      "50ms",
	})

	// Trip the circuit to Open.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != Open {
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
	if cb.State() != HalfOpen {
		t.Errorf("expected HalfOpen after timeout, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToClose(t *testing.T) {
	cb := New(Config{
		FailThreshold:    2,
		SuccessThreshold: 3,
		OpenTimeout:      "50ms",
	})

	// Trip to Open, wait for HalfOpen.
	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(70 * time.Millisecond)
	cb.Allow() // triggers Open -> HalfOpen transition

	if cb.State() != HalfOpen {
		t.Fatalf("expected HalfOpen, got %s", cb.State())
	}

	// Record successes below threshold — should stay HalfOpen.
	cb.RecordSuccess()
	cb.RecordSuccess()
	if cb.State() != HalfOpen {
		t.Fatalf("expected HalfOpen after 2 successes (threshold 3), got %s", cb.State())
	}

	// Third success should close the circuit.
	cb.RecordSuccess()
	if cb.State() != Closed {
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
	cb := New(Config{
		FailThreshold:    2,
		SuccessThreshold: 3,
		OpenTimeout:      "50ms",
	})

	// Trip to Open, wait for HalfOpen.
	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(70 * time.Millisecond)
	cb.Allow() // triggers HalfOpen

	if cb.State() != HalfOpen {
		t.Fatalf("expected HalfOpen, got %s", cb.State())
	}

	// One success, then a failure — should go back to Open.
	cb.RecordSuccess()
	cb.RecordFailure()
	if cb.State() != Open {
		t.Fatalf("expected Open after failure in HalfOpen, got %s", cb.State())
	}
}

func TestCircuitBreaker_Allow(t *testing.T) {
	cb := New(Config{
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
	cb.state = HalfOpen
	cb.mu.Unlock()
	if !cb.Allow() {
		t.Error("expected Allow()=true when HalfOpen")
	}
}

func TestCircuitBreaker_SuccessResetsFailures(t *testing.T) {
	cb := New(Config{
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
	if cb.State() != Closed {
		t.Error("expected Closed after 4 failures (threshold 5)")
	}
	cb.RecordFailure()
	if cb.State() != Open {
		t.Error("expected Open after 5 failures")
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := New(Config{
		FailThreshold:    2,
		SuccessThreshold: 1,
		OpenTimeout:      "10m",
	})

	// Trip to Open.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != Open {
		t.Fatalf("expected Open, got %s", cb.State())
	}

	// Manual reset should go back to Closed.
	cb.Reset()
	if cb.State() != Closed {
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
	cb := New(Config{
		FailThreshold:    3,
		SuccessThreshold: 2,
		OpenTimeout:      "50ms",
	})

	// Closed state: should have state and failures, no lastFailure.
	info := cb.StatusInfo()
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
	info = cb.StatusInfo()
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
	info = cb.StatusInfo()
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
	cb := New(Config{})
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
	cb2 := New(Config{OpenTimeout: "not-a-duration"})
	if cb2.openTimeout != 30*time.Second {
		t.Errorf("expected 30s for invalid openTimeout, got %v", cb2.openTimeout)
	}

	// Negative values should use defaults.
	cb3 := New(Config{FailThreshold: -1, SuccessThreshold: -1})
	if cb3.failThreshold != 5 {
		t.Errorf("expected 5 for negative failThreshold, got %d", cb3.failThreshold)
	}
	if cb3.successThreshold != 2 {
		t.Errorf("expected 2 for negative successThreshold, got %d", cb3.successThreshold)
	}
}

// --- Registry ---

func TestCircuitRegistry_LazyInit(t *testing.T) {
	cr := NewRegistry(Config{
		FailThreshold:    3,
		SuccessThreshold: 1,
		OpenTimeout:      "5s",
	})

	// First call should create a new breaker.
	cb1 := cr.Get("openai")
	if cb1 == nil {
		t.Fatal("expected non-nil breaker from Get()")
	}
	if cb1.State() != Closed {
		t.Errorf("expected Closed for new breaker, got %s", cb1.State())
	}
	if cb1.failThreshold != 3 {
		t.Errorf("expected failThreshold=3, got %d", cb1.failThreshold)
	}

	// Second call for the same provider should return the same instance.
	cb2 := cr.Get("openai")
	if cb1 != cb2 {
		t.Error("expected same breaker instance for same provider")
	}

	// Different provider should get a different breaker.
	cb3 := cr.Get("claude")
	if cb3 == cb1 {
		t.Error("expected different breaker for different provider")
	}
}

func TestCircuitRegistry_Status(t *testing.T) {
	cr := NewRegistry(Config{
		FailThreshold: 2,
		OpenTimeout:   "1m",
	})

	// No breakers yet — empty status.
	st := cr.Status()
	if len(st) != 0 {
		t.Errorf("expected empty status, got %d entries", len(st))
	}

	// Create breakers and record some events.
	cr.Get("openai").RecordFailure()
	cr.Get("openai").RecordFailure() // trips to Open
	cr.Get("claude").RecordSuccess()

	st = cr.Status()
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
	cr := NewRegistry(Config{
		FailThreshold: 2,
		OpenTimeout:   "1m",
	})

	// Reset non-existent provider returns false.
	if cr.ResetKey("nonexistent") {
		t.Error("expected ResetKey to return false for unknown provider")
	}

	// Trip a breaker and reset it.
	cr.Get("openai").RecordFailure()
	cr.Get("openai").RecordFailure()
	if cr.Get("openai").State() != Open {
		t.Fatal("expected openai to be Open")
	}

	if !cr.ResetKey("openai") {
		t.Error("expected ResetKey to return true for known provider")
	}
	if cr.Get("openai").State() != Closed {
		t.Errorf("expected openai to be Closed after ResetKey, got %s", cr.Get("openai").State())
	}
}

func TestCircuitRegistry_Concurrent(t *testing.T) {
	cr := NewRegistry(Config{
		FailThreshold:    10,
		SuccessThreshold: 5,
		OpenTimeout:      "1s",
	})

	var wg sync.WaitGroup
	providers := []string{"openai", "claude", "gemini", "local"}

	// 100 goroutines hammering Get/RecordSuccess/RecordFailure concurrently.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			p := providers[n%len(providers)]
			cb := cr.Get(p)
			cb.Allow()
			if n%3 == 0 {
				cb.RecordFailure()
			} else {
				cb.RecordSuccess()
			}
			cb.State()
			_ = cb.StatusInfo()
		}(i)
	}

	wg.Wait()

	// Just verify no panics and status is readable.
	st := cr.Status()
	if len(st) != len(providers) {
		t.Errorf("expected %d providers in status, got %d", len(providers), len(st))
	}
}

// --- State String ---

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{Closed, "closed"},
		{Open, "open"},
		{HalfOpen, "half-open"},
		{State(99), "unknown"},
	}
	for _, tc := range tests {
		got := tc.state.String()
		if got != tc.want {
			t.Errorf("State(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// --- State() implicit transition ---

func TestCircuitBreaker_StateImplicitTransition(t *testing.T) {
	cb := New(Config{
		FailThreshold: 2,
		OpenTimeout:   "50ms",
	})

	// Trip to Open.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != Open {
		t.Fatalf("expected Open, got %s", cb.State())
	}

	// Wait for timeout.
	time.Sleep(70 * time.Millisecond)

	// State() should implicitly transition to HalfOpen (even without calling Allow).
	if cb.State() != HalfOpen {
		t.Errorf("expected HalfOpen via implicit transition in State(), got %s", cb.State())
	}
}
