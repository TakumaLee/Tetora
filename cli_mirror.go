package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"
)

func cmdMirror(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora mirror <start|send|watch> [options]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  start   Create a mirror session and print hook config")
		fmt.Println("  send    Send a message to a mirror session (reads from stdin)")
		fmt.Println("  watch   Watch a Discord channel session in real-time")
		return
	}
	switch args[0] {
	case "start":
		mirrorStart(args[1:])
	case "send":
		mirrorSend(args[1:])
	case "watch":
		mirrorWatch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
		os.Exit(1)
	}
}

func mirrorStart(args []string) {
	cfg := loadConfig(findConfigPath())

	role := ""
	discordChannel := ""
	title := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role", "-r":
			if i+1 < len(args) {
				i++
				role = args[i]
			}
		case "--discord-channel", "-d":
			if i+1 < len(args) {
				i++
				discordChannel = args[i]
			}
		case "--title", "-t":
			if i+1 < len(args) {
				i++
				title = args[i]
			}
		}
	}

	if role == "" {
		role = "mirror"
	}
	if title == "" {
		title = fmt.Sprintf("Mirror: %s @ %s", role, time.Now().Format("2006-01-02 15:04"))
	}

	sessionID := newUUID()

	// Create session directly in DB if accessible, otherwise via API.
	if cfg.HistoryDB != "" {
		now := time.Now().Format(time.RFC3339)
		if err := createSession(cfg.HistoryDB, Session{
			ID:        sessionID,
			Role:      role,
			Source:    "mirror",
			Status:    "active",
			Title:     title,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating session: %v\n", err)
			os.Exit(1)
		}
	}

	addr := cfg.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:8991"
	}

	fmt.Printf("Mirror session created: %s\n", sessionID)
	fmt.Printf("Server: http://%s\n", addr)
	fmt.Println()
	fmt.Println("Set environment variable:")
	fmt.Printf("  export TETORA_MIRROR_SESSION=%s\n", sessionID)
	if discordChannel != "" {
		fmt.Printf("  export TETORA_MIRROR_DISCORD=%s\n", discordChannel)
	}
	fmt.Printf("  export TETORA_MIRROR_ADDR=%s\n", addr)
	fmt.Println()
	fmt.Println("Claude Code hooks (.claude/settings.json):")
	fmt.Println(`{`)
	fmt.Println(`  "hooks": {`)
	fmt.Println(`    "UserPromptSubmit": [{`)
	fmt.Printf("      \"command\": \"tetora mirror send --role user --session %s --addr %s", sessionID, addr)
	if discordChannel != "" {
		fmt.Printf(" --discord %s", discordChannel)
	}
	fmt.Println(`"`)
	fmt.Println(`    }],`)
	fmt.Println(`    "Stop": [{`)
	fmt.Printf("      \"command\": \"tetora mirror send --role assistant --session %s --addr %s", sessionID, addr)
	if discordChannel != "" {
		fmt.Printf(" --discord %s", discordChannel)
	}
	fmt.Println(`"`)
	fmt.Println(`    }]`)
	fmt.Println(`  }`)
	fmt.Println(`}`)
}

func mirrorSend(args []string) {
	role := ""
	sessionID := os.Getenv("TETORA_MIRROR_SESSION")
	addr := os.Getenv("TETORA_MIRROR_ADDR")
	discordChannel := os.Getenv("TETORA_MIRROR_DISCORD")
	token := os.Getenv("TETORA_API_TOKEN")

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role", "-r":
			if i+1 < len(args) {
				i++
				role = args[i]
			}
		case "--session", "-s":
			if i+1 < len(args) {
				i++
				sessionID = args[i]
			}
		case "--addr", "-a":
			if i+1 < len(args) {
				i++
				addr = args[i]
			}
		case "--discord", "-d":
			if i+1 < len(args) {
				i++
				discordChannel = args[i]
			}
		case "--token":
			if i+1 < len(args) {
				i++
				token = args[i]
			}
		}
	}

	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "Error: session ID required (--session or TETORA_MIRROR_SESSION)")
		os.Exit(1)
	}
	if addr == "" {
		addr = "127.0.0.1:8991"
	}
	if role == "" {
		role = "user"
	}

	// Read content from stdin.
	content, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		// Nothing to send.
		return
	}

	payload := map[string]any{
		"role":    role,
		"content": string(content),
	}
	if discordChannel != "" {
		payload["discordChannel"] = discordChannel
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://%s/sessions/%s/mirror", addr, sessionID)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Non-blocking: log to stderr but don't fail the hook.
		fmt.Fprintf(os.Stderr, "tetora mirror: send failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "tetora mirror: server returned %d: %s\n", resp.StatusCode, string(respBody))
	}
}

func mirrorWatch(args []string) {
	cfg := loadConfig(findConfigPath())

	discordChannel := ""
	sessionID := ""
	addr := ""
	token := os.Getenv("TETORA_API_TOKEN")
	tail := 20 // number of recent messages to show

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--discord", "-d":
			if i+1 < len(args) {
				i++
				discordChannel = args[i]
			}
		case "--session", "-s":
			if i+1 < len(args) {
				i++
				sessionID = args[i]
			}
		case "--addr", "-a":
			if i+1 < len(args) {
				i++
				addr = args[i]
			}
		case "--token":
			if i+1 < len(args) {
				i++
				token = args[i]
			}
		case "--tail", "-n":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &tail)
			}
		}
	}

	if addr == "" {
		addr = cfg.ListenAddr
	}
	if addr == "" {
		addr = "127.0.0.1:8991"
	}

	// Resolve session ID from Discord channel if needed.
	if sessionID == "" && discordChannel != "" {
		// Try local DB first.
		if cfg.HistoryDB != "" {
			chKey := channelSessionKey("discord", discordChannel)
			sess, err := findChannelSession(cfg.HistoryDB, chKey)
			if err == nil && sess != nil {
				sessionID = sess.ID
			}
		}
		// Fallback to API.
		if sessionID == "" {
			sid, err := mirrorWatchResolveSession(addr, token, discordChannel)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving channel %s: %v\n", discordChannel, err)
				os.Exit(1)
			}
			sessionID = sid
		}
	}

	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "Error: --discord <channel-id> or --session <id> required")
		os.Exit(1)
	}

	fmt.Printf("Watching session: %s\n", sessionID)
	fmt.Printf("Server: http://%s\n", addr)
	fmt.Println(strings.Repeat("-", 60))

	// Fetch and render history.
	mirrorWatchHistory(addr, token, sessionID, tail)

	// Set up signal handling for clean exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	// Connect SSE with reconnect loop.
	retryDelay := time.Second
	maxRetryDelay := 30 * time.Second

	for {
		done := make(chan struct{})
		go func() {
			mirrorWatchSSE(addr, token, sessionID)
			close(done)
		}()

		select {
		case <-sigCh:
			fmt.Println("\nDisconnected.")
			return
		case <-done:
			// Connection dropped, retry.
			fmt.Fprintf(os.Stderr, "\nConnection lost. Reconnecting in %s...\n", retryDelay)
			select {
			case <-sigCh:
				fmt.Println("\nDisconnected.")
				return
			case <-time.After(retryDelay):
			}
			retryDelay *= 2
			if retryDelay > maxRetryDelay {
				retryDelay = maxRetryDelay
			}
		}
	}
}

// mirrorWatchResolveSession calls the API to resolve a Discord channel to a session ID.
func mirrorWatchResolveSession(addr, token, channelID string) (string, error) {
	url := fmt.Sprintf("http://%s/api/discord/session?channel=%s", addr, channelID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var sess struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		return "", err
	}
	return sess.ID, nil
}

// mirrorWatchHistory fetches and prints recent session messages.
func mirrorWatchHistory(addr, token, sessionID string, tail int) {
	url := fmt.Sprintf("http://%s/sessions/%s", addr, sessionID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch history: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return
	}

	var detail struct {
		Session struct {
			Role  string `json:"role"`
			Title string `json:"title"`
		} `json:"session"`
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			CreatedAt string `json:"createdAt"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return
	}

	if detail.Session.Title != "" {
		fmt.Printf("Session: %s (%s)\n", detail.Session.Title, detail.Session.Role)
		fmt.Println(strings.Repeat("-", 60))
	}

	msgs := detail.Messages
	if len(msgs) > tail {
		msgs = msgs[len(msgs)-tail:]
		fmt.Printf("... (%d earlier messages)\n\n", len(detail.Messages)-tail)
	}

	for _, m := range msgs {
		ts := mirrorWatchFormatTime(m.CreatedAt)
		icon := mirrorWatchRoleIcon(m.Role)
		content := m.Content
		if len(content) > 500 {
			content = content[:497] + "..."
		}
		fmt.Printf("[%s] %s %s: %s\n", ts, icon, m.Role, content)
	}

	if len(msgs) > 0 {
		fmt.Println(strings.Repeat("-", 60))
	}
	fmt.Println("Watching for new events...")
	fmt.Println()
}

// mirrorWatchSSE connects to the SSE watch endpoint and renders events.
func mirrorWatchSSE(addr, token, sessionID string) {
	url := fmt.Sprintf("http://%s/sessions/%s/watch", addr, sessionID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SSE connect error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "SSE error %d: %s\n", resp.StatusCode, string(body))
		return
	}

	toolStarts := make(map[string]time.Time) // toolUseId → start time
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024) // 256KB buffer for large events

	for scanner.Scan() {
		line := scanner.Text()

		// SSE data lines start with "data: ".
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event SSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		ts := mirrorWatchFormatTime(event.Timestamp)
		dataMap, _ := event.Data.(map[string]any)

		switch event.Type {
		case SSETaskReceived:
			author, _ := dataMap["author"].(string)
			prompt, _ := dataMap["prompt"].(string)
			if len(prompt) > 300 {
				prompt = prompt[:297] + "..."
			}
			fmt.Printf("[%s] \U0001F4AC %s: %s\n", ts, author, prompt)

		case SSETaskRouting:
			role, _ := dataMap["role"].(string)
			confidence, _ := dataMap["confidence"].(float64)
			method, _ := dataMap["method"].(string)
			if confidence > 0 {
				fmt.Printf("[%s] \U0001F500 routing -> %s (%.2f, %s)\n", ts, role, confidence, method)
			} else {
				fmt.Printf("[%s] \U0001F500 routing -> %s (%s)\n", ts, role, method)
			}

		case SSEDiscordProcessing:
			role, _ := dataMap["role"].(string)
			fmt.Printf("[%s] \u2699\uFE0F  processing (%s)...\n", ts, role)

		case SSEToolCall:
			toolName, _ := dataMap["name"].(string)
			toolID, _ := dataMap["toolUseId"].(string)
			if toolID != "" {
				toolStarts[toolID] = time.Now()
			}
			fmt.Printf("[%s] \U0001F527 %s...\n", ts, toolName)

		case SSEToolResult:
			toolID, _ := dataMap["toolUseId"].(string)
			dur := ""
			if start, ok := toolStarts[toolID]; ok {
				dur = fmt.Sprintf(" (%.1fs)", time.Since(start).Seconds())
				delete(toolStarts, toolID)
			}
			fmt.Printf("[%s]    done%s\n", ts, dur)

		case SSESessionMessage:
			role, _ := dataMap["role"].(string)
			content, _ := dataMap["content"].(string)
			icon := mirrorWatchRoleIcon(role)
			if len(content) > 500 {
				content = content[:497] + "..."
			}
			fmt.Printf("[%s] %s %s: %s\n", ts, icon, role, content)

		case SSEDiscordReplying:
			status, _ := dataMap["status"].(string)
			if status != "success" {
				fmt.Printf("[%s] \u274C error: %s\n", ts, status)
			} else {
				fmt.Printf("[%s] \u2705 replying\n", ts)
			}

		case SSEError:
			errMsg := ""
			if dataMap != nil {
				errMsg, _ = dataMap["error"].(string)
			}
			if errMsg == "" {
				errMsg = fmt.Sprintf("%v", event.Data)
			}
			fmt.Printf("[%s] \u274C error: %s\n", ts, errMsg)

		case SSECompleted:
			// Task cycle done — watcher stays open for next task.

		case SSEHeartbeat:
			// Ignore heartbeats silently.

		default:
			// Show unknown event types for debugging.
			fmt.Printf("[%s] [%s] %v\n", ts, event.Type, event.Data)
		}
	}
}

// mirrorWatchFormatTime parses an RFC3339 timestamp and returns HH:MM:SS.
func mirrorWatchFormatTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("15:04:05")
}

// mirrorWatchRoleIcon returns an emoji icon for the given message role.
func mirrorWatchRoleIcon(role string) string {
	switch role {
	case "user":
		return "\U0001F4AC"
	case "assistant":
		return "\U0001F916"
	case "system":
		return "\u2699\uFE0F "
	default:
		return "\u2753"
	}
}
