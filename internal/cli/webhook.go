package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// incomingWebhookConfig is a CLI-local copy of root's IncomingWebhookConfig.
type incomingWebhookConfig struct {
	Agent    string `json:"agent"`
	Template string `json:"template,omitempty"`
	Secret   string `json:"secret,omitempty"`
	Filter   string `json:"filter,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

func (c incomingWebhookConfig) isEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// incomingWebhookResult is a CLI-local copy of root's IncomingWebhookResult.
type incomingWebhookResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	TaskID   string `json:"taskId,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Workflow string `json:"workflow,omitempty"`
	Message  string `json:"message,omitempty"`
}

func CmdWebhook(args []string) {
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
	cfg := LoadCLIConfig(FindConfigPath())

	// Try daemon API first.
	api := cfg.NewAPIClient()
	resp, err := api.Get("/webhooks/incoming")
	if err == nil && resp.StatusCode == 200 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var list []struct {
			Name      string `json:"name"`
			Agent     string `json:"agent"`
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
	var webhooks map[string]incomingWebhookConfig
	if cfg.IncomingWebhooks != nil {
		if err := json.Unmarshal(mustMarshalRawMap(cfg.IncomingWebhooks), &webhooks); err != nil {
			webhooks = nil
		}
	}
	if len(webhooks) == 0 {
		fmt.Println("No incoming webhooks configured.")
		fmt.Println("\nAdd webhooks in config.json under \"incomingWebhooks\".")
		return
	}
	fmt.Printf("Incoming Webhooks (%d):\n\n", len(webhooks))
	for name, wh := range webhooks {
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
	cfg := LoadCLIConfig(FindConfigPath())

	var webhooks map[string]incomingWebhookConfig
	if cfg.IncomingWebhooks != nil {
		json.Unmarshal(mustMarshalRawMap(cfg.IncomingWebhooks), &webhooks)
	}

	wh, ok := webhooks[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Webhook %q not found.\n", name)
		fmt.Fprintf(os.Stderr, "Available: %s\n", strings.Join(webhookNames(webhooks), ", "))
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
	cfg := LoadCLIConfig(FindConfigPath())

	// Validate webhook exists in config.
	var webhooks map[string]incomingWebhookConfig
	if cfg.IncomingWebhooks != nil {
		json.Unmarshal(mustMarshalRawMap(cfg.IncomingWebhooks), &webhooks)
	}
	if _, ok := webhooks[name]; !ok {
		fmt.Fprintf(os.Stderr, "Webhook %q not found.\n", name)
		fmt.Fprintf(os.Stderr, "Available: %s\n", strings.Join(webhookNames(webhooks), ", "))
		os.Exit(1)
	}

	// Validate JSON payload.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid JSON payload: %v\n", err)
		os.Exit(1)
	}

	api := cfg.NewAPIClient()
	resp, err := api.PostJSON("/hooks/"+name, parsed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending test webhook: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result incomingWebhookResult
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

func webhookNames(webhooks map[string]incomingWebhookConfig) []string {
	var names []string
	for name := range webhooks {
		names = append(names, name)
	}
	return names
}

// mustMarshalRawMap re-encodes a map[string]json.RawMessage so it can be
// unmarshalled into a concrete type. It never fails for a valid RawMessage map.
func mustMarshalRawMap[K comparable, V any](m map[K]V) []byte {
	b, _ := json.Marshal(m)
	return b
}
