package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolCurrencyConvert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("from") != "USD" || q.Get("to") != "JPY" {
			t.Errorf("unexpected query params: %v", q)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"amount": 100.0,
			"base":   "USD",
			"date":   "2026-02-23",
			"rates":  map[string]float64{"JPY": 14950.50},
		})
	}))
	defer srv.Close()

	origURL := currencyBaseURL
	currencyBaseURL = srv.URL
	defer func() { currencyBaseURL = origURL }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"amount": 100, "from": "USD", "to": "JPY"})
	result, err := toolCurrencyConvert(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "100.00 USD") {
		t.Errorf("expected amount in result, got: %s", result)
	}
	if !strings.Contains(result, "14950.50 JPY") {
		t.Errorf("expected converted amount, got: %s", result)
	}
	if !strings.Contains(result, "2026-02-23") {
		t.Errorf("expected date in result, got: %s", result)
	}
}

func TestToolCurrencyConvertMissingFields(t *testing.T) {
	cfg := &Config{}

	// Missing amount.
	input, _ := json.Marshal(map[string]any{"from": "USD", "to": "JPY"})
	_, err := toolCurrencyConvert(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for zero amount")
	}

	// Missing from.
	input, _ = json.Marshal(map[string]any{"amount": 100, "to": "JPY"})
	_, err = toolCurrencyConvert(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing from")
	}

	// Missing to.
	input, _ = json.Marshal(map[string]any{"amount": 100, "from": "USD"})
	_, err = toolCurrencyConvert(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for missing to")
	}
}

func TestToolCurrencyRates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"base": "EUR",
			"date": "2026-02-23",
			"rates": map[string]float64{
				"JPY": 162.50,
				"USD": 1.0850,
				"TWD": 34.20,
			},
		})
	}))
	defer srv.Close()

	origURL := currencyBaseURL
	currencyBaseURL = srv.URL
	defer func() { currencyBaseURL = origURL }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"base": "EUR", "currencies": "JPY,USD,TWD"})
	result, err := toolCurrencyRates(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "EUR") {
		t.Errorf("expected EUR in result, got: %s", result)
	}
	if !strings.Contains(result, "JPY") {
		t.Errorf("expected JPY in result, got: %s", result)
	}
	// Check sorted output — JPY should come before TWD, TWD before USD.
	jIdx := strings.Index(result, "JPY")
	tIdx := strings.Index(result, "TWD")
	uIdx := strings.Index(result, "USD")
	if jIdx >= tIdx || tIdx >= uIdx {
		t.Errorf("expected sorted output, got: %s", result)
	}
}

func TestToolCurrencyRatesDefaultBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("from") != "USD" {
			t.Errorf("expected default base USD, got: %s", q.Get("from"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"base":  "USD",
			"date":  "2026-02-23",
			"rates": map[string]float64{"EUR": 0.92},
		})
	}))
	defer srv.Close()

	origURL := currencyBaseURL
	currencyBaseURL = srv.URL
	defer func() { currencyBaseURL = origURL }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{})
	result, err := toolCurrencyRates(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "USD") {
		t.Errorf("expected USD in result, got: %s", result)
	}
}

func TestToolCurrencyAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
	}))
	defer srv.Close()

	origURL := currencyBaseURL
	currencyBaseURL = srv.URL
	defer func() { currencyBaseURL = origURL }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"amount": 100, "from": "USD", "to": "INVALID"})
	_, err := toolCurrencyConvert(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 in error, got: %v", err)
	}
}
