package main

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"
)

// TmuxProvider executes tasks by launching interactive CLI tool sessions in tmux.
// The tool-specific behavior (command construction, state detection, approval keys)
// is delegated to a tmuxCLIProfile, making this provider generic across CLI tools.
type TmuxProvider struct {
	binaryPath string
	cfg        *Config
	provCfg    ProviderConfig
	supervisor *tmuxSupervisor
	profile    tmuxCLIProfile
}

func (p *TmuxProvider) Name() string { return p.profile.Name() + "-tmux" }

func (p *TmuxProvider) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	start := time.Now()

	// Parse config defaults.
	cols := cmp.Or(p.provCfg.TmuxCols, 160)
	rows := cmp.Or(p.provCfg.TmuxRows, 50)
	pollInterval := parseDurationOr(p.provCfg.TmuxPollInterval, 2*time.Second)
	approvalTimeout := parseDurationOr(p.provCfg.TmuxApprovalTimeout, 5*time.Minute)

	// Generate unique tmux session name.
	taskShort := req.SessionID
	if len(taskShort) > 8 {
		taskShort = taskShort[:8]
	}
	if taskShort == "" {
		taskShort = fmt.Sprintf("%d", time.Now().UnixNano()%100000000)
	}
	tmuxName := "tetora-worker-" + taskShort

	// Build the CLI command via profile.
	command := p.profile.BuildCommand(p.binaryPath, req)

	// Step 1: Create tmux session.
	workdir := req.Workdir
	if workdir == "" {
		workdir = p.cfg.DefaultWorkdir
	}
	if err := tmuxCreate(tmuxName, cols, rows, command, workdir); err != nil {
		return nil, fmt.Errorf("tmux create: %w", err)
	}

	// Register worker in supervisor.
	promptPreview := req.Prompt
	if len(promptPreview) > 200 {
		promptPreview = promptPreview[:200]
	}
	worker := &tmuxWorker{
		TmuxName:    tmuxName,
		TaskID:      req.SessionID,
		Agent:       "", // caller can set via supervisor after Execute returns
		Prompt:      promptPreview,
		Workdir:     workdir,
		State:       tmuxStateStarting,
		CreatedAt:   time.Now(),
		LastChanged: time.Now(),
	}
	if p.supervisor != nil {
		p.supervisor.register(tmuxName, worker)
		defer func() {
			if !p.provCfg.TmuxKeepSessions {
				p.supervisor.unregister(tmuxName)
			}
		}()
	}

	// Cleanup on exit (unless keepSessions).
	defer func() {
		if !p.provCfg.TmuxKeepSessions && tmuxHasSession(tmuxName) {
			tmuxKill(tmuxName)
		}
	}()

	// Step 2: Wait for CLI tool to become ready (input prompt).
	if err := p.waitForReady(ctx, tmuxName, 60*time.Second); err != nil {
		return errResult("%s startup failed: %v", p.Name(), err), nil
	}

	// Step 3: Send prompt.
	if err := p.sendPrompt(tmuxName, req.Prompt); err != nil {
		return errResult("send prompt: %v", err), nil
	}

	// Update state.
	worker.State = tmuxStateWorking
	worker.LastChanged = time.Now()

	// Step 4: Poll until done.
	output, err := p.pollUntilDone(ctx, tmuxName, worker, pollInterval, approvalTimeout)
	if err != nil {
		return errResult("poll: %v", err), nil
	}

	elapsed := time.Since(start)
	return &ProviderResult{
		Output:     output,
		DurationMs: elapsed.Milliseconds(),
		Provider:   p.Name(),
	}, nil
}

// waitForReady polls the tmux session until the CLI tool shows its input prompt.
func (p *TmuxProvider) waitForReady(ctx context.Context, tmuxName string, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout waiting for CLI tool to start (%v)", timeout)
		case <-ticker.C:
			if !tmuxHasSession(tmuxName) {
				return fmt.Errorf("tmux session disappeared during startup")
			}
			capture, err := tmuxCapture(tmuxName)
			if err != nil {
				continue
			}
			state := p.profile.DetectState(capture)
			if state == tmuxStateWaiting {
				return nil
			}
			if state == tmuxStateDone {
				return fmt.Errorf("CLI tool exited during startup")
			}
		}
	}
}

// sendPrompt sends the task prompt to the interactive CLI session.
// Uses tmuxLoadAndPaste for long prompts, tmuxSendText for short ones.
func (p *TmuxProvider) sendPrompt(tmuxName, prompt string) error {
	const shortThreshold = 4096

	if len(prompt) <= shortThreshold {
		if err := tmuxSendText(tmuxName, prompt); err != nil {
			return err
		}
	} else {
		if err := tmuxLoadAndPaste(tmuxName, prompt); err != nil {
			return err
		}
	}

	// Press Enter to submit.
	return tmuxSendKeys(tmuxName, "Enter")
}

// pollUntilDone monitors the tmux session, handling approvals and detecting completion.
func (p *TmuxProvider) pollUntilDone(ctx context.Context, tmuxName string, worker *tmuxWorker, pollInterval, approvalTimeout time.Duration) (string, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Stability check: consecutive unchanged captures while in waiting state.
	const stabilityNeeded = 3
	stableCount := 0
	lastCapture := ""
	inApproval := false

	for {
		select {
		case <-ctx.Done():
			logInfo("tmux worker cancelled", "tmux", tmuxName)
			return "", fmt.Errorf("cancelled: %w", ctx.Err())

		case <-ticker.C:
			// Check session still alive.
			if !tmuxHasSession(tmuxName) {
				worker.State = tmuxStateDone
				worker.LastChanged = time.Now()
				// Collect whatever output we have.
				return p.collectOutput(tmuxName, worker.LastCapture), nil
			}

			capture, err := tmuxCapture(tmuxName)
			if err != nil {
				continue
			}

			state := p.profile.DetectState(capture)
			changed := capture != lastCapture
			lastCapture = capture

			// Update worker state.
			worker.LastCapture = capture
			if worker.State != state {
				worker.State = state
				worker.LastChanged = time.Now()
				// Publish state change via SSE.
				if p.supervisor != nil && p.supervisor.broker != nil {
					p.supervisor.broker.Publish(SSEDashboardKey, SSEEvent{
						Type: SSEWorkerUpdate,
						Data: map[string]string{"action": "state_changed", "name": tmuxName, "state": state.String()},
					})
				}
			}

			switch state {
			case tmuxStateApproval:
				if !inApproval {
					inApproval = true
					stableCount = 0
					approved := p.requestApproval(ctx, tmuxName, capture, approvalTimeout)
					if approved {
						tmuxSendKeys(tmuxName, p.profile.ApproveKeys()...)
					} else {
						tmuxSendKeys(tmuxName, p.profile.RejectKeys()...)
					}
					inApproval = false
				}

			case tmuxStateWaiting:
				if changed {
					stableCount = 1
				} else {
					stableCount++
				}
				// Completion: CLI tool returned to prompt after processing.
				// Need stability to avoid false positives during startup.
				if stableCount >= stabilityNeeded {
					worker.State = tmuxStateDone
					worker.LastChanged = time.Now()
					output := p.collectOutputFromHistory(tmuxName)
					return output, nil
				}

			case tmuxStateDone:
				output := p.collectOutputFromHistory(tmuxName)
				return output, nil

			default:
				stableCount = 0
			}
		}
	}
}

// requestApproval sends an approval request to Discord and waits for a response.
// Returns true if approved, false if rejected or timed out.
func (p *TmuxProvider) requestApproval(ctx context.Context, tmuxName, capture string, timeout time.Duration) bool {
	// Extract the approval context from the last few lines of capture.
	lines := strings.Split(capture, "\n")
	contextLines := lines
	if len(contextLines) > 10 {
		contextLines = contextLines[len(contextLines)-10:]
	}
	approvalContext := strings.Join(contextLines, "\n")

	// Find the supervisor's bot for Discord routing.
	if p.supervisor == nil {
		logWarn("tmux approval requested but no supervisor (auto-rejecting)", "tmux", tmuxName)
		return false
	}

	worker := p.supervisor.getWorker(tmuxName)
	if worker == nil {
		return false
	}

	logInfo("tmux worker approval requested", "tmux", tmuxName, "task", worker.TaskID)

	// Use Discord approval gate if available.
	bot := p.getDiscordBot()
	if bot == nil {
		logWarn("tmux approval requested but no Discord bot (auto-rejecting)", "tmux", tmuxName)
		return false
	}

	// Create approval channel.
	approvalCh := make(chan bool, 1)
	customApprove := "tmux_approve:" + tmuxName
	customReject := "tmux_reject:" + tmuxName

	// Build approval message.
	text := fmt.Sprintf("**Worker Approval Needed**\n\nWorker: `%s`\nTask: `%s`\n```\n%s\n```",
		tmuxName, worker.TaskID, renderTerminalScreen(approvalContext, 1500))

	components := []discordComponent{{
		Type: componentTypeActionRow,
		Components: []discordComponent{
			{Type: componentTypeButton, Style: buttonStyleSuccess, Label: "Approve", CustomID: customApprove},
			{Type: componentTypeButton, Style: buttonStyleDanger, Label: "Reject", CustomID: customReject},
		},
	}}

	ch := bot.notifyChannelID()
	if ch == "" {
		logWarn("no notify channel for tmux approval", "tmux", tmuxName)
		return false
	}

	// Register callbacks.
	bot.interactions.register(&pendingInteraction{
		CustomID:  customApprove,
		CreatedAt: time.Now(),
		Callback: func(data discordInteractionData) {
			select {
			case approvalCh <- true:
			default:
			}
		},
	})
	bot.interactions.register(&pendingInteraction{
		CustomID:  customReject,
		CreatedAt: time.Now(),
		Callback: func(data discordInteractionData) {
			select {
			case approvalCh <- false:
			default:
			}
		},
	})
	defer func() {
		bot.interactions.remove(customApprove)
		bot.interactions.remove(customReject)
	}()

	bot.sendMessageWithComponents(ch, text, components)

	// Wait for response.
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case approved := <-approvalCh:
		return approved
	case <-timeoutCtx.Done():
		logWarn("tmux approval timed out", "tmux", tmuxName)
		return false
	}
}

// getDiscordBot retrieves the DiscordBot from the config if available.
func (p *TmuxProvider) getDiscordBot() *DiscordBot {
	if p.cfg == nil {
		return nil
	}
	return p.cfg.discordBot
}

// collectOutput extracts the last meaningful output from a capture string.
func (p *TmuxProvider) collectOutput(tmuxName, lastCapture string) string {
	if lastCapture == "" {
		return "(no output captured)"
	}
	return strings.TrimSpace(lastCapture)
}

// collectOutputFromHistory gets the full scrollback and extracts the CLI tool's response.
func (p *TmuxProvider) collectOutputFromHistory(tmuxName string) string {
	history, err := tmuxCaptureHistory(tmuxName)
	if err != nil {
		logWarn("failed to capture tmux history", "tmux", tmuxName, "error", err)
		return "(failed to capture output)"
	}

	// The full scrollback contains the entire session. Try to extract
	// the portion after the prompt was sent.
	return extractScrollbackOutput(history)
}

// extractScrollbackOutput parses tmux scrollback to extract the CLI tool's response text.
// It returns the last substantial block of text, skipping the initial prompt.
func extractScrollbackOutput(history string) string {
	lines := strings.Split(history, "\n")

	// Trim trailing empty lines.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return "(empty output)"
	}

	// Simple approach: return the last portion of scrollback (up to 500 lines).
	// The caller gets raw terminal output; structured parsing isn't possible
	// since interactive mode doesn't produce JSON.
	maxLines := 500
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// shellQuote wraps a string in single quotes for shell safety.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// parseDurationOr parses a duration string, returning fallback on failure.
func parseDurationOr(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}
