package main

import "tetora/internal/quiet"

var quietGlobal = quiet.NewState(logInfo)

// toQuietCfg converts root QuietHoursConfig to internal quiet.Config.
func toQuietCfg(cfg *Config) quiet.Config {
	return quiet.Config{
		Enabled: cfg.QuietHours.Enabled,
		Start:   cfg.QuietHours.Start,
		End:     cfg.QuietHours.End,
		TZ:      cfg.QuietHours.TZ,
		Digest:  cfg.QuietHours.Digest,
	}
}

