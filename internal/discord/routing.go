package discord

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tetora/internal/audit"
	dtypes "tetora/internal/dispatch"
	"tetora/internal/log"
	"tetora/internal/provider"
	"tetora/internal/roles"
	"tetora/internal/session"
	"tetora/internal/trace"
	"tetora/internal/webhook"
)

// errNoSavedSession is the error string emitted when a provider cannot find the
// requested session. On seeing this we archive the stale session and retry fresh.
const errNoSavedSession = "No saved session found"

// publishToSSEBroker publishes an SSE event to the broker if non-nil.
func publishToSSEBroker(broker dtypes.SSEBrokerPublisher, event dtypes.SSEEvent) {
	if broker == nil {
		return
	}
	keys := []string{dtypes.SSEDashboardKey}
	if event.TaskID != "" {
		keys = append(keys, event.TaskID)
	}
	if event.SessionID != "" {
		keys = append(keys, event.SessionID)
	}
	for _, key := range keys {
		broker.Publish(key, event)
	}
}

// --- Direct Route (no SmartDispatch) ---

// handleDirectRoute dispatches a message directly to a known agent without smart routing.
func (b *Bot) handleDirectRoute(msg Message, prompt string, agent string) {
	route := dtypes.RouteResult{Agent: agent, Method: "explicit", Confidence: "high"}
	b.executeRoute(msg, prompt, route)
}

// --- Smart Dispatch ---

func (b *Bot) handleRoute(msg Message, prompt string) {
	ctx := trace.WithID(context.Background(), trace.NewID("discord"))
	route := b.deps.RouteTask(ctx, dtypes.RouteRequest{
		Prompt:    prompt,
		Source:    "discord",
		ChannelID: msg.ChannelID,
		GuildID:   msg.GuildID,
		UserID:    msg.Author.ID,
	})
	log.InfoCtx(ctx, "discord route result", "prompt", truncate(prompt, 60), "agent", route.Agent, "method", route.Method)
	b.executeRoute(msg, prompt, *route)
}

// archiveStaleSession detects a "No saved session found" error and, when found,
// archives the session in the DB so the next request gets a fresh start.
// Returns true if a stale session was detected (caller should send reset message).
func archiveStaleSession(ctx context.Context, dbPath string, sess *session.Session, resultErr string) bool {
	if !strings.Contains(resultErr, errNoSavedSession) {
		return false
	}
	log.WarnCtx(ctx, "Auto-cleared stale session ID", "error", resultErr)
	if sess != nil {
		if err := session.UpdateSessionStatus(dbPath, sess.ID, "archived"); err != nil {
			log.WarnCtx(ctx, "Failed to archive stale session", "sessionID", sess.ID, "error", err)
		}
	}
	return true
}

// executeRoute runs a routed task through the full Discord execution pipeline
// (session, SSE events, progress messages, reply).
func (b *Bot) executeRoute(msg Message, prompt string, route dtypes.RouteResult) {
	b.sendTyping(msg.ChannelID)

	// P14.3: Add queued reaction.
	if b.reactions != nil {
		b.reactions.ReactQueued(msg.ChannelID, msg.ID)
	}

	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()
	ctx := trace.WithID(baseCtx, trace.NewID("discord"))
	dbPath := b.cfg.HistoryDB

	// Generate a task ID early for Discord activity tracking.
	activityID := session.NewUUID()

	// Register Discord activity for dashboard visibility.
	if b.state != nil {
		b.state.SetDiscordActivity(activityID, route.Agent, "routing", msg.Author.Username, msg.ChannelID, truncate(prompt, 200), time.Now())
		defer b.state.RemoveDiscordActivity(activityID)
	}

	// Channel session.
	// Look up existing session once; reuse it directly when the agent matches
	// to avoid a redundant DB read inside getOrCreateChannelSession.
	chKey := session.ChannelSessionKey("discord", msg.ChannelID)
	agent := route.Agent

	existing, findErr := session.FindChannelSession(dbPath, chKey)
	if findErr != nil {
		log.WarnCtx(ctx, "discord findChannelSession error", "error", findErr)
	}

	// Auto-reset: archive session if context overflow or idle timeout.
	if resetReason := b.checkSessionReset(ctx, existing, chKey); resetReason != "" {
		b.sendMessage(msg.ChannelID, resetReason)
		existing = nil
	}

	// For non-deterministic routes (keyword/LLM), keep the existing session's
	// agent to avoid constant session churn.
	if route.Method != "binding" && route.Method != "explicit" && existing != nil {
		agent = existing.Agent
	}

	var sess *session.Session
	if existing != nil && existing.Agent == agent {
		sess = existing
	} else {
		var err error
		sess, err = session.GetOrCreateChannelSession(dbPath, "discord", chKey, agent, "")
		if err != nil {
			log.ErrorCtx(ctx, "discord session error", "error", err)
		}
	}

	// Update Discord activity with resolved agent (after session-stickiness override).
	if b.state != nil {
		b.state.SetDiscordActivity(activityID, agent, "routing", msg.Author.Username, msg.ChannelID, truncate(prompt, 200), time.Now())
	}

	// Context-aware prompt.
	// Skip text injection when:
	//  - Provider has native session support (e.g. claude-code), OR
	//  - session.Session has messages → CLI will --continue with native conversation history.
	// Both cases already have full context; injecting again would double it.
	contextPrompt := prompt
	canResume := sess != nil && sess.MessageCount > 0
	if sess != nil {
		providerName := b.deps.ResolveProviderName(dtypes.Task{Agent: route.Agent}, route.Agent)
		if !provider.HasNativeSession(providerName) && !canResume {
			sessionCtx := session.BuildSessionContext(dbPath, sess.ID, b.cfg.Session.ContextMessagesOrDefault())
			// New session with no history — carry forward context from the archived predecessor.
			if sessionCtx == "" {
				if prev, err := session.FindLastArchivedChannelSession(dbPath, chKey); err == nil && prev != nil {
					sessionCtx = session.BuildSessionContext(dbPath, prev.ID, b.cfg.Session.ContextMessagesOrDefault())
					log.InfoCtx(ctx, "auto-continuing from archived session",
						"prevSession", prev.ID[:8], "channel", chKey)
				}
			}
			contextPrompt = session.WrapWithContext(sessionCtx, prompt)
		}
		now := time.Now().Format(time.RFC3339)
		session.AddSessionMessage(dbPath, session.SessionMessage{
			SessionID: sess.ID, Role: "user", Content: truncateStr(prompt, 5000), CreatedAt: now,
		})
		session.UpdateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)
		title := prompt
		if len(title) > 100 {
			title = title[:100]
		}
		session.UpdateSessionTitle(dbPath, sess.ID, title)
	}

	// Publish task_received + task_routing (after session resolved so watchers see them).
	if b.state != nil && b.state.Broker() != nil {
		sessID := ""
		if sess != nil {
			sessID = sess.ID
		}
		publishToSSEBroker(b.state.Broker(), dtypes.SSEEvent{
			Type: dtypes.SSETaskReceived, TaskID: activityID, SessionID: sessID,
			Data: map[string]any{
				"source":  "discord",
				"author":  msg.Author.Username,
				"prompt":  prompt,
				"channel": msg.ChannelID,
			},
		})
		publishToSSEBroker(b.state.Broker(), dtypes.SSEEvent{
			Type: dtypes.SSETaskRouting, TaskID: activityID, SessionID: sessID,
			Data: map[string]any{
				"source":     "discord",
				"role":       route.Agent,
				"method":     route.Method,
				"confidence": route.Confidence,
			},
		})
	}

	// Build and run task. Pre-set ID so it matches the activityID used for dashboard tracking.
	task := dtypes.Task{ID: activityID, Prompt: contextPrompt, Agent: route.Agent, Source: "route:discord"}
	dtypes.FillDefaults(b.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
		task.PersistSession = true // channel sessions persist for --continue on next message
		task.Resume = canResume    // resume if session already has conversation history
	}
	if task.Agent != "" {
		if soulPrompt, err := roles.LoadAgentPrompt(b.cfg, task.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
	}
	// Discord tasks run unattended — default to bypassPermissions if not set by agent.
	if task.PermissionMode == "" {
		task.PermissionMode = "bypassPermissions"
	}

	task.Prompt = b.deps.ExpandPrompt(task.Prompt, "", b.cfg.HistoryDB, route.Agent, b.cfg.KnowledgeDir)

	// P28.0: Attach approval gate.
	if b.approvalGate != nil {
		task.ApprovalGate = b.approvalGate
	}

	// P14.3: Transition to thinking phase before task execution.
	if b.reactions != nil {
		b.reactions.ReactThinking(msg.ChannelID, msg.ID)
	}

	// Update Discord activity: routing → processing.
	if b.state != nil {
		b.state.UpdateDiscordPhase(activityID, "processing")
		if b.state.Broker() != nil {
			publishToSSEBroker(b.state.Broker(), dtypes.SSEEvent{
				Type: dtypes.SSEDiscordProcessing, TaskID: activityID, SessionID: task.SessionID,
				Data: map[string]any{
					"taskId":  activityID,
					"role":    route.Agent,
					"author":  msg.Author.Username,
					"channel": msg.ChannelID,
				},
			})
		}
	}

	// Wire up SSE streaming so dashboard can show live output.
	if b.state != nil && b.state.Broker() != nil {
		task.SSEBroker = b.state.Broker()
	}

	// Create cancellable context so Escape button and !cancel can interrupt.
	taskCtx, taskCancel := context.WithCancel(ctx)
	defer taskCancel()

	// Register task in dispatch state so !cancel can find it.
	if b.state != nil {
		done := b.state.TrackTask(task, taskCancel)
		defer done()
	}

	// Start progress message for live Discord updates.
	// Controlled by showProgress config (default: true).
	showProgress := b.cfg.Discord.ShowProgress == nil || *b.cfg.Discord.ShowProgress
	var progressMsgID string
	var progressStopCh chan struct{}
	var progressBuilder *ProgressBuilder
	var progressEscapeID string // interaction custom ID for escape button cleanup
	if showProgress && b.state != nil && b.state.Broker() != nil {
		// Build escape button for the progress message.
		escapeID := fmt.Sprintf("progress_escape:%s", task.ID)
		escapeComponents := []Component{
			discordActionRow(
				discordButton(escapeID, "Escape", ButtonStyleDanger),
			),
		}

		msgID, err := b.sendMessageWithComponentsReturningID(msg.ChannelID, "Working...", escapeComponents)
		if err == nil && msgID != "" {
			progressMsgID = msgID
			progressEscapeID = escapeID
			progressStopCh = make(chan struct{})
			progressBuilder = NewProgressBuilder()

			// Register escape button interaction.
			b.interactions.register(&pendingInteraction{
				CustomID:  escapeID,
				CreatedAt: time.Now(),
				Response: &InteractionResponse{
					Type: InteractionResponseUpdateMessage,
					Data: &InteractionResponseData{
						Content: "Interrupted.",
					},
				},
				Callback: func(data InteractionData) {
					log.Info("progress escape: cancelling task", "taskId", task.ID)
					// Cancel the base context directly — works for both
					// Discord chat mode (no state.running entry) and
					// dispatch mode.
					baseCancel()
				},
			})

			go b.runDiscordProgressUpdater(msg.ChannelID, progressMsgID, task.ID, task.SessionID, b.state.Broker(), progressStopCh, progressBuilder, escapeComponents)
		}
	}

	taskStart := time.Now()
	result := b.deps.RunSingleTask(taskCtx, task, route.Agent)

	// Stop progress updater and clean up progress message.
	if progressStopCh != nil {
		close(progressStopCh)
	}
	// Clean up escape button interaction.
	if progressEscapeID != "" {
		b.interactions.remove(progressEscapeID)
	}
	if progressMsgID != "" {
		if result.Status != "success" {
			// On error, edit progress to show error instead of deleting.
			// Clear components (remove escape button).
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			elapsed := time.Since(taskStart).Round(time.Second)
			b.editMessageWithComponents(msg.ChannelID, progressMsgID, fmt.Sprintf("Error (%s): %s", elapsed, errMsg), nil)
		} else {
			// On success: if output fits in one message, edit progress in-place (no flicker).
			// Otherwise delete and re-send as chunks.
			output := result.Output
			if strings.TrimSpace(output) == "" {
				// session.Session mode: result.Output is empty but progressBuilder may have accumulated content
				if progressBuilder != nil {
					if text := progressBuilder.GetText(); strings.TrimSpace(text) != "" {
						output = strings.TrimSpace(text)
					}
				}
				if strings.TrimSpace(output) == "" {
					output = "dtypes.Task completed successfully."
				}
			}
			if len(output) <= 1900 {
				b.editMessageWithComponents(msg.ChannelID, progressMsgID, output, nil)
				progressMsgID = "" // signal sendRouteResponse to skip output (already shown)
			} else {
				b.deleteMessage(msg.ChannelID, progressMsgID)
			}
		}
	}

	// Track whether output was already sent via progress message edit.
	outputAlreadySent := progressMsgID == "" && progressBuilder != nil && result.Status == "success"

	// Update Discord activity: processing → replying.
	if b.state != nil {
		b.state.UpdateDiscordPhase(activityID, "replying")
		if b.state.Broker() != nil {
			publishToSSEBroker(b.state.Broker(), dtypes.SSEEvent{
				Type: dtypes.SSEDiscordReplying, TaskID: activityID, SessionID: task.SessionID,
				Data: map[string]any{
					"taskId":  activityID,
					"role":    route.Agent,
					"author":  msg.Author.Username,
					"status":  result.Status,
				},
			})
		}
	}

	// P14.3: Set done/error reaction based on result.
	if b.reactions != nil {
		if result.Status == "success" {
			b.reactions.ReactDone(msg.ChannelID, msg.ID)
		} else {
			b.reactions.ReactError(msg.ChannelID, msg.ID)
		}
	}

	b.deps.RecordHistory(b.cfg.HistoryDB, task.ID, task.Name, task.Source, route.Agent, task, result,
		taskStart.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)

	// Record to session.
	if sess != nil {
		now := time.Now().Format(time.RFC3339)
		msgRole := "assistant"
		content := truncateStr(result.Output, 5000)
		if result.Status != "success" {
			msgRole = "system"
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			content = fmt.Sprintf("[%s] %s", result.Status, truncateStr(errMsg, 2000))
		}
		session.AddSessionMessage(dbPath, session.SessionMessage{
			SessionID: sess.ID, Role: msgRole, Content: content,
			CostUSD: result.CostUSD, TokensIn: result.TokensIn, TokensOut: result.TokensOut,
			Model: result.Model, TaskID: task.ID, CreatedAt: now,
		})
		session.UpdateSessionStats(dbPath, sess.ID, result.CostUSD, result.TokensIn, result.TokensOut, 1)

		// Publish session_message so watchers get the full output without polling.
		if b.state != nil && b.state.Broker() != nil {
			publishToSSEBroker(b.state.Broker(), dtypes.SSEEvent{
				Type: dtypes.SSESessionMessage, TaskID: task.ID, SessionID: sess.ID,
				Data: map[string]any{"role": msgRole, "content": content},
			})
		}

		b.deps.MaybeCompactSession(dbPath, sess.ID, sess.MessageCount+2, sess.TotalTokensIn+result.TokensIn, b.sem, b.childSem)
	}

	if result.Status == "success" {
		b.deps.SetMemory(route.Agent, "last_route_output", truncate(result.Output, 500))
		b.deps.SetMemory(route.Agent, "last_route_prompt", truncate(prompt, 200))
		b.deps.SetMemory(route.Agent, "last_route_time", time.Now().Format(time.RFC3339))
	}

	audit.Log(dbPath, "route.dispatch", "discord",
		fmt.Sprintf("agent=%s method=%s session=%s", route.Agent, route.Method, task.SessionID), "")

	if len(b.cfg.Webhooks) > 0 {
		whs := make([]webhook.Config, len(b.cfg.Webhooks))
		for i, w := range b.cfg.Webhooks {
			whs[i] = webhook.Config{URL: w.URL, Events: w.Events, Headers: w.Headers}
		}
		webhook.Send(whs, result.Status, webhook.Payload{
			JobID: task.ID, Name: task.Name, Source: task.Source,
			Status: result.Status, Cost: result.CostUSD, Duration: result.DurationMs,
			Model: result.Model, Output: truncate(result.Output, 500), Error: truncate(result.Error, 300),
		})
	}

	// Send slot pressure warning before response if present.
	if result.SlotWarning != "" {
		b.sendMessage(msg.ChannelID, result.SlotWarning)
	}

	// Auto-recover from stale session errors (provider switch or machine migration).
	if result.Status != "success" && archiveStaleSession(ctx, dbPath, sess, result.Error) {
		b.sendMessage(msg.ChannelID, "♻️ **System Reset**: Detected environment change (Provider/Migration). Starting new session...")
		return
	}

	// Send response embed.
	b.sendRouteResponse(msg.ChannelID, &route, result, task, outputAlreadySent, msg.ID)
}
