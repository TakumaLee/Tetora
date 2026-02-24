package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Base URL for Frankfurter API (overridable in tests).
var currencyBaseURL = "https://api.frankfurter.app"

func toolCurrencyConvert(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Amount float64 `json:"amount"`
		From   string  `json:"from"`
		To     string  `json:"to"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Amount <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}
	if args.From == "" || args.To == "" {
		return "", fmt.Errorf("both 'from' and 'to' currency codes are required")
	}
	args.From = strings.ToUpper(args.From)
	args.To = strings.ToUpper(args.To)

	apiURL := fmt.Sprintf("%s/latest?amount=%.2f&from=%s&to=%s",
		currencyBaseURL, args.Amount, args.From, args.To)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("currency API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("currency API returned %d", resp.StatusCode)
	}

	var result struct {
		Amount float64            `json:"amount"`
		Base   string             `json:"base"`
		Date   string             `json:"date"`
		Rates  map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%.2f %s =", args.Amount, result.Base)
	for cur, val := range result.Rates {
		fmt.Fprintf(&sb, " %.2f %s", val, cur)
	}
	fmt.Fprintf(&sb, "\n(as of %s)", result.Date)
	return sb.String(), nil
}

func toolCurrencyRates(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Base       string `json:"base"`
		Currencies string `json:"currencies"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	base := strings.ToUpper(args.Base)
	if base == "" {
		base = "USD"
	}

	apiURL := fmt.Sprintf("%s/latest?from=%s", currencyBaseURL, base)
	if args.Currencies != "" {
		apiURL += "&to=" + strings.ToUpper(strings.ReplaceAll(args.Currencies, " ", ""))
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("currency API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("currency API returned %d", resp.StatusCode)
	}

	var result struct {
		Base  string             `json:"base"`
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}

	// Sort currency codes for stable output.
	codes := make([]string, 0, len(result.Rates))
	for c := range result.Rates {
		codes = append(codes, c)
	}
	sort.Strings(codes)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Exchange rates for 1 %s (as of %s):\n", result.Base, result.Date)
	for _, c := range codes {
		fmt.Fprintf(&sb, "  %s: %.4f\n", c, result.Rates[c])
	}
	return sb.String(), nil
}
