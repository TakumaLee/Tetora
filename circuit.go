package main

import "tetora/internal/circuit"

// CircuitState is an alias for circuit.State for backward compatibility.
type CircuitState = circuit.State

const (
	CircuitClosed   = circuit.Closed
	CircuitOpen     = circuit.Open
	CircuitHalfOpen = circuit.HalfOpen
)

// CircuitBreaker is an alias for circuit.Breaker for backward compatibility.
type CircuitBreaker = circuit.Breaker

// newCircuitBreaker delegates to circuit.New.
func newCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	return circuit.New(circuit.Config{
		Enabled:          cfg.Enabled,
		FailThreshold:    cfg.FailThreshold,
		SuccessThreshold: cfg.SuccessThreshold,
		OpenTimeout:      cfg.OpenTimeout,
	})
}

// circuitRegistry is an alias for circuit.Registry for backward compatibility.
type circuitRegistry = circuit.Registry

// newCircuitRegistry delegates to circuit.NewRegistry.
func newCircuitRegistry(cfg CircuitBreakerConfig) *circuitRegistry {
	return circuit.NewRegistry(circuit.Config{
		Enabled:          cfg.Enabled,
		FailThreshold:    cfg.FailThreshold,
		SuccessThreshold: cfg.SuccessThreshold,
		OpenTimeout:      cfg.OpenTimeout,
	})
}
