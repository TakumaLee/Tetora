package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// --- SSE Event Types ---

// SSEEvent represents a Server-Sent Event for task/session streaming.
type SSEEvent struct {
	Type      string `json:"type"`                // "started", "progress", "output_chunk", "completed", "error", "heartbeat"
	TaskID    string `json:"taskId,omitempty"`     // which task produced this event
	SessionID string `json:"sessionId,omitempty"`  // which session this belongs to
	Data      any    `json:"data,omitempty"`       // event-specific payload
	Timestamp string `json:"timestamp"`            // RFC3339
}

// SSE event type constants.
const (
	SSEStarted     = "started"
	SSEProgress    = "progress"
	SSEOutputChunk = "output_chunk"
	SSECompleted   = "completed"
	SSEError       = "error"
	SSEHeartbeat   = "heartbeat"
	SSEQueued       = "task_queued"
	SSETaskReceived = "task_received"
	SSETaskRouting       = "task_routing"
	SSEDiscordProcessing = "discord_processing"
	SSEDiscordReplying   = "discord_replying"
	SSEDashboardKey      = "__dashboard__"
	SSEToolCall          = "tool_call"
	SSEToolResult        = "tool_result"
)

// --- SSE Broker ---

// sseBroker is an in-memory pub/sub hub for SSE events.
// Clients subscribe by key (task ID or session ID) and receive events on a channel.
type sseBroker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan SSEEvent]struct{} // key -> set of channels
}

func newSSEBroker() *sseBroker {
	return &sseBroker{
		subscribers: make(map[string]map[chan SSEEvent]struct{}),
	}
}

// Subscribe registers a new listener for the given key.
// Returns the event channel and an unsubscribe function.
// The channel is buffered to avoid blocking publishers.
func (b *sseBroker) Subscribe(key string) (chan SSEEvent, func()) {
	ch := make(chan SSEEvent, 64)
	b.mu.Lock()
	if b.subscribers[key] == nil {
		b.subscribers[key] = make(map[chan SSEEvent]struct{})
	}
	b.subscribers[key][ch] = struct{}{}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.subscribers[key]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(b.subscribers, key)
			}
		}
		// Drain remaining events to prevent goroutine leaks.
		for len(ch) > 0 {
			<-ch
		}
	}
	return ch, unsubscribe
}

// Publish sends an event to all subscribers of the given key.
// Non-blocking: if a subscriber's channel is full, the event is dropped.
func (b *sseBroker) Publish(key string, event SSEEvent) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339)
	}
	b.mu.RLock()
	subs := b.subscribers[key]
	b.mu.RUnlock()

	for ch := range subs {
		select {
		case ch <- event:
		default:
			// Channel full — drop event to avoid blocking.
		}
	}
}

// PublishMulti sends an event to subscribers of all given keys.
func (b *sseBroker) PublishMulti(keys []string, event SSEEvent) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339)
	}
	b.mu.RLock()
	// Collect unique channels to avoid sending duplicates.
	seen := make(map[chan SSEEvent]struct{})
	for _, key := range keys {
		for ch := range b.subscribers[key] {
			seen[ch] = struct{}{}
		}
	}
	b.mu.RUnlock()

	for ch := range seen {
		select {
		case ch <- event:
		default:
		}
	}
}

// HasSubscribers returns true if the given key has any active subscribers.
func (b *sseBroker) HasSubscribers(key string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers[key]) > 0
}

// --- SSE HTTP Helper ---

// serveSSE handles an SSE connection for the given subscription key.
// It writes events from the broker and sends heartbeats every 15s.
// Blocks until the client disconnects or a terminal event (completed/error) is received.
func serveSSE(w http.ResponseWriter, r *http.Request, broker *sseBroker, key string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx proxy support

	ch, unsub := broker.Subscribe(key)
	defer unsub()

	var eventID atomic.Int64

	// Write initial comment to establish connection.
	fmt.Fprintf(w, ": connected to %s\n\n", key)
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-ch:
			if !ok {
				return
			}
			id := eventID.Add(1)
			writeSSEEvent(w, id, event)
			flusher.Flush()

			// Close connection on terminal events.
			if event.Type == SSECompleted || event.Type == SSEError {
				return
			}

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

// serveDashboardSSE handles a persistent SSE connection for the global dashboard feed.
// Unlike serveSSE, it does NOT close on completed/error events (persistent stream).
func serveDashboardSSE(w http.ResponseWriter, r *http.Request, broker *sseBroker) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := broker.Subscribe(SSEDashboardKey)
	defer unsub()

	var eventID atomic.Int64

	fmt.Fprintf(w, ": connected to dashboard\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-ch:
			if !ok {
				return
			}
			id := eventID.Add(1)
			writeSSEEvent(w, id, event)
			flusher.Flush()

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

// writeSSEEvent formats and writes a single SSE event to the writer.
func writeSSEEvent(w http.ResponseWriter, id int64, event SSEEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event.Type, string(data))
}
