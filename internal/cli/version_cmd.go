package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"tetora/internal/version"
)

// HandleConfigVersionSubcommands handles version-related config subcommands.
// Returns true if the subcommand was handled.
func HandleConfigVersionSubcommands(action string, args []string) bool {
	switch action {
	case "history":
		configHistory(args)
		return true
	case "rollback":
		configRollback(args)
		return true
	case "diff":
		configDiff(args)
		return true
	case "snapshot":
		configSnapshot(args)
		return true
	case "show-version":
		cmdVersionShow(args)
		return true
	case "versions":
		cmdVersionOverview()
		return true
	}
	return false
}

// HandleWorkflowVersionSubcommands handles version-related workflow subcommands.
// Returns true if the subcommand was handled.
func HandleWorkflowVersionSubcommands(action string, args []string) bool {
	switch action {
	case "history":
		workflowHistory(args)
		return true
	case "rollback":
		workflowRollback(args)
		return true
	case "diff":
		workflowDiff(args)
		return true
	}
	return false
}

// PrintUsageVersioning returns help text for version-related commands.
func PrintUsageVersioning() string {
	var b strings.Builder
	b.WriteString("  history                                    Show version history\n")
	b.WriteString("  rollback <version-id>                      Restore to a previous version\n")
	b.WriteString("  diff <version1> <version2>                 Compare two versions\n")
	b.WriteString("  snapshot [--reason \"...\"]                   Create a manual snapshot\n")
	b.WriteString("  show-version <version-id>                  Show full content of a version\n")
	b.WriteString("  versions                                   List all versioned entities\n")
	return b.String()
}

// --- Config Version Commands ---

func configHistory(args []string) {
	cfg := LoadCLIConfig(FindConfigPath())

	limit := 20
	for i := 0; i < len(args); i++ {
		if args[i] == "--limit" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &limit)
			i++
		}
	}

	versions, err := version.QueryVersions(cfg.HistoryDB, "config", "config.json", limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(versions) == 0 {
		fmt.Println("No config version history.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tDATE\tBY\tCHANGES")
	for _, v := range versions {
		ts := v.CreatedAt
		if len(ts) > 19 {
			ts = ts[:19]
		}
		diff := v.DiffSummary
		if len(diff) > 60 {
			diff = diff[:60] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.VersionID, ts, v.ChangedBy, diff)
	}
	w.Flush()
}

func configRollback(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: tetora config rollback <version-id>")
		os.Exit(1)
	}

	cfg := LoadCLIConfig(FindConfigPath())
	configPath := FindConfigPath()
	versionID := args[0]

	prev, err := version.RestoreConfig(cfg.HistoryDB, configPath, versionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	_ = prev // previous content backed up in version history
	fmt.Printf("Config restored to version %s.\n", versionID)
	fmt.Println("Previous config was backed up in version history.")
	fmt.Println("Note: Restart the daemon for changes to take effect.")
}

func configDiff(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tetora config diff <version1> <version2>")
		os.Exit(1)
	}

	cfg := LoadCLIConfig(FindConfigPath())

	result, err := version.DiffDetail(cfg.HistoryDB, args[0], args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}

func configSnapshot(args []string) {
	cfg := LoadCLIConfig(FindConfigPath())
	configPath := FindConfigPath()

	reason := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--reason" && i+1 < len(args) {
			reason = args[i+1]
			i++
		}
	}

	if err := version.SnapshotConfig(cfg.HistoryDB, configPath, "cli", reason); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Config snapshot created.")
}

// --- Workflow Version Commands ---

func workflowHistory(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: tetora workflow history <name>")
		os.Exit(1)
	}
	name := args[0]
	cfg := LoadCLIConfig(FindConfigPath())

	limit := 20
	for i := 1; i < len(args); i++ {
		if args[i] == "--limit" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &limit)
			i++
		}
	}

	versions, err := version.QueryVersions(cfg.HistoryDB, "workflow", name, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(versions) == 0 {
		fmt.Printf("No version history for workflow %q.\n", name)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tDATE\tBY\tCHANGES")
	for _, v := range versions {
		ts := v.CreatedAt
		if len(ts) > 19 {
			ts = ts[:19]
		}
		diff := v.DiffSummary
		if len(diff) > 60 {
			diff = diff[:60] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.VersionID, ts, v.ChangedBy, diff)
	}
	w.Flush()
}

func workflowRollback(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tetora workflow rollback <name> <version-id>")
		os.Exit(1)
	}

	name := args[0]
	versionID := args[1]
	cfg := LoadCLIConfig(FindConfigPath())

	// Verify the version belongs to this workflow.
	ver, err := version.QueryByID(cfg.HistoryDB, versionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if ver.EntityName != name {
		fmt.Fprintf(os.Stderr, "Error: version %s belongs to workflow %q, not %q\n",
			versionID, ver.EntityName, name)
		os.Exit(1)
	}

	// Workflow restore requires root types (Workflow, saveWorkflow, loadWorkflowByName).
	// Delegate to the daemon API instead.
	api := cfg.NewAPIClient()
	resp, err := api.PostJSON(fmt.Sprintf("/api/workflows/%s/restore", name), map[string]string{
		"versionId": versionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: daemon not reachable — is tetora running?\n")
		fmt.Fprintf(os.Stderr, "Details: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "Error: daemon returned %s\n", resp.Status)
		os.Exit(1)
	}

	fmt.Printf("Workflow %q restored to version %s.\n", name, versionID)
}

func workflowDiff(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tetora workflow diff <version1> <version2>")
		os.Exit(1)
	}

	cfg := LoadCLIConfig(FindConfigPath())

	result, err := version.DiffDetail(cfg.HistoryDB, args[0], args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}

// --- Version Overview (all entities) ---

func cmdVersionOverview() {
	cfg := LoadCLIConfig(FindConfigPath())

	entities, err := version.QueryAllEntities(cfg.HistoryDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(entities) == 0 {
		fmt.Println("No versioned entities found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tNAME\tVERSIONS\tLAST UPDATED")
	for _, e := range entities {
		ts := e.CreatedAt
		if len(ts) > 19 {
			ts = ts[:19]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			e.EntityType, e.EntityName, e.Reason, ts)
	}
	w.Flush()
}

// --- Version Show (show full content of a version) ---

func cmdVersionShow(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: tetora config show-version <version-id>")
		os.Exit(1)
	}

	cfg := LoadCLIConfig(FindConfigPath())
	versionID := args[0]

	ver, err := version.QueryByID(cfg.HistoryDB, versionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Header info.
	fmt.Printf("Version:  %s\n", ver.VersionID)
	fmt.Printf("Type:     %s\n", ver.EntityType)
	fmt.Printf("Name:     %s\n", ver.EntityName)
	fmt.Printf("Changed:  %s by %s\n", ver.CreatedAt, ver.ChangedBy)
	if ver.Reason != "" {
		fmt.Printf("Reason:   %s\n", ver.Reason)
	}
	if ver.DiffSummary != "" {
		fmt.Printf("Changes:  %s\n", ver.DiffSummary)
	}

	// Content.
	fmt.Println("\n--- Content ---")
	if ver.EntityType == "prompt" {
		fmt.Println(ver.ContentJSON)
	} else {
		// Pretty-print JSON.
		var pretty json.RawMessage
		if json.Unmarshal([]byte(ver.ContentJSON), &pretty) == nil {
			out, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Println(string(out))
		} else {
			fmt.Println(ver.ContentJSON)
		}
	}
}
