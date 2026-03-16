// Package homeassistant provides a Home Assistant REST and WebSocket client.
package homeassistant

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config configures the Home Assistant integration.
type Config struct {
	Enabled    bool     `json:"enabled"`
	BaseURL    string   `json:"baseUrl"`              // e.g., "http://192.168.1.100:8123"
	Token      string   `json:"token"`                // long-lived access token ($ENV_VAR supported)
	WebSocket  bool     `json:"websocket"`            // enable WebSocket event subscription
	AreaFilter []string `json:"areaFilter,omitempty"` // optional area/room filter
}

// Entity represents a Home Assistant entity state.
type Entity struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged string         `json:"last_changed"`
	LastUpdated string         `json:"last_updated"`
}

// EventPublisher publishes events to an SSE broker or similar.
type EventPublisher interface {
	PublishEvent(key, eventType string, data any)
}

// LogFn is a structured log function accepting a message and alternating key/value pairs.
type LogFn func(msg string, keyvals ...any)

// Service wraps the Home Assistant REST + WebSocket API.
type Service struct {
	cfg     Config
	baseURL string
	token   string
	client  *http.Client

	logInfo  LogFn
	logWarn  LogFn
	logDebug LogFn

	// WebSocket reconnect state.
	wsMu       sync.Mutex
	wsConn     net.Conn
	wsStopping bool
}

// New creates a new Service. Nil log functions are replaced with no-ops.
func New(cfg Config, logInfo, logWarn, logDebug LogFn) *Service {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	noop := func(string, ...any) {}
	if logInfo == nil {
		logInfo = noop
	}
	if logWarn == nil {
		logWarn = noop
	}
	if logDebug == nil {
		logDebug = noop
	}
	return &Service{
		cfg:      cfg,
		baseURL:  baseURL,
		token:    cfg.Token,
		client:   &http.Client{Timeout: 10 * time.Second},
		logInfo:  logInfo,
		logWarn:  logWarn,
		logDebug: logDebug,
	}
}

// request performs a generic HTTP request to the HA REST API.
func (s *Service) request(method, path string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}

	reqURL := s.baseURL + path
	req, err := http.NewRequest(method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 2MB limit
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ListEntities returns entities, optionally filtered by domain (e.g. "light", "switch", "sensor").
// If AreaFilter is configured on the service, results are further narrowed by area.
func (s *Service) ListEntities(domain string) ([]Entity, error) {
	data, err := s.request("GET", "/api/states", nil)
	if err != nil {
		return nil, err
	}

	var entities []Entity
	if err := json.Unmarshal(data, &entities); err != nil {
		return nil, fmt.Errorf("parse entities: %w", err)
	}

	// Filter by domain if specified.
	if domain != "" {
		prefix := domain + "."
		filtered := make([]Entity, 0, len(entities))
		for _, e := range entities {
			if strings.HasPrefix(e.EntityID, prefix) {
				filtered = append(filtered, e)
			}
		}
		entities = filtered
	}

	// Filter by area if configured.
	if len(s.cfg.AreaFilter) > 0 {
		areaSet := make(map[string]bool, len(s.cfg.AreaFilter))
		for _, a := range s.cfg.AreaFilter {
			areaSet[strings.ToLower(a)] = true
		}
		filtered := make([]Entity, 0, len(entities))
		for _, e := range entities {
			if area, ok := e.Attributes["area"].(string); ok {
				if areaSet[strings.ToLower(area)] {
					filtered = append(filtered, e)
				}
			} else if friendlyName, ok := e.Attributes["friendly_name"].(string); ok {
				// Fallback: check if any area keyword is in the friendly name.
				lower := strings.ToLower(friendlyName)
				for a := range areaSet {
					if strings.Contains(lower, a) {
						filtered = append(filtered, e)
						break
					}
				}
			} else {
				// If no area info, include it anyway (better to show more than less).
				filtered = append(filtered, e)
			}
		}
		entities = filtered
	}

	return entities, nil
}

// GetState returns the state of a single entity.
func (s *Service) GetState(entityID string) (*Entity, error) {
	data, err := s.request("GET", "/api/states/"+entityID, nil)
	if err != nil {
		return nil, err
	}

	var entity Entity
	if err := json.Unmarshal(data, &entity); err != nil {
		return nil, fmt.Errorf("parse entity: %w", err)
	}

	return &entity, nil
}

// CallService invokes a Home Assistant service (e.g. light/turn_on).
func (s *Service) CallService(domain, service string, data map[string]any) error {
	path := fmt.Sprintf("/api/services/%s/%s", domain, service)
	_, err := s.request("POST", path, data)
	return err
}

// SetState directly sets the state of an entity.
func (s *Service) SetState(entityID, state string, attributes map[string]any) error {
	body := map[string]any{
		"state": state,
	}
	if len(attributes) > 0 {
		body["attributes"] = attributes
	}
	_, err := s.request("POST", "/api/states/"+entityID, body)
	return err
}

// StartEventListener connects to the HA WebSocket API, authenticates, subscribes
// to state_changed events, and publishes them via publisher.
// Auto-reconnects on disconnect with exponential backoff.
func (s *Service) StartEventListener(ctx context.Context, publisher EventPublisher) {
	backoff := time.Second
	maxBackoff := 2 * time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := s.wsConnect(ctx, publisher)
		if err != nil {
			s.wsMu.Lock()
			stopping := s.wsStopping
			s.wsMu.Unlock()
			if stopping {
				return
			}
			s.logWarn("ha websocket disconnected, reconnecting", "error", err, "backoff", backoff.String())
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Exponential backoff.
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// wsConnect establishes one WebSocket connection, authenticates, subscribes,
// and reads events until error or context cancellation.
func (s *Service) wsConnect(ctx context.Context, publisher EventPublisher) error {
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return fmt.Errorf("parse base url: %w", err)
	}

	// Determine WebSocket host:port.
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "8123"
		}
	}
	addr := net.JoinHostPort(host, port)

	wsScheme := "ws"
	if u.Scheme == "https" {
		wsScheme = "wss"
	}
	wsURL := wsScheme + "://" + host + ":" + port + "/api/websocket"

	// TCP connect with timeout.
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	s.wsMu.Lock()
	s.wsConn = conn
	s.wsMu.Unlock()
	defer func() {
		conn.Close()
		s.wsMu.Lock()
		s.wsConn = nil
		s.wsMu.Unlock()
	}()

	// Send HTTP upgrade request.
	wsKey := WsGenerateKey()
	upgradeReq := fmt.Sprintf(
		"GET /api/websocket HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\n"+
			"Sec-WebSocket-Version: 13\r\n"+
			"Origin: %s\r\n\r\n",
		host, wsKey, wsURL,
	)
	if _, err := conn.Write([]byte(upgradeReq)); err != nil {
		return fmt.Errorf("send upgrade: %w", err)
	}

	// Read upgrade response.
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read status: %w", err)
	}
	if !strings.Contains(statusLine, "101") {
		return fmt.Errorf("websocket upgrade failed: %s", strings.TrimSpace(statusLine))
	}
	// Consume remaining headers.
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read headers: %w", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}

	s.logInfo("ha websocket connected", "url", wsURL)

	// Read the auth_required message.
	msg, err := WsReadFrame(reader)
	if err != nil {
		return fmt.Errorf("read auth_required: %w", err)
	}
	var authReq struct {
		Type string `json:"type"`
	}
	json.Unmarshal(msg, &authReq)
	if authReq.Type != "auth_required" {
		return fmt.Errorf("expected auth_required, got: %s", string(msg))
	}

	// Send auth message.
	authMsg, _ := json.Marshal(map[string]string{
		"type":         "auth",
		"access_token": s.token,
	})
	if err := WsWriteFrame(conn, authMsg); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}

	// Read auth result.
	msg, err = WsReadFrame(reader)
	if err != nil {
		return fmt.Errorf("read auth result: %w", err)
	}
	var authResult struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	json.Unmarshal(msg, &authResult)
	if authResult.Type != "auth_ok" {
		return fmt.Errorf("auth failed: %s (%s)", authResult.Type, authResult.Message)
	}
	s.logInfo("ha websocket authenticated")

	// Subscribe to state_changed events.
	subMsg, _ := json.Marshal(map[string]any{
		"id":         1,
		"type":       "subscribe_events",
		"event_type": "state_changed",
	})
	if err := WsWriteFrame(conn, subMsg); err != nil {
		return fmt.Errorf("send subscribe: %w", err)
	}

	// Read subscription confirmation.
	msg, err = WsReadFrame(reader)
	if err != nil {
		return fmt.Errorf("read subscribe result: %w", err)
	}
	s.logDebug("ha websocket subscribed", "response", string(msg))

	// Event read loop.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Set read deadline to detect disconnects.
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		msg, err := WsReadFrame(reader)
		if err != nil {
			return fmt.Errorf("read event: %w", err)
		}

		// Parse HA event.
		var wsEvent struct {
			Type  string `json:"type"`
			Event struct {
				EventType string `json:"event_type"`
				Data      struct {
					EntityID string `json:"entity_id"`
					NewState Entity `json:"new_state"`
					OldState Entity `json:"old_state"`
				} `json:"data"`
			} `json:"event"`
		}
		if err := json.Unmarshal(msg, &wsEvent); err != nil {
			s.logDebug("ha websocket parse error", "error", err)
			continue
		}

		if wsEvent.Type != "event" {
			continue
		}

		entityID := wsEvent.Event.Data.EntityID
		if entityID == "" {
			entityID = wsEvent.Event.Data.NewState.EntityID
		}

		// Publish SSE event.
		if publisher != nil {
			sseData := map[string]any{
				"entity_id":  entityID,
				"old_state":  wsEvent.Event.Data.OldState.State,
				"new_state":  wsEvent.Event.Data.NewState.State,
				"attributes": wsEvent.Event.Data.NewState.Attributes,
			}
			publisher.PublishEvent("ha.state_changed", "ha.state_changed", sseData)
		}

		s.logDebug("ha state changed", "entity", entityID,
			"old", wsEvent.Event.Data.OldState.State,
			"new", wsEvent.Event.Data.NewState.State)
	}
}

// --- Minimal WebSocket Frame Helpers (stdlib only) ---

// WsGenerateKey generates a random base64-encoded key for the WebSocket handshake.
func WsGenerateKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	// Simple base64 encoding without importing encoding/base64.
	const enc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var result strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		remaining := len(b) - i
		if remaining >= 3 {
			n = uint32(b[i])<<16 | uint32(b[i+1])<<8 | uint32(b[i+2])
			result.WriteByte(enc[n>>18&0x3F])
			result.WriteByte(enc[n>>12&0x3F])
			result.WriteByte(enc[n>>6&0x3F])
			result.WriteByte(enc[n&0x3F])
		} else if remaining == 2 {
			n = uint32(b[i])<<16 | uint32(b[i+1])<<8
			result.WriteByte(enc[n>>18&0x3F])
			result.WriteByte(enc[n>>12&0x3F])
			result.WriteByte(enc[n>>6&0x3F])
			result.WriteByte('=')
		} else {
			n = uint32(b[i]) << 16
			result.WriteByte(enc[n>>18&0x3F])
			result.WriteByte(enc[n>>12&0x3F])
			result.WriteByte('=')
			result.WriteByte('=')
		}
	}
	return result.String()
}

// WsReadFrame reads a single WebSocket text frame from the reader.
// Handles server-to-client frames (unmasked).
func WsReadFrame(r *bufio.Reader) ([]byte, error) {
	// Byte 0: FIN + opcode.
	b0, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	opcode := b0 & 0x0F

	// Handle close frame.
	if opcode == 0x08 {
		return nil, fmt.Errorf("received close frame")
	}
	// Handle ping: read payload and discard (no pong sent from this simple client).
	if opcode == 0x09 {
		b1, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		length := int(b1 & 0x7F)
		if length > 0 {
			buf := make([]byte, length)
			if _, err := io.ReadFull(r, buf); err != nil {
				return nil, err
			}
		}
		return WsReadFrame(r) // Read next frame.
	}

	// Byte 1: mask flag + payload length.
	b1, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	masked := (b1 & 0x80) != 0
	length := int(b1 & 0x7F)

	// Extended payload length.
	if length == 126 {
		var buf [2]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, err
		}
		length = int(binary.BigEndian.Uint16(buf[:]))
	} else if length == 127 {
		var buf [8]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, err
		}
		length = int(binary.BigEndian.Uint64(buf[:]))
	}

	// Safety: limit frame size to 16MB.
	if length > 16*1024*1024 {
		return nil, fmt.Errorf("frame too large: %d bytes", length)
	}

	// Read mask key if present.
	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(r, maskKey[:]); err != nil {
			return nil, err
		}
	}

	// Read payload.
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	// Unmask if needed.
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	return payload, nil
}

// WsWriteFrame writes a masked text frame to the connection.
// Client-to-server frames must be masked per RFC 6455.
func WsWriteFrame(conn net.Conn, payload []byte) error {
	length := len(payload)

	// Calculate frame header size.
	headerSize := 2 + 4 // FIN+opcode, mask+length, 4-byte mask key
	if length >= 126 && length < 65536 {
		headerSize += 2
	} else if length >= 65536 {
		headerSize += 8
	}

	frame := make([]byte, headerSize+length)
	idx := 0

	// Byte 0: FIN + text opcode.
	frame[idx] = 0x81
	idx++

	// Byte 1: mask bit set + payload length.
	if length < 126 {
		frame[idx] = byte(0x80 | length)
		idx++
	} else if length < 65536 {
		frame[idx] = 0x80 | 126
		idx++
		binary.BigEndian.PutUint16(frame[idx:], uint16(length))
		idx += 2
	} else {
		frame[idx] = 0x80 | 127
		idx++
		binary.BigEndian.PutUint64(frame[idx:], uint64(length))
		idx += 8
	}

	// 4-byte mask key.
	var maskKey [4]byte
	rand.Read(maskKey[:])
	copy(frame[idx:], maskKey[:])
	idx += 4

	// Masked payload.
	for i, b := range payload {
		frame[idx+i] = b ^ maskKey[i%4]
	}

	_, err := conn.Write(frame)
	return err
}
