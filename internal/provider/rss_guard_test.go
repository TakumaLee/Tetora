//go:build unix

package provider

import (
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// TestRSSGuard_Disabled verifies that maxMB<=0 or pid==0 returns a no-op stop
// function and does not start a goroutine.
func TestRSSGuard_Disabled(t *testing.T) {
	cases := []struct {
		name  string
		pid   int
		maxMB int
	}{
		{"maxMB_zero", 1, 0},
		{"maxMB_negative", 1, -10},
		{"pid_zero", 0, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cancelCalled atomic.Bool
			cancel := func() { cancelCalled.Store(true) }
			stop := StartRSSGuard(tc.pid, tc.maxMB, 10*time.Millisecond, cancel, nil)
			// stop must be safe to call and idempotent
			stop()
			stop()
			time.Sleep(50 * time.Millisecond)
			if cancelCalled.Load() {
				t.Fatalf("disabled guard called cancel(): pid=%d maxMB=%d", tc.pid, tc.maxMB)
			}
		})
	}
}

// TestRSSGuard_BelowLimit starts a low-RSS subprocess and verifies the guard
// does NOT trigger when RSS stays under the limit.
func TestRSSGuard_BelowLimit(t *testing.T) {
	cmd := exec.Command("sleep", "1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	var cancelCalled atomic.Bool
	cancel := func() { cancelCalled.Store(true) }

	stop := StartRSSGuard(cmd.Process.Pid, 1000 /*MB*/, 50*time.Millisecond, cancel, nil)
	defer stop()

	// Poll for 500ms — sleep's RSS is <<1GB, so guard must not fire.
	time.Sleep(500 * time.Millisecond)

	if cancelCalled.Load() {
		t.Fatalf("guard fired for under-limit process")
	}
}

// TestRSSGuard_AboveLimit starts a subprocess and uses an impossibly low limit
// (1MB) so the guard must detect breach and call cancel.
func TestRSSGuard_AboveLimit(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	var cancelCalled atomic.Bool
	var breachMB atomic.Int32
	cancel := func() { cancelCalled.Store(true) }
	onBreach := func(rssMB int) { breachMB.Store(int32(rssMB)) }

	stop := StartRSSGuard(cmd.Process.Pid, 1 /*MB, always exceeded*/, 50*time.Millisecond, cancel, onBreach)
	defer stop()

	// Wait up to 2s for guard to fire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cancelCalled.Load() {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if !cancelCalled.Load() {
		t.Fatalf("guard did not fire within 2s for over-limit process (breachMB=%d)", breachMB.Load())
	}
	if breachMB.Load() <= 0 {
		t.Fatalf("onBreach not called with positive rssMB: got %d", breachMB.Load())
	}
}

// TestRSSGuard_ProcessDisappears verifies the guard exits silently (no panic,
// no false cancel) when the subprocess dies between polls.
func TestRSSGuard_ProcessDisappears(t *testing.T) {
	cmd := exec.Command("sleep", "0.05") // ~50ms lifetime
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid

	var cancelCalled atomic.Bool
	cancel := func() { cancelCalled.Store(true) }

	stop := StartRSSGuard(pid, 4096 /*generous*/, 20*time.Millisecond, cancel, nil)
	defer stop()

	_ = cmd.Wait() // let the process die

	// Give the guard time to poll a dead pid and exit silently.
	time.Sleep(200 * time.Millisecond)

	if cancelCalled.Load() {
		t.Fatalf("guard spuriously cancelled after process exit")
	}
}
