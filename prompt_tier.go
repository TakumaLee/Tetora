package main

import (
	"os"
	"path/filepath"
	"strings"
)

// buildTieredPrompt constructs a system prompt based on request complexity.
// This replaces the inline assembly in runTask() and runSingleTask().
//
// Tiering strategy:
//
//	Simple:   soul (truncated 4KB) only — no reflection, style, citation, rules, knowledge
//	Standard: full soul + 1 reflection + citation + rules index + knowledge index
//	Complex:  full soul + 3 reflections + citation + writing style + full rules + full knowledge
func buildTieredPrompt(cfg *Config, task *Task, agentName string, complexity RequestComplexity) {
	// --- Provider type check ---
	// If the provider is "claude-code", only inject soul prompt and skip everything else.
	providerType := ""
	pName := resolveProviderName(cfg, *task, agentName)
	if pc, ok := cfg.Providers[pName]; ok {
		providerType = pc.Type
	}

	// --- 1. Soul/Agent prompt (always loaded) ---
	if agentName != "" {
		soulPrompt := loadSoulFile(cfg, agentName)
		if soulPrompt == "" {
			if sp, err := loadAgentPrompt(cfg, agentName); err == nil {
				soulPrompt = sp
			}
		}
		if soulPrompt != "" {
			switch complexity {
			case ComplexitySimple:
				task.SystemPrompt = truncateToChars(soulPrompt, 4000)
			default:
				task.SystemPrompt = truncateToChars(soulPrompt, cfg.PromptBudget.soulMaxOrDefault())
			}
		}
	}

	// --- 2. Workspace directory setup (always) ---
	if agentName != "" {
		ws := resolveWorkspace(cfg, agentName)
		if ws.Dir != "" {
			task.Workdir = ws.Dir
		}
		task.AddDirs = append(task.AddDirs, cfg.baseDir)
	}

	// --- 3. Agent config overrides (always) ---
	if agentName != "" {
		if rc, ok := cfg.Agents[agentName]; ok {
			if task.Model == cfg.DefaultModel && rc.Model != "" {
				task.Model = rc.Model
			}
			if task.PermissionMode == cfg.DefaultPermissionMode && rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}

	// --- 4. Inject global defaultAddDirs (always) ---
	for _, d := range cfg.DefaultAddDirs {
		if strings.HasPrefix(d, "~/") {
			home, _ := os.UserHomeDir()
			d = filepath.Join(home, d[2:])
		} else if d == "~" {
			home, _ := os.UserHomeDir()
			d = home
		}
		task.AddDirs = append(task.AddDirs, d)
	}

	// If provider is claude-code, only the soul prompt is needed; skip everything else.
	if providerType == "claude-code" {
		return
	}

	// --- 5. Knowledge dir ---
	// Simple: skip. Standard/Complex: inject if exists and < 50KB.
	if complexity != ComplexitySimple {
		if cfg.KnowledgeDir != "" && knowledgeDirHasFiles(cfg.KnowledgeDir) && estimateDirSize(cfg.KnowledgeDir) <= 50*1024 {
			task.AddDirs = append(task.AddDirs, cfg.KnowledgeDir)
		}
	}

	// --- 6. Reflection ---
	// Simple: skip. Standard: limit 1. Complex: limit 3.
	if complexity != ComplexitySimple && cfg.Reflection.Enabled && agentName != "" && cfg.HistoryDB != "" {
		limit := 1
		if complexity == ComplexityComplex {
			limit = 3
		}
		if refCtx := buildReflectionContext(cfg.HistoryDB, agentName, limit); refCtx != "" {
			task.SystemPrompt += "\n\n" + refCtx
		}
	}

	// --- 7. Writing Style ---
	// Simple/Standard: skip. Complex: inject.
	if complexity == ComplexityComplex && cfg.WritingStyle.Enabled {
		style := loadWritingStyle(cfg)
		if style != "" {
			task.SystemPrompt += "\n\n## Writing Style\n\n" + style
		}
	}

	// --- 8. Citation Rules ---
	// Simple: skip. Standard/Complex: inject.
	if complexity != ComplexitySimple && cfg.Citation.Enabled {
		citationFmt := cfg.Citation.Format
		if citationFmt == "" {
			citationFmt = "bracket"
		}
		var citationRule string
		switch citationFmt {
		case "footnote":
			citationRule = "When using information from knowledge_search, note_search, or web_search results, " +
				"add numbered footnotes at the end of your response. Format: [1] source_name"
		case "inline":
			citationRule = "When using information from knowledge_search, note_search, or web_search results, " +
				"cite sources inline immediately after the relevant information. Format: (source: source_name)"
		default: // "bracket"
			citationRule = "When using information from knowledge_search, note_search, or web_search results, " +
				"cite the source at the end of your response. Format: [source_name]"
		}
		task.SystemPrompt += "\n\n## Citation Rules\n" + citationRule
	}

	// --- 9. Workspace Content Injection ---
	// Simple: skip entirely. Standard/Complex: call injectWorkspaceContent.
	if complexity != ComplexitySimple {
		injectWorkspaceContent(cfg, task, agentName)
	}

	// --- 10. AddDirs control ---
	// Simple: clear AddDirs, only keep baseDir.
	// Standard: keep workspace dir only (+ baseDir).
	// Complex: keep all.
	if complexity == ComplexitySimple {
		task.AddDirs = []string{cfg.baseDir}
	} else if complexity == ComplexityStandard {
		var kept []string
		ws := resolveWorkspace(cfg, agentName)
		for _, d := range task.AddDirs {
			if d == cfg.baseDir || d == ws.Dir {
				kept = append(kept, d)
			}
		}
		task.AddDirs = kept
	}
	// Complex: keep all (no filtering).

	// --- 12. Enforce total budget ---
	totalMax := cfg.PromptBudget.totalMaxOrDefault()
	if len(task.SystemPrompt) > totalMax {
		task.SystemPrompt = truncateToChars(task.SystemPrompt, totalMax)
	}
}

// truncateToChars truncates a string to maxChars, trying to cut at a newline boundary.
func truncateToChars(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	cut := s[:maxChars]
	if idx := strings.LastIndex(cut, "\n"); idx > maxChars*3/4 {
		cut = cut[:idx]
	}
	return cut + "\n\n[... truncated ...]"
}
