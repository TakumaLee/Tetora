package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Mock CDP Connection ---

type mockCDPConn struct {
	mu          sync.Mutex
	writeBuffer []byte
	readBuffer  []byte
	readPos     int
	closed      bool
	responses   map[int]cdpResponse // pre-configured responses
}

func newMockCDPConn() *mockCDPConn {
	return &mockCDPConn{
		responses: make(map[int]cdpResponse),
	}
}

func (m *mockCDPConn) Read(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, io.EOF
	}

	// Wait for data to be available.
	for len(m.readBuffer)-m.readPos == 0 {
		m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		m.mu.Lock()
		if m.closed {
			return 0, io.EOF
		}
	}

	n := copy(p, m.readBuffer[m.readPos:])
	m.readPos += n
	if m.readPos >= len(m.readBuffer) {
		m.readBuffer = nil
		m.readPos = 0
	}
	return n, nil
}

func (m *mockCDPConn) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, io.EOF
	}

	m.writeBuffer = append(m.writeBuffer, p...)

	// Parse CDP request and send pre-configured response.
	var req cdpRequest
	if err := json.Unmarshal(p, &req); err == nil {
		if resp, ok := m.responses[req.ID]; ok {
			respData, _ := json.Marshal(resp)
			m.readBuffer = append(m.readBuffer, respData...)
		} else {
			// Default success response.
			resp := cdpResponse{
				ID:     req.ID,
				Result: json.RawMessage(`{"ok":true}`),
			}
			respData, _ := json.Marshal(resp)
			m.readBuffer = append(m.readBuffer, respData...)
		}
	}

	return len(p), nil
}

func (m *mockCDPConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockCDPConn) setResponse(id int, resp cdpResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[id] = resp
}

// --- Test Helpers ---

func setupMockBrowserSession() *browserSession {
	conn := newMockCDPConn()
	sess := &browserSession{
		wsConn:       conn,
		pending:      make(map[int]chan cdpResponse),
		readLoopDone: make(chan struct{}),
	}
	go sess.readLoop()
	return sess
}

// --- Tests ---

func TestBrowserNavigate(t *testing.T) {
	globalSession = setupMockBrowserSession()
	defer func() { globalSession.close(); globalSession = nil }()

	input := map[string]any{"url": "https://example.com"}
	inputJSON, _ := json.Marshal(input)

	// Mock navigate response.
	mockConn := globalSession.wsConn.(*mockCDPConn)
	mockConn.setResponse(1, cdpResponse{
		ID:     1,
		Result: json.RawMessage(`{"frameId":"test-frame"}`),
	})

	var result map[string]any
	handleNavigate(1, inputJSON)

	// Since handleNavigate writes to stdout, we can't easily capture it in a test.
	// Instead, we verify the CDP call was made.
	mockConn.mu.Lock()
	sent := string(mockConn.writeBuffer)
	mockConn.mu.Unlock()

	if !strings.Contains(sent, "Page.navigate") {
		t.Errorf("expected Page.navigate call, got: %s", sent)
	}
	if !strings.Contains(sent, "https://example.com") {
		t.Errorf("expected URL in request, got: %s", sent)
	}

	// Additional validation would require capturing stdout, which is complex.
	// For now, we validate the CDP call structure.
	_ = result
}

func TestBrowserScreenshot(t *testing.T) {
	globalSession = setupMockBrowserSession()
	defer func() { globalSession.close(); globalSession = nil }()

	input := map[string]any{"format": "png"}
	inputJSON, _ := json.Marshal(input)

	mockConn := globalSession.wsConn.(*mockCDPConn)
	mockConn.setResponse(1, cdpResponse{
		ID:     1,
		Result: json.RawMessage(`{"data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="}`),
	})

	// handleScreenshot would write to stdout, but we verify the CDP call.
	handleScreenshot(1, inputJSON)

	mockConn.mu.Lock()
	sent := string(mockConn.writeBuffer)
	mockConn.mu.Unlock()

	if !strings.Contains(sent, "Page.captureScreenshot") {
		t.Errorf("expected Page.captureScreenshot call, got: %s", sent)
	}
}

func TestBrowserClick(t *testing.T) {
	globalSession = setupMockBrowserSession()
	defer func() { globalSession.close(); globalSession = nil }()

	input := map[string]any{"selector": "#button"}
	inputJSON, _ := json.Marshal(input)

	mockConn := globalSession.wsConn.(*mockCDPConn)
	// First response: Runtime.evaluate returns element coordinates.
	mockConn.setResponse(1, cdpResponse{
		ID:     1,
		Result: json.RawMessage(`{"result":{"value":{"x":100,"y":200}}}`),
	})
	// Second response: Input.dispatchMouseEvent (press).
	mockConn.setResponse(2, cdpResponse{
		ID:     2,
		Result: json.RawMessage(`{}`),
	})
	// Third response: Input.dispatchMouseEvent (release).
	mockConn.setResponse(3, cdpResponse{
		ID:     3,
		Result: json.RawMessage(`{}`),
	})

	handleClick(1, inputJSON)

	mockConn.mu.Lock()
	sent := string(mockConn.writeBuffer)
	mockConn.mu.Unlock()

	if !strings.Contains(sent, "Runtime.evaluate") {
		t.Errorf("expected Runtime.evaluate call")
	}
	if !strings.Contains(sent, "Input.dispatchMouseEvent") {
		t.Errorf("expected Input.dispatchMouseEvent call")
	}
}

func TestBrowserType(t *testing.T) {
	globalSession = setupMockBrowserSession()
	defer func() { globalSession.close(); globalSession = nil }()

	input := map[string]any{"selector": "#input", "text": "Hello"}
	inputJSON, _ := json.Marshal(input)

	mockConn := globalSession.wsConn.(*mockCDPConn)
	// Focus element response.
	mockConn.setResponse(1, cdpResponse{
		ID:     1,
		Result: json.RawMessage(`{}`),
	})
	// Typing responses (one per character).
	for i := 2; i <= 6; i++ {
		mockConn.setResponse(i, cdpResponse{
			ID:     i,
			Result: json.RawMessage(`{}`),
		})
	}

	handleType(1, inputJSON)

	mockConn.mu.Lock()
	sent := string(mockConn.writeBuffer)
	mockConn.mu.Unlock()

	if !strings.Contains(sent, "Runtime.evaluate") {
		t.Errorf("expected focus call")
	}
	if !strings.Contains(sent, "Input.dispatchKeyEvent") {
		t.Errorf("expected typing call")
	}
}

func TestBrowserEval(t *testing.T) {
	globalSession = setupMockBrowserSession()
	defer func() { globalSession.close(); globalSession = nil }()

	input := map[string]any{"expression": "1 + 1"}
	inputJSON, _ := json.Marshal(input)

	mockConn := globalSession.wsConn.(*mockCDPConn)
	mockConn.setResponse(1, cdpResponse{
		ID:     1,
		Result: json.RawMessage(`{"result":{"type":"number","value":2}}`),
	})

	handleEval(1, inputJSON)

	mockConn.mu.Lock()
	sent := string(mockConn.writeBuffer)
	mockConn.mu.Unlock()

	if !strings.Contains(sent, "Runtime.evaluate") {
		t.Errorf("expected Runtime.evaluate call")
	}
	if !strings.Contains(sent, "1 + 1") {
		t.Errorf("expected expression in request")
	}
}

func TestBrowserContent(t *testing.T) {
	globalSession = setupMockBrowserSession()
	defer func() { globalSession.close(); globalSession = nil }()

	mockConn := globalSession.wsConn.(*mockCDPConn)
	mockConn.setResponse(1, cdpResponse{
		ID:     1,
		Result: json.RawMessage(`{"result":{"value":"Page content text"}}`),
	})

	handleContent(1, json.RawMessage("{}"))

	mockConn.mu.Lock()
	sent := string(mockConn.writeBuffer)
	mockConn.mu.Unlock()

	if !strings.Contains(sent, "document.body.innerText") {
		t.Errorf("expected innerText extraction")
	}
}

func TestBrowserWaitSelector(t *testing.T) {
	globalSession = setupMockBrowserSession()
	defer func() { globalSession.close(); globalSession = nil }()

	input := map[string]any{"selector": "#element", "timeout": 1000}
	inputJSON, _ := json.Marshal(input)

	mockConn := globalSession.wsConn.(*mockCDPConn)
	mockConn.setResponse(1, cdpResponse{
		ID:     1,
		Result: json.RawMessage(`{"result":{"type":"boolean","value":true}}`),
	})

	handleWait(1, inputJSON)

	mockConn.mu.Lock()
	sent := string(mockConn.writeBuffer)
	mockConn.mu.Unlock()

	if !strings.Contains(sent, "Runtime.evaluate") {
		t.Errorf("expected wait via evaluate")
	}
}

func TestBrowserWaitTimeout(t *testing.T) {
	globalSession = setupMockBrowserSession()
	defer func() { globalSession.close(); globalSession = nil }()

	input := map[string]any{"timeout": 100}
	inputJSON, _ := json.Marshal(input)

	start := time.Now()
	handleWait(1, inputJSON)
	elapsed := time.Since(start)

	if elapsed < 100*time.Millisecond {
		t.Errorf("expected wait for at least 100ms, got %v", elapsed)
	}
}

func TestCDPSessionSendReceive(t *testing.T) {
	sess := setupMockBrowserSession()
	defer sess.close()

	mockConn := sess.wsConn.(*mockCDPConn)
	mockConn.setResponse(1, cdpResponse{
		ID:     1,
		Result: json.RawMessage(`{"testKey":"testValue"}`),
	})

	result, err := sess.sendCDP("Test.method", map[string]any{"arg": "value"})
	if err != nil {
		t.Fatalf("sendCDP failed: %v", err)
	}

	var resultMap map[string]string
	if err := json.Unmarshal(result, &resultMap); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	if resultMap["testKey"] != "testValue" {
		t.Errorf("expected testValue, got %s", resultMap["testKey"])
	}
}

func TestCDPSessionError(t *testing.T) {
	sess := setupMockBrowserSession()
	defer sess.close()

	mockConn := sess.wsConn.(*mockCDPConn)
	mockConn.setResponse(1, cdpResponse{
		ID: 1,
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{
			Code:    -32000,
			Message: "test error",
		},
	})

	_, err := sess.sendCDP("Test.error", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "test error") {
		t.Errorf("expected error message, got: %v", err)
	}
}

func TestWebSocketFraming(t *testing.T) {
	// Test WebSocket frame encode/decode.
	var buf bytes.Buffer
	conn := &wsConn{conn: &mockReadWriteCloser{buf: &buf}}

	testData := []byte(`{"test":"data"}`)
	n, err := conn.Write(testData)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != len(testData) {
		t.Errorf("expected %d bytes written, got %d", len(testData), n)
	}

	// Verify frame structure (FIN + text opcode + length + payload).
	frame := buf.Bytes()
	if frame[0] != 0x81 {
		t.Errorf("expected FIN + text frame (0x81), got 0x%02x", frame[0])
	}
	if int(frame[1]) != len(testData) {
		t.Errorf("expected length %d, got %d", len(testData), frame[1])
	}
	if !bytes.Equal(frame[2:], testData) {
		t.Errorf("payload mismatch")
	}
}

// --- Mock ReadWriteCloser ---

type mockReadWriteCloser struct {
	buf *bytes.Buffer
}

func (m *mockReadWriteCloser) Read(p []byte) (int, error) {
	return m.buf.Read(p)
}

func (m *mockReadWriteCloser) Write(p []byte) (int, error) {
	return m.buf.Write(p)
}

func (m *mockReadWriteCloser) Close() error {
	return nil
}
