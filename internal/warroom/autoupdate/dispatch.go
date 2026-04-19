package autoupdate

import (
	"context"
	"encoding/json"

	"tetora/internal/config"
)

// UpdaterFunc receives the current raw front JSON and returns a map of fields
// to merge into it. Return nil map + nil error to skip without error.
type UpdaterFunc func(ctx context.Context, cfg *config.Config, front json.RawMessage) (map[string]any, error)

var updaters = map[string]UpdaterFunc{
	"polymarket":          updatePolymarket,
	"taiwan-stock-auto":   updateTaiwanStockAuto,
	"taiwan-stock-manual": updateTaiwanStockManual,
	"tetora":              updateTetora,
}
