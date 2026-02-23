package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestNewTraceID_Format(t *testing.T) {
	id := newTraceID("http")
	matched, _ := regexp.MatchString(`^http-[0-9a-f]{6}$`, id)
	if !matched {
		t.Errorf("newTraceID('http') = %q, want format http-XXXXXX", id)
	}

	id2 := newTraceID("tg")
	matched2, _ := regexp.MatchString(`^tg-[0-9a-f]{6}$`, id2)
	if !matched2 {
		t.Errorf("newTraceID('tg') = %q, want format tg-XXXXXX", id2)
	}
}

func TestNewTraceID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := newTraceID("test")
		if seen[id] {
			t.Fatalf("duplicate trace ID at iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}

func TestNewTraceID_Prefix(t *testing.T) {
	prefixes := []string{"http", "tg", "slack", "cron", "wf", "cli"}
	for _, p := range prefixes {
		id := newTraceID(p)
		if !strings.HasPrefix(id, p+"-") {
			t.Errorf("newTraceID(%q) = %q, should start with %q", p, id, p+"-")
		}
	}
}

func TestWithTraceID_RoundTrip(t *testing.T) {
	ctx := withTraceID(context.Background(), "test-abc123")
	got := traceIDFromContext(ctx)
	if got != "test-abc123" {
		t.Errorf("traceIDFromContext = %q, want test-abc123", got)
	}
}

func TestTraceIDFromContext_Empty(t *testing.T) {
	got := traceIDFromContext(context.Background())
	if got != "" {
		t.Errorf("traceIDFromContext(Background) = %q, want empty", got)
	}
}

func TestTraceIDFromContext_Nil(t *testing.T) {
	got := traceIDFromContext(nil)
	if got != "" {
		t.Errorf("traceIDFromContext(nil) = %q, want empty", got)
	}
}

func TestTraceMiddleware_SetsHeader(t *testing.T) {
	handler := traceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	traceID := rec.Header().Get("X-Trace-Id")
	if traceID == "" {
		t.Error("X-Trace-Id header not set")
	}
	if !strings.HasPrefix(traceID, "http-") {
		t.Errorf("trace ID %q should start with http-", traceID)
	}
}

func TestTraceMiddleware_InjectsContext(t *testing.T) {
	var captured string
	handler := traceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = traceIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured == "" {
		t.Error("trace ID not injected into request context")
	}
	// Should match the header.
	headerID := rec.Header().Get("X-Trace-Id")
	if captured != headerID {
		t.Errorf("context trace ID %q != header trace ID %q", captured, headerID)
	}
}
