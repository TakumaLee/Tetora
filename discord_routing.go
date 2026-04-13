package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tetora/internal/audit"
	"tetora/internal/discord"
	"tetora/internal/log"
	"tetora/internal/trace"
	"tetora/internal/webhook"
)

// --- Direct Route (no SmartDispatch) ---

// handleDirectRoute dispatches a message directly to a known agent without smart routing.
func (db *DiscordBot) handleDirectRoute(msg discord.Message, prompt string, agent string) {
	route := RouteResult{Agent: agent, Method: "explicit", Confidence: "high"}
	db.executeRoute(msg, prompt, route)
}

// --- Smart Dispatch ---

func (db *DiscordBot) handleRoute(msg discord.Message, prompt string) {
	ctx := trace.WithID(context.Background(), trace.NewID("discord"))
	route := routeTask(ctx, db.cfg, RouteRequest{
		Prompt:    prompt,
		Source:    "discord",
		ChannelID: msg.ChannelID,
		GuildID:   msg.GuildID,
		UserID:    msg.Author.ID,
	})
	log.InfoCtx(ctx, "discord route result", "prompt", truncate(prompt, 60), "agent", route.Agent, "method", route.Method)
	db.executeRoute(msg, prompt, *route)
}

// archiveStaleSession detects a "No saved session found" error and, when found,
// archives the session in the DB so the next request gets a fresh start.
// Returns true if a stale session was detected (caller should send reset message).
func archiveStaleSession(ctx context.Context, dbPath string, sess *Session, resultErr string) bool {
	if !strings.Contains(resultErr, errNoSavedSession) {
		return false
	}
	log.WarnCtx(ctx, "Auto-cleared stale session ID", "error", resultErr)
	if sess != nil {
		if err := updateSessionStatus(dbPath, sess.ID, "archived"); err != nil {
			log.WarnCtx(ctx, "Failed to archive stale session", "sessionID", sess.ID, "error", err)
		}
	}
	return true
}

// executeRoute runs a routed task through the full Discord execution pipeline
// (session, SSE events, progress messages, reply).
func (db *DiscordBot) executeRoute(msg discord.Message, prompt string, route RouteResult) {
	db.sendTyping(msg.ChannelID)

	// P14.3: Add queued reaction.
	if db.reactions != nil {
		db.reactions.ReactQueued(msg.ChannelID, msg.ID)
	}

	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()
	ctx := trace.WithID(baseCtx, trace.NewID("discord"))
	dbPath := db.cfg.HistoryDB

	// Generate a task ID early for Discord activity tracking.
	activityID := newUUID()

	// Register Discord activity for dashboard visibility.
	if db.state != nil {
		db.state.setDiscordActivity(activityID, &discordActivity{
			TaskID:    activityID,
			Agent:     route.Agent,
			Phase:     "routing",
			Author:    msg.Author.Username,
			ChannelID: msg.ChannelID,
			StartAt:   time.Now(),
			Prompt:    truncate(prompt, 200),
		})
		defer db.state.removeDiscordActivity(activityID)
	}

	// Channel session.
	// Look up existing session once; reuse it directly when the agent matches
	// to avoid a redundant DB read inside getOrCreateChannelSession.
	chKey := channelSessionKey("discord", msg.ChannelID)
	agent := route.Agent

	existing, findErr := findChannelSession(dbPath, chKey)
	if findErr != nil {
		log.WarnCtx(ctx, "discord findChannelSession error", "error", findErr)
	}

	// Auto-reset: archive session if context overflow or idle timeout.
	if resetReason := db.checkSessionReset(ctx, existing, chKey); resetReason != "" {
		db.sendMessage(msg.ChannelID, resetReason)
		existing = nil
	}

	// For non-deterministic routes (keyword/LLM), keep the existing session's
	// agent to avoid constant session churn.
	if route.Method != "binding" && route.Method != "explicit" && existing != nil {
		agent = existing.Agent
	}

	var sess *Session
	if existing != nil && existing.Agent == agent {
		sess = existing
	} else {
		var err error
		sess, err = getOrCreateChannelSession(dbPath, "discord", chKey, agent, "")
		if err != nil {
			log.ErrorCtx(ctx, "discord session error", "error", err)
		}
	}

	// Update Discord activity with resolved agent (after session-stickiness override).
	if db.state != nil {
		db.state.mu.Lock()
		if da, ok := db.state.discordActivities[activityID]; ok {
			da.Agent = agent
		}
		db.state.mu.Unlock()
	}

	// Context-aware prompt.
	// Skip text injection when:
	//  - Provider has native session support (e.g. claude-code), OR
	//  - Session has messages → CLI will --continue with native conversation history.
	// Both cases already have full context; injecting again would double it.
	contextPrompt := prompt
	canResume := sess != nil && sess.MessageCount > 0
	if sess != nil {
		providerName := resolveProviderName(db.cfg, Task{Agent: route.Agent}, route.Agent)
		if !providerHasNativeSession(providerName) && !canResume {
			sessionCtx := buildSessionContext(dbPath, sess.ID, db.cfg.Session.ContextMessagesOrDefault())
			// New session with no history — carry forward context from the archived predecessor.
			if sessionCtx == "" {
				if prev, err := findLastArchivedChannelSession(dbPath, chKey); err == nil && prev != nil {
					sessionCtx = buildSessionContext(dbPath, prev.ID, db.cfg.Session.ContextMessagesOrDefault())
					log.InfoCtx(ctx, "auto-continuing from archived session",
						"prevSession", prev.ID[:8], "channel", chKey)
				}
			}
			contextPrompt = wrapWithContext(sessionCtx, prompt)
		}
		now := time.Now().Format(time.RFC3339)
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID, Role: "user", Content: truncateStr(prompt, 5000), CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)
		title := prompt
		if len(title) > 100 {
			title = title[:100]
		}
		updateSessionTitle(dbPath, sess.ID, title)
	}

	// Publish task_received + task_routing (after session resolved so watchers see them).
	if db.state != nil && db.state.broker != nil {
		sessID := ""
		if sess != nil {
			sessID = sess.ID
		}
		publishToSSEBroker(db.state.broker, SSEEvent{
			Type: SSETaskReceived, TaskID: activityID, SessionID: sessID,
			Data: map[string]any{
				"source":  "discord",
				"author":  msg.Author.Username,
				"prompt":  prompt,
				"channel": msg.ChannelID,
			},
		})
		publishToSSEBroker(db.state.broker, SSEEvent{
			Type: SSETaskRouting, TaskID: activityID, SessionID: sessID,
			Data: map[string]any{
				"source":     "discord",
				"role":       route.Agent,
				"method":     route.Method,
				"confidence": route.Confidence,
			},
		})
	}

	// Build and run task. Pre-set ID so it matches the activityID used for dashboard tracking.
	task := Task{ID: activityID, Prompt: contextPrompt, Agent: route.Agent, Source: "route:discord"}
	fillDefaults(db.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
		task.PersistSession = true // channel sessions persist for --continue on next message
		task.Resume = canResume    // resume if session already has conversation history
	}
	if task.Agent != "" {
		if soulPrompt, err := loadAgentPrompt(db.cfg, task.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
	}
	// Discord tasks run unattended — default to bypassPermissions if not set by agent.
	if task.PermissionMode == "" {
		task.PermissionMode = "bypassPermissions"
	}

	task.Prompt = expandPrompt(task.Prompt, "", db.cfg.HistoryDB, route.Agent, db.cfg.KnowledgeDir, db.cfg)

	// P28.0: Attach approval gate.
	if db.approvalGate != nil {
		task.ApprovalGate = db.approvalGate
	}

	// P14.3: Transition to thinking phase before task execution.
	if db.reactions != nil {
		db.reactions.ReactThinking(msg.ChannelID, msg.ID)
	}

	// Update Discord activity: routing → processing.
	if db.state != nil {
		db.state.updateDiscordPhase(activityID, "processing")
		if db.state.broker != nil {
			publishToSSEBroker(db.state.broker, SSEEvent{
				Type: SSEDiscordProcessing, TaskID: activityID, SessionID: task.SessionID,
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
	if db.state != nil && db.state.broker != nil {
		task.SSEBroker = db.state.broker
	}

	// Create cancellable context so Escape button and !cancel can interrupt.
	taskCtx, taskCancel := context.WithCancel(ctx)
	defer taskCancel()

	// Register task in dispatch state so !cancel can find it.
	if db.state != nil {
		db.state.mu.Lock()
		db.state.running[task.ID] = &taskState{
			task:         task,
			startAt:      time.Now(),
			lastActivity: time.Now(),
			cancelFn:     taskCancel,
		}
		db.state.mu.Unlock()
		defer func() {
			db.state.mu.Lock()
			delete(db.state.running, task.ID)
			db.state.mu.Unlock()
		}()
	}

	// Start progress message for live Discord updates.
	// Controlled by showProgress config (default: true).
	showProgress := db.cfg.Discord.ShowProgress == nil || *db.cfg.Discord.ShowProgress
	var progressMsgID string
	var progressStopCh chan struct{}
	var progressBuilder *discord.ProgressBuilder
	var progressEscapeID string // interaction custom ID for escape button cleanup
	if showProgress && db.state != nil && db.state.broker != nil {
		// Build escape button for the progress message.
		escapeID := fmt.Sprintf("progress_escape:%s", task.ID)
		escapeComponents := []discord.Component{
			discordActionRow(
				discordButton(escapeID, "Escape", discord.ButtonStyleDanger),
			),
		}

		msgID, err := db.sendMessageWithComponentsReturningID(msg.ChannelID, "Working...", escapeComponents)
		if err == nil && msgID != "" {
			progressMsgID = msgID
			progressEscapeID = escapeID
			progressStopCh = make(chan struct{})
			progressBuilder = discord.NewProgressBuilder()

			// Register escape button interaction.
			db.interactions.register(&pendingInteraction{
				CustomID:  escapeID,
				CreatedAt: time.Now(),
				Response: &discord.InteractionResponse{
					Type: discord.InteractionResponseUpdateMessage,
					Data: &discord.InteractionResponseData{
						Content: "Interrupted.",
					},
				},
				Callback: func(data discord.InteractionData) {
					log.Info("progress escape: cancelling task", "taskId", task.ID)
					// Cancel the base context directly — works for both
					// Discord chat mode (no state.running entry) and
					// dispatch mode.
					baseCancel()
				},
			})

			go db.runDiscordProgressUpdater(msg.ChannelID, progressMsgID, task.ID, task.SessionID, db.state.broker, progressStopCh, progressBuilder, escapeComponents)
		}
	}

	taskStart := time.Now()
	result := runSingleTask(taskCtx, db.cfg, task, db.sem, db.childSem, route.Agent)

	// Stop progress updater and clean up progress message.
	if progressStopCh != nil {
		close(progressStopCh)
	}
	// Clean up escape button interaction.
	if progressEscapeID != "" {
		db.interactions.remove(progressEscapeID)
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
			db.editMessageWithComponents(msg.ChannelID, progressMsgID, fmt.Sprintf("Error (%s): %s", elapsed, errMsg), nil)
		} else {
			// On success: if output fits in one message, edit progress in-place (no flicker).
			// Otherwise delete and re-send as chunks.
			output := result.Output
			if strings.TrimSpace(output) == "" {
				// Session mode: result.Output is empty but progressBuilder may have accumulated content
				if progressBuilder != nil {
					if text := progressBuilder.GetText(); strings.TrimSpace(text) != "" {
						output = strings.TrimSpace(text)
					}
				}
				if strings.TrimSpace(output) == "" {
					output = "Task completed successfully."
				}
			}
			if len(output) <= 1900 {
				db.editMessageWithComponents(msg.ChannelID, progressMsgID, output, nil)
				progressMsgID = "" // signal sendRouteResponse to skip output (already shown)
			} else {
				db.deleteMessage(msg.ChannelID, progressMsgID)
			}
		}
	}

	// Track whether output was already sent via progress message edit.
	outputAlreadySent := progressMsgID == "" && progressBuilder != nil && result.Status == "success"

	// Update Discord activity: processing → replying.
	if db.state != nil {
		db.state.updateDiscordPhase(activityID, "replying")
		if db.state.broker != nil {
			publishToSSEBroker(db.state.broker, SSEEvent{
				Type: SSEDiscordReplying, TaskID: activityID, SessionID: task.SessionID,
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
	if db.reactions != nil {
		if result.Status == "success" {
			db.reactions.ReactDone(msg.ChannelID, msg.ID)
		} else {
			db.reactions.ReactError(msg.ChannelID, msg.ID)
		}
	}

	recordHistory(db.cfg.HistoryDB, task.ID, task.Name, task.Source, route.Agent, task, result,
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
		addSessionMessage(dbPath, SessionMessage{
			SessionID: sess.ID, Role: msgRole, Content: content,
			CostUSD: result.CostUSD, TokensIn: result.TokensIn, TokensOut: result.TokensOut,
			Model: result.Model, TaskID: task.ID, CreatedAt: now,
		})
		updateSessionStats(dbPath, sess.ID, result.CostUSD, result.TokensIn, result.TokensOut, 1)

		// Publish session_message so watchers get the full output without polling.
		if db.state != nil && db.state.broker != nil {
			publishToSSEBroker(db.state.broker, SSEEvent{
				Type: SSESessionMessage, TaskID: task.ID, SessionID: sess.ID,
				Data: map[string]any{"role": msgRole, "content": content},
			})
		}

		maybeCompactSession(db.cfg, dbPath, sess.ID, sess.MessageCount+2, sess.TotalTokensIn+result.TokensIn, db.sem, db.childSem)
	}

	if result.Status == "success" {
		setMemory(db.cfg, route.Agent, "last_route_output", truncate(result.Output, 500))
		setMemory(db.cfg, route.Agent, "last_route_prompt", truncate(prompt, 200))
		setMemory(db.cfg, route.Agent, "last_route_time", time.Now().Format(time.RFC3339))
	}

	audit.Log(dbPath, "route.dispatch", "discord",
		fmt.Sprintf("agent=%s method=%s session=%s", route.Agent, route.Method, task.SessionID), "")

	sendWebhooks(db.cfg, result.Status, webhook.Payload{
		JobID: task.ID, Name: task.Name, Source: task.Source,
		Status: result.Status, Cost: result.CostUSD, Duration: result.DurationMs,
		Model: result.Model, Output: truncate(result.Output, 500), Error: truncate(result.Error, 300),
	})

	// Send slot pressure warning before response if present.
	if result.SlotWarning != "" {
		db.sendMessage(msg.ChannelID, result.SlotWarning)
	}

	// Auto-recover from stale session errors (provider switch or machine migration).
	if result.Status != "success" && archiveStaleSession(ctx, dbPath, sess, result.Error) {
		db.sendMessage(msg.ChannelID, "♻️ **System Reset**: Detected environment change (Provider/Migration). Starting new session...")
		return
	}

	// Send response embed.
	db.sendRouteResponse(msg.ChannelID, &route, result, task, outputAlreadySent, msg.ID)
}
