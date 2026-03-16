package cli

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/db"
)

// CmdDoctor implements `tetora doctor`.
func CmdDoctor() {
	configPath := FindConfigPath()

	fmt.Println("=== Tetora Doctor ===")
	fmt.Println()

	ok := true
	var suggestions []string

	// 1. Config
	if _, err := os.Stat(configPath); err != nil {
		doctorCheck(false, "Config", fmt.Sprintf("not found at %s — run 'tetora init'", configPath))
		os.Exit(1)
	}
	doctorCheck(true, "Config", configPath)

	cfg := LoadCLIConfig(configPath)

	// 2. Claude CLI
	if cfg.ClaudePath != "" {
		if _, err := os.Stat(cfg.ClaudePath); err != nil {
			doctorCheck(false, "Claude CLI", fmt.Sprintf("%s not found", cfg.ClaudePath))
			ok = false
		} else {
			out, err := exec.Command(cfg.ClaudePath, "--version").CombinedOutput()
			if err != nil {
				doctorCheck(false, "Claude CLI", fmt.Sprintf("error: %v", err))
				ok = false
			} else {
				doctorCheck(true, "Claude CLI", strings.TrimSpace(string(out)))
			}
		}
	}

	// 3. Provider check
	hasProvider := cfg.ClaudePath != "" || cfg.DefaultProvider != "" || len(cfg.Providers) > 0
	if !hasProvider {
		doctorSuggest(false, "Provider", "no AI provider configured")
		suggestions = append(suggestions, "Add a provider: set claudePath, or configure providers in config.json")
	} else {
		if cfg.DefaultProvider != "" {
			doctorCheck(true, "Provider", cfg.DefaultProvider)
		} else if cfg.ClaudePath != "" {
			doctorCheck(true, "Provider", "Claude CLI")
		}
	}

	// 4. Port availability
	ln, err := net.DialTimeout("tcp", cfg.ListenAddr, time.Second)
	if err != nil {
		doctorCheck(true, "Port", fmt.Sprintf("%s available", cfg.ListenAddr))
	} else {
		ln.Close()
		doctorCheck(true, "Port", fmt.Sprintf("%s in use (daemon running)", cfg.ListenAddr))
	}

	// 5. Channels
	hasChannel := false
	if cfg.Telegram.Enabled {
		if cfg.Telegram.BotToken != "" {
			doctorCheck(true, "Telegram", "enabled")
			hasChannel = true
		} else {
			doctorCheck(false, "Telegram", "enabled but no bot token")
			ok = false
		}
	}
	if cfg.Discord.Enabled {
		doctorCheck(true, "Discord", "enabled")
		hasChannel = true
	}
	if cfg.Slack.Enabled {
		doctorCheck(true, "Slack", "enabled")
		hasChannel = true
	}
	if !hasChannel {
		doctorSuggest(false, "Channel", "no messaging channel enabled")
		suggestions = append(suggestions, "Enable a channel: telegram, discord, or slack in config.json")
	}

	// 6. Jobs file
	if _, err := os.Stat(cfg.JobsFile); err != nil {
		doctorCheck(false, "Jobs", fmt.Sprintf("not found: %s", cfg.JobsFile))
		ok = false
	} else {
		// TODO: requires root function newCronEngine — skip cron engine check, just report file exists.
		doctorCheck(true, "Jobs", fmt.Sprintf("%s (file ok; job parsing skipped in CLI mode)", cfg.JobsFile))
	}

	// 7. History DB tasks check
	if cfg.HistoryDB != "" {
		if _, err := os.Stat(cfg.HistoryDB); err != nil {
			doctorCheck(false, "History DB (tasks)", "not found")
		} else {
			stats, err := doctorGetTaskStats(cfg.HistoryDB)
			if err != nil {
				doctorCheck(false, "History DB (tasks)", fmt.Sprintf("error: %v", err))
			} else {
				doctorCheck(true, "History DB (tasks)", fmt.Sprintf("%d tasks", stats.Total))
			}
		}
	}

	// 8. Workdir
	if cfg.DefaultWorkdir != "" {
		if _, err := os.Stat(cfg.DefaultWorkdir); err != nil {
			doctorCheck(false, "Workdir", fmt.Sprintf("not found: %s", cfg.DefaultWorkdir))
			ok = false
		} else {
			doctorCheck(true, "Workdir", cfg.DefaultWorkdir)
		}
	}

	// 9. Agents
	for name, rc := range cfg.Agents {
		// Try new path first: agents/{name}/SOUL.md
		path := filepath.Join(cfg.AgentsDir, name, "SOUL.md")
		if _, err := os.Stat(path); err != nil {
			// Fallback: try soulFile field directly
			sf := rc.SoulFile
			if sf != "" {
				if !filepath.IsAbs(sf) {
					sf = filepath.Join(cfg.BaseDir, sf)
				}
				path = sf
			}
		}
		if _, err := os.Stat(path); err != nil {
			doctorCheck(false, "Agent/"+name, "soul file missing")
		} else {
			desc := rc.Description
			if desc == "" {
				desc = rc.Model
			}
			doctorCheck(true, "Agent/"+name, desc)
		}
	}

	// 10. Binary location
	if exe, err := os.Executable(); err == nil {
		doctorCheck(true, "Binary", exe)
	}

	// 11. Encryption key
	// TODO: requires root function resolveEncryptionKey — skip key check in CLI mode.
	suggestions = append(suggestions, "Set encryptionKey in config.json to encrypt sensitive DB fields (verification requires daemon)")

	// 12. ffmpeg (for audio_normalize)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		suggestions = append(suggestions, "Install ffmpeg for audio_normalize tool: brew install ffmpeg")
	} else {
		doctorCheck(true, "ffmpeg", "available")
	}

	// 13. sqlite3
	if _, err := exec.LookPath("sqlite3"); err != nil {
		doctorCheck(false, "sqlite3", "not found — required for DB operations")
		ok = false
	} else {
		doctorCheck(true, "sqlite3", "available")
	}

	// 14. Security scan tool
	if _, err := exec.LookPath("npx"); err == nil {
		suggestions = append(suggestions, "Security: run 'npx @nexylore/sentori scan .' for security audit")
	} else {
		suggestions = append(suggestions, "Install Node.js for security scanning with Sentori: npx @nexylore/sentori scan .")
	}

	// 15. New directory structure check
	if _, err := os.Stat(cfg.AgentsDir); err == nil {
		agentEntries, _ := os.ReadDir(cfg.AgentsDir)
		doctorCheck(true, "Agents Dir", fmt.Sprintf("%s (%d agents)", cfg.AgentsDir, len(agentEntries)))
	} else {
		doctorSuggest(false, "Agents Dir", fmt.Sprintf("not found: %s — run 'tetora init'", cfg.AgentsDir))
	}

	if _, err := os.Stat(cfg.WorkspaceDir); err == nil {
		doctorCheck(true, "Workspace", cfg.WorkspaceDir)
	} else {
		doctorSuggest(false, "Workspace", fmt.Sprintf("not found: %s — run 'tetora init'", cfg.WorkspaceDir))
	}

	fmt.Println()
	if ok && len(suggestions) == 0 {
		fmt.Println("All checks passed.")
	} else if ok {
		fmt.Println("All checks passed.")
		fmt.Println()
		fmt.Println("Suggestions:")
		for _, s := range suggestions {
			fmt.Printf("  -> %s\n", s)
		}
	} else {
		fmt.Println("Some checks failed — see above.")
		if len(suggestions) > 0 {
			fmt.Println()
			fmt.Println("Suggestions:")
			for _, s := range suggestions {
				fmt.Printf("  -> %s\n", s)
			}
		}
		os.Exit(1)
	}
}

func doctorCheck(ok bool, label, detail string) {
	icon := "\033[32m✓\033[0m"
	if !ok {
		icon = "\033[31m✗\033[0m"
	}
	fmt.Printf("  %s %-16s %s\n", icon, label, detail)
}

func doctorSuggest(ok bool, label, detail string) {
	icon := "\033[33m~\033[0m"
	if ok {
		icon = "\033[32m✓\033[0m"
	}
	fmt.Printf("  %s %-16s %s\n", icon, label, detail)
}

// doctorGetTaskStats returns aggregate task counts by status from the tasks table.
func doctorGetTaskStats(dbPath string) (db.TaskStats, error) {
	rows, err := db.Query(dbPath,
		`SELECT status, COUNT(*) as cnt FROM tasks GROUP BY status`)
	if err != nil {
		return db.TaskStats{}, err
	}

	var stats db.TaskStats
	for _, row := range rows {
		status := db.Str(row["status"])
		cnt := db.Int(row["cnt"])
		switch status {
		case "todo":
			stats.Todo = cnt
		case "doing", "running":
			stats.Running = cnt
		case "review":
			stats.Review = cnt
		case "done":
			stats.Done = cnt
		case "failed":
			stats.Failed = cnt
		}
		stats.Total += cnt
	}
	return stats, nil
}

// Ensure io is used (via io.Discard reference in original; kept for import satisfaction).
var _ io.Writer = io.Discard
