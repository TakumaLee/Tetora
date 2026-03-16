package main

import (
	"context"
	"encoding/json"
	"fmt"

	"tetora/internal/tool"
)

// --- P23.4: Price Watch Engine ---
// Service struct, types, and method implementations are in internal/life/pricewatch/.
// Tool handler logic is in internal/tool/life_pricewatch.go.

// --- Tool Handler (adapter closure) ---

func toolPriceWatch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	fs := globalFinanceService
	if app != nil && app.Finance != nil {
		fs = app.Finance
	}
	if fs == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	engineCfg := cfg
	if engineCfg.HistoryDB == "" {
		engineCfg = &Config{HistoryDB: fs.DBPath()}
	}
	engine := newPriceWatchEngine(engineCfg)

	return tool.PriceWatch(engine, input)
}
