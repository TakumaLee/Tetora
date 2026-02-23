package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
)

func cmdQuick(args []string, cfg *Config) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora quick <list|run|search> [args...]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list              List all quick actions")
		fmt.Println("  run <name> [params]  Execute a quick action")
		fmt.Println("  search <query>    Search actions")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  tetora quick list")
		fmt.Println("  tetora quick run deploy")
		fmt.Println("  tetora quick run greet name=Alice age=30")
		fmt.Println("  tetora quick search code")
		return
	}

	cmd := args[0]
	switch cmd {
	case "list":
		quickList(cfg)
	case "run":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: run requires action name")
			fmt.Fprintln(os.Stderr, "Usage: tetora quick run <name> [params]")
			os.Exit(1)
		}
		quickRun(cfg, args[1], args[2:])
	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: search requires query")
			fmt.Fprintln(os.Stderr, "Usage: tetora quick search <query>")
			os.Exit(1)
		}
		quickSearch(cfg, args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		fmt.Fprintln(os.Stderr, "Use: tetora quick <list|run|search>")
		os.Exit(1)
	}
}

func quickList(cfg *Config) {
	url := fmt.Sprintf("http://%s/api/quick/list", cfg.ListenAddr)
	resp, err := httpGet(url, cfg.APIToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var actions []QuickAction
	if err := json.Unmarshal(resp, &actions); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(actions) == 0 {
		fmt.Println("No quick actions configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tLABEL\tROLE\tSHORTCUT")
	for _, a := range actions {
		role := a.Role
		if role == "" {
			role = "-"
		}
		shortcut := a.Shortcut
		if shortcut == "" {
			shortcut = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, a.Label, role, shortcut)
	}
	w.Flush()
}

func quickRun(cfg *Config, name string, paramArgs []string) {
	// Parse params from args: key=value format.
	params := make(map[string]any)
	for _, arg := range paramArgs {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "Invalid param format: %s (use key=value)\n", arg)
			os.Exit(1)
		}
		key := parts[0]
		value := parts[1]
		// Try to parse as number, otherwise treat as string.
		if v, err := parseNumber(value); err == nil {
			params[key] = v
		} else {
			params[key] = value
		}
	}

	payload := map[string]any{
		"name":   name,
		"params": params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	url := fmt.Sprintf("http://%s/api/quick/run", cfg.ListenAddr)
	resp, err := httpPost(url, cfg.APIToken, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var result TaskResult
	if err := json.Unmarshal(resp, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Status: %s\n", result.Status)
	fmt.Printf("Output:\n%s\n", result.Output)
	if result.Error != "" {
		fmt.Printf("Error: %s\n", result.Error)
	}
	fmt.Printf("Duration: %dms\n", result.DurationMs)
	fmt.Printf("Cost: $%.4f\n", result.CostUSD)
}

func quickSearch(cfg *Config, query string) {
	url := fmt.Sprintf("http://%s/api/quick/search?q=%s", cfg.ListenAddr, query)
	resp, err := httpGet(url, cfg.APIToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var actions []QuickAction
	if err := json.Unmarshal(resp, &actions); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(actions) == 0 {
		fmt.Printf("No actions found for query: %s\n", query)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tLABEL\tROLE\tSHORTCUT")
	for _, a := range actions {
		role := a.Role
		if role == "" {
			role = "-"
		}
		shortcut := a.Shortcut
		if shortcut == "" {
			shortcut = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, a.Label, role, shortcut)
	}
	w.Flush()
}

func parseNumber(s string) (any, error) {
	// Try int first, then float.
	var i int
	if _, err := fmt.Sscanf(s, "%d", &i); err == nil {
		return i, nil
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
		return f, nil
	}
	return nil, fmt.Errorf("not a number")
}

func httpGet(url, token string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return buf.Bytes(), nil
}

func httpPost(url, token string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return buf.Bytes(), nil
}
