package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func cmdTrust(args []string) {
	if len(args) == 0 {
		args = []string{"show"}
	}

	switch args[0] {
	case "show":
		cmdTrustShow()
	case "set":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: tetora trust set <agent> <level>\n")
			fmt.Fprintf(os.Stderr, "Levels: %s\n", strings.Join(validTrustLevels, ", "))
			os.Exit(1)
		}
		cmdTrustSet(args[1], args[2])
	case "events":
		role := ""
		if len(args) > 1 {
			role = args[1]
		}
		cmdTrustEvents(role)
	default:
		fmt.Fprintf(os.Stderr, "Usage: tetora trust <show|set|events>\n")
		os.Exit(1)
	}
}

func cmdTrustShow() {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Try daemon API first.
	api := newAPIClient(cfg)
	resp, err := api.get("/trust")
	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var statuses []TrustStatus
		if json.Unmarshal(body, &statuses) == nil {
			printTrustStatuses(statuses)
			return
		}
	}

	// Fallback: query directly.
	statuses := getAllTrustStatuses(cfg)
	printTrustStatuses(statuses)
}

func printTrustStatuses(statuses []TrustStatus) {
	if len(statuses) == 0 {
		fmt.Println("No agents configured.")
		return
	}

	fmt.Printf("%-10s %-10s %-8s %-12s %s\n", "Agent", "Trust", "Streak", "Tasks", "Status")
	fmt.Println(strings.Repeat("-", 55))

	for _, s := range statuses {
		status := ""
		if s.PromoteReady {
			status = fmt.Sprintf("-> %s ready", s.NextLevel)
		}
		fmt.Printf("%-10s %-10s %-8d %-12d %s\n",
			s.Agent, s.Level, s.ConsecutiveSuccess, s.TotalTasks, status)
	}
}

func cmdTrustSet(role, level string) {
	if !isValidTrustLevel(level) {
		fmt.Fprintf(os.Stderr, "Error: invalid trust level %q (valid: %s)\n", level, strings.Join(validTrustLevels, ", "))
		os.Exit(1)
	}

	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Try daemon API first.
	api := newAPIClient(cfg)
	payload := fmt.Sprintf(`{"level":"%s"}`, level)
	resp, err := api.post("/trust/"+role, payload)
	if err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		fmt.Printf("Trust level for %q set to %q.\n", role, level)
		return
	}

	// Fallback: update config directly.
	if _, ok := cfg.Agents[role]; !ok {
		fmt.Fprintf(os.Stderr, "Error: agent %q not found\n", role)
		os.Exit(1)
	}

	oldLevel := resolveTrustLevel(cfg, role)
	configPath := filepath.Join(cfg.baseDir, "config.json")
	if err := saveAgentTrustLevel(configPath, role, level); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	recordTrustEvent(cfg.HistoryDB, role, "set", oldLevel, level, 0, "set via CLI")
	fmt.Printf("Trust level for %q set to %q (was %q).\n", role, level, oldLevel)
	fmt.Println("Note: restart the daemon for changes to take effect.")
}

func cmdTrustEvents(role string) {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Try daemon API first.
	api := newAPIClient(cfg)
	path := "/trust-events"
	if role != "" {
		path += "?role=" + role
	}
	resp, err := api.get(path)
	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var events []map[string]any
		if json.Unmarshal(body, &events) == nil {
			printTrustEvents(events)
			return
		}
	}

	// Fallback: query directly.
	events, err := queryTrustEvents(cfg.HistoryDB, role, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	printTrustEvents(events)
}

func printTrustEvents(events []map[string]any) {
	if len(events) == 0 {
		fmt.Println("No trust events.")
		return
	}

	fmt.Printf("%-10s %-16s %-12s %-12s %-6s %s\n", "Agent", "Event", "From", "To", "Streak", "Time")
	fmt.Println(strings.Repeat("-", 75))

	for _, e := range events {
		fmt.Printf("%-10s %-16s %-12s %-12s %-6v %s\n",
			jsonStr(e["role"]),
			jsonStr(e["event_type"]),
			jsonStr(e["from_level"]),
			jsonStr(e["to_level"]),
			e["consecutive_success"],
			jsonStr(e["created_at"]))
	}
}
