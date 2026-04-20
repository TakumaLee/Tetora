package discord

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	dtypes "tetora/internal/dispatch"
	"tetora/internal/log"
	"tetora/internal/text"
)

// truncateStr delegates to text.TruncateStr.
var truncateStr = text.TruncateStr

// Notification levels for task events. Empty string means "inherit" (or default
// to LevelThread at the root for backwards compatibility).
const (
	LevelOff     = "off"     // silent — no message posted
	LevelChannel = "channel" // post to main/failure channel, no thread creation
	LevelThread  = "thread"  // legacy behavior — post + create thread for start; complete posts in thread if it exists
)

// NotifyOptions configures TaskNotifier behavior. All fields are optional.
// When every field is zero, behavior is identical to pre-config legacy defaults
// (thread for every event, no failure redirect, no mentions).
type NotifyOptions struct {
	TaskStart        string
	TaskCompleteOk   string
	TaskCompleteFail string
	FailureChannelID string
	MentionUserID    string
	MentionOnFail    bool
	Overrides        []NotifyOverride
}

// NotifyOverride applies custom levels to tasks matching Match. Empty level
// fields inherit from the top-level NotifyOptions.
type NotifyOverride struct {
	Match            NotifyMatch
	TaskStart        string
	TaskCompleteOk   string
	TaskCompleteFail string
}

// NotifyMatch matches a task. Empty fields act as wildcards; multiple fields
// combine with AND. NameContains is a case-sensitive substring check. An
// all-empty match never matches (guard against accidental global overrides).
type NotifyMatch struct {
	Agent        string
	NameContains string
	JobID        string
}

// TaskNotifier posts thread-per-task notifications to a fixed Discord channel,
// honoring per-event levels and optional overrides.
type TaskNotifier struct {
	client    *Client
	channelID string
	opts      NotifyOptions

	mu      sync.Mutex
	threads map[string]string // taskID → Discord thread channel ID
}

// NewTaskNotifier creates a notifier for the given channel with the given
// options. A zero-value NotifyOptions preserves legacy behavior.
func NewTaskNotifier(client *Client, channelID string, opts NotifyOptions) *TaskNotifier {
	return &TaskNotifier{
		client:    client,
		channelID: channelID,
		opts:      opts,
		threads:   make(map[string]string),
	}
}

// NotifyStart posts a start message and (if level=thread) creates a thread.
func (n *TaskNotifier) NotifyStart(task dtypes.Task) {
	level := n.resolveLevel(task, "start")
	if level == LevelOff {
		return
	}

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

	if level == LevelChannel {
		return
	}

	// level == LevelThread — create thread and remember it for completion.
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

// NotifyComplete posts a result embed. Behavior:
//   - level=off: silent.
//   - failure + FailureChannelID set: post to failure channel (not thread).
//   - level=thread + thread exists + not redirected: post inside the thread.
//   - otherwise: post flat to the target channel.
//
// Failure messages honor MentionOnFail + MentionUserID by prepending <@id>.
func (n *TaskNotifier) NotifyComplete(task dtypes.Task, result dtypes.TaskResult) {
	// Reclaim any stored thread ID, even if we end up not posting there, so the
	// map doesn't leak when start created a thread but complete is silent.
	n.mu.Lock()
	threadID, hadThread := n.threads[task.ID]
	if hadThread {
		delete(n.threads, task.ID)
	}
	n.mu.Unlock()

	isFail := result.Status != "success"
	event := "ok"
	if isFail {
		event = "fail"
	}
	level := n.resolveLevel(task, event)
	if level == LevelOff {
		return
	}

	var statusEmoji string
	var color int
	if isFail {
		statusEmoji = "❌"
		color = 0xED4245
	} else {
		statusEmoji = "✅"
		color = 0x57F287
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

	redirectedToFailure := isFail && n.opts.FailureChannelID != ""
	target := n.channelID
	if redirectedToFailure {
		target = n.opts.FailureChannelID
	} else if level == LevelThread && hadThread {
		target = threadID
	}

	var content string
	if isFail && n.opts.MentionOnFail && n.opts.MentionUserID != "" {
		content = fmt.Sprintf("<@%s>", n.opts.MentionUserID)
	}

	n.client.SendEmbedWithContent(target, content, embed)
}

// resolveLevel picks the applicable level for a task event ("start" | "ok" | "fail").
// Precedence: first matching override (if its field is non-empty) > top-level
// options > default LevelThread.
func (n *TaskNotifier) resolveLevel(task dtypes.Task, event string) string {
	var ovStart, ovOk, ovFail string
	for _, ov := range n.opts.Overrides {
		if matchTask(ov.Match, task) {
			ovStart = ov.TaskStart
			ovOk = ov.TaskCompleteOk
			ovFail = ov.TaskCompleteFail
			break
		}
	}
	switch event {
	case "start":
		return firstNonEmpty(ovStart, n.opts.TaskStart, LevelThread)
	case "ok":
		return firstNonEmpty(ovOk, n.opts.TaskCompleteOk, LevelThread)
	case "fail":
		return firstNonEmpty(ovFail, n.opts.TaskCompleteFail, LevelThread)
	}
	return LevelThread
}

func matchTask(m NotifyMatch, task dtypes.Task) bool {
	if m.Agent == "" && m.JobID == "" && m.NameContains == "" {
		return false
	}
	if m.Agent != "" && m.Agent != task.Agent {
		return false
	}
	if m.JobID != "" && m.JobID != task.ID {
		return false
	}
	if m.NameContains != "" && !strings.Contains(task.Name, m.NameContains) {
		return false
	}
	return true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
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
