package main

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// --- Slot Pressure Guard ---
// Protects interactive sessions (Discord/Telegram conversations) from being
// blocked when cron/batch tasks saturate the concurrency semaphore.
// Non-interactive tasks wait in a poll queue when slots are scarce;
// interactive tasks always acquire immediately but get a warning when pressure is high.

// SlotPressureConfig is the JSON-serializable configuration.
type SlotPressureConfig struct {
	Enabled               bool   `json:"enabled,omitempty"`               // default false
	ReservedSlots         int    `json:"reservedSlots,omitempty"`         // default 2
	WarnThreshold         int    `json:"warnThreshold,omitempty"`         // default 3
	NonInteractiveTimeout string `json:"nonInteractiveTimeout,omitempty"` // default "5m"
	PollInterval          string `json:"pollInterval,omitempty"`          // default "2s"
	MonitorEnabled        bool   `json:"monitorEnabled,omitempty"`        // default false
	MonitorInterval       string `json:"monitorInterval,omitempty"`       // default "30s"
}

// AcquireResult is returned by AcquireSlot with optional warning text.
type AcquireResult struct {
	Warning string // non-empty when pressure is high (for interactive sessions)
}

// SlotPressureGuard wraps sem acquisition with interactive reservation.
type SlotPressureGuard struct {
	cfg      SlotPressureConfig
	sem      chan struct{} // the parent semaphore (depth-0 tasks only)
	semCap   int          // capacity of sem (== MaxConcurrent)
	active   atomic.Int32 // shadows channel usage for non-blocking pressure check
	waiting  atomic.Int32 // number of non-interactive tasks in poll queue
	notifyFn func(string) // notification chain (Telegram/Discord)
	broker   *sseBroker   // SSE event broker

	lastAlertAt atomic.Int64 // unix seconds, cooldown for proactive alerts
}

// reservedSlots returns the configured reserved slots or the default (2).
func (g *SlotPressureGuard) reservedSlots() int {
	if g.cfg.ReservedSlots > 0 {
		return g.cfg.ReservedSlots
	}
	return 2
}

// warnThreshold returns the configured warn threshold or the default (3).
func (g *SlotPressureGuard) warnThreshold() int {
	if g.cfg.WarnThreshold > 0 {
		return g.cfg.WarnThreshold
	}
	return 3
}

// nonInteractiveTimeout returns the configured timeout or the default (5m).
func (g *SlotPressureGuard) nonInteractiveTimeout() time.Duration {
	if g.cfg.NonInteractiveTimeout != "" {
		d, err := time.ParseDuration(g.cfg.NonInteractiveTimeout)
		if err == nil && d > 0 {
			return d
		}
	}
	return 5 * time.Minute
}

// pollInterval returns the configured poll interval or the default (2s).
func (g *SlotPressureGuard) pollInterval() time.Duration {
	if g.cfg.PollInterval != "" {
		d, err := time.ParseDuration(g.cfg.PollInterval)
		if err == nil && d > 0 {
			return d
		}
	}
	return 2 * time.Second
}

// monitorInterval returns the configured monitor interval or the default (30s).
func (g *SlotPressureGuard) monitorInterval() time.Duration {
	if g.cfg.MonitorInterval != "" {
		d, err := time.ParseDuration(g.cfg.MonitorInterval)
		if err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// available returns the number of free slots (non-blocking).
func (g *SlotPressureGuard) available() int {
	return g.semCap - int(g.active.Load())
}

// isInteractiveSource classifies a task source as interactive or non-interactive.
func isInteractiveSource(source string) bool {
	switch {
	case source == "ask", source == "chat":
		return true
	case strings.HasPrefix(source, "route:discord"),
		strings.HasPrefix(source, "route:telegram"),
		strings.HasPrefix(source, "route:slack"),
		strings.HasPrefix(source, "route:line"),
		strings.HasPrefix(source, "route:imessage"),
		strings.HasPrefix(source, "route:matrix"),
		strings.HasPrefix(source, "route:signal"),
		strings.HasPrefix(source, "route:teams"),
		strings.HasPrefix(source, "route:whatsapp"),
		strings.HasPrefix(source, "route:googlechat"):
		return true
	default:
		// Non-interactive: cron, dispatch, queue, agent_dispatch, workflow:*, reflection, taskboard, etc.
		return false
	}
}

// AcquireSlot acquires a slot from the semaphore with pressure awareness.
// For interactive sources: always acquires immediately, returns warning if pressure is high.
// For non-interactive sources: if available <= reservedSlots, polls until a slot frees or timeout.
func (g *SlotPressureGuard) AcquireSlot(ctx context.Context, sem chan struct{}, source string) (*AcquireResult, error) {
	if isInteractiveSource(source) {
		return g.acquireInteractive(ctx, sem, source)
	}
	return g.acquireNonInteractive(ctx, sem, source)
}

// acquireInteractive acquires a slot immediately for interactive sessions.
func (g *SlotPressureGuard) acquireInteractive(ctx context.Context, sem chan struct{}, source string) (*AcquireResult, error) {
	select {
	case sem <- struct{}{}:
		g.active.Add(1)
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	result := &AcquireResult{}

	// Check if pressure is high — warn the user.
	avail := g.available()
	if avail <= g.warnThreshold() {
		used := int(g.active.Load())
		result.Warning = fmt.Sprintf("⚠️ 排程接近滿載（%d/%d slots 使用中），回應可能延遲", used, g.semCap)
	}

	return result, nil
}

// acquireNonInteractive polls for a slot, respecting the reserved slot pool.
func (g *SlotPressureGuard) acquireNonInteractive(ctx context.Context, sem chan struct{}, source string) (*AcquireResult, error) {
	// Fast path: enough slots available — acquire immediately.
	if g.available() > g.reservedSlots() {
		select {
		case sem <- struct{}{}:
			g.active.Add(1)
			return &AcquireResult{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Slow path: poll until a slot frees up or timeout.
	g.waiting.Add(1)
	defer g.waiting.Add(-1)

	logInfo("slot pressure: non-interactive task waiting",
		"source", source,
		"available", g.available(),
		"reserved", g.reservedSlots())

	timeout := time.NewTimer(g.nonInteractiveTimeout())
	defer timeout.Stop()

	poll := time.NewTicker(g.pollInterval())
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-timeout.C:
			// Timeout: force acquire to prevent starvation.
			logWarn("slot pressure: non-interactive task force-acquiring after timeout",
				"source", source, "timeout", g.nonInteractiveTimeout().String())
			select {
			case sem <- struct{}{}:
				g.active.Add(1)
				return &AcquireResult{}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}

		case <-poll.C:
			// Check if enough slots are now available.
			if g.available() > g.reservedSlots() {
				select {
				case sem <- struct{}{}:
					g.active.Add(1)
					return &AcquireResult{}, nil
				default:
					// Another goroutine grabbed it — keep polling.
				}
			}
		}
	}
}

// ReleaseSlot decrements the active counter. Must be called (via defer) after AcquireSlot.
func (g *SlotPressureGuard) ReleaseSlot() {
	g.active.Add(-1)
}

// RunMonitor is a background goroutine that periodically checks slot pressure
// and publishes SSE events / sends notifications when thresholds are crossed.
func (g *SlotPressureGuard) RunMonitor(ctx context.Context) {
	interval := g.monitorInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	const alertCooldown = 60 // seconds between proactive alerts

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			avail := g.available()
			used := int(g.active.Load())
			waiting := int(g.waiting.Load())

			// Publish SSE event for dashboard.
			if g.broker != nil {
				publishToSSEBroker(g.broker, SSEEvent{
					Type: "slot_pressure",
					Data: map[string]any{
						"available": avail,
						"used":     used,
						"capacity": g.semCap,
						"waiting":  waiting,
					},
				})
			}

			// Send proactive alert when pressure is high.
			if avail <= g.warnThreshold() && g.notifyFn != nil {
				now := time.Now().Unix()
				last := g.lastAlertAt.Load()
				if now-last >= alertCooldown {
					if g.lastAlertAt.CompareAndSwap(last, now) {
						msg := fmt.Sprintf("⚠️ 排程即將滿載（%d/%d slots），%d 個非互動任務已進入等待佇列",
							used, g.semCap, waiting)
						g.notifyFn(msg)
					}
				}
			}
		}
	}
}
