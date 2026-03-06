package main

import (
	"strings"
	"sync"
	"time"
)

// --- Tmux Worker Supervisor ---
// Tracks all tmux-based CLI tool worker sessions for monitoring and approval routing.

// tmuxScreenState represents the detected state of a tmux worker's screen.
type tmuxScreenState int

const (
	tmuxStateUnknown  tmuxScreenState = iota
	tmuxStateStarting                 // session just created, waiting for CLI tool to load
	tmuxStateWorking                  // CLI tool is actively processing (screen changing)
	tmuxStateWaiting                  // CLI tool is idle at input prompt
	tmuxStateApproval                 // CLI tool is asking for permission
	tmuxStateDone                     // session exited or returned to shell prompt
)

func (s tmuxScreenState) String() string {
	switch s {
	case tmuxStateStarting:
		return "starting"
	case tmuxStateWorking:
		return "working"
	case tmuxStateWaiting:
		return "waiting"
	case tmuxStateApproval:
		return "approval"
	case tmuxStateDone:
		return "done"
	default:
		return "unknown"
	}
}

// tmuxWorker represents a single tmux-based CLI tool worker session.
type tmuxWorker struct {
	TmuxName    string
	TaskID      string
	Agent       string
	Prompt      string // first 200 chars for display
	Workdir     string
	State       tmuxScreenState
	CreatedAt   time.Time
	LastCapture string
	LastChanged time.Time
}

// tmuxSupervisor tracks all active tmux workers.
type tmuxSupervisor struct {
	mu      sync.RWMutex
	workers map[string]*tmuxWorker // tmuxName → worker
	broker  *sseBroker             // optional, for SSE worker_update events
}

func newTmuxSupervisor() *tmuxSupervisor {
	return &tmuxSupervisor{
		workers: make(map[string]*tmuxWorker),
	}
}

func (s *tmuxSupervisor) register(name string, w *tmuxWorker) {
	s.mu.Lock()
	s.workers[name] = w
	broker := s.broker
	s.mu.Unlock()
	if broker != nil {
		broker.Publish(SSEDashboardKey, SSEEvent{
			Type: SSEWorkerUpdate,
			Data: map[string]string{"action": "registered", "name": name, "state": w.State.String()},
		})
	}
}

func (s *tmuxSupervisor) unregister(name string) {
	s.mu.Lock()
	delete(s.workers, name)
	broker := s.broker
	s.mu.Unlock()
	if broker != nil {
		broker.Publish(SSEDashboardKey, SSEEvent{
			Type: SSEWorkerUpdate,
			Data: map[string]string{"action": "unregistered", "name": name},
		})
	}
}

func (s *tmuxSupervisor) listWorkers() []*tmuxWorker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*tmuxWorker, 0, len(s.workers))
	for _, w := range s.workers {
		result = append(result, w)
	}
	return result
}

func (s *tmuxSupervisor) getWorker(name string) *tmuxWorker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workers[name]
}

// recoverWorkers scans for existing tetora-worker-* tmux sessions and
// re-registers them in the supervisor. This handles daemon restarts where
// tmux sessions survive but the in-memory supervisor state is lost.
// If a session is detected as "waiting" with text in the prompt (i.e. the
// daemon died between paste and Enter), it sends Enter to resume.
func (s *tmuxSupervisor) recoverWorkers(profile tmuxCLIProfile) {
	sessions := tmuxListSessions()
	recovered := 0
	for _, name := range sessions {
		if !strings.HasPrefix(name, "tetora-worker-") {
			continue
		}
		if s.getWorker(name) != nil {
			continue // already tracked
		}
		// Detect current state from capture.
		state := tmuxStateUnknown
		var capture string
		if c, err := tmuxCapture(name); err == nil && profile != nil {
			capture = c
			state = profile.DetectState(c)
		}

		// If "waiting" with text on prompt line, the daemon likely died
		// between paste and Enter. Send Enter to resume the stuck prompt.
		if state == tmuxStateWaiting && hasPromptText(capture) {
			logInfo("recovered worker has stuck prompt, sending Enter", "tmux", name)
			tmuxSendKeys(name, "Enter")
			state = tmuxStateWorking
		}

		w := &tmuxWorker{
			TmuxName:    name,
			State:       state,
			CreatedAt:   time.Now(), // approximate — real start time unknown
			LastChanged: time.Now(),
		}
		s.register(name, w)
		recovered++
	}
	if recovered > 0 {
		logInfo("recovered orphaned tmux workers", "count", recovered)
	}
}

// hasPromptText checks if the capture has text after the ❯ prompt character,
// indicating a prompt was typed/pasted but not submitted.
func hasPromptText(capture string) bool {
	for _, line := range strings.Split(capture, "\n") {
		trimmed := strings.TrimSpace(line)
		// Has ❯ followed by non-whitespace content (regular space, NBSP, or tab).
		if strings.HasPrefix(trimmed, "❯") && len(trimmed) > len("❯") {
			after := trimmed[len("❯"):]
			after = strings.TrimSpace(after)
			after = strings.ReplaceAll(after, "\u00a0", "")
			if after != "" {
				return true
			}
		}
	}
	return false
}

// isShellPrompt checks if a line looks like a shell prompt ($ or % at the end).
// Used by tmuxCLIProfile implementations for done-state detection.
func isShellPrompt(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Common shell prompt endings.
	return strings.HasSuffix(trimmed, "$") || strings.HasSuffix(trimmed, "%") || strings.HasSuffix(trimmed, "#")
}
