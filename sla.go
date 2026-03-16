package main

// sla.go is a thin facade wrapping internal/sla.
// Business logic lives in internal/sla/; this file bridges globals and *Config.

import (
	"context"
	"time"

	"tetora/internal/sla"
)

// --- Type aliases ---

type SLAConfig = sla.SLAConfig
type AgentSLACfg = sla.AgentSLACfg
type SLAMetrics = sla.SLAMetrics
type SLAStatus = sla.SLAStatus
type SLACheckResult = sla.SLACheckResult

// --- slaChecker facade ---

// slaChecker wraps sla.Checker, bridging *Config.
type slaChecker struct {
	cfg      *Config
	inner    *sla.Checker
	lastRun  time.Time
}

func newSLAChecker(cfg *Config, notifyFn func(string)) *slaChecker {
	return &slaChecker{
		cfg:   cfg,
		inner: sla.NewChecker(cfg.HistoryDB, cfg.SLA, notifyFn),
	}
}

func (s *slaChecker) tick(ctx context.Context) {
	if !s.cfg.SLA.Enabled {
		return
	}
	s.inner.Tick(ctx)
	s.lastRun = s.inner.LastRun()
}

