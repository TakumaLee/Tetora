package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func cmdDoctor() {
	configPath := findConfigPath()

	fmt.Println("=== Tetora Doctor ===")
	fmt.Println()

	ok := true
	var suggestions []string

	// 1. Config
	if _, err := os.Stat(configPath); err != nil {
		check(false, "Config", fmt.Sprintf("not found at %s — run 'tetora init'", configPath))
		os.Exit(1)
	}
	check(true, "Config", configPath)

	cfg := loadConfig(configPath)

	// 2. Claude CLI
	if cfg.ClaudePath != "" {
		if _, err := os.Stat(cfg.ClaudePath); err != nil {
			check(false, "Claude CLI", fmt.Sprintf("%s not found", cfg.ClaudePath))
			ok = false
		} else {
			out, err := exec.Command(cfg.ClaudePath, "--version").CombinedOutput()
			if err != nil {
				check(false, "Claude CLI", fmt.Sprintf("error: %v", err))
				ok = false
			} else {
				check(true, "Claude CLI", strings.TrimSpace(string(out)))
			}
		}
	}

	// 3. Provider check
	hasProvider := cfg.ClaudePath != "" || cfg.DefaultProvider != "" || len(cfg.Providers) > 0
	if !hasProvider {
		suggest(false, "Provider", "no AI provider configured")
		suggestions = append(suggestions, "Add a provider: set claudePath, or configure providers in config.json")
	} else {
		if cfg.DefaultProvider != "" {
			check(true, "Provider", cfg.DefaultProvider)
		} else if cfg.ClaudePath != "" {
			check(true, "Provider", "Claude CLI")
		}
	}

	// 4. Port availability
	ln, err := net.DialTimeout("tcp", cfg.ListenAddr, time.Second)
	if err != nil {
		check(true, "Port", fmt.Sprintf("%s available", cfg.ListenAddr))
	} else {
		ln.Close()
		check(true, "Port", fmt.Sprintf("%s in use (daemon running)", cfg.ListenAddr))
	}

	// 5. Channels
	hasChannel := false
	if cfg.Telegram.Enabled {
		if cfg.Telegram.BotToken != "" {
			check(true, "Telegram", fmt.Sprintf("enabled (chatID=%d)", cfg.Telegram.ChatID))
			hasChannel = true
		} else {
			check(false, "Telegram", "enabled but no bot token")
			ok = false
		}
	}
	if cfg.Discord.Enabled {
		check(true, "Discord", "enabled")
		hasChannel = true
	}
	if cfg.Slack.Enabled {
		check(true, "Slack", "enabled")
		hasChannel = true
	}
	if !hasChannel {
		suggest(false, "Channel", "no messaging channel enabled")
		suggestions = append(suggestions, "Enable a channel: telegram, discord, or slack in config.json")
	}

	// 6. Jobs file
	if _, err := os.Stat(cfg.JobsFile); err != nil {
		check(false, "Jobs", fmt.Sprintf("not found: %s", cfg.JobsFile))
		ok = false
	} else {
		origLog := log.Writer()
		log.SetOutput(io.Discard)
		ce := newCronEngine(cfg, make(chan struct{}, 1), nil)
		err := ce.loadJobs()
		log.SetOutput(origLog)
		if err != nil {
			check(false, "Jobs", fmt.Sprintf("parse error: %v", err))
			ok = false
		} else {
			enabled := ce.countEnabled()
			check(true, "Jobs", fmt.Sprintf("%d jobs (%d enabled)", len(ce.jobs), enabled))
		}
	}

	// 7. Dashboard DB
	if cfg.DashboardDB != "" {
		if _, err := os.Stat(cfg.DashboardDB); err != nil {
			check(false, "Dashboard DB", "not found")
		} else {
			stats, err := getTaskStats(cfg.DashboardDB)
			if err != nil {
				check(false, "Dashboard DB", fmt.Sprintf("error: %v", err))
			} else {
				check(true, "Dashboard DB", fmt.Sprintf("%d tasks", stats.Total))
			}
		}
	}

	// 8. Workdir
	if cfg.DefaultWorkdir != "" {
		if _, err := os.Stat(cfg.DefaultWorkdir); err != nil {
			check(false, "Workdir", fmt.Sprintf("not found: %s", cfg.DefaultWorkdir))
			ok = false
		} else {
			check(true, "Workdir", cfg.DefaultWorkdir)
		}
	}

	// 9. Roles
	for name, rc := range cfg.Roles {
		// Try new path first: agents/{name}/SOUL.md
		path := filepath.Join(cfg.AgentsDir, name, "SOUL.md")
		if _, err := os.Stat(path); err != nil {
			// Fallback: try workspace-resolved path
			ws := resolveWorkspace(cfg, name)
			path = ws.SoulFile
			if _, err := os.Stat(path); err != nil {
				// Legacy fallback
				path = rc.SoulFile
				if !filepath.IsAbs(path) {
					path = filepath.Join(cfg.DefaultWorkdir, path)
				}
			}
		}
		if _, err := os.Stat(path); err != nil {
			check(false, "Role/"+name, "soul file missing")
		} else {
			desc := rc.Description
			if desc == "" {
				desc = rc.Model
			}
			check(true, "Role/"+name, desc)
		}
	}

	// 10. Binary location
	if exe, err := os.Executable(); err == nil {
		check(true, "Binary", exe)
	}

	// 11. Encryption key
	if resolveEncryptionKey(cfg) == "" {
		suggestions = append(suggestions, "Set encryptionKey in config.json to encrypt sensitive DB fields")
	} else {
		check(true, "Encryption", "key configured")
	}

	// 12. ffmpeg (for audio_normalize)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		suggestions = append(suggestions, "Install ffmpeg for audio_normalize tool: brew install ffmpeg")
	} else {
		check(true, "ffmpeg", "available")
	}

	// 13. sqlite3
	if _, err := exec.LookPath("sqlite3"); err != nil {
		check(false, "sqlite3", "not found — required for DB operations")
		ok = false
	} else {
		check(true, "sqlite3", "available")
	}

	// 14. Security scan tool
	if _, err := exec.LookPath("npx"); err == nil {
		suggestions = append(suggestions, "Security: run 'npx @nexylore/sentori scan .' for security audit")
	} else {
		suggestions = append(suggestions, "Install Node.js for security scanning with Sentori: npx @nexylore/sentori scan .")
	}

	// 15. New directory structure check
	if _, err := os.Stat(filepath.Join(cfg.AgentsDir)); err == nil {
		agentEntries, _ := os.ReadDir(cfg.AgentsDir)
		check(true, "Agents Dir", fmt.Sprintf("%s (%d agents)", cfg.AgentsDir, len(agentEntries)))
	} else {
		suggest(false, "Agents Dir", fmt.Sprintf("not found: %s — run 'tetora init'", cfg.AgentsDir))
	}

	if _, err := os.Stat(cfg.WorkspaceDir); err == nil {
		check(true, "Workspace", cfg.WorkspaceDir)
	} else {
		suggest(false, "Workspace", fmt.Sprintf("not found: %s — run 'tetora init'", cfg.WorkspaceDir))
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

func check(ok bool, label, detail string) {
	icon := "\033[32m✓\033[0m"
	if !ok {
		icon = "\033[31m✗\033[0m"
	}
	fmt.Printf("  %s %-16s %s\n", icon, label, detail)
}

func suggest(ok bool, label, detail string) {
	icon := "\033[33m~\033[0m"
	if ok {
		icon = "\033[32m✓\033[0m"
	}
	fmt.Printf("  %s %-16s %s\n", icon, label, detail)
}
