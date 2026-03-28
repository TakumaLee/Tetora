package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

var claudeSettingsMu sync.Mutex

func detectTetoraPath() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".tetora", "bin", "tetora"),
		"/usr/local/bin/tetora",
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func claudeSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func readClaudeSettings() (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(claudeSettingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]json.RawMessage), nil
		}
		return nil, err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("invalid JSON in settings.json: %w", err)
	}
	return settings, nil
}

func writeClaudeSettings(settings map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(claudeSettingsPath(), data, 0o600)
}

// RegisterClaudeMCPRoutes registers Claude MCP integration API routes.
func RegisterClaudeMCPRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/claude-mcp/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		claudeSettingsMu.Lock()
		settings, err := readClaudeSettings()
		claudeSettingsMu.Unlock()

		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{
				"enabled": false,
				"healthy": false,
				"error":   err.Error(),
			})
			return
		}

		enabled := false
		if mcpRaw, ok := settings["mcpServers"]; ok {
			var mcpServers map[string]json.RawMessage
			if json.Unmarshal(mcpRaw, &mcpServers) == nil {
				_, enabled = mcpServers["tetora"]
			}
		}

		healthy := enabled && detectTetoraPath() != ""

		json.NewEncoder(w).Encode(map[string]any{
			"enabled": enabled,
			"healthy": healthy,
		})
	})

	mux.HandleFunc("/api/claude-mcp/toggle", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		var body struct {
			Enable bool `json:"enable"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}

		claudeSettingsMu.Lock()
		defer claudeSettingsMu.Unlock()

		settings, err := readClaudeSettings()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		var mcpServers map[string]json.RawMessage
		if mcpRaw, ok := settings["mcpServers"]; ok {
			if err := json.Unmarshal(mcpRaw, &mcpServers); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid mcpServers in settings.json"})
				return
			}
		} else {
			mcpServers = make(map[string]json.RawMessage)
		}

		if body.Enable {
			binPath := detectTetoraPath()
			if binPath == "" {
				home, _ := os.UserHomeDir()
				binPath = filepath.Join(home, ".tetora", "bin", "tetora")
			}

			entry := map[string]any{
				"command": binPath,
				"args":    []string{"mcp-server"},
			}
			entryJSON, _ := json.Marshal(entry)
			mcpServers["tetora"] = json.RawMessage(entryJSON)
		} else {
			delete(mcpServers, "tetora")
		}

		mcpJSON, _ := json.Marshal(mcpServers)
		settings["mcpServers"] = json.RawMessage(mcpJSON)

		if err := writeClaudeSettings(settings); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		healthy := body.Enable && detectTetoraPath() != ""
		json.NewEncoder(w).Encode(map[string]any{
			"enabled": body.Enable,
			"healthy": healthy,
		})
	})
}
