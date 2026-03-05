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
}

func newTmuxSupervisor() *tmuxSupervisor {
	return &tmuxSupervisor{
		workers: make(map[string]*tmuxWorker),
	}
}

func (s *tmuxSupervisor) register(name string, w *tmuxWorker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[name] = w
}

func (s *tmuxSupervisor) unregister(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workers, name)
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
