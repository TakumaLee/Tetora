package discord

import (
	dtypes "tetora/internal/dispatch"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"tetora/internal/audit"
	"tetora/internal/provider"
	"tetora/internal/roles"
	"tetora/internal/session"
	"tetora/internal/log"
	"tetora/internal/trace"
)

// --- Discord Channel Types ---

const (
	discordChannelTypePublicThread  = 11
	discordChannelTypePrivateThread = 12
	discordChannelTypeForum         = 15
)

// discordMessageWithType extends Message with channel type info
// used for thread detection during MESSAGE_CREATE dispatch.
type discordMessageWithType struct {
	Message
	ChannelType int `json:"channel_type,omitempty"`
}

// isThreadChannel returns true if the channel type represents a thread or forum.
func isThreadChannel(channelType int) bool {
	return channelType == discordChannelTypePublicThread ||
		channelType == discordChannelTypePrivateThread ||
		channelType == discordChannelTypeForum
}

// --- /focus and /unfocus Command Handlers ---

// availableRoleNames returns sorted agent names from config.
func (b *Bot) availableRoleNames() []string {
	if b == nil || b.cfg == nil || b.cfg.Agents == nil {
		return nil
	}
	names := make([]string, 0, len(b.cfg.Agents))
	for r := range b.cfg.Agents {
		names = append(names, r)
	}
	sort.Strings(names)
	return names
}

// handleFocusCommand processes the /focus <agent> command to bind a thread to an agent.
func (b *Bot) handleFocusCommand(msg Message, args string, channelType int) bool {
	if !isThreadChannel(channelType) {
		b.sendMessage(msg.ChannelID, "The `/focus` command can only be used inside a thread.")
		return true
	}

	role := strings.TrimSpace(strings.ToLower(args))
	if role == "" {
		available := b.availableRoleNames()
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Usage: `/focus <agent>` — Available agents: %s", strings.Join(available, ", ")))
		return true
	}

	// Validate agent exists in config.
	_, roleExists := b.cfg.Agents[role]
	if b.cfg.Agents == nil || !roleExists {
		available := b.availableRoleNames()
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Unknown agent `%s`. Available: %s", role, strings.Join(available, ", ")))
		return true
	}

	guildID := msg.GuildID
	threadID := msg.ChannelID // in a thread, channel_id IS the thread ID
	ttl := b.cfg.Discord.ThreadBindings.ThreadBindingsTTL()

	sessionID := b.threads.bind(guildID, threadID, role, ttl)
	log.Info("discord thread bound", "guild", guildID, "thread", threadID, "agent", role, "session", sessionID)

	b.sendEmbed(msg.ChannelID, Embed{
		Title:       fmt.Sprintf("Thread focused on %s", role),
		Description: fmt.Sprintf("This thread is now bound to agent **%s**.\nSession: `%s`\nExpires in %d hours.", role, sessionID, int(ttl.Hours())),
		Color:       0x57F287,
		Timestamp:   time.Now().Format(time.RFC3339),
	})
	return true
}

// handleUnfocusCommand processes the /unfocus command to unbind a thread.
func (b *Bot) handleUnfocusCommand(msg Message, channelType int) bool {
	if !isThreadChannel(channelType) {
		b.sendMessage(msg.ChannelID, "The `/unfocus` command can only be used inside a thread.")
		return true
	}

	guildID := msg.GuildID
	threadID := msg.ChannelID

	existing := b.threads.get(guildID, threadID)
	if existing == nil {
		b.sendMessage(msg.ChannelID, "This thread is not currently focused on any agent.")
		return true
	}

	b.threads.unbind(guildID, threadID)
	log.Info("discord thread unbound", "guild", guildID, "thread", threadID, "wasRole", existing.Agent)

	b.sendEmbed(msg.ChannelID, Embed{
		Title:       "Thread unfocused",
		Description: fmt.Sprintf("Agent **%s** has been unbound from this thread.", existing.Agent),
		Color:       0xFEE75C,
		Timestamp:   time.Now().Format(time.RFC3339),
	})
	return true
}

// --- Thread-Aware Message Routing ---

// handleThreadMessage checks if a message is in a bound thread and routes accordingly.
// Returns true if the message was handled (bound thread routing), false for normal routing.
func (b *Bot) handleThreadMessage(msg Message, channelType int) bool {
	if b.threads == nil || !b.cfg.Discord.ThreadBindings.Enabled {
		return false
	}

	// channelType may be 0 when Discord omits it from the payload.
	// If it's explicitly a non-thread type (1-10), skip. If 0 or thread type, check binding.
	if channelType > 0 && !isThreadChannel(channelType) {
		return false
	}

	// For channelType == 0 (unknown), check if we have a binding as a fallback signal.
	// This handles cases where Discord doesn't include channel_type in MESSAGE_CREATE.
	binding := b.threads.get(msg.GuildID, msg.ChannelID)
	isThread := isThreadChannel(channelType)

	// Check for /focus and /unfocus commands (only in confirmed threads).
	text := StripMention(msg.Content, b.botUserID)
	text = strings.TrimSpace(text)

	if isThread {
		if strings.HasPrefix(text, "/focus") {
			args := strings.TrimPrefix(text, "/focus")
			return b.handleFocusCommand(msg, args, channelType)
		}
		if text == "/unfocus" {
			return b.handleUnfocusCommand(msg, channelType)
		}
	}

	if binding == nil {
		// Auto-bind unbound threads to the default agent (parent route → system default).
		// This ensures threads created from bot messages inherit session context without
		// requiring an explicit /focus command.
		if !isThread {
			return false // channelType unknown and no binding, let normal routing handle
		}
		agent := b.resolveThreadDefaultAgent(msg.ChannelID, msg.GuildID)
		if agent == "" {
			return false // no default agent configured, fall through
		}
		ttl := b.cfg.Discord.ThreadBindings.ThreadBindingsTTL()
		sessionID := b.threads.bind(msg.GuildID, msg.ChannelID, agent, ttl)
		log.Info("discord thread auto-bound", "thread", msg.ChannelID, "agent", agent, "session", sessionID)
		binding = b.threads.get(msg.GuildID, msg.ChannelID)
		if binding == nil {
			return false
		}
	}

	// Thread is bound — route to the bound agent.
	b.handleThreadRoute(msg, text, binding)
	return true
}

// resolveThreadDefaultAgent returns the agent to use for auto-binding an unbound thread.
// Priority: parent channel route → system-wide default agent.
func (b *Bot) resolveThreadDefaultAgent(threadID, guildID string) string {
	if guildID != "" {
		if parentID := b.resolveThreadParent(threadID); parentID != "" {
			if route, ok := b.cfg.Discord.Routes[parentID]; ok && route.Agent != "" {
				return route.Agent
			}
		}
	}
	return b.cfg.DefaultAgent
}

// handleThreadRoute dispatches a message in a bound thread to the bound agent.
func (b *Bot) handleThreadRoute(msg Message, prompt string, binding *threadBinding) {
	if prompt == "" {
		return
	}

	b.sendTyping(msg.ChannelID)

	ctx := trace.WithID(context.Background(), trace.NewID("discord-thread"))
	dbPath := b.cfg.HistoryDB
	role := binding.Agent
	sessionID := binding.SessionID

	log.InfoCtx(ctx, "discord thread dispatch",
		"thread", msg.ChannelID, "agent", role, "session", sessionID, "prompt", truncate(prompt, 60))

	// Get or create session using the thread binding's session ID as channel key.
	sess, err := session.GetOrCreateChannelSession(dbPath, "discord", sessionID, role, "")
	if err != nil {
		log.ErrorCtx(ctx, "discord thread session error", "error", err)
	}

	// Context-aware prompt.
	// Skip text injection for providers with native session support (e.g. claude-code).
	contextPrompt := prompt
	if sess != nil {
		providerName := b.deps.ResolveProviderName(dtypes.Task{Agent: role}, role)
		if !provider.HasNativeSession(providerName) {
			sessionCtx := session.BuildSessionContext(dbPath, sess.ID, b.cfg.Session.ContextMessagesOrDefault())
			// New session with no history — carry forward context from the archived predecessor.
			if sessionCtx == "" && sess.MessageCount == 0 {
				if prev, err := session.FindLastArchivedChannelSession(dbPath, sessionID); err == nil && prev != nil {
					sessionCtx = session.BuildSessionContext(dbPath, prev.ID, b.cfg.Session.ContextMessagesOrDefault())
					log.InfoCtx(ctx, "auto-continuing from archived session",
						"prevSession", prev.ID[:8], "channel", sessionID)
				}
			}
			contextPrompt = session.WrapWithContext(sessionCtx, prompt)
		}
		now := time.Now().Format(time.RFC3339)
		session.AddSessionMessage(dbPath, session.SessionMessage{
			SessionID: sess.ID, Role: "user", Content: truncateStr(prompt, 5000), CreatedAt: now,
		})
		session.UpdateSessionStats(dbPath, sess.ID, 0, 0, 0, 1)
		title := fmt.Sprintf("[thread:%s] %s", role, prompt)
		if len(title) > 100 {
			title = title[:100]
		}
		session.UpdateSessionTitle(dbPath, sess.ID, title)
	}

	// Build and run task.
	task := dtypes.Task{Prompt: contextPrompt, Agent: role, Source: "route:discord:thread"}
	dtypes.FillDefaults(b.cfg, &task)
	if sess != nil {
		task.SessionID = sess.ID
	}
	if role != "" {
		if soulPrompt, err := roles.LoadAgentPrompt(b.cfg, role); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := b.cfg.Agents[role]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
			if rc.PermissionMode != "" {
				task.PermissionMode = rc.PermissionMode
			}
		}
	}
	task.Prompt = b.deps.ExpandPrompt(task.Prompt, "", b.cfg.HistoryDB, role, b.cfg.KnowledgeDir)

	taskStart := time.Now()
	result := b.deps.RunSingleTask(ctx, task, role)

	b.deps.RecordHistory(b.cfg.HistoryDB, task.ID, task.Name, task.Source, role, task, result,
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
		b.deps.MaybeCompactSession(dbPath, sess.ID, sess.MessageCount+2, sess.TotalTokensIn+result.TokensIn, b.sem, b.childSem)
	}

	if result.Status == "success" {
		b.deps.SetMemory(role, "last_thread_output", truncate(result.Output, 500))
		b.deps.SetMemory(role, "last_thread_prompt", truncate(prompt, 200))
		b.deps.SetMemory(role, "last_thread_time", time.Now().Format(time.RFC3339))
	}

	audit.Log(dbPath, "thread.dispatch", "discord",
		fmt.Sprintf("agent=%s thread=%s session=%s", role, msg.ChannelID, task.SessionID), "")

	// Send response embed.
	route := &dtypes.RouteResult{Agent: role, Method: "thread-binding"}
	b.sendRouteResponse(msg.ChannelID, route, result, task, false, msg.ID)
}

// runDiscordProgressUpdater subscribes to task SSE events and updates a Discord progress message.
func (b *Bot) runDiscordProgressUpdater(
	channelID, progressMsgID, taskID, sessionID string,
	broker *dtypes.Broker,
	stopCh <-chan struct{},
	builder *ProgressBuilder,
	components []Component,
) {
	eventCh, unsub := broker.Subscribe(taskID)
	defer unsub()

	log.Debug("discord progress updater started", "taskID", taskID, "sessionID", sessionID)

	var sessionEventCh chan dtypes.SSEEvent
	if sessionID != "" && sessionID != taskID {
		ch, u := broker.Subscribe(sessionID)
		sessionEventCh = ch
		defer u()
		log.Debug("discord progress updater subscribed to session", "sessionID", sessionID)
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastEdit time.Time

	tryEdit := func() {
		if builder.IsDirty() && time.Since(lastEdit) >= 1500*time.Millisecond {
			content := builder.Render()
			log.Debug("discord progress edit", "contentLen", len(content), "taskID", taskID)
			if err := b.editMessageWithComponents(channelID, progressMsgID, content, components); err != nil {
				log.Warn("discord progress edit failed", "error", err)
			}
			b.sendTyping(channelID)
			lastEdit = time.Now()
		}
	}

	handleEvent := func(ev dtypes.SSEEvent) (done bool) {
		switch ev.Type {
		case dtypes.SSEToolCall:
			if data, ok := ev.Data.(map[string]any); ok {
				if name, _ := data["name"].(string); name != "" {
					builder.AddToolCall(name)
					tryEdit()
				}
			}
		case dtypes.SSEOutputChunk:
			if data, ok := ev.Data.(map[string]any); ok {
				if chunk, _ := data["chunk"].(string); chunk != "" {
					log.Debug("discord progress got chunk", "len", len(chunk), "taskID", taskID)
					if replace, _ := data["replace"].(bool); replace {
						builder.ReplaceText(chunk)
					} else {
						builder.AddText(chunk)
					}
					tryEdit()
				}
			}
		case dtypes.SSECompleted, dtypes.SSEError:
			log.Debug("discord progress completed/error event", "type", ev.Type, "taskID", taskID)
			return true
		}
		return false
	}

	for {
		select {
		case <-stopCh:
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			if handleEvent(ev) {
				return
			}
		case ev, ok := <-sessionEventCh:
			if !ok {
				sessionEventCh = nil
			} else {
				handleEvent(ev)
			}
		case <-ticker.C:
			tryEdit()
		}
	}
}
