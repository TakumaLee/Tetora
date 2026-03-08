package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TerminalProvider executes tasks in persistent tmux sessions.
// Unlike ClaudeProvider (which spawns a short-lived subprocess), TerminalProvider
// creates a long-running tmux session and polls its screen until the task completes.
// This enables real-time visibility, interactive approval, and session reuse.
type TerminalProvider struct {
	binaryPath string
	profile    tmuxCLIProfile
	supervisor *tmuxSupervisor
	cfg        *Config
}

func (p *TerminalProvider) Name() string { return "terminal-" + p.profile.Name() }

func (p *TerminalProvider) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	// Generate tmux session name.
	sessionName := fmt.Sprintf("tetora-worker-%s-%d", p.profile.Name(), time.Now().UnixNano()%1000000)

	// Build the CLI command.
	command := p.profile.BuildCommand(p.binaryPath, req)

	// Determine dimensions.
	cols, rows := 120, 40

	// Create tmux session.
	workdir := req.Workdir
	if workdir == "" {
		workdir = p.cfg.DefaultWorkdir
	}
	if err := tmuxCreate(sessionName, cols, rows, command, workdir); err != nil {
		return nil, fmt.Errorf("create tmux session: %w", err)
	}

	// Register worker with supervisor.
	promptPreview := req.Prompt
	if len(promptPreview) > 200 {
		promptPreview = promptPreview[:200]
	}
	worker := &tmuxWorker{
		TmuxName:    sessionName,
		TaskID:      req.SessionID,
		Agent:       req.AgentName,
		Prompt:      promptPreview,
		Workdir:     workdir,
		State:       tmuxStateStarting,
		CreatedAt:   time.Now(),
		LastChanged: time.Now(),
	}
	p.supervisor.register(sessionName, worker)
	defer p.supervisor.unregister(sessionName)

	// Wait for the CLI tool to be ready, then send the prompt.
	if err := p.waitForReady(ctx, sessionName, 30*time.Second); err != nil {
		tmuxKill(sessionName)
		return nil, fmt.Errorf("tool not ready: %w", err)
	}

	// Send prompt.
	if req.Prompt != "" {
		if len(req.Prompt) > 1000 {
			tmuxLoadAndPaste(sessionName, req.Prompt)
		} else {
			tmuxSendText(sessionName, req.Prompt)
		}
		tmuxSendKeys(sessionName, "Enter")
	}

	// Poll until completion or timeout.
	start := time.Now()
	result, err := p.pollUntilDone(ctx, sessionName, worker, req)
	elapsed := time.Since(start)

	if err != nil {
		tmuxKill(sessionName)
		return &ProviderResult{
			IsError:    true,
			Error:      err.Error(),
			DurationMs: elapsed.Milliseconds(),
		}, nil
	}

	// Capture final output.
	history, _ := tmuxCaptureHistory(sessionName)

	// Clean up tmux session.
	tmuxKill(sessionName)

	result.DurationMs = elapsed.Milliseconds()
	if history != "" {
		result.Output = extractResultFromHistory(history)
	}

	return result, nil
}

// waitForReady polls until the CLI tool shows its input prompt.
func (p *TerminalProvider) waitForReady(ctx context.Context, sessionName string, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("CLI tool did not become ready within %v", timeout)
		case <-ticker.C:
			capture, err := tmuxCapture(sessionName)
			if err != nil {
				continue
			}
			state := p.profile.DetectState(capture)
			if state == tmuxStateWaiting || state == tmuxStateApproval {
				return nil
			}
		}
	}
}

// pollUntilDone polls the tmux session until the task is done or context is cancelled.
func (p *TerminalProvider) pollUntilDone(ctx context.Context, sessionName string, worker *tmuxWorker, req ProviderRequest) (*ProviderResult, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastCapture := ""
	stableCount := 0

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if !tmuxHasSession(sessionName) {
				// Session exited — treat as done.
				return &ProviderResult{
					Output: lastCapture,
				}, nil
			}

			capture, err := tmuxCapture(sessionName)
			if err != nil {
				continue
			}

			state := p.profile.DetectState(capture)

			// Update worker state.
			p.supervisor.mu.Lock()
			if w := p.supervisor.workers[sessionName]; w != nil {
				w.State = state
				w.LastCapture = capture
				if capture != lastCapture {
					w.LastChanged = time.Now()
				}
			}
			p.supervisor.mu.Unlock()

			// Emit SSE events if channel available.
			if req.EventCh != nil && capture != lastCapture {
				req.EventCh <- SSEEvent{
					Type:      SSEOutputChunk,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data: map[string]any{
						"chunk":     capture,
						"chunkType": "terminal_capture",
					},
					Timestamp: time.Now().Format(time.RFC3339),
				}
			}

			switch state {
			case tmuxStateDone:
				return &ProviderResult{
					Output: capture,
				}, nil

			case tmuxStateApproval:
				// Auto-approve based on permission mode.
				if req.PermissionMode == "bypassPermissions" || req.PermissionMode == "acceptEdits" {
					tmuxSendKeys(sessionName, p.profile.ApproveKeys()...)
				}
				// Otherwise wait for manual approval via Discord bridge.

			case tmuxStateWaiting:
				// CLI tool finished and is waiting for next input.
				// If this is a one-shot dispatch (not interactive), treat as done.
				if !req.PersistSession {
					stableCount++
					if stableCount >= 3 { // 3 consecutive polls showing waiting = truly done
						return &ProviderResult{
							Output: capture,
						}, nil
					}
				}

			default:
				stableCount = 0
			}

			if capture != lastCapture {
				lastCapture = capture
			}
		}
	}
}

// extractResultFromHistory extracts the meaningful output from tmux scrollback history.
func extractResultFromHistory(history string) string {
	lines := strings.Split(history, "\n")

	// Skip initial startup lines and extract the conversation.
	var resultLines []string
	inOutput := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip empty leading lines.
		if !inOutput && trimmed == "" {
			continue
		}
		// Start capturing after the first ❯ prompt (user's submitted prompt).
		if !inOutput && strings.HasPrefix(trimmed, "❯") {
			inOutput = true
			continue
		}
		if inOutput {
			// Stop at the next ❯ prompt (end of response).
			if strings.HasPrefix(trimmed, "❯") {
				break
			}
			resultLines = append(resultLines, line)
		}
	}

	result := strings.Join(resultLines, "\n")
	return strings.TrimSpace(result)
}
