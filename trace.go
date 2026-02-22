package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
)

// traceKey is the context key type for trace IDs.
type traceKey struct{}

// newTraceID generates a short, unique trace ID with the given prefix.
// Format: "<prefix>-<6 hex chars>" e.g. "http-a1b2c3", "tg-d4e5f6"
func newTraceID(prefix string) string {
	b := make([]byte, 3) // 3 bytes = 6 hex chars
	if _, err := rand.Read(b); err != nil {
		// Fallback: should never happen in practice.
		return prefix + "-000000"
	}
	return fmt.Sprintf("%s-%x", prefix, b)
}

// withTraceID returns a new context carrying the given trace ID.
func withTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceID)
}

// traceIDFromContext extracts the trace ID from context.
// Returns "" if not set.
func traceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(traceKey{}).(string); ok {
		return v
	}
	return ""
}

// traceMiddleware is HTTP middleware that generates a trace ID for each request
// and injects it into the request context. Also sets X-Trace-Id response header.
func traceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := newTraceID("http")
		ctx := withTraceID(r.Context(), traceID)
		w.Header().Set("X-Trace-Id", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
