package main

import (
	"context"
	"net/http"

	"tetora/internal/trace"
)

func newTraceID(prefix string) string                    { return trace.NewID(prefix) }
func withTraceID(ctx context.Context, traceID string) context.Context {
	return trace.WithID(ctx, traceID)
}
func traceIDFromContext(ctx context.Context) string      { return trace.IDFromContext(ctx) }
func traceMiddleware(next http.Handler) http.Handler     { return trace.Middleware(next) }
