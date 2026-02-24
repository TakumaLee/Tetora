package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ImportMode controls how OpenClaw data is mapped to Tetora structure.
type ImportMode int

const (
	ImportAuto   ImportMode = iota // hardcoded mapping table
	ImportLLM                      // LLM-analyzed mapping
	ImportCustom                   // interactive user-driven mapping
)

// ImportMapping describes a single file/directory mapping.
type ImportMapping struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Action      string `json:"action"` // "copy", "merge", "skip", "reference"
	Category    string `json:"category,omitempty"`
	Note        string `json:"note,omitempty"`
}

// ImportResult tracks the import outcome.
type ImportResult struct {
	Mappings       []ImportMapping `json:"mappings"`
	RolesImported  int             `json:"rolesImported"`
	RulesImported  int             `json:"rulesImported"`
	MemoryFiles    int             `json:"memoryFiles"`
	SkillsImported int             `json:"skillsImported"`
	ConfigMerged   int             `json:"configMerged"`
	OtherFiles     int             `json:"otherFiles"`
	Skipped        int             `json:"skipped"`
	VaultPath      string          `json:"vaultPath,omitempty"`
	Warnings       []string        `json:"warnings,omitempty"`
	Errors         []string        `json:"errors,omitempty"`
}

// importCategoryOrder defines the display/confirmation order for categories.
var importCategoryOrder = []string{
	"agent",
	"team",
	"rules",
	"memory",
	"knowledge",
	"skills",
	"drafts",
	"content-queue",
	"research",
	"intel",
	"products",
	"projects",
	"config",
	"cron",
	"mirror",
	"skip",
	"other",
}

// importCategoryLabel returns a human-readable label for a category.
func importCategoryLabel(cat string) string {
	labels := map[string]string{
		"agent":         "Identity (SOUL files)",
		"team":          "Governance (team rules)",
		"rules":         "Rules",
		"memory":        "Memory",
		"knowledge":     "Knowledge",
		"skills":        "Skills",
		"drafts":        "Drafts",
		"content-queue": "Content Queue",
		"research":      "Research",
		"intel":         "Intel",
		"products":      "Products",
		"projects":      "Projects",
		"config":        "Config",
		"cron":          "Cron Jobs",
		"mirror":        "Mirrored Directories",
		"skip":          "Skipped",
		"other":         "Other",
	}
	if l, ok := labels[cat]; ok {
		return l
	}
	return cat
}

// cmdImportOpenClaw handles "tetora import openclaw [flags]".
func cmdImportOpenClaw() {
	fs := flag.NewFlagSet("import-openclaw", flag.ExitOnError)
	mode := fs.String("mode", "auto", "Import mode: auto, llm, custom")
	ocDirFlag := fs.String("openclaw-dir", "", "OpenClaw directory (default: ~/.openclaw)")
	dryRun := fs.Bool("dry-run", false, "Preview import without writing files")
	yes := fs.Bool("yes", false, "Skip confirmation prompts (for scripting)")
	fs.Parse(os.Args[3:])

	// Detect OpenClaw directory.
	ocDir := *ocDirFlag
	if ocDir == "" {
		ocDir = detectOpenClaw()
	}
	if ocDir == "" {
		fmt.Println("No OpenClaw installation found at ~/.openclaw/")
		fmt.Println("Use --openclaw-dir to specify the directory.")
		return
	}
	fmt.Printf("Found OpenClaw at: %s\n\n", ocDir)

	cfg, err := tryLoadConfig(findConfigPath())
	if err != nil {
		fmt.Printf("Error loading Tetora config: %v\n", err)
		fmt.Println("Run 'tetora init' first to create a config.")
		return
	}

	// Step 1: Determine import mode.
	var importMode ImportMode
	switch *mode {
	case "auto":
		importMode = ImportAuto
		fmt.Println("Step 1/4: Import mode = Auto (hardcoded mapping table)")
	case "llm":
		importMode = ImportLLM
		fmt.Println("Step 1/4: Import mode = LLM (Claude-analyzed mapping)")
	case "custom":
		importMode = ImportCustom
		fmt.Println("Step 1/4: Import mode = Custom (interactive per-category)")
	default:
		fmt.Printf("Unknown mode: %s (use auto, llm, or custom)\n", *mode)
		return
	}

	if *dryRun {
		fmt.Println("\n=== DRY RUN MODE ===")
	}

	result, err := runImportPipeline(cfg, ocDir, importMode, *dryRun, *yes)
	if err != nil {
		fmt.Printf("\nImport failed: %v\n", err)
		return
	}

	// Print report.
	printImportReport(result, *dryRun)
}

// runImportPipeline executes the 4-step import pipeline:
//  1. Copy source to /tmp staging area
//  2. Generate mappings based on mode
//  3. Confirm mappings (step-by-step per category)
//  4. Execute confirmed mappings + vault snapshot
func runImportPipeline(cfg *Config, ocDir string, mode ImportMode, dryRun, autoYes bool) (*ImportResult, error) {
	// Step 2: Copy to temp staging directory.
	fmt.Println("\nStep 2/4: Staging data")
	tmpDir, err := importStageCopy(ocDir)
	if err != nil {
		return nil, fmt.Errorf("staging: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	fmt.Printf("  Copied to staging: %s\n", tmpDir)

	// Step 3: Generate mappings.
	fmt.Println("\nStep 3/4: Generating mappings")
	mappings, err := importStageMap(cfg, tmpDir, mode)
	if err != nil {
		return nil, fmt.Errorf("mapping: %w", err)
	}

	// Detect unmapped directories and add mirror mappings.
	mirrorMappings := detectUnmappedDirectories(cfg, tmpDir, mappings)
	mappings = append(mappings, mirrorMappings...)

	fmt.Printf("  Generated %d mappings\n", len(mappings))

	if dryRun {
		fmt.Println("\nPlanned mappings:")
		printMappingsByCategory(mappings)
		return &ImportResult{Mappings: mappings}, nil
	}

	// Step 3b: Confirm mappings by category.
	confirmed := mappings
	if !autoYes {
		confirmed = confirmMappingsByCategory(mappings)
		if len(confirmed) == 0 {
			fmt.Println("\nNo mappings confirmed. Import cancelled.")
			return &ImportResult{}, nil
		}
		fmt.Printf("\n  %d mappings confirmed\n", len(confirmed))
	}

	// Step 4: Execute mappings + vault snapshot.
	fmt.Println("\nStep 4/4: Executing import")
	result, err := importStageExecute(cfg, tmpDir, confirmed)
	if err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}

	// Create vault snapshot for rollback.
	vaultPath, vaultErr := createVaultSnapshot(cfg, ocDir)
	if vaultErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("vault snapshot failed: %v", vaultErr))
	} else {
		result.VaultPath = vaultPath
	}

	return result, nil
}

// --- Stage 1: Copy to Staging ---

// importSkipDirs are directories that don't need to be staged (skipped in mapping anyway).
var importSkipDirs = map[string]bool{
	"browser":    true,
	"sd-setup":   true,
	"demo-video": true,
	"sessions":   true,
	"node_modules": true,
}

func importStageCopy(ocDir string) (string, error) {
	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("tetora-import-%d", time.Now().Unix()))

	count := 0
	err := filepath.Walk(ocDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				return nil
			}
			return err
		}

		rel, err := filepath.Rel(ocDir, path)
		if err != nil {
			return err
		}

		// Skip known large irrelevant top-level directories.
		if info.IsDir() {
			topDir := strings.SplitN(rel, string(filepath.Separator), 2)[0]
			if importSkipDirs[topDir] {
				return filepath.SkipDir
			}
		}

		target := filepath.Join(tmpDir, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if err := migCopyFile(path, target); err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				return nil
			}
			return err
		}
		count++
		if count%50 == 0 {
			fmt.Printf("\r  Copying files... %d", count)
		}
		return nil
	})
	if count > 0 {
		fmt.Printf("\r  Copied %d files to staging\n", count)
	}
	if err != nil {
		return "", fmt.Errorf("copy %s to %s: %w", ocDir, tmpDir, err)
	}
	return tmpDir, nil
}

// --- Stage 2: Generate Mappings ---

func importStageMap(cfg *Config, tmpDir string, mode ImportMode) ([]ImportMapping, error) {
	switch mode {
	case ImportAuto:
		return importAutoMap(cfg, tmpDir)
	case ImportLLM:
		return importLLMMap(cfg, tmpDir)
	case ImportCustom:
		return importCustomMap(cfg, tmpDir)
	default:
		return importAutoMap(cfg, tmpDir)
	}
}

// importAutoMap generates mappings using the hardcoded mapping table.
func importAutoMap(cfg *Config, tmpDir string) ([]ImportMapping, error) {
	var mappings []ImportMapping

	wsDir := filepath.Join(tmpDir, "workspace")

	// --- Agents: SOUL files ---

	// Root SOUL.md -> agents/{name}/SOUL.md (coordinator)
	rootSoul := filepath.Join(wsDir, "SOUL.md")
	if _, err := os.Stat(rootSoul); err == nil {
		charName := parseSoulRoleName(rootSoul, "default")
		mappings = append(mappings, ImportMapping{
			Source:      rootSoul,
			Destination: filepath.Join(cfg.AgentsDir, charName, "SOUL.md"),
			Action:      "copy",
			Category:    "agent",
			Note:        fmt.Sprintf("coordinator: %s", charName),
		})
	}

	// workspace/team/{dir}/SOUL.md -> agents/{name}/SOUL.md
	teamDir := filepath.Join(wsDir, "team")
	if entries, err := os.ReadDir(teamDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dirName := e.Name()

			// SOUL.md
			soulPath := filepath.Join(teamDir, dirName, "SOUL.md")
			if _, err := os.Stat(soulPath); err != nil {
				continue
			}
			charName := parseSoulRoleName(soulPath, dirName)
			mappings = append(mappings, ImportMapping{
				Source:      soulPath,
				Destination: filepath.Join(cfg.AgentsDir, charName, "SOUL.md"),
				Action:      "copy",
				Category:    "agent",
				Note:        fmt.Sprintf("character: %s", charName),
			})

			// AGENTS.md -> agents/{name}/AGENTS.md
			agentsPath := filepath.Join(teamDir, dirName, "AGENTS.md")
			if _, err := os.Stat(agentsPath); err == nil {
				mappings = append(mappings, ImportMapping{
					Source:      agentsPath,
					Destination: filepath.Join(cfg.AgentsDir, charName, "AGENTS.md"),
					Action:      "copy",
					Category:    "agent",
					Note:        "agent metadata",
				})
			}

			// ROLE-COLLAB-RULES.md -> agents/{name}/COLLAB-RULES.md
			collabPath := filepath.Join(teamDir, dirName, "ROLE-COLLAB-RULES.md")
			if _, err := os.Stat(collabPath); err == nil {
				mappings = append(mappings, ImportMapping{
					Source:      collabPath,
					Destination: filepath.Join(cfg.AgentsDir, charName, "COLLAB-RULES.md"),
					Action:      "copy",
					Category:    "agent",
					Note:        "collaboration rules",
				})
			}

			// workspace/team/{name}/memory/ -> agents/{name}/memory/
			memoryDir := filepath.Join(teamDir, dirName, "memory")
			if fi, err := os.Stat(memoryDir); err == nil && fi.IsDir() {
				memEntries, _ := os.ReadDir(memoryDir)
				for _, me := range memEntries {
					mappings = append(mappings, ImportMapping{
						Source:      filepath.Join(memoryDir, me.Name()),
						Destination: filepath.Join(cfg.AgentsDir, charName, "memory", me.Name()),
						Action:      "copy",
						Category:    "agent",
						Note:        "agent memory",
					})
				}
			}
		}
	}

	// --- Team governance ---

	// TEAM-DIRECTORY.md, TEAM-RULEBOOK.md, AGENTS.md -> workspace/team/
	for _, name := range []string{"TEAM-DIRECTORY.md", "TEAM-RULEBOOK.md", "AGENTS.md"} {
		src := filepath.Join(wsDir, name)
		if _, err := os.Stat(src); err == nil {
			mappings = append(mappings, ImportMapping{
				Source:      src,
				Destination: filepath.Join(cfg.WorkspaceDir, "team", name),
				Action:      "copy",
				Category:    "team",
			})
		}
	}

	// --- Top-level workspace .md files ---

	topLevelMDs := []struct {
		name     string
		dstRel   string // relative to workspace dir
		category string
	}{
		{"USER.md", "USER.md", "agent"},
		{"IDENTITY.md", "IDENTITY.md", "agent"},
		{"MEMORY.md", filepath.Join("memory", "MEMORY.md"), "memory"},
		{"BOOT.md", "BOOT.md", "rules"},
		{"TOOLS.md", "TOOLS.md", "rules"},
		{"PROJECTS.md", filepath.Join("projects", "PROJECTS.md"), "projects"},
		{"DASHBOARD.md", "DASHBOARD.md", "other"},
		{"HEARTBEAT.md", "HEARTBEAT.md", "other"},
	}
	for _, md := range topLevelMDs {
		src := filepath.Join(wsDir, md.name)
		if _, err := os.Stat(src); err == nil {
			mappings = append(mappings, ImportMapping{
				Source:      src,
				Destination: filepath.Join(cfg.WorkspaceDir, md.dstRel),
				Action:      "copy",
				Category:    md.category,
				Note:        "top-level workspace file",
			})
		}
	}

	// --- Top-level workspace .json files ---

	for _, name := range []string{"dashboard.json", "x-used-images.json"} {
		src := filepath.Join(wsDir, name)
		if _, err := os.Stat(src); err == nil {
			mappings = append(mappings, ImportMapping{
				Source:      src,
				Destination: filepath.Join(cfg.WorkspaceDir, name),
				Action:      "copy",
				Category:    "other",
				Note:        "workspace data file",
			})
		}
	}

	// --- Top-level HEARTBEAT.md (outside workspace) ---

	heartbeat := filepath.Join(tmpDir, "HEARTBEAT.md")
	if _, err := os.Stat(heartbeat); err == nil {
		mappings = append(mappings, ImportMapping{
			Source:      heartbeat,
			Destination: filepath.Join(cfg.WorkspaceDir, "HEARTBEAT.md"),
			Action:      "copy",
			Category:    "other",
			Note:        "heartbeat file",
		})
	}

	// --- Workspace directories (direct copy) ---

	copyDirs := []struct {
		srcName  string
		dstRel   string // relative to workspace dir
		category string
	}{
		{"rules", "rules", "rules"},
		{"memory", "memory", "memory"},
		{"knowledge", "knowledge", "knowledge"},
		{"skills", "skills", "skills"},
		{"drafts", "drafts", "drafts"},
		{"intel", "intel", "intel"},
		{"products", "products", "products"},
		{"content-queue", "content-queue", "content-queue"},
		{"research", "research", "research"},
		{"reference", "reference", "knowledge"},
	}

	for _, d := range copyDirs {
		srcDir := filepath.Join(wsDir, d.srcName)
		fi, err := os.Stat(srcDir)
		if err != nil || !fi.IsDir() {
			continue
		}
		entries, _ := os.ReadDir(srcDir)
		for _, e := range entries {
			// Skip SQLite files in memory dir.
			if d.category == "memory" && (strings.HasSuffix(e.Name(), ".sqlite") || strings.HasSuffix(e.Name(), ".sqlite3") || strings.HasSuffix(e.Name(), ".db")) {
				continue
			}
			src := filepath.Join(srcDir, e.Name())
			dst := filepath.Join(cfg.WorkspaceDir, d.dstRel, e.Name())
			mappings = append(mappings, ImportMapping{
				Source:      src,
				Destination: dst,
				Action:      "copy",
				Category:    d.category,
			})
		}
	}

	// --- medium-articles -> workspace/drafts/medium-published/ ---
	mediumDir := filepath.Join(wsDir, "medium-articles")
	if fi, err := os.Stat(mediumDir); err == nil && fi.IsDir() {
		entries, _ := os.ReadDir(mediumDir)
		for _, e := range entries {
			mappings = append(mappings, ImportMapping{
				Source:      filepath.Join(mediumDir, e.Name()),
				Destination: filepath.Join(cfg.WorkspaceDir, "drafts", "medium-published", e.Name()),
				Action:      "copy",
				Category:    "drafts",
				Note:        "path changed: medium-articles/ -> drafts/medium-published/",
			})
		}
	}

	// --- Projects (reference only, not copied) ---
	projectsDir := filepath.Join(wsDir, "projects")
	if fi, err := os.Stat(projectsDir); err == nil && fi.IsDir() {
		entries, _ := os.ReadDir(projectsDir)
		for _, e := range entries {
			mappings = append(mappings, ImportMapping{
				Source:      filepath.Join(projectsDir, e.Name()),
				Destination: filepath.Join(cfg.WorkspaceDir, "projects", e.Name()),
				Action:      "reference",
				Category:    "projects",
				Note:        "reference only (git repos should be cloned separately)",
			})
		}
	}

	// --- Cron jobs ---
	cronJobs := filepath.Join(tmpDir, "cron", "jobs.json")
	if _, err := os.Stat(cronJobs); err == nil {
		mappings = append(mappings, ImportMapping{
			Source:      cronJobs,
			Destination: filepath.Join(cfg.baseDir, "jobs.json"),
			Action:      "merge",
			Category:    "cron",
			Note:        "cron jobs (paths rewritten: ~/.openclaw/ -> ~/.tetora/)",
		})
	}

	// --- Config merge ---
	ocConfig := filepath.Join(tmpDir, "openclaw.json")
	if _, err := os.Stat(ocConfig); err == nil {
		mappings = append(mappings, ImportMapping{
			Source:      ocConfig,
			Destination: filepath.Join(cfg.baseDir, "config.json"),
			Action:      "merge",
			Category:    "config",
			Note:        "selective config field merge",
		})
	}

	// --- Skip: directories that don't migrate ---
	for _, skipDir := range []string{"sd-setup", "demo-video", "browser", "sessions"} {
		src := filepath.Join(tmpDir, skipDir)
		if fi, err := os.Stat(src); err == nil && fi.IsDir() {
			mappings = append(mappings, ImportMapping{
				Source:   src,
				Action:   "skip",
				Category: "skip",
				Note:     "not applicable to Tetora",
			})
		}
	}

	return mappings, nil
}

// --- LLM Import Mode ---

// importLLMMap uses Claude CLI to analyze directory structures and generate mappings.
func importLLMMap(cfg *Config, tmpDir string) ([]ImportMapping, error) {
	claudePath := cfg.ClaudePath
	if claudePath == "" {
		claudePath = detectClaude()
	}
	if _, err := os.Stat(claudePath); err != nil {
		fmt.Println("  Claude CLI not found, falling back to Auto mode")
		return importAutoMap(cfg, tmpDir)
	}

	fmt.Println("  Building directory trees...")
	sourceTree := buildDirectoryTree(tmpDir, "openclaw", 3)
	targetTree := buildTargetTree(cfg)

	prompt := fmt.Sprintf(`You are a file migration assistant. Given a source directory tree (OpenClaw) and a target directory structure (Tetora v1.3.0), generate a JSON array of file mappings.

Source tree (OpenClaw):
%s

Target structure (Tetora v1.3.0):
%s

Rules:
1. SOUL.md files in workspace/team/{name}/ map to agents/{name}/SOUL.md
2. The root workspace/SOUL.md maps to agents/{coordinator-name}/SOUL.md
3. workspace/memory/ maps to workspace/memory/ (skip .sqlite/.db files)
4. workspace/rules/ maps to workspace/rules/
5. workspace/knowledge/ maps to workspace/knowledge/
6. workspace/medium-articles/ maps to workspace/drafts/medium-published/
7. workspace/projects/ entries are "reference" action (not copied)
8. cron/jobs.json maps to jobs.json with "merge" action
9. openclaw.json maps to config.json with "merge" action
10. Directories like sd-setup, demo-video, browser, sessions should be "skip" action
11. Unrecognized workspace dirs should map to workspace/{dirname}/

Output ONLY a JSON array of objects with these fields:
- source: relative path from OpenClaw root
- destination: absolute path under Tetora
- action: "copy", "merge", "skip", or "reference"
- category: "agent", "team", "rules", "memory", "knowledge", "skills", "drafts", "config", "cron", "projects", "other", "skip"
- note: optional explanation

Output raw JSON only, no markdown fences.`, sourceTree, targetTree)

	fmt.Println("  Sending to Claude CLI for analysis...")
	cmd := exec.Command(claudePath, "-p", prompt)
	out, err := cmd.Output()
	if err != nil {
		fmt.Printf("  Claude CLI error: %v, falling back to Auto mode\n", err)
		return importAutoMap(cfg, tmpDir)
	}

	// Parse response as JSON array.
	var rawMappings []struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
		Action      string `json:"action"`
		Category    string `json:"category"`
		Note        string `json:"note"`
	}

	// Try to extract JSON from response (may have text around it).
	jsonStr := extractJSON(string(out))
	if err := json.Unmarshal([]byte(jsonStr), &rawMappings); err != nil {
		fmt.Printf("  Failed to parse LLM response: %v\n  Falling back to Auto mode\n", err)
		return importAutoMap(cfg, tmpDir)
	}

	// Convert to ImportMapping with absolute paths.
	home, _ := os.UserHomeDir()
	tetoraDir := filepath.Join(home, ".tetora")
	var mappings []ImportMapping
	for _, rm := range rawMappings {
		m := ImportMapping{
			Source:   filepath.Join(tmpDir, rm.Source),
			Action:   rm.Action,
			Category: rm.Category,
			Note:     rm.Note,
		}
		// Resolve destination path.
		if rm.Destination != "" {
			if filepath.IsAbs(rm.Destination) {
				m.Destination = rm.Destination
			} else {
				m.Destination = filepath.Join(tetoraDir, rm.Destination)
			}
		}
		// Validate source exists.
		if _, err := os.Stat(m.Source); err != nil && m.Action != "skip" {
			continue
		}
		// Validate action.
		switch m.Action {
		case "copy", "merge", "skip", "reference":
			// ok
		default:
			m.Action = "copy"
		}
		mappings = append(mappings, m)
	}

	fmt.Printf("  LLM generated %d mappings\n", len(mappings))
	return mappings, nil
}

// --- Custom Import Mode ---

// importCustomMap starts with auto mappings and lets user customize per-category.
func importCustomMap(cfg *Config, tmpDir string) ([]ImportMapping, error) {
	// Start with auto mappings as baseline.
	autoMappings, err := importAutoMap(cfg, tmpDir)
	if err != nil {
		return nil, err
	}

	// Group by category.
	groups := groupMappingsByCategory(autoMappings)
	scanner := bufio.NewScanner(os.Stdin)

	var finalMappings []ImportMapping
	for _, cat := range importCategoryOrder {
		catMappings, ok := groups[cat]
		if !ok || len(catMappings) == 0 {
			continue
		}

		fmt.Printf("\n  %s -- %d items\n", importCategoryLabel(cat), len(catMappings))
		for _, m := range catMappings {
			src := filepath.Base(m.Source)
			if m.Action == "skip" {
				fmt.Printf("    [skip] %s\n", src)
			} else {
				fmt.Printf("    %s -> %s\n", src, shortPath(m.Destination))
			}
		}

		fmt.Printf("  Action? [K]eep / [S]kip / [C]ustom path: ")
		scanner.Scan()
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))

		switch {
		case answer == "s" || answer == "skip":
			// Skip entire category.
			for _, m := range catMappings {
				m.Action = "skip"
				m.Note = "skipped by user"
				finalMappings = append(finalMappings, m)
			}
		case answer == "c" || answer == "custom":
			// Custom destination for the category.
			fmt.Printf("  New destination directory (relative to workspace): ")
			scanner.Scan()
			newDst := strings.TrimSpace(scanner.Text())
			if newDst == "" {
				finalMappings = append(finalMappings, catMappings...)
				continue
			}
			for _, m := range catMappings {
				if m.Action == "skip" || m.Action == "reference" {
					finalMappings = append(finalMappings, m)
					continue
				}
				// Remap destination.
				m.Destination = filepath.Join(cfg.WorkspaceDir, newDst, filepath.Base(m.Source))
				m.Note = "custom path"
				finalMappings = append(finalMappings, m)
			}
		default:
			// Keep defaults.
			finalMappings = append(finalMappings, catMappings...)
		}
	}

	// Save custom rules to vault for reuse.
	saveCustomRules(cfg, tmpDir, finalMappings)

	return finalMappings, nil
}

// saveCustomRules saves the custom mapping overrides to vault for future reference.
func saveCustomRules(cfg *Config, tmpDir string, mappings []ImportMapping) {
	ts := time.Now().Format("20060102-150405")
	dir := filepath.Join(cfg.VaultDir, fmt.Sprintf("openclaw-%s", ts))
	os.MkdirAll(dir, 0o755)
	data, err := json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(dir, "custom-rules.json")
	os.WriteFile(path, append(data, '\n'), 0o644)
	fmt.Printf("  Custom rules saved: %s\n", path)
}

// --- Interactive Confirmation System ---

// confirmMappingsByCategory groups mappings by category and prompts for each group.
func confirmMappingsByCategory(mappings []ImportMapping) []ImportMapping {
	groups := groupMappingsByCategory(mappings)
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("\n=== Confirm Import by Category ===")

	var confirmed []ImportMapping
	for _, cat := range importCategoryOrder {
		catMappings, ok := groups[cat]
		if !ok || len(catMappings) == 0 {
			continue
		}

		// Count non-skip items.
		activeCount := 0
		for _, m := range catMappings {
			if m.Action != "skip" {
				activeCount++
			}
		}

		fmt.Printf("\n  %s -- %d items\n", importCategoryLabel(cat), len(catMappings))
		for _, m := range catMappings {
			src := filepath.Base(m.Source)
			switch m.Action {
			case "skip":
				fmt.Printf("    [skip] %s", src)
				if m.Note != "" {
					fmt.Printf(" (%s)", m.Note)
				}
				fmt.Println()
			case "reference":
				fmt.Printf("    [ref]  %s -> %s\n", src, shortPath(m.Destination))
			case "merge":
				fmt.Printf("    [merge] %s -> %s", src, shortPath(m.Destination))
				if m.Note != "" {
					fmt.Printf(" (%s)", m.Note)
				}
				fmt.Println()
			default:
				fmt.Printf("    %s -> %s", src, shortPath(m.Destination))
				if m.Note != "" && strings.HasPrefix(m.Note, "path changed") {
					fmt.Printf("\n      ! %s", m.Note)
				}
				fmt.Println()
			}
		}

		if cat == "skip" {
			// Auto-confirm skips.
			confirmed = append(confirmed, catMappings...)
			continue
		}

		fmt.Printf("  Apply? [Y/n]: ")
		scanner.Scan()
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if answer == "n" || answer == "no" {
			fmt.Printf("  Skipped %s\n", importCategoryLabel(cat))
			continue
		}
		confirmed = append(confirmed, catMappings...)
	}

	return confirmed
}

// groupMappingsByCategory groups mappings by their category.
func groupMappingsByCategory(mappings []ImportMapping) map[string][]ImportMapping {
	groups := make(map[string][]ImportMapping)
	for _, m := range mappings {
		cat := m.Category
		if cat == "" {
			cat = "other"
		}
		groups[cat] = append(groups[cat], m)
	}
	return groups
}

// --- Directory Mirroring ---

// detectUnmappedDirectories finds directories in the source that are not covered by any mapping.
// Unrecognized dirs are mirrored to workspace/{dirname}/.
func detectUnmappedDirectories(cfg *Config, tmpDir string, existingMappings []ImportMapping) []ImportMapping {
	// Build set of all source paths that are already mapped.
	mappedSources := make(map[string]bool)
	for _, m := range existingMappings {
		// Normalize to relative path from tmpDir.
		rel, err := filepath.Rel(tmpDir, m.Source)
		if err != nil {
			continue
		}
		// Track the top-level directory.
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		mappedSources[parts[0]] = true
		// Also track workspace sub-paths.
		if len(parts) > 1 && parts[0] == "workspace" {
			wsParts := strings.SplitN(parts[1], string(filepath.Separator), 2)
			mappedSources[filepath.Join("workspace", wsParts[0])] = true
		}
	}

	// Known directories that are handled (even if empty or via special logic).
	knownDirs := map[string]bool{
		"workspace":    true, // parent, always handled
		"cron":         true,
		"openclaw.json": true,
		"sd-setup":     true,
		"demo-video":   true,
		"browser":      true,
		"sessions":     true,
	}
	knownWsDirs := map[string]bool{
		"team":            true,
		"rules":           true,
		"memory":          true,
		"knowledge":       true,
		"skills":          true,
		"drafts":          true,
		"intel":           true,
		"products":        true,
		"content-queue":   true,
		"research":        true,
		"projects":        true,
		"medium-articles": true,
		"reference":       true,
	}

	// Known path-changed directories.
	pathChanges := map[string]string{
		"medium-articles": "drafts/medium-published",
	}

	var mirrors []ImportMapping

	// Scan top-level.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		if knownDirs[name] || mappedSources[name] {
			continue
		}
		if !e.IsDir() {
			continue
		}
		// Unknown top-level dir -> mirror to workspace.
		mirrors = append(mirrors, ImportMapping{
			Source:      filepath.Join(tmpDir, name),
			Destination: filepath.Join(cfg.WorkspaceDir, name),
			Action:      "copy",
			Category:    "mirror",
			Note:        "mirrored (unrecognized top-level directory)",
		})
	}

	// Scan workspace sub-directories.
	wsDir := filepath.Join(tmpDir, "workspace")
	wsEntries, err := os.ReadDir(wsDir)
	if err != nil {
		return mirrors
	}
	for _, e := range wsEntries {
		name := e.Name()
		wsPath := filepath.Join("workspace", name)
		if knownWsDirs[name] || mappedSources[wsPath] {
			continue
		}
		// Skip non-directory entries (handled by top-level .md/.json mappings above).
		if !e.IsDir() {
			continue
		}
		// Check if this is a known path-changed directory.
		if newPath, ok := pathChanges[name]; ok {
			fmt.Printf("  ! Path changed: %s/ -> workspace/%s/\n", name, newPath)
			fmt.Printf("    Update any references to the old path.\n")
			continue
		}
		// Unknown workspace dir -> mirror.
		mirrors = append(mirrors, ImportMapping{
			Source:      filepath.Join(wsDir, name),
			Destination: filepath.Join(cfg.WorkspaceDir, name),
			Action:      "copy",
			Category:    "mirror",
			Note:        fmt.Sprintf("mirrored: workspace/%s/", name),
		})
	}

	return mirrors
}

// --- Stage 3: Execute Mappings ---

func importStageExecute(cfg *Config, tmpDir string, mappings []ImportMapping) (*ImportResult, error) {
	result := &ImportResult{Mappings: mappings}
	configPath := filepath.Join(cfg.baseDir, "config.json")

	// Ensure v1.3.0 directories exist.
	if err := initDirectories(cfg); err != nil {
		return nil, fmt.Errorf("init directories: %w", err)
	}

	for i, m := range mappings {
		switch m.Action {
		case "copy":
			if err := executeCopyMapping(m); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("[%s] %s: %v", m.Category, filepath.Base(m.Source), err))
				continue
			}
			trackImportCategory(result, m)
			fmt.Printf("  [%s] %s\n", m.Category, filepath.Base(m.Destination))

		case "merge":
			switch m.Category {
			case "config":
				count, warnings := importMergeConfig(cfg, m.Source)
				result.ConfigMerged = count
				result.Warnings = append(result.Warnings, warnings...)
				fmt.Printf("  [config] merged %d fields\n", count)

			case "cron":
				count, warnings := importMergeCronJobs(cfg, m.Source)
				result.OtherFiles += count
				result.Warnings = append(result.Warnings, warnings...)
				fmt.Printf("  [cron] merged %d jobs\n", count)
			}

		case "reference":
			result.OtherFiles++
			fmt.Printf("  [%s] %s (reference only)\n", m.Category, filepath.Base(m.Source))

		case "skip":
			result.Skipped++
		}
		_ = i
	}

	// Register imported roles in config.json.
	registerImportedRoles(cfg, configPath, result)

	return result, nil
}

// executeCopyMapping copies a single source to destination.
func executeCopyMapping(m ImportMapping) error {
	srcInfo, err := os.Stat(m.Source)
	if err != nil {
		return fmt.Errorf("source not found: %s", m.Source)
	}

	if srcInfo.IsDir() {
		return copyDir(m.Source, m.Destination)
	}

	if err := os.MkdirAll(filepath.Dir(m.Destination), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return migCopyFile(m.Source, m.Destination)
}

// trackImportCategory increments the appropriate counter in ImportResult.
func trackImportCategory(result *ImportResult, m ImportMapping) {
	switch m.Category {
	case "agent":
		if strings.HasSuffix(m.Destination, "SOUL.md") {
			result.RolesImported++
		}
	case "rules", "team":
		result.RulesImported++
	case "memory":
		result.MemoryFiles++
	case "skills":
		result.SkillsImported++
	default:
		result.OtherFiles++
	}
}

// registerImportedRoles adds imported SOUL files as roles in config.json.
func registerImportedRoles(cfg *Config, configPath string, result *ImportResult) {
	for _, m := range result.Mappings {
		if m.Action != "copy" || m.Category != "agent" || !strings.HasSuffix(m.Destination, "SOUL.md") {
			continue
		}
		roleName := filepath.Base(filepath.Dir(m.Destination))
		if _, exists := cfg.Roles[roleName]; exists {
			continue // already registered
		}
		desc := parseSoulDescription(m.Source)
		if desc == "" {
			desc = fmt.Sprintf("Imported from OpenClaw (%s)", roleName)
		}
		rc := RoleConfig{
			SoulFile:       "SOUL.md",
			Model:          cfg.DefaultModel,
			Description:    desc,
			PermissionMode: "acceptEdits",
			ToolProfile:    "standard",
		}
		if err := updateConfigRoles(configPath, roleName, &rc); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("register role %s: %v", roleName, err))
		} else {
			fmt.Printf("  [role] registered: %s\n", roleName)
		}
	}

	// Enable smart dispatch when multiple roles exist.
	if result.RolesImported > 1 {
		enableSmartDispatch(configPath)
	}
}

// --- Vault Snapshot ---

// createVaultSnapshot copies the original OpenClaw directory to vault/ for rollback.
func createVaultSnapshot(cfg *Config, ocDir string) (string, error) {
	ts := time.Now().Format("20060102-150405")
	vaultPath := filepath.Join(cfg.VaultDir, fmt.Sprintf("openclaw-%s", ts))
	if err := os.MkdirAll(cfg.VaultDir, 0o755); err != nil {
		return "", err
	}
	if err := copyDir(ocDir, vaultPath); err != nil {
		return "", fmt.Errorf("vault snapshot: %w", err)
	}
	fmt.Printf("  Vault snapshot: %s\n", vaultPath)
	return vaultPath, nil
}

// --- Config Merge ---

// importMergeConfig merges relevant fields from openclaw.json into the running config.
func importMergeConfig(cfg *Config, ocConfigPath string) (int, []string) {
	data, err := os.ReadFile(ocConfigPath)
	if err != nil {
		return 0, []string{fmt.Sprintf("read openclaw.json: %v", err)}
	}

	var oc map[string]any
	if err := json.Unmarshal(data, &oc); err != nil {
		return 0, []string{fmt.Sprintf("parse openclaw.json: %v", err)}
	}

	var warnings []string
	merged := 0

	// Default model.
	if model := getNestedString(oc, "agents", "defaults", "model", "primary"); model != "" {
		if cfg.DefaultModel == "" || cfg.DefaultModel == "sonnet" {
			cfg.DefaultModel = stripModelPrefix(model)
			merged++
		}
	}

	// Max concurrent.
	if mc := getNestedInt(oc, "agents", "defaults", "maxConcurrent"); mc > 0 && cfg.MaxConcurrent <= 3 {
		cfg.MaxConcurrent = mc
		merged++
	}

	// Tokens are sensitive — just warn.
	if token := getNestedString(oc, "channels", "telegram", "botToken"); token != "" {
		warnings = append(warnings, fmt.Sprintf("telegram.botToken found (%s) -- add manually if needed", maskSecret(token)))
	}
	if token := getNestedString(oc, "channels", "discord", "token"); token != "" {
		warnings = append(warnings, fmt.Sprintf("discord.token found (%s) -- add manually if needed", maskSecret(token)))
	}

	return merged, warnings
}

// importMergeCronJobs merges OpenClaw cron jobs into Tetora jobs.json.
// Rewrites ~/.openclaw/ paths to ~/.tetora/.
func importMergeCronJobs(cfg *Config, cronPath string) (int, []string) {
	data, err := os.ReadFile(cronPath)
	if err != nil {
		return 0, []string{fmt.Sprintf("read cron jobs: %v", err)}
	}

	// Rewrite OpenClaw paths to Tetora paths.
	content := string(data)
	content = strings.ReplaceAll(content, "~/.openclaw/", "~/.tetora/")
	content = strings.ReplaceAll(content, ".openclaw/", ".tetora/")

	// OpenClaw uses array of job objects.
	var ocJobs []map[string]any
	if err := json.Unmarshal([]byte(content), &ocJobs); err != nil {
		return 0, []string{fmt.Sprintf("parse cron jobs: %v", err)}
	}

	// Read existing Tetora jobs.
	var tetoraJobs []map[string]any
	if existing, err := os.ReadFile(cfg.JobsFile); err == nil {
		json.Unmarshal(existing, &tetoraJobs)
	}

	// Merge: add OpenClaw jobs that don't already exist (by name).
	existingNames := make(map[string]bool)
	for _, j := range tetoraJobs {
		if name, ok := j["name"].(string); ok {
			existingNames[name] = true
		}
	}

	added := 0
	var warnings []string
	for _, job := range ocJobs {
		name, _ := job["name"].(string)
		if name == "" {
			continue
		}
		if existingNames[name] {
			warnings = append(warnings, fmt.Sprintf("cron job %q already exists, skipping", name))
			continue
		}
		tetoraJobs = append(tetoraJobs, job)
		added++
	}

	if added > 0 {
		out, err := json.MarshalIndent(tetoraJobs, "", "  ")
		if err != nil {
			return 0, []string{fmt.Sprintf("marshal jobs: %v", err)}
		}
		if err := os.WriteFile(cfg.JobsFile, append(out, '\n'), 0o644); err != nil {
			return 0, []string{fmt.Sprintf("write jobs: %v", err)}
		}
	}

	return added, warnings
}

// --- Directory Tree Helpers ---

// buildDirectoryTree creates a text tree representation of a directory.
func buildDirectoryTree(root, label string, maxDepth int) string {
	var sb strings.Builder
	sb.WriteString(label + "/\n")
	buildTreeRecursive(&sb, root, "", maxDepth, 0)
	return sb.String()
}

func buildTreeRecursive(sb *strings.Builder, dir, prefix string, maxDepth, depth int) {
	if depth >= maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	// Sort entries: dirs first, then files.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	for i, e := range entries {
		isLast := i == len(entries)-1
		connector := "├── "
		childPrefix := "│   "
		if isLast {
			connector = "└── "
			childPrefix = "    "
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		sb.WriteString(prefix + connector + name + "\n")
		if e.IsDir() {
			buildTreeRecursive(sb, filepath.Join(dir, e.Name()), prefix+childPrefix, maxDepth, depth+1)
		}
	}
}

// buildTargetTree creates a text representation of the Tetora v1.3.0 target structure.
func buildTargetTree(cfg *Config) string {
	return `~/.tetora/
├── agents/{name}/SOUL.md
├── workspace/
│   ├── rules/
│   ├── memory/
│   ├── team/
│   ├── knowledge/
│   ├── drafts/
│   ├── intel/
│   ├── products/
│   ├── projects/
│   ├── content-queue/
│   ├── research/
│   └── skills/
├── runtime/
│   ├── sessions/
│   ├── outputs/
│   ├── logs/
│   ├── cache/
│   ├── security/
│   └── cron-runs/
├── dbs/
├── vault/
└── media/`
}

// --- Display Helpers ---

// printMappingsByCategory prints mappings grouped by category.
func printMappingsByCategory(mappings []ImportMapping) {
	groups := groupMappingsByCategory(mappings)
	for _, cat := range importCategoryOrder {
		catMappings, ok := groups[cat]
		if !ok || len(catMappings) == 0 {
			continue
		}
		fmt.Printf("\n  %s (%d)\n", importCategoryLabel(cat), len(catMappings))
		for _, m := range catMappings {
			switch m.Action {
			case "skip":
				fmt.Printf("    [skip] %s (%s)\n", filepath.Base(m.Source), m.Note)
			case "reference":
				fmt.Printf("    [ref]  %s -> %s\n", filepath.Base(m.Source), shortPath(m.Destination))
			case "merge":
				line := fmt.Sprintf("    [merge] %s -> %s", filepath.Base(m.Source), shortPath(m.Destination))
				if m.Note != "" {
					line += fmt.Sprintf(" (%s)", m.Note)
				}
				fmt.Println(line)
			default:
				line := fmt.Sprintf("    %s -> %s", filepath.Base(m.Source), shortPath(m.Destination))
				if m.Note != "" {
					line += fmt.Sprintf(" (%s)", m.Note)
				}
				fmt.Println(line)
			}
		}
	}
}

func printMappings(mappings []ImportMapping) {
	for _, m := range mappings {
		switch m.Action {
		case "skip":
			fmt.Printf("  [skip] %s (%s)\n", filepath.Base(m.Source), m.Note)
		case "reference":
			fmt.Printf("  [ref]  %s -> %s\n", filepath.Base(m.Source), m.Destination)
		default:
			line := fmt.Sprintf("  [%s] %s -> %s", m.Action, filepath.Base(m.Source), m.Destination)
			if m.Note != "" {
				line += fmt.Sprintf(" (%s)", m.Note)
			}
			fmt.Println(line)
		}
	}
}

func printImportReport(result *ImportResult, dryRun bool) {
	fmt.Println("\n=== Import Report ===")
	fmt.Printf("Roles imported:   %d\n", result.RolesImported)
	fmt.Printf("Rules imported:   %d\n", result.RulesImported)
	fmt.Printf("Memory files:     %d\n", result.MemoryFiles)
	fmt.Printf("Skills imported:  %d\n", result.SkillsImported)
	fmt.Printf("Config merged:    %d\n", result.ConfigMerged)
	fmt.Printf("Other files:      %d\n", result.OtherFiles)
	if result.Skipped > 0 {
		fmt.Printf("Skipped:          %d\n", result.Skipped)
	}
	if result.VaultPath != "" {
		fmt.Printf("Vault snapshot:   %s\n", result.VaultPath)
	}

	if len(result.Warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, w := range result.Warnings {
			fmt.Printf("  ! %s\n", w)
		}
	}
	if len(result.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range result.Errors {
			fmt.Printf("  x %s\n", e)
		}
	}

	if !dryRun && len(result.Errors) == 0 {
		fmt.Println("\nImport complete!")
	}
}

// shortPath abbreviates ~/.tetora/ paths for display.
func shortPath(p string) string {
	home, _ := os.UserHomeDir()
	tetoraDir := filepath.Join(home, ".tetora")
	if strings.HasPrefix(p, tetoraDir) {
		return "~/.tetora" + p[len(tetoraDir):]
	}
	return p
}
