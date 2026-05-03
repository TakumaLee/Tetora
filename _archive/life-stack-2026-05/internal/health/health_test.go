package health

import (
	"fmt"
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
		got := DegradeStatus(tc.current, tc.proposed)
		if got != tc.want {
			t.Errorf("DegradeStatus(%q, %q) = %q, want %q", tc.current, tc.proposed, got, tc.want)
		}
	}
}

func TestDeepCheck_Basic(t *testing.T) {
	input := CheckInput{
		Version:   "test-v1",
		StartTime: time.Now().Add(-5 * time.Minute),
		BaseDir:   t.TempDir(),
		Providers: map[string]ProviderInfo{},
	}

	result := DeepCheck(input)

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
	if result["version"] != "test-v1" {
		t.Errorf("expected version %q, got %v", "test-v1", result["version"])
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

func TestDeepCheck_DBError(t *testing.T) {
	input := CheckInput{
		Version:   "test",
		StartTime: time.Now(),
		DBCheck: func() (int, error) {
			return 0, fmt.Errorf("connection refused")
		},
		DBPath: "/tmp/test.db",
	}

	result := DeepCheck(input)
	if result["status"] != "unhealthy" {
		t.Errorf("expected unhealthy when DB fails, got %v", result["status"])
	}
}

func TestDeepCheck_DegradedProvider(t *testing.T) {
	input := CheckInput{
		Version:   "test",
		StartTime: time.Now(),
		Providers: map[string]ProviderInfo{
			"openai": {Type: "openai-compatible", Status: "open", Circuit: "open"},
		},
	}

	result := DeepCheck(input)
	if result["status"] != "degraded" {
		t.Errorf("expected degraded when provider circuit is open, got %v", result["status"])
	}
}

func TestDeepCheck_WithQueue(t *testing.T) {
	input := CheckInput{
		Version:   "test",
		StartTime: time.Now(),
		Queue:     &QueueInfo{Pending: 3, Max: 100},
	}

	result := DeepCheck(input)
	if result["status"] != "degraded" {
		t.Errorf("expected degraded when queue has pending items, got %v", result["status"])
	}
	q, ok := result["queue"].(map[string]any)
	if !ok {
		t.Fatal("expected queue section")
	}
	if q["status"] != "draining" {
		t.Errorf("expected queue status 'draining', got %v", q["status"])
	}
}

func TestDiskInfo(t *testing.T) {
	dir := t.TempDir()
	info := DiskInfo(dir)
	if info["status"] != "ok" {
		t.Errorf("expected status ok, got %v", info["status"])
	}
	if _, ok := info["freeGB"]; !ok {
		t.Log("freeGB not available (may be expected on some platforms)")
	}
}
