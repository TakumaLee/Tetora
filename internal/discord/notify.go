package discord

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	dtypes "tetora/internal/dispatch"
	"tetora/internal/log"
	"tetora/internal/text"
)

// truncateStr delegates to text.TruncateStr.
var truncateStr = text.TruncateStr

// TaskNotifier posts thread-per-task notifications to a fixed Discord channel.
type TaskNotifier struct {
	client    *Client
	channelID string

	mu      sync.Mutex
	threads map[string]string // taskID → Discord thread channel ID
}

// NewTaskNotifier creates a notifier for the given channel.
func NewTaskNotifier(client *Client, channelID string) *TaskNotifier {
	return &TaskNotifier{
		client:    client,
		channelID: channelID,
		threads:   make(map[string]string),
	}
}

// NotifyStart posts a start message and creates a thread.
func (n *TaskNotifier) NotifyStart(task dtypes.Task) {
	role := task.Agent
	if role == "" {
		role = "default"
	}
	name := task.Name
	if name == "" {
		name = "Task " + task.ID[:8]
	}
	promptSnippet := truncateStr(task.Prompt, 120)

	parentMsg := fmt.Sprintf("⏳ **%s** | agent: `%s` | id: `%s`\n> %s",
		name, role, task.ID[:8], promptSnippet)

	msgID, err := n.client.SendMessageReturningID(n.channelID, parentMsg)
	if err != nil {
		log.Warn("discord notify: send start message failed", "taskId", task.ID[:8], "error", err)
		return
	}

	threadName := name
	if len([]rune(threadName)) > 97 {
		threadName = string([]rune(threadName)[:97]) + "..."
	}

	threadID, err := n.createThread(msgID, threadName)
	if err != nil {
		log.Warn("discord notify: create thread failed", "taskId", task.ID[:8], "error", err)
		return
	}

	n.mu.Lock()
	n.threads[task.ID] = threadID
	n.mu.Unlock()
}

// NotifyComplete posts a result embed to the task's thread.
func (n *TaskNotifier) NotifyComplete(taskID string, result dtypes.TaskResult) {
	n.mu.Lock()
	threadID, ok := n.threads[taskID]
	if ok {
		delete(n.threads, taskID)
	}
	n.mu.Unlock()

	if !ok {
		return
	}

	var statusEmoji string
	var color int
	switch result.Status {
	case "success":
		statusEmoji = "✅"
		color = 0x57F287
	default:
		statusEmoji = "❌"
		color = 0xED4245
	}

	elapsed := time.Duration(result.DurationMs) * time.Millisecond
	desc := fmt.Sprintf("%s **%s** | duration: `%s` | cost: `$%.5f`",
		statusEmoji, result.Status, elapsed.Round(time.Second), result.CostUSD)

	if result.Error != "" {
		desc += "\n**Error:** " + truncateStr(result.Error, 300)
	}

	if result.Output != "" {
		out := result.Output
		if len(out) > 400 {
			out = "…" + out[len(out)-399:]
		}
		preview := "```\n" + out + "\n```"
		if len(desc)+len(preview) <= 4000 {
			desc += "\n" + preview
		}
	}

	embed := Embed{
		Color:       color,
		Description: desc,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Footer: &EmbedFooter{
			Text: fmt.Sprintf("tokens: %d in / %d out | model: %s", result.TokensIn, result.TokensOut, result.Model),
		},
	}
	n.client.SendEmbed(threadID, embed)
}

func (n *TaskNotifier) createThread(messageID, name string) (string, error) {
	body, err := n.client.Request(
		"POST",
		fmt.Sprintf("/channels/%s/messages/%s/threads", n.channelID, messageID),
		map[string]any{
			"name":                  name,
			"auto_archive_duration": 60,
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
