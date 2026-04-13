package main

import (
	"context"
	"fmt"
	"time"

	"tetora/internal/discord"
	"tetora/internal/log"
	"tetora/internal/trace"
)

func (db *DiscordBot) getChatLock(channelID string) string {
	db.chatLockMu.RLock()
	defer db.chatLockMu.RUnlock()
	return db.chatLock[channelID]
}

func (db *DiscordBot) cmdChat(msg discord.Message, agentName string) {
	if _, ok := db.cfg.Agents[agentName]; !ok {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found.", agentName))
		return
	}
	db.chatLockMu.Lock()
	if db.chatLock == nil {
		db.chatLock = make(map[string]string)
	}
	db.chatLock[msg.ChannelID] = agentName
	db.chatLockMu.Unlock()
	db.sendMessage(msg.ChannelID, fmt.Sprintf("Locked to **%s**. All messages route directly to this agent. Use `!end` to unlock.", agentName))
}

func (db *DiscordBot) cmdEnd(msg discord.Message) {
	db.chatLockMu.Lock()
	agent := db.chatLock[msg.ChannelID]
	delete(db.chatLock, msg.ChannelID)
	db.chatLockMu.Unlock()
	if agent == "" {
		db.sendMessage(msg.ChannelID, "No active chat lock.")
		return
	}
	db.sendMessage(msg.ChannelID, fmt.Sprintf("Unlocked from **%s**. Smart dispatch resumed.", agent))
}

// checkSessionReset inspects the existing channel session and archives it if
// context overflow or idle timeout is detected. Returns a non-empty reason
// string if a reset was performed (caller should nil out the session pointer).
func (db *DiscordBot) checkSessionReset(ctx context.Context, existing *Session, chKey string) string {
	if existing == nil {
		return ""
	}
	dbPath := db.cfg.HistoryDB

	maxTokens := db.cfg.Session.MaxContextTokensOrDefault()
	if existing.ContextSize > maxTokens {
		if err := archiveChannelSession(dbPath, chKey); err != nil {
			log.WarnCtx(ctx, "discord session archive error (context overflow)", "error", err)
		}
		return fmt.Sprintf("_Session reset: context reached %d tokens (limit %d). Starting fresh — previous context carried forward._", existing.ContextSize, maxTokens)
	}

	idleTimeout := db.cfg.Session.IdleTimeoutOrDefault()
	if existing.UpdatedAt != "" {
		if updatedAt, err := time.Parse(time.RFC3339, existing.UpdatedAt); err == nil {
			if idle := time.Since(updatedAt); idle > idleTimeout {
				if err := archiveChannelSession(dbPath, chKey); err != nil {
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
func (db *DiscordBot) autoNewSession(channelID, oldProvider, newProvider string) {
	if oldProvider == newProvider || oldProvider == "" || newProvider == "" {
		return
	}
	dbPath := db.cfg.HistoryDB
	if dbPath == "" {
		return
	}
	chKey := channelSessionKey("discord", channelID)
	_ = archiveChannelSession(dbPath, chKey) // best-effort
}

func (db *DiscordBot) cmdNewSession(msg discord.Message) {
	dbPath := db.cfg.HistoryDB
	if dbPath == "" {
		db.sendMessage(msg.ChannelID, "History DB not configured.")
		return
	}
	chKey := channelSessionKey("discord", msg.ChannelID)
	if err := archiveChannelSession(dbPath, chKey); err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}
	db.sendMessage(msg.ChannelID, "New session started.")
}

func (db *DiscordBot) cmdAsk(msg discord.Message, prompt string) {
	db.sendTyping(msg.ChannelID)

	ctx := trace.WithID(context.Background(), trace.NewID("discord"))

	task := Task{Prompt: prompt, Source: "ask:discord"}
	fillDefaults(db.cfg, &task)

	// Use default agent but no routing overhead.
	agentName := db.cfg.SmartDispatch.DefaultAgent
	if agentName != "" {
		if soulPrompt, err := loadAgentPrompt(db.cfg, agentName); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
		if rc, ok := db.cfg.Agents[agentName]; ok {
			if rc.Model != "" {
				task.Model = rc.Model
			}
		}
	}

	result := runSingleTask(ctx, db.cfg, task, db.sem, db.childSem, agentName)

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
	db.sendEmbed(msg.ChannelID, discord.Embed{
		Description: output,
		Color:       color,
		Fields: []discord.EmbedField{
			{Name: "Cost", Value: fmt.Sprintf("$%.4f", result.CostUSD), Inline: true},
			{Name: "Duration", Value: formatDurationMs(result.DurationMs), Inline: true},
		},
		Footer:    &discord.EmbedFooter{Text: fmt.Sprintf("ask | %s", task.ID[:8])},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}
