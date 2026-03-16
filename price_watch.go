package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// --- P23.4: Price Watch Engine ---
// Service struct, types, and method implementations are in internal/life/pricewatch/.
// This file keeps the tool handler. globalFinanceService is used as the guard.

// --- Tool Handler ---

// toolPriceWatch handles the price_watch tool.
func toolPriceWatch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	fs := globalFinanceService
	if app != nil && app.Finance != nil {
		fs = app.Finance
	}
	if fs == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	var args struct {
		Action        string  `json:"action"`
		From          string  `json:"from"`
		To            string  `json:"to"`
		Condition     string  `json:"condition"`
		Threshold     float64 `json:"threshold"`
		ID            int     `json:"id"`
		UserID        string  `json:"userId"`
		NotifyChannel string  `json:"notifyChannel"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	engineCfg := cfg
	if engineCfg.HistoryDB == "" {
		engineCfg = &Config{HistoryDB: fs.DBPath()}
	}
	engine := newPriceWatchEngine(engineCfg)

	switch args.Action {
	case "add":
		err := engine.AddWatch(args.UserID, args.From, args.To, args.Condition, args.Threshold, args.NotifyChannel)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Price watch added: alert when %s/%s %s %.4f",
			strings.ToUpper(args.From), strings.ToUpper(args.To), args.Condition, args.Threshold), nil

	case "list":
		watches, err := engine.ListWatches(args.UserID)
		if err != nil {
			return "", err
		}
		if len(watches) == 0 {
			return "No price watches configured.", nil
		}
		out, _ := json.MarshalIndent(watches, "", "  ")
		return string(out), nil

	case "cancel":
		if args.ID <= 0 {
			return "", fmt.Errorf("id is required for cancel action")
		}
		err := engine.CancelWatch(args.ID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Price watch #%d cancelled.", args.ID), nil

	default:
		return "", fmt.Errorf("unknown action %q (use: add, list, cancel)", args.Action)
	}
}
