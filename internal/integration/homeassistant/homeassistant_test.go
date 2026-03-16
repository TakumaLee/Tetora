package homeassistant

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- mock helpers ---

type mockAddr struct{}

func (mockAddr) Network() string { return "tcp" }
func (mockAddr) String() string  { return "mock:0" }

// mockConn is an in-memory net.Conn backed by two byte buffers.
type mockConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
}

func newMockConn(initial []byte) *mockConn {
	return &mockConn{
		readBuf:  bytes.NewBuffer(initial),
		writeBuf: &bytes.Buffer{},
	}
}

func (c *mockConn) Read(b []byte) (int, error)         { return c.readBuf.Read(b) }
func (c *mockConn) Write(b []byte) (int, error)        { return c.writeBuf.Write(b) }
func (c *mockConn) Close() error                       { return nil }
func (c *mockConn) LocalAddr() net.Addr                { return mockAddr{} }
func (c *mockConn) RemoteAddr() net.Addr               { return mockAddr{} }
func (c *mockConn) SetDeadline(t time.Time) error      { return nil }
func (c *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *mockConn) SetWriteDeadline(t time.Time) error { return nil }

// --- Config tests ---

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	if cfg.Enabled {
		t.Error("expected Enabled to default to false")
	}
	if cfg.WebSocket {
		t.Error("expected WebSocket to default to false")
	}
	if cfg.BaseURL != "" {
		t.Error("expected BaseURL to default to empty string")
	}
	if len(cfg.AreaFilter) != 0 {
		t.Error("expected AreaFilter to default to nil/empty")
	}
}

func TestConfigJSON(t *testing.T) {
	raw := `{
		"enabled": true,
		"baseUrl": "http://ha.local:8123",
		"token": "tok_abc",
		"websocket": true,
		"areaFilter": ["living_room", "bedroom"]
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if cfg.BaseURL != "http://ha.local:8123" {
		t.Errorf("unexpected BaseURL: %s", cfg.BaseURL)
	}
	if cfg.Token != "tok_abc" {
		t.Errorf("unexpected Token: %s", cfg.Token)
	}
	if !cfg.WebSocket {
		t.Error("expected WebSocket=true")
	}
	if len(cfg.AreaFilter) != 2 || cfg.AreaFilter[0] != "living_room" {
		t.Errorf("unexpected AreaFilter: %v", cfg.AreaFilter)
	}
}

// --- Constructor tests ---

func TestNewService(t *testing.T) {
	cfg := Config{
		BaseURL: "http://192.168.1.10:8123/",
		Token:   "mytoken",
	}
	svc := New(cfg, nil, nil, nil)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.baseURL != "http://192.168.1.10:8123" {
		t.Errorf("trailing slash not stripped, got: %s", svc.baseURL)
	}
	if svc.token != "mytoken" {
		t.Errorf("unexpected token: %s", svc.token)
	}
	// Logger noop should not panic.
	svc.logInfo("test message", "key", "value")
	svc.logWarn("test warn")
	svc.logDebug("test debug")
}

func TestNewServiceLoggersStored(t *testing.T) {
	var infoGot, warnGot, debugGot string
	logInfo := func(msg string, _ ...any) { infoGot = msg }
	logWarn := func(msg string, _ ...any) { warnGot = msg }
	logDebug := func(msg string, _ ...any) { debugGot = msg }

	svc := New(Config{}, logInfo, logWarn, logDebug)
	svc.logInfo("info-msg")
	svc.logWarn("warn-msg")
	svc.logDebug("debug-msg")

	if infoGot != "info-msg" {
		t.Errorf("logInfo not wired, got %q", infoGot)
	}
	if warnGot != "warn-msg" {
		t.Errorf("logWarn not wired, got %q", warnGot)
	}
	if debugGot != "debug-msg" {
		t.Errorf("logDebug not wired, got %q", debugGot)
	}
}

// --- Entity parsing ---

func TestEntityParsing(t *testing.T) {
	raw := `{
		"entity_id": "light.living_room",
		"state": "on",
		"attributes": {"brightness": 200, "friendly_name": "Living Room Light"},
		"last_changed": "2024-01-01T00:00:00+00:00",
		"last_updated": "2024-01-01T00:01:00+00:00"
	}`
	var e Entity
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("unmarshal entity: %v", err)
	}
	if e.EntityID != "light.living_room" {
		t.Errorf("unexpected EntityID: %s", e.EntityID)
	}
	if e.State != "on" {
		t.Errorf("unexpected State: %s", e.State)
	}
	if e.LastChanged == "" {
		t.Error("expected LastChanged to be set")
	}
	if e.LastUpdated == "" {
		t.Error("expected LastUpdated to be set")
	}
	if name, _ := e.Attributes["friendly_name"].(string); name != "Living Room Light" {
		t.Errorf("unexpected friendly_name: %s", name)
	}
}

// --- HTTP-backed tests ---

func newTestService(t *testing.T, srv *httptest.Server) *Service {
	t.Helper()
	cfg := Config{
		BaseURL: srv.URL,
		Token:   "test-token",
	}
	return New(cfg, nil, nil, nil)
}

func TestListEntities(t *testing.T) {
	entities := []Entity{
		{EntityID: "light.kitchen", State: "on", Attributes: map[string]any{}},
		{EntityID: "light.bedroom", State: "off", Attributes: map[string]any{}},
		{EntityID: "switch.fan", State: "on", Attributes: map[string]any{}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entities)
	}))
	defer srv.Close()

	svc := newTestService(t, srv)

	// No domain filter.
	all, err := svc.ListEntities("")
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 entities, got %d", len(all))
	}

	// Domain filter "light".
	lights, err := svc.ListEntities("light")
	if err != nil {
		t.Fatalf("ListEntities(light): %v", err)
	}
	if len(lights) != 2 {
		t.Errorf("expected 2 lights, got %d", len(lights))
	}
	for _, e := range lights {
		if !strings.HasPrefix(e.EntityID, "light.") {
			t.Errorf("unexpected entity in light domain: %s", e.EntityID)
		}
	}

	// Domain filter with no matches.
	none, err := svc.ListEntities("sensor")
	if err != nil {
		t.Fatalf("ListEntities(sensor): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 sensors, got %d", len(none))
	}
}

func TestGetState(t *testing.T) {
	entity := Entity{
		EntityID:   "sensor.temperature",
		State:      "21.5",
		Attributes: map[string]any{"unit_of_measurement": "°C"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states/sensor.temperature" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entity)
	}))
	defer srv.Close()

	svc := newTestService(t, srv)
	got, err := svc.GetState("sensor.temperature")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if got.EntityID != "sensor.temperature" {
		t.Errorf("unexpected EntityID: %s", got.EntityID)
	}
	if got.State != "21.5" {
		t.Errorf("unexpected State: %s", got.State)
	}
}

func TestCallService(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	svc := newTestService(t, srv)
	err := svc.CallService("light", "turn_on", map[string]any{"entity_id": "light.kitchen"})
	if err != nil {
		t.Fatalf("CallService: %v", err)
	}
	if gotPath != "/api/services/light/turn_on" {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if id, _ := gotBody["entity_id"].(string); id != "light.kitchen" {
		t.Errorf("unexpected entity_id in body: %v", gotBody)
	}
}

func TestSetState(t *testing.T) {
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"entity_id": "input_boolean.test", "state": "on"})
	}))
	defer srv.Close()

	svc := newTestService(t, srv)
	err := svc.SetState("input_boolean.test", "on", map[string]any{"icon": "mdi:check"})
	if err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if gotPath != "/api/states/input_boolean.test" {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if state, _ := gotBody["state"].(string); state != "on" {
		t.Errorf("unexpected state in body: %v", gotBody)
	}
	if attrs, ok := gotBody["attributes"].(map[string]any); !ok || attrs["icon"] != "mdi:check" {
		t.Errorf("unexpected attributes in body: %v", gotBody)
	}
}

func TestSetStateNoAttributes(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	svc := newTestService(t, srv)
	if err := svc.SetState("sensor.x", "unavailable", nil); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if _, hasAttrs := gotBody["attributes"]; hasAttrs {
		t.Error("expected no attributes key when attributes is nil")
	}
}

// --- Error handling ---

func TestErrorHandlingConnectionRefused(t *testing.T) {
	cfg := Config{
		BaseURL: "http://127.0.0.1:1", // port 1 should be refused
		Token:   "tok",
	}
	svc := New(cfg, nil, nil, nil)
	_, err := svc.GetState("light.x")
	if err == nil {
		t.Error("expected error on connection refused")
	}
}

func TestErrorHandling401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	svc := newTestService(t, srv)
	_, err := svc.GetState("light.x")
	if err == nil {
		t.Error("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}

func TestErrorHandlingBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "not json{{{")
	}))
	defer srv.Close()

	svc := newTestService(t, srv)
	_, err := svc.GetState("light.x")
	if err == nil {
		t.Error("expected parse error on bad JSON")
	}
}

// --- Area filter ---

func TestAreaFilter(t *testing.T) {
	entities := []Entity{
		{
			EntityID: "light.a",
			State:    "on",
			Attributes: map[string]any{
				"area":          "Living Room",
				"friendly_name": "Main Light",
			},
		},
		{
			EntityID: "light.b",
			State:    "off",
			Attributes: map[string]any{
				"friendly_name": "Bedroom Lamp",
			},
		},
		{
			EntityID: "light.c",
			State:    "on",
			Attributes: map[string]any{
				"area": "kitchen",
			},
		},
		{
			EntityID:   "light.d",
			State:      "on",
			Attributes: map[string]any{},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entities)
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:    srv.URL,
		Token:      "tok",
		AreaFilter: []string{"living room", "bedroom"},
	}
	svc := New(cfg, nil, nil, nil)

	result, err := svc.ListEntities("")
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}

	// light.a: area "Living Room" matches "living room" (case insensitive) → included
	// light.b: no area, friendly_name "Bedroom Lamp" contains "bedroom" → included
	// light.c: area "kitchen" does not match → excluded
	// light.d: no area info → included (fallback)
	if len(result) != 3 {
		ids := make([]string, len(result))
		for i, e := range result {
			ids[i] = e.EntityID
		}
		t.Errorf("expected 3 entities, got %d: %v", len(result), ids)
	}

	for _, e := range result {
		if e.EntityID == "light.c" {
			t.Error("light.c (kitchen) should have been filtered out")
		}
	}
}

// --- WebSocket frame helpers ---

// buildServerFrame constructs an unmasked WebSocket text frame (server → client).
func buildServerFrame(payload []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(0x81) // FIN + text opcode
	if len(payload) < 126 {
		buf.WriteByte(byte(len(payload))) // no mask bit
	} else if len(payload) < 65536 {
		buf.WriteByte(126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(len(payload)))
		buf.Write(ext[:])
	} else {
		buf.WriteByte(127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
		buf.Write(ext[:])
	}
	buf.Write(payload)
	return buf.Bytes()
}

func TestWsFrameWriteRead(t *testing.T) {
	original := []byte(`{"type":"subscribe_events","id":1}`)

	conn := newMockConn(nil)
	if err := WsWriteFrame(conn, original); err != nil {
		t.Fatalf("WsWriteFrame: %v", err)
	}

	// Feed the written bytes back as a reader, but they're masked so we need
	// to decode manually. Instead, verify the round-trip by building an
	// unmasked server frame from the same payload and reading it back.
	serverFrame := buildServerFrame(original)
	reader := bufio.NewReader(bytes.NewReader(serverFrame))
	got, err := WsReadFrame(reader)
	if err != nil {
		t.Fatalf("WsReadFrame: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("payload mismatch: got %q, want %q", got, original)
	}
}

func TestWsReadServerFrame(t *testing.T) {
	payload := []byte(`{"type":"auth_required","ha_version":"2024.1.0"}`)
	frame := buildServerFrame(payload)
	reader := bufio.NewReader(bytes.NewReader(frame))

	got, err := WsReadFrame(reader)
	if err != nil {
		t.Fatalf("WsReadFrame: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("unexpected payload: got %q, want %q", got, payload)
	}
}

func TestWsReadLargeFrame(t *testing.T) {
	// Build a payload of exactly 200 bytes (triggers 126 extended-length path).
	payload := bytes.Repeat([]byte("x"), 200)
	frame := buildServerFrame(payload)
	reader := bufio.NewReader(bytes.NewReader(frame))

	got, err := WsReadFrame(reader)
	if err != nil {
		t.Fatalf("WsReadFrame (large): %v", err)
	}
	if len(got) != 200 {
		t.Errorf("expected 200 bytes, got %d", len(got))
	}
	if !bytes.Equal(got, payload) {
		t.Error("payload content mismatch")
	}
}

func TestWsReadCloseFrame(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0x88) // FIN + close opcode
	buf.WriteByte(0x00) // zero length
	reader := bufio.NewReader(&buf)

	_, err := WsReadFrame(reader)
	if err == nil {
		t.Error("expected error for close frame")
	}
	if !strings.Contains(err.Error(), "close frame") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestWsReadPingThenData(t *testing.T) {
	data := []byte(`{"type":"pong"}`)

	var buf bytes.Buffer
	// Write a ping frame first.
	buf.WriteByte(0x89)            // FIN + ping opcode
	buf.WriteByte(0x04)            // 4-byte payload, unmasked
	buf.Write([]byte("ping"))      // ping payload
	buf.Write(buildServerFrame(data)) // then a normal text frame

	reader := bufio.NewReader(&buf)
	got, err := WsReadFrame(reader)
	if err != nil {
		t.Fatalf("WsReadFrame after ping: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("expected data after ping, got %q", got)
	}
}

func TestWsWriteFrameRoundTrip(t *testing.T) {
	// Write a masked frame and decode it manually to verify masking is correct.
	payload := []byte("hello websocket")
	conn := newMockConn(nil)
	if err := WsWriteFrame(conn, payload); err != nil {
		t.Fatalf("WsWriteFrame: %v", err)
	}

	written := conn.writeBuf.Bytes()
	if len(written) < 6 {
		t.Fatalf("frame too short: %d bytes", len(written))
	}

	// Byte 0: FIN + text opcode.
	if written[0] != 0x81 {
		t.Errorf("expected 0x81, got 0x%02x", written[0])
	}
	// Byte 1: mask bit must be set.
	if written[1]&0x80 == 0 {
		t.Error("mask bit not set in client frame")
	}
	payloadLen := int(written[1] & 0x7F)
	if payloadLen != len(payload) {
		t.Errorf("expected payload length %d, got %d", len(payload), payloadLen)
	}

	// Extract mask key and decode payload.
	maskKey := written[2:6]
	masked := written[6:]
	decoded := make([]byte, len(masked))
	for i, b := range masked {
		decoded[i] = b ^ maskKey[i%4]
	}
	if !bytes.Equal(decoded, payload) {
		t.Errorf("decoded payload mismatch: got %q, want %q", decoded, payload)
	}
}

func TestWsGenerateKey(t *testing.T) {
	key := WsGenerateKey()
	if len(key) == 0 {
		t.Error("expected non-empty key")
	}
	// Base64 output for 16 bytes is always 24 characters (with padding).
	if len(key) != 24 {
		t.Errorf("expected 24-char base64 key, got length %d: %q", len(key), key)
	}

	// Keys should be random — two consecutive calls should differ.
	key2 := WsGenerateKey()
	if key == key2 {
		t.Error("expected different keys from consecutive calls (extremely unlikely to collide)")
	}

	// All characters must be valid base64.
	const validChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
	for i, c := range key {
		if !strings.ContainsRune(validChars, c) {
			t.Errorf("invalid base64 character %q at position %d", c, i)
		}
	}
}

// --- EventPublisher interface ---

type mockPublisher struct {
	events []struct {
		key, eventType string
		data           any
	}
}

func (m *mockPublisher) PublishEvent(key, eventType string, data any) {
	m.events = append(m.events, struct {
		key, eventType string
		data           any
	}{key, eventType, data})
}

func TestEventPublisherInterface(t *testing.T) {
	// Verify mockPublisher satisfies the interface at compile time.
	var _ EventPublisher = (*mockPublisher)(nil)

	pub := &mockPublisher{}
	pub.PublishEvent("ha.state_changed", "ha.state_changed", map[string]any{"entity_id": "light.x"})
	if len(pub.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(pub.events))
	}
	if pub.events[0].key != "ha.state_changed" {
		t.Errorf("unexpected key: %s", pub.events[0].key)
	}
}

// --- request helper: auth header ---

func TestRequestAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"entity_id":"x","state":"on","attributes":{}}`)
	}))
	defer srv.Close()

	svc := newTestService(t, srv)
	_, err := svc.GetState("x")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("unexpected Authorization header: %q", gotAuth)
	}
}
