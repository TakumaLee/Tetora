package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func formatDurationShort(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func (s *Server) registerHealthRoutes(mux *http.ServeMux) {
	cfg := s.cfg

	// --- Agent Count (for pre-update check) ---
	mux.HandleFunc("/api/health/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		s.state.mu.Lock()
		count := len(s.state.running)
		draining := s.state.draining
		type agentInfo struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Agent    string `json:"agent,omitempty"`
			Source  string `json:"source,omitempty"`
			Elapsed string `json:"elapsed"`
			Stalled bool   `json:"stalled,omitempty"`
			Silent  string `json:"silent,omitempty"` // time since last output
		}
		agents := make([]agentInfo, 0, count)
		for _, ts := range s.state.running {
			info := agentInfo{
				ID:      ts.task.ID,
				Name:    ts.task.Name,
				Agent:    ts.task.Agent,
				Source:  ts.task.Source,
				Elapsed: time.Since(ts.startAt).Round(time.Second).String(),
				Stalled: ts.stalled,
			}
			if !ts.lastActivity.IsZero() {
				info.Silent = time.Since(ts.lastActivity).Round(time.Second).String()
			}
			agents = append(agents, info)
		}
		s.state.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"active":   count,
			"draining": draining,
			"agents":   agents,
		})
	})

	// --- Dashboard Health Summary ---
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		uptime := time.Since(s.startTime)
		uptimeStr := formatDurationShort(uptime)
		dbSize := "-"
		if cfg.HistoryDB != "" {
			if fi, err := os.Stat(cfg.HistoryDB); err == nil {
				mb := float64(fi.Size()) / (1024 * 1024)
				dbSize = fmt.Sprintf("%.1f MB", mb)
			}
		}
		sseClients := 0
		if s.state != nil && s.state.broker != nil {
			sseClients = s.state.broker.ClientCount()
		}
		lastCron := "-"
		if s.cron != nil {
			if last := s.cron.LastRunTime(); !last.IsZero() {
				ago := time.Since(last)
				lastCron = formatDurationShort(ago) + " ago"
			}
		}
		provider := cfg.DefaultProvider
		if provider == "" && len(cfg.Agents) > 0 {
			for _, a := range cfg.Agents {
				if a.Model != "" {
					provider = a.Model
					break
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"uptime":     uptimeStr,
			"dbSize":     dbSize,
			"sseClients": sseClients,
			"lastCron":   lastCron,
			"provider":   provider,
		})
	})

	// --- Health ---
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		checks := deepHealthCheck(cfg, s.state, s.cron, s.startTime)
		// Report degraded services.
		if len(s.DegradedServices) > 0 {
			checks["degradedServices"] = s.DegradedServices
			if st, ok := checks["status"].(string); ok {
				checks["status"] = degradeStatus(st, "degraded")
			}
		}
		// Heartbeat monitor stats.
		if s.heartbeatMonitor != nil {
			stats := s.heartbeatMonitor.Stats()
			hbInfo := map[string]any{
				"enabled":         true,
				"checkCount":      stats.CheckCount,
				"stallsDetected":  stats.StallsDetected,
				"stallsRecovered": stats.StallsRecovered,
				"autoCancelled":   stats.AutoCancelled,
				"timeoutWarnings": stats.TimeoutWarnings,
			}
			if !stats.LastCheck.IsZero() {
				hbInfo["lastCheck"] = stats.LastCheck.Format(time.RFC3339)
			}
			// Count currently stalled tasks.
			stalledCount := 0
			s.state.mu.Lock()
			for _, ts := range s.state.running {
				if ts.stalled {
					stalledCount++
				}
			}
			s.state.mu.Unlock()
			hbInfo["stalledNow"] = stalledCount
			if stalledCount > 0 {
				if st, ok := checks["status"].(string); ok {
					checks["status"] = degradeStatus(st, "degraded")
				}
			}
			checks["heartbeat"] = hbInfo
		} else {
			checks["heartbeat"] = map[string]any{"enabled": false}
		}
		b, _ := json.MarshalIndent(checks, "", "  ")
		w.Write(b)
	})

	// --- Metrics ---
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if metrics == nil {
			http.Error(w, "metrics not initialized", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		metrics.WriteMetrics(w)
	})

	// --- Circuit Breakers ---
	mux.HandleFunc("/circuits", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var status map[string]any
		if cfg.circuits != nil {
			status = cfg.circuits.Status()
		} else {
			status = map[string]any{}
		}
		b, _ := json.MarshalIndent(status, "", "  ")
		w.Write(b)
	})

	mux.HandleFunc("/circuits/", func(w http.ResponseWriter, r *http.Request) {
		// POST /circuits/{provider}/reset
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/circuits/")
		provider := strings.TrimSuffix(path, "/reset")
		if provider == "" || !strings.HasSuffix(path, "/reset") {
			http.Error(w, `{"error":"use POST /circuits/{provider}/reset"}`, http.StatusBadRequest)
			return
		}
		if cfg.circuits == nil {
			http.Error(w, `{"error":"circuit breaker not initialized"}`, http.StatusServiceUnavailable)
			return
		}
		if ok := cfg.circuits.ResetKey(provider); !ok {
			http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
			return
		}
		auditLog(cfg.HistoryDB, "circuit.reset", r.RemoteAddr, provider, "")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"provider":%q,"state":"closed"}`, provider)))
	})
}
