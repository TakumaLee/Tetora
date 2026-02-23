package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func cmdStatus(args []string) {
	jsonOutput := false
	for _, a := range args {
		if a == "--json" {
			jsonOutput = true
		}
	}

	cfg := loadConfig(findConfigPath())
	api := newAPIClient(cfg)
	api.client.Timeout = 3 * time.Second

	if jsonOutput {
		cmdStatusJSON(cfg, api)
		return
	}

	fmt.Printf("tetora v%s\n\n", tetoraVersion)

	// 1. Daemon health.
	daemonOK := false
	resp, err := api.get("/healthz")
	if err != nil {
		fmt.Printf("  Daemon:   \033[31moffline\033[0m (%s)\n", cfg.ListenAddr)
	} else {
		defer resp.Body.Close()
		var health map[string]any
		json.NewDecoder(resp.Body).Decode(&health)
		daemonOK = true

		fmt.Printf("  Daemon:   \033[32mrunning\033[0m (%s)\n", cfg.ListenAddr)

		if cronData, ok := health["cron"].(map[string]any); ok {
			enabled := int(jsonFloatSafe(cronData["enabled"]))
			total := int(jsonFloatSafe(cronData["jobs"]))
			running := int(jsonFloatSafe(cronData["running"]))
			fmt.Printf("  Jobs:     %d enabled / %d total", enabled, total)
			if running > 0 {
				fmt.Printf(" (\033[33m%d running\033[0m)", running)
			}
			fmt.Println()
		}
	}

	// 2. Cost stats.
	if daemonOK {
		resp2, err := api.get("/stats/cost")
		if err == nil {
			defer resp2.Body.Close()
			var costData map[string]any
			if json.NewDecoder(resp2.Body).Decode(&costData) == nil {
				today := jsonFloatSafe(costData["today"])
				week := jsonFloatSafe(costData["week"])
				month := jsonFloatSafe(costData["month"])
				fmt.Printf("  Cost:     $%.2f today | $%.2f week | $%.2f month\n", today, week, month)

				// Budget warning.
				dailyLimit := jsonFloatSafe(costData["dailyLimit"])
				weeklyLimit := jsonFloatSafe(costData["weeklyLimit"])
				if dailyLimit > 0 && today >= dailyLimit*0.8 {
					pct := (today / dailyLimit) * 100
					color := "\033[33m" // yellow
					if today >= dailyLimit {
						color = "\033[31m" // red
					}
					fmt.Printf("  Budget:   %s%.0f%% of daily limit ($%.2f)\033[0m\n", color, pct, dailyLimit)
				}
				if weeklyLimit > 0 && week >= weeklyLimit*0.8 {
					pct := (week / weeklyLimit) * 100
					color := "\033[33m"
					if week >= weeklyLimit {
						color = "\033[31m"
					}
					fmt.Printf("  Budget:   %s%.0f%% of weekly limit ($%.2f)\033[0m\n", color, pct, weeklyLimit)
				}
			}
		}
	}

	// 3. Next scheduled job + failing jobs.
	if daemonOK {
		resp3, err := api.get("/cron")
		if err == nil {
			defer resp3.Body.Close()
			var jobs []CronJobInfo
			if json.NewDecoder(resp3.Body).Decode(&jobs) == nil {
				// Find next scheduled job.
				var nextJob *CronJobInfo
				for i, j := range jobs {
					if j.Enabled && !j.NextRun.IsZero() {
						if nextJob == nil || j.NextRun.Before(nextJob.NextRun) {
							nextJob = &jobs[i]
						}
					}
				}
				if nextJob != nil {
					until := time.Until(nextJob.NextRun)
					fmt.Printf("  Next:     %s in %s (%s)\n",
						nextJob.Name, formatDuration(until), nextJob.NextRun.Format("15:04"))
				}

				// Failing jobs warning.
				var failing []string
				for _, j := range jobs {
					if j.Errors > 0 {
						failing = append(failing, fmt.Sprintf("%s (x%d)", j.Name, j.Errors))
					}
				}
				if len(failing) > 0 {
					fmt.Printf("  \033[31mFailing:\033[0m  %s\n", joinStrings(failing, ", "))
				}
			}
		}
	}

	// 4. Last execution.
	if daemonOK {
		resp4, err := api.get("/history?limit=1")
		if err == nil {
			defer resp4.Body.Close()
			var histResp map[string]any
			if json.NewDecoder(resp4.Body).Decode(&histResp) == nil {
				if runsRaw, ok := histResp["runs"].([]any); ok && len(runsRaw) > 0 {
					if runMap, ok := runsRaw[0].(map[string]any); ok {
						status := jsonStrSafe(runMap["status"])
						name := jsonStrSafe(runMap["name"])
						cost := jsonFloatSafe(runMap["costUsd"])
						startedAt := jsonStrSafe(runMap["startedAt"])
						icon := "\033[32mOK\033[0m"
						if status != "success" {
							icon = fmt.Sprintf("\033[31m%s\033[0m", status)
						}
						ago := formatTimeAgo(startedAt)
						fmt.Printf("  Last:     %s %s ($%.2f) %s\n", icon, name, cost, ago)
					}
				}
			}
		}
	}

	// 5. Per-role cost (from /history job costs + config role mapping).
	if daemonOK {
		resp5, err := api.get("/cron")
		if err == nil {
			defer resp5.Body.Close()
			// We already fetched jobs above for next/failing, but let's get role costs differently.
			// Use the jobs info to map job->role, then show per-role costs.
		}
	}

	// 6. Quiet hours.
	if cfg.QuietHours.Enabled {
		if isQuietHours(cfg) {
			queued := quiet.queuedCount()
			fmt.Printf("  Quiet:    \033[33mactive\033[0m (%s - %s)", cfg.QuietHours.Start, cfg.QuietHours.End)
			if queued > 0 {
				fmt.Printf(", %d queued", queued)
			}
			fmt.Println()
		} else {
			fmt.Printf("  Quiet:    inactive (%s - %s)\n", cfg.QuietHours.Start, cfg.QuietHours.End)
		}
	}

	// 7. Service status.
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  Service:  installed\n")
	} else {
		fmt.Printf("  Service:  not installed\n")
	}
}

func cmdStatusJSON(cfg *Config, api *apiClient) {
	result := map[string]any{
		"version": tetoraVersion,
	}

	// Daemon health.
	resp, err := api.get("/healthz")
	if err != nil {
		result["daemon"] = "offline"
	} else {
		defer resp.Body.Close()
		var health map[string]any
		json.NewDecoder(resp.Body).Decode(&health)
		result["daemon"] = "running"
		result["cron"] = health["cron"]
	}

	// Cost.
	if result["daemon"] == "running" {
		resp2, err := api.get("/stats/cost")
		if err == nil {
			defer resp2.Body.Close()
			var costData map[string]any
			json.NewDecoder(resp2.Body).Decode(&costData)
			result["cost"] = costData
		}

		// Jobs.
		resp3, err := api.get("/cron")
		if err == nil {
			defer resp3.Body.Close()
			var jobs []CronJobInfo
			if json.NewDecoder(resp3.Body).Decode(&jobs) == nil {
				result["jobs"] = jobs

				// Next job.
				var nextJob *CronJobInfo
				for i, j := range jobs {
					if j.Enabled && !j.NextRun.IsZero() {
						if nextJob == nil || j.NextRun.Before(nextJob.NextRun) {
							nextJob = &jobs[i]
						}
					}
				}
				if nextJob != nil {
					result["nextJob"] = map[string]any{
						"id":      nextJob.ID,
						"name":    nextJob.Name,
						"nextRun": nextJob.NextRun,
					}
				}

				// Failing jobs.
				var failing []map[string]any
				for _, j := range jobs {
					if j.Errors > 0 {
						failing = append(failing, map[string]any{
							"id":     j.ID,
							"name":   j.Name,
							"errors": j.Errors,
						})
					}
				}
				if len(failing) > 0 {
					result["failingJobs"] = failing
				}
			}
		}
	}

	// Quiet hours.
	if cfg.QuietHours.Enabled {
		result["quietHours"] = map[string]any{
			"active":  isQuietHours(cfg),
			"start":   cfg.QuietHours.Start,
			"end":     cfg.QuietHours.End,
			"queued":  quiet.queuedCount(),
		}
	}

	// Service.
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		result["service"] = "installed"
	} else {
		result["service"] = "not installed"
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(result)
}

func jsonFloatSafe(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func jsonStrSafe(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func formatTimeAgo(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
