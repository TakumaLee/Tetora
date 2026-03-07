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
	supervisor *tmuxSupervisor // legacy: kept for API-registered providers
	profile    tmuxCLIProfile
}

func (p *TmuxProvider) Name() string { return p.profile.Name() + "-tmux" }

// getSupervisor returns the tmux supervisor, preferring the live cfg reference
// over the stored field (which may be nil if the provider was created during
// initProviders before the supervisor was initialized).
func (p *TmuxProvider) getSupervisor() *tmuxSupervisor {
	if p.supervisor != nil {
		return p.supervisor
	}
	if p.cfg != nil {
		return p.cfg.tmuxSupervisor
	}
	return nil
}

func (p *TmuxProvider) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	start := time.Now()

	// Parse config defaults.
	cols := cmp.Or(p.provCfg.TmuxCols, 160)
	rows := cmp.Or(p.provCfg.TmuxRows, 50)
	pollInterval := parseDurationOr(p.provCfg.TmuxPollInterval, 2*time.Second)
	// Relax poll interval when hooks are active (hooks provide instant state updates).
	if p.cfg.hookRecv != nil && p.cfg.Hooks.Enabled && pollInterval < 10*time.Second {
		pollInterval = 15 * time.Second
	}
	approvalTimeout := parseDurationOr(p.provCfg.TmuxApprovalTimeout, 5*time.Minute)

	workdir := req.Workdir
	if workdir == "" {
		workdir = p.cfg.DefaultWorkdir
	}

	// Try to reuse an existing idle session (keepSessions mode).
	tmuxName, worker, reused := p.findOrCreateSession(ctx, req, cols, rows, workdir)
	if tmuxName == "" {
		return nil, fmt.Errorf("tmux session setup failed")
	}

	if sup := p.getSupervisor(); sup != nil {
		sup.register(tmuxName, worker)
		defer func() {
			if !p.provCfg.TmuxKeepSessions {
				p.getSupervisor().unregister(tmuxName)
			}
		}()
	}

	// Cleanup on exit (unless keepSessions).
	defer func() {
		if !p.provCfg.TmuxKeepSessions && tmuxHasSession(tmuxName) {
			tmuxKill(tmuxName)
		}
	}()

	// Wait for CLI tool to become ready (skip if reusing idle session).
	if !reused {
		if err := p.waitForReady(ctx, tmuxName, 60*time.Second); err != nil {
			return errResult("%s startup failed: %v", p.Name(), err), nil
		}
	}

	// Send prompt.
	if err := p.sendPrompt(tmuxName, req.Prompt); err != nil {
		return errResult("send prompt: %v", err), nil
	}

	// Update state.
	worker.State = tmuxStateWorking
	worker.LastChanged = time.Now()

	// Poll until done.
	logDebug("tmux execute poll start", "tmux", tmuxName, "hasEventCh", req.EventCh != nil, "sessionID", req.SessionID)
	output, err := p.pollUntilDone(ctx, tmuxName, worker, pollInterval, approvalTimeout, req.EventCh, req.SessionID)
	if err != nil {
		// If we captured partial output before the error, include it in the result.
		if output != "" {
			return &ProviderResult{
				Output:     output,
				IsError:    true,
				Error:      err.Error(),
				DurationMs: time.Since(start).Milliseconds(),
				Provider:   p.Name(),
			}, nil
		}
		return errResult("poll: %v", err), nil
	}

	// If keepSessions, mark worker as idle for reuse (not "done").
	if p.provCfg.TmuxKeepSessions {
		worker.State = tmuxStateWaiting
		worker.LastChanged = time.Now()
	}

	elapsed := time.Since(start)
	return &ProviderResult{
		Output:     output,
		DurationMs: elapsed.Milliseconds(),
		Provider:   p.Name(),
	}, nil
}

// findOrCreateSession tries to reuse an existing idle tmux session (when keepSessions
// is enabled) or creates a new one. Returns the tmux session name, worker info, and
// whether an existing session was reused.
func (p *TmuxProvider) findOrCreateSession(ctx context.Context, req ProviderRequest, cols, rows int, workdir string) (string, *tmuxWorker, bool) {
	promptPreview := req.Prompt
	if len(promptPreview) > 200 {
		promptPreview = promptPreview[:200]
	}

	// Try reuse: find an existing idle session managed by the supervisor.
	// When the request has a SessionID (e.g. Discord channel session), only reuse
	// the worker that previously served that same session — different channels must
	// not share workers because the Claude Code conversation context would mix.
	sup := p.getSupervisor()
	if p.provCfg.TmuxKeepSessions && sup != nil {
		for _, w := range sup.listWorkers() {
			// Auto-clean dead sessions: if tmux session is gone, unregister the stale worker.
			if !tmuxHasSession(w.TmuxName) {
				logInfo("tmux worker session dead, cleaning up", "tmux", w.TmuxName)
				sup.unregister(w.TmuxName)
				continue
			}
			if w.State != tmuxStateWaiting {
				continue
			}
			// Session affinity: only reuse worker with matching session ID.
			// Recovered workers (TaskID="") can be claimed by any session.
			if req.SessionID != "" && w.TaskID != "" && w.TaskID != req.SessionID {
				continue
			}
			// Verify it's actually at the prompt.
			if capture, err := tmuxCapture(w.TmuxName); err == nil {
				if p.profile.DetectState(capture) == tmuxStateWaiting {
					w.TaskID = req.SessionID
					w.Agent = req.AgentName
					w.Prompt = promptPreview
					w.LastChanged = time.Now()
					return w.TmuxName, w, true
				}
			}
		}
	}

	// Create new session.
	taskShort := req.SessionID
	if len(taskShort) > 8 {
		taskShort = taskShort[:8]
	}
	if taskShort == "" {
		taskShort = fmt.Sprintf("%d", time.Now().UnixNano()%100000000)
	}
	tmuxName := "tetora-worker-" + taskShort

	// If a tmux session with this name already exists (e.g. previous task still running
	// or worker in non-waiting state), append a suffix to avoid duplicate names.
	if tmuxHasSession(tmuxName) {
		tmuxName = fmt.Sprintf("%s-%d", tmuxName, time.Now().UnixNano()%10000)
	}

	command := p.profile.BuildCommand(p.binaryPath, req)
	if err := tmuxCreate(tmuxName, cols, rows, command, workdir); err != nil {
		logWarn("tmux create failed", "error", err, "name", tmuxName)
		return "", nil, false
	}

	worker := &tmuxWorker{
		TmuxName:    tmuxName,
		TaskID:      req.SessionID,
		Agent:       req.AgentName,
		Prompt:      promptPreview,
		Workdir:     workdir,
		State:       tmuxStateStarting,
		CreatedAt:   time.Now(),
		LastChanged: time.Now(),
	}
	return tmuxName, worker, false
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

	// Delay to let tmux/CLI TUI fully process the pasted text before Enter.
	time.Sleep(500 * time.Millisecond)

	// Press Enter to submit.
	if err := tmuxSendKeys(tmuxName, "Enter"); err != nil {
		return err
	}

	// Verify Enter was received: check if the prompt still shows the text
	// (meaning Enter was lost/swallowed by the TUI). Retry once if stuck.
	time.Sleep(500 * time.Millisecond)
	if capture, err := tmuxCapture(tmuxName); err == nil {
		if isPromptStuck(capture) {
			logInfo("prompt Enter may not have registered, retrying", "tmux", tmuxName)
			time.Sleep(300 * time.Millisecond)
			return tmuxSendKeys(tmuxName, "Enter")
		}
	}
	return nil
}

// pollUntilDone monitors the tmux session, handling approvals and detecting completion.
func (p *TmuxProvider) pollUntilDone(ctx context.Context, tmuxName string, worker *tmuxWorker, pollInterval, approvalTimeout time.Duration, eventCh chan<- SSEEvent, sessionID string) (string, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Stability check: consecutive unchanged captures while in waiting state.
	// sawNonWaiting prevents premature completion if Enter didn't register —
	// we only count stability after the worker has actually started processing.
	// waitingTicks counts how long we've been in waiting without seeing work;
	// after a threshold, we resend Enter (in case it was lost) and start counting.
	const stabilityNeeded = 3
	const waitingTicksBeforeRetry = 5 // ~10s at 2s poll
	stableCount := 0
	lastStripped := "" // capture with status bars removed, for stability comparison
	inApproval := false
	sawNonWaiting := false
	waitingTicks := 0
	enterRetried := false // safety net: retry Enter once if prompt appears stuck in "working"

	for {
		select {
		case <-ctx.Done():
			logInfo("tmux worker cancelled", "tmux", tmuxName)
			// Collect whatever output we have before returning the error.
			if worker.LastCapture != "" {
				return extractScrollbackOutput(worker.LastCapture), fmt.Errorf("cancelled: %w", ctx.Err())
			}
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
			// Use stripped capture (status bars removed) for change detection,
			// so that status bar updates (timestamps, CPU, etc.) don't count as changes.
			stripped := stripStatusBars(capture)
			changed := stripped != lastStripped
			if changed {
				logDebug("tmux poll state", "tmux", tmuxName, "state", state.String(), "changed", changed)
			}
			lastStripped = stripped

			// Update worker state.
			worker.LastCapture = capture
			if worker.State != state {
				worker.State = state
				worker.LastChanged = time.Now()
				// Publish state change via SSE.
				if sup := p.getSupervisor(); sup != nil && sup.broker != nil {
					sup.broker.Publish(SSEDashboardKey, SSEEvent{
						Type: SSEWorkerUpdate,
						Data: map[string]string{"action": "state_changed", "name": tmuxName, "state": state.String()},
					})
				}
			}

			switch state {
			case tmuxStateApproval:
				sawNonWaiting = true
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

			case tmuxStateQuestion:
				sawNonWaiting = true
				if !inApproval {
					inApproval = true
					stableCount = 0
					parsed := parseQuestionFromCapture(capture)
					if parsed != nil && parsed.IsMultiSelect {
						result := p.requestMultiSelectChoice(ctx, tmuxName, parsed, approvalTimeout)
						if result != nil {
							p.executeMultiSelect(tmuxName, result, parsed)
						}
					} else if parsed != nil {
						selectedOption := p.requestQuestionChoice(ctx, tmuxName, parsed, approvalTimeout)
						if selectedOption >= 0 {
							// Navigate to selected option: go to top first, then down to target.
							for i := 0; i < 20; i++ {
								tmuxSendKeys(tmuxName, "Up")
								time.Sleep(50 * time.Millisecond)
							}
							for i := 0; i < selectedOption; i++ {
								tmuxSendKeys(tmuxName, "Down")
								time.Sleep(50 * time.Millisecond)
							}
							tmuxSendKeys(tmuxName, "Enter")
						}
					}
					inApproval = false
				}

			case tmuxStateWaiting:
				if !sawNonWaiting {
					waitingTicks++
					if waitingTicks >= waitingTicksBeforeRetry {
						// Waited long enough — either Enter was lost or task completed
						// before our first poll. Resend Enter as safety net, then
						// start counting stability.
						logInfo("tmux worker still waiting, resending Enter", "tmux", tmuxName, "ticks", waitingTicks)
						tmuxSendKeys(tmuxName, "Enter")
						sawNonWaiting = true
					}
					stableCount = 0
					break
				}
				if changed {
					stableCount = 1
					// Stream during generation phase: Claude is outputting (⏺ blocks)
					// but ✽ is gone, so state shows waiting. Content still changing =
					// generation in progress, so stream the output to Discord.
					if eventCh != nil {
						cleaned := cleanCaptureForStreaming(capture)
						if cleaned != "" {
							eventCh <- SSEEvent{
								Type:      SSEOutputChunk,
								TaskID:    sessionID,
								SessionID: sessionID,
								Data:      map[string]any{"chunk": cleaned, "replace": true},
							}
						}
					}
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
				sawNonWaiting = true
				stableCount = 0
				// Safety: if "working" but the prompt is stuck (Enter lost), retry once.
				if !enterRetried && isPromptStuck(capture) {
					logInfo("prompt stuck in working state, retrying Enter", "tmux", tmuxName)
					time.Sleep(300 * time.Millisecond)
					tmuxSendKeys(tmuxName, "Enter")
					enterRetried = true
				}
				// Stream current visible output to Discord progress updater.
				if changed && eventCh != nil {
					cleaned := cleanCaptureForStreaming(capture)
					logDebug("tmux streaming", "hasContent", cleaned != "", "contentLen", len(cleaned), "tmux", tmuxName)
					if cleaned != "" {
						eventCh <- SSEEvent{
							Type:      SSEOutputChunk,
							TaskID:    sessionID,
							SessionID: sessionID,
							Data:      map[string]any{"chunk": cleaned, "replace": true},
						}
					}
				}
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
	sup := p.getSupervisor()
	if sup == nil {
		logWarn("tmux approval requested but no supervisor (auto-rejecting)", "tmux", tmuxName)
		return false
	}

	worker := sup.getWorker(tmuxName)
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

	ch := bot.notifyChannelID()
	if ch == "" {
		logWarn("no notify channel for tmux approval", "tmux", tmuxName)
		return false
	}

	// Check if this is a plan mode review (hook-detected ExitPlanMode).
	isPlanReview := false
	var cachedPlanData *cachedPlan
	if hookRecv := p.cfg.hookRecv; hookRecv != nil {
		// Try to find plan by session ID (from worker).
		if worker.TaskID != "" {
			cachedPlanData = hookRecv.GetCachedPlan(worker.TaskID)
		}
	}
	// Also detect plan mode from capture content.
	if cachedPlanData == nil {
		isPlanReview = isPlanModeCapture(approvalContext)
	} else {
		isPlanReview = cachedPlanData.ReadyForReview
	}

	if isPlanReview && cachedPlanData != nil && cachedPlanData.Content != "" {
		// Rich plan review with embed.
		reviewID := fmt.Sprintf("pr-%s-%d", truncate(worker.TaskID, 8), time.Now().Unix())
		review := &PlanReview{
			ID:         reviewID,
			SessionID:  worker.TaskID,
			WorkerName: tmuxName,
			Agent:      worker.Agent,
			PlanText:   cachedPlanData.Content,
			CreatedAt:  time.Now().Format(time.RFC3339),
		}

		// Store in DB for dashboard.
		if p.cfg.HistoryDB != "" {
			if err := insertPlanReview(p.cfg.HistoryDB, review); err != nil {
				logWarn("failed to insert plan review", "error", err)
			}
		}

		// Build rich Discord embed.
		embed := buildPlanReviewEmbed(review)
		components := buildPlanReviewComponents(reviewID)

		// Override component custom IDs to route back to our approval channel.
		components = []discordComponent{
			discordActionRow(
				discordButton(customApprove, "Approve Plan", buttonStyleSuccess),
				discordButton(customReject, "Reject Plan", buttonStyleDanger),
			),
		}

		logInfo("sending plan review to Discord", "reviewId", reviewID, "worker", tmuxName)

		// Register callbacks.
		bot.interactions.register(&pendingInteraction{
			CustomID:  customApprove,
			CreatedAt: time.Now(),
			Callback: func(data discordInteractionData) {
				// Mark review as approved in DB.
				if p.cfg.HistoryDB != "" {
					updatePlanReviewStatus(p.cfg.HistoryDB, reviewID, "approved", "discord", "")
				}
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
				if p.cfg.HistoryDB != "" {
					updatePlanReviewStatus(p.cfg.HistoryDB, reviewID, "rejected", "discord", "")
				}
				select {
				case approvalCh <- false:
				default:
				}
			},
		})
		defer func() {
			bot.interactions.remove(customApprove)
			bot.interactions.remove(customReject)
			// Clear cached plan after review.
			if hookRecv := p.cfg.hookRecv; hookRecv != nil {
				hookRecv.ClearPlanCache(worker.TaskID)
			}
		}()

		bot.sendEmbedWithComponents(ch, embed, components)
	} else {
		// Standard approval message (non-plan).
		text := fmt.Sprintf("**Worker Approval Needed**\n\nWorker: `%s`\nTask: `%s`\n```\n%s\n```",
			tmuxName, worker.TaskID, renderTerminalScreen(approvalContext, 1500))

		components := []discordComponent{
			discordActionRow(
				discordButton(customApprove, "Approve", buttonStyleSuccess),
				discordButton(customReject, "Reject", buttonStyleDanger),
			),
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
	}

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

// requestQuestionChoice sends AskUserQuestion options to Discord and waits for selection.
// Returns the 0-based index of the selected option, or -1 on timeout/error.
func (p *TmuxProvider) requestQuestionChoice(ctx context.Context, tmuxName string, parsed *parsedQuestion, timeout time.Duration) int {
	if parsed == nil || len(parsed.Options) == 0 {
		logWarn("tmux question detected but could not parse options", "tmux", tmuxName)
		return -1
	}
	question := parsed.Question
	options := parsed.Options

	sup := p.getSupervisor()
	if sup == nil {
		logWarn("tmux question but no supervisor (ignoring)", "tmux", tmuxName)
		return -1
	}

	worker := sup.getWorker(tmuxName)
	if worker == nil {
		return -1
	}

	logInfo("tmux worker question detected", "tmux", tmuxName, "question", question, "options", len(options))

	bot := p.getDiscordBot()
	if bot == nil {
		logWarn("tmux question but no Discord bot (ignoring)", "tmux", tmuxName)
		return -1
	}

	choiceCh := make(chan int, 1)

	// Build buttons for each option.
	var buttons []discordComponent
	for i, opt := range options {
		label := opt
		if len(label) > 80 {
			label = label[:77] + "..."
		}
		customID := fmt.Sprintf("tmux_question:%s:%d", tmuxName, i)
		style := buttonStyleSecondary
		if i == 0 {
			style = buttonStylePrimary
		}
		idx := i
		bot.interactions.register(&pendingInteraction{
			CustomID:  customID,
			CreatedAt: time.Now(),
			Callback: func(data discordInteractionData) {
				select {
				case choiceCh <- idx:
				default:
				}
			},
		})
		defer bot.interactions.remove(customID)
		buttons = append(buttons, discordComponent{
			Type: componentTypeButton, Style: style, Label: label, CustomID: customID,
		})
	}

	// Discord limits 5 buttons per action row.
	var components []discordComponent
	for i := 0; i < len(buttons); i += 5 {
		end := i + 5
		if end > len(buttons) {
			end = len(buttons)
		}
		components = append(components, discordComponent{
			Type:       componentTypeActionRow,
			Components: buttons[i:end],
		})
	}

	agentLabel := ""
	if worker.Agent != "" {
		agentLabel = " (" + worker.Agent + ")"
	}
	text := fmt.Sprintf("**Question%s**\n\n%s\nWorker: `%s`", agentLabel, question, tmuxName)

	ch := bot.notifyChannelID()
	if ch == "" {
		logWarn("no notify channel for tmux question", "tmux", tmuxName)
		return -1
	}

	bot.sendMessageWithComponents(ch, text, components)

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case idx := <-choiceCh:
		return idx
	case <-timeoutCtx.Done():
		logWarn("tmux question timed out", "tmux", tmuxName)
		return -1
	}
}

// multiSelectResult holds the user's response to a multi-select question.
type multiSelectResult struct {
	Indices   []int  // selected option indices (from the original parsed list)
	TypedText string // custom text from "Type something" modal
}

// requestMultiSelectChoice sends a multi-select question to Discord as a string select menu
// and waits for the user to make selections. Returns nil on timeout/error.
func (p *TmuxProvider) requestMultiSelectChoice(ctx context.Context, tmuxName string, parsed *parsedQuestion, timeout time.Duration) *multiSelectResult {
	sup := p.getSupervisor()
	if sup == nil {
		logWarn("tmux multi-select but no supervisor (ignoring)", "tmux", tmuxName)
		return nil
	}
	worker := sup.getWorker(tmuxName)
	if worker == nil {
		return nil
	}

	logInfo("tmux worker multi-select question detected", "tmux", tmuxName, "question", parsed.Question, "options", len(parsed.Options))

	bot := p.getDiscordBot()
	if bot == nil {
		logWarn("tmux multi-select but no Discord bot (ignoring)", "tmux", tmuxName)
		return nil
	}

	resultCh := make(chan *multiSelectResult, 1)
	selectCustomID := fmt.Sprintf("tmux_multiselect:%s", tmuxName)
	typeButtonID := fmt.Sprintf("tmux_multiselect_type:%s", tmuxName)
	typeModalID := fmt.Sprintf("tmux_multiselect_modal:%s", tmuxName)

	// Build selectable options (exclude Submit and Type entries).
	var selectOptions []discordSelectOption
	for i, opt := range parsed.Options {
		if i == parsed.SubmitIndex || i == parsed.TypeIndex {
			continue
		}
		label := opt
		if len(label) > 100 {
			label = label[:97] + "..."
		}
		selectOptions = append(selectOptions, discordSelectOption{
			Label: label,
			Value: fmt.Sprintf("%d", i),
		})
	}

	if len(selectOptions) == 0 {
		logWarn("tmux multi-select: no selectable options after filtering", "tmux", tmuxName)
		return nil
	}
	if len(selectOptions) > 25 {
		selectOptions = selectOptions[:25]
	}

	// Register select menu callback.
	bot.interactions.register(&pendingInteraction{
		CustomID:  selectCustomID,
		CreatedAt: time.Now(),
		Callback: func(data discordInteractionData) {
			var indices []int
			for _, v := range data.Values {
				var idx int
				fmt.Sscanf(v, "%d", &idx)
				indices = append(indices, idx)
			}
			select {
			case resultCh <- &multiSelectResult{Indices: indices}:
			default:
			}
		},
	})
	defer bot.interactions.remove(selectCustomID)

	// Build components: multi-select menu + optional type button.
	maxVals := len(selectOptions)
	menu := discordMultiSelectMenu(selectCustomID, "Select options...", selectOptions, maxVals)
	var components []discordComponent
	components = append(components, discordActionRow(menu))

	if parsed.HasTypeOption {
		// Button click shows a modal for custom text input.
		modalResp := discordBuildModal(typeModalID, "Custom Answer",
			discordTextInput("custom_text", "Your answer", true))
		bot.interactions.register(&pendingInteraction{
			CustomID:      typeButtonID,
			CreatedAt:     time.Now(),
			ModalResponse: &modalResp,
			Callback:      func(data discordInteractionData) {},
		})
		defer bot.interactions.remove(typeButtonID)

		// Register modal submit callback.
		bot.interactions.register(&pendingInteraction{
			CustomID:  typeModalID,
			CreatedAt: time.Now(),
			Callback: func(data discordInteractionData) {
				values := extractModalValues(data.Components)
				text := values["custom_text"]
				if text != "" {
					select {
					case resultCh <- &multiSelectResult{TypedText: text}:
					default:
					}
				}
			},
		})
		defer bot.interactions.remove(typeModalID)

		components = append(components, discordActionRow(
			discordButton(typeButtonID, "Type custom answer", buttonStyleSecondary),
		))
	}

	// Build and send message.
	agentLabel := ""
	if worker.Agent != "" {
		agentLabel = " (" + worker.Agent + ")"
	}
	text := fmt.Sprintf("**Multi-Select Question%s**\n\n%s\nWorker: `%s`\n\n*Select one or more options from the menu*",
		agentLabel, parsed.Question, tmuxName)

	ch := bot.notifyChannelID()
	if ch == "" {
		logWarn("no notify channel for tmux multi-select", "tmux", tmuxName)
		return nil
	}

	bot.sendMessageWithComponents(ch, text, components)

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case result := <-resultCh:
		return result
	case <-timeoutCtx.Done():
		logWarn("tmux multi-select question timed out", "tmux", tmuxName)
		return nil
	}
}

// executeMultiSelect drives tmux navigation for a multi-select question response.
// Strategy: go to top of list, walk linearly toggling selected items, then submit.
func (p *TmuxProvider) executeMultiSelect(tmuxName string, result *multiSelectResult, parsed *parsedQuestion) {
	// Build set of indices to toggle.
	selectedSet := make(map[int]bool)
	for _, idx := range result.Indices {
		selectedSet[idx] = true
	}

	// If typed text was provided, select the Type option checkbox.
	if result.TypedText != "" && parsed.TypeIndex >= 0 {
		selectedSet[parsed.TypeIndex] = true
	}

	// Go to top of list.
	for i := 0; i < 20; i++ {
		tmuxSendKeys(tmuxName, "Up")
		time.Sleep(50 * time.Millisecond)
	}

	// Walk linearly from option 0 to submitIndex-1, toggling selected items.
	endIdx := len(parsed.Options) - 1
	if parsed.SubmitIndex >= 0 {
		endIdx = parsed.SubmitIndex - 1
	}

	for i := 0; i <= endIdx; i++ {
		if selectedSet[i] {
			tmuxSendKeys(tmuxName, "Space")
			time.Sleep(50 * time.Millisecond)
		}
		tmuxSendKeys(tmuxName, "Down")
		time.Sleep(50 * time.Millisecond)
	}

	// Now at Submit → Enter.
	tmuxSendKeys(tmuxName, "Enter")

	// If custom text was typed, wait for the text input to appear and send it.
	if result.TypedText != "" {
		time.Sleep(500 * time.Millisecond)
		tmuxSendText(tmuxName, result.TypedText)
		time.Sleep(100 * time.Millisecond)
		tmuxSendKeys(tmuxName, "Enter")
	}
}

// getDiscordBot retrieves the DiscordBot from the config if available.
func (p *TmuxProvider) getDiscordBot() *DiscordBot {
	if p.cfg == nil {
		return nil
	}
	return p.cfg.discordBot
}

// cleanCaptureForStreaming strips Claude Code UI chrome from a visible capture
// for streaming progress display. Simpler than extractScrollbackOutput — just
// removes status bars and prompt from the bottom of the current screen.
func cleanCaptureForStreaming(capture string) string {
	lines := strings.Split(capture, "\n")

	// Strip from bottom: empty lines, prompt, status bars, separators.
	for len(lines) > 0 {
		trimmed := strings.TrimSpace(lines[len(lines)-1])
		if trimmed == "" ||
			trimmed == "❯" || strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\t") ||
			strings.Contains(trimmed, "───") ||
			strings.Contains(trimmed, "⏵") ||
			strings.Contains(trimmed, "⏰") ||
			strings.Contains(trimmed, "🤖") ||
			strings.Contains(trimmed, "📝") ||
			strings.Contains(trimmed, "💻") ||
			strings.Contains(trimmed, "📁") ||
			strings.Contains(trimmed, "🆔") {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}

	// Take last 30 lines as a streaming preview (don't overwhelm Discord).
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}

	// Trim leading empty lines.
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
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

// extractScrollbackOutput parses tmux scrollback to extract only the LAST response.
// With keepSessions the scrollback contains all previous conversation turns — we must
// isolate the latest one by finding the last user prompt submission (❯ <text>).
func extractScrollbackOutput(history string) string {
	lines := strings.Split(history, "\n")

	// Trim trailing empty lines.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return "(empty output)"
	}

	// Strip Claude Code terminal UI from the bottom:
	// status bars (⏰, 💻, 📁, ⏵), prompt (❯), separator (────).
	// Keep 🤖 (model) and 📝 (token usage) lines.
	var keptStatusLines []string
	for len(lines) > 0 {
		trimmed := strings.TrimSpace(lines[len(lines)-1])
		if trimmed == "" ||
			trimmed == "❯" || strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\t") ||
			strings.Contains(trimmed, "───") ||
			strings.Contains(trimmed, "⏵") ||
			strings.Contains(trimmed, "⏰") ||
			strings.Contains(trimmed, "💻") ||
			strings.Contains(trimmed, "📁") ||
			strings.Contains(trimmed, "🆔") {
			lines = lines[:len(lines)-1]
			continue
		}
		if strings.Contains(trimmed, "🤖") || strings.Contains(trimmed, "📝") {
			keptStatusLines = append([]string{trimmed}, keptStatusLines...)
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}

	// Find the last user prompt submission line (❯ <text>) to isolate the latest response.
	// Scan from bottom up — the first ❯ line with actual text is the prompt for this turn.
	lastPromptIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if (strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\t")) && len(trimmed) > 3 {
			lastPromptIdx = i
			break
		}
	}
	if lastPromptIdx >= 0 && lastPromptIdx+1 < len(lines) {
		lines = lines[lastPromptIdx+1:]
	} else {
		// No prompt marker found — fall back to stripping welcome banner from top.
		startIdx := 0
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "╰") && strings.Contains(trimmed, "───") {
				startIdx = i + 1
				break
			}
			if i > 20 {
				break
			}
		}
		if startIdx > 0 && startIdx < len(lines) {
			lines = lines[startIdx:]
		}
	}

	// Trim leading/trailing empty lines after stripping.
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 && len(keptStatusLines) == 0 {
		return "(empty output)"
	}

	// Append kept status lines (🤖 model, 📝 tokens) at the end.
	if len(keptStatusLines) > 0 {
		lines = append(lines, keptStatusLines...)
	}

	// Cap at 500 lines.
	maxLines := 500
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	result := strings.TrimSpace(strings.Join(lines, "\n"))

	// Strip ANSI escape codes — they add invisible bytes and look like garbage on Discord.
	result = ansiEscapeRe.ReplaceAllString(result, "")

	return result
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

// isPlanModeCapture checks if a tmux capture looks like a plan mode approval.
// This is a heuristic fallback when hooks haven't detected the plan.
func isPlanModeCapture(capture string) bool {
	lower := strings.ToLower(capture)
	planKeywords := []string{"plan mode", "exitplanmode", "implementation plan", "approve this plan"}
	for _, kw := range planKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
