package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- P20.1: Home Assistant Integration Tests ---

func TestHAConfigDefaults(t *testing.T) {
	cfg := HomeAssistantConfig{}
	if cfg.Enabled {
		t.Error("expected Enabled to be false by default")
	}
	if cfg.WebSocket {
		t.Error("expected WebSocket to be false by default")
	}
	if len(cfg.AreaFilter) != 0 {
		t.Error("expected empty AreaFilter by default")
	}
}

func TestHAConfigJSON(t *testing.T) {
	raw := `{
		"enabled": true,
		"baseUrl": "http://192.168.1.100:8123",
		"token": "$HA_TOKEN",
		"websocket": true,
		"areaFilter": ["living_room", "bedroom"]
	}`
	var cfg HomeAssistantConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.BaseURL != "http://192.168.1.100:8123" {
		t.Errorf("unexpected baseUrl: %s", cfg.BaseURL)
	}
	if cfg.Token != "$HA_TOKEN" {
		t.Errorf("unexpected token: %s", cfg.Token)
	}
	if !cfg.WebSocket {
		t.Error("expected websocket=true")
	}
	if len(cfg.AreaFilter) != 2 {
		t.Errorf("expected 2 area filters, got %d", len(cfg.AreaFilter))
	}
}

func TestNewHAService(t *testing.T) {
	cfg := HomeAssistantConfig{
		Enabled: true,
		BaseURL: "http://192.168.1.100:8123/",
		Token:   "test-token-abc",
	}
	svc := newHAService(cfg)
	if svc.baseURL != "http://192.168.1.100:8123" {
		t.Errorf("expected trailing slash stripped, got: %s", svc.baseURL)
	}
	if svc.token != "test-token-abc" {
		t.Errorf("unexpected token: %s", svc.token)
	}
	if svc.client == nil {
		t.Error("expected non-nil client")
	}
	if svc.client.Timeout != 10*1e9 { // 10 seconds in nanoseconds
		t.Errorf("unexpected timeout: %v", svc.client.Timeout)
	}
}

func TestHAEntityParsing(t *testing.T) {
	raw := `{
		"entity_id": "light.living_room",
		"state": "on",
		"attributes": {
			"friendly_name": "Living Room Light",
			"brightness": 255,
			"color_temp": 370
		},
		"last_changed": "2024-01-15T10:30:00+00:00",
		"last_updated": "2024-01-15T10:30:01+00:00"
	}`
	var entity HAEntity
	if err := json.Unmarshal([]byte(raw), &entity); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entity.EntityID != "light.living_room" {
		t.Errorf("unexpected entity_id: %s", entity.EntityID)
	}
	if entity.State != "on" {
		t.Errorf("unexpected state: %s", entity.State)
	}
	if entity.Attributes["friendly_name"] != "Living Room Light" {
		t.Errorf("unexpected friendly_name: %v", entity.Attributes["friendly_name"])
	}
	if entity.LastChanged != "2024-01-15T10:30:00+00:00" {
		t.Errorf("unexpected last_changed: %s", entity.LastChanged)
	}
}

func TestHAListEntities(t *testing.T) {
	// Mock HA server.
	entities := []HAEntity{
		{EntityID: "light.living_room", State: "on", Attributes: map[string]any{"friendly_name": "Living Room"}},
		{EntityID: "light.bedroom", State: "off", Attributes: map[string]any{"friendly_name": "Bedroom"}},
		{EntityID: "switch.kitchen", State: "on", Attributes: map[string]any{"friendly_name": "Kitchen Switch"}},
		{EntityID: "sensor.temperature", State: "22.5", Attributes: map[string]any{"friendly_name": "Temperature", "unit_of_measurement": "C"}},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/states" {
			json.NewEncoder(w).Encode(entities)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	svc := newHAService(HomeAssistantConfig{
		Enabled: true,
		BaseURL: ts.URL,
		Token:   "test-token",
	})

	// List all entities.
	all, err := svc.ListEntities("")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 entities, got %d", len(all))
	}

	// Filter by domain.
	lights, err := svc.ListEntities("light")
	if err != nil {
		t.Fatalf("list lights: %v", err)
	}
	if len(lights) != 2 {
		t.Errorf("expected 2 lights, got %d", len(lights))
	}
	for _, l := range lights {
		if !strings.HasPrefix(l.EntityID, "light.") {
			t.Errorf("unexpected entity in lights: %s", l.EntityID)
		}
	}

	// Filter by non-existent domain.
	empty, err := svc.ListEntities("climate")
	if err != nil {
		t.Fatalf("list climate: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 entities, got %d", len(empty))
	}
}

func TestHAGetState(t *testing.T) {
	entity := HAEntity{
		EntityID:   "sensor.temperature",
		State:      "22.5",
		Attributes: map[string]any{"unit_of_measurement": "C"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/states/sensor.temperature" {
			json.NewEncoder(w).Encode(entity)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	svc := newHAService(HomeAssistantConfig{
		Enabled: true,
		BaseURL: ts.URL,
		Token:   "test-token",
	})

	e, err := svc.GetState("sensor.temperature")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if e.State != "22.5" {
		t.Errorf("unexpected state: %s", e.State)
	}
	if e.Attributes["unit_of_measurement"] != "C" {
		t.Errorf("unexpected unit: %v", e.Attributes["unit_of_measurement"])
	}
}

func TestHACallService(t *testing.T) {
	var receivedBody map[string]any
	var receivedPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		receivedPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	svc := newHAService(HomeAssistantConfig{
		Enabled: true,
		BaseURL: ts.URL,
		Token:   "test-token",
	})

	data := map[string]any{
		"entity_id":  "light.living_room",
		"brightness": float64(128),
	}
	err := svc.CallService("light", "turn_on", data)
	if err != nil {
		t.Fatalf("call service: %v", err)
	}
	if receivedPath != "/api/services/light/turn_on" {
		t.Errorf("unexpected path: %s", receivedPath)
	}
	if receivedBody["entity_id"] != "light.living_room" {
		t.Errorf("unexpected entity_id in body: %v", receivedBody["entity_id"])
	}
}

func TestHASetState(t *testing.T) {
	var receivedBody map[string]any
	var receivedPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		receivedPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	svc := newHAService(HomeAssistantConfig{
		Enabled: true,
		BaseURL: ts.URL,
		Token:   "test-token",
	})

	attrs := map[string]any{"unit_of_measurement": "C"}
	err := svc.SetState("sensor.custom", "25.0", attrs)
	if err != nil {
		t.Fatalf("set state: %v", err)
	}
	if receivedPath != "/api/states/sensor.custom" {
		t.Errorf("unexpected path: %s", receivedPath)
	}
	if receivedBody["state"] != "25.0" {
		t.Errorf("unexpected state in body: %v", receivedBody["state"])
	}
	if attrMap, ok := receivedBody["attributes"].(map[string]any); !ok || attrMap["unit_of_measurement"] != "C" {
		t.Errorf("unexpected attributes in body: %v", receivedBody["attributes"])
	}
}

func TestHAErrorHandling(t *testing.T) {
	// Test connection refused.
	svc := newHAService(HomeAssistantConfig{
		Enabled: true,
		BaseURL: "http://127.0.0.1:1", // non-existent port
		Token:   "test-token",
	})

	_, err := svc.ListEntities("")
	if err == nil {
		t.Error("expected error for connection refused")
	}

	// Test invalid token (401).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message": "unauthorized"}`, http.StatusUnauthorized)
	}))
	defer ts.Close()

	svc2 := newHAService(HomeAssistantConfig{
		Enabled: true,
		BaseURL: ts.URL,
		Token:   "bad-token",
	})

	_, err = svc2.ListEntities("")
	if err == nil {
		t.Error("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}

func TestHAToolHandlerListEntities(t *testing.T) {
	entities := []HAEntity{
		{EntityID: "light.test", State: "on", Attributes: map[string]any{"friendly_name": "Test Light"}},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer test-token")
		json.NewEncoder(w).Encode(entities)
	}))
	defer ts.Close()

	// Set global service.
	oldSvc := globalHAService
	globalHAService = newHAService(HomeAssistantConfig{
		Enabled: true,
		BaseURL: ts.URL,
		Token:   "test-token",
	})
	defer func() { globalHAService = oldSvc }()

	result, err := toolHAListEntities(context.Background(), &Config{}, json.RawMessage(`{"domain":"light"}`))
	if err != nil {
		t.Fatalf("tool handler: %v", err)
	}

	var summaries []struct {
		EntityID     string `json:"entity_id"`
		State        string `json:"state"`
		FriendlyName string `json:"friendly_name"`
	}
	if err := json.Unmarshal([]byte(result), &summaries); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if len(summaries) != 1 {
		t.Errorf("expected 1 entity, got %d", len(summaries))
	}
	if summaries[0].EntityID != "light.test" {
		t.Errorf("unexpected entity: %s", summaries[0].EntityID)
	}
}

func TestHAToolHandlerNotConfigured(t *testing.T) {
	oldSvc := globalHAService
	globalHAService = nil
	defer func() { globalHAService = oldSvc }()

	_, err := toolHAListEntities(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' error, got: %v", err)
	}

	_, err = toolHAGetState(context.Background(), &Config{}, json.RawMessage(`{"entity_id":"light.test"}`))
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' error, got: %v", err)
	}

	_, err = toolHACallService(context.Background(), &Config{}, json.RawMessage(`{"domain":"light","service":"turn_on"}`))
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' error, got: %v", err)
	}

	_, err = toolHASetState(context.Background(), &Config{}, json.RawMessage(`{"entity_id":"light.test","state":"on"}`))
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' error, got: %v", err)
	}
}

func TestHAToolHandlerValidation(t *testing.T) {
	oldSvc := globalHAService
	globalHAService = newHAService(HomeAssistantConfig{
		Enabled: true,
		BaseURL: "http://localhost:9999",
		Token:   "test",
	})
	defer func() { globalHAService = oldSvc }()

	// GetState: missing entity_id.
	_, err := toolHAGetState(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "entity_id is required") {
		t.Errorf("expected 'entity_id is required', got: %v", err)
	}

	// CallService: missing domain.
	_, err = toolHACallService(context.Background(), &Config{}, json.RawMessage(`{"service":"turn_on"}`))
	if err == nil || !strings.Contains(err.Error(), "domain and service are required") {
		t.Errorf("expected 'domain and service are required', got: %v", err)
	}

	// SetState: missing entity_id.
	_, err = toolHASetState(context.Background(), &Config{}, json.RawMessage(`{"state":"on"}`))
	if err == nil || !strings.Contains(err.Error(), "entity_id and state are required") {
		t.Errorf("expected 'entity_id and state are required', got: %v", err)
	}
}

func TestWsFrameWriteRead(t *testing.T) {
	// Test WebSocket frame encoding/decoding round-trip.
	payload := []byte(`{"type":"auth","access_token":"test"}`)

	// Write a masked frame.
	var buf bytes.Buffer
	conn := &mockConn{buf: &buf}
	if err := wsWriteFrame(conn, payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	// The frame should be > payload length (header + mask + masked payload).
	if buf.Len() <= len(payload) {
		t.Errorf("frame too short: %d bytes for %d byte payload", buf.Len(), len(payload))
	}

	// Verify first byte is 0x81 (FIN + text opcode).
	if buf.Bytes()[0] != 0x81 {
		t.Errorf("expected first byte 0x81, got 0x%02x", buf.Bytes()[0])
	}

	// Verify mask bit is set.
	if buf.Bytes()[1]&0x80 == 0 {
		t.Error("expected mask bit to be set")
	}
}

func TestWsReadServerFrame(t *testing.T) {
	// Construct an unmasked server→client text frame.
	payload := []byte(`{"type":"auth_required","ha_version":"2024.1.0"}`)
	frame := make([]byte, 2+len(payload))
	frame[0] = 0x81 // FIN + text
	frame[1] = byte(len(payload)) // no mask bit
	copy(frame[2:], payload)

	reader := bufio.NewReader(bytes.NewReader(frame))
	msg, err := wsReadFrame(reader)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if string(msg) != string(payload) {
		t.Errorf("payload mismatch: got %q", string(msg))
	}
}

func TestWsReadLargeFrame(t *testing.T) {
	// Construct a frame with 126-byte extended length.
	payload := make([]byte, 200)
	for i := range payload {
		payload[i] = byte('A' + (i % 26))
	}

	frame := make([]byte, 4+len(payload))
	frame[0] = 0x81
	frame[1] = 126 // extended length marker
	frame[2] = byte(len(payload) >> 8)
	frame[3] = byte(len(payload))
	copy(frame[4:], payload)

	reader := bufio.NewReader(bytes.NewReader(frame))
	msg, err := wsReadFrame(reader)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if len(msg) != 200 {
		t.Errorf("expected 200 bytes, got %d", len(msg))
	}
}

func TestWsGenerateKey(t *testing.T) {
	key1 := wsGenerateKey()
	key2 := wsGenerateKey()

	if len(key1) != 24 { // 16 bytes base64-encoded = 24 chars
		t.Errorf("unexpected key length: %d", len(key1))
	}
	if key1 == key2 {
		t.Error("keys should be random and different")
	}
}

func TestHAAreaFilter(t *testing.T) {
	entities := []HAEntity{
		{EntityID: "light.living_room", State: "on", Attributes: map[string]any{"friendly_name": "Living Room Light", "area": "living_room"}},
		{EntityID: "light.bedroom", State: "off", Attributes: map[string]any{"friendly_name": "Bedroom Light", "area": "bedroom"}},
		{EntityID: "light.kitchen", State: "on", Attributes: map[string]any{"friendly_name": "Kitchen Light", "area": "kitchen"}},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entities)
	}))
	defer ts.Close()

	svc := newHAService(HomeAssistantConfig{
		Enabled:    true,
		BaseURL:    ts.URL,
		Token:      "test-token",
		AreaFilter: []string{"living_room", "bedroom"},
	})

	result, err := svc.ListEntities("")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 entities after area filter, got %d", len(result))
	}

	// Verify only living_room and bedroom entities are returned.
	ids := make(map[string]bool)
	for _, e := range result {
		ids[e.EntityID] = true
	}
	if !ids["light.living_room"] || !ids["light.bedroom"] {
		t.Errorf("unexpected entities after area filter: %v", ids)
	}
}

// mockConn implements net.Conn for testing wsWriteFrame.
type mockConn struct {
	buf *bytes.Buffer
}

func (m *mockConn) Write(b []byte) (int, error)           { return m.buf.Write(b) }
func (m *mockConn) Read(b []byte) (int, error)            { return m.buf.Read(b) }
func (m *mockConn) Close() error                          { return nil }
func (m *mockConn) LocalAddr() net.Addr                   { return &mockAddr{} }
func (m *mockConn) RemoteAddr() net.Addr                  { return &mockAddr{} }
func (m *mockConn) SetDeadline(_ time.Time) error         { return nil }
func (m *mockConn) SetReadDeadline(_ time.Time) error     { return nil }
func (m *mockConn) SetWriteDeadline(_ time.Time) error    { return nil }

type mockAddr struct{}
func (a *mockAddr) Network() string { return "tcp" }
func (a *mockAddr) String() string  { return "127.0.0.1:0" }

// Ensure the mock implements a subset for testing — the real net.Conn is satisfied
// at compile time by the test invocation of wsWriteFrame(conn, ...).
var _ = fmt.Sprintf // suppress unused import
