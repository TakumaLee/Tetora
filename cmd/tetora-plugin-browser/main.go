package main

// --- P16.1: Browser Automation Plugin ---
// tetora-plugin-browser — Chrome DevTools Protocol (CDP) plugin for Tetora.
//
// This plugin manages a headless Chrome instance and provides browser automation
// tools via CDP. It communicates with Tetora via JSON-RPC over stdin/stdout.
//
// Build: go build ./cmd/tetora-plugin-browser/

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// --- JSON-RPC Types ---

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- CDP Types ---

type cdpRequest struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type cdpResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// --- Browser Session ---

type browserSession struct {
	chromePath   string
	chromeCmd    *exec.Cmd
	wsURL        string
	wsConn       io.ReadWriteCloser
	mu           sync.Mutex
	pending      map[int]chan cdpResponse
	nextID       int32
	targetID     string // current tab/target ID
	sessionID    string // CDP session ID
	readLoopDone chan struct{}
}

var (
	globalSession *browserSession
	sessionMu     sync.Mutex
)

// --- Main ---

func main() {
	// Parse CLI args for Chrome path.
	chromePath := findChrome()
	for i := 1; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--chrome-path" {
			chromePath = os.Args[i+1]
		}
	}

	logDebug("browser plugin starting", "chromePath", chromePath)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 2*1024*1024), 2*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			writeError(0, -32700, "parse error: "+err.Error())
			continue
		}

		handleRequest(req, chromePath)
	}

	// Cleanup on exit.
	if globalSession != nil {
		globalSession.close()
	}
}

func handleRequest(req jsonRPCRequest, chromePath string) {
	switch req.Method {
	case "ping":
		writeResult(req.ID, map[string]any{"pong": true})

	case "tool/execute":
		handleToolExecute(req, chromePath)

	default:
		writeError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// --- Tool Execute Router ---

func handleToolExecute(req jsonRPCRequest, chromePath string) {
	var params struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	// Ensure browser session is initialized.
	sessionMu.Lock()
	if globalSession == nil {
		sess, err := newBrowserSession(chromePath)
		if err != nil {
			sessionMu.Unlock()
			writeError(req.ID, -32000, fmt.Sprintf("failed to start browser: %v", err))
			return
		}
		globalSession = sess
	}
	sessionMu.Unlock()

	// Route to the appropriate tool handler.
	switch params.Name {
	case "browser_navigate":
		handleNavigate(req.ID, params.Input)
	case "browser_screenshot":
		handleScreenshot(req.ID, params.Input)
	case "browser_click":
		handleClick(req.ID, params.Input)
	case "browser_type":
		handleType(req.ID, params.Input)
	case "browser_eval":
		handleEval(req.ID, params.Input)
	case "browser_content":
		handleContent(req.ID, params.Input)
	case "browser_wait":
		handleWait(req.ID, params.Input)
	default:
		writeError(req.ID, -32601, fmt.Sprintf("unknown tool: %s", params.Name))
	}
}

// --- Tool Handlers ---

func handleNavigate(reqID int, input json.RawMessage) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		writeError(reqID, -32602, "invalid input: "+err.Error())
		return
	}
	if args.URL == "" {
		writeError(reqID, -32602, "url is required")
		return
	}

	// Navigate using Page.navigate.
	params := map[string]any{"url": args.URL}
	resp, err := globalSession.sendCDP("Page.navigate", params)
	if err != nil {
		writeError(reqID, -32000, fmt.Sprintf("navigate failed: %v", err))
		return
	}

	// Wait for Page.loadEventFired (simple wait).
	time.Sleep(500 * time.Millisecond)

	writeResult(reqID, map[string]any{
		"ok":     true,
		"url":    args.URL,
		"result": json.RawMessage(resp),
	})
}

func handleScreenshot(reqID int, input json.RawMessage) {
	var args struct {
		Format  string `json:"format"`  // "png" or "jpeg"
		Quality int    `json:"quality"` // for jpeg (1-100)
	}
	if err := json.Unmarshal(input, &args); err != nil {
		args.Format = "png"
	}
	if args.Format == "" {
		args.Format = "png"
	}

	params := map[string]any{"format": args.Format}
	if args.Format == "jpeg" && args.Quality > 0 {
		params["quality"] = args.Quality
	}

	resp, err := globalSession.sendCDP("Page.captureScreenshot", params)
	if err != nil {
		writeError(reqID, -32000, fmt.Sprintf("screenshot failed: %v", err))
		return
	}

	var result struct {
		Data string `json:"data"` // base64 encoded
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		writeError(reqID, -32000, fmt.Sprintf("parse screenshot response: %v", err))
		return
	}

	writeResult(reqID, map[string]any{
		"ok":         true,
		"format":     args.Format,
		"screenshot": result.Data,
	})
}

func handleClick(reqID int, input json.RawMessage) {
	var args struct {
		Selector string `json:"selector"` // CSS selector
	}
	if err := json.Unmarshal(input, &args); err != nil {
		writeError(reqID, -32602, "invalid input: "+err.Error())
		return
	}
	if args.Selector == "" {
		writeError(reqID, -32602, "selector is required")
		return
	}

	// Find the element using Runtime.evaluate to get node.
	script := fmt.Sprintf(`
		(function() {
			const el = document.querySelector(%s);
			if (!el) return {error: "element not found"};
			const rect = el.getBoundingClientRect();
			return {x: rect.x + rect.width/2, y: rect.y + rect.height/2};
		})()
	`, strconv.Quote(args.Selector))

	evalResp, err := globalSession.sendCDP("Runtime.evaluate", map[string]any{
		"expression":    script,
		"returnByValue": true,
	})
	if err != nil {
		writeError(reqID, -32000, fmt.Sprintf("evaluate failed: %v", err))
		return
	}

	var evalResult struct {
		Result struct {
			Value map[string]any `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(evalResp, &evalResult); err != nil {
		writeError(reqID, -32000, fmt.Sprintf("parse eval response: %v", err))
		return
	}

	if errMsg, ok := evalResult.Result.Value["error"].(string); ok {
		writeError(reqID, -32000, errMsg)
		return
	}

	x, _ := evalResult.Result.Value["x"].(float64)
	y, _ := evalResult.Result.Value["y"].(float64)

	// Send mouse click at (x, y).
	if _, err := globalSession.sendCDP("Input.dispatchMouseEvent", map[string]any{
		"type":   "mousePressed",
		"x":      x,
		"y":      y,
		"button": "left",
		"clickCount": 1,
	}); err != nil {
		writeError(reqID, -32000, fmt.Sprintf("mouse press failed: %v", err))
		return
	}

	if _, err := globalSession.sendCDP("Input.dispatchMouseEvent", map[string]any{
		"type":   "mouseReleased",
		"x":      x,
		"y":      y,
		"button": "left",
		"clickCount": 1,
	}); err != nil {
		writeError(reqID, -32000, fmt.Sprintf("mouse release failed: %v", err))
		return
	}

	writeResult(reqID, map[string]any{
		"ok":       true,
		"selector": args.Selector,
		"x":        x,
		"y":        y,
	})
}

func handleType(reqID int, input json.RawMessage) {
	var args struct {
		Selector string `json:"selector"` // CSS selector
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		writeError(reqID, -32602, "invalid input: "+err.Error())
		return
	}
	if args.Selector == "" {
		writeError(reqID, -32602, "selector is required")
		return
	}
	if args.Text == "" {
		writeError(reqID, -32602, "text is required")
		return
	}

	// Focus the element.
	script := fmt.Sprintf(`document.querySelector(%s).focus()`, strconv.Quote(args.Selector))
	if _, err := globalSession.sendCDP("Runtime.evaluate", map[string]any{
		"expression": script,
	}); err != nil {
		writeError(reqID, -32000, fmt.Sprintf("focus failed: %v", err))
		return
	}

	// Type each character.
	for _, ch := range args.Text {
		if _, err := globalSession.sendCDP("Input.dispatchKeyEvent", map[string]any{
			"type": "char",
			"text": string(ch),
		}); err != nil {
			writeError(reqID, -32000, fmt.Sprintf("type failed: %v", err))
			return
		}
	}

	writeResult(reqID, map[string]any{
		"ok":       true,
		"selector": args.Selector,
		"text":     args.Text,
	})
}

func handleEval(reqID int, input json.RawMessage) {
	var args struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		writeError(reqID, -32602, "invalid input: "+err.Error())
		return
	}
	if args.Expression == "" {
		writeError(reqID, -32602, "expression is required")
		return
	}

	resp, err := globalSession.sendCDP("Runtime.evaluate", map[string]any{
		"expression":    args.Expression,
		"returnByValue": true,
	})
	if err != nil {
		writeError(reqID, -32000, fmt.Sprintf("eval failed: %v", err))
		return
	}

	var result struct {
		Result struct {
			Type  string `json:"type"`
			Value any    `json:"value"`
		} `json:"result"`
		ExceptionDetails map[string]any `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		writeError(reqID, -32000, fmt.Sprintf("parse eval response: %v", err))
		return
	}

	if result.ExceptionDetails != nil {
		writeError(reqID, -32000, fmt.Sprintf("eval exception: %v", result.ExceptionDetails))
		return
	}

	writeResult(reqID, map[string]any{
		"ok":    true,
		"type":  result.Result.Type,
		"value": result.Result.Value,
	})
}

func handleContent(reqID int, input json.RawMessage) {
	// Get page text content using document.body.innerText.
	script := `document.body.innerText`
	resp, err := globalSession.sendCDP("Runtime.evaluate", map[string]any{
		"expression":    script,
		"returnByValue": true,
	})
	if err != nil {
		writeError(reqID, -32000, fmt.Sprintf("content failed: %v", err))
		return
	}

	var result struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		writeError(reqID, -32000, fmt.Sprintf("parse content response: %v", err))
		return
	}

	writeResult(reqID, map[string]any{
		"ok":      true,
		"content": result.Result.Value,
	})
}

func handleWait(reqID int, input json.RawMessage) {
	var args struct {
		Selector string `json:"selector"` // CSS selector
		Timeout  int    `json:"timeout"`  // milliseconds
	}
	if err := json.Unmarshal(input, &args); err != nil {
		writeError(reqID, -32602, "invalid input: "+err.Error())
		return
	}

	timeout := args.Timeout
	if timeout <= 0 {
		timeout = 5000 // 5 seconds default
	}

	if args.Selector != "" {
		// Wait for selector to appear.
		script := fmt.Sprintf(`
			new Promise((resolve, reject) => {
				const timeout = setTimeout(() => reject("timeout"), %d);
				const check = () => {
					const el = document.querySelector(%s);
					if (el) {
						clearTimeout(timeout);
						resolve(true);
					} else {
						setTimeout(check, 100);
					}
				};
				check();
			})
		`, timeout, strconv.Quote(args.Selector))

		resp, err := globalSession.sendCDP("Runtime.evaluate", map[string]any{
			"expression":     script,
			"awaitPromise":   true,
			"returnByValue":  true,
		})
		if err != nil {
			writeError(reqID, -32000, fmt.Sprintf("wait failed: %v", err))
			return
		}

		var result struct {
			Result struct {
				Type  string `json:"type"`
				Value any    `json:"value"`
			} `json:"result"`
			ExceptionDetails map[string]any `json:"exceptionDetails"`
		}
		if err := json.Unmarshal(resp, &result); err != nil {
			writeError(reqID, -32000, fmt.Sprintf("parse wait response: %v", err))
			return
		}

		if result.ExceptionDetails != nil {
			writeError(reqID, -32000, "wait timeout")
			return
		}

		writeResult(reqID, map[string]any{
			"ok":       true,
			"selector": args.Selector,
		})
	} else {
		// Simple sleep.
		time.Sleep(time.Duration(timeout) * time.Millisecond)
		writeResult(reqID, map[string]any{
			"ok": true,
		})
	}
}

// --- Browser Session Management ---

func newBrowserSession(chromePath string) (*browserSession, error) {
	// Launch Chrome in headless mode with remote debugging.
	port := "9222"
	cmd := exec.Command(chromePath,
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--remote-debugging-port="+port,
		"about:blank",
	)

	// Suppress Chrome stderr (too noisy).
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start chrome: %w", err)
	}

	logDebug("chrome launched", "pid", cmd.Process.Pid)

	// Wait for Chrome to be ready (retry /json/version endpoint).
	var wsURL string
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get("http://localhost:" + port + "/json/version")
		if err == nil {
			var versionData struct {
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			if json.NewDecoder(resp.Body).Decode(&versionData) == nil {
				wsURL = versionData.WebSocketDebuggerURL
				resp.Body.Close()
				break
			}
			resp.Body.Close()
		}
	}

	if wsURL == "" {
		cmd.Process.Kill()
		return nil, fmt.Errorf("chrome did not start in time")
	}

	logDebug("chrome ready", "wsURL", wsURL)

	// Connect to WebSocket.
	wsConn, err := dialWebSocket(wsURL)
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("websocket connect: %w", err)
	}

	sess := &browserSession{
		chromePath:   chromePath,
		chromeCmd:    cmd,
		wsURL:        wsURL,
		wsConn:       wsConn,
		pending:      make(map[int]chan cdpResponse),
		readLoopDone: make(chan struct{}),
	}

	// Start WebSocket read loop.
	go sess.readLoop()

	// Enable Page domain.
	if _, err := sess.sendCDP("Page.enable", nil); err != nil {
		sess.close()
		return nil, fmt.Errorf("enable page: %w", err)
	}

	// Enable Runtime domain.
	if _, err := sess.sendCDP("Runtime.enable", nil); err != nil {
		sess.close()
		return nil, fmt.Errorf("enable runtime: %w", err)
	}

	logDebug("browser session ready")
	return sess, nil
}

func (s *browserSession) sendCDP(method string, params any) (json.RawMessage, error) {
	id := int(atomic.AddInt32(&s.nextID, 1))

	var paramBytes json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		paramBytes = b
	}

	req := cdpRequest{
		ID:     id,
		Method: method,
		Params: paramBytes,
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal cdp request: %w", err)
	}

	ch := make(chan cdpResponse, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	// Write to WebSocket.
	if _, err := s.wsConn.Write(reqData); err != nil {
		return nil, fmt.Errorf("write to websocket: %w", err)
	}

	// Wait for response with timeout.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("cdp error: %s", resp.Error.Message)
		}
		return resp.Result, nil
	case <-timer.C:
		return nil, fmt.Errorf("cdp timeout (method=%s)", method)
	}
}

func (s *browserSession) readLoop() {
	defer close(s.readLoopDone)

	buf := make([]byte, 4*1024*1024) // 4MB buffer for large messages
	for {
		n, err := s.wsConn.Read(buf)
		if err != nil {
			logDebug("websocket read error", "error", err)
			return
		}

		var resp cdpResponse
		if err := json.Unmarshal(buf[:n], &resp); err != nil {
			logDebug("invalid cdp response", "error", err)
			continue
		}

		if resp.ID > 0 {
			s.mu.Lock()
			ch, ok := s.pending[resp.ID]
			s.mu.Unlock()

			if ok {
				ch <- resp
			}
		}
		// Ignore events (no ID) for now.
	}
}

func (s *browserSession) close() {
	if s.wsConn != nil {
		s.wsConn.Close()
	}
	if s.chromeCmd != nil && s.chromeCmd.Process != nil {
		s.chromeCmd.Process.Kill()
		s.chromeCmd.Wait()
	}

	// Wait for read loop to exit.
	select {
	case <-s.readLoopDone:
	case <-time.After(2 * time.Second):
	}

	logDebug("browser session closed")
}

// --- WebSocket Dialer (minimal HTTP upgrade) ---

func dialWebSocket(url string) (io.ReadWriteCloser, error) {
	// Parse ws://host:port/path
	if !strings.HasPrefix(url, "ws://") {
		return nil, fmt.Errorf("unsupported websocket URL: %s", url)
	}
	url = strings.TrimPrefix(url, "ws://")
	parts := strings.SplitN(url, "/", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid websocket URL")
	}
	host := parts[0]
	path := "/" + parts[1]

	// TCP connect.
	conn, err := http.DefaultTransport.(*http.Transport).Dial("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}

	// Send WebSocket upgrade request.
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n", path, host)
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write upgrade request: %w", err)
	}

	// Read response (simple check for "101 Switching Protocols").
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read upgrade response: %w", err)
	}
	if !bytes.Contains(buf[:n], []byte("101")) {
		conn.Close()
		return nil, fmt.Errorf("websocket upgrade failed: %s", string(buf[:n]))
	}

	return &wsConn{conn: conn}, nil
}

type wsConn struct {
	conn   io.ReadWriteCloser
	readMu sync.Mutex
}

func (w *wsConn) Read(p []byte) (int, error) {
	w.readMu.Lock()
	defer w.readMu.Unlock()

	// Read WebSocket frame header (simple unmask).
	header := make([]byte, 2)
	if _, err := io.ReadFull(w.conn, header); err != nil {
		return 0, err
	}

	payloadLen := int(header[1] & 0x7F)
	if payloadLen == 126 {
		extLen := make([]byte, 2)
		if _, err := io.ReadFull(w.conn, extLen); err != nil {
			return 0, err
		}
		payloadLen = int(extLen[0])<<8 | int(extLen[1])
	} else if payloadLen == 127 {
		extLen := make([]byte, 8)
		if _, err := io.ReadFull(w.conn, extLen); err != nil {
			return 0, err
		}
		payloadLen = int(extLen[4])<<24 | int(extLen[5])<<16 | int(extLen[6])<<8 | int(extLen[7])
	}

	if payloadLen > len(p) {
		payloadLen = len(p)
	}

	return io.ReadFull(w.conn, p[:payloadLen])
}

func (w *wsConn) Write(p []byte) (int, error) {
	// Build WebSocket frame (text, no mask).
	frame := make([]byte, 0, len(p)+10)
	frame = append(frame, 0x81) // FIN + text frame

	if len(p) < 126 {
		frame = append(frame, byte(len(p)))
	} else if len(p) < 65536 {
		frame = append(frame, 126, byte(len(p)>>8), byte(len(p)))
	} else {
		frame = append(frame, 127, 0, 0, 0, 0, byte(len(p)>>24), byte(len(p)>>16), byte(len(p)>>8), byte(len(p)))
	}
	frame = append(frame, p...)

	_, err := w.conn.Write(frame)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *wsConn) Close() error {
	return w.conn.Close()
}

// --- Chrome Path Detection ---

func findChrome() string {
	// Try common Chrome/Chromium paths.
	candidates := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Default to "google-chrome" in PATH.
	return "google-chrome"
}

// --- JSON-RPC Helpers ---

func writeResult(id int, result any) {
	data, _ := json.Marshal(result)
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  data,
	}
	out, _ := json.Marshal(resp)
	fmt.Fprintln(os.Stdout, string(out))
}

func writeError(id int, code int, message string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
	out, _ := json.Marshal(resp)
	fmt.Fprintln(os.Stdout, string(out))
}

// --- Logging ---

func logDebug(msg string, args ...any) {
	// Format as key=value pairs.
	var parts []string
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			parts = append(parts, fmt.Sprintf("%v=%v", args[i], args[i+1]))
		}
	}
	fmt.Fprintf(os.Stderr, "[browser-plugin] %s %s\n", msg, strings.Join(parts, " "))
}
