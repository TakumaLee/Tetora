package main

import (
	"sync"
	"time"
)

// --- Circuit Breaker ---

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // Normal operation
	CircuitOpen                         // Failing, reject requests
	CircuitHalfOpen                     // Testing recovery
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern for a single provider.
type CircuitBreaker struct {
	mu              sync.Mutex
	state           CircuitState
	failures        int
	successes       int // consecutive successes in half-open
	lastFailure     time.Time
	lastStateChange time.Time

	// Config
	failThreshold    int
	successThreshold int
	openTimeout      time.Duration
}

func newCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	ft := cfg.FailThreshold
	if ft <= 0 {
		ft = 5
	}
	st := cfg.SuccessThreshold
	if st <= 0 {
		st = 2
	}
	ot, err := time.ParseDuration(cfg.OpenTimeout)
	if err != nil || ot <= 0 {
		ot = 30 * time.Second
	}
	return &CircuitBreaker{
		state:            CircuitClosed,
		lastStateChange:  time.Now(),
		failThreshold:    ft,
		successThreshold: st,
		openTimeout:      ot,
	}
}

// Allow checks whether a request should be allowed through.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		// Check if timeout has elapsed → transition to half-open.
		if time.Since(cb.lastStateChange) >= cb.openTimeout {
			cb.state = CircuitHalfOpen
			cb.successes = 0
			cb.lastStateChange = time.Now()
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	}
	return false
}

// RecordSuccess records a successful provider call.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitHalfOpen:
		cb.successes++
		if cb.successes >= cb.successThreshold {
			cb.state = CircuitClosed
			cb.failures = 0
			cb.successes = 0
			cb.lastStateChange = time.Now()
		}
	case CircuitClosed:
		// Reset failure count on success.
		cb.failures = 0
	}
}

// RecordFailure records a failed provider call.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = time.Now()

	switch cb.state {
	case CircuitClosed:
		cb.failures++
		if cb.failures >= cb.failThreshold {
			cb.state = CircuitOpen
			cb.lastStateChange = time.Now()
		}
	case CircuitHalfOpen:
		// Any failure in half-open → back to open.
		cb.state = CircuitOpen
		cb.lastStateChange = time.Now()
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check for implicit transition open → half-open.
	if cb.state == CircuitOpen && time.Since(cb.lastStateChange) >= cb.openTimeout {
		cb.state = CircuitHalfOpen
		cb.successes = 0
		cb.lastStateChange = time.Now()
	}
	return cb.state
}

// Reset manually resets the circuit breaker to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = CircuitClosed
	cb.failures = 0
	cb.successes = 0
	cb.lastStateChange = time.Now()
}

// statusInfo returns a snapshot of the circuit breaker state for reporting.
func (cb *CircuitBreaker) statusInfo() map[string]any {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	info := map[string]any{
		"state":    cb.state.String(),
		"failures": cb.failures,
	}
	if !cb.lastFailure.IsZero() {
		info["lastFailure"] = cb.lastFailure.Format(time.RFC3339)
	}
	if cb.state == CircuitHalfOpen {
		info["successes"] = cb.successes
	}
	return info
}

// --- Circuit Registry ---

// circuitRegistry manages per-provider circuit breakers.
type circuitRegistry struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
	cfg      CircuitBreakerConfig
}

func newCircuitRegistry(cfg CircuitBreakerConfig) *circuitRegistry {
	return &circuitRegistry{
		breakers: make(map[string]*CircuitBreaker),
		cfg:      cfg,
	}
}

// get returns the circuit breaker for a provider, creating one lazily if needed.
func (cr *circuitRegistry) get(provider string) *CircuitBreaker {
	cr.mu.RLock()
	cb, ok := cr.breakers[provider]
	cr.mu.RUnlock()
	if ok {
		return cb
	}

	cr.mu.Lock()
	defer cr.mu.Unlock()

	// Double-check after acquiring write lock.
	if cb, ok := cr.breakers[provider]; ok {
		return cb
	}

	cb = newCircuitBreaker(cr.cfg)
	cr.breakers[provider] = cb
	return cb
}

// status returns a snapshot of all circuit breaker states.
func (cr *circuitRegistry) status() map[string]any {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	result := make(map[string]any, len(cr.breakers))
	for name, cb := range cr.breakers {
		result[name] = cb.statusInfo()
	}
	return result
}

// reset resets the circuit breaker for a specific provider.
func (cr *circuitRegistry) reset(provider string) bool {
	cr.mu.RLock()
	cb, ok := cr.breakers[provider]
	cr.mu.RUnlock()
	if !ok {
		return false
	}
	cb.Reset()
	return true
}
