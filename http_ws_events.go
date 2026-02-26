package main

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"
)

// wsEventsHub manages WebSocket connections that mirror the SSE dashboard feed.
type wsEventsHub struct {
	mu      sync.RWMutex
	clients map[net.Conn]struct{}
}

func newWSEventsHub() *wsEventsHub {
	return &wsEventsHub{
		clients: make(map[net.Conn]struct{}),
	}
}

// add registers a new WebSocket client connection.
func (h *wsEventsHub) add(conn net.Conn) {
	h.mu.Lock()
	h.clients[conn] = struct{}{}
	h.mu.Unlock()
}

// remove unregisters and closes a WebSocket client connection.
func (h *wsEventsHub) remove(conn net.Conn) {
	h.mu.Lock()
	delete(h.clients, conn)
	h.mu.Unlock()
	conn.Close()
}

// broadcast sends a JSON-encoded event to all connected WebSocket clients.
// Clients that fail to receive are removed.
func (h *wsEventsHub) broadcast(event SSEEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	h.mu.RLock()
	conns := make([]net.Conn, 0, len(h.clients))
	for conn := range h.clients {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()

	var failed []net.Conn
	for _, conn := range conns {
		if err := relayWSWriteMessage(conn, data); err != nil {
			failed = append(failed, conn)
		}
	}
	for _, conn := range failed {
		h.remove(conn)
	}
}

// global hub instance — initialized in registerWSEventsRoutes.
var globalWSEventsHub *wsEventsHub

func (s *Server) registerWSEventsRoutes(mux *http.ServeMux) {
	state := s.state

	hub := newWSEventsHub()
	globalWSEventsHub = hub

	// Start a goroutine that subscribes to the SSE dashboard broker and forwards
	// events to all connected WebSocket clients.
	if state != nil && state.broker != nil {
		broker := state.broker
		go func() {
			// Subscribe to the global dashboard feed. This channel lives for the
			// duration of the process — the unsubscribe is called if this goroutine
			// ever exits (shouldn't happen in normal operation).
			ch, unsub := broker.Subscribe(SSEDashboardKey)
			defer unsub()
			for event := range ch {
				hub.broadcast(event)
			}
		}()
	}

	// GET /ws/events — WebSocket upgrade endpoint.
	mux.HandleFunc("/ws/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, `{"error":"websocket upgrade required"}`, http.StatusBadRequest)
			return
		}

		key := r.Header.Get("Sec-WebSocket-Key")
		if key == "" {
			http.Error(w, `{"error":"missing Sec-WebSocket-Key"}`, http.StatusBadRequest)
			return
		}

		acceptKey := computeWebSocketAccept(key)

		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, `{"error":"hijack not supported"}`, http.StatusInternalServerError)
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Send WebSocket upgrade response.
		bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		bufrw.WriteString("Upgrade: websocket\r\n")
		bufrw.WriteString("Connection: Upgrade\r\n")
		bufrw.WriteString("Sec-WebSocket-Accept: " + acceptKey + "\r\n\r\n")
		bufrw.Flush()

		hub.add(conn)
		logInfo("ws/events client connected", "remote", conn.RemoteAddr().String())

		// Read loop: drain incoming frames until client disconnects.
		// This is a server-push only endpoint; client messages are discarded.
		wsEventsReadLoop(conn)

		hub.remove(conn)
		logInfo("ws/events client disconnected")
	})
}

// wsEventsReadLoop reads and discards frames from the client until disconnect.
func wsEventsReadLoop(conn net.Conn) {
	for {
		_, err := relayWSReadMessage(conn)
		if err != nil {
			return
		}
	}
}
