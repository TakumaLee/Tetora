package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tetora/internal/quiet"
)

func CmdStatus(args []string) {
	jsonOutput := false
	for _, a := range args {
		if a == "--json" {
			jsonOutput = true
		}
	}

	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()
	api.Client.Timeout = 3 * time.Second

	if jsonOutput {
		cmdStatusJSON(cfg, api)
		return
	}

	fmt.Printf("tetora v%s\n\n", TetoraVersion)

	// 1. Daemon health.
	daemonOK := false
	resp, err := api.Get("/healthz")
	if err != nil {
		fmt.Printf("  Daemon:   \033[31moffline\033[0m (%s)\n", cfg.ListenAddr)
	} else {
		defer resp.Body.Close()
		var health map[string]any
		json.NewDecoder(resp.Body).Decode(&health)
		daemonOK = true

		fmt.Printf("  Daemon:   \033[32mrunning\033[0m (%s)\n", cfg.ListenAddr)

		if cronData, ok := health["cron"].(map[string]any); ok {
			enabled := int(JSONFloatSafe(cronData["enabled"]))
			total := int(JSONFloatSafe(cronData["jobs"]))
			running := int(JSONFloatSafe(cronData["running"]))
			fmt.Printf("  Jobs:     %d enabled / %d total", enabled, total)
			if running > 0 {
				fmt.Printf(" (\033[33m%d running\033[0m)", running)
			}
			fmt.Println()
		}
	}

	// 2. Cost stats.
	if daemonOK {
		resp2, err := api.Get("/stats/cost")
		if err == nil {
			defer resp2.Body.Close()
			var costData map[string]any
			if json.NewDecoder(resp2.Body).Decode(&costData) == nil {
				today := JSONFloatSafe(costData["today"])
				week := JSONFloatSafe(costData["week"])
				month := JSONFloatSafe(costData["month"])
				fmt.Printf("  Cost:     $%.2f today | $%.2f week | $%.2f month\n", today, week, month)

				// Budget warning.
				dailyLimit := JSONFloatSafe(costData["dailyLimit"])
				weeklyLimit := JSONFloatSafe(costData["weeklyLimit"])
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
		resp3, err := api.Get("/cron")
		if err == nil {
			defer resp3.Body.Close()
			var jobs []map[string]any
			if json.NewDecoder(resp3.Body).Decode(&jobs) == nil {
				// Find next scheduled job.
				var nextJobName string
				var nextJobNextRun time.Time
				for _, j := range jobs {
					if enabled, _ := j["enabled"].(bool); !enabled {
						continue
					}
					nextRunStr := JSONStrSafe(j["nextRun"])
					if nextRunStr == "" {
						continue
					}
					t, err := time.Parse(time.RFC3339, nextRunStr)
					if err != nil {
						continue
					}
					if nextJobNextRun.IsZero() || t.Before(nextJobNextRun) {
						nextJobNextRun = t
						nextJobName = JSONStrSafe(j["name"])
					}
				}
				if !nextJobNextRun.IsZero() {
					until := time.Until(nextJobNextRun)
					fmt.Printf("  Next:     %s in %s (%s)\n",
						nextJobName, FormatDuration(until), nextJobNextRun.Format("15:04"))
				}

				// Failing jobs warning.
				var failing []string
				for _, j := range jobs {
					errors := int(JSONFloatSafe(j["errors"]))
					if errors > 0 {
						name := JSONStrSafe(j["name"])
						failing = append(failing, fmt.Sprintf("%s (x%d)", name, errors))
					}
				}
				if len(failing) > 0 {
					fmt.Printf("  \033[31mFailing:\033[0m  %s\n", JoinStrings(failing, ", "))
				}
			}
		}
	}

	// 4. Last execution.
	if daemonOK {
		resp4, err := api.Get("/history?limit=1")
		if err == nil {
			defer resp4.Body.Close()
			var histResp map[string]any
			if json.NewDecoder(resp4.Body).Decode(&histResp) == nil {
				if runsRaw, ok := histResp["runs"].([]any); ok && len(runsRaw) > 0 {
					if runMap, ok := runsRaw[0].(map[string]any); ok {
						status := JSONStrSafe(runMap["status"])
						name := JSONStrSafe(runMap["name"])
						cost := JSONFloatSafe(runMap["costUsd"])
						startedAt := JSONStrSafe(runMap["startedAt"])
						icon := "\033[32mOK\033[0m"
						if status != "success" {
							icon = fmt.Sprintf("\033[31m%s\033[0m", status)
						}
						ago := FormatTimeAgo(startedAt)
						fmt.Printf("  Last:     %s %s ($%.2f) %s\n", icon, name, cost, ago)
					}
				}
			}
		}
	}

	// 6. Quiet hours.
	if cfg.QuietHours.Enabled {
		qcfg := quiet.Config{
			Enabled: cfg.QuietHours.Enabled,
			Start:   cfg.QuietHours.Start,
			End:     cfg.QuietHours.End,
			TZ:      cfg.QuietHours.TZ,
			Digest:  cfg.QuietHours.Digest,
		}
		if quiet.IsQuietHours(qcfg) {
			// In CLI context the quiet queue is not accessible; omit queued count.
			fmt.Printf("  Quiet:    \033[33mactive\033[0m (%s - %s)\n", cfg.QuietHours.Start, cfg.QuietHours.End)
		} else {
			fmt.Printf("  Quiet:    inactive (%s - %s)\n", cfg.QuietHours.Start, cfg.QuietHours.End)
		}
	}

	// 7. Service status.
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", PlistLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  Service:  installed\n")
	} else {
		fmt.Printf("  Service:  not installed\n")
	}
}

func cmdStatusJSON(cfg *CLIConfig, api *APIClient) {
	result := map[string]any{
		"version": TetoraVersion,
	}

	// Daemon health.
	resp, err := api.Get("/healthz")
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
		resp2, err := api.Get("/stats/cost")
		if err == nil {
			defer resp2.Body.Close()
			var costData map[string]any
			json.NewDecoder(resp2.Body).Decode(&costData)
			result["cost"] = costData
		}

		// Jobs.
		resp3, err := api.Get("/cron")
		if err == nil {
			defer resp3.Body.Close()
			var jobs []map[string]any
			if json.NewDecoder(resp3.Body).Decode(&jobs) == nil {
				result["jobs"] = jobs

				// Next job.
				var nextJobName, nextJobID string
				var nextJobNextRun time.Time
				for _, j := range jobs {
					if enabled, _ := j["enabled"].(bool); !enabled {
						continue
					}
					nextRunStr := JSONStrSafe(j["nextRun"])
					if nextRunStr == "" {
						continue
					}
					t, err := time.Parse(time.RFC3339, nextRunStr)
					if err != nil {
						continue
					}
					if nextJobNextRun.IsZero() || t.Before(nextJobNextRun) {
						nextJobNextRun = t
						nextJobName = JSONStrSafe(j["name"])
						nextJobID = JSONStrSafe(j["id"])
					}
				}
				if !nextJobNextRun.IsZero() {
					result["nextJob"] = map[string]any{
						"id":      nextJobID,
						"name":    nextJobName,
						"nextRun": nextJobNextRun,
					}
				}

				// Failing jobs.
				var failing []map[string]any
				for _, j := range jobs {
					errors := int(JSONFloatSafe(j["errors"]))
					if errors > 0 {
						failing = append(failing, map[string]any{
							"id":     JSONStrSafe(j["id"]),
							"name":   JSONStrSafe(j["name"]),
							"errors": errors,
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
		qcfg := quiet.Config{
			Enabled: cfg.QuietHours.Enabled,
			Start:   cfg.QuietHours.Start,
			End:     cfg.QuietHours.End,
			TZ:      cfg.QuietHours.TZ,
			Digest:  cfg.QuietHours.Digest,
		}
		// In CLI context the quiet queue is not accessible; queued is always 0.
		result["quietHours"] = map[string]any{
			"active": quiet.IsQuietHours(qcfg),
			"start":  cfg.QuietHours.Start,
			"end":    cfg.QuietHours.End,
			"queued": 0,
		}
	}

	// Service.
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", PlistLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		result["service"] = "installed"
	} else {
		result["service"] = "not installed"
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(result)
}
