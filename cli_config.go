package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func cmdConfig(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora config <show|set|validate|migrate|history|rollback|diff|snapshot|show-version|versions>")
		return
	}
	// Try version-related subcommands first.
	if handleConfigVersionSubcommands(args[0], args[1:]) {
		return
	}
	switch args[0] {
	case "show":
		configShow()
	case "set":
		if len(args) < 3 {
			fmt.Println("Usage: tetora config set <key> <value>")
			return
		}
		configSet(args[1], strings.Join(args[2:], " "))
	case "validate":
		configValidate()
	case "migrate":
		configMigrate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
	}
}

// configMigrate runs config migrations manually.
func configMigrate(args []string) {
	dryRun := false
	for _, a := range args {
		if a == "--dry-run" || a == "-n" {
			dryRun = true
		}
	}

	configPath := findConfigPath()
	fmt.Printf("Config: %s\n", configPath)

	// Show current version.
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config: %v\n", err)
		os.Exit(1)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config: %v\n", err)
		os.Exit(1)
	}
	currentVer := getConfigVersion(raw)
	fmt.Printf("Current version: %d\n", currentVer)
	fmt.Printf("Target version:  %d\n", currentConfigVersion)

	if currentVer >= currentConfigVersion {
		fmt.Println("Config is already up to date.")
		return
	}

	if dryRun {
		fmt.Println("\n[dry-run] Migrations that would be applied:")
	} else {
		fmt.Println("\nApplying migrations:")
	}

	applied, err := migrateConfig(configPath, dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	for _, desc := range applied {
		fmt.Printf("  - %s\n", desc)
	}

	if len(applied) == 0 {
		fmt.Println("  (no migrations needed)")
	} else if dryRun {
		fmt.Println("\nRun without --dry-run to apply.")
	} else {
		fmt.Printf("\nConfig migrated to version %d.\n", currentConfigVersion)
	}
}

// configShow prints config with secrets masked.
func configShow() {
	configPath := findConfigPath()
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config: %v\n", err)
		os.Exit(1)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config: %v\n", err)
		os.Exit(1)
	}

	// Mask secret fields.
	maskSecrets(raw)

	out, _ := json.MarshalIndent(raw, "", "  ")
	fmt.Println(string(out))
}

// maskSecrets replaces known secret values with "***".
func maskSecrets(m map[string]any) {
	secretKeys := []string{"apiToken", "botToken", "password", "token", "apiKey"}
	for k, v := range m {
		// Check if this key is a secret.
		for _, sk := range secretKeys {
			if k == sk {
				if s, ok := v.(string); ok && s != "" {
					m[k] = "***"
				}
			}
		}
		// Recurse into nested objects.
		if sub, ok := v.(map[string]any); ok {
			maskSecrets(sub)
		}
		// Recurse into arrays of objects.
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				if sub, ok := item.(map[string]any); ok {
					maskSecrets(sub)
				}
			}
		}
	}
}

// configSet updates a single config field using dot-path notation.
func configSet(key, value string) {
	configPath := findConfigPath()
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config: %v\n", err)
		os.Exit(1)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config: %v\n", err)
		os.Exit(1)
	}

	// Parse value to appropriate type.
	var parsed any
	if v, err := strconv.ParseFloat(value, 64); err == nil {
		// Check if it's actually an integer.
		if v == float64(int64(v)) {
			parsed = int64(v)
		} else {
			parsed = v
		}
	} else if value == "true" {
		parsed = true
	} else if value == "false" {
		parsed = false
	} else {
		parsed = value
	}

	// Navigate dot path and set value.
	parts := strings.Split(key, ".")
	target := raw
	for i := 0; i < len(parts)-1; i++ {
		sub, ok := target[parts[i]]
		if !ok {
			// Create intermediate object.
			newMap := make(map[string]any)
			target[parts[i]] = newMap
			target = newMap
			continue
		}
		subMap, ok := sub.(map[string]any)
		if !ok {
			fmt.Fprintf(os.Stderr, "Cannot traverse %q: not an object\n", strings.Join(parts[:i+1], "."))
			os.Exit(1)
		}
		target = subMap
	}

	target[parts[len(parts)-1]] = parsed

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding config: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
		os.Exit(1)
	}

	// Auto-snapshot config version.
	cfg := loadConfig(configPath)
	snapshotConfig(cfg.HistoryDB, configPath, "cli", fmt.Sprintf("set %s", key))

	fmt.Printf("Updated %s = %v\n", key, parsed)
}

// configValidate checks config and jobs for common issues.
func configValidate() {
	configPath := findConfigPath()
	cfg := loadConfig(configPath)
	errors := 0
	warnings := 0

	check := func(ok bool, level, msg string) {
		if ok {
			fmt.Printf("  OK    %s\n", msg)
		} else if level == "ERROR" {
			fmt.Printf("  ERROR %s\n", msg)
			errors++
		} else {
			fmt.Printf("  WARN  %s\n", msg)
			warnings++
		}
	}

	fmt.Println("=== Config Validation ===")
	fmt.Println()

	// Claude binary.
	claudePath := cfg.ClaudePath
	if claudePath == "" {
		claudePath = "claude"
	}
	_, err := exec.LookPath(claudePath)
	check(err == nil, "ERROR", fmt.Sprintf("claude binary: %s", claudePath))

	// Listen address.
	check(cfg.ListenAddr != "", "ERROR", fmt.Sprintf("listenAddr: %s", cfg.ListenAddr))

	// API token.
	check(cfg.APIToken != "", "WARN", "apiToken configured")

	// History DB.
	check(cfg.HistoryDB != "", "WARN", fmt.Sprintf("historyDB: %s", cfg.HistoryDB))

	// Default workdir.
	if cfg.DefaultWorkdir != "" {
		_, err := os.Stat(cfg.DefaultWorkdir)
		check(err == nil, "WARN", fmt.Sprintf("defaultWorkdir exists: %s", cfg.DefaultWorkdir))
	}

	// Telegram.
	if cfg.Telegram.Enabled {
		check(cfg.Telegram.BotToken != "", "ERROR", "telegram.botToken set")
		check(cfg.Telegram.ChatID != 0, "ERROR", "telegram.chatID set")
	}

	// Dashboard auth.
	if cfg.DashboardAuth.Enabled {
		hasCreds := (cfg.DashboardAuth.Password != "") || (cfg.DashboardAuth.Token != "")
		check(hasCreds, "ERROR", "dashboardAuth credentials set")
	}

	// Agents — check soul files exist.
	fmt.Println()
	fmt.Println("=== Agents ===")
	for name, rc := range cfg.Agents {
		if rc.SoulFile != "" {
			path := rc.SoulFile
			if !filepath.IsAbs(path) {
				path = filepath.Join(cfg.DefaultWorkdir, path)
			}
			_, err := os.Stat(path)
			check(err == nil, "WARN", fmt.Sprintf("agent %q soul file: %s", name, rc.SoulFile))
		} else {
			fmt.Printf("  OK    agent %q (no soul file)\n", name)
		}
	}
	if len(cfg.Agents) == 0 {
		fmt.Println("  (no agents configured)")
	}

	// Jobs — validate cron expressions.
	fmt.Println()
	fmt.Println("=== Cron Jobs ===")
	data, err := os.ReadFile(cfg.JobsFile)
	if err != nil {
		fmt.Printf("  WARN  cannot read jobs file: %s\n", cfg.JobsFile)
		warnings++
	} else {
		var jf JobsFile
		if err := json.Unmarshal(data, &jf); err != nil {
			fmt.Printf("  ERROR invalid jobs JSON: %v\n", err)
			errors++
		} else {
			for _, j := range jf.Jobs {
				_, err := parseCronExpr(j.Schedule)
				check(err == nil, "ERROR", fmt.Sprintf("job %q schedule: %s", j.ID, j.Schedule))

				// Check agent exists if specified.
				if j.Agent != "" {
					_, ok := cfg.Agents[j.Agent]
					check(ok, "WARN", fmt.Sprintf("job %q agent %q exists", j.ID, j.Agent))
				}
			}
			if len(jf.Jobs) == 0 {
				fmt.Println("  (no jobs configured)")
			}
		}
	}

	// Security.
	fmt.Println()
	fmt.Println("=== Security ===")

	// Rate limit.
	if cfg.RateLimit.Enabled {
		check(cfg.RateLimit.MaxPerMin > 0, "WARN",
			fmt.Sprintf("rateLimit: %d req/min", cfg.RateLimit.MaxPerMin))
	} else {
		fmt.Println("  --    rate limiting disabled")
	}

	// IP allowlist.
	if len(cfg.AllowedIPs) > 0 {
		allValid := true
		for _, entry := range cfg.AllowedIPs {
			entry = strings.TrimSpace(entry)
			if strings.Contains(entry, "/") {
				if _, _, err := net.ParseCIDR(entry); err != nil {
					allValid = false
				}
			} else {
				if net.ParseIP(entry) == nil {
					allValid = false
				}
			}
		}
		check(allValid, "ERROR",
			fmt.Sprintf("allowedIPs: %d entries", len(cfg.AllowedIPs)))
	} else {
		fmt.Println("  --    IP allowlist disabled (all IPs allowed)")
	}

	// TLS.
	if cfg.tlsEnabled {
		_, certErr := os.Stat(cfg.TLS.CertFile)
		check(certErr == nil, "ERROR", fmt.Sprintf("tls.certFile: %s", cfg.TLS.CertFile))
		_, keyErr := os.Stat(cfg.TLS.KeyFile)
		check(keyErr == nil, "ERROR", fmt.Sprintf("tls.keyFile: %s", cfg.TLS.KeyFile))
	} else {
		fmt.Println("  --    TLS disabled (HTTP only)")
	}

	// Security alerts.
	if cfg.SecurityAlert.Enabled {
		check(cfg.SecurityAlert.FailThreshold > 0, "WARN",
			fmt.Sprintf("securityAlert: threshold=%d, window=%dm",
				cfg.SecurityAlert.FailThreshold, cfg.SecurityAlert.FailWindowMin))
	} else {
		fmt.Println("  --    security alerts disabled")
	}

	// Providers.
	if len(cfg.Providers) > 0 {
		fmt.Println()
		fmt.Println("=== Providers ===")
		for name, pc := range cfg.Providers {
			switch pc.Type {
			case "claude-cli":
				path := pc.Path
				if path == "" {
					path = cfg.ClaudePath
				}
				if path == "" {
					path = "claude"
				}
				_, err := exec.LookPath(path)
				check(err == nil, "WARN", fmt.Sprintf("provider %q (%s): %s", name, pc.Type, path))
			case "openai-compatible":
				hasURL := strings.HasPrefix(pc.BaseURL, "http://") || strings.HasPrefix(pc.BaseURL, "https://")
				check(hasURL, "ERROR", fmt.Sprintf("provider %q baseUrl: %s", name, pc.BaseURL))
				hasModel := pc.Model != ""
				check(hasModel, "WARN", fmt.Sprintf("provider %q default model", name))
			default:
				fmt.Printf("  ERROR provider %q: unknown type %q\n", name, pc.Type)
				errors++
			}
		}
		if cfg.DefaultProvider != "" {
			_, exists := cfg.Providers[cfg.DefaultProvider]
			check(exists, "ERROR", fmt.Sprintf("defaultProvider %q exists in providers", cfg.DefaultProvider))
		}
	}

	// Docker sandbox.
	fmt.Println()
	fmt.Println("=== Docker Sandbox ===")
	if cfg.Docker.Enabled {
		check(cfg.Docker.Image != "", "ERROR", "docker.image configured")
		if err := checkDockerAvailable(); err != nil {
			fmt.Printf("  ERROR docker: %v\n", err)
			errors++
		} else {
			fmt.Println("  OK    docker daemon accessible")
			if cfg.Docker.Image != "" {
				if err := checkDockerImage(cfg.Docker.Image); err != nil {
					check(false, "WARN", fmt.Sprintf("docker image: %v", err))
				} else {
					check(true, "OK", fmt.Sprintf("docker image: %s", cfg.Docker.Image))
				}
			}
		}
		if cfg.Docker.Network != "" {
			validNet := cfg.Docker.Network == "none" || cfg.Docker.Network == "host" || cfg.Docker.Network == "bridge"
			check(validNet, "WARN", fmt.Sprintf("docker.network: %s", cfg.Docker.Network))
		}
	} else {
		fmt.Println("  --    docker sandbox disabled")
	}

	// Webhooks — check URLs.
	if len(cfg.Webhooks) > 0 {
		fmt.Println()
		fmt.Println("=== Webhooks ===")
		for i, wh := range cfg.Webhooks {
			hasURL := strings.HasPrefix(wh.URL, "http://") || strings.HasPrefix(wh.URL, "https://")
			check(hasURL, "ERROR", fmt.Sprintf("webhook[%d] URL valid: %s", i, wh.URL))
		}
	}

	// MCP Configs — check commands exist.
	if len(cfg.MCPConfigs) > 0 {
		fmt.Println()
		fmt.Println("=== MCP Servers ===")
		for name, raw := range cfg.MCPConfigs {
			cmd, _ := extractMCPSummary(raw)
			if cmd != "" {
				_, err := exec.LookPath(cmd)
				check(err == nil, "WARN", fmt.Sprintf("mcp %q command: %s", name, cmd))
			} else {
				fmt.Printf("  WARN  mcp %q: could not parse command\n", name)
				warnings++
			}
		}
	}

	// Notifications — check URLs.
	if len(cfg.Notifications) > 0 {
		fmt.Println()
		fmt.Println("=== Notifications ===")
		for i, ch := range cfg.Notifications {
			validType := ch.Type == "slack" || ch.Type == "discord"
			check(validType, "ERROR", fmt.Sprintf("notification[%d] type: %s", i, ch.Type))
			hasURL := strings.HasPrefix(ch.WebhookURL, "https://")
			check(hasURL, "WARN", fmt.Sprintf("notification[%d] webhookUrl: %s", i, ch.WebhookURL))
		}
	}

	fmt.Println()
	if errors > 0 {
		fmt.Printf("%d errors, %d warnings\n", errors, warnings)
		os.Exit(1)
	} else if warnings > 0 {
		fmt.Printf("0 errors, %d warnings\n", warnings)
	} else {
		fmt.Println("All checks passed.")
	}
}
