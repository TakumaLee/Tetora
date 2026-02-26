package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// discordTaskNotifier posts thread-per-task notifications to a fixed Discord channel.
// It is safe for concurrent use: each task gets its own thread, all state is
// protected by a mutex, and entries are always cleaned up after NotifyComplete.
type discordTaskNotifier struct {
	bot       *DiscordBot
	channelID string

	mu      sync.Mutex
	threads map[string]string // taskID → Discord thread channel ID
}

func newDiscordTaskNotifier(bot *DiscordBot, channelID string) *discordTaskNotifier {
	return &discordTaskNotifier{
		bot:       bot,
		channelID: channelID,
		threads:   make(map[string]string),
	}
}

// NotifyStart posts a start message to the notification channel and creates a thread
// from it. The thread ID is stored for later use by NotifyComplete.
// Errors are logged but never surfaced — notification is best-effort.
func (n *discordTaskNotifier) NotifyStart(task Task) {
	// Build the parent message shown in the main channel.
	role := task.Agent
	if role == "" {
		role = "default"
	}
	name := task.Name
	if name == "" {
		name = "Task " + task.ID[:8]
	}
	promptSnippet := truncate(task.Prompt, 120)

	parentMsg := fmt.Sprintf("⏳ **%s** | agent: `%s` | id: `%s`\n> %s",
		name, role, task.ID[:8], promptSnippet)

	msgID, err := n.bot.sendMessageReturningID(n.channelID, parentMsg)
	if err != nil {
		logWarn("discord notify: send start message failed", "taskId", task.ID[:8], "error", err)
		return
	}

	// Thread name = task name (max 100 chars, Discord limit).
	threadName := name
	if len([]rune(threadName)) > 97 {
		threadName = string([]rune(threadName)[:97]) + "..."
	}

	threadID, err := n.createThread(msgID, threadName)
	if err != nil {
		logWarn("discord notify: create thread failed", "taskId", task.ID[:8], "error", err)
		return
	}

	n.mu.Lock()
	n.threads[task.ID] = threadID
	n.mu.Unlock()
}

// NotifyComplete posts a result embed to the task's thread and removes the entry
// from the internal map. Safe to call even if NotifyStart failed (no-op).
func (n *discordTaskNotifier) NotifyComplete(taskID string, result TaskResult) {
	n.mu.Lock()
	threadID, ok := n.threads[taskID]
	if ok {
		delete(n.threads, taskID)
	}
	n.mu.Unlock()

	if !ok {
		return // NotifyStart failed or was not called; nothing to do.
	}

	var statusEmoji string
	var color int
	switch result.Status {
	case "success":
		statusEmoji = "✅"
		color = 0x57F287 // green
	default:
		statusEmoji = "❌"
		color = 0xED4245 // red
	}

	elapsed := time.Duration(result.DurationMs) * time.Millisecond
	desc := fmt.Sprintf("%s **%s** | duration: `%s` | cost: `$%.5f`",
		statusEmoji, result.Status, elapsed.Round(time.Second), result.CostUSD)

	if result.Error != "" {
		desc += "\n**Error:** " + truncate(result.Error, 300)
	}

	// Show tail of output for quick preview (Discord embed limit is 4096 chars).
	if result.Output != "" {
		out := result.Output
		if len(out) > 400 {
			out = "…" + out[len(out)-399:]
		}
		preview := "```\n" + out + "\n```"
		// Ensure total description stays within Discord's 4096-char embed limit.
		if len(desc)+len(preview) <= 4000 {
			desc += "\n" + preview
		}
	}

	embed := discordEmbed{
		Color:       color,
		Description: desc,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Footer: &discordEmbedFooter{
			Text: fmt.Sprintf("tokens: %d in / %d out | model: %s", result.TokensIn, result.TokensOut, result.Model),
		},
	}
	n.bot.sendEmbed(threadID, embed)
}

// createThread creates a public thread from an existing message in the notification
// channel and returns the new thread's channel ID.
func (n *discordTaskNotifier) createThread(messageID, name string) (string, error) {
	body, err := n.bot.discordRequestWithResponse(
		"POST",
		fmt.Sprintf("/channels/%s/messages/%s/threads", n.channelID, messageID),
		map[string]any{
			"name":                  name,
			"auto_archive_duration": 60, // archive after 60 min of inactivity
		},
	)
	if err != nil {
		return "", err
	}

	var ch struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ch); err != nil {
		return "", fmt.Errorf("parse thread response: %w", err)
	}
	if ch.ID == "" {
		return "", fmt.Errorf("discord returned empty thread ID")
	}
	return ch.ID, nil
}
