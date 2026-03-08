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
	tmuxStateWorking                  // CLI tool is actively processing
	tmuxStateWaiting                  // CLI tool is idle at input prompt
	tmuxStateApproval                 // CLI tool is asking for permission
	tmuxStateQuestion                 // CLI tool is showing AskUserQuestion choices
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
	case tmuxStateQuestion:
		return "question"
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

// checkSessionHealth inspects all tracked workers for health issues.
// Returns a list of issues found (empty = healthy).
func (s *tmuxSupervisor) checkSessionHealth() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var issues []string
	for name, w := range s.workers {
		// Check if tmux session still exists.
		if !tmuxHasSession(name) {
			issues = append(issues, "zombie worker: "+name+" (tmux session gone)")
			continue
		}
		// Check for stalled workers (no capture change in 10 minutes).
		if w.State == tmuxStateWorking && time.Since(w.LastChanged) > 10*time.Minute {
			issues = append(issues, "stalled worker: "+name+" (no change in 10m)")
		}
	}
	return issues
}

// cleanupOrphanedSessions handles tetora-worker-* tmux sessions left from a previous daemon run.
func (s *tmuxSupervisor) cleanupOrphanedSessions(keepOne bool, profile tmuxCLIProfile) {
	sessions := tmuxListSessions()
	cleaned := 0
	kept := false
	for _, name := range sessions {
		if !strings.HasPrefix(name, "tetora-worker-") && !strings.HasPrefix(name, "tetora-term-") {
			continue
		}
		if s.getWorker(name) != nil {
			continue // actively managed
		}

		if keepOne && !kept && profile != nil {
			if capture, err := tmuxCapture(name); err == nil {
				if profile.DetectState(capture) == tmuxStateWaiting {
					w := &tmuxWorker{
						TmuxName:    name,
						State:       tmuxStateWaiting,
						CreatedAt:   time.Now(),
						LastChanged: time.Now(),
					}
					s.register(name, w)
					kept = true
					logInfo("keeping idle tmux session for reuse", "tmux", name)
					continue
				}
			}
		}

		tmuxKill(name)
		cleaned++
	}
	if cleaned > 0 {
		logInfo("cleaned up orphaned tmux sessions", "count", cleaned)
	}
}

// isShellPrompt checks if a line looks like a shell prompt.
func isShellPrompt(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return strings.HasSuffix(trimmed, "$") || strings.HasSuffix(trimmed, "%") || strings.HasSuffix(trimmed, "#")
}
