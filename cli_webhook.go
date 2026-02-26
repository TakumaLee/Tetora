package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func cmdWebhook(args []string) {
	if len(args) == 0 {
		args = []string{"list"}
	}

	switch args[0] {
	case "list":
		cmdWebhookList()
	case "test":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: tetora webhook test <name> [json-payload]\n")
			os.Exit(1)
		}
		payload := `{"test":true}`
		if len(args) >= 3 {
			payload = args[2]
		}
		cmdWebhookTest(args[1], payload)
	case "show":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: tetora webhook show <name>\n")
			os.Exit(1)
		}
		cmdWebhookShow(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Usage: tetora webhook <list|show|test>\n")
		os.Exit(1)
	}
}

func cmdWebhookList() {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Try daemon API first.
	api := newAPIClient(cfg)
	resp, err := api.get("/webhooks/incoming")
	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var list []struct {
			Name      string `json:"name"`
			Agent      string `json:"agent"`
			Enabled   bool   `json:"enabled"`
			Template  string `json:"template,omitempty"`
			Filter    string `json:"filter,omitempty"`
			Workflow  string `json:"workflow,omitempty"`
			HasSecret bool   `json:"hasSecret"`
		}
		if json.Unmarshal(body, &list) == nil {
			if len(list) == 0 {
				fmt.Println("No incoming webhooks configured.")
				fmt.Println("\nAdd webhooks in config.json under \"incomingWebhooks\".")
				return
			}
			fmt.Printf("Incoming Webhooks (%d):\n\n", len(list))
			for _, wh := range list {
				status := "enabled"
				if !wh.Enabled {
					status = "disabled"
				}
				secret := "no"
				if wh.HasSecret {
					secret = "yes"
				}
				fmt.Printf("  %-20s  agent=%-8s  status=%-8s  secret=%s\n", wh.Name, wh.Agent, status, secret)
				if wh.Filter != "" {
					fmt.Printf("  %20s  filter: %s\n", "", wh.Filter)
				}
				if wh.Workflow != "" {
					fmt.Printf("  %20s  workflow: %s\n", "", wh.Workflow)
				}
			}
			addr := cfg.ListenAddr
			if addr == "" {
				addr = "localhost:3456"
			}
			fmt.Printf("\nEndpoint: POST http://%s/hooks/{name}\n", addr)
			return
		}
	}

	// Fallback: read from config directly.
	if len(cfg.IncomingWebhooks) == 0 {
		fmt.Println("No incoming webhooks configured.")
		fmt.Println("\nAdd webhooks in config.json under \"incomingWebhooks\".")
		return
	}
	fmt.Printf("Incoming Webhooks (%d):\n\n", len(cfg.IncomingWebhooks))
	for name, wh := range cfg.IncomingWebhooks {
		status := "enabled"
		if !wh.isEnabled() {
			status = "disabled"
		}
		secret := "no"
		if wh.Secret != "" {
			secret = "yes"
		}
		fmt.Printf("  %-20s  agent=%-8s  status=%-8s  secret=%s\n", name, wh.Agent, status, secret)
		if wh.Filter != "" {
			fmt.Printf("  %20s  filter: %s\n", "", wh.Filter)
		}
		if wh.Workflow != "" {
			fmt.Printf("  %20s  workflow: %s\n", "", wh.Workflow)
		}
	}
}

func cmdWebhookShow(name string) {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	wh, ok := cfg.IncomingWebhooks[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Webhook %q not found.\n", name)
		fmt.Fprintf(os.Stderr, "Available: %s\n", strings.Join(webhookNames(cfg), ", "))
		os.Exit(1)
	}

	fmt.Printf("Incoming Webhook: %s\n\n", name)
	fmt.Printf("  Agent:     %s\n", wh.Agent)
	fmt.Printf("  Enabled:  %v\n", wh.isEnabled())
	fmt.Printf("  Secret:   %v\n", wh.Secret != "")
	if wh.Filter != "" {
		fmt.Printf("  Filter:   %s\n", wh.Filter)
	}
	if wh.Workflow != "" {
		fmt.Printf("  Workflow: %s\n", wh.Workflow)
	}
	if wh.Template != "" {
		fmt.Printf("  Template:\n    %s\n", strings.ReplaceAll(wh.Template, "\n", "\n    "))
	}

	addr := cfg.ListenAddr
	if addr == "" {
		addr = "localhost:3456"
	}
	fmt.Printf("\n  Endpoint: POST http://%s/hooks/%s\n", addr, name)
}

func cmdWebhookTest(name, payload string) {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Validate webhook exists.
	if _, ok := cfg.IncomingWebhooks[name]; !ok {
		fmt.Fprintf(os.Stderr, "Webhook %q not found.\n", name)
		fmt.Fprintf(os.Stderr, "Available: %s\n", strings.Join(webhookNames(cfg), ", "))
		os.Exit(1)
	}

	// Validate JSON payload.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid JSON payload: %v\n", err)
		os.Exit(1)
	}

	api := newAPIClient(cfg)
	resp, err := api.postJSON("/hooks/"+name, parsed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending test webhook: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result IncomingWebhookResult
	if json.Unmarshal(body, &result) == nil {
		fmt.Printf("Status: %s\n", result.Status)
		if result.TaskID != "" {
			fmt.Printf("Task:   %s\n", result.TaskID[:8])
		}
		if result.Workflow != "" {
			fmt.Printf("Workflow: %s\n", result.Workflow)
		}
		if result.Message != "" {
			fmt.Printf("Message: %s\n", result.Message)
		}
	} else {
		fmt.Println(string(body))
	}
}

func webhookNames(cfg *Config) []string {
	var names []string
	for name := range cfg.IncomingWebhooks {
		names = append(names, name)
	}
	return names
}
