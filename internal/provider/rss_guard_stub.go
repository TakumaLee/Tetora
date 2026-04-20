//go:build !unix

package provider

import (
	"context"
	"time"
)

// StartRSSGuard is a no-op stub for non-Unix platforms.
func StartRSSGuard(pid int, maxMB int, interval time.Duration, cancel context.CancelFunc, onBreach func(rssMB int)) (stop func()) {
	return func() {}
}
