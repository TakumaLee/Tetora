package main

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- P21.6: Browser Extension Relay Tests ---

func TestComputeWebSocketAccept(t *testing.T) {
	// RFC 6455 Section 4.2.2 example:
	// Key: "dGhlIHNhbXBsZSBub25jZQ=="
	// Expected Accept: "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	expected := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	got := computeWebSocketAccept(key)
	if got != expected {
		t.Errorf("computeWebSocketAccept(%q) = %q, want %q", key, got, expected)
	}
}

func TestComputeWebSocketAcceptDifferentKeys(t *testing.T) {
	// Verify different keys produce different accept values.
	key1 := "dGhlIHNhbXBsZSBub25jZQ=="
	key2 := "AQIDBAUGBwgJCgsMDQ4PEA=="
	accept1 := computeWebSocketAccept(key1)
	accept2 := computeWebSocketAccept(key2)
	if accept1 == accept2 {
		t.Error("different keys should produce different accept values")
	}
}

func TestComputeWebSocketAcceptManual(t *testing.T) {
	// Manually verify the computation.
	key := "testkey123"
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magic))
	expected := base64.StdEncoding.EncodeToString(h.Sum(nil))
	got := computeWebSocketAccept(key)
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestGenerateRelayID(t *testing.T) {
	id := generateRelayID()
	if id == "" {
		t.Error("generateRelayID returned empty string")
	}
	if len(id) != 16 { // 8 bytes = 16 hex chars
		t.Errorf("expected 16 hex chars, got %d: %q", len(id), id)
	}
}

func TestGenerateRelayIDUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateRelayID()
		if seen[id] {
			t.Errorf("duplicate ID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestNewBrowserRelay(t *testing.T) {
	cfg := &BrowserRelayConfig{
		Enabled: true,
		Port:    19000,
		Token:   "test-token",
	}
	br := newBrowserRelay(cfg)
	if br == nil {
		t.Fatal("newBrowserRelay returned nil")
	}
	if br.cfg != cfg {
		t.Error("config not stored correctly")
	}
	if br.pending == nil {
		t.Error("pending map not initialized")
	}
	if br.conn != nil {
		t.Error("conn should be nil initially")
	}
	if br.Connected() {
		t.Error("should not be connected initially")
	}
}

func TestBrowserRelayHealthEndpoint(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true, Port: 18792}
	br := newBrowserRelay(cfg)

	req := httptest.NewRequest(http.MethodGet, "/relay/health", nil)
	w := httptest.NewRecorder()
	br.handleHealth(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Errorf("unexpected health body: %s", body)
	}
}

func TestBrowserRelayStatusEndpoint(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true, Port: 18792}
	br := newBrowserRelay(cfg)

	req := httptest.NewRequest(http.MethodGet, "/relay/status", nil)
	w := httptest.NewRecorder()
	br.handleStatus(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status code = %d, want 200", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["connected"] != false {
		t.Errorf("expected connected=false, got %v", result["connected"])
	}
	if result["pending"].(float64) != 0 {
		t.Errorf("expected pending=0, got %v", result["pending"])
	}
}

func TestBrowserRelayWebSocketRejectNoUpgrade(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true}
	br := newBrowserRelay(cfg)

	req := httptest.NewRequest(http.MethodGet, "/relay/ws", nil)
	w := httptest.NewRecorder()
	br.handleWebSocket(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-websocket, got %d", w.Code)
	}
}

func TestBrowserRelayWebSocketRejectBadToken(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true, Token: "correct-token"}
	br := newBrowserRelay(cfg)

	req := httptest.NewRequest(http.MethodGet, "/relay/ws?token=wrong-token", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	br.handleWebSocket(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong token, got %d", w.Code)
	}
}

func TestBrowserRelayWebSocketRejectMissingKey(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true}
	br := newBrowserRelay(cfg)

	req := httptest.NewRequest(http.MethodGet, "/relay/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	br.handleWebSocket(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing Sec-WebSocket-Key, got %d", w.Code)
	}
}

func TestBrowserRelayToolRequestMethodNotAllowed(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true}
	br := newBrowserRelay(cfg)

	handler := br.handleToolRequest("navigate")
	req := httptest.NewRequest(http.MethodGet, "/relay/navigate", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestBrowserRelayToolRequestNoConnection(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true}
	br := newBrowserRelay(cfg)

	handler := br.handleToolRequest("navigate")
	body := strings.NewReader(`{"url": "https://example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/relay/navigate", body)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", w.Code)
	}
	respBody, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(respBody), "no browser extension connected") {
		t.Errorf("unexpected error body: %s", respBody)
	}
}

func TestBrowserRelaySendCommandNoConnection(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true}
	br := newBrowserRelay(cfg)

	_, err := br.SendCommand("navigate", json.RawMessage(`{"url":"http://example.com"}`), time.Second)
	if err == nil {
		t.Error("expected error when no connection")
	}
	if !strings.Contains(err.Error(), "no browser extension connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBrowserRelayConfigJSON(t *testing.T) {
	raw := `{"enabled": true, "port": 19000, "token": "secret123"}`
	var cfg BrowserRelayConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.Port != 19000 {
		t.Errorf("expected port=19000, got %d", cfg.Port)
	}
	if cfg.Token != "secret123" {
		t.Errorf("expected token=secret123, got %s", cfg.Token)
	}
}

func TestBrowserRelayConfigDefaults(t *testing.T) {
	var cfg BrowserRelayConfig
	if cfg.Enabled {
		t.Error("expected enabled=false by default")
	}
	if cfg.Port != 0 {
		t.Error("expected port=0 by default")
	}
	if cfg.Token != "" {
		t.Error("expected empty token by default")
	}
}

func TestToolBrowserRelayNoRelay(t *testing.T) {
	old := globalBrowserRelay
	globalBrowserRelay = nil
	defer func() { globalBrowserRelay = old }()

	handler := toolBrowserRelay("navigate")
	_, err := handler(context.Background(), &Config{}, json.RawMessage(`{"url":"http://example.com"}`))
	if err == nil {
		t.Error("expected error when globalBrowserRelay is nil")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToolBrowserRelayNotConnected(t *testing.T) {
	old := globalBrowserRelay
	globalBrowserRelay = newBrowserRelay(&BrowserRelayConfig{Enabled: true})
	defer func() { globalBrowserRelay = old }()

	handler := toolBrowserRelay("content")
	_, err := handler(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error when not connected")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRelayWSWriteReadRoundtrip(t *testing.T) {
	// Create a pipe to simulate a connection.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	msg := `{"id":"abc123","action":"navigate","params":{"url":"https://example.com"}}`
	var wg sync.WaitGroup
	wg.Add(1)

	var readErr error
	var readData []byte

	go func() {
		defer wg.Done()
		readData, readErr = relayWSReadMessage(server)
	}()

	// Write an unmasked frame (server->client direction).
	if err := relayWSWriteMessage(client, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}

	wg.Wait()
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if string(readData) != msg {
		t.Errorf("got %q, want %q", string(readData), msg)
	}
}

func TestRelayWSWriteReadLargePayload(t *testing.T) {
	// Test with payload > 125 bytes (extended length).
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	msg := strings.Repeat("x", 300) // > 125 bytes, triggers 2-byte extended length
	var wg sync.WaitGroup
	wg.Add(1)

	var readErr error
	var readData []byte

	go func() {
		defer wg.Done()
		readData, readErr = relayWSReadMessage(server)
	}()

	if err := relayWSWriteMessage(client, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}

	wg.Wait()
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if string(readData) != msg {
		t.Errorf("payload length mismatch: got %d, want %d", len(readData), len(msg))
	}
}

func TestRelayWSReadMaskedFrame(t *testing.T) {
	// Simulate a masked frame (client->server direction, as Chrome would send).
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	payload := []byte(`{"id":"test1","result":"ok"}`)
	maskKey := [4]byte{0x37, 0xfa, 0x21, 0x3d}

	var wg sync.WaitGroup
	wg.Add(1)

	var readData []byte
	var readErr error

	go func() {
		defer wg.Done()
		readData, readErr = relayWSReadMessage(server)
	}()

	// Build a masked frame manually.
	frame := []byte{0x81} // FIN + text opcode
	frame = append(frame, byte(len(payload)|0x80)) // masked + length
	frame = append(frame, maskKey[:]...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ maskKey[i%4]
	}
	frame = append(frame, masked...)

	if _, err := client.Write(frame); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	wg.Wait()
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if string(readData) != string(payload) {
		t.Errorf("got %q, want %q", string(readData), string(payload))
	}
}

func TestRelayWSReadCloseFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		// Send a close frame: opcode 0x08.
		frame := []byte{0x88, 0x00} // FIN + close opcode, zero length
		client.Write(frame)
	}()

	_, err := relayWSReadMessage(server)
	if err == nil {
		t.Error("expected error for close frame")
	}
	if !strings.Contains(err.Error(), "close frame") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBrowserRelayFullRoundtrip(t *testing.T) {
	// Integration test: start relay, connect via WebSocket, send command, get response.
	cfg := &BrowserRelayConfig{Enabled: true, Port: 0} // Port 0 = use default 18792, but we will use our own listener
	br := newBrowserRelay(cfg)

	// Use a random port to avoid conflicts.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/relay/ws", br.handleWebSocket)
	mux.HandleFunc("/relay/health", br.handleHealth)
	mux.HandleFunc("/relay/navigate", br.handleToolRequest("navigate"))

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	defer srv.Close()

	addr := listener.Addr().String()

	// Connect a fake extension via raw TCP + WebSocket handshake.
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// WebSocket handshake.
	wsKey := base64.StdEncoding.EncodeToString([]byte("test-ws-key-1234"))
	handshake := fmt.Sprintf(
		"GET /relay/ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		addr, wsKey,
	)
	if _, err := conn.Write([]byte(handshake)); err != nil {
		t.Fatalf("handshake write: %v", err)
	}

	// Read upgrade response.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("handshake read: %v", err)
	}
	resp := string(buf[:n])
	if !strings.Contains(resp, "101 Switching Protocols") {
		t.Fatalf("expected 101 response, got: %s", resp)
	}
	conn.SetReadDeadline(time.Time{}) // Clear deadline.

	// Wait for the connection to register.
	time.Sleep(50 * time.Millisecond)

	if !br.Connected() {
		t.Fatal("relay should show connected after handshake")
	}

	// Now send a command via the relay and respond from our fake extension.
	var wg sync.WaitGroup
	wg.Add(1)

	var cmdResult string
	var cmdErr error

	go func() {
		defer wg.Done()
		cmdResult, cmdErr = br.SendCommand("navigate", json.RawMessage(`{"url":"https://example.com"}`), 5*time.Second)
	}()

	// Read the command from WebSocket.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	data, err := relayWSReadMessage(conn)
	if err != nil {
		t.Fatalf("read command: %v", err)
	}
	conn.SetReadDeadline(time.Time{})

	var req relayRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.Action != "navigate" {
		t.Errorf("expected action=navigate, got %s", req.Action)
	}

	// Send response back (masked, as a client would).
	response := relayResponse{ID: req.ID, Result: "navigated to https://example.com"}
	respData, _ := json.Marshal(response)

	// Build a masked frame.
	maskKey := [4]byte{0x12, 0x34, 0x56, 0x78}
	frame := []byte{0x81} // FIN + text
	pLen := len(respData)
	if pLen <= 125 {
		frame = append(frame, byte(pLen|0x80)) // masked
	} else {
		frame = append(frame, byte(126|0x80), byte(pLen>>8), byte(pLen))
	}
	frame = append(frame, maskKey[:]...)
	masked := make([]byte, pLen)
	for i := range respData {
		masked[i] = respData[i] ^ maskKey[i%4]
	}
	frame = append(frame, masked...)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write response: %v", err)
	}

	wg.Wait()
	if cmdErr != nil {
		t.Fatalf("SendCommand error: %v", cmdErr)
	}
	if cmdResult != "navigated to https://example.com" {
		t.Errorf("unexpected result: %s", cmdResult)
	}
}

func TestBrowserRelaySendCommandTimeout(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true}
	br := newBrowserRelay(cfg)

	// Set a fake connection so SendCommand doesn't fail at the nil check.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	br.mu.Lock()
	br.conn = server
	br.mu.Unlock()

	// Drain the client side so the write doesn't block, but never send a response.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := client.Read(buf); err != nil {
				return
			}
		}
	}()

	// Use very short timeout — no response will arrive.
	_, err := br.SendCommand("navigate", json.RawMessage(`{"url":"http://example.com"}`), 50*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBrowserRelayExtensionError(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true}
	br := newBrowserRelay(cfg)

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	br.mu.Lock()
	br.conn = client
	br.mu.Unlock()

	// Start the read loop to process responses.
	go br.readLoop(client)

	var wg sync.WaitGroup
	wg.Add(1)

	var cmdErr error
	go func() {
		defer wg.Done()
		_, cmdErr = br.SendCommand("navigate", json.RawMessage(`{}`), 2*time.Second)
	}()

	// Read the request from the server side.
	data, err := relayWSReadMessage(server)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var req relayRequest
	json.Unmarshal(data, &req)

	// Send back an error response.
	resp := relayResponse{ID: req.ID, Error: "page not found"}
	respData, _ := json.Marshal(resp)
	relayWSWriteMessage(server, respData)

	wg.Wait()
	if cmdErr == nil {
		t.Error("expected error from extension")
	}
	if !strings.Contains(cmdErr.Error(), "page not found") {
		t.Errorf("unexpected error: %v", cmdErr)
	}
}

func TestBrowserRelayTokenAuth(t *testing.T) {
	cfg := &BrowserRelayConfig{Enabled: true, Token: "my-secret"}
	br := newBrowserRelay(cfg)

	// Test with correct token -- should pass the token check
	// (will fail at Sec-WebSocket-Key, but that means token passed).
	req := httptest.NewRequest(http.MethodGet, "/relay/ws?token=my-secret", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	br.handleWebSocket(w, req)
	// Should fail with 400 (missing key), not 401 (bad token).
	if w.Code != http.StatusBadRequest {
		t.Errorf("correct token: expected 400 (missing key), got %d", w.Code)
	}
}

func TestBrowserRelayConfigInConfig(t *testing.T) {
	raw := `{
		"browserRelay": {
			"enabled": true,
			"port": 19999,
			"token": "abc"
		}
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.BrowserRelay.Enabled {
		t.Error("expected browserRelay.enabled=true")
	}
	if cfg.BrowserRelay.Port != 19999 {
		t.Errorf("expected port=19999, got %d", cfg.BrowserRelay.Port)
	}
	if cfg.BrowserRelay.Token != "abc" {
		t.Errorf("expected token=abc, got %s", cfg.BrowserRelay.Token)
	}
}

// Suppress unused import warnings.
var _ = rand.Read
