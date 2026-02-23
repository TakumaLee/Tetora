package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func cmdPairing(args []string) {
	if len(args) == 0 {
		args = []string{"list"}
	}

	switch args[0] {
	case "list":
		cmdPairingList()
	case "approve":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: tetora pairing approve <code>\n")
			os.Exit(1)
		}
		cmdPairingApprove(args[1])
	case "reject":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: tetora pairing reject <code>\n")
			os.Exit(1)
		}
		cmdPairingReject(args[1])
	case "revoked", "approved":
		cmdPairingApproved()
	case "revoke":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: tetora pairing revoke <channel> <userId>\n")
			os.Exit(1)
		}
		cmdPairingRevoke(args[1], args[2])
	default:
		fmt.Fprintf(os.Stderr, "Usage: tetora pairing <list|approve|reject|revoked|revoke>\n")
		os.Exit(1)
	}
}

func cmdPairingList() {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Call daemon API.
	api := newAPIClient(cfg)
	resp, err := api.get("/api/pairing/pending")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to connect to daemon: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: %s\n", string(body))
		os.Exit(1)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Pending []PairingRequest `json:"pending"`
		Count   int              `json:"count"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to parse response: %v\n", err)
		os.Exit(1)
	}

	if result.Count == 0 {
		fmt.Println("No pending pairing requests.")
		return
	}

	fmt.Printf("%-8s %-12s %-12s %-20s %s\n", "Code", "Channel", "UserID", "Username", "Expires")
	fmt.Println(strings.Repeat("-", 70))

	for _, req := range result.Pending {
		fmt.Printf("%-8s %-12s %-12s %-20s %s\n",
			req.Code, req.Channel, req.UserID, req.Username,
			req.ExpiresAt.Format("2006-01-02 15:04:05"))
	}
}

func cmdPairingApprove(code string) {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Call daemon API.
	api := newAPIClient(cfg)
	payload := fmt.Sprintf(`{"code":"%s"}`, code)
	resp, err := api.post("/api/pairing/approve", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to connect to daemon: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errResp.Error)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", string(body))
		}
		os.Exit(1)
	}

	var result struct {
		Status  string `json:"status"`
		Channel string `json:"channel"`
		UserID  string `json:"userId"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to parse response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Approved: %s user %s\n", result.Channel, result.UserID)
}

func cmdPairingReject(code string) {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Call daemon API.
	api := newAPIClient(cfg)
	payload := fmt.Sprintf(`{"code":"%s"}`, code)
	resp, err := api.post("/api/pairing/reject", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to connect to daemon: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errResp.Error)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", string(body))
		}
		os.Exit(1)
	}

	fmt.Printf("Rejected pairing request.\n")
}

func cmdPairingApproved() {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Call daemon API.
	api := newAPIClient(cfg)
	resp, err := api.get("/api/pairing/approved")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to connect to daemon: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: %s\n", string(body))
		os.Exit(1)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Approved []map[string]any `json:"approved"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to parse response: %v\n", err)
		os.Exit(1)
	}

	if result.Count == 0 {
		fmt.Println("No approved users.")
		return
	}

	fmt.Printf("%-12s %-12s %-20s %s\n", "Channel", "UserID", "Username", "Approved At")
	fmt.Println(strings.Repeat("-", 65))

	for _, row := range result.Approved {
		fmt.Printf("%-12s %-12s %-20s %s\n",
			jsonStr(row["channel"]),
			jsonStr(row["user_id"]),
			jsonStr(row["username"]),
			jsonStr(row["approved_at"]))
	}
}

func cmdPairingRevoke(channel, userID string) {
	cfg := loadConfig("")
	defaultLogger = initLogger(cfg.Logging, cfg.baseDir)

	// Call daemon API.
	api := newAPIClient(cfg)
	payload := fmt.Sprintf(`{"channel":"%s","userId":"%s"}`, channel, userID)
	resp, err := api.post("/api/pairing/revoke", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to connect to daemon: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errResp.Error)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", string(body))
		}
		os.Exit(1)
	}

	fmt.Printf("Revoked access for %s user %s\n", channel, userID)
}
