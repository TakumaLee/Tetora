package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/provider"
	"tetora/internal/skill"
)

// --- Anthropic Messages API (slim client for skill init) ---

type skillInitMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type skillInitRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []skillInitMessage `json:"messages"`
}

type skillInitResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

const (
	skillInitDefaultModel = "claude-sonnet-4-6-20250514"
	skillInitMaxTokens    = 4096
	skillInitMaxTurns     = 10
	anthropicMessagesURL = "https://api.anthropic.com/v1/messages"
)

// CmdSkillInit runs an AI-driven interview to generate a SKILL.md.
func CmdSkillInit(nameArg string) {
	cfg := LoadCLIConfig(FindConfigPath())

	apiKey := resolveAnthropicKey(cfg)
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: ANTHROPIC_API_KEY not set.")
		fmt.Fprintln(os.Stderr, "Set the env var or configure a provider with apiKey in config.json.")
		os.Exit(1)
	}

	scanner := bufio.NewScanner(os.Stdin)

	// Get skill name.
	name := nameArg
	if name == "" {
		fmt.Print("Skill name (e.g. my-tool): ")
		if !scanner.Scan() {
			return
		}
		name = strings.TrimSpace(scanner.Text())
	}

	if !skill.IsValidSkillName(name) {
		fmt.Fprintf(os.Stderr, "Error: invalid skill name %q (alphanumeric + hyphens, max 64 chars)\n", name)
		os.Exit(1)
	}

	// Check if skill already exists.
	dir := filepath.Join(cliSkillsDir(cfg), name)
	if _, err := os.Stat(dir); err == nil {
		fmt.Fprintf(os.Stderr, "Error: skill %q already exists at %s\n", name, dir)
		os.Exit(1)
	}

	fmt.Printf("\n--- skill init: %s ---\n", name)
	fmt.Println("AI will interview you to create SKILL.md. Type 'done' to finish early.")
	fmt.Println()

	system := skillInitSystemPrompt(name)
	messages := []skillInitMessage{
		{Role: "user", Content: "Start."},
	}

	// Phase 1: Interview loop.
	for turn := 0; turn < skillInitMaxTurns; turn++ {
		reply, err := callAnthropicInit(apiKey, system, messages)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nAPI error: %v\n", err)
			os.Exit(1)
		}
		messages = append(messages, skillInitMessage{Role: "assistant", Content: reply})

		// Check if AI decided it has enough and generated the SKILL.md.
		if md := extractSkillMD(reply); md != "" {
			writeSkillInitOutput(cfg, name, md)
			return
		}

		// Print AI question.
		fmt.Printf("AI: %s\n\n", reply)

		// Read user input.
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		lower := strings.ToLower(input)
		if lower == "done" || lower == "完成" {
			// Ask AI to generate with what we have.
			messages = append(messages, skillInitMessage{
				Role:    "user",
				Content: "That's all the information I have. Please generate the SKILL.md now.",
			})
			reply, err := callAnthropicInit(apiKey, system, messages)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nAPI error: %v\n", err)
				os.Exit(1)
			}
			if md := extractSkillMD(reply); md != "" {
				writeSkillInitOutput(cfg, name, md)
				return
			}
			// Fallback: print raw output.
			fmt.Println(reply)
			fmt.Fprintln(os.Stderr, "Error: could not extract SKILL.md from AI output.")
			os.Exit(1)
		}

		messages = append(messages, skillInitMessage{Role: "user", Content: input})
	}

	// Max turns reached — force generation.
	messages = append(messages, skillInitMessage{
		Role:    "user",
		Content: "Please generate the SKILL.md now with all information collected.",
	})
	reply, err := callAnthropicInit(apiKey, system, messages)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nAPI error: %v\n", err)
		os.Exit(1)
	}
	if md := extractSkillMD(reply); md != "" {
		writeSkillInitOutput(cfg, name, md)
		return
	}
	fmt.Println(reply)
	fmt.Fprintln(os.Stderr, "Error: could not extract SKILL.md from AI output.")
	os.Exit(1)
}

// resolveAnthropicKey finds an Anthropic API key from env or config.
func resolveAnthropicKey(cfg *CLIConfig) string {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return key
	}
	// CLIConfig.Providers is map[string]json.RawMessage; unmarshal each to inspect.
	for _, raw := range cfg.Providers {
		var pc struct {
			Type    string `json:"type"`
			BaseURL string `json:"baseUrl"`
			APIKey  string `json:"apiKey"`
		}
		if json.Unmarshal(raw, &pc) != nil {
			continue
		}
		if pc.APIKey != "" && (pc.Type == "claude-api" || strings.Contains(pc.BaseURL, "anthropic.com")) {
			return os.ExpandEnv(pc.APIKey)
		}
	}
	return ""
}

// cliSkillsDir returns the skills directory path from CLIConfig.
// Mirrors skill.SkillsDir logic without requiring a full skill.AppConfig.
func cliSkillsDir(cfg *CLIConfig) string {
	if cfg.WorkspaceDir != "" {
		return filepath.Join(cfg.WorkspaceDir, "skills")
	}
	return filepath.Join(cfg.BaseDir, "skills")
}

// callAnthropicInit calls the Anthropic Messages API.
func callAnthropicInit(apiKey, system string, messages []skillInitMessage) (string, error) {
	body := skillInitRequest{
		Model:     skillInitDefaultModel,
		MaxTokens: skillInitMaxTokens,
		System:    system,
		Messages:  messages,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", anthropicMessagesURL, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", provider.AnthropicVersion)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateBytesInit(respBody, 300))
	}

	var ar skillInitResponse
	if err := json.Unmarshal(respBody, &ar); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if ar.Error != nil {
		return "", fmt.Errorf("%s: %s", ar.Error.Type, ar.Error.Message)
	}

	var text strings.Builder
	for _, block := range ar.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String(), nil
}

// truncateBytesInit converts a byte slice to string, capping at maxLen bytes.
func truncateBytesInit(b []byte, maxLen int) string {
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

// skillInitSystemPrompt returns the system prompt for the interview.
func skillInitSystemPrompt(name string) string {
	return `You are helping create a SKILL.md for a Tetora skill named "` + name + `".

Tetora skills are modular capabilities for AI agents. Each skill has a SKILL.md documenting what it does and how to use it.

## Your task

Interview the user to gather information, then generate a SKILL.md. Ask ONE question at a time. Be concise.

## Information to gather

1. What does this skill do? (one-line description)
2. Detailed purpose and use case
3. Trigger keywords (words that indicate when this skill is relevant)
4. Prerequisites/dependencies (other skills, tools, services)
5. Usage examples (commands, parameters)
6. Files it includes (scripts, configs)
7. Maintainer (agent name: ruri, hisui, kokuyou, kohaku)
8. Edge cases, limitations, anti-patterns

Skip questions the user already answered. Aim for 3-5 questions total.

## Output format

When you have enough information, output the SKILL.md inside a code block:

` + "```" + `skillmd
---
name: ` + name + `
description: One-line description
version: "1.0"
maintainer: agent-name
depends: [dep1, dep2]
triggers: [keyword1, keyword2, keyword3]
requires: [requirement1, requirement2]
---

# ` + name + `

Detailed description.

## Files

| File | Purpose |
|------|---------|
| run.py | Main script |

## Usage

` + "```" + `bash
python3 run.py --arg value
` + "```" + `

## Parameters

Explain parameters, env vars, etc.

## Edge Cases

- Known limitations
- Anti-patterns
` + "```" + `

## Rules

- Ask in the SAME LANGUAGE the user uses (繁體中文 → 繁體中文, English → English)
- Be concise — short questions, no filler
- Write documentation in the user's language; technical terms stay in English
- Start by asking what the skill does`
}

// extractSkillMD extracts SKILL.md content from AI output.
func extractSkillMD(reply string) string {
	// Look for ```skillmd ... ``` block.
	if idx := strings.Index(reply, "```skillmd\n"); idx >= 0 {
		start := idx + len("```skillmd\n")
		end := strings.Index(reply[start:], "\n```")
		if end >= 0 {
			return strings.TrimSpace(reply[start : start+end])
		}
		return strings.TrimSpace(reply[start:])
	}
	return ""
}

// writeSkillInitOutput writes the generated SKILL.md and metadata.json.
func writeSkillInitOutput(cfg *CLIConfig, name, skillMD string) {
	dir := filepath.Join(cliSkillsDir(cfg), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating dir: %v\n", err)
		os.Exit(1)
	}

	// Write SKILL.md.
	mdPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(mdPath, []byte(skillMD+"\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing SKILL.md: %v\n", err)
		os.Exit(1)
	}

	// Extract metadata from frontmatter and write metadata.json.
	meta := extractSkillInitMeta(name, skillMD)
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling metadata: %v\n", err)
		os.Exit(1)
	}
	metaPath := filepath.Join(dir, "metadata.json")
	if err := os.WriteFile(metaPath, metaData, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing metadata.json: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("Skill created: %s\n", dir)
	fmt.Printf("  SKILL.md:      %s\n", mdPath)
	fmt.Printf("  metadata.json: %s\n", metaPath)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. Add a run.py or run.sh script to %s\n", dir)
	fmt.Printf("  2. tetora skill approve %s\n", name)
}

// extractSkillInitMeta parses YAML frontmatter into skill.SkillMetadata.
func extractSkillInitMeta(name, content string) skill.SkillMetadata {
	meta := skill.SkillMetadata{
		Name:      name,
		CreatedBy: "skill-init",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if !strings.HasPrefix(content, "---") {
		return meta
	}

	end := strings.Index(content[3:], "\n---")
	if end < 0 {
		return meta
	}

	fm := content[3 : 3+end]
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, "\"'")

		switch key {
		case "description":
			meta.Description = val
		case "triggers":
			kw := initParseYAMLList(val)
			if len(kw) > 0 {
				meta.Matcher = &skill.SkillMatcher{Keywords: kw}
			}
		case "maintainer":
			meta.CreatedBy = val
		}
	}

	return meta
}

// initParseYAMLList parses a simple YAML inline list like [a, b, c].
func initParseYAMLList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	var result []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "\"'")
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
