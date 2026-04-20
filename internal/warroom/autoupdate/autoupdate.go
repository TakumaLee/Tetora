package autoupdate

import (
	"context"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
	"tetora/internal/warroom"
)

// Run iterates all fronts in status.json, calls the registered updater for
// each front with auto=true (unless manual_override is active), and saves the
// result. Callers must NOT hold warroom.Mu before calling Run — Run acquires
// it internally to prevent concurrent writes.
func Run(ctx context.Context, cfg *config.Config) error {
	warroom.Mu.Lock()
	defer warroom.Mu.Unlock()

	statusPath := warroom.StatusPath(cfg.BaseDir)
	s, err := warroom.LoadStatus(statusPath)
	if err != nil {
		return err
	}

	var updated, skipped, failed int
	for i, raw := range s.Fronts {
		id, err := warroom.FrontID(raw)
		if err != nil {
			log.Error("warroom autoupdate: cannot read front id", "err", err)
			failed++
			continue
		}

		var auto bool
		warroom.FrontField(raw, "auto", &auto) //nolint:errcheck — missing field defaults to false
		if !auto {
			skipped++
			continue
		}

		// Respect manual override.
		var override struct {
			Active    bool    `json:"active"`
			ExpiresAt *string `json:"expires_at"`
		}
		if err := warroom.FrontField(raw, "manual_override", &override); err == nil {
			if override.Active {
				if override.ExpiresAt == nil || *override.ExpiresAt == "" {
					log.Info("warroom autoupdate: manual override active (no expiry), skipping", "front", id)
					skipped++
					continue
				}
				if t, err := time.Parse(time.RFC3339, *override.ExpiresAt); err == nil && t.After(time.Now()) {
					log.Info("warroom autoupdate: manual override active, skipping", "front", id, "expires_at", *override.ExpiresAt)
					skipped++
					continue
				}
			}
		}

		updater, ok := updaters[id]
		if !ok {
			skipped++
			continue
		}

		fields, err := updater(ctx, cfg, raw)
		if err != nil {
			log.Error("warroom autoupdate: updater failed", "front", id, "err", err)
			failed++
			continue
		}
		if fields == nil {
			skipped++
			continue
		}

		newRaw, err := warroom.UpdateFrontFields(raw, fields)
		if err != nil {
			log.Error("warroom autoupdate: UpdateFrontFields failed", "front", id, "err", err)
			failed++
			continue
		}
		s.Fronts[i] = newRaw
		updated++
	}

	if err := warroom.SaveStatus(statusPath, s); err != nil {
		return err
	}
	log.Info("warroom autoupdate complete", "updated", updated, "skipped", skipped, "failed", failed)
	return nil
}
