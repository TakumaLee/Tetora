package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// --- P27.1: Skill Install + Sentori Security Scanner ---

// SentoriReport is the result of a security scan on a skill script.
type SentoriReport struct {
	SkillName   string           `json:"skillName"`
	ScannedAt   string           `json:"scannedAt"`
	Findings    []SentoriFinding `json:"findings"`
	OverallRisk string           `json:"overallRisk"` // "safe" | "review" | "dangerous"
	Score       int              `json:"score"`       // 0-100
}

// SentoriFinding represents a single security finding in a skill script.
type SentoriFinding struct {
	Severity    string `json:"severity"` // "critical" | "high" | "medium" | "low"
	Category    string `json:"category"` // "exec" | "path_access" | "exfiltration" | "env_read" | "listener"
	Description string `json:"description"`
	Line        int    `json:"line,omitempty"`
	Match       string `json:"match,omitempty"`
}

// SkillRegistryEntry represents a skill in the remote skill registry.
type SkillRegistryEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	TrustRating string `json:"trustRating"`
}

// --- Sentori Pattern Definitions ---

type sentoriPattern struct {
	re       *regexp.Regexp
	severity string
	category string
	desc     string
}

// Pre-compiled regexp patterns for security scanning.
var sentoriPatterns = []sentoriPattern{
	// exec (critical): shell execution, eval, backtick patterns
	{regexp.MustCompile(`(?i)\bexec\s*\(`), "critical", "exec", "exec() call detected"},
	{regexp.MustCompile(`(?i)\beval\s*\(`), "critical", "exec", "eval() call detected"},
	{regexp.MustCompile("`[^`]+`"), "critical", "exec", "backtick command execution detected"},
	{regexp.MustCompile(`(?i)\bsh\s+-c\b`), "critical", "exec", "sh -c shell execution detected"},
	{regexp.MustCompile(`(?i)\bsubprocess\b`), "critical", "exec", "subprocess usage detected"},
	{regexp.MustCompile(`(?i)\bos\.system\s*\(`), "critical", "exec", "os.system() call detected"},

	// path_access (high): sensitive file access
	{regexp.MustCompile(`~/\.ssh`), "high", "path_access", "SSH directory access detected"},
	{regexp.MustCompile(`~/\.aws`), "high", "path_access", "AWS credentials directory access detected"},
	{regexp.MustCompile(`(?i)\bconfig\.json\b`), "high", "path_access", "config.json access detected"},
	{regexp.MustCompile(`(?i)\.env\b`), "high", "path_access", ".env file access detected"},
	{regexp.MustCompile(`/etc/passwd`), "high", "path_access", "/etc/passwd access detected"},
	{regexp.MustCompile(`~/\.gnupg`), "high", "path_access", "GnuPG directory access detected"},

	// exfiltration (critical): data exfiltration patterns
	{regexp.MustCompile(`(?i)\bcurl\b.*\s-d\b`), "critical", "exfiltration", "curl POST with data detected"},
	{regexp.MustCompile(`(?i)\bwget\b.*--post`), "critical", "exfiltration", "wget POST detected"},
	{regexp.MustCompile(`\|\s*nc\b`), "critical", "exfiltration", "pipe to netcat detected"},
	{regexp.MustCompile(`(?i)\bhttp\b.*\bpost\b.*\bread\b`), "critical", "exfiltration", "HTTP POST with file read detected"},

	// env_read (medium): environment variable access
	{regexp.MustCompile(`\$API_KEY`), "medium", "env_read", "$API_KEY environment variable access"},
	{regexp.MustCompile(`\$TOKEN`), "medium", "env_read", "$TOKEN environment variable access"},
	{regexp.MustCompile(`\$SECRET`), "medium", "env_read", "$SECRET environment variable access"},
	{regexp.MustCompile(`(?i)\bos\.getenv\s*\(`), "medium", "env_read", "os.getenv() call detected"},
	{regexp.MustCompile(`(?i)\bprocess\.env\b`), "medium", "env_read", "process.env access detected"},

	// listener (high): network listener patterns
	{regexp.MustCompile(`(?i)\bnc\s+-l`), "high", "listener", "netcat listener detected"},
	{regexp.MustCompile(`(?i)\bpython\s+-m\s+http\.server\b`), "high", "listener", "Python HTTP server detected"},
	{regexp.MustCompile(`(?i)\bbind\s*\(`), "high", "listener", "socket bind() detected"},
	{regexp.MustCompile(`(?i)\blisten\s*\(`), "high", "listener", "socket listen() detected"},
}

// severityScore returns the score increment for a given severity level.
func severityScore(severity string) int {
	switch severity {
	case "critical":
		return 25
	case "high":
		return 15
	case "medium":
		return 8
	case "low":
		return 3
	default:
		return 0
	}
}

// sentoriScan performs a security scan on a skill script and returns a report.
func sentoriScan(skillName, content string) *SentoriReport {
	report := &SentoriReport{
		SkillName: skillName,
		ScannedAt: time.Now().UTC().Format(time.RFC3339),
		Findings:  []SentoriFinding{},
	}

	lines := strings.Split(content, "\n")

	for lineNum, line := range lines {
		for _, pat := range sentoriPatterns {
			match := pat.re.FindString(line)
			if match != "" {
				report.Findings = append(report.Findings, SentoriFinding{
					Severity:    pat.severity,
					Category:    pat.category,
					Description: pat.desc,
					Line:        lineNum + 1, // 1-indexed
					Match:       match,
				})
			}
		}
	}

	// Calculate score.
	score := 0
	for _, f := range report.Findings {
		score += severityScore(f.Severity)
	}
	if score > 100 {
		score = 100
	}
	report.Score = score

	// Determine overall risk.
	switch {
	case score <= 20:
		report.OverallRisk = "safe"
	case score <= 50:
		report.OverallRisk = "review"
	default:
		report.OverallRisk = "dangerous"
	}

	return report
}

// loadFileSkillScript reads the script file from a file-based skill directory.
// It looks for skills/{name}/script.* (first glob match) or falls back to run.sh/run.py.
func loadFileSkillScript(cfg *Config, name string) (string, error) {
	if !isValidSkillName(name) {
		return "", fmt.Errorf("invalid skill name %q", name)
	}

	dir := filepath.Join(skillsDir(cfg), name)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("skill %q not found", name)
	}

	// Try script.* glob first.
	matches, _ := filepath.Glob(filepath.Join(dir, "script.*"))
	if len(matches) > 0 {
		data, err := os.ReadFile(matches[0])
		if err != nil {
			return "", fmt.Errorf("read script: %w", err)
		}
		return string(data), nil
	}

	// Fall back to run.sh or run.py.
	for _, name := range []string{"run.sh", "run.py"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
	}

	return "", fmt.Errorf("no script file found in skill %q", name)
}

// --- Tool Handlers ---

// toolSentoriScan is the tool handler for the sentori_scan built-in tool.
func toolSentoriScan(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.Name == "" && args.Content == "" {
		return "", fmt.Errorf("either 'name' or 'content' is required")
	}

	var content string
	var skillName string

	if args.Content != "" {
		// Scan raw content directly.
		content = args.Content
		skillName = args.Name
		if skillName == "" {
			skillName = "inline"
		}
	} else {
		// Load skill script by name.
		var err error
		content, err = loadFileSkillScript(cfg, args.Name)
		if err != nil {
			return "", fmt.Errorf("load skill script: %w", err)
		}
		skillName = args.Name
	}

	report := sentoriScan(skillName, content)

	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}
	return string(b), nil
}

// skillInstallMaxSize is the maximum size for a downloaded skill package (1MB).
const skillInstallMaxSize = 1 << 20

// toolSkillInstall is the tool handler for the skill_install built-in tool.
func toolSkillInstall(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		URL         string `json:"url"`
		AutoApprove bool   `json:"auto_approve"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.URL == "" {
		return "", fmt.Errorf("url is required")
	}

	// Download the skill package.
	logInfo("skill install: downloading", "url", args.URL)

	resp, err := http.Get(args.URL)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Limit read to 1MB.
	limited := io.LimitReader(resp.Body, skillInstallMaxSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if len(body) > skillInstallMaxSize {
		return "", fmt.Errorf("skill package too large (max %d bytes)", skillInstallMaxSize)
	}

	// Parse the downloaded JSON package.
	var pkg struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Command     string   `json:"command"`
		Args        []string `json:"args,omitempty"`
		Script      string   `json:"script"`
	}
	if err := json.Unmarshal(body, &pkg); err != nil {
		return "", fmt.Errorf("parse skill package: %w", err)
	}

	if pkg.Name == "" {
		return "", fmt.Errorf("skill package missing 'name' field")
	}
	if pkg.Script == "" {
		return "", fmt.Errorf("skill package missing 'script' field")
	}
	if !isValidSkillName(pkg.Name) {
		return "", fmt.Errorf("invalid skill name %q in package", pkg.Name)
	}

	// Run sentori security scan on the script.
	report := sentoriScan(pkg.Name, pkg.Script)

	// If dangerous, refuse installation.
	if report.OverallRisk == "dangerous" {
		logWarn("skill install refused: dangerous", "name", pkg.Name, "score", report.Score)
		reportJSON, _ := json.MarshalIndent(report, "", "  ")
		result := map[string]any{
			"status": "refused",
			"reason": "skill scored as dangerous",
			"name":   pkg.Name,
			"report": json.RawMessage(reportJSON),
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}

	// Determine approval status.
	approved := false
	if report.OverallRisk == "safe" && args.AutoApprove {
		approved = true
	}
	// "review" always requires manual approval.

	// Default command to ./run.sh if not specified.
	command := pkg.Command
	if command == "" {
		command = "./run.sh"
	}

	meta := SkillMetadata{
		Name:        pkg.Name,
		Description: pkg.Description,
		Command:     command,
		Args:        pkg.Args,
		Approved:    approved,
		CreatedBy:   "skill_install",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := createSkill(cfg, meta, pkg.Script); err != nil {
		return "", fmt.Errorf("create skill: %w", err)
	}

	// Store sentori report alongside the skill.
	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		logWarn("marshal sentori report failed", "error", err)
	} else {
		reportPath := filepath.Join(skillsDir(cfg), pkg.Name, "sentori-report.json")
		if err := os.WriteFile(reportPath, reportJSON, 0o644); err != nil {
			logWarn("write sentori report failed", "path", reportPath, "error", err)
		}
	}

	logInfo("skill installed", "name", pkg.Name, "risk", report.OverallRisk, "approved", approved)

	status := "installed (pending approval)"
	if approved {
		status = "installed (auto-approved)"
	}

	result := map[string]any{
		"status":   status,
		"name":     pkg.Name,
		"risk":     report.OverallRisk,
		"score":    report.Score,
		"findings": len(report.Findings),
		"path":     filepath.Join(skillsDir(cfg), pkg.Name),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolSkillSearch is the tool handler for the skill_search built-in tool.
func toolSkillSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	// Load registry file.
	registryPath := filepath.Join(cfg.baseDir, "skill-registry.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		result := map[string]any{
			"results": []any{},
			"message": "skill registry not found; create skill-registry.json to enable search",
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}

	var registry []SkillRegistryEntry
	if err := json.Unmarshal(data, &registry); err != nil {
		return "", fmt.Errorf("parse skill registry: %w", err)
	}

	// Case-insensitive substring match on name + description.
	query := strings.ToLower(args.Query)
	var matches []SkillRegistryEntry
	for _, entry := range registry {
		nameLower := strings.ToLower(entry.Name)
		descLower := strings.ToLower(entry.Description)
		if strings.Contains(nameLower, query) || strings.Contains(descLower, query) {
			matches = append(matches, entry)
			if len(matches) >= 10 {
				break
			}
		}
	}

	result := map[string]any{
		"query":   args.Query,
		"results": matches,
		"count":   len(matches),
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal results: %w", err)
	}
	return string(b), nil
}
