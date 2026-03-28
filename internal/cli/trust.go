package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tetora/internal/db"
)

// --- Trust Level Constants ---

const (
	TrustObserve = "observe"
	TrustSuggest = "suggest"
	TrustAuto    = "auto"
)

var validTrustLevels = []string{TrustObserve, TrustSuggest, TrustAuto}

// TrustStatus holds the trust state for a single agent.
type TrustStatus struct {
	Agent              string `json:"agent"`
	Level              string `json:"level"`
	ConsecutiveSuccess int    `json:"consecutiveSuccess"`
	PromoteReady       bool   `json:"promoteReady"`
	NextLevel          string `json:"nextLevel,omitempty"`
	TotalTasks         int    `json:"totalTasks"`
	LastUpdated        string `json:"lastUpdated,omitempty"`
}

func CmdTrust(args []string) {
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
	cfg := LoadCLIConfig("")

	// Try daemon API first.
	api := cfg.NewAPIClient()
	resp, err := api.Get("/trust")
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

	cfg := LoadCLIConfig("")

	// Try daemon API first.
	api := cfg.NewAPIClient()
	payload := fmt.Sprintf(`{"level":"%s"}`, level)
	resp, err := api.Post("/trust/"+role, payload)
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

	oldLevel := resolveTrustLevelCLI(cfg, role)
	configPath := filepath.Join(cfg.BaseDir, "config.json")
	if err := saveAgentTrustLevel(configPath, role, level); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	recordTrustEvent(cfg.HistoryDB, role, "set", oldLevel, level, 0, "set via CLI")
	fmt.Printf("Trust level for %q set to %q (was %q).\n", role, level, oldLevel)
	fmt.Println("Note: restart the daemon for changes to take effect.")
}

func cmdTrustEvents(role string) {
	cfg := LoadCLIConfig("")

	// Try daemon API first.
	api := cfg.NewAPIClient()
	path := "/trust-events"
	if role != "" {
		path += "?role=" + role
	}
	resp, err := api.Get(path)
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
			JSONStrSafe(e["role"]),
			JSONStrSafe(e["event_type"]),
			JSONStrSafe(e["from_level"]),
			JSONStrSafe(e["to_level"]),
			e["consecutive_success"],
			JSONStrSafe(e["created_at"]))
	}
}

// --- Trust operations (replicated from root trust.go using db package) ---

func isValidTrustLevel(level string) bool {
	for _, v := range validTrustLevels {
		if v == level {
			return true
		}
	}
	return false
}

func nextTrustLevelCLI(current string) string {
	for i, v := range validTrustLevels {
		if v == current && i < len(validTrustLevels)-1 {
			return validTrustLevels[i+1]
		}
	}
	return ""
}

func resolveTrustLevelCLI(cfg *CLIConfig, agentName string) string {
	if agentName == "" {
		return TrustAuto
	}
	if rc, ok := cfg.Agents[agentName]; ok && rc.TrustLevel != "" {
		if isValidTrustLevel(rc.TrustLevel) {
			return rc.TrustLevel
		}
	}
	return TrustAuto
}

func queryConsecutiveSuccessCLI(dbPath, role string) int {
	if dbPath == "" || role == "" {
		return 0
	}
	sql := fmt.Sprintf(
		`SELECT status FROM job_runs WHERE agent = '%s' ORDER BY id DESC LIMIT 50`,
		db.Escape(role))
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return 0
	}
	count := 0
	for _, r := range rows {
		if JSONStrSafe(r["status"]) == "success" {
			count++
		} else {
			break
		}
	}
	return count
}

func recordTrustEvent(dbPath, role, eventType, fromLevel, toLevel string, consecutiveSuccess int, note string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT INTO trust_events (agent, event_type, from_level, to_level, consecutive_success, created_at, note)
		 VALUES ('%s', '%s', '%s', '%s', %d, datetime('now'), '%s')`,
		db.Escape(role),
		db.Escape(eventType),
		db.Escape(fromLevel),
		db.Escape(toLevel),
		consecutiveSuccess,
		db.Escape(note))
	db.Exec(dbPath, sql) //nolint:errcheck
}

func queryTrustEvents(dbPath, role string, limit int) ([]map[string]any, error) {
	if dbPath == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	where := ""
	if role != "" {
		where = fmt.Sprintf("WHERE agent = '%s'", db.Escape(role))
	}
	sql := fmt.Sprintf(
		`SELECT agent, event_type, from_level, to_level, consecutive_success, created_at, note
		 FROM trust_events %s ORDER BY id DESC LIMIT %d`, where, limit)
	return db.Query(dbPath, sql)
}

func getAllTrustStatuses(cfg *CLIConfig) []TrustStatus {
	roles := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		roles = append(roles, name)
	}
	sort.Strings(roles)

	statuses := make([]TrustStatus, 0, len(roles))
	for _, role := range roles {
		level := resolveTrustLevelCLI(cfg, role)
		consecutiveSuccess := queryConsecutiveSuccessCLI(cfg.HistoryDB, role)
		next := nextTrustLevelCLI(level)
		promoteReady := next != "" && consecutiveSuccess >= 10

		totalTasks := 0
		if cfg.HistoryDB != "" {
			sql := fmt.Sprintf(`SELECT COUNT(*) as cnt FROM job_runs WHERE agent = '%s'`, db.Escape(role))
			if rows, err := db.Query(cfg.HistoryDB, sql); err == nil && len(rows) > 0 {
				if f, ok := rows[0]["cnt"].(float64); ok {
					totalTasks = int(f)
				}
			}
		}

		lastUpdated := ""
		if events, err := queryTrustEvents(cfg.HistoryDB, role, 1); err == nil && len(events) > 0 {
			lastUpdated = JSONStrSafe(events[0]["created_at"])
		}

		statuses = append(statuses, TrustStatus{
			Agent:              role,
			Level:              level,
			ConsecutiveSuccess: consecutiveSuccess,
			PromoteReady:       promoteReady,
			NextLevel:          next,
			TotalTasks:         totalTasks,
			LastUpdated:        lastUpdated,
		})
	}
	return statuses
}

func saveAgentTrustLevel(configPath, agentName, newLevel string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	agents, ok := raw["agents"].(map[string]any)
	if !ok {
		agents, ok = raw["roles"].(map[string]any)
		if !ok {
			return fmt.Errorf("agents not found in config")
		}
	}
	rc, ok := agents[agentName].(map[string]any)
	if !ok {
		return fmt.Errorf("agent %q not found", agentName)
	}
	rc["trustLevel"] = newLevel

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}
