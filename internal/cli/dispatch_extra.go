package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// CmdDispatchList shows currently running tasks via the daemon's /tasks/running endpoint.
func CmdDispatchList(cfg *CLIConfig, api *APIClient) {
	resp, err := api.Get("/tasks/running")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach daemon at %s: %v\n", cfg.ListenAddr, err)
		fmt.Fprintln(os.Stderr, "is the daemon running? try: tetora serve")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: list failed (HTTP %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	type runningTask struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Source   string `json:"source"`
		Model    string `json:"model"`
		Timeout  string `json:"timeout"`
		Elapsed  string `json:"elapsed"`
		Prompt   string `json:"prompt,omitempty"`
		Agent    string `json:"agent,omitempty"`
		ParentID string `json:"parentId,omitempty"`
		Depth    int    `json:"depth,omitempty"`
	}

	var tasks []runningTask
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		fmt.Fprintf(os.Stderr, "error: parse response: %v\n", err)
		os.Exit(1)
	}

	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "no tasks running")
		return
	}

	fmt.Fprintf(os.Stderr, "%-12s  %-20s  %-8s  %-8s  %-6s  %s\n",
		"ID", "NAME", "MODEL", "ELAPSED", "DEPTH", "PROMPT")
	fmt.Fprintln(os.Stderr, "------------  --------------------  --------  --------  ------  ------")

	for _, t := range tasks {
		id := t.ID
		if len(id) > 12 {
			id = id[:12]
		}
		name := t.Name
		if len(name) > 20 {
			name = name[:17] + "..."
		}
		model := t.Model
		if len(model) > 8 {
			model = model[:8]
		}
		prompt := t.Prompt
		if len(prompt) > 60 {
			prompt = prompt[:57] + "..."
		}
		depth := ""
		if t.ParentID != "" {
			depth = fmt.Sprintf("%d (sub)", t.Depth)
		} else {
			depth = fmt.Sprintf("%d", t.Depth)
		}
		fmt.Fprintf(os.Stderr, "%-12s  %-20s  %-8s  %-8s  %-6s  %s\n",
			id, name, model, t.Elapsed, depth, prompt)
	}
}

// CmdDispatchSubtasks shows subtasks of a given parent job ID using the history API.
func CmdDispatchSubtasks(cfg *CLIConfig, api *APIClient, parentID string) {
	resp, err := api.Get("/history?parent_id=" + parentID + "&limit=50")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach daemon at %s: %v\n", cfg.ListenAddr, err)
		fmt.Fprintln(os.Stderr, "is the daemon running? try: tetora serve")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: subtasks failed (HTTP %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	// Decode as map to avoid importing root package types.
	var result struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "error: parse response: %v\n", err)
		os.Exit(1)
	}

	if len(result.Items) == 0 {
		fmt.Fprintf(os.Stderr, "no subtasks found for parent %s\n", parentID)
		return
	}

	fmt.Fprintf(os.Stderr, "Subtasks of %s (%d total)\n\n", parentID, result.Total)
	fmt.Fprintf(os.Stderr, "%-5s  %-12s  %-20s  %-10s  %-8s  %s\n",
		"ROW", "JOB_ID", "NAME", "STATUS", "COST", "STARTED")
	fmt.Fprintln(os.Stderr, "-----  ------------  --------------------  ----------  --------  -------")

	for i, run := range result.Items {
		id := JSONStrSafe(run["jobId"])
		if len(id) > 12 {
			id = id[:12]
		}
		name := JSONStrSafe(run["name"])
		if len(name) > 20 {
			name = name[:17] + "..."
		}
		started := JSONStrSafe(run["startedAt"])
		if len(started) > 16 {
			started = started[:16]
		}
		rowID := int(JSONFloatSafe(run["id"]))
		if rowID == 0 {
			rowID = i + 1
		}
		status := JSONStrSafe(run["status"])
		cost := JSONFloatSafe(run["costUsd"])
		fmt.Fprintf(os.Stderr, "%-5d  %-12s  %-20s  %-10s  $%-7.4f  %s\n",
			rowID, id, name, status, cost, started)
	}
}
