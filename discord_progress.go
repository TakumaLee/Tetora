package main

import (
	"fmt"
	"strings"
	"sync"
	"time"


)

// --- Discord Progress Updater ---

// discordProgressBuilder accumulates SSE events and renders a progress display for Discord.
type discordProgressBuilder struct {
	mu      sync.Mutex
	startAt time.Time
	tools   []string        // tool names in order
	text    strings.Builder // accumulated text content
	dirty   bool            // whether content changed since last render
}

func newDiscordProgressBuilder() *discordProgressBuilder {
	return &discordProgressBuilder{
		startAt: time.Now(),
	}
}

func (b *discordProgressBuilder) addToolCall(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools = append(b.tools, name)
	b.dirty = true
}

func (b *discordProgressBuilder) addText(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text = ansiEscapeRe.ReplaceAllString(text, "")
	if text == "" {
		return
	}
	b.text.WriteString(text)
	b.dirty = true
}

func (b *discordProgressBuilder) replaceText(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text = ansiEscapeRe.ReplaceAllString(text, "")
	b.text.Reset()
	b.text.WriteString(text)
	b.dirty = true
}

func (b *discordProgressBuilder) render() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dirty = false

	elapsed := time.Since(b.startAt).Round(time.Second)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Working... (%s)\n", elapsed))

	// Show last 5 tool calls.
	start := 0
	if len(b.tools) > 5 {
		start = len(b.tools) - 5
		sb.WriteString(fmt.Sprintf("... and %d earlier steps\n", start))
	}
	for _, t := range b.tools[start:] {
		sb.WriteString(fmt.Sprintf("> %s\n", t))
	}

	// Append accumulated text content (rolling window to fit Discord's 2000 char limit).
	accumulated := b.text.String()
	if accumulated != "" {
		sb.WriteString("\n")
		header := sb.String()
		maxText := 2000 - len(header) - 10 // leave margin
		if maxText < 100 {
			maxText = 100
		}
		if len(accumulated) > maxText {
			// Trim from front to nearest newline.
			trimmed := accumulated[len(accumulated)-maxText:]
			if idx := strings.Index(trimmed, "\n"); idx >= 0 && idx < len(trimmed)/2 {
				trimmed = trimmed[idx+1:]
			}
			sb.WriteString("..." + trimmed)
		} else {
			sb.WriteString(accumulated)
		}
	}

	return sb.String()
}

func (b *discordProgressBuilder) getText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

func (b *discordProgressBuilder) isDirty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dirty
}

// runDiscordProgressUpdater subscribes to task SSE events and updates a Discord progress message.
// It stops when stopCh is closed or the event channel closes.
func (db *DiscordBot) runDiscordProgressUpdater(
	channelID, progressMsgID, taskID, sessionID string,
	broker *sseBroker,
	stopCh <-chan struct{},
	builder *discordProgressBuilder,
	components []discordComponent,
) {
	eventCh, unsub := broker.Subscribe(taskID)
	defer unsub()

	logDebug("discord progress updater started", "taskID", taskID, "sessionID", sessionID)

	// Also subscribe to sessionID to receive output_chunk events, which are published
	// under the session key by the provider.
	var sessionEventCh chan SSEEvent
	if sessionID != "" && sessionID != taskID {
		ch, u := broker.Subscribe(sessionID)
		sessionEventCh = ch
		defer u()
		logDebug("discord progress updater subscribed to session", "sessionID", sessionID)
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastEdit time.Time

	tryEdit := func() {
		if builder.isDirty() && time.Since(lastEdit) >= 1500*time.Millisecond {
			content := builder.render()
			logDebug("discord progress edit", "contentLen", len(content), "taskID", taskID)
			if err := db.editMessageWithComponents(channelID, progressMsgID, content, components); err != nil {
				logWarn("discord progress edit failed", "error", err)
			}
			db.sendTyping(channelID)
			lastEdit = time.Now()
		}
	}

	handleEvent := func(ev SSEEvent) (done bool) {
		switch ev.Type {
		case SSEToolCall:
			if data, ok := ev.Data.(map[string]any); ok {
				if name, _ := data["name"].(string); name != "" {
					builder.addToolCall(name)
					tryEdit() // trigger immediate update on each tool call
				}
			}
		case SSEOutputChunk:
			if data, ok := ev.Data.(map[string]any); ok {
				if chunk, _ := data["chunk"].(string); chunk != "" {
					logDebug("discord progress got chunk", "len", len(chunk), "taskID", taskID)
					if replace, _ := data["replace"].(bool); replace {
						builder.replaceText(chunk)
					} else {
						builder.addText(chunk)
					}
					tryEdit()
				}
			}
		case SSECompleted, SSEError:
			logDebug("discord progress completed/error event", "type", ev.Type, "taskID", taskID)
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
