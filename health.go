package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// deepHealthCheck performs a comprehensive health check and returns a structured report.
func deepHealthCheck(cfg *Config, state *dispatchState, cron *CronEngine, startTime time.Time) map[string]any {
	checks := map[string]any{}
	overall := "healthy"

	// --- Uptime ---
	uptime := time.Since(startTime)
	checks["uptime"] = map[string]any{
		"startedAt": startTime.Format(time.RFC3339),
		"duration":  uptime.Round(time.Second).String(),
		"seconds":   int(uptime.Seconds()),
	}

	// --- Version ---
	checks["version"] = tetoraVersion

	// --- DB Check ---
	if cfg.HistoryDB != "" {
		dbStart := time.Now()
		rows, err := queryDB(cfg.HistoryDB, "SELECT count(*) as cnt FROM job_runs;")
		dbLatency := time.Since(dbStart)
		if err != nil {
			checks["db"] = map[string]any{
				"status":    "error",
				"error":     err.Error(),
				"latencyMs": dbLatency.Milliseconds(),
			}
			overall = degradeStatus(overall, "unhealthy")
		} else {
			count := 0
			if len(rows) > 0 {
				if v, ok := rows[0]["cnt"]; ok {
					fmt.Sscanf(fmt.Sprint(v), "%d", &count)
				}
			}
			checks["db"] = map[string]any{
				"status":    "ok",
				"path":      cfg.HistoryDB,
				"latencyMs": dbLatency.Milliseconds(),
				"records":   count,
			}
		}
	} else {
		checks["db"] = map[string]any{"status": "disabled"}
	}

	// --- Providers ---
	providerChecks := map[string]any{}
	if cfg.registry != nil {
		for name := range cfg.Providers {
			pc := map[string]any{
				"status": "ok",
				"type":   cfg.Providers[name].Type,
			}
			// Circuit breaker status.
			if cfg.circuits != nil {
				cb := cfg.circuits.get(name)
				st := cb.State()
				pc["circuit"] = st.String()
				if st == CircuitOpen {
					pc["status"] = "open"
					overall = degradeStatus(overall, "degraded")
				} else if st == CircuitHalfOpen {
					pc["status"] = "recovering"
					overall = degradeStatus(overall, "degraded")
				}
			}
			providerChecks[name] = pc
		}
		// Always include default "claude" provider.
		if _, exists := providerChecks["claude"]; !exists {
			pc := map[string]any{"status": "ok", "type": "claude-cli"}
			if cfg.circuits != nil {
				cb := cfg.circuits.get("claude")
				st := cb.State()
				pc["circuit"] = st.String()
				if st == CircuitOpen {
					pc["status"] = "open"
					overall = degradeStatus(overall, "degraded")
				}
			}
			providerChecks["claude"] = pc
		}
	}
	checks["providers"] = providerChecks

	// --- Disk ---
	if cfg.baseDir != "" {
		di := diskInfo(cfg.baseDir)
		checks["disk"] = di
		if freeGB, ok := di["freeGB"].(float64); ok && freeGB < 1.0 {
			overall = degradeStatus(overall, "degraded")
		}
	}

	// --- Dispatch State ---
	var dispatchInfo map[string]any
	json.Unmarshal(state.statusJSON(), &dispatchInfo)
	checks["dispatch"] = dispatchInfo

	// --- Cron ---
	if cron != nil {
		jobs := cron.ListJobs()
		running := 0
		enabled := 0
		for _, j := range jobs {
			if j.Running {
				running++
			}
			if j.Enabled {
				enabled++
			}
		}
		checks["cron"] = map[string]any{
			"jobs":    len(jobs),
			"enabled": enabled,
			"running": running,
		}
	}

	// --- Circuit Breakers (summary) ---
	if cfg.circuits != nil {
		circuitStatus := cfg.circuits.status()
		if len(circuitStatus) > 0 {
			checks["circuits"] = circuitStatus
		}
	}

	// --- Offline Queue ---
	if cfg.OfflineQueue.Enabled && cfg.HistoryDB != "" {
		pending := countPendingQueue(cfg.HistoryDB)
		queueInfo := map[string]any{
			"status":  "ok",
			"pending": pending,
			"max":     cfg.OfflineQueue.maxItemsOrDefault(),
		}
		if pending > 0 {
			overall = degradeStatus(overall, "degraded")
			queueInfo["status"] = "draining"
		}
		checks["queue"] = queueInfo
	}

	// --- Overall Status ---
	checks["status"] = overall

	return checks
}

// degradeStatus returns the worse of the current and proposed status.
// Order: healthy < degraded < unhealthy.
func degradeStatus(current, proposed string) string {
	ranks := map[string]int{"healthy": 0, "degraded": 1, "unhealthy": 2}
	if ranks[proposed] > ranks[current] {
		return proposed
	}
	return current
}

// diskInfo returns free disk space info for the given path.
func diskInfo(path string) map[string]any {
	// Use os.Stat to check if path exists, then check dir size as an approximation.
	// For actual free space, we'd need syscall.Statfs which is platform-specific.
	// Use a simple approach: check outputs directory size.
	info := map[string]any{"status": "ok"}

	outputsDir := filepath.Join(path, "outputs")
	var totalSize int64
	filepath.Walk(outputsDir, func(_ string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			totalSize += fi.Size()
		}
		return nil
	})
	info["outputsSizeMB"] = float64(totalSize) / (1024 * 1024)

	logsDir := filepath.Join(path, "logs")
	var logsSize int64
	filepath.Walk(logsDir, func(_ string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			logsSize += fi.Size()
		}
		return nil
	})
	info["logsSizeMB"] = float64(logsSize) / (1024 * 1024)

	// Check actual free disk space using statfs (darwin/linux).
	if freeBytes := diskFreeBytes(path); freeBytes > 0 {
		freeGB := float64(freeBytes) / (1024 * 1024 * 1024)
		info["freeGB"] = float64(int(freeGB*100)) / 100 // round to 2 decimals
	}

	return info
}
