package health

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CheckInput contains everything needed for a deep health check,
// decoupled from root-level types.
type CheckInput struct {
	Version   string
	StartTime time.Time
	BaseDir   string

	// Disk thresholds
	DiskBlockMB  int
	DiskWarnMB   int
	DiskBudgetGB float64

	// DB check callback — returns (recordCount, error).
	// nil means DB is disabled.
	DBCheck func() (int, error)
	DBPath  string

	// Providers — key is provider name.
	Providers map[string]ProviderInfo

	// Dispatch state as pre-marshalled JSON.
	DispatchJSON []byte

	// Cron summary (nil = no cron).
	Cron *CronSummary

	// Circuit breaker status (nil = not configured).
	CircuitStatus map[string]any

	// Offline queue info (nil = disabled).
	Queue *QueueInfo
}

// ProviderInfo describes a provider's health.
type ProviderInfo struct {
	Type    string // e.g. "claude-cli", "openai-compatible"
	Status  string // ok, open, recovering
	Circuit string // closed, open, half-open
}

// CronSummary is a snapshot of cron job counts.
type CronSummary struct {
	Total   int
	Enabled int
	Running int
}

// QueueInfo describes offline queue state.
type QueueInfo struct {
	Pending int
	Max     int
}

// DeepCheck performs a comprehensive health check and returns a structured report.
func DeepCheck(input CheckInput) map[string]any {
	checks := map[string]any{}
	overall := "healthy"

	// --- Uptime ---
	uptime := time.Since(input.StartTime)
	checks["uptime"] = map[string]any{
		"startedAt": input.StartTime.Format(time.RFC3339),
		"duration":  uptime.Round(time.Second).String(),
		"seconds":   int(uptime.Seconds()),
	}

	// --- Version ---
	checks["version"] = input.Version

	// --- DB Check ---
	if input.DBCheck != nil {
		dbStart := time.Now()
		count, err := input.DBCheck()
		dbLatency := time.Since(dbStart)
		if err != nil {
			checks["db"] = map[string]any{
				"status":    "error",
				"error":     err.Error(),
				"latencyMs": dbLatency.Milliseconds(),
			}
			overall = DegradeStatus(overall, "unhealthy")
		} else {
			checks["db"] = map[string]any{
				"status":    "ok",
				"path":      input.DBPath,
				"latencyMs": dbLatency.Milliseconds(),
				"records":   count,
			}
		}
	} else {
		checks["db"] = map[string]any{"status": "disabled"}
	}

	// --- Providers ---
	providerChecks := map[string]any{}
	for name, pi := range input.Providers {
		pc := map[string]any{
			"status": pi.Status,
			"type":   pi.Type,
		}
		if pi.Circuit != "" {
			pc["circuit"] = pi.Circuit
		}
		if pi.Status == "open" || pi.Status == "recovering" {
			overall = DegradeStatus(overall, "degraded")
		}
		providerChecks[name] = pc
	}
	checks["providers"] = providerChecks

	// --- Disk ---
	if input.BaseDir != "" {
		di := DiskInfo(input.BaseDir)
		blockGB := 0.2 // 200MB default
		if input.DiskBlockMB > 0 {
			blockGB = float64(input.DiskBlockMB) / 1024
		}
		warnGB := 0.5 // 500MB default
		if input.DiskWarnMB > 0 {
			warnGB = float64(input.DiskWarnMB) / 1024
		} else if input.DiskBudgetGB > 0 {
			warnGB = input.DiskBudgetGB // backward compat
		}
		if freeGB, ok := di["freeGB"].(float64); ok {
			switch {
			case freeGB < blockGB:
				di["status"] = "critical"
				di["warn"] = true
				overall = DegradeStatus(overall, "unhealthy")
			case freeGB < warnGB:
				di["status"] = "warning"
				di["warn"] = true
				overall = DegradeStatus(overall, "degraded")
			default:
				di["status"] = "ok"
			}
		}
		checks["disk"] = di
	}

	// --- Dispatch State ---
	if input.DispatchJSON != nil {
		var dispatchInfo map[string]any
		json.Unmarshal(input.DispatchJSON, &dispatchInfo)
		checks["dispatch"] = dispatchInfo
	}

	// --- Cron ---
	if input.Cron != nil {
		checks["cron"] = map[string]any{
			"jobs":    input.Cron.Total,
			"enabled": input.Cron.Enabled,
			"running": input.Cron.Running,
		}
	}

	// --- Circuit Breakers (summary) ---
	if len(input.CircuitStatus) > 0 {
		checks["circuits"] = input.CircuitStatus
	}

	// --- Offline Queue ---
	if input.Queue != nil {
		queueInfo := map[string]any{
			"status":  "ok",
			"pending": input.Queue.Pending,
			"max":     input.Queue.Max,
		}
		if input.Queue.Pending > 0 {
			overall = DegradeStatus(overall, "degraded")
			queueInfo["status"] = "draining"
		}
		checks["queue"] = queueInfo
	}

	// --- Overall Status ---
	checks["status"] = overall

	return checks
}

// DegradeStatus returns the worse of the current and proposed status.
// Order: healthy < degraded < unhealthy.
func DegradeStatus(current, proposed string) string {
	ranks := map[string]int{"healthy": 0, "degraded": 1, "unhealthy": 2}
	if ranks[proposed] > ranks[current] {
		return proposed
	}
	return current
}

// DiskInfo returns free disk space info for the given path.
func DiskInfo(path string) map[string]any {
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
	if freeBytes := DiskFreeBytes(path); freeBytes > 0 {
		freeGB := float64(freeBytes) / (1024 * 1024 * 1024)
		info["freeGB"] = float64(int(freeGB*100)) / 100 // round to 2 decimals
	}

	return info
}
