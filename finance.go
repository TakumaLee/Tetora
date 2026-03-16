package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// --- P23.4: Financial Tracking ---
// Service struct, types, and method implementations are in internal/life/finance/.
// This file keeps tool handlers and the global singleton.

var globalFinanceService *FinanceService

// --- Tool Handlers ---

// toolExpenseAdd handles the expense_add tool.
func toolExpenseAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	var args struct {
		Text        string   `json:"text"`
		Amount      float64  `json:"amount"`
		Currency    string   `json:"currency"`
		Category    string   `json:"category"`
		Description string   `json:"description"`
		UserID      string   `json:"userId"`
		Tags        []string `json:"tags"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	amount := args.Amount
	currency := args.Currency
	category := args.Category
	description := args.Description

	// If natural language text is provided, parse it.
	if args.Text != "" {
		nlAmount, nlCurrency, nlCategory, nlDesc := parseExpenseNL(args.Text, cfg.Finance.defaultCurrencyOrTWD())
		if amount <= 0 {
			amount = nlAmount
		}
		if currency == "" {
			currency = nlCurrency
		}
		if category == "" {
			category = nlCategory
		}
		if description == "" {
			description = nlDesc
		}
	}

	if amount <= 0 {
		return "", fmt.Errorf("could not determine amount; provide amount or natural language text like '午餐 350 元'")
	}

	expense, err := app.Finance.AddExpense(args.UserID, amount, currency, category, description, args.Tags)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(expense, "", "  ")
	return string(out), nil
}

// toolExpenseReport handles the expense_report tool.
func toolExpenseReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	var args struct {
		Period   string `json:"period"`
		Category string `json:"category"`
		UserID   string `json:"userId"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	period := args.Period
	if period == "" {
		period = "month"
	}

	report, err := app.Finance.GenerateReport(args.UserID, period, args.Currency)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}

// toolExpenseBudget handles the expense_budget tool.
func toolExpenseBudget(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	var args struct {
		Action   string  `json:"action"`
		Category string  `json:"category"`
		Limit    float64 `json:"limit"`
		Currency string  `json:"currency"`
		UserID   string  `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "set":
		if args.Category == "" {
			return "", fmt.Errorf("category is required for set action")
		}
		if args.Limit <= 0 {
			return "", fmt.Errorf("limit must be positive for set action")
		}
		err := app.Finance.SetBudget(args.UserID, args.Category, args.Limit, args.Currency)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Budget set: %s = %.2f %s/month",
			args.Category, args.Limit,
			func() string {
				if args.Currency != "" {
					return strings.ToUpper(args.Currency)
				}
				return cfg.Finance.defaultCurrencyOrTWD()
			}()), nil

	case "list":
		budgets, err := app.Finance.GetBudgets(args.UserID)
		if err != nil {
			return "", err
		}
		if len(budgets) == 0 {
			return "No budgets configured.", nil
		}
		out, _ := json.MarshalIndent(budgets, "", "  ")
		return string(out), nil

	case "check":
		statuses, err := app.Finance.CheckBudgets(args.UserID)
		if err != nil {
			return "", err
		}
		if len(statuses) == 0 {
			return "No budgets configured.", nil
		}
		out, _ := json.MarshalIndent(statuses, "", "  ")
		return string(out), nil

	default:
		return "", fmt.Errorf("unknown action %q (use: set, list, check)", args.Action)
	}
}
