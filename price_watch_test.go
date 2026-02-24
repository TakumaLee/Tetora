package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func setupPriceWatchTestDB(t *testing.T) (string, *PriceWatchEngine) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initFinanceDB(dbPath); err != nil {
		t.Fatalf("initFinanceDB: %v", err)
	}
	cfg := &Config{
		HistoryDB: dbPath,
		Finance: FinanceConfig{
			Enabled:         true,
			DefaultCurrency: "TWD",
		},
	}
	engine := newPriceWatchEngine(cfg)
	return dbPath, engine
}

func TestAddWatch(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	err := engine.AddWatch("user1", "USD", "JPY", "gt", 150.0, "telegram")
	if err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	watches, err := engine.ListWatches("user1")
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("expected 1 watch, got %d", len(watches))
	}
	if watches[0].FromCurrency != "USD" || watches[0].ToCurrency != "JPY" {
		t.Errorf("currencies: got %s/%s", watches[0].FromCurrency, watches[0].ToCurrency)
	}
	if watches[0].Condition != "gt" {
		t.Errorf("condition: got %s, want gt", watches[0].Condition)
	}
	if watches[0].Threshold != 150.0 {
		t.Errorf("threshold: got %f, want 150", watches[0].Threshold)
	}
	if watches[0].Status != "active" {
		t.Errorf("status: got %s, want active", watches[0].Status)
	}
}

func TestAddWatch_Validation(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	// Missing currencies.
	if err := engine.AddWatch("user1", "", "JPY", "gt", 150, ""); err == nil {
		t.Error("expected error for empty from currency")
	}
	if err := engine.AddWatch("user1", "USD", "", "gt", 150, ""); err == nil {
		t.Error("expected error for empty to currency")
	}

	// Invalid condition.
	if err := engine.AddWatch("user1", "USD", "JPY", "eq", 150, ""); err == nil {
		t.Error("expected error for invalid condition")
	}

	// Invalid threshold.
	if err := engine.AddWatch("user1", "USD", "JPY", "gt", 0, ""); err == nil {
		t.Error("expected error for zero threshold")
	}
	if err := engine.AddWatch("user1", "USD", "JPY", "gt", -10, ""); err == nil {
		t.Error("expected error for negative threshold")
	}
}

func TestListWatches(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	engine.AddWatch("user1", "USD", "JPY", "gt", 150, "")
	engine.AddWatch("user1", "EUR", "USD", "lt", 1.0, "")
	engine.AddWatch("user2", "GBP", "USD", "gt", 1.3, "")

	watches, err := engine.ListWatches("user1")
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 2 {
		t.Errorf("expected 2 watches for user1, got %d", len(watches))
	}

	watches2, err := engine.ListWatches("user2")
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches2) != 1 {
		t.Errorf("expected 1 watch for user2, got %d", len(watches2))
	}
}

func TestListWatches_Empty(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	watches, err := engine.ListWatches("nobody")
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	if len(watches) != 0 {
		t.Errorf("expected 0 watches, got %d", len(watches))
	}
}

func TestCancelWatch(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	engine.AddWatch("user1", "USD", "JPY", "gt", 150, "")

	watches, _ := engine.ListWatches("user1")
	if len(watches) != 1 {
		t.Fatalf("expected 1 watch, got %d", len(watches))
	}

	err := engine.CancelWatch(watches[0].ID)
	if err != nil {
		t.Fatalf("CancelWatch: %v", err)
	}

	// Verify it's cancelled.
	watches, _ = engine.ListWatches("user1")
	if len(watches) != 1 {
		t.Fatalf("expected 1 watch (cancelled), got %d", len(watches))
	}
	if watches[0].Status != "cancelled" {
		t.Errorf("status: got %s, want cancelled", watches[0].Status)
	}
}

func TestCheckWatches_Triggered(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	// Mock the Frankfurter API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"rates": map[string]float64{"JPY": 155.0},
		})
	}))
	defer srv.Close()

	origURL := currencyBaseURL
	currencyBaseURL = srv.URL
	defer func() { currencyBaseURL = origURL }()

	// Add a watch: alert when USD/JPY > 150.
	engine.AddWatch("user1", "USD", "JPY", "gt", 150.0, "")

	triggered, err := engine.CheckWatches(context.Background())
	if err != nil {
		t.Fatalf("CheckWatches: %v", err)
	}
	if len(triggered) != 1 {
		t.Fatalf("expected 1 triggered, got %d", len(triggered))
	}
	if triggered[0].Status != "triggered" {
		t.Errorf("status: got %s, want triggered", triggered[0].Status)
	}

	// After trigger, check again — should not trigger (status = triggered).
	triggered2, err := engine.CheckWatches(context.Background())
	if err != nil {
		t.Fatalf("CheckWatches 2nd: %v", err)
	}
	if len(triggered2) != 0 {
		t.Errorf("expected 0 triggered on 2nd check, got %d", len(triggered2))
	}
}

func TestCheckWatches_NotTriggered(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"rates": map[string]float64{"JPY": 145.0},
		})
	}))
	defer srv.Close()

	origURL := currencyBaseURL
	currencyBaseURL = srv.URL
	defer func() { currencyBaseURL = origURL }()

	// Watch: alert when USD/JPY > 150. Rate is 145, should NOT trigger.
	engine.AddWatch("user1", "USD", "JPY", "gt", 150.0, "")

	triggered, err := engine.CheckWatches(context.Background())
	if err != nil {
		t.Fatalf("CheckWatches: %v", err)
	}
	if len(triggered) != 0 {
		t.Errorf("expected 0 triggered, got %d", len(triggered))
	}
}

func TestCheckWatches_LessThan(t *testing.T) {
	_, engine := setupPriceWatchTestDB(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"rates": map[string]float64{"USD": 0.95},
		})
	}))
	defer srv.Close()

	origURL := currencyBaseURL
	currencyBaseURL = srv.URL
	defer func() { currencyBaseURL = origURL }()

	// Watch: alert when EUR/USD < 1.0.
	engine.AddWatch("user1", "EUR", "USD", "lt", 1.0, "")

	triggered, err := engine.CheckWatches(context.Background())
	if err != nil {
		t.Fatalf("CheckWatches: %v", err)
	}
	if len(triggered) != 1 {
		t.Fatalf("expected 1 triggered, got %d", len(triggered))
	}
}

func TestToolPriceWatch_Add(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"action":    "add",
		"from":      "USD",
		"to":        "JPY",
		"condition": "gt",
		"threshold": 155.0,
		"userId":    "tester",
	})

	result, err := toolPriceWatch(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolPriceWatch add: %v", err)
	}
	if !strings.Contains(result, "watch added") {
		t.Errorf("expected 'watch added' in result, got: %s", result)
	}
}

func TestToolPriceWatch_List(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	cfg := svc.cfg

	// Add a watch first.
	engine := newPriceWatchEngine(cfg)
	engine.AddWatch("tester", "USD", "JPY", "gt", 155, "")

	input, _ := json.Marshal(map[string]any{
		"action": "list",
		"userId": "tester",
	})

	result, err := toolPriceWatch(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolPriceWatch list: %v", err)
	}
	if !strings.Contains(result, "USD") || !strings.Contains(result, "JPY") {
		t.Errorf("expected USD/JPY in list result, got: %s", result)
	}
}

func TestToolPriceWatch_Cancel(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	cfg := svc.cfg
	engine := newPriceWatchEngine(cfg)
	engine.AddWatch("tester", "USD", "JPY", "gt", 155, "")

	watches, _ := engine.ListWatches("tester")
	if len(watches) == 0 {
		t.Fatal("expected at least 1 watch")
	}

	input, _ := json.Marshal(map[string]any{
		"action": "cancel",
		"id":     watches[0].ID,
	})

	result, err := toolPriceWatch(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolPriceWatch cancel: %v", err)
	}
	if !strings.Contains(result, "cancelled") {
		t.Errorf("expected 'cancelled' in result, got: %s", result)
	}
}

func TestToolPriceWatch_InvalidAction(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"action": "invalid",
	})

	_, err := toolPriceWatch(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestToolPriceWatch_NotInitialized(t *testing.T) {
	oldSvc := globalFinanceService
	globalFinanceService = nil
	defer func() { globalFinanceService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"action": "list"})
	_, err := toolPriceWatch(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when service not initialized")
	}
}
