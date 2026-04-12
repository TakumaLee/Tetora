package trace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestNewID_Format(t *testing.T) {
	id := NewID("http")
	matched, _ := regexp.MatchString(`^http-[0-9a-f]{8}$`, id)
	if !matched {
		t.Errorf("NewID('http') = %q, want format http-XXXXXXXX", id)
	}

	id2 := NewID("tg")
	matched2, _ := regexp.MatchString(`^tg-[0-9a-f]{8}$`, id2)
	if !matched2 {
		t.Errorf("NewID('tg') = %q, want format tg-XXXXXXXX", id2)
	}
}

func TestNewID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := NewID("test")
		if seen[id] {
			t.Fatalf("duplicate trace ID at iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}

func TestNewID_Prefix(t *testing.T) {
	prefixes := []string{"http", "tg", "slack", "cron", "wf", "cli"}
	for _, p := range prefixes {
		id := NewID(p)
		if !strings.HasPrefix(id, p+"-") {
			t.Errorf("NewID(%q) = %q, should start with %q", p, id, p+"-")
		}
	}
}

func TestWithID_RoundTrip(t *testing.T) {
	ctx := WithID(context.Background(), "test-abc123")
	got := IDFromContext(ctx)
	if got != "test-abc123" {
		t.Errorf("IDFromContext = %q, want test-abc123", got)
	}
}

func TestIDFromContext_Empty(t *testing.T) {
	got := IDFromContext(context.Background())
	if got != "" {
		t.Errorf("IDFromContext(Background) = %q, want empty", got)
	}
}

func TestIDFromContext_Nil(t *testing.T) {
	got := IDFromContext(nil)
	if got != "" {
		t.Errorf("IDFromContext(nil) = %q, want empty", got)
	}
}

func TestMiddleware_SetsHeader(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestMiddleware_InjectsContext(t *testing.T) {
	var captured string
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = IDFromContext(r.Context())
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
