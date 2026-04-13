package discord

import (
	"tetora/internal/roles"
	dtypes "tetora/internal/dispatch"
	"tetora/internal/session"
	"context"
	"fmt"
	"time"

	"tetora/internal/log"
	"tetora/internal/trace"
)

func (b *Bot) getChatLock(channelID string) string {
	b.chatLockMu.RLock()
	defer b.chatLockMu.RUnlock()
	return b.chatLock[channelID]
}

func (b *Bot) cmdChat(msg Message, agentName string) {
	if _, ok := b.cfg.Agents[agentName]; !ok {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found.", agentName))
		return
	}
	b.chatLockMu.Lock()
	if b.chatLock == nil {
		b.chatLock = make(map[string]string)
	}
	b.chatLock[msg.ChannelID] = agentName
	b.chatLockMu.Unlock()
	b.sendMessage(msg.ChannelID, fmt.Sprintf("Locked to **%s**. All messages route directly to this agent. Use `!end` to unlock.", agentName))
}

func (b *Bot) cmdEnd(msg Message) {
	b.chatLockMu.Lock()
	agent := b.chatLock[msg.ChannelID]
	delete(b.chatLock, msg.ChannelID)
	b.chatLockMu.Unlock()
	if agent == "" {
		b.sendMessage(msg.ChannelID, "No active chat lock.")
		return
	}
	b.sendMessage(msg.ChannelID, fmt.Sprintf("Unlocked from **%s**. Smart dispatch resumed.", agent))
}

// checkSessionReset inspects the existing channel session and archives it if
// context overflow or idle timeout is detected. Returns a non-empty reason
// string if a reset was performed (caller should nil out the session pointer).
func (b *Bot) checkSessionReset(ctx context.Context, existing *session.Session, chKey string) string {
	if existing == nil {
		return ""
	}
	dbPath := b.cfg.HistoryDB

	maxTokens := b.cfg.Session.MaxContextTokensOrDefault()
	if existing.ContextSize > maxTokens {
		if err := session.ArchiveChannelSession(dbPath, chKey); err != nil {
			log.WarnCtx(ctx, "discord session archive error (context overflow)", "error", err)
		}
		return fmt.Sprintf("_Session reset: context reached %d tokens (limit %d). Starting fresh — previous context carried forward._", existing.ContextSize, maxTokens)
	}

	idleTimeout := b.cfg.Session.IdleTimeoutOrDefault()
	if existing.UpdatedAt != "" {
		if updatedAt, err := time.Parse(time.RFC3339, existing.UpdatedAt); err == nil {
			if idle := time.Since(updatedAt); idle > idleTimeout {
				if err := session.ArchiveChannelSession(dbPath, chKey); err != nil {
					log.WarnCtx(ctx, "discord session archive error (idle timeout)", "error", err)
				}
				return fmt.Sprintf("_Session reset: idle for %d min (limit %d min). Starting fresh._", int(idle.Minutes()), int(idleTimeout.Minutes()))
			}
		}
	}

	return ""
}

// autoNewSession archives the current channel session when provider changes,
// since session/thread IDs from one provider are invalid in another.
func (b *Bot) autoNewSession(channelID, oldProvider, newProvider string) {
	if oldProvider == newProvider || oldProvider == "" || newProvider == "" {
		return
	}
	dbPath := b.cfg.HistoryDB
	if dbPath == "" {
		return
	}
	chKey := session.ChannelSessionKey("discord", channelID)
	_ = session.ArchiveChannelSession(dbPath, chKey) // best-effort
}

func (b *Bot) cmdNewSession(msg Message) {
	dbPath := b.cfg.HistoryDB
	if dbPath == "" {
		b.sendMessage(msg.ChannelID, "History DB not configured.")
		return
	}
	chKey := session.ChannelSessionKey("discord", msg.ChannelID)
	if err := session.ArchiveChannelSession(dbPath, chKey); err != nil {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}
	b.sendMessage(msg.ChannelID, "New session started.")
}

func (b *Bot) cmdAsk(msg Message, prompt string) {
	b.sendTyping(msg.ChannelID)

	ctx := trace.WithID(context.Background(), trace.NewID("discord"))

	task := dtypes.Task{Prompt: prompt, Source: "ask:discord"}
	dtypes.FillDefaults(b.cfg, &task)

	// Use default agent but no routing overhead.
	agentName := b.cfg.SmartDispatch.DefaultAgent
	if agentName != "" {
		if soulPrompt, err := roles.LoadAgentPrompt(b.cfg, agentName); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := b.cfg.Agents[agentName]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
		}
	}

	result := b.deps.RunSingleTask(ctx, task, agentName)

	output := result.Output
	if result.Status != "success" {
		output = result.Error
		if output == "" {
			output = result.Status
		}
	}
	if len(output) > 3800 {
		output = output[:3797] + "..."
	}

	color := 0x57F287
	if result.Status != "success" {
		color = 0xED4245
	}
	b.sendEmbed(msg.ChannelID, Embed{
		Description: output,
		Color:       color,
		Fields: []EmbedField{
			{Name: "Cost", Value: fmt.Sprintf("$%.4f", result.CostUSD), Inline: true},
			{Name: "Duration", Value: formatDurationMs(result.DurationMs), Inline: true},
		},
		Footer:    &EmbedFooter{Text: fmt.Sprintf("ask | %s", task.ID[:8])},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}
