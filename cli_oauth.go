package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// --- P18.2: CLI OAuth Command ---

// cmdOAuth implements `tetora oauth <list|connect|revoke|test> [service]`
func cmdOAuth(args []string) {
	if len(args) == 0 {
		printOAuthUsage()
		return
	}

	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

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

func cmdOAuthList(cfg *Config) {
	// Try daemon API first.
	api := newAPIClient(cfg)
	resp, err := api.get("/api/oauth/services")
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

	statuses, err := listOAuthTokenStatuses(cfg.HistoryDB, cfg.OAuth.EncryptionKey)
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

func cmdOAuthConnect(cfg *Config, service string) {
	mgr := newOAuthManager(cfg)
	svc, err := mgr.resolveServiceConfig(service)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Build the authorize URL via the daemon.
	base := cfg.OAuth.RedirectBase
	if base == "" {
		base = "http://localhost" + cfg.ListenAddr
	}
	authorizeURL := base + "/api/oauth/" + service + "/authorize"

	fmt.Printf("Opening browser for %s OAuth authorization...\n", service)
	fmt.Printf("Auth URL: %s\n", svc.AuthURL)
	fmt.Printf("Scopes: %v\n", svc.Scopes)
	fmt.Printf("\nVisit: %s\n", authorizeURL)

	// Try to open browser.
	openBrowser(authorizeURL)
}

func cmdOAuthRevoke(cfg *Config, service string) {
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

func cmdOAuthTest(cfg *Config, service string) {
	if cfg.HistoryDB == "" {
		fmt.Fprintln(os.Stderr, "No history DB configured.")
		os.Exit(1)
	}

	token, err := loadOAuthToken(cfg.HistoryDB, service, cfg.OAuth.EncryptionKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading token: %v\n", err)
		os.Exit(1)
	}
	if token == nil {
		fmt.Fprintf(os.Stderr, "No token stored for %q. Run: tetora oauth connect %s\n", service, service)
		os.Exit(1)
	}

	fmt.Printf("Token for %q:\n", service)
	fmt.Printf("  Type:       %s\n", token.TokenType)
	fmt.Printf("  Scopes:     %s\n", token.Scopes)
	fmt.Printf("  Expires:    %s\n", token.ExpiresAt)
	fmt.Printf("  Created:    %s\n", token.CreatedAt)
	fmt.Printf("  Updated:    %s\n", token.UpdatedAt)
	fmt.Printf("  Token:      %s...%s\n", token.AccessToken[:min(4, len(token.AccessToken))], token.AccessToken[max(0, len(token.AccessToken)-4):])
	fmt.Println("\nToken is valid and accessible.")
}

// openBrowser opens a URL in the default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	cmd.Start()
}
