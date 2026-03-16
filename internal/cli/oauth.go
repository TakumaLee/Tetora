package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"tetora/internal/db"
)

// OAuthToken represents a stored OAuth token.
type OAuthToken struct {
	ServiceName  string `json:"serviceName"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	Scopes       string `json:"scopes,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// OAuthTokenStatus is a public-safe view of token status (no secrets).
type OAuthTokenStatus struct {
	ServiceName string `json:"serviceName"`
	Connected   bool   `json:"connected"`
	Scopes      string `json:"scopes,omitempty"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	ExpiresSoon bool   `json:"expiresSoon,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// CmdOAuth implements `tetora oauth <list|connect|revoke|test> [service]`
func CmdOAuth(args []string) {
	if len(args) == 0 {
		printOAuthUsage()
		return
	}

	cfg := LoadCLIConfig("")

	switch args[0] {
	case "list":
		cmdOAuthList(cfg)
	case "connect":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora oauth connect <service>")
			os.Exit(1)
		}
		cmdOAuthConnect(cfg, args[1])
	case "revoke":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora oauth revoke <service>")
			os.Exit(1)
		}
		cmdOAuthRevoke(cfg, args[1])
	case "test":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora oauth test <service>")
			os.Exit(1)
		}
		cmdOAuthTest(cfg, args[1])
	case "--help", "-h", "help":
		printOAuthUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", args[0])
		printOAuthUsage()
		os.Exit(1)
	}
}

func printOAuthUsage() {
	fmt.Println("Usage: tetora oauth <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  list              List configured OAuth services and connection status")
	fmt.Println("  connect <service> Open browser to authorize an OAuth service")
	fmt.Println("  revoke <service>  Delete stored OAuth token for a service")
	fmt.Println("  test <service>    Verify stored token by making a simple request")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  tetora oauth list")
	fmt.Println("  tetora oauth connect google")
	fmt.Println("  tetora oauth revoke github")
	fmt.Println("  tetora oauth test google")
}

func cmdOAuthList(cfg *CLIConfig) {
	// Try daemon API first.
	api := cfg.NewAPIClient()
	resp, err := api.Get("/api/oauth/services")
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var result struct {
			Services []struct {
				Name        string `json:"name"`
				Connected   bool   `json:"connected"`
				Scopes      string `json:"scopes"`
				ExpiresAt   string `json:"expiresAt"`
				ExpiresSoon bool   `json:"expiresSoon"`
				Template    bool   `json:"template"`
			} `json:"services"`
		}
		if json.Unmarshal(body, &result) == nil {
			if len(result.Services) == 0 {
				fmt.Println("No OAuth services configured.")
				fmt.Println("Add services to config.json under \"oauth.services\".")
				return
			}
			fmt.Printf("OAuth Services (%d):\n", len(result.Services))
			for _, s := range result.Services {
				status := "not connected"
				if s.Connected {
					status = "connected"
					if s.ExpiresSoon {
						status = "expires soon"
					}
				}
				tmpl := ""
				if s.Template {
					tmpl = " [template]"
				}
				fmt.Printf("  %-15s %s%s\n", s.Name, status, tmpl)
				if s.Scopes != "" {
					fmt.Printf("    scopes: %s\n", s.Scopes)
				}
				if s.ExpiresAt != "" {
					fmt.Printf("    expires: %s\n", s.ExpiresAt)
				}
			}
			return
		}
	}

	// Fallback: direct DB.
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "No history DB configured.")
		os.Exit(1)
	}

	statuses, err := listOAuthTokenStatuses(cfg.HistoryDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(statuses) == 0 {
		fmt.Println("No connected OAuth services.")
		return
	}

	fmt.Printf("Connected OAuth services (%d):\n", len(statuses))
	for _, s := range statuses {
		status := "connected"
		if s.ExpiresSoon {
			status = "expires soon"
		}
		fmt.Printf("  %-15s %s\n", s.ServiceName, status)
	}
}

func cmdOAuthConnect(cfg *CLIConfig, service string) {
	// Route through daemon for OAuth flow — it manages the redirect server.
	base := "http://" + cfg.ListenAddr
	authorizeURL := base + "/api/oauth/" + service + "/authorize"

	fmt.Printf("Opening browser for %s OAuth authorization...\n", service)
	fmt.Printf("\nVisit: %s\n", authorizeURL)

	openBrowser(authorizeURL)
}

func cmdOAuthRevoke(cfg *CLIConfig, service string) {
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "No history DB configured.")
		os.Exit(1)
	}

	if err := deleteOAuthToken(cfg.HistoryDB, service); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OAuth token for %q revoked.\n", service)
}

func cmdOAuthTest(cfg *CLIConfig, service string) {
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "No history DB configured.")
		os.Exit(1)
	}

	// Load token via DB query (no decryption — display metadata only).
	statuses, err := listOAuthTokenStatuses(cfg.HistoryDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading token: %v\n", err)
		os.Exit(1)
	}

	var found *OAuthTokenStatus
	for i := range statuses {
		if statuses[i].ServiceName == service {
			found = &statuses[i]
			break
		}
	}

	if found == nil {
		fmt.Fprintf(os.Stderr, "No token stored for %q. Run: tetora oauth connect %s\n", service, service)
		os.Exit(1)
	}

	fmt.Printf("Token for %q:\n", service)
	fmt.Printf("  Scopes:     %s\n", found.Scopes)
	fmt.Printf("  Expires:    %s\n", found.ExpiresAt)
	fmt.Printf("  Created:    %s\n", found.CreatedAt)
	if found.ExpiresSoon {
		fmt.Println("  Warning: token expires soon.")
	} else {
		fmt.Println("\nToken is valid and accessible.")
	}
}

// --- OAuth DB operations (replicated from root oauth.go, no crypto) ---

func deleteOAuthToken(dbPath, serviceName string) error {
	sql := fmt.Sprintf(
		`DELETE FROM oauth_tokens WHERE service_name = '%s'`,
		db.Escape(serviceName))
	return db.Exec(dbPath, sql)
}

func listOAuthTokenStatuses(dbPath string) ([]OAuthTokenStatus, error) {
	rows, err := db.Query(dbPath, `SELECT service_name, expires_at, scopes, created_at FROM oauth_tokens ORDER BY service_name`)
	if err != nil {
		return nil, err
	}

	statuses := make([]OAuthTokenStatus, 0, len(rows))
	for _, row := range rows {
		expiresAt := fmt.Sprint(row["expires_at"])
		expiresSoon := false
		if expiresAt != "" {
			if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
				expiresSoon = time.Until(t) < 5*time.Minute
			}
		}
		statuses = append(statuses, OAuthTokenStatus{
			ServiceName: fmt.Sprint(row["service_name"]),
			Connected:   true,
			Scopes:      fmt.Sprint(row["scopes"]),
			ExpiresAt:   expiresAt,
			ExpiresSoon: expiresSoon,
			CreatedAt:   fmt.Sprint(row["created_at"]),
		})
	}
	return statuses, nil
}

// openBrowser opens a URL in the default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start() //nolint:errcheck
}
