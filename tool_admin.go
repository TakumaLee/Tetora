package main

import (
	"encoding/json"
)

// registerAdminTools registers admin/ops tools (backup, export, health,
// skills, sentori, create_skill).
// Note: most handler functions are defined in their own files (ops.go, skill_install.go, etc.).
func registerAdminTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	// --- P18.4: Self-Improving Skills ---
	if enabled("create_skill") {
		r.Register(&ToolDef{
			Name:        "create_skill",
			Description: "Create a new reusable skill (shell script or Python script) that can be used in future tasks. The skill will need approval before it can execute unless autoApprove is enabled.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Skill name (alphanumeric and hyphens only, max 64 chars)"},
					"description": {"type": "string", "description": "What the skill does"},
					"script": {"type": "string", "description": "The script content (bash or python)"},
					"language": {"type": "string", "enum": ["bash", "python"], "description": "Script language (default: bash)"},
					"matcher": {"type": "object", "properties": {"agents": {"type": "array", "items": {"type": "string"}}, "keywords": {"type": "array", "items": {"type": "string"}}}, "description": "Conditions for auto-injecting this skill"}
				},
				"required": ["name", "description", "script"]
			}`),
			Handler:     createSkillToolHandler,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	// --- P23.7: Reliability & Operations Tools ---
	if enabled("backup_now") {
		r.Register(&ToolDef{
			Name:        "backup_now",
			Description: "Trigger an immediate backup of the database",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler:     toolBackupNow,
			Builtin:     true,
			RequireAuth: true,
		})
	}
	if enabled("export_data") {
		r.Register(&ToolDef{
			Name:        "export_data",
			Description: "Export user data as a ZIP archive (GDPR compliance)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID to filter export (optional)"}
				}
			}`),
			Handler:     toolExportData,
			Builtin:     true,
			RequireAuth: true,
		})
	}
	if enabled("system_health") {
		r.Register(&ToolDef{
			Name:        "system_health",
			Description: "Get the overall system health status including database, channels, and integrations",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolSystemHealth,
			Builtin: true,
		})
	}

	// --- P27.1: Skill Install + Sentori Scanner ---
	if enabled("sentori_scan") {
		r.Register(&ToolDef{
			Name:        "sentori_scan",
			Description: "Security scan a skill script for dangerous patterns (exec, path access, exfiltration, env reads, listeners)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Skill name to scan (from store)"},
					"content": {"type": "string", "description": "Raw script content to scan (alternative to name)"}
				}
			}`),
			Handler: toolSentoriScan,
			Builtin: true,
		})
	}
	if enabled("skill_install") {
		r.Register(&ToolDef{
			Name:        "skill_install",
			Description: "Download and install a skill from a URL. Runs Sentori security scan before installation.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL to download skill from"},
					"auto_approve": {"type": "boolean", "description": "Auto-approve safe skills (default false)"}
				},
				"required": ["url"]
			}`),
			Handler:     toolSkillInstall,
			Builtin:     true,
			RequireAuth: true,
		})
	}
	if enabled("skill_search") {
		r.Register(&ToolDef{
			Name:        "skill_search",
			Description: "Search the skill registry for installable skills by keyword",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"}
				},
				"required": ["query"]
			}`),
			Handler: toolSkillSearch,
			Builtin: true,
		})
	}
}
