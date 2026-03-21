package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tetora/internal/history"
)

// --- Integration tests for multi-tenant isolation ---

// TestIntegration_HistoryHandler_PerClientIsolation verifies that the /history
// endpoint routes to different DBs based on X-Client-ID.
func TestIntegration_HistoryHandler_PerClientIsolation(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		HistoryDB:       filepath.Join(dir, "dbs", "history.db"),
		ClientsDir:      filepath.Join(dir, "clients"),
		DefaultClientID: "cli_default",
	}
	srv := &Server{cfg: cfg}

	// Init default DB and insert a record.
	os.MkdirAll(filepath.Dir(cfg.HistoryDB), 0o755)
	if err := history.InitDB(cfg.HistoryDB); err != nil {
		t.Fatalf("init default DB: %v", err)
	}
	if err := history.InsertRun(cfg.HistoryDB, history.JobRun{
		JobID: "job-default", Name: "default-task", Source: "test",
		StartedAt: "2026-01-01T00:00:00Z", FinishedAt: "2026-01-01T00:01:00Z",
		Status: "done", Model: "test-model",
	}); err != nil {
		t.Fatalf("insert default run: %v", err)
	}

	// Init client-A DB and insert a different record.
	clientADB := cfg.HistoryDBFor("cli_app-a")
	os.MkdirAll(filepath.Dir(clientADB), 0o755)
	if err := history.InitDB(clientADB); err != nil {
		t.Fatalf("init client-A DB: %v", err)
	}
	if err := history.InsertRun(clientADB, history.JobRun{
		JobID: "job-app-a", Name: "app-a-task", Source: "test",
		StartedAt: "2026-01-01T00:00:00Z", FinishedAt: "2026-01-01T00:01:00Z",
		Status: "done", Model: "test-model",
	}); err != nil {
		t.Fatalf("insert client-A run: %v", err)
	}

	// Build handler with client middleware.
	mux := http.NewServeMux()
	mux.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		clientID := getClientID(r)
		dbPath := srv.resolveHistoryDB(cfg, clientID)
		runs, _, err := history.QueryFiltered(dbPath, history.HistoryQuery{Limit: 50})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"runs": runs})
	})
	handler := clientMiddleware("cli_default", mux)

	// Query default client — should see "job-default" only.
	t.Run("default_client_sees_own_data", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/history", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != 200 {
			t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
		}
		var resp struct {
			Runs []history.JobRun `json:"runs"`
		}
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if len(resp.Runs) != 1 || resp.Runs[0].JobID != "job-default" {
			t.Errorf("default client: expected [job-default], got %v", jobIDs(resp.Runs))
		}
	})

	// Query client-A — should see "job-app-a" only.
	t.Run("client_a_sees_own_data", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/history", nil)
		req.Header.Set("X-Client-ID", "cli_app-a")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != 200 {
			t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
		}
		var resp struct {
			Runs []history.JobRun `json:"runs"`
		}
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if len(resp.Runs) != 1 || resp.Runs[0].JobID != "job-app-a" {
			t.Errorf("client-A: expected [job-app-a], got %v", jobIDs(resp.Runs))
		}
	})

	// Query client-B (DB initialized but no data) — should see empty.
	t.Run("client_b_sees_empty", func(t *testing.T) {
		clientBDB := cfg.HistoryDBFor("cli_app-b")
		os.MkdirAll(filepath.Dir(clientBDB), 0o755)
		if err := history.InitDB(clientBDB); err != nil {
			t.Fatalf("init client-B DB: %v", err)
		}

		req := httptest.NewRequest("GET", "/history", nil)
		req.Header.Set("X-Client-ID", "cli_app-b")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != 200 {
			t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
		}
		var resp struct {
			Runs []history.JobRun `json:"runs"`
		}
		json.Unmarshal(rr.Body.Bytes(), &resp)
		if len(resp.Runs) != 0 {
			t.Errorf("client-B: expected empty, got %v", jobIDs(resp.Runs))
		}
	})
}

// TestIntegration_NoClientID_RoutesToDefault verifies that requests without
// X-Client-ID header route to the default client DB.
func TestIntegration_NoClientID_RoutesToDefault(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		HistoryDB:       filepath.Join(dir, "dbs", "history.db"),
		ClientsDir:      filepath.Join(dir, "clients"),
		DefaultClientID: "cli_default",
	}
	srv := &Server{cfg: cfg}

	os.MkdirAll(filepath.Dir(cfg.HistoryDB), 0o755)
	history.InitDB(cfg.HistoryDB)
	history.InsertRun(cfg.HistoryDB, history.JobRun{
		JobID: "job-no-header", Name: "no-header-task", Source: "test",
		StartedAt: "2026-01-01T00:00:00Z", FinishedAt: "2026-01-01T00:01:00Z",
		Status: "done", Model: "test-model",
	})

	// Resolve without header should give default DB.
	ctx := context.WithValue(context.Background(), clientIDKey, "cli_default")
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	clientID := getClientID(req)
	dbPath := srv.resolveHistoryDB(cfg, clientID)

	if dbPath != cfg.HistoryDB {
		t.Errorf("expected default DB %q, got %q", cfg.HistoryDB, dbPath)
	}
}

// TestIntegration_OutputsDirIsolation verifies that output files are
// stored in per-client directories.
func TestIntegration_OutputsDirIsolation(t *testing.T) {
	cfg := &Config{
		BaseDir:         "/home/user/.tetora",
		ClientsDir:      "/home/user/.tetora/clients",
		DefaultClientID: "cli_default",
	}

	tests := []struct {
		clientID string
		expected string
	}{
		{"cli_default", "/home/user/.tetora"},
		{"", "/home/user/.tetora"},
		{"cli_app-a", "/home/user/.tetora/clients/cli_app-a"},
		{"cli_app-b", "/home/user/.tetora/clients/cli_app-b"},
	}

	for _, tt := range tests {
		got := cfg.OutputsDirFor(tt.clientID)
		if got != tt.expected {
			t.Errorf("OutputsDirFor(%q) = %q, want %q", tt.clientID, got, tt.expected)
		}
	}
}

// TestIntegration_DispatchManager_ConcurrentClients verifies that concurrent
// dispatches from different clients maintain isolated state.
func TestIntegration_DispatchManager_ConcurrentClients(t *testing.T) {
	dm := newDispatchManager(4, 12)

	// Simulate concurrent client registrations.
	clients := []string{"cli_app-a", "cli_app-b", "cli_app-c"}
	done := make(chan string, len(clients))

	for _, id := range clients {
		go func(clientID string) {
			state, sem, childSem := dm.getOrCreate(clientID)
			if state == nil || sem == nil || childSem == nil {
				t.Errorf("nil returned for %s", clientID)
			}
			done <- clientID
		}(id)
	}

	for range clients {
		<-done
	}

	// Verify all clients have separate state.
	all := dm.allStates()
	if len(all) != len(clients) {
		t.Errorf("expected %d states, got %d", len(clients), len(all))
	}

	// Verify state isolation — modifying one doesn't affect others.
	stateA, _, _ := dm.getOrCreate("cli_app-a")
	stateB, _, _ := dm.getOrCreate("cli_app-b")
	stateA.active = true
	if stateB.active {
		t.Error("modifying cli_app-a state affected cli_app-b")
	}
}

// TestIntegration_ResolveHistoryDB_CreatesDir verifies that resolveHistoryDB
// creates the client DB directory if it doesn't exist.
func TestIntegration_ResolveHistoryDB_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		HistoryDB:       filepath.Join(dir, "dbs", "history.db"),
		ClientsDir:      filepath.Join(dir, "clients"),
		DefaultClientID: "cli_default",
	}
	srv := &Server{cfg: cfg}

	// Directory should not exist yet.
	clientDBDir := filepath.Join(dir, "clients", "cli_new-app", "dbs")
	if _, err := os.Stat(clientDBDir); !os.IsNotExist(err) {
		t.Fatal("client DB dir should not exist before resolveHistoryDB")
	}

	// Resolve should create the directory.
	dbPath := srv.resolveHistoryDB(cfg, "cli_new-app")
	expected := filepath.Join(clientDBDir, "history.db")
	if dbPath != expected {
		t.Errorf("dbPath = %q, want %q", dbPath, expected)
	}

	// Directory should now exist.
	info, err := os.Stat(clientDBDir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory, got file")
	}
}

// jobIDs extracts job IDs from a slice of JobRun for error messages.
func jobIDs(runs []history.JobRun) []string {
	var ids []string
	for _, r := range runs {
		ids = append(ids, r.JobID)
	}
	return ids
}
