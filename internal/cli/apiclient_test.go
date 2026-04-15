package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestAPIClient_ClientIDHeader verifies that APIClient sets X-Client-ID header
// when ClientID is non-empty, and omits it when empty.
func TestAPIClient_ClientIDHeader(t *testing.T) {
	var capturedClientID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClientID = r.Header.Get("X-Client-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Strip http:// from URL to match NewAPIClient expectations.
	listenAddr := strings.TrimPrefix(srv.URL, "http://")

	t.Run("sets header when ClientID is non-empty", func(t *testing.T) {
		api := NewAPIClient(listenAddr, "")
		api.ClientID = "cli_myapp"

		resp, err := api.Do("GET", "/test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if capturedClientID != "cli_myapp" {
			t.Errorf("X-Client-ID = %q, want %q", capturedClientID, "cli_myapp")
		}
	})

	t.Run("omits header when ClientID is empty", func(t *testing.T) {
		capturedClientID = ""
		api := NewAPIClient(listenAddr, "")
		// ClientID intentionally not set.

		resp, err := api.Do("GET", "/test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if capturedClientID != "" {
			t.Errorf("X-Client-ID should be absent, got %q", capturedClientID)
		}
	})

	t.Run("sends header with POST body", func(t *testing.T) {
		capturedClientID = ""
		api := NewAPIClient(listenAddr, "")
		api.ClientID = "cli_test"

		resp, err := api.Do("POST", "/test", strings.NewReader(`{"prompt":"hello"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if capturedClientID != "cli_test" {
			t.Errorf("X-Client-ID = %q, want %q", capturedClientID, "cli_test")
		}
	})
}

// TestAPIClient_SubAgent_SetsHeader verifies that Do() adds X-Tetora-Source: agent_dispatch
// when SubAgent is true, and omits it when false.
func TestAPIClient_SubAgent_SetsHeader(t *testing.T) {
	var capturedSource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSource = r.Header.Get("X-Tetora-Source")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	listenAddr := strings.TrimPrefix(srv.URL, "http://")

	t.Run("sets X-Tetora-Source when SubAgent=true", func(t *testing.T) {
		api := NewAPIClient(listenAddr, "")
		api.SubAgent = true

		resp, err := api.Do("GET", "/test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if capturedSource != "agent_dispatch" {
			t.Errorf("X-Tetora-Source = %q, want %q", capturedSource, "agent_dispatch")
		}
	})

	t.Run("omits X-Tetora-Source when SubAgent=false", func(t *testing.T) {
		capturedSource = ""
		api := NewAPIClient(listenAddr, "")
		// SubAgent intentionally not set (defaults to false).

		resp, err := api.Do("GET", "/test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if capturedSource != "" {
			t.Errorf("X-Tetora-Source should be absent when SubAgent=false, got %q", capturedSource)
		}
	})
}

// TestDispatch_TETORA_SOURCE_EnvSetsSubAgent verifies that when TETORA_SOURCE=agent_dispatch
// is set in the environment, a dispatch client sends the X-Tetora-Source: agent_dispatch header.
// This mirrors the logic in CmdDispatch: os.Getenv("TETORA_SOURCE") == "agent_dispatch" → api.SubAgent = true.
func TestDispatch_TETORA_SOURCE_EnvSetsSubAgent(t *testing.T) {
	var capturedSource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSource = r.Header.Get("X-Tetora-Source")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	listenAddr := strings.TrimPrefix(srv.URL, "http://")

	t.Setenv("TETORA_SOURCE", "agent_dispatch")

	// Simulate the CmdDispatch env-check logic.
	api := NewAPIClient(listenAddr, "")
	if os.Getenv("TETORA_SOURCE") == "agent_dispatch" {
		api.SubAgent = true
	}

	resp, err := api.Do("POST", "/dispatch", strings.NewReader(`[{"prompt":"test"}]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if capturedSource != "agent_dispatch" {
		t.Errorf("X-Tetora-Source = %q, want \"agent_dispatch\" when TETORA_SOURCE env is set", capturedSource)
	}
}
