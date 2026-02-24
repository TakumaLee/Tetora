package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// MigrationReport tracks results of an OpenClaw-to-Tetora migration.
type MigrationReport struct {
	ConfigMerged   int      `json:"configMerged"`
	MemoryFiles    int      `json:"memoryFiles"`
	SkillsImported int      `json:"skillsImported"`
	WorkspaceFiles int      `json:"workspaceFiles"`
	CronJobs       int      `json:"cronJobs"`
	RolesImported  int      `json:"rolesImported"`
	Warnings       []string `json:"warnings,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

// --- Nested JSON access helpers ---

func getNestedString(m map[string]any, keys ...string) string {
	v := traverseNested(m, keys...)
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func getNestedInt(m map[string]any, keys ...string) int {
	v := traverseNested(m, keys...)
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func getNestedBool(m map[string]any, keys ...string) bool {
	v := traverseNested(m, keys...)
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func getNestedMap(m map[string]any, keys ...string) map[string]any {
	v := traverseNested(m, keys...)
	if sub, ok := v.(map[string]any); ok {
		return sub
	}
	return nil
}

func getNestedSlice(m map[string]any, keys ...string) []any {
	v := traverseNested(m, keys...)
	if arr, ok := v.([]any); ok {
		return arr
	}
	return nil
}

func traverseNested(m map[string]any, keys ...string) any {
	if len(keys) == 0 || m == nil {
		return nil
	}
	current := any(m)
	for _, k := range keys {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = cm[k]
		if !ok {
			return nil
		}
	}
	return current
}

// maskSecret returns first 4 chars + "****" if len > 4, otherwise "****".
func maskSecret(s string) string {
	if len(s) > 4 {
		return s[:4] + "****"
	}
	return "****"
}

// stripModelPrefix removes "anthropic/" or "openai/" prefix from model names.
func stripModelPrefix(model string) string {
	model = strings.TrimPrefix(model, "anthropic/")
	model = strings.TrimPrefix(model, "openai/")
	return model
}

// parseIncludeList parses a comma-separated include string into a set.
func parseIncludeList(s string) map[string]bool {
	if s == "all" || s == "" {
		return map[string]bool{
			"config":    true,
			"memory":    true,
			"skills":    true,
			"cron":      true,
			"workspace": true,
			"roles":     true,
		}
	}
	result := make(map[string]bool)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result[part] = true
		}
	}
	return result
}

// cmdMigrateOpenClaw handles the "tetora migrate openclaw" CLI command.
func cmdMigrateOpenClaw(args []string) {
	fs := flag.NewFlagSet("migrate-openclaw", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Preview migration without writing files")
	includeStr := fs.String("include", "all", "Comma-separated list of components: config,memory,skills,cron,workspace,roles")
	includeSecrets := fs.Bool("include-secrets", false, "Include secret values in migration (tokens, API keys)")
	fs.Parse(args)

	ocDir := detectOpenClaw()
	if ocDir == "" {
		fmt.Println("No OpenClaw installation found at ~/.openclaw/")
		return
	}
	fmt.Printf("Found OpenClaw installation at %s\n", ocDir)

	cfg, err := tryLoadConfig(findConfigPath())
	if err != nil {
		fmt.Printf("Error loading Tetora config: %v\n", err)
		return
	}

	include := parseIncludeList(*includeStr)

	if *dryRun {
		fmt.Println("=== DRY RUN MODE ===")
	}

	report, err := migrateOpenClaw(cfg, ocDir, *dryRun, include, *includeSecrets)
	if err != nil {
		fmt.Printf("Migration failed: %v\n", err)
		return
	}

	fmt.Println("\n=== Migration Report ===")
	fmt.Printf("Config fields merged: %d\n", report.ConfigMerged)
	fmt.Printf("Memory files: %d\n", report.MemoryFiles)
	fmt.Printf("Skills imported: %d\n", report.SkillsImported)
	fmt.Printf("Workspace files: %d\n", report.WorkspaceFiles)
	fmt.Printf("Cron jobs: %d\n", report.CronJobs)

	if len(report.Warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, w := range report.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
	if len(report.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range report.Errors {
			fmt.Printf("  - %s\n", e)
		}
	}
	if !*dryRun && len(report.Errors) == 0 {
		fmt.Println("\nMigration complete!")
	}
}

// detectOpenClaw returns the path to ~/.openclaw/ if it exists, or empty string.
func detectOpenClaw() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".openclaw")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}

// migrateOpenClaw orchestrates the full migration from OpenClaw to Tetora.
func migrateOpenClaw(cfg *Config, ocDir string, dryRun bool, include map[string]bool, includeSecrets bool) (*MigrationReport, error) {
	report := &MigrationReport{}

	// Check OpenClaw dir exists.
	if _, err := os.Stat(ocDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("OpenClaw directory not found: %s", ocDir)
	}

	// 1. Config
	if include["config"] {
		if err := migrateOpenClawConfig(cfg, ocDir, dryRun, includeSecrets, report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("config migration: %v", err))
		}
	}

	// 2. Memory
	if include["memory"] {
		if err := migrateOpenClawMemory(cfg, ocDir, dryRun, report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("memory migration: %v", err))
		}
	}

	// 3. Workspace
	if include["workspace"] {
		if err := migrateOpenClawWorkspace(cfg, ocDir, dryRun, report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("workspace migration: %v", err))
		}
	}

	// 4. Skills
	if include["skills"] {
		if err := migrateOpenClawSkills(cfg, ocDir, dryRun, report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("skills migration: %v", err))
		}
	}

	// 5. Cron
	if include["cron"] {
		if err := migrateOpenClawCron(cfg, ocDir, dryRun, report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("cron migration: %v", err))
		}
	}

	// 6. Roles (soul files → Tetora roles)
	if include["roles"] {
		if err := migrateOpenClawRoles(cfg, ocDir, dryRun, report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("roles migration: %v", err))
		}
	}

	return report, nil
}

// migrateOpenClawConfig parses openclaw.json as raw map and maps nested fields to Tetora config.
// Only empty/zero Tetora fields are overwritten.
func migrateOpenClawConfig(cfg *Config, ocDir string, dryRun bool, includeSecrets bool, report *MigrationReport) error {
	data, err := os.ReadFile(filepath.Join(ocDir, "openclaw.json"))
	if err != nil {
		if os.IsNotExist(err) {
			report.Warnings = append(report.Warnings, "openclaw.json not found, skipping config migration")
			return nil
		}
		return fmt.Errorf("reading openclaw.json: %w", err)
	}

	var oc map[string]any
	if err := json.Unmarshal(data, &oc); err != nil {
		return fmt.Errorf("parsing openclaw.json: %w", err)
	}

	merged := 0

	// Telegram bot token
	if token := getNestedString(oc, "channels", "telegram", "botToken"); token != "" {
		if cfg.Telegram.BotToken == "" {
			if includeSecrets && !dryRun {
				cfg.Telegram.BotToken = token
			}
			merged++
			report.Warnings = append(report.Warnings, fmt.Sprintf("telegram.botToken merged (%s)", maskSecret(token)))
		} else {
			report.Warnings = append(report.Warnings, "telegram.botToken already set in Tetora, skipped")
		}
	}

	// Telegram allowFrom -> ChatID (first element as int64)
	if allowFrom := getNestedSlice(oc, "channels", "telegram", "allowFrom"); len(allowFrom) > 0 {
		if cfg.Telegram.ChatID == 0 {
			var chatID int64
			switch v := allowFrom[0].(type) {
			case float64:
				chatID = int64(v)
			case string:
				chatID, _ = strconv.ParseInt(v, 10, 64)
			}
			if chatID != 0 {
				if !dryRun {
					cfg.Telegram.ChatID = chatID
				}
				merged++
			}
		}
	}

	// Discord token
	if token := getNestedString(oc, "channels", "discord", "token"); token != "" {
		if cfg.Discord.BotToken == "" {
			if includeSecrets && !dryRun {
				cfg.Discord.BotToken = token
			}
			merged++
			report.Warnings = append(report.Warnings, fmt.Sprintf("discord.token merged (%s)", maskSecret(token)))
		} else {
			report.Warnings = append(report.Warnings, "discord.token already set in Tetora, skipped")
		}
	}

	// Slack botToken
	if token := getNestedString(oc, "channels", "slack", "botToken"); token != "" {
		if cfg.Slack.BotToken == "" {
			if includeSecrets && !dryRun {
				cfg.Slack.BotToken = token
			}
			merged++
			report.Warnings = append(report.Warnings, fmt.Sprintf("slack.botToken merged (%s)", maskSecret(token)))
		} else {
			report.Warnings = append(report.Warnings, "slack.botToken already set in Tetora, skipped")
		}
	}

	// Slack appToken warning
	if appToken := getNestedString(oc, "channels", "slack", "appToken"); appToken != "" {
		report.Warnings = append(report.Warnings, "Slack appToken found — Tetora uses signingSecret instead")
	}

	// Default model
	if model := getNestedString(oc, "agents", "defaults", "model", "primary"); model != "" {
		if cfg.DefaultModel == "" {
			if !dryRun {
				cfg.DefaultModel = stripModelPrefix(model)
			}
			merged++
		}
	}

	// Max concurrent
	if mc := getNestedInt(oc, "agents", "defaults", "maxConcurrent"); mc > 0 {
		if cfg.MaxConcurrent == 0 {
			if !dryRun {
				cfg.MaxConcurrent = mc
			}
			merged++
		}
	}

	// Gateway port
	if port := getNestedInt(oc, "gateway", "port"); port > 0 {
		if cfg.ListenAddr == "" {
			if !dryRun {
				cfg.ListenAddr = fmt.Sprintf("127.0.0.1:%d", port)
			}
			merged++
		}
	}

	// Gateway auth token
	if token := getNestedString(oc, "gateway", "auth", "token"); token != "" {
		if cfg.APIToken == "" {
			if includeSecrets && !dryRun {
				cfg.APIToken = token
			}
			merged++
			report.Warnings = append(report.Warnings, fmt.Sprintf("gateway.auth.token merged (%s)", maskSecret(token)))
		}
	}

	// Anthropic provider base URL
	if baseURL := getNestedString(oc, "models", "providers", "anthropic", "baseUrl"); baseURL != "" {
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]ProviderConfig)
		}
		p := cfg.Providers["anthropic"]
		if p.BaseURL == "" {
			if !dryRun {
				p.Type = "anthropic"
				p.BaseURL = baseURL
				cfg.Providers["anthropic"] = p
			}
			merged++
		}
	}

	// Anthropic API key
	if apiKey := getNestedString(oc, "models", "providers", "anthropic", "apiKey"); apiKey != "" {
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]ProviderConfig)
		}
		p := cfg.Providers["anthropic"]
		if p.APIKey == "" {
			if includeSecrets && !dryRun {
				p.Type = "anthropic"
				p.APIKey = apiKey
				cfg.Providers["anthropic"] = p
			}
			merged++
			report.Warnings = append(report.Warnings, fmt.Sprintf("anthropic.apiKey merged (%s)", maskSecret(apiKey)))
		}
	}

	report.ConfigMerged = merged

	if !dryRun && merged > 0 {
		logInfo("migrate-openclaw: merged %d config fields", merged)
	}

	return nil
}

// migrateOpenClawMemory copies memory files from OpenClaw workspace/memory/ to notes vault.
func migrateOpenClawMemory(cfg *Config, ocDir string, dryRun bool, report *MigrationReport) error {
	memDir := filepath.Join(ocDir, "workspace", "memory")
	entries, err := os.ReadDir(memDir)
	if err != nil {
		if os.IsNotExist(err) {
			report.Warnings = append(report.Warnings, "no workspace/memory/ directory found")
			return nil
		}
		return fmt.Errorf("reading memory dir: %w", err)
	}

	vaultPath := cfg.Notes.vaultPathResolved(cfg.baseDir)
	targetDir := filepath.Join(vaultPath, "openclaw-memory")

	if !dryRun {
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return fmt.Errorf("creating target dir: %w", err)
		}
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// Skip sqlite files — embeddings are not portable.
		if strings.HasSuffix(name, ".sqlite") || strings.HasSuffix(name, ".sqlite3") || name == "main.sqlite" {
			report.Warnings = append(report.Warnings, fmt.Sprintf("skipping %s — embeddings not portable, will auto-rebuild", name))
			continue
		}

		src := filepath.Join(memDir, name)
		dst := filepath.Join(targetDir, name)

		if !dryRun {
			if err := migCopyFile(src, dst); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("copying %s: %v", name, err))
				continue
			}
		}
		count++
	}

	report.MemoryFiles = count
	return nil
}

// migrateOpenClawWorkspace processes special workspace files from OpenClaw.
func migrateOpenClawWorkspace(cfg *Config, ocDir string, dryRun bool, report *MigrationReport) error {
	wsDir := filepath.Join(ocDir, "workspace")
	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		report.Warnings = append(report.Warnings, "no workspace/ directory found")
		return nil
	}

	vaultPath := cfg.Notes.vaultPathResolved(cfg.baseDir)
	wsTargetDir := filepath.Join(vaultPath, "openclaw-workspace")
	memTargetDir := filepath.Join(vaultPath, "openclaw-memory")

	count := 0

	// SOUL.md -> workspace/SOUL-openclaw.md
	soulSrc := filepath.Join(wsDir, "SOUL.md")
	if _, err := os.Stat(soulSrc); err == nil {
		soulDst := filepath.Join(cfg.baseDir, "workspace", "SOUL-openclaw.md")
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(soulDst), 0o755); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("creating workspace dir: %v", err))
			} else if err := migCopyFile(soulSrc, soulDst); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("copying SOUL.md: %v", err))
			} else {
				count++
			}
		} else {
			count++
		}
	}

	// AGENTS.md, USER.md, IDENTITY.md -> notes vault openclaw-workspace/
	for _, name := range []string{"AGENTS.md", "USER.md", "IDENTITY.md"} {
		src := filepath.Join(wsDir, name)
		if _, err := os.Stat(src); err == nil {
			dst := filepath.Join(wsTargetDir, name)
			if !dryRun {
				if err := os.MkdirAll(wsTargetDir, 0o755); err != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("creating workspace target dir: %v", err))
					continue
				}
				if err := migCopyFile(src, dst); err != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("copying %s: %v", name, err))
					continue
				}
			}
			count++
		}
	}

	// MEMORY.md -> notes vault openclaw-memory/MEMORY.md
	memorySrc := filepath.Join(wsDir, "MEMORY.md")
	if _, err := os.Stat(memorySrc); err == nil {
		memoryDst := filepath.Join(memTargetDir, "MEMORY.md")
		if !dryRun {
			if err := os.MkdirAll(memTargetDir, 0o755); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("creating memory target dir: %v", err))
			} else if err := migCopyFile(memorySrc, memoryDst); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("copying MEMORY.md: %v", err))
			} else {
				count++
			}
		} else {
			count++
		}
	}

	// HEARTBEAT.md -> warning only
	heartbeatSrc := filepath.Join(wsDir, "HEARTBEAT.md")
	if _, err := os.Stat(heartbeatSrc); err == nil {
		report.Warnings = append(report.Warnings, "HEARTBEAT.md found — convert to Tetora cron job manually")
	}

	report.WorkspaceFiles = count
	return nil
}

// migrateOpenClawSkills imports skills from OpenClaw, supporting both file-based and folder-based skills.
func migrateOpenClawSkills(cfg *Config, ocDir string, dryRun bool, report *MigrationReport) error {
	skillsDir := filepath.Join(ocDir, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			report.Warnings = append(report.Warnings, "no skills/ directory found")
			return nil
		}
		return fmt.Errorf("reading skills dir: %w", err)
	}

	targetDir := filepath.Join(cfg.baseDir, "skills")
	if !dryRun {
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return fmt.Errorf("creating skills dir: %w", err)
		}
	}

	count := 0
	for _, entry := range entries {
		name := entry.Name()
		src := filepath.Join(skillsDir, name)

		if entry.IsDir() {
			// Folder-based skill: must contain SKILL.md
			skillMD := filepath.Join(src, "SKILL.md")
			if _, err := os.Stat(skillMD); err != nil {
				continue // not a skill folder, skip silently
			}
			dst := filepath.Join(targetDir, name)
			if !dryRun {
				if err := copyDir(src, dst); err != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("copying skill folder %s: %v", name, err))
					continue
				}
			}
			count++
		} else {
			// File-based skill
			if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".json") {
				continue
			}
			dst := filepath.Join(targetDir, name)
			if !dryRun {
				if err := migCopyFile(src, dst); err != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("copying skill %s: %v", name, err))
					continue
				}
			}
			count++
		}
	}

	report.SkillsImported = count
	return nil
}

// migrateOpenClawCron reads OpenClaw cron jobs and converts them to Tetora format.
func migrateOpenClawCron(cfg *Config, ocDir string, dryRun bool, report *MigrationReport) error {
	cronFile := filepath.Join(ocDir, "cron", "jobs.json")
	data, err := os.ReadFile(cronFile)
	if err != nil {
		if os.IsNotExist(err) {
			report.Warnings = append(report.Warnings, "no cron/jobs.json found")
			return nil
		}
		return fmt.Errorf("reading cron jobs: %w", err)
	}

	// OpenClaw cron format: {"version":1, "jobs":[...]} or bare array.
	var ocJobs []map[string]any
	var wrapper struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper.Jobs) > 0 {
		ocJobs = wrapper.Jobs
	} else if err := json.Unmarshal(data, &ocJobs); err != nil {
		return fmt.Errorf("parsing cron jobs: %w", err)
	}

	// Read existing Tetora jobs file.
	var existingJobs []CronJobConfig
	if cfg.JobsFile != "" {
		if existingData, err := os.ReadFile(cfg.JobsFile); err == nil {
			var jf struct {
				Jobs []CronJobConfig `json:"jobs"`
			}
			if err := json.Unmarshal(existingData, &jf); err == nil {
				existingJobs = jf.Jobs
			}
		}
	}

	existingNames := make(map[string]bool)
	for _, j := range existingJobs {
		existingNames[j.Name] = true
	}

	count := 0
	for _, ocJob := range ocJobs {
		name, _ := ocJob["name"].(string)
		if name == "" {
			continue
		}

		if existingNames[name] {
			report.Warnings = append(report.Warnings, fmt.Sprintf("cron job %q already exists, skipped", name))
			continue
		}

		enabled, _ := ocJob["enabled"].(bool)

		schedule := getNestedMap(ocJob, "schedule")
		expr := ""
		tz := ""
		if schedule != nil {
			expr, _ = schedule["expr"].(string)
			tz, _ = schedule["tz"].(string)
		}

		payload := getNestedMap(ocJob, "payload")
		message := ""
		model := ""
		timeoutSeconds := 0
		if payload != nil {
			message, _ = payload["message"].(string)
			model, _ = payload["model"].(string)
			if ts, ok := payload["timeoutSeconds"].(float64); ok {
				timeoutSeconds = int(ts)
			}
		}

		delivery := getNestedMap(ocJob, "delivery")
		notify := false
		if delivery != nil {
			mode, _ := delivery["mode"].(string)
			notify = mode == "announce"
		}

		timeout := "15m"
		if timeoutSeconds > 0 {
			minutes := timeoutSeconds / 60
			if minutes < 1 {
				minutes = 1
			}
			timeout = fmt.Sprintf("%dm", minutes)
		}

		job := CronJobConfig{
			ID:       slugify(name),
			Name:     name,
			Enabled:  enabled,
			Schedule: expr,
			TZ:       tz,
			Task: CronTaskConfig{
				Prompt:  message,
				Model:   stripModelPrefix(model),
				Timeout: timeout,
				Budget:  2.0,
			},
			Notify: notify,
		}

		existingJobs = append(existingJobs, job)
		existingNames[name] = true
		count++
	}

	if count > 0 && !dryRun && cfg.JobsFile != "" {
		jf := struct {
			Jobs []CronJobConfig `json:"jobs"`
		}{Jobs: existingJobs}

		out, err := json.MarshalIndent(jf, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling jobs: %w", err)
		}
		if err := os.WriteFile(cfg.JobsFile, out, 0o644); err != nil {
			return fmt.Errorf("writing jobs file: %w", err)
		}
	}

	report.CronJobs = count
	return nil
}

// migrateOpenClawRoles finds SOUL files in OpenClaw workspace and creates
// corresponding Tetora roles with soul files copied to ~/.tetora/workspace/.
func migrateOpenClawRoles(cfg *Config, ocDir string, dryRun bool, report *MigrationReport) error {
	wsDir := filepath.Join(ocDir, "workspace")
	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		report.Warnings = append(report.Warnings, "no workspace/ directory, skipping roles")
		return nil
	}

	tetoraWs := filepath.Join(cfg.baseDir, "workspace")
	configPath := filepath.Join(cfg.baseDir, "config.json")
	count := 0

	// Collect soul files: root SOUL.md → "default", team/*/SOUL.md → directory name
	type soulEntry struct {
		name    string
		srcPath string
	}
	var souls []soulEntry

	// Root SOUL.md
	rootSoul := filepath.Join(wsDir, "SOUL.md")
	if _, err := os.Stat(rootSoul); err == nil {
		souls = append(souls, soulEntry{name: "default", srcPath: rootSoul})
	}

	// team/*/SOUL.md
	teamDir := filepath.Join(wsDir, "team")
	if entries, err := os.ReadDir(teamDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			soulPath := filepath.Join(teamDir, e.Name(), "SOUL.md")
			if _, err := os.Stat(soulPath); err == nil {
				souls = append(souls, soulEntry{name: e.Name(), srcPath: soulPath})
			}
		}
	}

	if len(souls) == 0 {
		report.Warnings = append(report.Warnings, "no SOUL files found in OpenClaw workspace")
		return nil
	}

	// Read OpenClaw config for default model
	defaultModel := "sonnet"
	ocCfgPath := filepath.Join(ocDir, "openclaw.json")
	if data, err := os.ReadFile(ocCfgPath); err == nil {
		var oc map[string]any
		if json.Unmarshal(data, &oc) == nil {
			if m := getNestedString(oc, "agents", "defaults", "model", "primary"); m != "" {
				defaultModel = stripModelPrefix(m)
			}
		}
	}

	for _, s := range souls {
		dstFile := fmt.Sprintf("SOUL-%s.md", s.name)
		if s.name == "default" {
			dstFile = "SOUL.md"
		}
		dstPath := filepath.Join(tetoraWs, dstFile)

		if !dryRun {
			if err := os.MkdirAll(tetoraWs, 0o755); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("creating workspace dir: %v", err))
				continue
			}
			if err := migCopyFile(s.srcPath, dstPath); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("copying %s: %v", filepath.Base(s.srcPath), err))
				continue
			}

			rc := RoleConfig{
				SoulFile:       dstFile,
				Model:          defaultModel,
				Description:    fmt.Sprintf("Imported from OpenClaw (%s)", s.name),
				PermissionMode: "acceptEdits",
			}
			if err := updateConfigRoles(configPath, s.name, &rc); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("adding role %q: %v", s.name, err))
				continue
			}
		}
		count++
	}

	report.RolesImported = count
	return nil
}

// slugify converts a string to a URL-friendly slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// migCopyFile copies a single file from src to dst, creating parent directories as needed.
// Named differently from copyFile in backup_schedule.go to avoid redeclaration.
func migCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}

// copyDir recursively copies a directory tree from src to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		return migCopyFile(path, target)
	})
}
