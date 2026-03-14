package main

import (
	"testing"
)

// Circuit breaker tests are in internal/circuit/circuit_test.go.
// This file tests provider helpers that remain in package main.

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
			Agents:          map[string]AgentConfig{},
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
			Agents:          map[string]AgentConfig{},
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
			Agents:            map[string]AgentConfig{},
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
