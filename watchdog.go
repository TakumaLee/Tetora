package main

import (
	"context"

	"tetora/internal/config"
	"tetora/internal/lifecycle"
)

// startWatchdog is a thin shim that delegates to the lifecycle package.
func startWatchdog(ctx context.Context, cfg config.WatchdogConfig, listenAddr string) {
	lifecycle.StartWatchdog(ctx, cfg, listenAddr)
}
