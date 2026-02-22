package main

import (
	"testing"
	"time"
)

func TestDegradeStatus(t *testing.T) {
	tests := []struct {
		current, proposed, want string
	}{
		{"healthy", "healthy", "healthy"},
		{"healthy", "degraded", "degraded"},
		{"healthy", "unhealthy", "unhealthy"},
		{"degraded", "healthy", "degraded"},
		{"degraded", "degraded", "degraded"},
		{"degraded", "unhealthy", "unhealthy"},
		{"unhealthy", "healthy", "unhealthy"},
		{"unhealthy", "degraded", "unhealthy"},
		{"unhealthy", "unhealthy", "unhealthy"},
	}
	for _, tc := range tests {
		got := degradeStatus(tc.current, tc.proposed)
		if got != tc.want {
			t.Errorf("degradeStatus(%q, %q) = %q, want %q", tc.current, tc.proposed, got, tc.want)
		}
	}
}

func TestDeepHealthCheck_Basic(t *testing.T) {
	cfg := &Config{
		baseDir:   t.TempDir(),
		circuits:  newCircuitRegistry(CircuitBreakerConfig{}),
		Providers: map[string]ProviderConfig{},
	}
	cfg.registry = initProviders(cfg)

	state := newDispatchState()
	startTime := time.Now().Add(-5 * time.Minute)

	result := deepHealthCheck(cfg, state, nil, startTime)

	// Should have status.
	status, ok := result["status"].(string)
	if !ok || status == "" {
		t.Errorf("expected non-empty status, got %v", result["status"])
	}
	if status != "healthy" {
		t.Errorf("expected healthy status with no issues, got %q", status)
	}

	// Should have uptime.
	uptime, ok := result["uptime"].(map[string]any)
	if !ok {
		t.Fatal("expected uptime section")
	}
	secs, _ := uptime["seconds"].(int)
	if secs < 300 {
		t.Errorf("expected uptime >= 300s, got %d", secs)
	}

	// Should have version.
	if result["version"] != tetoraVersion {
		t.Errorf("expected version %q, got %v", tetoraVersion, result["version"])
	}

	// Should have dispatch.
	if result["dispatch"] == nil {
		t.Error("expected dispatch section")
	}

	// DB disabled.
	db, ok := result["db"].(map[string]any)
	if !ok {
		t.Fatal("expected db section")
	}
	if db["status"] != "disabled" {
		t.Errorf("expected db status 'disabled', got %v", db["status"])
	}
}

func TestDeepHealthCheck_DegradedCircuit(t *testing.T) {
	cfg := &Config{
		baseDir:  t.TempDir(),
		circuits: newCircuitRegistry(CircuitBreakerConfig{FailThreshold: 2, OpenTimeout: "10m"}),
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai-compatible"},
		},
	}
	cfg.registry = initProviders(cfg)

	// Trip the openai circuit.
	cfg.circuits.get("openai").RecordFailure()
	cfg.circuits.get("openai").RecordFailure()

	state := newDispatchState()
	result := deepHealthCheck(cfg, state, nil, time.Now())

	if result["status"] != "degraded" {
		t.Errorf("expected degraded status when circuit is open, got %v", result["status"])
	}

	providers, ok := result["providers"].(map[string]any)
	if !ok {
		t.Fatal("expected providers section")
	}
	openai, ok := providers["openai"].(map[string]any)
	if !ok {
		t.Fatal("expected openai provider entry")
	}
	if openai["circuit"] != "open" {
		t.Errorf("expected circuit 'open', got %v", openai["circuit"])
	}
}

func TestDiskInfo(t *testing.T) {
	dir := t.TempDir()
	info := diskInfo(dir)
	if info["status"] != "ok" {
		t.Errorf("expected status ok, got %v", info["status"])
	}
	// freeGB should be present on unix.
	if _, ok := info["freeGB"]; !ok {
		t.Log("freeGB not available (may be expected on some platforms)")
	}
}
