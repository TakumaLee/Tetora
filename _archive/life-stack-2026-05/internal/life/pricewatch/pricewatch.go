// Package pricewatch provides currency price alert management.
package pricewatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"tetora/internal/life/lifedb"
)

// PriceWatch represents a currency price alert.
type PriceWatch struct {
	ID            int     `json:"id"`
	UserID        string  `json:"userId"`
	FromCurrency  string  `json:"fromCurrency"`
	ToCurrency    string  `json:"toCurrency"`
	Condition     string  `json:"condition"` // "lt", "gt"
	Threshold     float64 `json:"threshold"`
	Status        string  `json:"status"` // "active", "triggered", "cancelled"
	NotifyChannel string  `json:"notifyChannel"`
	LastChecked   string  `json:"lastChecked"`
	CreatedAt     string  `json:"createdAt"`
}

// Service checks price conditions periodically.
type Service struct {
	db              lifedb.DB
	dbPath          string
	currencyBaseURL string
}

// New creates a new price watch Service.
func New(dbPath, currencyBaseURL string, db lifedb.DB) *Service {
	return &Service{
		db:              db,
		dbPath:          dbPath,
		currencyBaseURL: currencyBaseURL,
	}
}

// AddWatch creates a new price watch.
func (pw *Service) AddWatch(userID, from, to, condition string, threshold float64, notifyChannel string) error {
	if userID == "" {
		userID = "default"
	}
	from = strings.ToUpper(from)
	to = strings.ToUpper(to)
	if from == "" || to == "" {
		return fmt.Errorf("from and to currencies are required")
	}
	if condition != "lt" && condition != "gt" {
		return fmt.Errorf("condition must be 'lt' or 'gt'")
	}
	if threshold <= 0 {
		return fmt.Errorf("threshold must be positive")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO price_watches (user_id, from_currency, to_currency, condition, threshold, status, notify_channel, created_at)
		 VALUES ('%s', '%s', '%s', '%s', %f, 'active', '%s', '%s')`,
		pw.db.Escape(userID), pw.db.Escape(from), pw.db.Escape(to),
		pw.db.Escape(condition), threshold,
		pw.db.Escape(notifyChannel), now,
	)

	cmd := exec.Command("sqlite3", pw.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("add watch: %w: %s", err, string(out))
	}
	return nil
}

// ListWatches returns all watches for a user.
func (pw *Service) ListWatches(userID string) ([]PriceWatch, error) {
	if userID == "" {
		userID = "default"
	}

	sql := fmt.Sprintf(
		`SELECT id, user_id, from_currency, to_currency, condition, threshold, status, notify_channel, last_checked, created_at
		 FROM price_watches WHERE user_id = '%s' ORDER BY created_at DESC`,
		pw.db.Escape(userID),
	)

	rows, err := pw.db.Query(pw.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list watches: %w", err)
	}

	watches := make([]PriceWatch, 0, len(rows))
	for _, row := range rows {
		watches = append(watches, PriceWatch{
			ID:            int(jsonFloat(row["id"])),
			UserID:        jsonStr(row["user_id"]),
			FromCurrency:  jsonStr(row["from_currency"]),
			ToCurrency:    jsonStr(row["to_currency"]),
			Condition:     jsonStr(row["condition"]),
			Threshold:     jsonFloat(row["threshold"]),
			Status:        jsonStr(row["status"]),
			NotifyChannel: jsonStr(row["notify_channel"]),
			LastChecked:   jsonStr(row["last_checked"]),
			CreatedAt:     jsonStr(row["created_at"]),
		})
	}
	return watches, nil
}

// CancelWatch sets a watch status to cancelled.
func (pw *Service) CancelWatch(id int) error {
	sql := fmt.Sprintf(
		`UPDATE price_watches SET status = 'cancelled' WHERE id = %d AND status = 'active'`,
		id,
	)
	cmd := exec.Command("sqlite3", pw.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cancel watch: %w: %s", err, string(out))
	}
	return nil
}

// FetchRate fetches the current exchange rate from the configured currency API.
func (pw *Service) FetchRate(from, to string) (float64, error) {
	apiURL := fmt.Sprintf("%s/latest?from=%s&to=%s", pw.currencyBaseURL, from, to)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return 0, fmt.Errorf("fetch rate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("rate API returned %d", resp.StatusCode)
	}

	var result struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode rate: %w", err)
	}

	rate, ok := result.Rates[to]
	if !ok {
		return 0, fmt.Errorf("rate not found for %s/%s", from, to)
	}
	return rate, nil
}

// CheckWatches evaluates all active watches and returns triggered ones.
func (pw *Service) CheckWatches(ctx context.Context) ([]PriceWatch, error) {
	sql := `SELECT id, user_id, from_currency, to_currency, condition, threshold, status, notify_channel, last_checked, created_at
	        FROM price_watches WHERE status = 'active'`

	rows, err := pw.db.Query(pw.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("check watches: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var triggered []PriceWatch

	for _, row := range rows {
		id := int(jsonFloat(row["id"]))
		from := jsonStr(row["from_currency"])
		to := jsonStr(row["to_currency"])
		condition := jsonStr(row["condition"])
		threshold := jsonFloat(row["threshold"])

		updateSQL := fmt.Sprintf(
			`UPDATE price_watches SET last_checked = '%s' WHERE id = %d`,
			now, id,
		)
		cmd := exec.Command("sqlite3", pw.dbPath, updateSQL)
		cmd.Run()

		rate, err := pw.FetchRate(from, to)
		if err != nil {
			pw.db.LogWarn("price watch fetch failed", "id", id, "from", from, "to", to, "error", err)
			continue
		}

		met := false
		switch condition {
		case "lt":
			met = rate < threshold
		case "gt":
			met = rate > threshold
		}

		if met {
			triggerSQL := fmt.Sprintf(
				`UPDATE price_watches SET status = 'triggered', last_checked = '%s' WHERE id = %d`,
				now, id,
			)
			cmd := exec.Command("sqlite3", pw.dbPath, triggerSQL)
			cmd.Run()

			triggered = append(triggered, PriceWatch{
				ID:            id,
				UserID:        jsonStr(row["user_id"]),
				FromCurrency:  from,
				ToCurrency:    to,
				Condition:     condition,
				Threshold:     threshold,
				Status:        "triggered",
				NotifyChannel: jsonStr(row["notify_channel"]),
				LastChecked:   now,
				CreatedAt:     jsonStr(row["created_at"]),
			})
		}
	}

	return triggered, nil
}

// Start runs the price watch check loop (every 30 minutes).
func (pw *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()

		if triggered, err := pw.CheckWatches(ctx); err == nil && len(triggered) > 0 {
			for _, w := range triggered {
				pw.db.LogInfo("price watch triggered",
					"id", w.ID, "from", w.FromCurrency, "to", w.ToCurrency,
					"condition", w.Condition, "threshold", w.Threshold)
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				triggered, err := pw.CheckWatches(ctx)
				if err != nil {
					pw.db.LogWarn("price watch check error", "error", err)
					continue
				}
				for _, w := range triggered {
					pw.db.LogInfo("price watch triggered",
						"id", w.ID, "from", w.FromCurrency, "to", w.ToCurrency,
						"condition", w.Condition, "threshold", w.Threshold)
				}
			}
		}
	}()
}

// --- local helpers ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		var f float64
		fmt.Sscanf(x, "%f", &f)
		return f
	}
	return 0
}

// SetBaseURL updates the currency API base URL (used in tests).
func (pw *Service) SetBaseURL(url string) { pw.currencyBaseURL = url }
