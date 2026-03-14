// Package circuit implements the circuit breaker pattern.
package circuit

import (
	"sync"
	"time"
)

// State represents the state of a circuit breaker.
type State int

const (
	Closed   State = iota // Normal operation
	Open                  // Failing, reject requests
	HalfOpen              // Testing recovery
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config holds circuit breaker configuration.
type Config struct {
	Enabled          bool   `json:"enabled,omitempty"`
	FailThreshold    int    `json:"failThreshold,omitempty"`
	SuccessThreshold int    `json:"successThreshold,omitempty"`
	OpenTimeout      string `json:"openTimeout,omitempty"`
}

// Breaker implements the circuit breaker pattern for a single provider.
type Breaker struct {
	mu              sync.Mutex
	state           State
	failures        int
	successes       int // consecutive successes in half-open
	lastFailure     time.Time
	lastStateChange time.Time

	// Config
	failThreshold    int
	successThreshold int
	openTimeout      time.Duration
}

// New creates a new circuit breaker with the given configuration.
func New(cfg Config) *Breaker {
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
	return &Breaker{
		state:            Closed,
		lastStateChange:  time.Now(),
		failThreshold:    ft,
		successThreshold: st,
		openTimeout:      ot,
	}
}

// Allow checks whether a request should be allowed through.
func (cb *Breaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case Closed:
		return true
	case Open:
		// Check if timeout has elapsed -> transition to half-open.
		if time.Since(cb.lastStateChange) >= cb.openTimeout {
			cb.state = HalfOpen
			cb.successes = 0
			cb.lastStateChange = time.Now()
			return true
		}
		return false
	case HalfOpen:
		return true
	}
	return false
}

// RecordSuccess records a successful call.
func (cb *Breaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case HalfOpen:
		cb.successes++
		if cb.successes >= cb.successThreshold {
			cb.state = Closed
			cb.failures = 0
			cb.successes = 0
			cb.lastStateChange = time.Now()
		}
	case Closed:
		// Reset failure count on success.
		cb.failures = 0
	}
}

// RecordFailure records a failed call.
func (cb *Breaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = time.Now()

	switch cb.state {
	case Closed:
		cb.failures++
		if cb.failures >= cb.failThreshold {
			cb.state = Open
			cb.lastStateChange = time.Now()
		}
	case HalfOpen:
		// Any failure in half-open -> back to open.
		cb.state = Open
		cb.lastStateChange = time.Now()
	}
}

// State returns the current circuit state.
func (cb *Breaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check for implicit transition open -> half-open.
	if cb.state == Open && time.Since(cb.lastStateChange) >= cb.openTimeout {
		cb.state = HalfOpen
		cb.successes = 0
		cb.lastStateChange = time.Now()
	}
	return cb.state
}

// Reset manually resets the circuit breaker to closed state.
func (cb *Breaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = Closed
	cb.failures = 0
	cb.successes = 0
	cb.lastStateChange = time.Now()
}

// StatusInfo returns a snapshot of the circuit breaker state for reporting.
func (cb *Breaker) StatusInfo() map[string]any {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	info := map[string]any{
		"state":    cb.state.String(),
		"failures": cb.failures,
	}
	if !cb.lastFailure.IsZero() {
		info["lastFailure"] = cb.lastFailure.Format(time.RFC3339)
	}
	if cb.state == HalfOpen {
		info["successes"] = cb.successes
	}
	return info
}

// Registry manages per-key circuit breakers.
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker
	cfg      Config
}

// NewRegistry creates a new circuit breaker registry.
func NewRegistry(cfg Config) *Registry {
	return &Registry{
		breakers: make(map[string]*Breaker),
		cfg:      cfg,
	}
}

// Get returns the circuit breaker for a key, creating one lazily if needed.
func (cr *Registry) Get(key string) *Breaker {
	cr.mu.RLock()
	cb, ok := cr.breakers[key]
	cr.mu.RUnlock()
	if ok {
		return cb
	}

	cr.mu.Lock()
	defer cr.mu.Unlock()

	// Double-check after acquiring write lock.
	if cb, ok := cr.breakers[key]; ok {
		return cb
	}

	cb = New(cr.cfg)
	cr.breakers[key] = cb
	return cb
}

// Status returns a snapshot of all circuit breaker states.
func (cr *Registry) Status() map[string]any {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	result := make(map[string]any, len(cr.breakers))
	for name, cb := range cr.breakers {
		result[name] = cb.StatusInfo()
	}
	return result
}

// ResetKey resets the circuit breaker for a specific key.
func (cr *Registry) ResetKey(key string) bool {
	cr.mu.RLock()
	cb, ok := cr.breakers[key]
	cr.mu.RUnlock()
	if !ok {
		return false
	}
	cb.Reset()
	return true
}
