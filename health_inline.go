package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type healthCheckInput struct {
	Version      string
	StartTime    time.Time
	BaseDir      string
	DiskBlockMB  int
	DiskWarnMB   int
	DiskBudgetGB float64
	DBCheck      func() (int, error)
	DBPath       string
	Providers    map[string]healthProviderInfo
	DispatchJSON []byte
	Cron         *healthCronSummary
	CircuitStatus map[string]any
	Queue        *healthQueueInfo
}

type healthProviderInfo struct {
	Type    string
	Status  string
	Circuit string
}

type healthCronSummary struct {
	Total   int
	Enabled int
	Running int
}

type healthQueueInfo struct {
	Pending int
	Max     int
}

func healthDeepCheck(input healthCheckInput) map[string]any {
	checks := map[string]any{}
	overall := "healthy"

	uptime := time.Since(input.StartTime)
	checks["uptime"] = map[string]any{
		"startedAt": input.StartTime.Format(time.RFC3339),
		"duration":  uptime.Round(time.Second).String(),
		"seconds":   int(uptime.Seconds()),
	}
	checks["version"] = input.Version

	if input.DBCheck != nil {
		dbStart := time.Now()
		count, err := input.DBCheck()
		dbLatency := time.Since(dbStart)
		if err != nil {
			checks["db"] = map[string]any{"status": "error", "error": err.Error(), "latencyMs": dbLatency.Milliseconds()}
			overall = healthDegradeStatus(overall, "unhealthy")
		} else {
			checks["db"] = map[string]any{"status": "ok", "path": input.DBPath, "latencyMs": dbLatency.Milliseconds(), "records": count}
		}
	} else {
		checks["db"] = map[string]any{"status": "disabled"}
	}

	providerChecks := map[string]any{}
	for name, pi := range input.Providers {
		pc := map[string]any{"status": pi.Status, "type": pi.Type}
		if pi.Circuit != "" {
			pc["circuit"] = pi.Circuit
		}
		if pi.Status == "open" || pi.Status == "recovering" {
			overall = healthDegradeStatus(overall, "degraded")
		}
		providerChecks[name] = pc
	}
	checks["providers"] = providerChecks

	if input.BaseDir != "" {
		di := healthDiskInfo(input.BaseDir)
		blockGB := 0.2
		if input.DiskBlockMB > 0 {
			blockGB = float64(input.DiskBlockMB) / 1024
		}
		warnGB := 0.5
		if input.DiskWarnMB > 0 {
			warnGB = float64(input.DiskWarnMB) / 1024
		} else if input.DiskBudgetGB > 0 {
			warnGB = input.DiskBudgetGB
		}
		if freeGB, ok := di["freeGB"].(float64); ok {
			switch {
			case freeGB < blockGB:
				di["status"] = "critical"
				di["warn"] = true
				overall = healthDegradeStatus(overall, "unhealthy")
			case freeGB < warnGB:
				di["status"] = "warning"
				di["warn"] = true
				overall = healthDegradeStatus(overall, "degraded")
			default:
				di["status"] = "ok"
			}
		}
		checks["disk"] = di
	}

	if input.DispatchJSON != nil {
		var dispatchInfo map[string]any
		json.Unmarshal(input.DispatchJSON, &dispatchInfo) //nolint:errcheck
		checks["dispatch"] = dispatchInfo
	}

	if input.Cron != nil {
		checks["cron"] = map[string]any{"jobs": input.Cron.Total, "enabled": input.Cron.Enabled, "running": input.Cron.Running}
	}

	if len(input.CircuitStatus) > 0 {
		checks["circuits"] = input.CircuitStatus
	}

	if input.Queue != nil {
		queueInfo := map[string]any{"status": "ok", "pending": input.Queue.Pending, "max": input.Queue.Max}
		if input.Queue.Pending > 0 {
			overall = healthDegradeStatus(overall, "degraded")
			queueInfo["status"] = "draining"
		}
		checks["queue"] = queueInfo
	}

	checks["status"] = overall
	return checks
}

func healthDegradeStatus(current, proposed string) string {
	ranks := map[string]int{"healthy": 0, "degraded": 1, "unhealthy": 2}
	if ranks[proposed] > ranks[current] {
		return proposed
	}
	return current
}

func healthDiskInfo(path string) map[string]any {
	info := map[string]any{"status": "ok"}

	var totalSize int64
	filepath.Walk(filepath.Join(path, "outputs"), func(_ string, fi os.FileInfo, _ error) error { //nolint:errcheck
		if fi != nil && !fi.IsDir() {
			totalSize += fi.Size()
		}
		return nil
	})
	info["outputsSizeMB"] = float64(totalSize) / (1024 * 1024)

	var logsSize int64
	filepath.Walk(filepath.Join(path, "logs"), func(_ string, fi os.FileInfo, _ error) error { //nolint:errcheck
		if fi != nil && !fi.IsDir() {
			logsSize += fi.Size()
		}
		return nil
	})
	info["logsSizeMB"] = float64(logsSize) / (1024 * 1024)

	if freeBytes := rootDiskFreeBytes(path); freeBytes > 0 {
		freeGB := float64(freeBytes) / (1024 * 1024 * 1024)
		info["freeGB"] = float64(int(freeGB*100)) / 100
	}

	return info
}
