package main

// wire_tmux.go bridges root callers to internal/tmux.
// Type aliases and function forwarders keep the root API stable.

import "tetora/internal/tmux"

// --- Type aliases ---

type tmuxScreenState = tmux.ScreenState
type tmuxWorker = tmux.Worker
type tmuxSupervisor = tmux.Supervisor

// --- State constants ---

const (
	tmuxStateUnknown  = tmux.StateUnknown
	tmuxStateStarting = tmux.StateStarting
	tmuxStateWorking  = tmux.StateWorking
	tmuxStateWaiting  = tmux.StateWaiting
	tmuxStateApproval = tmux.StateApproval
	tmuxStateQuestion = tmux.StateQuestion
	tmuxStateDone     = tmux.StateDone
)

// --- Tmux primitives ---

func tmuxCreate(name string, cols, rows int, command, workdir string) error {
	return tmux.Create(name, cols, rows, command, workdir)
}

func tmuxCapture(name string) (string, error)        { return tmux.Capture(name) }
func tmuxCaptureHistory(name string) (string, error)  { return tmux.CaptureHistory(name) }
func tmuxSendKeys(name string, keys ...string) error  { return tmux.SendKeys(name, keys...) }
func tmuxSendText(name, text string) error            { return tmux.SendText(name, text) }
func tmuxLoadAndPaste(name, text string) error        { return tmux.LoadAndPaste(name, text) }
func tmuxKill(name string) error                      { return tmux.Kill(name) }
func tmuxHasSession(name string) bool                 { return tmux.HasSession(name) }
func tmuxListSessions() []string                      { return tmux.ListSessions() }

// --- Supervisor ---

func newTmuxSupervisor() *tmux.Supervisor { return tmux.NewSupervisor() }
