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

	// 1. Config
	if _, err := os.Stat(configPath); err != nil {
		check(false, "Config", fmt.Sprintf("not found at %s — run 'tetora init'", configPath))
		os.Exit(1)
	}
	check(true, "Config", configPath)

	cfg := loadConfig(configPath)

	// 2. Claude CLI
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

	// 3. Port availability
	ln, err := net.DialTimeout("tcp", cfg.ListenAddr, time.Second)
	if err != nil {
		check(true, "Port", fmt.Sprintf("%s available", cfg.ListenAddr))
	} else {
		ln.Close()
		check(true, "Port", fmt.Sprintf("%s in use (daemon running)", cfg.ListenAddr))
	}

	// 4. Telegram
	if cfg.Telegram.Enabled {
		if cfg.Telegram.BotToken != "" {
			check(true, "Telegram", fmt.Sprintf("enabled (chatID=%d)", cfg.Telegram.ChatID))
		} else {
			check(false, "Telegram", "enabled but no bot token")
			ok = false
		}
	} else {
		check(true, "Telegram", "disabled")
	}

	// 5. Jobs file
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

	// 6. Dashboard DB
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

	// 7. Workdir
	if cfg.DefaultWorkdir != "" {
		if _, err := os.Stat(cfg.DefaultWorkdir); err != nil {
			check(false, "Workdir", fmt.Sprintf("not found: %s", cfg.DefaultWorkdir))
			ok = false
		} else {
			check(true, "Workdir", cfg.DefaultWorkdir)
		}
	}

	// 8. Roles
	for name, rc := range cfg.Roles {
		path := rc.SoulFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(cfg.DefaultWorkdir, path)
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

	// 9. Binary location
	if exe, err := os.Executable(); err == nil {
		check(true, "Binary", exe)
	}

	fmt.Println()
	if ok {
		fmt.Println("All checks passed.")
	} else {
		fmt.Println("Some checks failed — see above.")
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
