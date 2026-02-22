package main

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"
)

// expandPrompt replaces template variables in a prompt string.
// Supported variables:
//
//	{{date}}          — current date (YYYY-MM-DD)
//	{{datetime}}      — current datetime (RFC3339)
//	{{weekday}}       — day of week (Monday, Tuesday, ...)
//	{{last_output}}   — output summary from the last run of this job
//	{{last_status}}   — status from the last run of this job
//	{{last_error}}    — error from the last run of this job
//	{{env.KEY}}       — environment variable value
//	{{memory.KEY}}    — agent memory value for the current role
//	{{knowledge_dir}} — path to knowledge base directory
//	{{skill.NAME}}    — output of named skill execution
func expandPrompt(prompt, jobID, dbPath, roleName, knowledgeDir string, cfg *Config) string {
	if !strings.Contains(prompt, "{{") {
		return prompt
	}

	now := time.Now()

	// Static replacements.
	r := strings.NewReplacer(
		"{{date}}", now.Format("2006-01-02"),
		"{{datetime}}", now.Format(time.RFC3339),
		"{{weekday}}", now.Weekday().String(),
		"{{knowledge_dir}}", knowledgeDir,
	)
	prompt = r.Replace(prompt)

	// Last job run replacements (only if jobID + dbPath are available).
	if jobID != "" && dbPath != "" &&
		(strings.Contains(prompt, "{{last_output}}") ||
			strings.Contains(prompt, "{{last_status}}") ||
			strings.Contains(prompt, "{{last_error}}")) {

		last := queryLastJobRun(dbPath, jobID)
		lastOutput := ""
		lastStatus := ""
		lastError := ""
		if last != nil {
			lastOutput = last.OutputSummary
			lastStatus = last.Status
			lastError = last.Error
		}

		r2 := strings.NewReplacer(
			"{{last_output}}", lastOutput,
			"{{last_status}}", lastStatus,
			"{{last_error}}", lastError,
		)
		prompt = r2.Replace(prompt)
	}

	// Environment variable replacements: {{env.KEY}}
	envRe := regexp.MustCompile(`\{\{env\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
	prompt = envRe.ReplaceAllStringFunc(prompt, func(match string) string {
		parts := envRe.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		return os.Getenv(parts[1])
	})

	// Agent memory replacements: {{memory.KEY}}
	if roleName != "" && dbPath != "" {
		memRe := regexp.MustCompile(`\{\{memory\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
		prompt = memRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := memRe.FindStringSubmatch(match)
			if len(parts) < 2 {
				return match
			}
			val, _ := getMemory(dbPath, roleName, parts[1])
			return val
		})
	}

	// Skill output replacements: {{skill.NAME}}
	if cfg != nil && strings.Contains(prompt, "{{skill.") {
		skillRe := regexp.MustCompile(`\{\{skill\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
		prompt = skillRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := skillRe.FindStringSubmatch(match)
			if len(parts) < 2 {
				return match
			}
			skill := getSkill(cfg, parts[1])
			if skill == nil {
				return match
			}
			result, err := executeSkill(context.Background(), *skill, nil)
			if err != nil || result.Status != "success" {
				return "(skill error)"
			}
			return strings.TrimSpace(result.Output)
		})
	}

	return prompt
}
