// NOTE: Unresolved root-only dependencies for configMigrate:
//   - getConfigVersion(raw map[string]json.RawMessage) int  — defined in migrate.go
//   - currentConfigVersion (const int)                      — defined in migrate.go
//   - migrateConfig(configPath string, dryRun bool)         — defined in migrate.go
// These three functions/constants are tightly coupled to root's migration table
// and cannot be cleanly extracted without moving migrate.go to internal/.
// configMigrate below calls the daemon's /api/config/migrate endpoint instead.
// All other subcommands (show, set, validate, history, rollback, diff, snapshot,
// show-version, versions) are fully self-contained here.

package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"tetora/internal/cron"
	"tetora/internal/version"
)

// --- Local type replicas for configValidate ---
// These mirror root types with only the fields needed by the validate command.

type validateRateLimitConfig struct {
	Enabled   bool `json:"enabled"`
	MaxPerMin int  `json:"maxPerMin,omitempty"`
}

type validateTLSConfig struct {
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
}

type validateSecurityAlertConfig struct {
	Enabled       bool `json:"enabled"`
	FailThreshold int  `json:"failThreshold,omitempty"`
	FailWindowMin int  `json:"failWindowMin,omitempty"`
}

type validateDockerConfig struct {
	Enabled bool   `json:"enabled"`
	Image   string `json:"image,omitempty"`
	Network string `json:"network,omitempty"`
}

type validateDashboardAuthConfig struct {
	Enabled  bool   `json:"enabled"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

type validateWebhookConfig struct {
	URL string `json:"url"`
}

type validateNotificationChannel struct {
	Type       string `json:"type"`
	WebhookURL string `json:"webhookUrl"`
}

type validateProviderConfig struct {
	Type    string `json:"type"`
	Path    string `json:"path,omitempty"`
	BaseURL string `json:"baseUrl,omitempty"`
	Model   string `json:"model,omitempty"`
}

type validateTelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"botToken"`
	ChatID   int64  `json:"chatID"`
}

type validateCronJobConfig struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule"`
	Agent    string `json:"agent,omitempty"`
}

type validateJobsFile struct {
	Jobs []validateCronJobConfig `json:"jobs"`
}

// validateFullConfig extends CLIConfig with fields only needed for validation.
// It is decoded on demand, not used for normal CLI operations.
type validateFullConfig struct {
	CLIConfig
	RateLimit     validateRateLimitConfig            `json:"rateLimit,omitempty"`
	TLS           validateTLSConfig                  `json:"tls,omitempty"`
	TLSEnabled    bool                               `json:"-"`
	SecurityAlert validateSecurityAlertConfig        `json:"securityAlert,omitempty"`
	AllowedIPs    []string                           `json:"allowedIPs,omitempty"`
	Docker        validateDockerConfig               `json:"docker,omitempty"`
	DashboardAuth validateDashboardAuthConfig        `json:"dashboardAuth,omitempty"`
	Webhooks      []validateWebhookConfig            `json:"webhooks,omitempty"`
	Notifications []validateNotificationChannel      `json:"notifications,omitempty"`
	Providers     map[string]validateProviderConfig  `json:"providers,omitempty"`
	Telegram      validateTelegramConfig             `json:"telegram,omitempty"`
}

// --- Entry Points ---

func CmdConfig(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora config <show|set|validate|migrate|history|rollback|diff|snapshot|show-version|versions>")
		return
	}
	// Try version-related subcommands first.
	if HandleConfigVersionSubcommands(args[0], args[1:]) {
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

// configMigrate delegates to the daemon's migrate endpoint because the
// migration table lives in root/migrate.go and cannot be imported here.
func configMigrate(args []string) {
	dryRun := false
	for _, a := range args {
		if a == "--dry-run" || a == "-n" {
			dryRun = true
		}
	}

	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()

	path := "/api/config/migrate"
	if dryRun {
		path += "?dryRun=true"
	}

	resp, err := api.Post(path, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: daemon not reachable — is tetora running?\n")
		fmt.Fprintf(os.Stderr, "Details: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var result map[string]any
	if decErr := json.NewDecoder(resp.Body).Decode(&result); decErr != nil {
		fmt.Fprintf(os.Stderr, "Error decoding response: %v\n", decErr)
		os.Exit(1)
	}

	if msg, ok := result["message"].(string); ok {
		fmt.Println(msg)
	} else {
		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(out))
	}
}

// configShow prints config with secrets masked.
func configShow() {
	configPath := FindConfigPath()
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
	configPath := FindConfigPath()
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
	if err := os.WriteFile(configPath, append(out, '\n'), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
		os.Exit(1)
	}

	// Auto-snapshot config version.
	cfg := LoadCLIConfig(configPath)
	version.SnapshotConfig(cfg.HistoryDB, configPath, "cli", fmt.Sprintf("set %s", key))

	fmt.Printf("Updated %s = %v\n", key, parsed)
}

// configValidate checks config and jobs for common issues.
func configValidate() {
	configPath := FindConfigPath()

	// Load full config including validate-only fields.
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config: %v\n", err)
		os.Exit(1)
	}
	var cfg validateFullConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config: %v\n", err)
		os.Exit(1)
	}
	// Resolve paths so we can stat files.
	baseDir := filepath.Dir(configPath)
	if cfg.TLS.CertFile != "" && !filepath.IsAbs(cfg.TLS.CertFile) {
		cfg.TLS.CertFile = filepath.Join(baseDir, cfg.TLS.CertFile)
	}
	if cfg.TLS.KeyFile != "" && !filepath.IsAbs(cfg.TLS.KeyFile) {
		cfg.TLS.KeyFile = filepath.Join(baseDir, cfg.TLS.KeyFile)
	}
	cfg.TLSEnabled = cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != ""

	// Also load CLIConfig for resolved HistoryDB/JobsFile paths.
	lcfg := LoadCLIConfig(configPath)

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
	claudePath := lcfg.ClaudePath
	if claudePath == "" {
		claudePath = "claude"
	}
	_, lookErr := exec.LookPath(claudePath)
	check(lookErr == nil, "ERROR", fmt.Sprintf("claude binary: %s", claudePath))

	// Listen address.
	check(lcfg.ListenAddr != "", "ERROR", fmt.Sprintf("listenAddr: %s", lcfg.ListenAddr))

	// API token.
	check(lcfg.APIToken != "", "WARN", "apiToken configured")

	// History DB.
	check(lcfg.HistoryDB != "", "WARN", fmt.Sprintf("historyDB: %s", lcfg.HistoryDB))

	// Default workdir.
	if lcfg.DefaultWorkdir != "" {
		_, statErr := os.Stat(lcfg.DefaultWorkdir)
		check(statErr == nil, "WARN", fmt.Sprintf("defaultWorkdir exists: %s", lcfg.DefaultWorkdir))
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
	for name, rc := range lcfg.Agents {
		if rc.SoulFile != "" {
			path := rc.SoulFile
			if !filepath.IsAbs(path) {
				path = filepath.Join(lcfg.DefaultWorkdir, path)
			}
			_, statErr := os.Stat(path)
			check(statErr == nil, "WARN", fmt.Sprintf("agent %q soul file: %s", name, rc.SoulFile))
		} else {
			fmt.Printf("  OK    agent %q (no soul file)\n", name)
		}
	}
	if len(lcfg.Agents) == 0 {
		fmt.Println("  (no agents configured)")
	}

	// Jobs — validate cron expressions.
	fmt.Println()
	fmt.Println("=== Cron Jobs ===")
	jobsData, jobsErr := os.ReadFile(lcfg.JobsFile)
	if jobsErr != nil {
		fmt.Printf("  WARN  cannot read jobs file: %s\n", lcfg.JobsFile)
		warnings++
	} else {
		var jf validateJobsFile
		if err := json.Unmarshal(jobsData, &jf); err != nil {
			fmt.Printf("  ERROR invalid jobs JSON: %v\n", err)
			errors++
		} else {
			for _, j := range jf.Jobs {
				_, cronErr := cron.Parse(j.Schedule)
				check(cronErr == nil, "ERROR", fmt.Sprintf("job %q schedule: %s", j.ID, j.Schedule))

				// Check agent exists if specified.
				if j.Agent != "" {
					_, ok := lcfg.Agents[j.Agent]
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
				if _, _, ipErr := net.ParseCIDR(entry); ipErr != nil {
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
	if cfg.TLSEnabled {
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
					path = lcfg.ClaudePath
				}
				if path == "" {
					path = "claude"
				}
				_, lookErr := exec.LookPath(path)
				check(lookErr == nil, "WARN", fmt.Sprintf("provider %q (%s): %s", name, pc.Type, path))
			case "anthropic":
				if pc.BaseURL != "" {
					hasURL := strings.HasPrefix(pc.BaseURL, "http://") || strings.HasPrefix(pc.BaseURL, "https://")
					check(hasURL, "ERROR", fmt.Sprintf("provider %q baseUrl: %s", name, pc.BaseURL))
				}
				hasModel := pc.Model != ""
				check(hasModel, "WARN", fmt.Sprintf("provider %q default model", name))
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
		if lcfg.DefaultProvider != "" {
			_, exists := cfg.Providers[lcfg.DefaultProvider]
			check(exists, "ERROR", fmt.Sprintf("defaultProvider %q exists in providers", lcfg.DefaultProvider))
		}
	}

	// Docker sandbox.
	fmt.Println()
	fmt.Println("=== Docker Sandbox ===")
	if cfg.Docker.Enabled {
		check(cfg.Docker.Image != "", "ERROR", "docker.image configured")
		if dockerErr := checkDockerAvailable(); dockerErr != nil {
			fmt.Printf("  ERROR docker: %v\n", dockerErr)
			errors++
		} else {
			fmt.Println("  OK    docker daemon accessible")
			if cfg.Docker.Image != "" {
				if imgErr := checkDockerImage(cfg.Docker.Image); imgErr != nil {
					check(false, "WARN", fmt.Sprintf("docker image: %v", imgErr))
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
	if len(lcfg.MCPConfigs) > 0 {
		fmt.Println()
		fmt.Println("=== MCP Servers ===")
		for name, raw := range lcfg.MCPConfigs {
			cmd, _ := extractMCPSummary(raw)
			if cmd != "" {
				_, lookErr := exec.LookPath(cmd)
				check(lookErr == nil, "WARN", fmt.Sprintf("mcp %q command: %s", name, cmd))
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

// --- Docker helpers (replicated from root/sandbox.go) ---

func checkDockerAvailable() error {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker not found in PATH")
	}

	out, err := exec.Command(dockerPath, "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return fmt.Errorf("docker daemon not accessible: %v", err)
	}

	v := strings.TrimSpace(string(out))
	if v == "" {
		return fmt.Errorf("docker daemon returned empty version")
	}

	return nil
}

func checkDockerImage(image string) error {
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		return fmt.Errorf("image %q not found locally (docker pull %s)", image, image)
	}
	return nil
}

// extractMCPSummary is defined in mcp.go (same package).
