package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Tests for multi-tenant client ID validation ---

func TestIsValidClientID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"cli_default", true},
		{"cli_abc", true},
		{"cli_my-app-123", true},
		{"cli_a", true},
		{"cli_abcdefghijklmnopqrstuvwxyz12", true}, // 28 chars after prefix
		{"", false},                                 // empty
		{"default", false},                          // no cli_ prefix
		{"cli_", false},                             // nothing after prefix
		{"cli_ABC", false},                          // uppercase not allowed
		{"cli_hello world", false},                  // space not allowed
		{"cli_../etc", false},                       // path traversal
		{"cli_hello_world", false},                  // underscore not allowed after prefix
		{"CLI_default", false},                      // uppercase prefix
		{"cli_abcdefghijklmnopqrstuvwxyz123", false}, // 29 chars = too long
	}

	for _, tt := range tests {
		got := isValidClientID(tt.id)
		if got != tt.valid {
			t.Errorf("isValidClientID(%q) = %v, want %v", tt.id, got, tt.valid)
		}
	}
}

// --- Tests for clientMiddleware ---

func TestClientMiddleware_DefaultClientID(t *testing.T) {
	var gotClientID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClientID = getClientID(r)
		w.WriteHeader(http.StatusOK)
	})

	handler := clientMiddleware("cli_default", inner)
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if gotClientID != "cli_default" {
		t.Errorf("expected cli_default, got %q", gotClientID)
	}
}

func TestClientMiddleware_CustomClientID(t *testing.T) {
	var gotClientID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClientID = getClientID(r)
		w.WriteHeader(http.StatusOK)
	})

	handler := clientMiddleware("cli_default", inner)
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Client-ID", "cli_my-app")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if gotClientID != "cli_my-app" {
		t.Errorf("expected cli_my-app, got %q", gotClientID)
	}
}

func TestClientMiddleware_InvalidClientID(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not have been called")
	})

	handler := clientMiddleware("cli_default", inner)

	invalidIDs := []string{"../etc", "bad", "cli_UP", "cli_hello world"}
	for _, id := range invalidIDs {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Client-ID", id)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("X-Client-ID=%q: expected 400, got %d", id, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "invalid client id") {
			t.Errorf("X-Client-ID=%q: expected error message, got %q", id, rr.Body.String())
		}
	}
}

// --- Tests for getClientID ---

func TestGetClientID_WithContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), clientIDKey, "cli_test")
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)

	got := getClientID(req)
	if got != "cli_test" {
		t.Errorf("expected cli_test, got %q", got)
	}
}

func TestGetClientID_WithoutContext(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)

	got := getClientID(req)
	if got != "cli_default" {
		t.Errorf("expected cli_default fallback, got %q", got)
	}
}

// --- Tests for dispatchManager ---

func TestDispatchManager_GetOrCreate_NewClient(t *testing.T) {
	dm := newDispatchManager(4, 12)

	state1, sem1, childSem1 := dm.getOrCreate("cli_a")
	if state1 == nil {
		t.Fatal("state should not be nil")
	}
	if cap(sem1) != 4 {
		t.Errorf("sem capacity = %d, want 4", cap(sem1))
	}
	if cap(childSem1) != 12 {
		t.Errorf("childSem capacity = %d, want 12", cap(childSem1))
	}

	// Second call should return the same instance.
	state2, sem2, childSem2 := dm.getOrCreate("cli_a")
	if state1 != state2 {
		t.Error("expected same state instance on second call")
	}
	if sem1 != sem2 || childSem1 != childSem2 {
		t.Error("expected same semaphore instances on second call")
	}
}

func TestDispatchManager_Isolation(t *testing.T) {
	dm := newDispatchManager(4, 12)

	stateA, _, _ := dm.getOrCreate("cli_a")
	stateB, _, _ := dm.getOrCreate("cli_b")

	if stateA == stateB {
		t.Error("different clients should have different state instances")
	}

	// Verify that changes to one don't affect the other.
	stateA.active = true
	if stateB.active {
		t.Error("setting active on cli_a should not affect cli_b")
	}
}

func TestDispatchManager_Register(t *testing.T) {
	dm := newDispatchManager(4, 12)

	existingState := newDispatchState()
	existingState.broker = newSSEBroker()
	existingSem := make(chan struct{}, 8)
	existingChildSem := make(chan struct{}, 24)

	dm.register("cli_default", existingState, existingSem, existingChildSem)

	state, sem, childSem := dm.getOrCreate("cli_default")
	if state != existingState {
		t.Error("expected registered state")
	}
	if sem != existingSem {
		t.Error("expected registered sem")
	}
	if childSem != existingChildSem {
		t.Error("expected registered childSem")
	}
}

func TestDispatchManager_AllStates(t *testing.T) {
	dm := newDispatchManager(4, 12)
	dm.getOrCreate("cli_a")
	dm.getOrCreate("cli_b")

	all := dm.allStates()
	if len(all) != 2 {
		t.Errorf("expected 2 states, got %d", len(all))
	}
	if _, ok := all["cli_a"]; !ok {
		t.Error("missing cli_a")
	}
	if _, ok := all["cli_b"]; !ok {
		t.Error("missing cli_b")
	}
}

// --- Tests for Config tenant helpers ---

func TestConfigClientDir(t *testing.T) {
	cfg := &Config{ClientsDir: "/home/user/.tetora/clients", DefaultClientID: "cli_default"}

	got := cfg.ClientDir("cli_test")
	if got != "/home/user/.tetora/clients/cli_test" {
		t.Errorf("ClientDir = %q", got)
	}
}

func TestConfigHistoryDBFor(t *testing.T) {
	cfg := &Config{ClientsDir: "/home/user/.tetora/clients", DefaultClientID: "cli_default"}

	got := cfg.HistoryDBFor("cli_test")
	if got != "/home/user/.tetora/clients/cli_test/dbs/history.db" {
		t.Errorf("HistoryDBFor = %q", got)
	}
}

func TestConfigTaskboardDBFor(t *testing.T) {
	cfg := &Config{ClientsDir: "/home/user/.tetora/clients", DefaultClientID: "cli_default"}

	got := cfg.TaskboardDBFor("cli_test")
	if got != "/home/user/.tetora/clients/cli_test/dbs/taskboard.db" {
		t.Errorf("TaskboardDBFor = %q", got)
	}
}

// --- Tests for resolveHistoryDB on Server ---

func TestServerResolveHistoryDB_Default(t *testing.T) {
	cfg := &Config{
		HistoryDB:       "/home/user/.tetora/dbs/history.db",
		ClientsDir:      "/home/user/.tetora/clients",
		DefaultClientID: "cli_default",
	}
	srv := &Server{cfg: cfg}

	got := srv.resolveHistoryDB(cfg, "cli_default")
	if got != cfg.HistoryDB {
		t.Errorf("expected default DB path, got %q", got)
	}
}

func TestServerResolveHistoryDB_OtherClient(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		HistoryDB:       dir + "/dbs/history.db",
		ClientsDir:      dir + "/clients",
		DefaultClientID: "cli_default",
	}
	srv := &Server{cfg: cfg}

	got := srv.resolveHistoryDB(cfg, "cli_other")
	expected := dir + "/clients/cli_other/dbs/history.db"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// --- Tests for historyDBForTask ---

func TestHistoryDBForTask_DefaultClient(t *testing.T) {
	cfg := &Config{
		HistoryDB:       "/db/history.db",
		ClientsDir:      "/clients",
		DefaultClientID: "cli_default",
	}

	task := Task{ClientID: "cli_default"}
	got := historyDBForTask(cfg, task)
	if got != "/db/history.db" {
		t.Errorf("expected default DB, got %q", got)
	}
}

func TestHistoryDBForTask_EmptyClient(t *testing.T) {
	cfg := &Config{
		HistoryDB:       "/db/history.db",
		ClientsDir:      "/clients",
		DefaultClientID: "cli_default",
	}

	task := Task{ClientID: ""}
	got := historyDBForTask(cfg, task)
	if got != "/db/history.db" {
		t.Errorf("expected default DB, got %q", got)
	}
}

func TestHistoryDBForTask_OtherClient(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		HistoryDB:       dir + "/history.db",
		ClientsDir:      dir + "/clients",
		DefaultClientID: "cli_default",
	}

	task := Task{ClientID: "cli_other"}
	got := historyDBForTask(cfg, task)
	expected := dir + "/clients/cli_other/dbs/history.db"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
