package discord

import (
	"context"
	"time"

	dtypes "tetora/internal/dispatch"
)

// DiscordActivity tracks a Discord-initiated task for dashboard visibility.
// Mirrors the main-package discordActivity struct.
type DiscordActivity struct {
	TaskID    string    `json:"taskId"`
	Agent     string    `json:"agent"`
	Phase     string    `json:"phase"`
	Author    string    `json:"author"`
	ChannelID string    `json:"channelId"`
	StartAt   time.Time `json:"startedAt"`
	Prompt    string    `json:"prompt"`
}

// StateAccessor abstracts *dispatchState for the discord package.
// Implemented by dispatchStateAdapter in the root package.
type StateAccessor interface {
	// TrackTask registers a task as running and returns a cleanup func.
	// The cleanup func removes it from the running map.
	TrackTask(task dtypes.Task, cancelFn context.CancelFunc) func()

	// RunningCount returns the current number of running tasks (thread-safe).
	RunningCount() int

	// CancelAll cancels all running tasks via the top-level context cancel.
	CancelAll()

	// Discord activity lifecycle — used for dashboard visibility.
	SetDiscordActivity(taskID, agent, phase, author, channelID, prompt string, startAt time.Time)
	UpdateDiscordPhase(taskID, phase string)
	RemoveDiscordActivity(taskID string)
	DiscordActivity(id string) (*DiscordActivity, bool)

	// Broker returns the SSE broker for streaming progress events.
	// Returns *dtypes.Broker (concrete type) so callers can Subscribe, not just Publish.
	// May be nil if SSE is not configured.
	Broker() *dtypes.Broker
}
