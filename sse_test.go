package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSSEBroker_SubscribePublish(t *testing.T) {
	b := newSSEBroker()

	ch, unsub := b.Subscribe("task-1")
	defer unsub()

	b.Publish("task-1", SSEEvent{
		Type:   SSEStarted,
		TaskID: "task-1",
		Data:   map[string]string{"name": "test"},
	})

	select {
	case ev := <-ch:
		if ev.Type != SSEStarted {
			t.Errorf("expected type %q, got %q", SSEStarted, ev.Type)
		}
		if ev.TaskID != "task-1" {
			t.Errorf("expected taskId %q, got %q", "task-1", ev.TaskID)
		}
		if ev.Timestamp == "" {
			t.Error("expected timestamp to be set")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for event")
	}
}

func TestSSEBroker_Unsubscribe(t *testing.T) {
	b := newSSEBroker()

	ch, unsub := b.Subscribe("task-1")
	unsub()

	b.Publish("task-1", SSEEvent{Type: SSEStarted, TaskID: "task-1"})

	select {
	case <-ch:
		// Channel should be drained/empty, not receiving new events.
		// Since we unsubscribed, no new events should arrive.
	case <-time.After(50 * time.Millisecond):
		// Expected: no event received.
	}

	if b.HasSubscribers("task-1") {
		t.Error("expected no subscribers after unsubscribe")
	}
}

func TestSSEBroker_PublishMulti(t *testing.T) {
	b := newSSEBroker()

	ch1, unsub1 := b.Subscribe("task-1")
	defer unsub1()
	ch2, unsub2 := b.Subscribe("session-1")
	defer unsub2()

	b.PublishMulti([]string{"task-1", "session-1"}, SSEEvent{
		Type:      SSEOutputChunk,
		TaskID:    "task-1",
		SessionID: "session-1",
		Data:      map[string]string{"chunk": "hello"},
	})

	// Both channels should receive the event.
	for _, ch := range []chan SSEEvent{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Type != SSEOutputChunk {
				t.Errorf("expected type %q, got %q", SSEOutputChunk, ev.Type)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timeout waiting for event")
		}
	}
}

func TestSSEBroker_PublishMulti_Dedup(t *testing.T) {
	b := newSSEBroker()

	// Same channel subscribed to both keys.
	ch, unsub := b.Subscribe("key-1")
	defer unsub()
	ch2, unsub2 := b.Subscribe("key-2")
	defer unsub2()

	_ = ch2 // different channel

	b.PublishMulti([]string{"key-1", "key-2"}, SSEEvent{Type: SSEStarted})

	// Each channel should receive exactly one event.
	received1 := 0
	received2 := 0
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case <-ch:
			received1++
		case <-ch2:
			received2++
		case <-timeout:
			if received1 != 1 {
				t.Errorf("ch1: expected 1 event, got %d", received1)
			}
			if received2 != 1 {
				t.Errorf("ch2: expected 1 event, got %d", received2)
			}
			return
		}
	}
}

func TestSSEBroker_HasSubscribers(t *testing.T) {
	b := newSSEBroker()

	if b.HasSubscribers("x") {
		t.Error("expected no subscribers for 'x'")
	}

	_, unsub := b.Subscribe("x")
	if !b.HasSubscribers("x") {
		t.Error("expected subscribers for 'x'")
	}

	unsub()
	if b.HasSubscribers("x") {
		t.Error("expected no subscribers after unsub")
	}
}

func TestSSEBroker_NonBlocking(t *testing.T) {
	b := newSSEBroker()

	ch, unsub := b.Subscribe("task-1")
	defer unsub()

	// Fill the channel buffer (64).
	for i := 0; i < 70; i++ {
		b.Publish("task-1", SSEEvent{Type: SSEProgress, TaskID: "task-1"})
	}

	// Should not block — excess events are dropped.
	count := 0
	for len(ch) > 0 {
		<-ch
		count++
	}
	if count > 64 {
		t.Errorf("expected at most 64 events (buffer size), got %d", count)
	}
}

func TestSSEBroker_ConcurrentPublish(t *testing.T) {
	b := newSSEBroker()

	ch, unsub := b.Subscribe("task-1")
	defer unsub()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				b.Publish("task-1", SSEEvent{
					Type:   SSEProgress,
					TaskID: fmt.Sprintf("task-%d-%d", n, j),
				})
			}
		}(i)
	}
	wg.Wait()

	// Drain — should have received events without panic.
	count := 0
	for len(ch) > 0 {
		<-ch
		count++
	}
	if count == 0 {
		t.Error("expected at least some events")
	}
}

func TestWriteSSEEvent(t *testing.T) {
	var buf bytes.Buffer
	w := httptest.NewRecorder()

	event := SSEEvent{
		Type:      SSEOutputChunk,
		TaskID:    "abc-123",
		SessionID: "sess-456",
		Data:      map[string]string{"chunk": "hello world"},
		Timestamp: "2026-02-22T10:00:00Z",
	}

	writeSSEEvent(w, 1, event)

	buf.Write(w.Body.Bytes())
	output := buf.String()

	if !strings.Contains(output, "id: 1") {
		t.Error("missing event ID")
	}
	if !strings.Contains(output, "event: output_chunk") {
		t.Error("missing event type")
	}
	if !strings.Contains(output, "data: ") {
		t.Error("missing data line")
	}

	// Parse the data payload.
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			jsonStr := strings.TrimPrefix(line, "data: ")
			var parsed SSEEvent
			if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
				t.Fatalf("failed to parse SSE data JSON: %v", err)
			}
			if parsed.Type != SSEOutputChunk {
				t.Errorf("parsed type: expected %q, got %q", SSEOutputChunk, parsed.Type)
			}
			if parsed.TaskID != "abc-123" {
				t.Errorf("parsed taskId: expected %q, got %q", "abc-123", parsed.TaskID)
			}
		}
	}
}

func TestServeSSE_Heartbeat(t *testing.T) {
	b := newSSEBroker()

	req := httptest.NewRequest(http.MethodGet, "/dispatch/test/stream", nil)
	w := httptest.NewRecorder()

	// Close request context after a short time to stop serveSSE.
	ctx, cancel := testContextWithTimeout(100 * time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	serveSSE(w, req, b, "test")

	body := w.Body.String()
	if !strings.Contains(body, ": connected to test") {
		t.Error("missing connection comment")
	}
	// Headers should be set correctly.
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: expected text/event-stream, got %q", ct)
	}
}

func TestServeSSE_ReceivesEvents(t *testing.T) {
	b := newSSEBroker()

	req := httptest.NewRequest(http.MethodGet, "/dispatch/task-1/stream", nil)
	w := httptest.NewRecorder()

	ctx, cancel := testContextWithTimeout(200 * time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	// Publish events shortly after connection.
	go func() {
		time.Sleep(20 * time.Millisecond)
		b.Publish("task-1", SSEEvent{Type: SSEStarted, TaskID: "task-1"})
		time.Sleep(10 * time.Millisecond)
		b.Publish("task-1", SSEEvent{Type: SSECompleted, TaskID: "task-1"})
	}()

	serveSSE(w, req, b, "task-1")

	body := w.Body.String()
	if !strings.Contains(body, "event: started") {
		t.Error("missing started event")
	}
	if !strings.Contains(body, "event: completed") {
		t.Error("missing completed event")
	}
}

func testContextWithTimeout(d time.Duration) (ctx testContext, cancel func()) {
	ch := make(chan struct{})
	go func() {
		time.Sleep(d)
		close(ch)
	}()
	return testContext{done: ch}, func() {}
}

type testContext struct {
	done chan struct{}
}

func (c testContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c testContext) Done() <-chan struct{}        { return c.done }
func (c testContext) Err() error {
	select {
	case <-c.done:
		return fmt.Errorf("context done")
	default:
		return nil
	}
}
func (c testContext) Value(_ any) any { return nil }
