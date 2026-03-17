package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"tetora/internal/log"
)

// --- Agent Heartbeat / Self-healing ---

// HeartbeatMonitor periodically checks running tasks for signs of being stuck.
type HeartbeatMonitor struct {
	cfg      HeartbeatConfig
	state    *dispatchState
	notifyFn func(string)

	mu    sync.Mutex
	stats HeartbeatStats

	// Idle tracking.
	systemIdleCheckFn func() bool // injected idle check function
	idleMu            sync.RWMutex
	systemIdleSince   time.Time // when system became idle (zero = not idle)
}

// HeartbeatStats tracks heartbeat monitor activity.
type HeartbeatStats struct {
	CheckCount      int        `json:"checkCount"`                // total scan cycles performed
	StallsDetected  int        `json:"stallsDetected"`            // total stall events
	StallsRecovered int        `json:"stallsRecovered"`           // stalls that resolved (task produced output again)
	AutoCancelled   int        `json:"autoCancelled"`             // tasks force-cancelled by heartbeat
	TimeoutWarnings int        `json:"timeoutWarnings"`           // timeout proximity warnings emitted
	LastCheck       time.Time  `json:"lastCheck"`                 // timestamp of last scan cycle
	SystemIdleSince *time.Time `json:"systemIdleSince,omitempty"` // when system entered idle state
}

func newHeartbeatMonitor(cfg HeartbeatConfig, state *dispatchState, notifyFn func(string)) *HeartbeatMonitor {
	return &HeartbeatMonitor{
		cfg:      cfg,
		state:    state,
		notifyFn: notifyFn,
	}
}

// Start begins the heartbeat monitor loop. Blocks until ctx is cancelled.
func (h *HeartbeatMonitor) Start(ctx context.Context) {
	interval := h.cfg.IntervalOrDefault()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Info("heartbeat monitor started",
		"interval", interval.String(),
		"stallThreshold", h.cfg.StallThresholdOrDefault().String(),
		"timeoutWarnRatio", fmt.Sprintf("%.0f%%", h.cfg.TimeoutWarnRatioOrDefault()*100),
		"autoCancel", h.cfg.AutoCancel)

	for {
		select {
		case <-ctx.Done():
			log.Info("heartbeat monitor stopped")
			return
		case <-ticker.C:
			h.check()
		}
	}
}

// SetIdleCheckFn sets the function used to check system idle state.
func (h *HeartbeatMonitor) SetIdleCheckFn(fn func() bool) {
	h.systemIdleCheckFn = fn
}

// SystemIdleDuration returns how long the system has been continuously idle.
// Returns 0 if the system is not idle or idle tracking is not configured.
func (h *HeartbeatMonitor) SystemIdleDuration() time.Duration {
	h.idleMu.RLock()
	defer h.idleMu.RUnlock()
	if h.systemIdleSince.IsZero() {
		return 0
	}
	return time.Since(h.systemIdleSince)
}

// Stats returns a snapshot of heartbeat statistics.
func (h *HeartbeatMonitor) Stats() HeartbeatStats {
	h.mu.Lock()
	s := h.stats
	h.mu.Unlock()

	h.idleMu.RLock()
	if !h.systemIdleSince.IsZero() {
		t := h.systemIdleSince
		s.SystemIdleSince = &t
	}
	h.idleMu.RUnlock()
	return s
}

// check performs a single heartbeat scan of all running tasks.
func (h *HeartbeatMonitor) check() {
	h.mu.Lock()
	h.stats.CheckCount++
	h.stats.LastCheck = time.Now()
	h.mu.Unlock()

	stallThreshold := h.cfg.StallThresholdOrDefault()
	warnRatio := h.cfg.TimeoutWarnRatioOrDefault()
	now := time.Now()

	h.state.mu.Lock()
	// Snapshot running tasks under lock.
	type taskSnapshot struct {
		id           string
		name         string
		agent        string
		startAt      time.Time
		lastActivity time.Time
		timeout      string
		stalled      bool
		cancelFn     context.CancelFunc
	}
	tasks := make([]taskSnapshot, 0, len(h.state.running))
	for _, ts := range h.state.running {
		tasks = append(tasks, taskSnapshot{
			id:           ts.task.ID,
			name:         ts.task.Name,
			agent:        ts.task.Agent,
			startAt:      ts.startAt,
			lastActivity: ts.lastActivity,
			timeout:      ts.task.Timeout,
			stalled:      ts.stalled,
			cancelFn:     ts.cancelFn,
		})
	}
	h.state.mu.Unlock()

	if len(tasks) == 0 {
		return
	}

	for _, t := range tasks {
		silent := now.Sub(t.lastActivity)
		elapsed := now.Sub(t.startAt)
		shortID := t.id
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		// --- Stall detection ---
		if silent > stallThreshold {
			if !t.stalled {
				// Newly stalled.
				h.mu.Lock()
				h.stats.StallsDetected++
				h.mu.Unlock()

				h.state.mu.Lock()
				if ts, ok := h.state.running[t.id]; ok {
					ts.stalled = true
				}
				h.state.mu.Unlock()

				log.Warn("heartbeat: task stalled",
					"taskId", shortID,
					"name", t.name,
					"agent", t.agent,
					"silent", silent.Round(time.Second).String(),
					"threshold", stallThreshold.String())

				// Publish stall SSE event.
				h.state.publishSSE(SSEEvent{
					Type:   SSETaskStalled,
					TaskID: t.id,
					Data: map[string]any{
						"name":      t.name,
						"agent":     t.agent,
						"silent":    silent.Round(time.Second).String(),
						"elapsed":   elapsed.Round(time.Second).String(),
						"threshold": stallThreshold.String(),
					},
				})

				// Notify.
				if h.notifyFn != nil && h.cfg.NotifyOnStallOrDefault() {
					h.notifyFn(fmt.Sprintf("Agent heartbeat alert: task %s (%s) has stalled — no output for %s",
						shortID, t.name, silent.Round(time.Second)))
				}
			}

			// Auto-cancel if stalled for 2x threshold.
			if h.cfg.AutoCancel && silent > 2*stallThreshold {
				log.Warn("heartbeat: auto-cancelling stalled task",
					"taskId", shortID,
					"name", t.name,
					"silent", silent.Round(time.Second).String())

				if t.cancelFn != nil {
					t.cancelFn()
				}

				h.mu.Lock()
				h.stats.AutoCancelled++
				h.mu.Unlock()

				h.state.publishSSE(SSEEvent{
					Type:   SSEHeartbeatAlert,
					TaskID: t.id,
					Data: map[string]any{
						"action":  "auto_cancel",
						"name":    t.name,
						"agent":   t.agent,
						"silent":  silent.Round(time.Second).String(),
						"elapsed": elapsed.Round(time.Second).String(),
					},
				})

				if h.notifyFn != nil {
					h.notifyFn(fmt.Sprintf("Agent heartbeat: auto-cancelled stalled task %s (%s) after %s of silence",
						shortID, t.name, silent.Round(time.Second)))
				}
			}
		} else if t.stalled {
			// Task was stalled but is now producing output again — recovered.
			h.state.mu.Lock()
			if ts, ok := h.state.running[t.id]; ok {
				ts.stalled = false
			}
			h.state.mu.Unlock()

			h.mu.Lock()
			h.stats.StallsRecovered++
			h.mu.Unlock()

			log.Info("heartbeat: task recovered",
				"taskId", shortID,
				"name", t.name,
				"agent", t.agent)

			h.state.publishSSE(SSEEvent{
				Type:   SSETaskRecovered,
				TaskID: t.id,
				Data: map[string]any{
					"name":  t.name,
					"agent": t.agent,
				},
			})
		}

		// --- Timeout proximity warning ---
		if t.timeout != "" {
			if timeout, err := time.ParseDuration(t.timeout); err == nil && timeout > 0 {
				if elapsed > time.Duration(float64(timeout)*warnRatio) && !t.stalled {
					// Only warn once per task by checking if we're close to the boundary.
					// We emit this warning when elapsed first crosses warnRatio * timeout.
					// Since check() runs periodically, we allow a window of 2 intervals.
					boundary := time.Duration(float64(timeout) * warnRatio)
					if elapsed-boundary < 2*h.cfg.IntervalOrDefault() {
						h.mu.Lock()
						h.stats.TimeoutWarnings++
						h.mu.Unlock()

						remaining := timeout - elapsed
						log.Warn("heartbeat: task approaching timeout",
							"taskId", shortID,
							"name", t.name,
							"elapsed", elapsed.Round(time.Second).String(),
							"timeout", timeout.String(),
							"remaining", remaining.Round(time.Second).String())

						h.state.publishSSE(SSEEvent{
							Type:   SSEHeartbeatAlert,
							TaskID: t.id,
							Data: map[string]any{
								"action":    "timeout_warning",
								"name":      t.name,
								"agent":     t.agent,
								"elapsed":   elapsed.Round(time.Second).String(),
								"timeout":   timeout.String(),
								"remaining": remaining.Round(time.Second).String(),
							},
						})
					}
				}
			}
		}
	}

	// --- Idle state tracking ---
	if h.systemIdleCheckFn != nil {
		idle := h.systemIdleCheckFn()
		h.idleMu.Lock()
		if idle {
			if h.systemIdleSince.IsZero() {
				h.systemIdleSince = time.Now()
				log.Debug("heartbeat: system entered idle state")
			}
		} else {
			if !h.systemIdleSince.IsZero() {
				log.Debug("heartbeat: system left idle state",
					"idleDuration", time.Since(h.systemIdleSince).Round(time.Second).String())
			}
			h.systemIdleSince = time.Time{}
		}
		h.idleMu.Unlock()
	}
}
