package main

import (
	"context"
	"encoding/json"
	"fmt"

	"tetora/internal/tool"
)

// --- P23.4: Financial Tracking ---
// Service struct, types, and method implementations are in internal/life/finance/.
// Tool handler logic is in internal/tool/life_finance.go.
// This file keeps adapter closures and the global singleton.

var globalFinanceService *FinanceService

// --- Tool Handlers (adapter closures) ---

func toolExpenseAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}
	return tool.ExpenseAdd(app.Finance, parseExpenseNL, cfg.Finance.defaultCurrencyOrTWD(), input)
}

func toolExpenseReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}
	return tool.ExpenseReport(app.Finance, input)
}

func toolExpenseBudget(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}
	return tool.ExpenseBudget(app.Finance, cfg.Finance.defaultCurrencyOrTWD(), input)
}
